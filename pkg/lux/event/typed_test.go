package event

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

type testEvent struct {
	UserID int    `json:"userId"`
	Name   string `json:"name"`
}

func TestOnDecodeDirectPayload(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var got testEvent
	done := make(chan struct{})

	OnDecode(bus, "test.event", json.Unmarshal, func(ctx context.Context, payload testEvent) {
		got = payload
		close(done)
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

	OnDecode(bus, "test.bytes", json.Unmarshal, func(ctx context.Context, payload testEvent) {
		got = payload
		close(done)
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

	OnQueueDecode(bus, "queue.test", "group1", json.Unmarshal, func(ctx context.Context, payload testEvent) {
		got = payload
		close(done)
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

func TestOnDecodeIgnoresUnknownType(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var count atomic.Int32
	OnDecode(bus, "typed.only", json.Unmarshal, func(ctx context.Context, payload testEvent) {
		count.Add(1)
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

	OnQueueDecode(bus, "queue.bytes", "group1", json.Unmarshal, func(ctx context.Context, payload testEvent) {
		got = payload
		close(done)
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

func TestOnQueueDecodeIgnoresUnknownType(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var count atomic.Int32
	OnQueueDecode(bus, "queue.typed", "g1", json.Unmarshal, func(ctx context.Context, payload testEvent) {
		count.Add(1)
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
	OnQueueDecode(bus, "queue.bad", "g1", json.Unmarshal, func(ctx context.Context, payload testEvent) {
		count.Add(1)
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
	OnDecode(bus, "bad.json", json.Unmarshal, func(ctx context.Context, payload testEvent) {
		count.Add(1)
	})

	// Emit invalid JSON bytes — handler should not fire
	bus.Emit(context.Background(), "bad.json", []byte("{invalid"))
	time.Sleep(50 * time.Millisecond)

	if count.Load() != 0 {
		t.Errorf("handler should not fire for invalid JSON, got %d calls", count.Load())
	}
}
