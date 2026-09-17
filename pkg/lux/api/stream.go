package api

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
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
	Ch           chan []byte        // encoded data ready to write to WS
	Params       *StreamParams      // subscription parameters (roomId, etc.)
	Identity     any                // authenticated user (nil if no @auth)
	FieldMask    []byte             // which fields the client wants
	Binary       bool               // negotiated Luxo binary mode
	cancel       context.CancelFunc // cancel this sub's producer
	cancelWriter context.CancelFunc // optional WS writer; independent of completion
	sendMu       sync.RWMutex       // native Send vs. hub-owned channel close
	closed       bool               // protected by sendMu
	result       error              // terminal result, protected by sendMu
}

// StreamParams holds subscription parameters from the client.
type StreamParams struct {
	values map[string]any
	binary []byte
}

// Int returns an int parameter.
func (p *StreamParams) Int(name string) int64 {
	value, _ := p.LookupInt(name)
	return value
}

// LookupInt returns an integer parameter and whether its wire value is valid.
func (p *StreamParams) LookupInt(name string) (int64, bool) {
	if p == nil || p.values == nil {
		return 0, false
	}
	switch v := p.values[name].(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		converted := int64(v)
		return converted, float64(converted) == v
	}
	return 0, false
}

// String returns a string parameter.
func (p *StreamParams) String(name string) string {
	value, _ := p.LookupString(name)
	return value
}

// LookupString returns a string parameter and whether it exists with the expected type.
func (p *StreamParams) LookupString(name string) (string, bool) {
	if p == nil || p.values == nil {
		return "", false
	}
	if v, ok := p.values[name].(string); ok {
		return v, true
	}
	return "", false
}

// Float returns a floating-point parameter.
func (p *StreamParams) Float(name string) float64 {
	value, _ := p.LookupFloat(name)
	return value
}

// LookupFloat returns a floating-point parameter and whether its wire value is valid.
func (p *StreamParams) LookupFloat(name string) (float64, bool) {
	if p == nil || p.values == nil {
		return 0, false
	}
	switch v := p.values[name].(type) {
	case float64:
		return v, true
	case int64:
		return float64(v), true
	case int:
		return float64(v), true
	}
	return 0, false
}

// Boolean returns a boolean parameter.
func (p *StreamParams) Boolean(name string) bool {
	value, _ := p.LookupBoolean(name)
	return value
}

// LookupBoolean returns a boolean parameter and whether it exists with the expected type.
func (p *StreamParams) LookupBoolean(name string) (bool, bool) {
	if p == nil || p.values == nil {
		return false, false
	}
	value, ok := p.values[name].(bool)
	return value, ok
}

// Duration returns a duration parameter as canonical nanoseconds.
func (p *StreamParams) Duration(name string) int64 {
	value, _ := p.LookupDuration(name)
	return value
}

// LookupDuration returns canonical duration nanoseconds and whether the value is valid.
func (p *StreamParams) LookupDuration(name string) (int64, bool) {
	if p == nil || p.values == nil {
		return 0, false
	}
	switch value := p.values[name].(type) {
	case time.Duration:
		return int64(value), true
	case string:
		parsed, err := time.ParseDuration(value)
		return int64(parsed), err == nil
	default:
		return p.LookupInt(name)
	}
}

// UUID returns a UUID parameter in its allocation-free 16-byte wire form.
func (p *StreamParams) UUID(name string) [16]byte {
	value, _ := p.LookupUUID(name)
	return value
}

