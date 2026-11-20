package event

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func BenchmarkChanBusSubscriptionLifecycle(b *testing.B) {
	handler := func(context.Context, any) error { return nil }
	b.ReportAllocs()
	for b.Loop() {
		bus := NewChanBus(1)
		if err := bus.On("events", handler); err != nil {
			b.Fatal(err)
		}
		bus.Close()
	}
}

func TestChanBusRejectsNilHandler(t *testing.T) {
	bus := NewChanBus(1)
	defer bus.Close()
	if err := bus.On("events", nil); err == nil {
		t.Error("nil broadcast handler registered")
	}
	if err := bus.OnQueue("events", "workers", nil); err == nil {
		t.Error("nil queue handler registered")
	}
	if len(bus.channels) != 0 || len(bus.subs) != 0 {
		t.Fatal("invalid handlers started dispatchers")
	}
}

func TestChanBusEmitOn(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var received atomic.Value
	done := make(chan struct{})

	bus.On("user.created", func(ctx context.Context, payload any) error {
		received.Store(payload)
		close(done)
		return nil
	})

	bus.Emit(context.Background(), "user.created", testEvent{UserID: 1})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}

	got := received.Load().(testEvent)
	if got.UserID != 1 {
		t.Errorf("got %v", got)
	}
}

func TestChanBusListenerAddedDuringDispatchReceivesQueuedEvent(t *testing.T) {
	bus := NewChanBus(2)
	waiting, release := context.WithCancel(t.Context())
	defer bus.Close()
	defer release()
	started := make(chan struct{})
	if err := bus.On("audit", func(_ context.Context, value any) error {
		if value == 1 {
			close(started)
			<-waiting.Done()
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.Emit(t.Context(), "audit", immutablePayload(1)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first listener did not start")
	}
	if err := bus.Emit(t.Context(), "audit", immutablePayload(2)); err != nil {
		t.Fatal(err)
	}
	received := make(chan int, 2)
	if err := bus.On("audit", func(_ context.Context, value any) error {
		received <- value.(int)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	release()
	if err := bus.Close(); err != nil {
		t.Fatal(err)
	}
	if len(received) != 1 || <-received != 2 {
		t.Fatal("new listener did not receive exactly the queued event")
	}
}

func TestChanBusGoexitDoesNotDiscardAcceptedEvents(t *testing.T) {
	bus := NewChanBus(2)
	started := make(chan struct{})
	release := make(chan struct{})
	var received atomic.Int32
	if err := bus.On("audit", func(_ context.Context, value any) error {
		if value == 1 {
			close(started)
			<-release
			runtime.Goexit()
		}
		received.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.Emit(context.Background(), "audit", immutablePayload(1)); err != nil {
		t.Fatal(err)
	}
	<-started
	err := bus.Emit(context.Background(), "audit", immutablePayload(2))
	close(release)
	bus.Close()
	if err != nil {
		t.Fatal(err)
	}
	if received.Load() != 1 {
		t.Fatalf("Close returned without draining accepted second event: received=%d", received.Load())
	}
}

func TestChanBusHandlerContextOutlivesEmitter(t *testing.T) {
	bus := NewChanBus(1)
	defer bus.Close()

	type contextKey struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "request-value"))
	cancel()
	result := make(chan struct {
		err   error
		value any
	}, 1)
	if err := bus.On("audit", func(ctx context.Context, _ any) error {
		result <- struct {
			err   error
			value any
		}{err: ctx.Err(), value: ctx.Value(contextKey{})}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.Emit(ctx, "audit", immutablePayload(0)); err != nil {
		t.Fatal(err)
	}
	got := <-result
	if got.err != nil {
		t.Fatalf("handler inherited emitter cancellation: %v", got.err)
	}
	if got.value != nil {
		t.Fatalf("handler inherited request-scoped value: %v", got.value)
	}
}

func TestChanBusMultipleHandlers(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var count atomic.Int32
	var wg sync.WaitGroup
	wg.Add(2)

	bus.On("order.placed", func(ctx context.Context, payload any) error {
		count.Add(1)
		wg.Done()
		return nil
	})
	bus.On("order.placed", func(ctx context.Context, payload any) error {
		count.Add(1)
		wg.Done()
		return nil
	})

	bus.Emit(context.Background(), "order.placed", immutablePayload("test"))

	wg.Wait()
	if count.Load() != 2 {
		t.Errorf("expected 2 handlers called, got %d", count.Load())
	}
}

func TestChanBusNoSubscribers(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	err := bus.Emit(context.Background(), "nobody.listens", immutablePayload("test"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestChanBusBufferFull(t *testing.T) {
	bus := NewChanBus(1)
	defer bus.Close()

	block := make(chan struct{})
	started := make(chan struct{})
	bus.On("slow", func(ctx context.Context, payload any) error {
		if payload == "1" {
			close(started)
		}
		<-block
		return nil
	})

	if err := bus.Emit(context.Background(), "slow", immutablePayload("1")); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := bus.Emit(context.Background(), "slow", immutablePayload("2")); err != nil {
		t.Fatal(err)
	}
	if err := bus.Emit(context.Background(), "slow", immutablePayload("3")); !errors.Is(err, ErrBufferFull) {
		t.Fatalf("full buffer error = %v, want %v", err, ErrBufferFull)
	}

	close(block)
}

func TestChanBusCloseDrainsAcceptedEvents(t *testing.T) {
	bus := NewChanBus(16)
	var handled atomic.Int32
	if err := bus.On("audit", func(context.Context, any) error {
		handled.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for i := range 16 {
		if err := bus.Emit(context.Background(), "audit", immutablePayload(i)); err != nil {
			t.Fatal(err)
		}
	}
	bus.Close()
	if got := handled.Load(); got != 16 {
		t.Fatalf("Close handled %d events, want 16", got)
	}
}

func TestChanBusClose(t *testing.T) {
	bus := NewChanBus(10)

	bus.On("test", func(ctx context.Context, payload any) error { return nil })
	bus.Close()
	bus.Close() // double close should not panic

	bus.Emit(context.Background(), "test", immutablePayload("after close"))
}

func TestChanBusCloseWaitsForInFlightEmit(t *testing.T) {
	bus := NewChanBus(1)
	handled := make(chan struct{}, 1)
	if err := bus.On("test", func(context.Context, any) error {
		handled <- struct{}{}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	bus.mu.RLock()
	ch := bus.channels["test"]
	closed := make(chan struct{})
	go func() {
		bus.Close()
		close(closed)
	}()
	deadline := time.Now().Add(time.Second)
	for !bus.closed.Load() {
		if time.Now().After(deadline) {
			bus.mu.RUnlock()
			t.Fatal("Close did not publish the closed state")
		}
		runtime.Gosched()
	}
	owned, err := immutablePayload("accepted").SnapshotEvent()
	if err != nil {
		t.Fatal(err)
	}
	ch <- owned
	bus.mu.RUnlock()

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not wait for the accepted event")
	}
	select {
	case <-handled:
	default:
		t.Fatal("accepted event was not handled before Close returned")
	}
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

	bus.On("order.created", func(ctx context.Context, payload any) error {
		got = payload.(OrderEvent) // direct type assertion, zero serialization
		close(done)
		return nil
	})

	bus.Emit(context.Background(), "order.created", immutablePayload(OrderEvent{OrderID: 1, UserID: 42, Total: 99.99}))

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

	bus.On("concurrent", func(ctx context.Context, payload any) error {
		count.Add(1)
		wg.Done()
		return nil
	})

	n := 100
	wg.Add(n)
	for range n {
		go bus.Emit(context.Background(), "concurrent", immutablePayload("test"))
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

	bus.On("crash", func(ctx context.Context, payload any) error {
		count.Add(1)
		panic("boom")
	})
	bus.On("crash", func(ctx context.Context, payload any) error {
		count.Add(1)
		close(done)
		return nil
	})

	bus.Emit(context.Background(), "crash", immutablePayload("test"))

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
	err := InvokeHandler(func(ctx context.Context, payload any) error {
		panic("should not crash")
	}, context.Background(), nil)
	if err == nil {
		t.Error("InvokeHandler should return panic-converted error")
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Errorf("error should mention panic: %v", err)
	}
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

	bus.OnQueue("user.created", "user-service", func(ctx context.Context, payload any) error {
		received.Store(payload)
		close(done)
		return nil
	})

	bus.Emit(context.Background(), "user.created", testEvent{UserID: 1})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}

	got := received.Load().(testEvent)
	if got.UserID != 1 {
		t.Errorf("got %v", got)
	}
}

func TestChanBusOnQueueMultipleGroups(t *testing.T) {
	bus := NewChanBus(10)
	defer bus.Close()

	var count atomic.Int32
	var wg sync.WaitGroup
	wg.Add(2)

	bus.OnQueue("order.created", "user-service", func(ctx context.Context, payload any) error {
		count.Add(1)
		wg.Done()
		return nil
	})
	bus.OnQueue("order.created", "post-service", func(ctx context.Context, payload any) error {
		count.Add(1)
		wg.Done()
		return nil
	})

	bus.Emit(context.Background(), "order.created", immutablePayload("test"))

	wg.Wait()
	if count.Load() != 2 {
		t.Errorf("expected 2 groups called, got %d", count.Load())
	}
}

func TestChanBusEmitAfterClose(t *testing.T) {
	bus := NewChanBus(10)
	bus.On("test", func(ctx context.Context, payload any) error { return nil })
	bus.Close()

	err := bus.Emit(context.Background(), "test", immutablePayload("data"))
	if err == nil {
		t.Fatal("Emit after Close should return error")
	}
}

func TestChanBusOnAfterClose(t *testing.T) {
	bus := NewChanBus(10)
	bus.Close()

	err := bus.On("test", func(ctx context.Context, payload any) error { return nil })
	if err == nil {
		t.Fatal("On after Close should return error")
	}
}

func BenchmarkChanBusEmit(b *testing.B) {
	bus := NewChanBus(1024)
	if err := bus.On("benchmark", func(context.Context, any) error { return nil }); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := bus.Close(); err != nil {
			b.Error(err)
		}
	})
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for {
			err := bus.Emit(context.Background(), "benchmark", immutablePayload("payload"))
			if err == nil {
				break
			}
			if !errors.Is(err, ErrBufferFull) {
				b.Fatal(err)
			}
			runtime.Gosched()
		}
	}
}
func TestChanBusDispatchChannelClose(t *testing.T) {
	bus := NewChanBus(1)

	block := make(chan struct{})
	bus.On("dispatch.close", func(ctx context.Context, payload any) error {
		<-block
		return nil
	})

	bus.Emit(context.Background(), "dispatch.close", immutablePayload("1"))
	time.Sleep(10 * time.Millisecond)

	go func() {
		time.Sleep(10 * time.Millisecond)
		bus.Close()
	}()
	close(block)
	time.Sleep(50 * time.Millisecond)
}

type hostilePanicPayload struct{ called *bool }

func (p hostilePanicPayload) String() string {
	*p.called = true
	panic("must not format panic payload")
}

func TestPanicRecoveryDoesNotInvokeFormatter(t *testing.T) {
	called := false
	err := InvokeHandler(func(context.Context, any) error {
		panic(hostilePanicPayload{&called})
	}, t.Context(), nil)
	if called || err == nil {
		t.Fatal("panic recovery invoked a user formatter")
	}
}

func TestChanBusGoexitContinuesListenersWithOwnedSnapshots(t *testing.T) {
	bus := NewChanBus(64)
	var second, third atomic.Int32
	if err := bus.On("audit", func(_ context.Context, value any) error {
		value.([]byte)[0] = 99
		runtime.Goexit()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.On("audit", func(_ context.Context, value any) error {
		data := value.([]byte)
		if data[0] != 7 {
			t.Errorf("second listener received mutated data: %v", data)
		}
		data[0] = 88
		second.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.On("audit", func(_ context.Context, value any) error {
		if value.([]byte)[0] != 7 {
			t.Errorf("final listener received mutated data: %v", value)
		}
		third.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for range 64 {
		if err := bus.Emit(t.Context(), "audit", []byte{7}); err != nil {
			t.Fatal(err)
		}
	}
	bus.Close()
	if second.Load() != 64 || third.Load() != 64 {
		t.Fatalf("accepted dispatch lost listeners: second=%d third=%d", second.Load(), third.Load())
	}
}

func BenchmarkChanBusDispatchRoundTrip(b *testing.B) {
	for _, tc := range []struct {
		name      string
		listeners int
	}{{"one", 1}, {"eight", 8}} {
		b.Run(tc.name, func(b *testing.B) {
			bus := NewChanBus(1)
			done := make(chan struct{}, 1)
			for i := range tc.listeners {
				handler := func(context.Context, any) error { return nil }
				if i+1 == tc.listeners {
					handler = func(context.Context, any) error { done <- struct{}{}; return nil }
				}
				if err := bus.On("events", handler); err != nil {
					b.Fatal(err)
				}
			}
			b.Cleanup(func() {
				if err := bus.Close(); err != nil {
					b.Error(err)
				}
			})
			payload := immutablePayload("payload")
			b.ReportAllocs()
			for b.Loop() {
				if err := bus.Emit(b.Context(), "events", payload); err != nil {
					b.Fatal(err)
				}
				<-done
			}
		})
	}
}
