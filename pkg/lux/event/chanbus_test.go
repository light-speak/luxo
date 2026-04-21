package event

import (
	"context"
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

	bus.On("user.created", func(ctx context.Context, payload any) {
		received.Store(payload)
		close(done)
	})

	bus.Emit(context.Background(), "user.created", map[string]any{"id": 1})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}

	got := received.Load().(map[string]any)
	if got["id"] != 1 {
		t.Errorf("got %v", got)
	}
}

func TestChanBusMultipleHandlers(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var count atomic.Int32
	var wg sync.WaitGroup
	wg.Add(2)

	bus.On("order.placed", func(ctx context.Context, payload any) {
		count.Add(1)
		wg.Done()
	})
	bus.On("order.placed", func(ctx context.Context, payload any) {
		count.Add(1)
		wg.Done()
	})

	bus.Emit(context.Background(), "order.placed", "test")

	wg.Wait()
	if count.Load() != 2 {
		t.Errorf("expected 2 handlers called, got %d", count.Load())
	}
}

func TestChanBusNoSubscribers(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	err := bus.Emit(context.Background(), "nobody.listens", "test")
	if err != nil {
		t.Fatal(err)
	}
}

func TestChanBusBufferFull(t *testing.T) {
	bus := NewChanBus(1)
	defer bus.Close()

	block := make(chan struct{})
	bus.On("slow", func(ctx context.Context, payload any) {
		<-block
	})

	bus.Emit(context.Background(), "slow", "1")
	time.Sleep(10 * time.Millisecond)

	bus.Emit(context.Background(), "slow", "2")
	bus.Emit(context.Background(), "slow", "3")

	close(block)
}

func TestChanBusClose(t *testing.T) {
	bus := NewChanBus(10)

	bus.On("test", func(ctx context.Context, payload any) {})
	bus.Close()
	bus.Close() // double close should not panic

	bus.Emit(context.Background(), "test", "after close")
}

func TestChanBusTypedPayload(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	type OrderEvent struct {
		OrderID int
		UserID  int
		Total   float64
	}

	done := make(chan struct{})
	var got OrderEvent

	bus.On("order.created", func(ctx context.Context, payload any) {
		got = payload.(OrderEvent) // direct type assertion, zero serialization
		close(done)
	})

	bus.Emit(context.Background(), "order.created", OrderEvent{OrderID: 1, UserID: 42, Total: 99.99})

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

	bus.On("concurrent", func(ctx context.Context, payload any) {
		count.Add(1)
		wg.Done()
	})

	n := 100
	wg.Add(n)
	for range n {
		go bus.Emit(context.Background(), "concurrent", "test")
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

	bus.On("crash", func(ctx context.Context, payload any) {
		count.Add(1)
		panic("boom")
	})
	bus.On("crash", func(ctx context.Context, payload any) {
		count.Add(1)
		close(done)
	})

	bus.Emit(context.Background(), "crash", "test")

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
	safeCall(func(ctx context.Context, payload any) {
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

func TestChanBusOnQueue(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var received atomic.Value
	done := make(chan struct{})

	bus.OnQueue("user.created", "user-service", func(ctx context.Context, payload any) {
		received.Store(payload)
		close(done)
	})

	bus.Emit(context.Background(), "user.created", map[string]any{"id": 1})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}

	got := received.Load().(map[string]any)
	if got["id"] != 1 {
		t.Errorf("got %v", got)
	}
}

func TestChanBusOnQueueMultipleGroups(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var count atomic.Int32
	var wg sync.WaitGroup
	wg.Add(2)

	bus.OnQueue("order.created", "user-service", func(ctx context.Context, payload any) {
		count.Add(1)
		wg.Done()
	})
	bus.OnQueue("order.created", "post-service", func(ctx context.Context, payload any) {
		count.Add(1)
		wg.Done()
	})

	bus.Emit(context.Background(), "order.created", "test")

	wg.Wait()
	if count.Load() != 2 {
		t.Errorf("expected 2 groups called, got %d", count.Load())
	}
}

func TestChanBusEmitAfterClose(t *testing.T) {
	bus := NewChanBus(10)
	bus.On("test", func(ctx context.Context, payload any) {})
	bus.Close()

	err := bus.Emit(context.Background(), "test", "data")
	if err == nil {
		t.Fatal("Emit after Close should return error")
	}
}

func TestChanBusOnAfterClose(t *testing.T) {
	bus := NewChanBus(10)
	bus.Close()

	err := bus.On("test", func(ctx context.Context, payload any) {})
	if err == nil {
		t.Fatal("On after Close should return error")
	}
}