// LookupUUID returns a UUID parameter and whether the value is valid.
func (p *StreamParams) LookupUUID(name string) ([16]byte, bool) {
	if p == nil || p.values == nil {
		return [16]byte{}, false
	}
	switch value := p.values[name].(type) {
	case uuid.UUID:
		return [16]byte(value), true
	case [16]byte:
		return value, true
	case string:
		parsed, err := uuid.Parse(value)
		return [16]byte(parsed), err == nil
	default:
		return [16]byte{}, false
	}
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

// StreamSubscriberMatcher matches already-decoded event data against one subscriber.
// Generated Luxo matchers use this path so event fields are not decoded per subscriber.
type StreamSubscriberMatcher func(params *StreamParams, identity any) bool

// Stream is passed to @native @stream handlers without event source.
// The handler calls Send() to push data to the client.
type Stream struct {
	sub *StreamSub
	ctx context.Context
}

// TypedStream encodes generated @stream @native return values according to the
// subscriber's field selection and negotiated transport mode.
type TypedStream[T any] struct {
	stream *Stream
	encode func(value T, fieldMask []byte, binary bool) ([]byte, error)
}

// NewTypedStream binds a generated typed encoder to a raw stream transport.
func NewTypedStream[T any](stream *Stream, encode func(value T, fieldMask []byte, binary bool) ([]byte, error)) *TypedStream[T] {
	return &TypedStream[T]{stream: stream, encode: encode}
}

// Send encodes and pushes one typed stream value.
func (s *TypedStream[T]) Send(value T) error {
	data, err := s.encode(value, s.stream.sub.FieldMask, s.stream.sub.Binary)
	if err != nil {
		return err
	}
	return s.stream.Send(data)
}

// Context returns the subscription context.
func (s *TypedStream[T]) Context() context.Context {
	return s.stream.Context()
}

// InternalStream is a transport-neutral subscription used by RPC bridges.
type InternalStream struct {
	hub     *StreamHub
	apiName string
	sub     *StreamSub
	once    sync.Once
}

// Messages returns canonical Luxo binary stream payloads.
func (s *InternalStream) Messages() <-chan []byte {
	return s.sub.Ch
}

// Err returns the terminal result. Read Messages until closed before interpreting
// nil as successful completion; an active subscription also returns nil.
func (s *InternalStream) Err() error {
	s.sub.sendMu.RLock()
	defer s.sub.sendMu.RUnlock()
	return s.sub.result
}

// Close removes the subscription and cancels its handler context.
func (s *InternalStream) Close() {
	s.once.Do(func() {
		s.hub.Unsubscribe(s.apiName, s.sub)
	})
}

// ErrStreamFull is returned when the subscriber channel is full (client too slow).
var ErrStreamFull = fmt.Errorf("stream: subscriber channel full, client too slow")

// Send pushes encoded data to the client.
// Returns ErrStreamFull immediately if channel is full — caller should stop pushing.
func (s *Stream) Send(data []byte) error {
	s.sub.sendMu.RLock()
	defer s.sub.sendMu.RUnlock()
	if s.sub.closed {
		return context.Canceled
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	select {
	case s.sub.Ch <- data:
		return nil
	default:
		return ErrStreamFull
	}
}

// close is called once under the hub's write lock. Event dispatch already holds
// the hub read lock; only native Send needs the per-subscription lock as well.
// Lock order is hub.mu -> sendMu; Send never acquires hub.mu.
func (s *StreamSub) close(err error) {
	s.sendMu.Lock()
	s.closed = true
	s.result = err
	close(s.Ch)
	s.sendMu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

// Context returns the stream's context (cancelled when client disconnects).
func (s *Stream) Context() context.Context {
	return s.ctx
}

// FieldMask returns the subscriber's canonical recursive selection mask.
func (s *Stream) FieldMask() []byte {
	return s.sub.FieldMask
}

// Binary reports whether the subscriber negotiated Luxo binary transport.
func (s *Stream) Binary() bool {
	return s.sub.Binary
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

// MaxSubscribersPerAPI limits subscribers per API to prevent memory exhaustion.
const MaxSubscribersPerAPI = 10000

// Subscribe adds a subscriber for an API.
// The provided cancel function is called when the subscriber is too slow (channel full).
// Typically this cancels the subscriber's context, causing WritePump to exit.
// Returns nil if the per-API subscriber limit is reached.
func (h *StreamHub) Subscribe(apiName string, params map[string]any, identity any, fieldMask []byte, cancel context.CancelFunc) *StreamSub {
	return h.SubscribeMode(apiName, params, identity, fieldMask, false, cancel)
}

// SubscribeMode adds a subscriber and records its negotiated wire mode.
func (h *StreamHub) SubscribeMode(apiName string, params map[string]any, identity any, fieldMask []byte, binary bool, cancel context.CancelFunc) *StreamSub {
	return h.subscribeMode(apiName, params, identity, fieldMask, binary, cancel, nil)
}

func (h *StreamHub) subscribeMode(apiName string, params map[string]any, identity any, fieldMask []byte, binary bool, cancel, cancelWriter context.CancelFunc) *StreamSub {
	sub := &StreamSub{
		Ch:           make(chan []byte, 64), // buffered to absorb bursts
		Params:       &StreamParams{values: params},
		Identity:     identity,
		FieldMask:    fieldMask,
		Binary:       binary,
		cancel:       cancel,
		cancelWriter: cancelWriter,
	}

	h.mu.Lock()
	if len(h.subs[apiName]) >= MaxSubscribersPerAPI {
		h.mu.Unlock()
		return nil
	}
	h.subs[apiName] = append(h.subs[apiName], sub)
	h.mu.Unlock()

	return sub
}

// Unsubscribe removes a subscriber.
func (h *StreamHub) Unsubscribe(apiName string, sub *StreamSub) {
	// Stop the writer before closing Ch. Otherwise it can observe the closed
	// queue while its context is live and emit cancellation as an Internal end.
	// A completed producer is already absent from the hub, but its writer may
	// still be draining. Explicit cancellation must stop that writer as well.
	if sub != nil {
		sub.stopWriting()
	}
	h.finish(apiName, sub, context.Canceled)
}

func (s *StreamSub) stopWriting() {
	if s.cancelWriter != nil {
		s.cancelWriter()
	}
}

// finish publishes one result before closing the queue. Removal owns completion;
// a later handler return or cancellation cannot overwrite the first result.
func (h *StreamHub) finish(apiName string, sub *StreamSub, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	subs := h.subs[apiName]
	for i, s := range subs {
		if s == sub {
			kept := append(subs[:i], subs[i+1:]...)
			subs[len(subs)-1] = nil
			if len(kept) == 0 {
				delete(h.subs, apiName)
			} else {
				h.subs[apiName] = kept
			}
			sub.close(err)
			return
		}
	}
}

// Dispatch sends data to matching subscribers and propagates encoding failures.
// A matcher uses one default encoding; subscriber projections reuse the same dispatcher.
func (h *StreamHub) Dispatch(apiName string, rawData any, matcher StreamMatcher, encodeFn func(data any, fieldMask []byte) ([]byte, error)) error {
	var match StreamSubscriberMatcher
	var defaultEncoded []byte
	if matcher != nil {
		if h.SubCount(apiName) == 0 {
			return nil
		}
		var err error
		defaultEncoded, err = encodeFn(rawData, nil)
		if err != nil {
			return err
		}
		match = func(params *StreamParams, identity any) bool {
			return matcher(defaultEncoded, params, identity)
		}
	}
	return h.DispatchPreparedEvent(apiName, match, func(mask []byte, _ bool) ([]byte, error) {
		if matcher != nil && len(mask) == 0 {
			return defaultEncoded, nil
		}
		return encodeFn(rawData, mask)
	})
}

// DispatchEvent encodes the stream result for each negotiated transport mode.
// matchData is the already-encoded binary event used by legacy matchers.
func (h *StreamHub) DispatchEvent(apiName string, matchData []byte, matcher StreamMatcher, encodeFn func(fieldMask []byte, binary bool) ([]byte, error)) error {
	var match StreamSubscriberMatcher
	if matcher != nil {
		match = func(params *StreamParams, identity any) bool {
			return matcher(matchData, params, identity)
		}
	}
	return h.DispatchPreparedEvent(apiName, match, encodeFn)
}

// DispatchPreparedEvent sends an event whose generated matcher captures typed
// event fields. The event is decoded once by OnDecode, independent of subscriber count.
// An encoding failure stops this dispatch. Previously sent valid messages cannot be retracted.
func (h *StreamHub) DispatchPreparedEvent(apiName string, matcher StreamSubscriberMatcher, encodeFn func(fieldMask []byte, binary bool) ([]byte, error)) error {
	h.mu.RLock()

	var encodings streamEncodings
	var slow []*StreamSub
	for _, sub := range h.subs[apiName] {
		if matcher != nil && !matcher(sub.Params, sub.Identity) {
			continue
		}
		encoded, err := cachedStreamEncoding(&encodings, sub.FieldMask, sub.Binary, encodeFn)
		if err != nil {
			h.mu.RUnlock()
			h.removeSlowSubscribers(apiName, slow)
			return err
		}
		if !enqueueStreamData(sub, encoded) {
			slow = append(slow, sub)
		}
	}
	h.mu.RUnlock()
	h.removeSlowSubscribers(apiName, slow)
	return nil
}

type streamEncoding struct {
	mask   []byte
	binary bool
	data   []byte
}

// Keep the common single-projection cache on the stack. Additional masks or
// transport modes allocate only when the dispatch actually needs them.
type streamEncodings struct {
	first    streamEncoding
	hasFirst bool
	rest     []streamEncoding
}

func cachedStreamEncoding(encodings *streamEncodings, mask []byte, binary bool, encodeFn func([]byte, bool) ([]byte, error)) ([]byte, error) {
	if encodings.hasFirst && encodings.first.binary == binary && bytes.Equal(encodings.first.mask, mask) {
		return encodings.first.data, nil
	}
	for i := range encodings.rest {
		cached := &encodings.rest[i]
		if cached.binary == binary && bytes.Equal(cached.mask, mask) {
			return cached.data, nil
		}
	}
	data, err := encodeFn(mask, binary)
	if err != nil {
		return nil, err
	}
	entry := streamEncoding{mask: mask, binary: binary, data: data}
	if !encodings.hasFirst {
		encodings.first, encodings.hasFirst = entry, true
	} else {
		encodings.rest = append(encodings.rest, entry)
	}
	return data, nil
}

// DispatchEncoded sends pre-encoded data to all matching subscribers.
// Used when data is already Luxo binary (from event bus).
// Slow subscribers (channel full) are disconnected immediately.
func (h *StreamHub) DispatchEncoded(apiName string, encoded []byte, matcher StreamMatcher) {
	h.mu.RLock()

	var slow []*StreamSub
	for _, sub := range h.subs[apiName] {
		if matcher != nil && !matcher(encoded, sub.Params, sub.Identity) {
			continue
		}
		if !enqueueStreamData(sub, encoded) {
			slow = append(slow, sub)
		}
	}
	h.mu.RUnlock()
	h.removeSlowSubscribers(apiName, slow)
}

func enqueueStreamData(sub *StreamSub, data []byte) bool {
	select {
	case sub.Ch <- data:
		return true
	default:
		return false
	}
}

func (h *StreamHub) removeSlowSubscribers(apiName string, slow []*StreamSub) {
	if len(slow) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	slowSet := make(map[*StreamSub]struct{}, len(slow))
	for _, sub := range slow {
		slowSet[sub] = struct{}{}
	}
	subs := h.subs[apiName]
	kept := subs[:0]
	for _, sub := range subs {
		if _, remove := slowSet[sub]; remove {
			sub.stopWriting()
			sub.close(ErrStreamFull)
			continue
		}
		kept = append(kept, sub)
	}
	for i := len(kept); i < len(subs); i++ {
		subs[i] = nil
	}
	if len(kept) == 0 {
		delete(h.subs, apiName)
		return
	}
	h.subs[apiName] = kept
}

// SubCount returns the number of active subscribers for an API.
func (h *StreamHub) SubCount(apiName string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs[apiName])
}

// OpenInternalStream creates a binary subscription for an internal transport.
// It shares validation, matching, backpressure, and native handlers with WebSocket.
func (rt *Router) OpenInternalStream(ctx context.Context, req *Request) (*InternalStream, error) {
	if req == nil || !rt.isStreamAPI(req.API) {
		return nil, fmt.Errorf("api: %q is not a stream", requestAPIName(req))
	}
	streamCtx, cancel := context.WithCancel(ctx)
	params := binaryStreamParams(req)
	identity := rt.identityFromCtx(streamCtx)
	sub := rt.Streams.SubscribeMode(req.API, params, identity, req.FieldMask, true, cancel)
	if sub == nil {
		cancel()
		return nil, fmt.Errorf("api: stream %q reached subscriber limit", req.API)
	}
	sub.Params.binary = append([]byte(nil), req.BinaryParams()...)
	internal := &InternalStream{hub: rt.Streams, apiName: req.API, sub: sub}
	rt.startNativeStreamHandler(streamCtx, req.API, params, identity, sub)
	return internal, nil
}

func (rt *Router) startNativeStreamHandler(ctx context.Context, apiName string, params map[string]any, identity any, sub *StreamSub) {
	handler, ok := rt.streamHandlers[apiName]
	if !ok {
		return
	}
	stream := &Stream{sub: sub, ctx: ctx}
	go func() {
		err := handler(ctx, sub.Params, identity, stream)
		if err == nil {
			err = ctx.Err()
		}
		rt.Streams.finish(apiName, sub, err)
	}()
}

// Binary returns the canonical encoded parameter fields including the end marker.
func (p *StreamParams) Binary() []byte {
	if p == nil {
		return nil
	}
	return p.binary
}

func requestAPIName(req *Request) string {
	if req == nil {
		return ""
	}
	return req.API
}

func binaryStreamParams(req *Request) map[string]any {
	params := make(map[string]any, req.paramCount)
	for i := 0; i < req.paramCount && i < len(req.paramNames); i++ {
		if req.paramSet&(uint16(1)<<i) != 0 {
			params[req.paramNames[i]] = req.paramSlots[i]
		}
	}
	return params
}

// WritePumpJSON runs the write pump for a JSON WS subscription.
// Reads from sub.Ch, wraps in {"$stream":subscriptionID,"data":...}, writes to WS.
// Nil means the closed producer queue was observed; callers must still check ctx.
func WritePumpJSON(ctx context.Context, ws *wsConn, id uint64, sub *StreamSub) error {
	buf := GetBuf()
	defer PutBuf(buf)

	for {
		select {
		case data, ok := <-sub.Ch:
			if !ok {
				return nil
			}
			buf.B = buf.B[:0]
			buf.AppendString(`{"$stream":`)
			buf.B = strconv.AppendUint(buf.B, id, 10)
			buf.AppendString(`,"data":`)
			buf.B = append(buf.B, data...)
			buf.AppendByte('}')
			if err := ws.writeText(ctx, buf.B); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// WritePumpBinary runs the write pump for a binary WS subscription.
// Reads from sub.Ch, wraps in [0x12][subscription UInt64][payload], writes to WS.
// Nil means the closed producer queue was observed; callers must still check ctx.
func WritePumpBinary(ctx context.Context, ws *wsConn, id uint64, sub *StreamSub) error {
	for {
		select {
		case data, ok := <-sub.Ch:
			if !ok {
				return nil
			}
			buf := GetBuf()
			buf.B = append(buf.B, BinaryFrameStream)
			buf.B = codec.AppendVarint(buf.B, id)
			buf.B = append(buf.B, data...)
			err := ws.writeBinary(ctx, buf.B)
			PutBuf(buf)
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
