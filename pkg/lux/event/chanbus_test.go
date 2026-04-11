package event

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestChanBusEmitOn(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var received atomic.Value
	done := make(chan struct{})

	bus.On("user.created", func(ctx context.Context, payload []byte) {
		received.Store(string(payload))
		close(done)
	})

	bus.Emit(context.Background(), "user.created", []byte(`{"id":1}`))

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}

	if received.Load().(string) != `{"id":1}` {
		t.Errorf("got %v", received.Load())
	}
}

func TestChanBusMultipleHandlers(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var count atomic.Int32
	var wg sync.WaitGroup
	wg.Add(2)

	bus.On("order.placed", func(ctx context.Context, payload []byte) {
		count.Add(1)
		wg.Done()
	})
	bus.On("order.placed", func(ctx context.Context, payload []byte) {
		count.Add(1)
		wg.Done()
	})

	bus.Emit(context.Background(), "order.placed", []byte("{}"))

	wg.Wait()
	if count.Load() != 2 {
		t.Errorf("expected 2 handlers called, got %d", count.Load())
	}
}

func TestChanBusNoSubscribers(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	// Emit with no subscribers should not error
	err := bus.Emit(context.Background(), "nobody.listens", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestChanBusBufferFull(t *testing.T) {
	bus := NewChanBus(1) // tiny buffer
	defer bus.Close()

	// Block the handler
	block := make(chan struct{})
	bus.On("slow", func(ctx context.Context, payload []byte) {
		<-block
	})

	// Fill the buffer
	bus.Emit(context.Background(), "slow", []byte("1"))
	time.Sleep(10 * time.Millisecond) // let dispatcher pick up first msg

	// This should be buffered
	bus.Emit(context.Background(), "slow", []byte("2"))

	// This should be dropped (buffer full + handler blocked)
	bus.Emit(context.Background(), "slow", []byte("3"))

	close(block)
	// No panic, no block — test passes
}

func TestChanBusClose(t *testing.T) {
	bus := NewChanBus(10)

	bus.On("test", func(ctx context.Context, payload []byte) {})
	bus.Close()
	bus.Close() // double close should not panic

	// Emit after close should not panic
	bus.Emit(context.Background(), "test", []byte("{}"))
}

func TestChanBusTypedPayload(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	type OrderEvent struct {
		OrderID int     `json:"orderId"`
		UserID  int     `json:"userId"`
		Total   float64 `json:"total"`
	}

	done := make(chan struct{})
	var got OrderEvent

	bus.On("order.created", func(ctx context.Context, payload []byte) {
		json.Unmarshal(payload, &got)
		close(done)
	})

	event := OrderEvent{OrderID: 1, UserID: 42, Total: 99.99}
	data, _ := json.Marshal(event)
	bus.Emit(context.Background(), "order.created", data)

	<-done
	if got.OrderID != 1 || got.UserID != 42 {
		t.Errorf("got %+v", got)
	}
}

func TestChanBusConcurrent(t *testing.T) {
	bus := NewChanBus(1000)
	defer bus.Close()

	var count atomic.Int32
	var wg sync.WaitGroup

	bus.On("concurrent", func(ctx context.Context, payload []byte) {
		count.Add(1)
		wg.Done()
	})

	n := 100
	wg.Add(n)
	for range n {
		go bus.Emit(context.Background(), "concurrent", []byte("{}"))
	}

	wg.Wait()
	if count.Load() != int32(n) {
		t.Errorf("expected %d, got %d", n, count.Load())
	}
}

func TestChanBusHandlerPanicRecovery(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var count atomic.Int32
	done := make(chan struct{})

	bus.On("crash", func(ctx context.Context, payload []byte) {
		count.Add(1)
		panic("boom")
	})
	bus.On("crash", func(ctx context.Context, payload []byte) {
		count.Add(1)
		close(done)
	})

	bus.Emit(context.Background(), "crash", []byte("{}"))

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout — second handler not called after panic")
	}

	if count.Load() != 2 {
		t.Errorf("expected 2, got %d", count.Load())
	}
}

func TestSafeCall(t *testing.T) {
	safeCall(func(ctx context.Context, payload []byte) {
		panic("should not crash")
	}, context.Background(), nil)
}

func TestNewChanBusDefaultBufSize(t *testing.T) {
	bus := NewChanBus(0)
	if bus.bufSize != 256 {
		t.Errorf("default bufSize = %d, want 256", bus.bufSize)
	}
	bus.Close()

	bus2 := NewChanBus(-1)
	if bus2.bufSize != 256 {
		t.Errorf("negative bufSize = %d, want 256", bus2.bufSize)
	}
	bus2.Close()
}
