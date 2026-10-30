package api

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
)

// WebSocketLimits bounds admitted unary calls and queued/in-flight socket writes.
// Zero selects a finite default. Configure before serving; negative values fail Prepare.
// Calls have no waiting queue: admission failure returns 503 without dispatch.
type WebSocketLimits struct {
	ConnectionsTotal        int64
	ReadTimeout             time.Duration
	CallsPerConnection      int64
	CallsTotal              int64
	WriteBytesPerConnection int64
	WriteBytesTotal         int64
	WriteTimeout            time.Duration
}

var (
	errInvalidWSLimits = stderrors.New("api: WebSocket limits must be positive")
	errWSWriteCapacity = stderrors.New("api: WebSocket write byte capacity exceeded")
)

func normalizeWSLimits(l WebSocketLimits) WebSocketLimits {
	if l.ConnectionsTotal == 0 {
		l.ConnectionsTotal = 128
	}
	if l.ReadTimeout == 0 {
		l.ReadTimeout = 30 * time.Second
	}
	if l.CallsPerConnection == 0 {
		l.CallsPerConnection = 32
	}
	if l.CallsTotal == 0 {
		l.CallsTotal = 1024
	}
	if l.WriteBytesPerConnection == 0 {
		l.WriteBytesPerConnection = 16 << 20
	}
	if l.WriteBytesTotal == 0 {
		l.WriteBytesTotal = 256 << 20
	}
	if l.WriteTimeout == 0 {
		l.WriteTimeout = 5 * time.Second
	}
	return l
}

func (l WebSocketLimits) validate() error {
	if l.ConnectionsTotal <= 0 || l.ReadTimeout <= 0 {
		return errInvalidWSLimits
	}
	if l.CallsPerConnection <= 0 || l.CallsTotal <= 0 || l.WriteBytesPerConnection <= 0 || l.WriteBytesTotal <= 0 || l.WriteTimeout <= 0 {
		return errInvalidWSLimits
	}
	return nil
}

func reserveWS(counter *atomic.Int64, amount, limit int64) bool {
	for current := counter.Load(); amount <= limit-current; current = counter.Load() {
		if counter.CompareAndSwap(current, current+amount) {
			return true
		}
	}
	return false
}

func (rt *Router) acquireWSCall(ws *wsConn) bool {
	if !reserveWS(&ws.calls, 1, rt.wsLimits.CallsPerConnection) {
		return false
	}
	if !reserveWS(&rt.wsCalls, 1, rt.wsLimits.CallsTotal) {
		ws.calls.Add(-1)
		return false
	}
	return true
}

func (rt *Router) releaseWSCall(ws *wsConn) {
	ws.calls.Add(-1)
	rt.wsCalls.Add(-1)
}

var errWSBusy = errors.New("Unavailable", http.StatusServiceUnavailable, "WebSocket call capacity reached")

func (c *wsConn) reserveWrite(size int64, limits WebSocketLimits) bool {
	if !reserveWS(&c.writeBytes, size, limits.WriteBytesPerConnection) {
		return false
	}
	if c.totalWriteBytes != nil && !reserveWS(c.totalWriteBytes, size, limits.WriteBytesTotal) {
		c.writeBytes.Add(-size)
		return false
	}
	return true
}

func (c *wsConn) releaseWrite(size int64) {
	c.writeBytes.Add(-size)
	if c.totalWriteBytes != nil {
		c.totalWriteBytes.Add(-size)
	}
}

func (rt *Router) dispatchWSJSON(ctx context.Context, ws *wsConn, raw map[string]json.RawMessage) {
	rt.dispatchWSJSONOwned(ctx, ws, raw, receiveLease{})
}

func (rt *Router) dispatchWSJSONOwned(ctx context.Context, ws *wsConn, raw map[string]json.RawMessage, lease receiveLease) {
	id, valid := parseWSJSONSequence(raw["$id"])
	if !valid {
		lease.release()
		return
	}
	if !rt.acquireWSCall(ws) {
		lease.release()
		rt.writeWSJSONError(ctx, ws, id, errWSBusy)
		return
	}
	go func() {
		defer lease.release()
		defer rt.releaseWSCall(ws)
		rt.handleWSJSON(ctx, ws, raw)
	}()
}

func (rt *Router) dispatchWSBinary(ctx context.Context, ws *wsConn, data []byte) {
	rt.dispatchWSBinaryOwned(ctx, ws, data, receiveLease{})
}

func (rt *Router) dispatchWSBinaryOwned(ctx context.Context, ws *wsConn, data []byte, lease receiveLease) {
	id, n := codec.ReadVarint(data, 1)
	if n <= 0 {
		lease.release()
		return
	}
	if !rt.acquireWSCall(ws) {
		lease.release()
		rt.writeWSBinaryError(ctx, ws, id, errWSBusy)
		return
	}
	go func() {
		defer lease.release()
		defer rt.releaseWSCall(ws)
		rt.handleWSBinary(ctx, ws, data)
	}()
}
