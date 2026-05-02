package api

import (
	"context"
	"fmt"
	"sync"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

// StreamHub manages all WebSocket stream subscriptions.
// Central dispatch: event triggers → iterate subs → matcher → push chan.
// Thread-safe for concurrent subscribe/unsubscribe/dispatch.
type StreamHub struct {
	mu   sync.RWMutex
	subs map[string][]*StreamSub // apiName → subscribers
}

// StreamSub represents one client's subscription to a @stream API.
type StreamSub struct {
	Ch        chan []byte        // encoded data ready to write to WS
	Params    *StreamParams      // subscription parameters (roomId, etc.)
	Identity  any                // authenticated user (nil if no @auth)
	FieldMask []byte             // which fields the client wants
	cancel    context.CancelFunc // cancel this sub's write pump
}

// StreamParams holds subscription parameters from the client.
type StreamParams struct {
	values map[string]any
}

// Int returns an int parameter.
func (p *StreamParams) Int(name string) int64 {
	if p == nil || p.values == nil {
		return 0
	}
	switch v := p.values[name].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return 0
}

// String returns a string parameter.
func (p *StreamParams) String(name string) string {
	if p == nil || p.values == nil {
		return ""
	}
	if v, ok := p.values[name].(string); ok {
		return v
	}
	return ""
}

// Get returns a raw parameter value.
func (p *StreamParams) Get(name string) any {
	if p == nil || p.values == nil {
		return nil
	}
	return p.values[name]
}

// StreamMatcher decides whether to push data to a subscriber.
// Return true to push, false to skip.
type StreamMatcher func(data []byte, params *StreamParams, identity any) bool

// Stream is passed to @native @stream handlers without event source.
// The handler calls Send() to push data to the client.
type Stream struct {
	sub *StreamSub
	ctx context.Context
}

// ErrStreamFull is returned when the subscriber channel is full (client too slow).
var ErrStreamFull = fmt.Errorf("stream: subscriber channel full, client too slow")

// Send pushes encoded data to the client.
// Returns ErrStreamFull immediately if channel is full — caller should stop pushing.
func (s *Stream) Send(data []byte) error {
	select {
	case s.sub.Ch <- data:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	default:
		return ErrStreamFull
	}
}

// Context returns the stream's context (cancelled when client disconnects).
func (s *Stream) Context() context.Context {
	return s.ctx
}

// IdentityClaims provides field access for stream matcher identity.
// The identity stored in StreamSub is a Claims-like object with Int/String methods.
type IdentityClaims interface {
	ID() int64
	Int(key string) int64
	String(key string) string
}

// IdentityID extracts int64 ID from identity (nil-safe).
func IdentityID(identity any) int64 {
	if c, ok := identity.(IdentityClaims); ok {
		return c.ID()
	}
	return 0
}

// IdentityInt extracts an int64 claim from identity by key (nil-safe).
func IdentityInt(identity any, key string) int64 {
	if c, ok := identity.(IdentityClaims); ok {
		return c.Int(key)
	}
	return 0
}

// IdentityString extracts a string claim from identity by key (nil-safe).
func IdentityString(identity any, key string) string {
	if c, ok := identity.(IdentityClaims); ok {
		return c.String(key)
	}
	return ""
}

// NewStreamHub creates an empty hub.
func NewStreamHub() *StreamHub {
	return &StreamHub{
		subs: make(map[string][]*StreamSub),
	}
}

// Subscribe adds a subscriber for an API.
// The provided cancel function is called when the subscriber is too slow (channel full).
// Typically this cancels the subscriber's context, causing WritePump to exit.
func (h *StreamHub) Subscribe(apiName string, params map[string]any, identity any, fieldMask []byte, cancel context.CancelFunc) *StreamSub {
	sub := &StreamSub{
		Ch:        make(chan []byte, 64), // buffered to absorb bursts
		Params:    &StreamParams{values: params},
		Identity:  identity,
		FieldMask: fieldMask,
		cancel:    cancel,
	}

	h.mu.Lock()
	h.subs[apiName] = append(h.subs[apiName], sub)
	h.mu.Unlock()

	return sub
}

// Unsubscribe removes a subscriber.
func (h *StreamHub) Unsubscribe(apiName string, sub *StreamSub) {
	h.mu.Lock()
	defer h.mu.Unlock()

	subs := h.subs[apiName]
	for i, s := range subs {
		if s == sub {
			h.subs[apiName] = append(subs[:i], subs[i+1:]...)
			close(sub.Ch)
			if sub.cancel != nil {
				sub.cancel()
			}
			return
		}
	}
}

// Dispatch sends data to all matching subscribers of an API.
// Dispatch sends data to all matching subscribers of an API.
// matcher can be nil (broadcast to all).
// encodeFn encodes the raw event data per subscriber's fieldMask.
// Holds RLock during send — safe because sends are non-blocking (select/default).
func (h *StreamHub) Dispatch(apiName string, rawData any, matcher StreamMatcher, encodeFn func(data any, fieldMask []byte) []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	subs := h.subs[apiName]
	if len(subs) == 0 {
		return
	}

	var defaultEncoded []byte

	for _, sub := range subs {
		if matcher != nil {
			if defaultEncoded == nil {
				defaultEncoded = encodeFn(rawData, nil)
			}
			if !matcher(defaultEncoded, sub.Params, sub.Identity) {
				continue
			}
		}

		var encoded []byte
		if len(sub.FieldMask) == 0 {
			if defaultEncoded == nil {
				defaultEncoded = encodeFn(rawData, nil)
			}
			encoded = defaultEncoded
		} else {
			encoded = encodeFn(rawData, sub.FieldMask)
		}

		select {
		case sub.Ch <- encoded:
		default:
			// Client too slow — disconnect
			sub.cancel()
		}
	}
}

// DispatchEncoded sends pre-encoded data to all matching subscribers.
// Used when data is already Luxo binary (from event bus).
// Slow subscribers (channel full) are disconnected immediately.
func (h *StreamHub) DispatchEncoded(apiName string, encoded []byte, matcher StreamMatcher) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, sub := range h.subs[apiName] {
		if matcher != nil && !matcher(encoded, sub.Params, sub.Identity) {
			continue
		}
		select {
		case sub.Ch <- encoded:
		default:
			// Client too slow — disconnect by cancelling context
			sub.cancel()
		}
	}
}

