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
