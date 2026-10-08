package api

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	stderrors "errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"unicode/utf8"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
	"nhooyr.io/websocket"
)

// wsConn wraps a WebSocket connection with write serialization.
// WebSocket spec: concurrent writes are not safe, so we serialize with a mutex.
// Reads are single-goroutine (the read loop), no lock needed.
type wsConn struct {
	conn           *websocket.Conn
	ctx            context.Context // connection lifetime, never a subscription's context
	acceptLanguage string
	mu             sync.Mutex // serialize writes
}

func (c *wsConn) writeText(ctx context.Context, data []byte) error {
	return c.write(ctx, websocket.MessageText, data)
}

func (c *wsConn) writeBinary(ctx context.Context, data []byte) error {
	return c.write(ctx, websocket.MessageBinary, data)
}

func (c *wsConn) write(ctx context.Context, kind websocket.MessageType, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// A queued message can be discarded. Once writing starts, the entire frame
	// must finish: cancelling websocket.Write closes the shared connection.
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return c.conn.Write(c.ctx, kind, data)
	}
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
//	  Subscribe: [0x10][subscription UInt64][API ID][FieldMask len][mask][Params...][0x00]
//	  Unsub:     [0x11][subscription UInt64]
//	  Stream:    [0x12][subscription UInt64][payload]
//	  Sub OK:    [0x13][subscription UInt64]
//	  Sub Error: [0x14][subscription UInt64][Binary Error Envelope]
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

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	ws := &wsConn{conn: conn, ctx: ctx, acceptLanguage: r.Header.Get("Accept-Language")}

	// Track this connection's stream subscriptions for cleanup on disconnect
	var connSubs connectionSubscriptions
	defer connSubs.close(rt.Streams)

	for {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			return
		}

		if msgType == websocket.MessageText {
			if !rt.handleWSText(ctx, ws, data, &connSubs) {
				return
			}
		} else if len(data) > 0 {
			switch data[0] {
			case 0x04, 0x05, 0x06, 0x07, 0x08:
				conn.Close(websocket.StatusProtocolError, "retired stream protocol")
				return
			case BinaryFrameSubscribe, BinaryFrameUnsubscribe:
				rt.handleWSStreamBinary(ctx, ws, data, &connSubs)
			case BinaryFrameCallRequest:
				go rt.handleWSBinary(ctx, ws, data)
			}
		}
	}
}

// Validate the complete message, not individual fragments. RFC 6455 §8.1.
// Classify only decoded top-level names, including escaped control keys.
func (rt *Router) handleWSText(ctx context.Context, ws *wsConn, data []byte, connSubs *connectionSubscriptions) bool {
	raw, err := jsonutil.ParseObject(data)
	if err != nil {
		if invalidJSONUnicode(data, err) {
			ws.conn.Close(websocket.StatusInvalidFramePayloadData, "invalid JSON Unicode")
			return false
		}
		return true // Preserve the existing malformed-JSON drop policy.
	}
	if isStreamRequest(raw) {
		rt.handleWSStreamJSON(ctx, ws, raw, connSubs)
	} else {
		go rt.handleWSJSON(ctx, ws, raw)
	}
	return true
}

func isStreamRequest(raw map[string]json.RawMessage) bool {
	_, sub := raw["$sub"]
	_, unsub := raw["$unsub"]
	return sub || unsub
}

func invalidJSONUnicode(data []byte, err error) bool {
	if !utf8.Valid(data) {
		return true
	}
	// Only the failure path distinguishes invalid escapes from ordinary syntax
	// errors. No second scan is added to successful messages. The strict parser
	// already allows duplicates, so a v1-valid syntactic failure is Unicode.
	_, syntax := stderrors.AsType[*jsontext.SyntacticError](err)
	return syntax && json.Valid(data)
}

