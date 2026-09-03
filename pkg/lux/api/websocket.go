package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	stderrors "errors"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"nhooyr.io/websocket"
)

// wsConn wraps a WebSocket connection with write serialization.
// WebSocket spec: concurrent writes are not safe, so we serialize with a mutex.
// Reads are single-goroutine (the read loop), no lock needed.
type wsConn struct {
	conn           *websocket.Conn
	acceptLanguage string
	mu             sync.Mutex // serialize writes
}

func (c *wsConn) writeText(ctx context.Context, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Write(ctx, websocket.MessageText, data)
}

func (c *wsConn) writeBinary(ctx context.Context, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Write(ctx, websocket.MessageBinary, data)
}

// handleWebSocket upgrades the HTTP connection to WebSocket and runs the message loop.
// Reuses the same Router handlers as HTTP — zero code duplication.
//
// Wire format:
//
//	JSON (text frame):
//	  Request:  {"$id": 1, "$api": "getUser", "id": 42}
//	  Response: {"$id": 1, "data": {...}} or {"$id": 1, "error": "...", "code": 404, ...}
//
//	Binary (binary frame):
//	  Call:      [0x01][seq varint][API ID varint][FieldMask len][mask][Params...][0x00]
//	  Success:   [0x02][seq varint][payload]
//	  Error:     [0x03][seq varint][Binary Error Envelope]
//	  Subscribe: [0x04][API ID varint][FieldMask len][mask][Params...][0x00]
//	  Unsub:     [0x05][API ID varint]
//	  Stream:    [0x06][API ID varint][payload]
//	  Sub OK:    [0x07][API ID varint]
//	  Sub Error: [0x08][API ID varint][Binary Error Envelope]
func (rt *Router) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	opts := &websocket.AcceptOptions{}
	if rt.WSAllowAllOrigins {
		opts.InsecureSkipVerify = true
	} else if len(rt.WSOrigins) > 0 {
		opts.OriginPatterns = rt.WSOrigins
	} else if rt.devMode {
		opts.InsecureSkipVerify = true
	}
	conn, err := websocket.Accept(w, r, opts)
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 20) // 1MB max incoming message
	defer conn.Close(websocket.StatusNormalClosure, "")

	ws := &wsConn{conn: conn, acceptLanguage: r.Header.Get("Accept-Language")}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Track this connection's stream subscriptions for cleanup on disconnect
	var connSubs []connStreamSub
	defer func() {
		for _, cs := range connSubs {
			rt.Streams.Unsubscribe(cs.apiName, cs.sub)
		}
	}()

	for {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			return
		}

		if msgType == websocket.MessageText {
			// Check for $sub/$unsub before dispatching as request
			if isStreamMsg(data) {
				rt.handleWSStreamJSON(ctx, ws, data, &connSubs)
			} else {
				go rt.handleWSJSON(ctx, ws, data)
			}
		} else if len(data) > 0 {
			switch data[0] {
			case BinaryFrameSubscribe, BinaryFrameUnsubscribe:
				rt.handleWSStreamBinary(ctx, ws, data, &connSubs)
			case BinaryFrameCallRequest:
				go rt.handleWSBinary(ctx, ws, data)
			}
		}
	}
}

// connStreamSub tracks a subscription belonging to this WS connection.
type connStreamSub struct {
	apiName string
	sub     *StreamSub
}

