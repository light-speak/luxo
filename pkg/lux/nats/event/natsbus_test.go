package event

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/nats-io/nats.go"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
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

func TestNATSBusNilHandlerRejectedBeforeConnection(t *testing.T) {
	bus := &NATSBus{}
	for _, err := range []error{bus.On("events", nil), bus.OnQueue("events", "workers", nil)} {
		if err == nil || err.Error() != "event: handler is required" {
			t.Errorf("handler validation did not precede connection access: %v", err)
		}
	}
	if len(bus.subs) != 0 {
		t.Fatal("invalid handler registered a subscription")
	}
}

func TestNATSBusGoexitPreservesSubscription(t *testing.T) {
	for _, queue := range []bool{false, true} {
		t.Run(fmt.Sprint(queue), func(t *testing.T) { testNATSBusGoexit(t, queue) })
	}
}

func testNATSBusGoexit(t *testing.T, queue bool) {
	if os.Getenv("NATS_URL") == "" {
		t.Skip("requires explicit isolated NATS_URL")
	}
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	exited := make(chan struct{})
	received := make(chan struct{}, 1)
	control := make(chan struct{}, 1)
	subscribe := func(handler func(context.Context, any) error) error {
		if queue {
			return bus.OnQueue("audit.goexit", "audit-workers", handler)
		}
		return bus.On("audit.goexit", handler)
	}
	if err := subscribe(func(_ context.Context, payload any) error {
		if string(payload.([]byte)) == "first" {
			defer close(exited)
			runtime.Goexit()
		}
		received <- struct{}{}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.On("audit.control", func(context.Context, any) error { control <- struct{}{}; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := bus.Emit(t.Context(), "audit.goexit", []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := bus.conn.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("first handler not invoked")
	}
	if err := bus.Emit(t.Context(), "audit.goexit", []byte("second")); err != nil {
		t.Fatal(err)
	}
	if err := bus.Emit(t.Context(), "audit.control", []byte("control")); err != nil {
		t.Fatal(err)
	}
	if err := bus.conn.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case <-control:
	case <-time.After(time.Second):
		t.Fatal("healthy subscription stopped")
	}
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("Goexit permanently stopped event subscription")
	}
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

type failingLuxoPayload struct{ err error }

func (p failingLuxoPayload) MarshalLuxo() ([]byte, error) {
	return []byte{1, 2}, p.err
}

func TestNATSBusStopsBeforePublishOnEncodingError(t *testing.T) {
	failure := errors.New("payload encoding failed")
	payload := failingLuxoPayload{err: failure}
	data, err := natsPayloadBytes(payload)
	if !errors.Is(err, failure) || data != nil {
		t.Fatalf("partial payload escaped: %x, %v", data, err)
	}
	// A nil connection is intentional: encoding must fail before Publish.
	bus := &NATSBus{}
	if err := bus.Emit(context.Background(), "invalid", payload); !errors.Is(err, failure) {
		t.Fatalf("encoding error was lost: %v", err)
	}
}

// luxoPayload implements codec.LuxoMarshaler for testing the binary path.
type luxoPayload struct {
	data []byte
}

func (p *luxoPayload) MarshalLuxo() ([]byte, error) {
	return p.data, nil
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

func TestNATSBusCloseWaitsForAcceptedHandler(t *testing.T) {
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Fatal(err)
	}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); bus.Close() })
	if err := bus.On(t.Name(), func(context.Context, any) error {
		close(entered)
		<-release
		close(finished)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.conn.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	if err := bus.Emit(t.Context(), t.Name(), []byte{42}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not receive the accepted event")
	}
	closed := make(chan struct{})
	go func() { bus.Close(); close(closed) }()
	select {
	case <-closed:
		t.Error("Close returned while an accepted handler was still using its dependencies")
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not finish after releasing the handler")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("accepted handler did not finish")
	}
}

func TestNATSBusReportsHandlerFailures(t *testing.T) {
	for _, queue := range []bool{false, true} {
		for _, panics := range []bool{false, true} {
			name := "broadcast"
			if queue {
				name = "queue"
			}
			if panics {
				name += "-panic"
			} else {
				name += "-error"
			}
			t.Run(name, func(t *testing.T) { checkNATSHandlerDiagnostic(t, queue, panics) })
		}
	}
}

func checkNATSHandlerDiagnostic(t *testing.T, queue, panics bool) {
	t.Helper()
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = writer
	defer func() { os.Stderr = previous; writer.Close(); reader.Close() }()
	done := make(chan struct{})
	handler := func(_ context.Context, payload any) error {
		if payload.([]byte)[0] == 2 {
			close(done)
			return nil
		}
		if panics {
			panic("diagnostic-probe")
		}
		return errors.New("diagnostic-probe")
	}
	subject := strings.ReplaceAll(t.Name(), "/", ".")
	if queue {
		err = bus.OnQueue(subject, "audit", handler)
	} else {
		err = bus.On(subject, handler)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = bus.conn.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []byte{1, 2} {
		if err = bus.Emit(t.Context(), subject, []byte{marker}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler failure stopped delivery")
	}
	if err := bus.Close(); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	text := string(output)
	if strings.Count(text, "diagnostic-probe") != 1 || !strings.Contains(text, subject) {
		t.Fatalf("handler failure must be reported exactly once with its subject: %q", text)
	}
}

func TestNATSBusTLSURLRejectsPlainPeer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	observed := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			observed <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		_, err = io.WriteString(conn, "INFO {\"server_id\":\"audit\",\"version\":\"2.10.0\",\"proto\":1,\"max_payload\":1048576,\"tls_required\":false}\r\n")
		if err != nil {
			observed <- err
			return
		}
		var first [1]byte
		n, err := conn.Read(first[:])
		if n == 0 && errors.Is(err, io.EOF) {
			observed <- nil
			return
		}
		if n == 1 && first[0] == 0x16 {
			observed <- nil
			return
		}
		observed <- fmt.Errorf("TLS URL emitted non-TLS bytes or did not terminate: bytes=%x err=%v", first[:n], err)
	}()
	bus, err := NewNATSBus("tls://" + listener.Addr().String())
	if bus != nil {
		bus.Close()
	}
	if err == nil {
		t.Fatal("plaintext peer accepted for TLS URL")
	}
	select {
	case err := <-observed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("TLS probe did not finish")
	}
}

func TestNATSBusGoexitDuringDrainKeepsBufferedEvents(t *testing.T) {
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); bus.Close() })
	var received atomic.Int32
	subject := fmt.Sprintf("audit.drain.%d", time.Now().UnixNano())
	if err := bus.On(subject, func(_ context.Context, value any) error {
		if string(value.([]byte)) == "exit" {
			close(entered)
			<-release
			runtime.Goexit()
		}
		received.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.conn.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	if err := bus.Emit(t.Context(), subject, []byte("exit")); err != nil {
		t.Fatal(err)
	}
	<-entered
	for range 4 {
		if err := bus.Emit(t.Context(), subject, []byte("next")); err != nil {
			t.Fatal(err)
		}
	}
	if err := bus.conn.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { bus.Close(); close(closed) }()
	deadline := time.Now().Add(time.Second)
	for {
		bus.mu.RLock()
		closing := bus.closed
		bus.mu.RUnlock()
		if closing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Close did not reject new work")
		}
		time.Sleep(time.Millisecond)
	}
	if err := bus.On(subject, func(context.Context, any) error { return nil }); err == nil {
		t.Error("subscription accepted during Close")
	}
	if err := bus.Emit(t.Context(), subject, []byte("late")); err == nil {
		t.Error("publication accepted during Close")
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not drain after Goexit")
	}
	if received.Load() != 4 {
		t.Fatalf("buffered events lost: %d", received.Load())
	}
}