// handleWSStreamJSON handles JSON $sub/$unsub messages.
func (rt *Router) handleWSStreamJSON(ctx context.Context, ws *wsConn, raw map[string]json.RawMessage, connSubs *connectionSubscriptions) {
	// Subscribe: {"$id": 1, "$sub": "watchDanmaku", "roomId": 123}
	if _, ok := raw["$sub"]; ok {
		id, valid := parseWSJSONSequence(raw["$id"])
		if !valid {
			return // No trustworthy subscription ID; do not dispatch or reply.
		}
		if !connSubs.reserve(id) {
			rt.writeWSJSONSubscriptionError(ctx, ws, id, errors.New("Conflict", http.StatusConflict, "subscription IDs must increase and cannot be reused"))
			return
		}
		if _, ambiguous := raw["$unsub"]; ambiguous {
			rt.writeWSJSONSubscriptionError(ctx, ws, id, errors.New("BadRequest", http.StatusBadRequest, "subscription cannot also unsubscribe"))
			return
		}
		req, params, err := rt.prepareWSJSONSubscription(raw)
		if err != nil {
			rt.writeWSJSONSubscriptionError(ctx, ws, id, errors.New("BadRequest", http.StatusBadRequest, err.Error()))
			return
		}
		rt.startWSSubscription(ctx, ws, connSubs, id, req, params, false)
		return
	}

	// Unsubscribe: {"$unsub": 1}; API names never identify an existing instance.
	if unsubRaw, ok := raw["$unsub"]; ok && len(raw) == 1 {
		if id, valid := parseWSJSONSequence(unsubRaw); valid {
			rt.unsubscribeWS(connSubs, id)
		}
	}
}

func (rt *Router) prepareWSJSONSubscription(raw map[string]json.RawMessage) (*Request, map[string]any, error) {
	req, params, err := rt.parseJSONSubscription(raw)
	if err != nil {
		return nil, nil, err
	}
	if !rt.isStreamAPI(req.API) {
		return nil, nil, fmt.Errorf("%s is not a stream API", req.API)
	}
	return req, params, nil
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
	if rt.streamHandlers[req.API].preparesInputs() {
		if err := rt.prepareCallRequest(req, false, true); err != nil {
			return nil, nil, err
		}
		return req, nil, nil
	}
	if err := rt.prepareRequest(req, false); err != nil {
		return nil, nil, err
	}
	if rt.streamHandlers[req.API].forwardPrepare != nil {
		return req, nil, nil
	}
	canonical, err := rt.Registry.ParseBinaryRequest(req.BinaryRequest())
	if err != nil {
		return nil, nil, err
	}
	return canonical, binaryStreamParams(canonical), nil
}

// handleWSStreamBinary handles binary stream subscribe/unsubscribe.
func (rt *Router) handleWSStreamBinary(ctx context.Context, ws *wsConn, data []byte, connSubs *connectionSubscriptions) {
	if len(data) < 2 {
		return
	}

	frameType := data[0]
	off := 1

	id, n := codec.ReadVarint(data, off)
	if n <= 0 {
		return
	}
	off += n

	if frameType == BinaryFrameUnsubscribe {
		if off != len(data) {
			return
		}
		rt.unsubscribeWS(connSubs, id)
		return
	}
	if !connSubs.reserve(id) {
		rt.writeWSBinarySubscriptionError(ctx, ws, id, errors.New("Conflict", http.StatusConflict, "subscription IDs must increase and cannot be reused"))
		return
	}
	apiID, size := codec.ReadVarint(data, off)
	if size <= 0 {
		rt.writeWSBinarySubscriptionError(ctx, ws, id, errors.New("BadRequest", http.StatusBadRequest, "invalid API ID"))
		return
	}
	apiName, ok := rt.Registry.NameByID(int(apiID))
	if !ok || uint64(int(apiID)) != apiID || int(apiID) < 0 {
		rt.writeWSBinarySubscriptionError(ctx, ws, id, errors.NotFound.WithData(errors.ResourceError{Resource: fmt.Sprintf("API %d", apiID)}))
		return
	}
	if !rt.isStreamAPI(apiName) {
		rt.writeWSBinarySubscriptionError(ctx, ws, id, errors.New("BadRequest", http.StatusBadRequest, apiName+" is not a stream API"))
		return
	}

	// Subscribe body is identical to the canonical HTTP binary request body.
	req := GetRequest()
	defer PutRequest(req)
	if err := rt.Registry.ParseBinaryRequestInto(data[off:], req); err != nil {
		rt.writeWSBinarySubscriptionError(ctx, ws, id, errors.New("BadRequest", http.StatusBadRequest, err.Error()))
		return
	}
	if err := rt.validatePublicSelection(req, true); err != nil {
		rt.writeWSBinarySubscriptionError(ctx, ws, id, errors.New("BadRequest", http.StatusBadRequest, err.Error()))
		return
	}
	rt.startWSSubscription(ctx, ws, connSubs, id, req, rt.streamInputValues(req), true)
}

