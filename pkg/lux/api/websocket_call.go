package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"nhooyr.io/websocket"
)

// Each call owns this stack-local completion state inside its existing task.
// Once a write starts, an abnormal exit must close the connection instead of
// sending a second terminal frame for the same correlation ID.
type wsUnaryCall struct {
	router   *Router
	ws       *wsConn
	ctx      context.Context
	id       uint64
	binary   bool
	returned bool
	writing  bool
	raw      *ResponseBuf
	output   *ResponseBuf
}

func (c *wsUnaryCall) finish() {
	c.releaseBuffers()
	if c.returned {
		return
	}
	_ = recover()
	if c.writing {
		c.ws.conn.CloseNow()
		return
	}
	c.fallback()
}

// Fallback must not invoke the failed error-data/translation/cause callbacks.
// A second abnormal exit during output closes the socket, so callers cannot
// wait forever on a connection that has lost their terminal response.
func (c *wsUnaryCall) fallback() {
	returned := false
	defer func() {
		c.releaseBuffers()
		if !returned {
			_ = recover()
			c.ws.conn.CloseNow()
		}
	}()
	c.sendWireError(wireError{Code: 500, Name: "Internal", Message: "error.internal"})
	returned = true
}

func (c *wsUnaryCall) handleJSON(raw map[string]json.RawMessage) {
	req, err := parseRawRequest(raw)
	if err != nil {
		c.sendError(errors.New("BadRequest", http.StatusBadRequest, err.Error()))
		return
	}
	fn, ok := c.router.handlers[req.API]
	if !ok {
		c.sendError(errors.New("NotFound", http.StatusNotFound, req.API+" not found"))
		return
	}
	if err := c.router.prepareCallRequest(req, false, fn.localJSON); err != nil {
		c.sendError(errors.New("BadRequest", http.StatusBadRequest, err.Error()))
		return
	}
	c.replyJSON(req, fn.call)
}

func (c *wsUnaryCall) handleBinary(data []byte) {
	req := GetRequest()
	defer PutRequest(req)
	if err := c.router.Registry.ParseBinaryRequestInto(data, req); err != nil {
		c.sendError(errors.New("BadRequest", http.StatusBadRequest, err.Error()))
		return
	}
	fn, ok := c.router.handlers[req.API]
	if !ok {
		c.sendError(errors.NotFound.WithData(errors.ResourceError{Resource: req.API}))
		return
	}
	if err := c.router.validatePublicSelection(req, true); err != nil {
		c.sendError(errors.New("BadRequest", http.StatusBadRequest, err.Error()))
		return
	}
	c.replyBinary(req, fn.call)
}

func (c *wsUnaryCall) invoke(req *Request, handler HandlerFunc) (*ResponseBuf, error) {
	c.raw = GetBuf()
	req.Buf = c.raw
	err := c.router.callHandler(handler, c.ctx, req)
	if err != nil {
		c.releaseBuffers()
		return nil, err
	}
	return c.raw, nil
}

func (c *wsUnaryCall) replyJSON(req *Request, handler HandlerFunc) {
	buf, err := c.invoke(req, handler)
	if err != nil {
		c.sendError(err)
		return
	}
	jsonBody, err := c.router.convertBinaryToJSON(req.API, buf.B)
	if err != nil {
		c.releaseBuffers()
		c.sendError(err)
		return
	}
	response := GetBuf()
	c.output = response
	response.AppendString(`{"$id":`)
	response.B = strconv.AppendUint(response.B, c.id, 10)
	response.AppendString(`,"data":`)
	response.B = append(response.B, jsonBody...)
	response.AppendByte('}')
	c.write(websocket.MessageText, response.B)
}

func (c *wsUnaryCall) replyBinary(req *Request, handler HandlerFunc) {
	buf, err := c.invoke(req, handler)
	if err != nil {
		c.sendError(err)
		return
	}
	response := GetBuf()
	c.output = response
	response.B = codec.AppendVarint(append(response.B, BinaryFrameCallSuccess), c.id)
	response.B = append(response.B, buf.B...)
	c.write(websocket.MessageBinary, response.B)
}

func (c *wsUnaryCall) write(kind websocket.MessageType, data []byte) {
	c.writing = true
	_ = c.ws.write(c.ctx, kind, data)
}

func (c *wsUnaryCall) sendError(err error) {
	value := c.router.buildWireError(c.ctx, c.ws.acceptLanguage, err)
	c.sendWireError(value)
}

func (c *wsUnaryCall) sendWireError(value wireError) {
	buf := GetBuf()
	c.output = buf
	if c.binary {
		appendWSBinaryCallError(buf, c.id, value)
		c.write(websocket.MessageBinary, buf.B)
		return
	}
	appendWSJSONError(buf, c.id, value)
	c.write(websocket.MessageText, buf.B)
}

func (c *wsUnaryCall) releaseBuffers() {
	if c.output != nil {
		PutBuf(c.output)
		c.output = nil
	}
	if c.raw != nil {
		PutBuf(c.raw)
		c.raw = nil
	}
}

func appendWSBinaryCallError(buf *ResponseBuf, id uint64, value wireError) {
	buf.B = codec.AppendVarint(append(buf.B, BinaryFrameCallError), id)
	appendWireErrorFields(buf, value, true)
}
