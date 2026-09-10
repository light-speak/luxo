package event

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func natsURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("NATS_URL")
	if url == "" {
		url = "nats://localhost:4222"
	}
	return url
}

func TestNATSBusConnectFailure(t *testing.T) {
	_, err := NewNATSBus("nats://localhost:19999")
	if err == nil {
		t.Fatal("expected connection error")
	}
}

func TestNATSBusEmitOn(t *testing.T) {
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer bus.Close()

	var received atomic.Value
	done := make(chan struct{})

	bus.On("test.nats.emit", func(ctx context.Context, payload any) error {
		// NATSBus delivers []byte from wire
		received.Store(string(payload.([]byte)))
		close(done)
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	bus.Emit(context.Background(), "test.nats.emit", []byte{0x2a})

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
	}

	if got := received.Load().(string); got != "*" {
		t.Errorf("got %q, want raw byte payload", got)
	}
}

func TestNATSBusMultipleHandlers(t *testing.T) {
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer bus.Close()

	var count atomic.Int32
	var wg sync.WaitGroup
	wg.Add(2)

	bus.On("test.nats.multi", func(ctx context.Context, payload any) error {
		count.Add(1)
		wg.Done()
		return nil
	})
	bus.On("test.nats.multi", func(ctx context.Context, payload any) error {
		count.Add(1)
		wg.Done()
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	bus.Emit(context.Background(), "test.nats.multi", []byte("test"))

	wg.Wait()
	if count.Load() != 2 {
		t.Errorf("expected 2 handlers called, got %d", count.Load())
	}
}

func TestNATSBusRawPayload(t *testing.T) {
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer bus.Close()

	done := make(chan struct{})
	var got string

	bus.On("test.nats.typed", func(ctx context.Context, payload any) error {
		got = string(payload.([]byte))
		close(done)
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	bus.Emit(context.Background(), "test.nats.typed", []byte("order:1:99.99"))

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}

	if got != "order:1:99.99" {
		t.Errorf("got %q", got)
	}
}

func TestNATSBusHandlerPanicRecovery(t *testing.T) {
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer bus.Close()

	done := make(chan struct{})

	bus.On("test.nats.panic", func(ctx context.Context, payload any) error {
		panic("boom")
	})
	bus.On("test.nats.after.panic", func(ctx context.Context, payload any) error {
		close(done)
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	bus.Emit(context.Background(), "test.nats.panic", []byte("test"))
	time.Sleep(50 * time.Millisecond)
	bus.Emit(context.Background(), "test.nats.after.panic", []byte("test"))

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout — bus broken after panic")
	}
}

func TestNATSBusClose(t *testing.T) {
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}

	bus.On("test.nats.close", func(ctx context.Context, payload any) error { return nil })
	bus.Close()
	bus.Close() // double close should not panic
}

func TestNATSBusOnAfterClose(t *testing.T) {
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}

	bus.Close()

	err = bus.On("test.nats.closed", func(ctx context.Context, payload any) error { return nil })
	if err == nil {
		t.Fatal("expected error subscribing on closed bus")
	}
}

func TestChanBusDispatchChannelClose(t *testing.T) {
	bus := NewChanBus(1)

	block := make(chan struct{})
	bus.On("dispatch.close", func(ctx context.Context, payload any) error {
		<-block
		return nil
	})

	bus.Emit(context.Background(), "dispatch.close", "1")
	time.Sleep(10 * time.Millisecond)

	go func() {
		time.Sleep(10 * time.Millisecond)
		bus.Close()
	}()
	close(block)
	time.Sleep(50 * time.Millisecond)
}

func TestNATSBusOnQueue(t *testing.T) {
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer bus.Close()

	var received atomic.Value
	done := make(chan struct{})

	bus.OnQueue("test.nats.queue.basic", "user-service", func(ctx context.Context, payload any) error {
		received.Store(string(payload.([]byte)))
		close(done)
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	bus.Emit(context.Background(), "test.nats.queue.basic", []byte{1})

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}

	if received.Load().(string) == "" {
		t.Error("should have received data")
	}
}

func TestNATSBusOnQueueCompeting(t *testing.T) {
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer bus.Close()

	var count atomic.Int32
	var wg sync.WaitGroup

	n := 50
	wg.Add(n)

	bus.OnQueue("test.nats.queue.compete", "same-group", func(ctx context.Context, payload any) error {
		count.Add(1)
		wg.Done()
		return nil
	})
	bus.OnQueue("test.nats.queue.compete", "same-group", func(ctx context.Context, payload any) error {
		count.Add(1)
		wg.Done()
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	for range n {
		bus.Emit(context.Background(), "test.nats.queue.compete", []byte("test"))
	}

	wg.Wait()
	if count.Load() != int32(n) {
		t.Errorf("expected %d (one per message), got %d", n, count.Load())
	}
}

func TestNATSBusOnQueueCrossGroup(t *testing.T) {
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer bus.Close()

	var userCount, postCount atomic.Int32
	var wg sync.WaitGroup

	n := 20
	wg.Add(n * 2)

	bus.OnQueue("test.nats.queue.cross", "user-service", func(ctx context.Context, payload any) error {
		userCount.Add(1)
		wg.Done()
		return nil
	})
	bus.OnQueue("test.nats.queue.cross", "post-service", func(ctx context.Context, payload any) error {
		postCount.Add(1)
		wg.Done()
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	for range n {
		bus.Emit(context.Background(), "test.nats.queue.cross", []byte("test"))
	}

	wg.Wait()
	if userCount.Load() != int32(n) {
		t.Errorf("user-service: expected %d, got %d", n, userCount.Load())
	}
	if postCount.Load() != int32(n) {
		t.Errorf("post-service: expected %d, got %d", n, postCount.Load())
	}
}

func TestNATSBusOnQueueAfterClose(t *testing.T) {
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}

	bus.Close()

	err = bus.OnQueue("test.nats.queue.closed", "group", func(ctx context.Context, payload any) error { return nil })
	if err == nil {
		t.Fatal("expected error on closed bus")
	}
}

func TestNATSBusOnQueuePanicRecovery(t *testing.T) {
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer bus.Close()

	done := make(chan struct{})

	bus.OnQueue("test.nats.queue.panic", "group", func(ctx context.Context, payload any) error {
		panic("boom in queue handler")
	})
	bus.On("test.nats.queue.panic.after", func(ctx context.Context, payload any) error {
		close(done)
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	bus.Emit(context.Background(), "test.nats.queue.panic", []byte("test"))
	time.Sleep(50 * time.Millisecond)
	bus.Emit(context.Background(), "test.nats.queue.panic.after", []byte("test"))

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout — bus broken after queue handler panic")
	}
}

func TestNATSBusConcurrent(t *testing.T) {
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer bus.Close()

	var count atomic.Int32
	var wg sync.WaitGroup

	n := 50
	wg.Add(n)

	bus.On("test.nats.concurrent", func(ctx context.Context, payload any) error {
		count.Add(1)
		wg.Done()
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	for range n {
		go bus.Emit(context.Background(), "test.nats.concurrent", []byte("test"))
	}

	wg.Wait()
	if count.Load() != int32(n) {
		t.Errorf("expected %d, got %d", n, count.Load())
	}
}

func TestNATSBusEmitMarshalError(t *testing.T) {
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer bus.Close()

	err = bus.Emit(context.Background(), "test.nats.marshal.err", "implicit JSON is forbidden")
	if !errors.Is(err, ErrBinaryPayloadRequired) {
		t.Fatalf("error = %v, want ErrBinaryPayloadRequired", err)
	}
}

func TestNATSPayloadBytes(t *testing.T) {
	raw := []byte{0x01, 0x02}
	got, err := natsPayloadBytes(raw)
	if err != nil || len(got) != 2 || &got[0] != &raw[0] {
		t.Fatalf("raw payload = %v, %v", got, err)
	}

	got, err = natsPayloadBytes(&luxoPayload{data: []byte{0xde, 0xad}})
	if err != nil || len(got) != 2 || got[0] != 0xde || got[1] != 0xad {
		t.Fatalf("Luxo payload = %v, %v", got, err)
	}

	if _, err = natsPayloadBytes(struct{}{}); !errors.Is(err, ErrBinaryPayloadRequired) {
		t.Fatalf("unsupported payload error = %v", err)
	}
}

// luxoPayload implements codec.LuxoMarshaler for testing the binary path.
type luxoPayload struct {
	data []byte
}

func (p *luxoPayload) MarshalLuxo() []byte {
	return p.data
}

func TestNATSBusEmitLuxoMarshaler(t *testing.T) {
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer bus.Close()

	var received atomic.Value
	done := make(chan struct{})

	bus.On("test.nats.luxo.marshal", func(ctx context.Context, payload any) error {
		received.Store(payload.([]byte))
		close(done)
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	// Emit with LuxoMarshaler — should use binary path, not JSON
	err = bus.Emit(context.Background(), "test.nats.luxo.marshal", &luxoPayload{data: []byte{0xDE, 0xAD}})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}

	got := received.Load().([]byte)
	if len(got) != 2 || got[0] != 0xDE || got[1] != 0xAD {
		t.Errorf("got %v, want [0xDE 0xAD]", got)
	}
}