// isStreamMsg checks if a JSON message has "$sub" or "$unsub" as a top-level key.
// Tracks brace depth to ensure the key is at the root object level (depth == 1).
func isStreamMsg(data []byte) bool {
	depth := 0
	inString := false
	escape := false
	for i := 0; i < len(data); i++ {
		if escape {
			escape = false
			continue
		}
		ch := data[i]
		if inString {
			if ch == '\\' {
				escape = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
		case '"':
			if depth == 1 && matchStreamKey(data, i) {
				return true
			}
			inString = true
		}
	}
	return false
}

// matchStreamKey checks if data[i:] starts with "$sub": or "$unsub":.
func matchStreamKey(data []byte, i int) bool {
	if i+1 >= len(data) || data[i+1] != '$' {
		return false
	}
	if i+6 <= len(data) && data[i+2] == 's' && data[i+3] == 'u' && data[i+4] == 'b' && data[i+5] == '"' {
		return hasColonAfter(data, i+6)
	}
	if i+8 <= len(data) && data[i+2] == 'u' && data[i+3] == 'n' && data[i+4] == 's' && data[i+5] == 'u' && data[i+6] == 'b' && data[i+7] == '"' {
		return hasColonAfter(data, i+8)
	}
	return false
}

// hasColonAfter checks if the next non-whitespace byte at or after pos is ':'.
func hasColonAfter(data []byte, pos int) bool {
	for pos < len(data) {
		if data[pos] == ' ' || data[pos] == '\t' || data[pos] == '\n' || data[pos] == '\r' {
			pos++
			continue
		}
		return data[pos] == ':'
	}
	return false
}

// handleWSStreamJSON handles JSON $sub/$unsub messages.
func (rt *Router) handleWSStreamJSON(ctx context.Context, ws *wsConn, data []byte, connSubs *[]connStreamSub) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}

	// Subscribe: {"$sub": "watchDanmaku", "roomId": 123}
	if subRaw, ok := raw["$sub"]; ok {
		var apiName string
		if err := json.Unmarshal(subRaw, &apiName); err != nil || apiName == "" {
			rt.writeWSJSONSubscriptionError(ctx, ws, apiName, errors.New("BadRequest", http.StatusBadRequest, "invalid $sub API name"))
			return
		}
		req, params, err := rt.parseJSONSubscription(raw)
		if err != nil {
			rt.writeWSJSONSubscriptionError(ctx, ws, apiName, errors.New("BadRequest", http.StatusBadRequest, err.Error()))
			return
		}
		if !rt.isStreamAPI(apiName) {
			rt.writeWSJSONSubscriptionError(ctx, ws, apiName, errors.New("BadRequest", http.StatusBadRequest, apiName+" is not a stream API"))
			return
		}
		if hasConnectionSubscription(*connSubs, apiName) {
			rt.writeWSJSONSubscriptionError(ctx, ws, apiName, errors.New("Conflict", http.StatusConflict, apiName+" is already subscribed"))
			return
		}

		// Extract identity from context
		identity := rt.identityFromCtx(ctx)

		// Per-subscription context — cancelled on slow consumer or connection close.
		subCtx, subCancel := context.WithCancel(ctx)

		sub := rt.Streams.SubscribeMode(apiName, params, identity, req.FieldMask, false, subCancel)
		if sub == nil {
			subCancel()
			rt.writeWSJSONSubscriptionError(ctx, ws, apiName, errors.New("Unavailable", http.StatusServiceUnavailable, apiName+" subscriber limit reached"))
			return
		}
		sub.Params.binary = append([]byte(nil), req.BinaryParams()...)
		*connSubs = append(*connSubs, connStreamSub{apiName: apiName, sub: sub})
		if err := rt.writeWSJSONSubscriptionSuccess(ctx, ws, apiName); err != nil {
			return
		}

		// Start write pump
		go WritePumpJSON(subCtx, ws, apiName, sub)

		rt.startNativeStreamHandler(subCtx, apiName, params, identity, sub)
		return
	}

	// Unsubscribe: {"$unsub": "watchDanmaku"}
	if unsubRaw, ok := raw["$unsub"]; ok {
		var apiName string
		json.Unmarshal(unsubRaw, &apiName)
		for i, cs := range *connSubs {
			if cs.apiName == apiName {
				rt.Streams.Unsubscribe(apiName, cs.sub)
				*connSubs = append((*connSubs)[:i], (*connSubs)[i+1:]...)
				return
			}
		}
	}
}

func (rt *Router) parseJSONSubscription(raw map[string]json.RawMessage) (*Request, map[string]any, error) {
	requestRaw := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		if key != "$sub" && key != "$unsub" {
			requestRaw[key] = value
		}
	}
	requestRaw["$api"] = raw["$sub"]
	req, err := parseRawRequest(requestRaw)
	if err != nil {
		return nil, nil, err
	}
	if err := rt.prepareRequest(req, false); err != nil {
		return nil, nil, err
	}
	canonical, err := rt.Registry.ParseBinaryRequest(req.BinaryRequest())
	if err != nil {
		return nil, nil, err
	}
	return canonical, binaryStreamParams(canonical), nil
}

