package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

type testEvent struct {
	UserID int    `json:"userId"`
	Name   string `json:"name"`
}

func (p testEvent) SnapshotEvent() (Snapshot, error) {
	return immutablePayload(p).SnapshotEvent()
}

type capturedTypedBus struct {
	Bus
	handler Handler
}

func (b *capturedTypedBus) On(_ string, handler Handler) error {
	b.handler = handler
	return nil
}

func (b *capturedTypedBus) OnQueue(_ string, _ string, handler Handler) error {
	b.handler = handler
	return nil
}

func TestTypedSubscriptionRejectsMissingCallbacks(t *testing.T) {
	for _, queue := range []bool{false, true} {
		for _, missing := range []string{"handler", "decoder", "both"} {
			t.Run(fmt.Sprintf("queue=%t/%s", queue, missing), func(t *testing.T) {
				bus := NewChanBus(1)
				t.Cleanup(func() { _ = bus.Close() })
				decode := func([]byte, any) error { return nil }
				handle := func(context.Context, testEvent) error { return nil }
				want := "event: decoder is required"
				if missing != "decoder" {
					handle, want = nil, ErrHandlerRequired.Error()
				}
				if missing != "handler" {
					decode = nil
				}
				var err error
				if queue {
					err = OnQueueDecode(bus, "probe", "group", decode, handle)
				} else {
					err = OnDecode(bus, "probe", decode, handle)
				}
				if err == nil || err.Error() != want {
					t.Fatalf("invalid typed subscription: got %v, want %s", err, want)
				}
				if len(bus.channels) != 0 || len(bus.subs) != 0 {
					t.Fatal("rejected callbacks created subscription resources")
				}
			})
		}
	}
}

func TestTypedSubscriptionValidatesBeforeResolvingBackend(t *testing.T) {
	for _, queue := range []bool{false, true} {
		for _, missing := range []string{"handler", "decoder", "both"} {
			t.Run(fmt.Sprintf("queue=%t/%s", queue, missing), func(t *testing.T) {
				decode := func([]byte, any) error { return nil }
				handle := func(context.Context, testEvent) error { return nil }
				want := ErrDecoderRequired
				if missing != "decoder" {
					handle, want = nil, ErrHandlerRequired
				}
				if missing != "handler" {
					decode = nil
				}
				var err error
				if queue {
					err = OnQueueDecode[testEvent](nil, "probe", "", decode, handle)
				} else {
					err = OnDecode[testEvent](nil, "probe", decode, handle)
				}
				if !errors.Is(err, want) {
					t.Fatalf("got %v, want %v before resolving a nil backend", err, want)
				}
			})
		}
	}
}

func TestTypedDecodePreservesRegistrationRoute(t *testing.T) {
	decode := func([]byte, any) error { return nil }
	handle := func(context.Context, testEvent) error { return nil }
	failure := errors.New("subscription rejected")
	for _, group := range []string{"", "workers"} {
		bus := &valueRouteBus{failure: failure}
		if err := OnQueueDecode(bus, "probe", group, decode, handle); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		if bus.broadcast != 0 || bus.queue != 1 || bus.group != group {
			t.Fatalf("queue routing changed: %+v", bus)
		}
	}
	bus := &valueRouteBus{failure: failure}
	if err := OnDecode(bus, "probe", decode, handle); !errors.Is(err, failure) || bus.broadcast != 1 || bus.queue != 0 {
		t.Fatal(err, bus)
	}
}

