package api

import (
	"context"
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

// Send pushes encoded data to the client.
func (s *Stream) Send(data []byte) error {
	select {
	case s.sub.Ch <- data:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	default:
		// chan full — client too slow, drop
		return nil
	}
}

// Context returns the stream's context (cancelled when client disconnects).
func (s *Stream) Context() context.Context {
	return s.ctx
}

// NewStreamHub creates an empty hub.
func NewStreamHub() *StreamHub {
	return &StreamHub{
		subs: make(map[string][]*StreamSub),
	}
}

// Subscribe adds a subscriber for an API.
// Returns the StreamSub (caller starts writePump goroutine).
func (h *StreamHub) Subscribe(apiName string, params map[string]any, identity any, fieldMask []byte) *StreamSub {
	sub := &StreamSub{
		Ch:        make(chan []byte, 64), // buffered to absorb bursts
		Params:    &StreamParams{values: params},
		Identity:  identity,
		FieldMask: fieldMask,
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
// matcher can be nil (broadcast to all).
// encodeFn encodes the raw event data per subscriber's fieldMask.
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
		}
	}
}

// DispatchEncoded sends pre-encoded data to all matching subscribers.
// Used when data is already Luxo binary (from event bus).
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