func (rt *Router) startWSSubscription(ctx context.Context, ws *wsConn, connSubs *connectionSubscriptions, id uint64, req *Request, params map[string]any, binary bool) {
	apiName := req.API
	identity := rt.identityFromCtx(ctx)
	prepared, err := rt.prepareStreamHandler(ctx, req, identity)
	if err != nil {
		if binary {
			rt.writeWSBinarySubscriptionError(ctx, ws, id, err)
		} else {
			rt.writeWSJSONSubscriptionError(ctx, ws, id, err)
		}
		return
	}
	sub, subCtx, writerCtx := rt.Streams.subscribePreparedWS(ctx, apiName, params, identity, preparedStreamFieldMask(req, prepared), binary, prepared)
	if sub == nil {
		if prepared.release != nil {
			prepared.release()
		}
		err := errors.New("Unavailable", http.StatusServiceUnavailable, apiName+" subscriber limit reached")
		if binary {
			rt.writeWSBinarySubscriptionError(ctx, ws, id, err)
		} else {
			rt.writeWSJSONSubscriptionError(ctx, ws, id, err)
		}
		return
	}
	if !prepared.valid() {
		sub.Params.binary = append([]byte(nil), req.BinaryParams()...)
	}
	connSubs.add(id, apiName, sub)
	if binary {
		err = rt.writeWSBinarySubscriptionSuccess(ctx, ws, id)
	} else {
		err = rt.writeWSJSONSubscriptionSuccess(ctx, ws, id)
	}
	if err != nil {
		rt.Streams.Unsubscribe(apiName, sub)
		connSubs.remove(id, sub)
		return
	}
	go rt.writeSubscription(writerCtx, ws, apiName, id, sub, connSubs)
	rt.startNativeStreamHandler(subCtx, apiName, identity, sub, prepared.run)
}

func (rt *Router) unsubscribeWS(connSubs *connectionSubscriptions, id uint64) {
	if item, ok := connSubs.take(id); ok {
		rt.Streams.Unsubscribe(item.apiName, item.sub)
	}
}

func (rt *Router) writeSubscription(ctx context.Context, ws *wsConn, apiName string, id uint64, sub *StreamSub, connSubs *connectionSubscriptions) {
	defer connSubs.remove(id, sub)
	defer rt.Streams.Unsubscribe(apiName, sub)
	var err error
	if sub.Binary {
		err = WritePumpBinary(ctx, ws, id, sub)
	} else {
		err = WritePumpJSON(ctx, ws, id, sub)
	}
	if err == nil && ctx.Err() == nil {
		// Receiving the closed channel publishes the immutable terminal result.
		// Only this writer sends it, after all accepted data, at most once.
		rt.writeWSStreamEnd(ctx, ws, id, sub.Binary, sub.result)
	}
}