func TestTypedBytesPayloadPrecedesWireDecode(t *testing.T) {
	for _, queue := range []bool{false, true} {
		bus := &capturedTypedBus{}
		decoded, handled := 0, 0
		decode := func([]byte, any) error { decoded++; return nil }
		handle := func(_ context.Context, value []byte) error { handled += int(value[0]); return nil }
		var err error
		if queue {
			err = OnQueueDecode(bus, "probe", "", decode, handle)
		} else {
			err = OnDecode(bus, "probe", decode, handle)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := bus.handler(t.Context(), []byte{7}); err != nil || decoded != 0 || handled != 7 {
			t.Fatal(err, decoded, handled)
		}
	}
}

func TestTypedDecoderRetainedTargetsRemainIndependent(t *testing.T) {
	bus := &capturedTypedBus{}
	var retained []*testEvent
	decode := func(raw []byte, target any) error {
		value := target.(*testEvent)
		value.UserID = int(raw[0])
		retained = append(retained, value)
		return nil
	}
	if err := OnDecode(bus, "probe", decode, func(context.Context, testEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, value := range []byte{1, 2} {
		if err := bus.handler(t.Context(), []byte{value}); err != nil {
			t.Fatal(err)
		}
	}
	if len(retained) != 2 || retained[0] == retained[1] || retained[0].UserID != 1 || retained[1].UserID != 2 {
		t.Fatal("decoder targets were reused or changed after delivery", retained)
	}
}

func TestTypedHandlersRejectUnexpectedPayload(t *testing.T) {
	for _, queue := range []bool{false, true} {
		bus := &capturedTypedBus{}
		decoded, handled := 0, 0
		decode := func([]byte, any) error { decoded++; return nil }
		handle := func(context.Context, testEvent) error { handled++; return nil }
		var err error
		if queue {
			err = OnQueueDecode(bus, "probe", "group", decode, handle)
		} else {
			err = OnDecode(bus, "probe", decode, handle)
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, payload := range []any{nil, 42, "wrong", (*testEvent)(nil)} {
			if err := bus.handler(t.Context(), payload); !errors.Is(err, ErrUnexpectedPayload) {
				t.Fatalf("queue=%t: incompatible payload returned %v", queue, err)
			}
		}
		if decoded != 0 || handled != 0 {
			t.Fatalf("invalid payload reached decoder/handler: %d/%d", decoded, handled)
		}
		if err := bus.handler(t.Context(), testEvent{}); err != nil {
			t.Fatal(err)
		}
		if decoded != 0 || handled != 1 {
			t.Fatal("rejection affected the next valid event")
		}
	}
}

func BenchmarkTypedCallbackRegistration(b *testing.B) {
	for _, queue := range []bool{false, true} {
		b.Run(fmt.Sprintf("queue=%t", queue), func(b *testing.B) {
			bus := &capturedTypedBus{}
			decode := func([]byte, any) error { return nil }
			handle := func(context.Context, testEvent) error { return nil }
			b.ReportAllocs()
			for b.Loop() {
				var err error
				if queue {
					err = OnQueueDecode(bus, "probe", "group", decode, handle)
				} else {
					err = OnDecode(bus, "probe", decode, handle)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkTypedRegisteredDispatch(b *testing.B) {
	for _, wire := range []bool{false, true} {
		b.Run(fmt.Sprintf("wire=%t", wire), func(b *testing.B) {
			bus := &capturedTypedBus{}
			handled := 0
			decode := func(raw []byte, target any) error { target.(*testEvent).UserID = int(raw[0]); return nil }
			handle := func(_ context.Context, value testEvent) error { handled += value.UserID; return nil }
			if err := OnDecode(bus, "probe", decode, handle); err != nil {
				b.Fatal(err)
			}
			var payload any = testEvent{UserID: 42}
			if wire {
				payload = []byte{42}
			}
			ctx := b.Context()
			b.ReportAllocs()
			for b.Loop() {
				if err := bus.handler(ctx, payload); err != nil {
					b.Fatal(err)
				}
			}
			if handled != b.N*42 {
				b.Fatal("registered callback did not receive every event")
			}
		})
	}
}

func BenchmarkTypedInvalidRegistration(b *testing.B) {
	for _, queue := range []bool{false, true} {
		for _, missing := range []string{"handler", "decoder", "both"} {
			b.Run(fmt.Sprintf("queue=%t/%s", queue, missing), func(b *testing.B) {
				decode := func([]byte, any) error { return nil }
				handle := func(context.Context, testEvent) error { return nil }
				want := ErrDecoderRequired
				if missing != "decoder" {
					handle, want = nil, ErrHandlerRequired
				}
				if missing != "handler" {
					decode = nil
				}
				b.ReportAllocs()
				for b.Loop() {
					var err error
					if queue {
						err = OnQueueDecode[testEvent](nil, "probe", "", decode, handle)
					} else {
						err = OnDecode[testEvent](nil, "probe", decode, handle)
					}
					if err != want {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// The historical adapter only differs in its silent mismatched-type result.
// Both sides reuse preboxed payloads and exclude queues, wire decoding and I/O.
func BenchmarkTypedPayloadDispatch(b *testing.B) {
	for _, sample := range []struct {
		name     string
		payload  any
		rejected bool
	}{
		{"direct", testEvent{UserID: 42}, false},
		{"bytes", []byte{42}, false},
		{"mismatch", 42, true},
	} {
		for _, previous := range []bool{true, false} {
			b.Run(fmt.Sprintf("%s/previous=%t", sample.name, previous), func(b *testing.B) {
				count := 0
				decode := func(data []byte, target any) error { target.(*testEvent).UserID = int(data[0]); return nil }
				handle := func(_ context.Context, value testEvent) error { count += value.UserID; return nil }
				bus := &capturedTypedBus{}
				if err := OnDecode(bus, "probe", decode, handle); err != nil {
					b.Fatal(err)
				}
				if previous {
					bus.handler = typedHandlerBeforeRejection(decode, handle)
				}
				ctx := b.Context()
				b.ReportAllocs()
				for b.Loop() {
					err := bus.handler(ctx, sample.payload)
					if (err != nil) != (sample.rejected && !previous) {
						b.Fatal(err)
					}
				}
				if !sample.rejected && count != b.N*42 {
					b.Fatal("valid dispatch was skipped")
				}
			})
		}
	}
}

func typedHandlerBeforeRejection[T any](unmarshal func([]byte, any) error, handler func(context.Context, T) error) Handler {
	return func(ctx context.Context, raw any) error {
		switch value := raw.(type) {
		case T:
			return handler(ctx, value)
		case []byte:
			var target T
			if err := unmarshal(value, &target); err != nil {
				return err
			}
			return handler(ctx, target)
		}
		return nil
	}
}

func TestOnDecodeDirectPayload(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var got testEvent
	done := make(chan struct{})

	OnDecode(bus, "test.event", json.Unmarshal, func(ctx context.Context, payload testEvent) error {
		got = payload
		close(done)
		return nil
	})

	bus.Emit(context.Background(), "test.event", testEvent{UserID: 42, Name: "alice"})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}

	if got.UserID != 42 || got.Name != "alice" {
		t.Errorf("got %+v", got)
	}
}

func TestOnDecodeFromBytes(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var got testEvent
	done := make(chan struct{})

	OnDecode(bus, "test.bytes", json.Unmarshal, func(ctx context.Context, payload testEvent) error {
		got = payload
		close(done)
		return nil
	})

	// Simulate NATSBus by emitting []byte
	data, _ := json.Marshal(testEvent{UserID: 7, Name: "bob"})
	bus.Emit(context.Background(), "test.bytes", data)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}

	if got.UserID != 7 || got.Name != "bob" {
		t.Errorf("got %+v", got)
	}
}

func TestOnQueueDecodeDirectPayload(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var got testEvent
	done := make(chan struct{})

	OnQueueDecode(bus, "queue.test", "group1", json.Unmarshal, func(ctx context.Context, payload testEvent) error {
		got = payload
		close(done)
		return nil
	})

	bus.Emit(context.Background(), "queue.test", testEvent{UserID: 99, Name: "carol"})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}

	if got.UserID != 99 || got.Name != "carol" {
		t.Errorf("got %+v", got)
	}
}

func TestOnDecodeDoesNotDeliverUnknownType(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var count atomic.Int32
	OnDecode(bus, "typed.only", json.Unmarshal, func(ctx context.Context, payload testEvent) error {
		count.Add(1)
		return nil
	})

	// Emit a completely different type — handler should not fire
	bus.Emit(context.Background(), "typed.only", 12345)
	time.Sleep(50 * time.Millisecond)

	if count.Load() != 0 {
		t.Errorf("handler should not fire for wrong type, got %d calls", count.Load())
	}
}

func TestOnQueueDecodeFromBytes(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var got testEvent
	done := make(chan struct{})

	OnQueueDecode(bus, "queue.bytes", "group1", json.Unmarshal, func(ctx context.Context, payload testEvent) error {
		got = payload
		close(done)
		return nil
	})

	// Simulate NATSBus by emitting []byte
	data, _ := json.Marshal(testEvent{UserID: 7, Name: "bob"})
	bus.Emit(context.Background(), "queue.bytes", data)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}

	if got.UserID != 7 || got.Name != "bob" {
		t.Errorf("got %+v", got)
	}
}

func TestOnQueueDecodeDoesNotDeliverUnknownType(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var count atomic.Int32
	OnQueueDecode(bus, "queue.typed", "g1", json.Unmarshal, func(ctx context.Context, payload testEvent) error {
		count.Add(1)
		return nil
	})

	bus.Emit(context.Background(), "queue.typed", 12345)
	time.Sleep(50 * time.Millisecond)

	if count.Load() != 0 {
		t.Errorf("handler should not fire for wrong type, got %d", count.Load())
	}
}

func TestOnQueueDecodeInvalidJSON(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var count atomic.Int32
	OnQueueDecode(bus, "queue.bad", "g1", json.Unmarshal, func(ctx context.Context, payload testEvent) error {
		count.Add(1)
		return nil
	})

	bus.Emit(context.Background(), "queue.bad", []byte("{invalid"))
	time.Sleep(50 * time.Millisecond)

	if count.Load() != 0 {
		t.Errorf("handler should not fire for invalid JSON, got %d", count.Load())
	}
}

func TestOnDecodeInvalidJSON(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var count atomic.Int32
	OnDecode(bus, "bad.json", json.Unmarshal, func(ctx context.Context, payload testEvent) error {
		count.Add(1)
		return nil
	})

	// Emit invalid JSON bytes — handler should not fire
	bus.Emit(context.Background(), "bad.json", []byte("{invalid"))
	time.Sleep(50 * time.Millisecond)

	if count.Load() != 0 {
		t.Errorf("handler should not fire for invalid JSON, got %d calls", count.Load())
	}
}

func registerValueTest(bus Bus, queue bool, group string, decode func([]byte) (testEvent, error), handler func(context.Context, testEvent) error) error {
	if queue {
		return OnQueueValueDecode(bus, "probe", group, decode, handler)
	}
	return OnValueDecode(bus, "probe", decode, handler)
}

func TestValueDecodeRejectsMissingCallbacks(t *testing.T) {
	for _, queue := range []bool{false, true} {
		for _, missing := range []string{"handler", "decoder", "both"} {
			t.Run(fmt.Sprintf("queue=%t/%s", queue, missing), func(t *testing.T) {
				bus := NewChanBus(1)
				t.Cleanup(func() { _ = bus.Close() })
				decode := func([]byte) (testEvent, error) { return testEvent{}, nil }
				handle := func(context.Context, testEvent) error { return nil }
				want := ErrDecoderRequired
				if missing != "decoder" {
					handle, want = nil, ErrHandlerRequired
				}
				if missing != "handler" {
					decode = nil
				}
				if err := registerValueTest(bus, queue, "", decode, handle); !errors.Is(err, want) {
					t.Fatalf("got %v, want %v", err, want)
				}
				if len(bus.channels) != 0 || len(bus.subs) != 0 {
					t.Fatal("invalid callback created subscription resources")
				}
			})
		}
	}
}

func TestValueDecodeDispatch(t *testing.T) {
	for _, queue := range []bool{false, true} {
		t.Run(fmt.Sprintf("queue=%t", queue), func(t *testing.T) {
			bus := &capturedTypedBus{}
			decoded, handled := 0, 0
			decodeFailure, handlerFailure := errors.New("decode failed"), errors.New("handler failed")
			decode := func(raw []byte) (testEvent, error) {
				decoded++
				if len(raw) == 0 {
					return testEvent{}, decodeFailure
				}
				return testEvent{UserID: int(raw[0])}, nil
			}
			handle := func(_ context.Context, value testEvent) error {
				handled += value.UserID
				return handlerFailure
			}
			if err := registerValueTest(bus, queue, "group", decode, handle); err != nil {
				t.Fatal(err)
			}
			for _, raw := range []any{nil, 42, "wrong", (*testEvent)(nil)} {
				if err := bus.handler(t.Context(), raw); !errors.Is(err, ErrUnexpectedPayload) {
					t.Fatalf("bad payload returned %v", err)
				}
			}
			if decoded != 0 || handled != 0 {
				t.Fatal("bad payload reached decoder or handler")
			}
			if err := bus.handler(t.Context(), testEvent{UserID: 7}); !errors.Is(err, handlerFailure) || decoded != 0 || handled != 7 {
				t.Fatal(err, decoded, handled)
			}
			if err := bus.handler(t.Context(), []byte{9}); !errors.Is(err, handlerFailure) || decoded != 1 || handled != 16 {
				t.Fatal(err, decoded, handled)
			}
			if err := bus.handler(t.Context(), []byte{}); !errors.Is(err, decodeFailure) || decoded != 2 || handled != 16 {
				t.Fatal(err, decoded, handled)
			}
		})
	}
}

type valueRouteBus struct {
	Bus
	broadcast, queue int
	group            string
	failure          error
}

func (b *valueRouteBus) On(_ string, _ Handler) error {
	b.broadcast++
	return b.failure
}

func (b *valueRouteBus) OnQueue(_ string, group string, _ Handler) error {
	b.queue++
	b.group = group
	return b.failure
}

func TestValueDecodePreservesRegistrationRoute(t *testing.T) {
	decode := func([]byte) (testEvent, error) { return testEvent{}, nil }
	handle := func(context.Context, testEvent) error { return nil }
	failure := errors.New("subscription rejected")
	for _, group := range []string{"", "workers"} {
		bus := &valueRouteBus{failure: failure}
		if err := registerValueTest(bus, true, group, decode, handle); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		if bus.broadcast != 0 || bus.queue != 1 || bus.group != group {
			t.Fatalf("queue routing changed: %+v", bus)
		}
	}
	bus := &valueRouteBus{failure: failure}
	if err := registerValueTest(bus, false, "", decode, handle); !errors.Is(err, failure) || bus.broadcast != 1 || bus.queue != 0 {
		t.Fatal(err, bus)
	}
}

func BenchmarkValueCallbackRegistration(b *testing.B) {
	for _, queue := range []bool{false, true} {
		b.Run(fmt.Sprintf("queue=%t", queue), func(b *testing.B) {
			bus := &capturedTypedBus{}
			decode := func([]byte) (testEvent, error) { return testEvent{}, nil }
			handle := func(context.Context, testEvent) error { return nil }
			b.ReportAllocs()
			for b.Loop() {
				var err error
				if queue {
					err = OnQueueValueDecode(bus, "probe", "group", decode, handle)
				} else {
					err = OnValueDecode(bus, "probe", decode, handle)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkValueRegisteredDispatch(b *testing.B) {
	for _, wire := range []bool{false, true} {
		b.Run(fmt.Sprintf("wire=%t", wire), func(b *testing.B) {
			bus := &capturedTypedBus{}
			handled := 0
			decode := func(raw []byte) (testEvent, error) { return testEvent{UserID: int(raw[0])}, nil }
			handle := func(_ context.Context, value testEvent) error { handled += value.UserID; return nil }
			if err := OnValueDecode(bus, "probe", decode, handle); err != nil {
				b.Fatal(err)
			}
			var payload any = testEvent{UserID: 42}
			if wire {
				payload = []byte{42}
			}
			ctx := b.Context()
			b.ReportAllocs()
			for b.Loop() {
				if err := bus.handler(ctx, payload); err != nil {
					b.Fatal(err)
				}
			}
			if handled != b.N*42 {
				b.Fatal("registered callback did not receive every event")
			}
		})
	}
}
