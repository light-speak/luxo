package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	stderrors "errors"

	"github.com/bytedance/sonic"
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
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Allow all origins — CORS is handled at the middleware layer.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ws := &wsConn{conn: conn}
	ctx := r.Context()

	// Auth: extract token from query param (WS can't set custom headers)
	// Token is set before upgrade, identity is in r.Context() from AuthMiddleware.

	for {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			return // connection closed or error
		}

		if msgType == websocket.MessageText {
			// JSON mode — dispatch in goroutine for concurrency
			go rt.handleWSJSON(ctx, ws, data)
		} else {
			// Binary mode
			go rt.handleWSBinary(ctx, ws, data)
		}
	}
}

// handleWSJSON processes a JSON WebSocket message.
func (rt *Router) handleWSJSON(ctx context.Context, ws *wsConn, data []byte) {
	// Parse: {"$id": N, "$api": "name", ...params}
	var raw map[string]json.RawMessage
	if err := sonic.Unmarshal(data, &raw); err != nil {
		return // malformed, drop
	}

	// Extract $id
	var seqID int64
	if idRaw, ok := raw["$id"]; ok {
		sonic.Unmarshal(idRaw, &seqID)
	}

	// Extract $api
	var apiName string
	if apiRaw, ok := raw["$api"]; ok {
		sonic.Unmarshal(apiRaw, &apiName)
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
		if sonic.Unmarshal(selRaw, &selStr) == nil && selStr != "" {
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