// handleWSStreamBinary handles binary stream subscribe/unsubscribe.
func (rt *Router) handleWSStreamBinary(ctx context.Context, ws *wsConn, data []byte, connSubs *[]connStreamSub) {
	if len(data) < 2 {
		return
	}

	frameType := data[0]
	off := 1

	apiID, n := codec.ReadVarint(data, off)
	if n <= 0 {
		return
	}
	off += n

	apiName, ok := rt.Registry.NameByID(int(apiID))
	if !ok {
		if frameType == BinaryFrameSubscribe {
			rt.writeWSBinarySubscriptionError(ctx, ws, apiID, errors.NotFound.WithData(errors.ResourceError{Resource: fmt.Sprintf("API %d", apiID)}))
		}
		return
	}

	if frameType == BinaryFrameUnsubscribe {
		if off != len(data) {
			return
		}
		// Unsubscribe
		for i, cs := range *connSubs {
			if cs.apiName == apiName {
				rt.Streams.Unsubscribe(apiName, cs.sub)
				*connSubs = append((*connSubs)[:i], (*connSubs)[i+1:]...)
				return
			}
		}
		return
	}
	if !rt.isStreamAPI(apiName) {
		rt.writeWSBinarySubscriptionError(ctx, ws, apiID, errors.New("BadRequest", http.StatusBadRequest, apiName+" is not a stream API"))
		return
	}
	if hasConnectionSubscription(*connSubs, apiName) {
		rt.writeWSBinarySubscriptionError(ctx, ws, apiID, errors.New("Conflict", http.StatusConflict, apiName+" is already subscribed"))
		return
	}

	// Subscribe body is identical to the canonical HTTP binary request body.
	req, err := rt.Registry.ParseBinaryRequest(data[1:])
	if err != nil {
		rt.writeWSBinarySubscriptionError(ctx, ws, apiID, errors.New("BadRequest", http.StatusBadRequest, err.Error()))
		return
	}
	params := binaryStreamParams(req)

	identity := rt.identityFromCtx(ctx)
	subCtx, subCancel := context.WithCancel(ctx)

	sub := rt.Streams.SubscribeMode(apiName, params, identity, req.FieldMask, true, subCancel)
	if sub == nil {
		subCancel()
		rt.writeWSBinarySubscriptionError(ctx, ws, apiID, errors.New("Unavailable", http.StatusServiceUnavailable, apiName+" subscriber limit reached"))
		return
	}
	sub.Params.binary = append([]byte(nil), req.BinaryParams()...)
	*connSubs = append(*connSubs, connStreamSub{apiName: apiName, sub: sub})
	if err := rt.writeWSBinarySubscriptionSuccess(ctx, ws, apiID); err != nil {
		return
	}

	go WritePumpBinary(subCtx, ws, int(apiID), sub)

	rt.startNativeStreamHandler(subCtx, apiName, params, identity, sub)
}

func hasConnectionSubscription(subscriptions []connStreamSub, apiName string) bool {
	for _, subscription := range subscriptions {
		if subscription.apiName == apiName {
			return true
		}
	}
	return false
}

// IdentityExtractor extracts identity from context.
// Set by luvia package at init to avoid circular imports.
var IdentityExtractor func(ctx context.Context) any

func identityFromCtx(ctx context.Context) any {
	if IdentityExtractor != nil {
		return IdentityExtractor(ctx)
	}
	return nil
}

func (rt *Router) identityFromCtx(ctx context.Context) any {
	if rt.IdentityExtractor != nil {
		return rt.IdentityExtractor(ctx)
	}
	return identityFromCtx(ctx)
}

// handleWSJSON processes a JSON WebSocket message.
func (rt *Router) handleWSJSON(ctx context.Context, ws *wsConn, data []byte) {
	// Parse: {"$id": N, "$api": "name", ...params}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return // malformed, drop
	}

	// Extract $id
	var seqID int64
	if idRaw, ok := raw["$id"]; ok {
		json.Unmarshal(idRaw, &seqID)
	}

	req, err := parseRawRequest(raw)
	if err != nil {
		rt.writeWSJSONError(ctx, ws, seqID, "BadRequest", 400, err.Error())
		return
	}
	req.BinaryMode = true // handler always writes binary

	// Find handler
	fn, ok := rt.handlers[req.API]
	if !ok {
		rt.writeWSJSONError(ctx, ws, seqID, "NotFound", 404, req.API+" not found")
		return
	}

	if err := rt.prepareRequest(req, false); err != nil {
		rt.writeWSJSONError(ctx, ws, seqID, "BadRequest", 400, err.Error())
		return
	}

	// Execute handler
	buf := GetBuf()
	req.Buf = buf

	herr := rt.callHandler(fn, ctx, req)
	if herr != nil {
		PutBuf(buf)
		var appErr *errors.AppError
		if !stderrors.As(herr, &appErr) {
			appErr = errors.Wrap(herr)
		}
		rt.writeWSJSONError(ctx, ws, seqID, appErr.Name, appErr.Code, appErr.Message)
		return
	}

	// Write JSON response: {"$id": N, "data": ...}
	resp := GetBuf()
	resp.AppendString(`{"$id":`)
	resp.AppendInt(seqID)
	resp.AppendString(`,"data":`)
	resp.B = append(resp.B, rt.convertBinaryToJSON(req.API, buf.B)...)
	resp.AppendByte('}')

	ws.writeText(ctx, resp.B)
	PutBuf(resp)
	PutBuf(buf)
}