// subscribeWS gives the producer and writer sibling lifetimes. Finishing the
// producer cancels its work immediately without interrupting accepted writes.
// Explicit unsubscribe, slow-consumer removal and disconnect still abort both.
func (h *StreamHub) subscribeWS(ctx context.Context, apiName string, params map[string]any, identity any, mask []byte, binary bool) (*StreamSub, context.Context, context.Context) {
	return h.subscribePreparedWS(ctx, apiName, params, identity, mask, binary, preparedStream{})
}

func (h *StreamHub) subscribePreparedWS(ctx context.Context, apiName string, params map[string]any, identity any, mask []byte, binary bool, prepared preparedStream) (*StreamSub, context.Context, context.Context) {
	producer, cancelProducer := context.WithCancel(ctx)
	writer, cancelWriter := context.WithCancel(ctx)
	sub := h.subscribePreparedMode(apiName, params, identity, mask, binary, cancelProducer, cancelWriter, prepared)
	if sub == nil {
		cancelProducer()
		cancelWriter()
	}
	return sub, producer, writer
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
func (rt *Router) handleWSJSON(ctx context.Context, ws *wsConn, raw map[string]json.RawMessage) {
	seqID, valid := parseWSJSONSequence(raw["$id"])
	if !valid {
		return // No trustworthy correlation ID; drop just like an invalid binary sequence.
	}

	req, err := parseRawRequest(raw)
	if err != nil {
		rt.writeWSJSONError(ctx, ws, seqID, errors.New("BadRequest", http.StatusBadRequest, err.Error()))
		return
	}
	// Find handler
	fn, ok := rt.handlers[req.API]
	if !ok {
		rt.writeWSJSONError(ctx, ws, seqID, errors.New("NotFound", http.StatusNotFound, req.API+" not found"))
		return
	}

	if err := rt.prepareCallRequest(req, false, fn.localJSON); err != nil {
		rt.writeWSJSONError(ctx, ws, seqID, errors.New("BadRequest", http.StatusBadRequest, err.Error()))
		return
	}

	// Execute handler
	buf := GetBuf()
	req.Buf = buf

	herr := rt.callHandler(fn.call, ctx, req)
	if herr != nil {
		PutBuf(buf)
		rt.writeWSJSONError(ctx, ws, seqID, herr)
		return
	}

	// Write JSON response: {"$id": N, "data": ...}
	jsonBody, err := rt.convertBinaryToJSON(req.API, buf.B)
	if err != nil {
		PutBuf(buf)
		rt.writeWSJSONError(ctx, ws, seqID, err)
		return
	}
	resp := GetBuf()
	resp.AppendString(`{"$id":`)
	resp.B = strconv.AppendUint(resp.B, seqID, 10)
	resp.AppendString(`,"data":`)
	resp.B = append(resp.B, jsonBody...)
	resp.AppendByte('}')

	ws.writeText(ctx, resp.B)
	PutBuf(resp)
	PutBuf(buf)
}

// parseWSJSONSequence preserves the binary uint64 domain without a float or signed intermediate.
// Parse the integer token directly; this adds no reflection or JSON serialization pass.
func parseWSJSONSequence(raw []byte) (uint64, bool) {
	if len(raw) == 0 || raw[0] < '0' || raw[0] > '9' || (len(raw) > 1 && raw[0] == '0') {
		return 0, false
	}
	id, err := strconv.ParseUint(string(raw), 10, 64)
	return id, err == nil
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
	req := GetRequest()
	defer PutRequest(req)
	if err := rt.Registry.ParseBinaryRequestInto(reqBody, req); err != nil {
		rt.writeWSBinaryError(ctx, ws, seq, errors.New("BadRequest", http.StatusBadRequest, err.Error()))
		return
	}

	// Find handler
	fn, ok := rt.handlers[req.API]
	if !ok {
		rt.writeWSBinaryError(ctx, ws, seq, errors.NotFound.WithData(errors.ResourceError{Resource: req.API}))
		return
	}
	if err := rt.validatePublicSelection(req, true); err != nil {
		rt.writeWSBinaryError(ctx, ws, seq, errors.New("BadRequest", http.StatusBadRequest, err.Error()))
		return
	}

	// Execute handler
	buf := GetBuf()
	req.Buf = buf

	herr := rt.callHandler(fn.call, ctx, req)
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
func (rt *Router) writeWSJSONError(ctx context.Context, ws *wsConn, seqID uint64, err error) {
	buf := GetBuf()
	appendWSJSONError(buf, seqID, rt.buildWireError(ctx, ws.acceptLanguage, err))
	ws.writeText(ctx, buf.B)
	PutBuf(buf)
}

func appendWSJSONError(buf *ResponseBuf, seqID uint64, err wireError) {
	buf.AppendString(`{"$id":`)
	buf.B = strconv.AppendUint(buf.B, seqID, 10)
	buf.AppendByte(',')
	appendWireErrorFields(buf, err, false)
	buf.AppendByte('}')
}

// writeWSBinaryError sends [0x03][seq varint][canonical binary error envelope].
func (rt *Router) writeWSBinaryError(ctx context.Context, ws *wsConn, seq uint64, err error) {
	buf := GetBuf()
	buf.B = append(buf.B, BinaryFrameCallError)
	buf.B = codec.AppendVarint(buf.B, seq)
	appendWireErrorFields(buf, rt.buildWireError(ctx, ws.acceptLanguage, err), true)
	ws.writeBinary(ctx, buf.B)
	PutBuf(buf)
}

func (rt *Router) writeWSJSONSubscriptionSuccess(ctx context.Context, ws *wsConn, id uint64) error {
	return rt.writeWSJSONStreamResult(ctx, ws, id, false, nil)
}

func (rt *Router) writeWSJSONSubscriptionError(ctx context.Context, ws *wsConn, id uint64, err error) error {
	return rt.writeWSJSONStreamResult(ctx, ws, id, false, err)
}

func (rt *Router) writeWSJSONStreamResult(ctx context.Context, ws *wsConn, id uint64, terminal bool, err error) error {
	buf := GetBuf()
	defer PutBuf(buf)
	if terminal {
		buf.AppendString(`{"$end":`)
	} else {
		buf.AppendString(`{"$sub":`)
	}
	buf.B = strconv.AppendUint(buf.B, id, 10)
	buf.AppendByte(',')
	if err == nil {
		buf.AppendString(`"ok":true`)
	} else {
		appendWireErrorFields(buf, rt.buildWireError(ctx, ws.acceptLanguage, err), false)
	}
	buf.AppendByte('}')
	return ws.writeText(ctx, buf.B)
}

func (rt *Router) writeWSBinarySubscriptionSuccess(ctx context.Context, ws *wsConn, id uint64) error {
	return rt.writeWSBinaryStreamResult(ctx, ws, id, BinaryFrameSubscribeSuccess, nil)
}

func (rt *Router) writeWSBinarySubscriptionError(ctx context.Context, ws *wsConn, id uint64, err error) error {
	return rt.writeWSBinaryStreamResult(ctx, ws, id, BinaryFrameSubscribeError, err)
}

func (rt *Router) writeWSBinaryStreamResult(ctx context.Context, ws *wsConn, id uint64, frame byte, err error) error {
	buf := GetBuf()
	defer PutBuf(buf)
	buf.B = append(buf.B, frame)
	buf.B = codec.AppendVarint(buf.B, id)
	if err != nil {
		appendWireErrorFields(buf, rt.buildWireError(ctx, ws.acceptLanguage, err), true)
	}
	return ws.writeBinary(ctx, buf.B)
}

func (rt *Router) writeWSStreamEnd(ctx context.Context, ws *wsConn, id uint64, binary bool, err error) error {
	if !binary {
		return rt.writeWSJSONStreamResult(ctx, ws, id, true, err)
	}
	frame := BinaryFrameStreamComplete
	if err != nil {
		frame = BinaryFrameStreamError
	}
	return rt.writeWSBinaryStreamResult(ctx, ws, id, frame, err)
}
