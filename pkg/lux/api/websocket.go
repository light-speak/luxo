package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	stderrors "errors"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/lux/selection"
	"nhooyr.io/websocket"
)

// wsConn wraps a WebSocket connection with write serialization.
// WebSocket spec: concurrent writes are not safe, so we serialize with a mutex.
// Reads are single-goroutine (the read loop), no lock needed.
type wsConn struct {
	conn *websocket.Conn
	mu   sync.Mutex // serialize writes
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
//	  Request:  [seq varint][API ID varint][FieldMask len][mask][Params...][0x00]
//	  Response: [seq varint][status: 0x01=ok | 0x00=error][payload]
func (rt *Router) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	opts := &websocket.AcceptOptions{}
	if len(rt.WSOrigins) > 0 {
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

	ws := &wsConn{conn: conn}
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
		} else {
			// Binary: check prefix byte for stream messages
			if len(data) > 0 && (data[0] == 0xFE || data[0] == 0xFF) {
				rt.handleWSStreamBinary(ctx, ws, data, &connSubs)
			} else {
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
		json.Unmarshal(subRaw, &apiName)
		if apiName == "" {
			return
		}

		// Extract params (non-reserved keys)
		params := make(map[string]any)
		for k, v := range raw {
			if k == "$sub" || k == "$select" {
				continue
			}
			var val any
			json.Unmarshal(v, &val)
			params[k] = val
		}

		// Extract identity from context
		identity := identityFromCtx(ctx)

		// Per-subscription context — cancelled on slow consumer or connection close.
		subCtx, subCancel := context.WithCancel(ctx)

		sub := rt.Streams.SubscribeMode(apiName, params, identity, nil, false, subCancel)
		*connSubs = append(*connSubs, connStreamSub{apiName: apiName, sub: sub})

		// Start write pump
		go WritePumpJSON(subCtx, ws, apiName, sub)

		// If @stream @native without event source, invoke handler
		if handler, ok := rt.streamHandlers[apiName]; ok {
			streamParams := &StreamParams{values: params}
			stream := &Stream{sub: sub, ctx: subCtx}
			go handler(subCtx, streamParams, identity, stream)
		}
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

// handleWSStreamBinary handles binary stream subscribe/unsubscribe.
// 0xFE = subscribe: [0xFE][API ID varint][FieldMask len][mask][Params][0x00]
// 0xFF = unsubscribe: [0xFF][API ID varint]
func (rt *Router) handleWSStreamBinary(ctx context.Context, ws *wsConn, data []byte, connSubs *[]connStreamSub) {
	if len(data) < 2 {
		return
	}

	prefix := data[0]
	off := 1

	apiID, n := codec.ReadVarint(data, off)
	if n <= 0 {
		return
	}
	off += n

	apiName, ok := rt.Registry.NameByID(int(apiID))
	if !ok {
		return
	}

	if prefix == 0xFF {
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

	// Subscribe (0xFE)
	// Read field mask
	var fieldMask []byte
	maskLen, mn := codec.ReadVarint(data, off)
	if mn > 0 {
		off += mn
		if maskLen > 0 && off+int(maskLen) <= len(data) {
			fieldMask = data[off : off+int(maskLen)]
			off += int(maskLen)
		}
	}

	// Read params
	params := make(map[string]any)
	paramMeta := rt.Registry.ParamOrder(apiName)
	paramBuf := data[off:]
	poff := 0
	for poff < len(paramBuf) {
		fid, pn := codec.ReadVarint(paramBuf, poff)
		if pn <= 0 || fid == 0 {
			break
		}
		poff += pn
		matched := false
		for _, pm := range paramMeta {
			if pm.FieldID == int(fid) {
				matched = true
				switch pm.Type {
				case "Int":
					v, vn := codec.ReadSvarint(paramBuf, poff)
					if vn > 0 {
						poff += vn
						params[pm.Name] = v
					}
				case "Float":
					v, vn := codec.ReadFixed64(paramBuf, poff)
					if vn > 0 {
						poff += vn
						params[pm.Name] = v
					}
				case "String":
					v, vn := codec.ReadString(paramBuf, poff)
					if vn > 0 {
						poff += vn
						params[pm.Name] = v
					}
				case "Boolean":
					v, vn := codec.ReadBool(paramBuf, poff)
					if vn > 0 {
						poff += vn
						params[pm.Name] = v
					}
				default:
					// Unsupported type — stop parsing
					return
				}
				break
			}
		}
		if !matched {
			// Unknown field ID — stop parsing to avoid desync
			break
		}
	}

	identity := identityFromCtx(ctx)
	subCtx, subCancel := context.WithCancel(ctx)

	sub := rt.Streams.SubscribeMode(apiName, params, identity, fieldMask, true, subCancel)
	*connSubs = append(*connSubs, connStreamSub{apiName: apiName, sub: sub})

	go WritePumpBinary(subCtx, ws, int(apiID), sub)

	// If @stream @native without event source, invoke handler
	if handler, ok := rt.streamHandlers[apiName]; ok {
		streamParams := &StreamParams{values: params}
		stream := &Stream{sub: sub, ctx: subCtx}
		go handler(subCtx, streamParams, identity, stream)
	}
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

	// Extract $api
	var apiName string
	if apiRaw, ok := raw["$api"]; ok {
		json.Unmarshal(apiRaw, &apiName)
	}
	if apiName == "" {
		rt.writeWSJSONError(ctx, ws, seqID, "BadRequest", 400, "missing $api")
		return
	}

	// Find handler
	fn, ok := rt.handlers[apiName]
	if !ok {
		rt.writeWSJSONError(ctx, ws, seqID, "NotFound", 404, apiName+" not found")
		return
	}

	// Build Request (reuse existing parsing for params)
	req := &Request{
		API:        apiName,
		Params:     make(map[string]json.RawMessage),
		BinaryMode: true, // handler always writes binary
		Page:       1,
		PageSize:   20,
	}

	// Extract params (non-reserved keys)
	for k, v := range raw {
		if k == "$id" || reservedKeys[k] {
			continue
		}
		req.Params[k] = v
	}

	// Parse $select
	if selRaw, ok := raw["$select"]; ok {
		var selStr string
		if json.Unmarshal(selRaw, &selStr) == nil && selStr != "" {
			fields, _ := selection.Parse(selStr)
			req.Select = fields
		}
	}
	// Parse page, pageSize, $filters, $sorters
	req.parseListParams(raw)

	// Convert $select to FieldMask
	if req.Select != nil && rt.Schema != nil {
		if apiMeta := rt.Schema.APIs[apiName]; apiMeta != nil && apiMeta.ReturnType != "" {
			if model := rt.Schema.Models[apiMeta.ReturnType]; model != nil {
				req.FieldMask = schema.SelectToFieldMask(req.Select, model)
			}
		}
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
	resp.B = append(resp.B, rt.convertBinaryToJSON(apiName, buf.B)...)
	resp.AppendByte('}')

	ws.writeText(ctx, resp.B)
	PutBuf(resp)
	PutBuf(buf)
}

// handleWSBinary processes a binary WebSocket message.
// WS binary format: [seq varint][binary request body (same as HTTP binary)]
// Reuses ParseBinaryRequest for the actual parsing.
func (rt *Router) handleWSBinary(ctx context.Context, ws *wsConn, data []byte) {
	if len(data) < 2 {
		return
	}

	// Read seq varint (WS-specific prefix, not part of HTTP binary format)
	seq, n := codec.ReadVarint(data, 0)
	if n <= 0 {
		return
	}

	// Remaining bytes = standard binary request body (same as HTTP)
	reqBody := data[n:]
	req, err := rt.Registry.ParseBinaryRequest(reqBody)
	if err != nil {
		rt.writeWSBinaryError(ctx, ws, int(seq), "BadRequest", 400, err.Error())
		return
	}

	// Find handler
	fn, ok := rt.handlers[req.API]
	if !ok {
		rt.writeWSBinaryError(ctx, ws, int(seq), "NotFound", 404, req.API+" not found")
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
		rt.writeWSBinaryError(ctx, ws, int(seq), appErr.Name, appErr.Code, appErr.Message)
		return
	}

	// Write binary response: [seq varint][0x01=ok][payload]
	resp := GetBuf()
	resp.B = codec.AppendVarint(resp.B, seq)
	resp.B = append(resp.B, 0x01) // status: ok
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

// writeWSBinaryError sends a binary error frame: [seq varint][0x00=error][code svarint][name string][msg string]
func (rt *Router) writeWSBinaryError(ctx context.Context, ws *wsConn, seq int, name string, code int, msg string) {
	buf := GetBuf()
	buf.B = codec.AppendVarint(buf.B, uint64(seq))
	buf.B = append(buf.B, 0x00) // status: error
	buf.B = codec.AppendSvarint(buf.B, int64(code))
	buf.B = codec.AppendString(buf.B, name)
	buf.B = codec.AppendString(buf.B, msg)
	ws.writeBinary(ctx, buf.B)
	PutBuf(buf)
}