// handleWSBinary processes a binary WebSocket message.
// WS binary format: [frame type][seq varint][binary request body (same as HTTP binary)]
// Reuses ParseBinaryRequest for the actual parsing.
func (rt *Router) handleWSBinary(ctx context.Context, ws *wsConn, data []byte) {
	if len(data) < 3 || data[0] != BinaryFrameCallRequest {
		return
	}

	seq, n := codec.ReadVarint(data, 1)
	if n <= 0 {
		return
	}

	reqBody := data[1+n:]
	req, err := rt.Registry.ParseBinaryRequest(reqBody)
	if err != nil {
		rt.writeWSBinaryError(ctx, ws, seq, errors.New("BadRequest", http.StatusBadRequest, err.Error()))
		return
	}

	// Find handler
	fn, ok := rt.handlers[req.API]
	if !ok {
		rt.writeWSBinaryError(ctx, ws, seq, errors.NotFound.WithData(errors.ResourceError{Resource: req.API}))
		return
	}

	// Execute handler
	buf := GetBuf()
	req.Buf = buf

	herr := rt.callHandler(fn, ctx, req)
	if herr != nil {
		PutBuf(buf)
		rt.writeWSBinaryError(ctx, ws, seq, herr)
		return
	}

	// Write binary response: [0x02=success][seq varint][payload]
	resp := GetBuf()
	resp.B = append(resp.B, BinaryFrameCallSuccess)
	resp.B = codec.AppendVarint(resp.B, seq)
	resp.B = append(resp.B, buf.B...)

	ws.writeBinary(ctx, resp.B)
	PutBuf(resp)
	PutBuf(buf)
}

// writeWSJSONError sends a JSON error frame.
func (rt *Router) writeWSJSONError(ctx context.Context, ws *wsConn, seqID int64, name string, code int, msg string) {
	buf := GetBuf()
	buf.AppendString(`{"$id":`)
	buf.AppendInt(seqID)
	buf.AppendString(`,"error":`)
	buf.AppendJSONString(name)
	buf.AppendString(`,"code":`)
	buf.AppendInt(int64(code))
	buf.AppendString(`,"message":`)
	buf.AppendJSONString(msg)
	buf.AppendByte('}')
	ws.writeText(ctx, buf.B)
	PutBuf(buf)
}

// writeWSBinaryError sends [0x03][seq varint][canonical binary error envelope].
func (rt *Router) writeWSBinaryError(ctx context.Context, ws *wsConn, seq uint64, err error) {
	buf := GetBuf()
	buf.B = append(buf.B, BinaryFrameCallError)
	buf.B = codec.AppendVarint(buf.B, seq)
	buf.B = appendBinaryError(buf.B, rt.buildWireError(ctx, ws.acceptLanguage, err))
	ws.writeBinary(ctx, buf.B)
	PutBuf(buf)
}

func (rt *Router) writeWSJSONSubscriptionSuccess(ctx context.Context, ws *wsConn, apiName string) error {
	buf := GetBuf()
	defer PutBuf(buf)
	buf.AppendString(`{"$sub":`)
	buf.AppendJSONString(apiName)
	buf.AppendString(`,"ok":true}`)
	return ws.writeText(ctx, buf.B)
}

func (rt *Router) writeWSJSONSubscriptionError(ctx context.Context, ws *wsConn, apiName string, err error) error {
	buf := GetBuf()
	defer PutBuf(buf)
	buf.AppendString(`{"$sub":`)
	buf.AppendJSONString(apiName)
	buf.AppendByte(',')
	appendJSONErrorFields(buf, rt.buildWireError(ctx, ws.acceptLanguage, err))
	buf.AppendByte('}')
	return ws.writeText(ctx, buf.B)
}

func (rt *Router) writeWSBinarySubscriptionSuccess(ctx context.Context, ws *wsConn, apiID uint64) error {
	buf := GetBuf()
	defer PutBuf(buf)
	buf.B = append(buf.B, BinaryFrameSubscribeSuccess)
	buf.B = codec.AppendVarint(buf.B, apiID)
	return ws.writeBinary(ctx, buf.B)
}

func (rt *Router) writeWSBinarySubscriptionError(ctx context.Context, ws *wsConn, apiID uint64, err error) error {
	buf := GetBuf()
	defer PutBuf(buf)
	buf.B = append(buf.B, BinaryFrameSubscribeError)
	buf.B = codec.AppendVarint(buf.B, apiID)
	buf.B = appendBinaryError(buf.B, rt.buildWireError(ctx, ws.acceptLanguage, err))
	return ws.writeBinary(ctx, buf.B)
}