// SubCount returns the number of active subscribers for an API.
func (h *StreamHub) SubCount(apiName string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs[apiName])
}

// WritePumpJSON runs the write pump for a JSON WS subscription.
// Reads from sub.Ch, wraps in {"$stream":"apiName","data":...}, writes to WS.
func WritePumpJSON(ctx context.Context, ws *wsConn, apiName string, sub *StreamSub) {
	buf := GetBuf()
	defer PutBuf(buf)

	for {
		select {
		case data, ok := <-sub.Ch:
			if !ok {
				return
			}
			buf.B = buf.B[:0]
			buf.AppendString(`{"$stream":`)
			buf.AppendJSONString(apiName)
			buf.AppendString(`,"data":`)
			buf.B = append(buf.B, data...)
			buf.AppendByte('}')
			if err := ws.writeText(ctx, buf.B); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// WritePumpBinary runs the write pump for a binary WS subscription.
// Reads from sub.Ch, wraps in [0xFD][API ID varint][payload], writes to WS.
func WritePumpBinary(ctx context.Context, ws *wsConn, apiID int, sub *StreamSub) {
	for {
		select {
		case data, ok := <-sub.Ch:
			if !ok {
				return
			}
			buf := GetBuf()
			buf.B = append(buf.B, 0xFD) // stream push marker
			buf.B = codec.AppendVarint(buf.B, uint64(apiID))
			buf.B = append(buf.B, data...)
			err := ws.writeBinary(ctx, buf.B)
			PutBuf(buf)
			if err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}