func TestNATSBusClosePreservesConnectionFailure(t *testing.T) {
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	bus.conn.Close()
	results := make(chan error, 4)
	for range 4 {
		go func() { results <- bus.Close() }()
	}
	for range 4 {
		if err := <-results; !errors.Is(err, nats.ErrConnectionClosed) {
			t.Errorf("closed connection lost shutdown failure: %v", err)
		}
	}
}

func TestNATSBusCloseReportsConnectionLostDuringDrain(t *testing.T) {
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }); _ = bus.Close() })
	subject := fmt.Sprintf("audit.disconnect.%d", time.Now().UnixNano())
	if err := bus.On(subject, func(context.Context, any) error { close(entered); <-release; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := bus.conn.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	if err := bus.Emit(t.Context(), subject, []byte("accepted")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	sub := bus.subs[0].sub
	result := make(chan error, 1)
	go func() { result <- bus.Close() }()
	deadline := time.Now().Add(time.Second)
	for sub.IsValid() && !sub.IsDraining() {
		if time.Now().After(deadline) {
			t.Fatal("drain did not start")
		}
		time.Sleep(time.Millisecond)
	}
	bus.conn.Close()
	once.Do(func() { close(release) })
	if err := <-result; err == nil {
		t.Fatal("connection loss during drain reported success")
	}
}

func TestNATSBusConcurrentCloseAfterGoexitAndConnectionLoss(t *testing.T) {
	for _, queue := range []bool{false, true} {
		t.Run(fmt.Sprint(queue), func(t *testing.T) {
			bus, subscription, release := closingGoexitBus(t, queue)
			results := make(chan error, 8)
			for range cap(results) {
				go func() { results <- bus.Close() }()
			}
			deadline := time.Now().Add(time.Second)
			for subscription.sub.IsValid() && !subscription.sub.IsDraining() {
				if time.Now().After(deadline) {
					t.Fatal("drain did not start")
				}
				time.Sleep(time.Millisecond)
			}
			bus.conn.Close()
			select {
			case err := <-results:
				t.Fatalf("close returned before accepted handler exited: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			release()
			var first error
			for range cap(results) {
				select {
				case err := <-results:
					if !errors.Is(err, nats.ErrConnectionClosed) || (first != nil && first != err) {
						t.Fatalf("concurrent close changed or lost failure: %v", err)
					}
					first = err
				case <-time.After(3 * time.Second):
					t.Fatal("close hung after handler Goexit")
				}
			}
			select {
			case <-subscription.done:
			default:
				t.Fatal("shutdown left the subscription worker running")
			}
			if bus.Close() != first || !bus.conn.IsClosed() {
				t.Fatal("later close changed failure or retained the connection")
			}
		})
	}
}

func closingGoexitBus(t *testing.T, queue bool) (*NATSBus, *busSubscription, func()) {
	t.Helper()
	bus, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Fatal(err)
	}
	entered, released := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }
	t.Cleanup(func() { release(); bus.conn.Close(); _ = bus.Close() })
	subject := fmt.Sprintf("audit.close.goexit.%d", time.Now().UnixNano())
	handler := func(context.Context, any) error { close(entered); <-released; runtime.Goexit(); return nil }
	if queue {
		err = bus.OnQueue(subject, "audit-workers", handler)
	} else {
		err = bus.On(subject, handler)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.conn.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	if err := bus.Emit(t.Context(), subject, []byte("accepted")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	return bus, bus.subs[0], release
}

func TestNATSBusOutboundOnlyCloseFlushesPublications(t *testing.T) {
	receiver, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	publisher, err := NewNATSBus(natsURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	subject := fmt.Sprintf("audit.outbound.%d", time.Now().UnixNano())
	done := make(chan struct{})
	var count atomic.Int32
	if err := receiver.On(subject, func(_ context.Context, payload any) error {
		next := count.Add(1)
		if got := payload.([]byte); len(got) != 1 || got[0] != byte(next-1) {
			t.Errorf("publication order/data changed: %v index=%d", got, next)
		}
		if next == 64 {
			close(done)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := receiver.conn.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	for i := range 64 {
		if err := publisher.Emit(t.Context(), subject, []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("outbound publications missing: %d", count.Load())
	}
}

func TestNATSBusCloseReportsFlushTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.WriteString(conn, "INFO {\"server_id\":\"close-audit\",\"version\":\"2.10.0\",\"proto\":1,\"max_payload\":1048576}\r\n")
		reader := bufio.NewReader(conn)
		initial := true
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if line == "PING\r\n" && initial {
				_, _ = io.WriteString(conn, "PONG\r\n")
				initial = false
			}
		}
	}()
	bus, err := NewNATSBus("nats://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.Close(); !errors.Is(err, nats.ErrTimeout) {
		t.Fatalf("flush timeout lost: %v", err)
	}
	if err := bus.Close(); !errors.Is(err, nats.ErrTimeout) {
		t.Fatalf("repeated close lost timeout: %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("timed out close left connection open")
	}
}

func TestNATSBusRejectsBadSubjectAndPreservesSubscribedCloseFailure(t *testing.T) {
	for _, queue := range []bool{false, true} {
		t.Run(fmt.Sprint(queue), func(t *testing.T) {
			bus, err := NewNATSBus(natsURL(t))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = bus.Close() })
			handler := func(context.Context, any) error { return nil }
			subscribe := func(name string) error {
				if queue {
					return bus.OnQueue(name, "error-workers", handler)
				}
				return bus.On(name, handler)
			}
			if err := subscribe("invalid subject"); !errors.Is(err, nats.ErrBadSubject) || len(bus.subs) != 0 {
				t.Fatalf("invalid subject registered: %v", err)
			}
			if err := subscribe("audit.error.closed"); err != nil {
				t.Fatal(err)
			}
			bus.conn.Close()
			failure := bus.Close()
			if !errors.Is(failure, nats.ErrConnectionClosed) || !strings.Contains(failure.Error(), "nats event drain") || !strings.Contains(failure.Error(), "nats event unsubscribe") {
				t.Fatalf("close lost subscribed failure: %v", failure)
			}
			if bus.Close() != failure || !bus.closed || len(bus.subs) != 0 {
				t.Fatal("close lost its terminal state")
			}
		})
	}
}

func TestNATSSubscriptionContinuesAfterSlowConsumer(t *testing.T) {
	conn, err := nats.Connect(natsURL(t))
	if err != nil {
		t.Fatal(err)
	}
	bus := &NATSBus{conn: conn}
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Error(err)
		}
	})
	sub, err := conn.SubscribeSync("audit.slow.consumer")
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.SetPendingLimits(1, -1); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"first", "drop-one", "drop-two"} {
		if err := conn.Publish(sub.Subject, []byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.Flush(); err != nil {
		t.Fatal(err)
	}
	if dropped, err := sub.Dropped(); err != nil || dropped < 2 {
		t.Fatalf("slow consumer was not exercised: dropped=%d err=%v", dropped, err)
	}
	received := make(chan string, 2)
	owned := &busSubscription{sub: sub, done: make(chan struct{}), bus: bus, handler: func(message *nats.Msg) { received <- string(message.Data) }}
	bus.subs = append(bus.subs, owned)
	go owned.run()
	for _, want := range []string{"first", "after"} {
		select {
		case got := <-received:
			if got != want {
				t.Fatalf("delivery=%q want=%q", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("subscription stopped after slow-consumer signal")
		}
		if want == "first" {
			if err := conn.Publish(sub.Subject, []byte("after")); err != nil {
				t.Fatal(err)
			}
			if err := conn.Flush(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := bus.Close(); err != nil {
		t.Fatal(err)
	}
	if dropped, failed := bus.DiagnosticStats(); dropped != 0 || failed != 0 {
		t.Fatalf("diagnostic failed: dropped=%d failed=%d", dropped, failed)
	}
}
