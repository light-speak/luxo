package queue

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		MaxRetries:  3,
		RetryDelay:  10 * time.Millisecond, // fast retries for tests
		Concurrency: 2,
	}
}

func TestMemoryPublishSubscribe(t *testing.T) {
	q := NewMemoryQueue(testConfig())
	defer q.Close()

	var received atomic.Value
	done := make(chan struct{})

	err := q.Subscribe("test", func(ctx context.Context, payload []byte) error {
		received.Store(string(payload))
		close(done)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = q.Publish(context.Background(), "test", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for handler")
	}

	if got := received.Load().(string); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestMemoryPublishNoSubscriber(t *testing.T) {
	q := NewMemoryQueue(testConfig())
	defer q.Close()

	err := q.Publish(context.Background(), "noqueue", []byte("hello"))
	if err == nil {
		t.Error("expected error publishing to queue with no subscriber")
	}
}

func TestMemoryDoubleSubscribe(t *testing.T) {
	q := NewMemoryQueue(testConfig())
	defer q.Close()

	err := q.Subscribe("test", func(ctx context.Context, payload []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	err = q.Subscribe("test", func(ctx context.Context, payload []byte) error { return nil })
	if err == nil {
		t.Error("expected error on double subscribe")
	}
}

func TestMemoryRetryOnError(t *testing.T) {
	q := NewMemoryQueue(testConfig())
	defer q.Close()

	var attempts atomic.Int32
	done := make(chan struct{})

	err := q.Subscribe("retry", func(ctx context.Context, payload []byte) error {
		n := attempts.Add(1)
		if n < 3 {
			return fmt.Errorf("fail attempt %d", n)
		}
		close(done)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = q.Publish(context.Background(), "retry", []byte("task"))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout: got %d attempts", attempts.Load())
	}

	if got := attempts.Load(); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

func TestMemoryRetryExhaustedDLQ(t *testing.T) {
	q := NewMemoryQueue(testConfig())
	defer q.Close()

	var dlqReceived atomic.Value
	dlqDone := make(chan struct{})

	// Subscribe to DLQ first.
	err := q.Subscribe("fail.dlq", func(ctx context.Context, payload []byte) error {
		dlqReceived.Store(string(payload))
		close(dlqDone)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Handler that always fails.
	err = q.Subscribe("fail", func(ctx context.Context, payload []byte) error {
		return fmt.Errorf("always fail")
	})
	if err != nil {
		t.Fatal(err)
	}

	err = q.Publish(context.Background(), "fail", []byte("doomed"))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-dlqDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for DLQ")
	}

	if got := dlqReceived.Load().(string); got != "doomed" {
		t.Errorf("DLQ got %q, want %q", got, "doomed")
	}
}

func TestMemoryRetryExhaustedNoDLQ(t *testing.T) {
	// When no DLQ subscriber exists, task is dropped with a log (no panic).
	q := NewMemoryQueue(testConfig())
	defer q.Close()

	done := make(chan struct{})
	var attempts atomic.Int32

	err := q.Subscribe("nodlq", func(ctx context.Context, payload []byte) error {
		if attempts.Add(1) >= int32(testConfig().MaxRetries) {
			defer close(done)
		}
		return fmt.Errorf("fail")
	})
	if err != nil {
		t.Fatal(err)
	}

	err = q.Publish(context.Background(), "nodlq", []byte("data"))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

func TestMemoryPanicRecovery(t *testing.T) {
	q := NewMemoryQueue(testConfig())
	defer q.Close()

	done := make(chan struct{})
	var attempts atomic.Int32

	err := q.Subscribe("panic", func(ctx context.Context, payload []byte) error {
		n := attempts.Add(1)
		if n == 1 {
			panic("boom")
		}
		close(done)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = q.Publish(context.Background(), "panic", []byte("task"))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

func TestMemoryConcurrency(t *testing.T) {
	cfg := testConfig()
	cfg.Concurrency = 4
	q := NewMemoryQueue(cfg)
	defer q.Close()

	const taskCount = 100
	var received atomic.Int32
	var wg sync.WaitGroup
	wg.Add(taskCount)

	err := q.Subscribe("concurrent", func(ctx context.Context, payload []byte) error {
		received.Add(1)
		wg.Done()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for i := range taskCount {
		err := q.Publish(context.Background(), "concurrent", []byte(fmt.Sprintf("task-%d", i)))
		if err != nil {
			t.Fatal(err)
		}
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout: received %d/%d", received.Load(), taskCount)
	}

	if got := received.Load(); got != taskCount {
		t.Errorf("received %d, want %d", got, taskCount)
	}
}

func TestMemoryPayloadIsolation(t *testing.T) {
	// Verify that mutating the original slice after Publish doesn't affect the task.
	q := NewMemoryQueue(testConfig())
	defer q.Close()

	var received atomic.Value
	done := make(chan struct{})

	err := q.Subscribe("iso", func(ctx context.Context, payload []byte) error {
		received.Store(string(payload))
		close(done)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	data := []byte("original")
	err = q.Publish(context.Background(), "iso", data)
	if err != nil {
		t.Fatal(err)
	}

	// Mutate original.
	data[0] = 'X'

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}

	if got := received.Load().(string); got != "original" {
		t.Errorf("got %q, want %q (payload was mutated)", got, "original")
	}
}

func TestMemoryCloseIdempotent(t *testing.T) {
	q := NewMemoryQueue(testConfig())
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	// Second close should be no-op.
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryPublishAfterClose(t *testing.T) {
	q := NewMemoryQueue(testConfig())
	q.Subscribe("test", func(ctx context.Context, payload []byte) error { return nil })
	q.Close()

	err := q.Publish(context.Background(), "test", []byte("late"))
	if err == nil {
		t.Error("expected error publishing to closed queue")
	}
}

func TestMemorySubscribeAfterClose(t *testing.T) {
	q := NewMemoryQueue(testConfig())
	q.Close()

	err := q.Subscribe("test", func(ctx context.Context, payload []byte) error { return nil })
	if err == nil {
		t.Error("expected error subscribing to closed queue")
	}
}

func TestMemoryGracefulShutdown(t *testing.T) {
	// Verify Close() waits for in-flight tasks.
	q := NewMemoryQueue(testConfig())

	var processed atomic.Int32

	err := q.Subscribe("drain", func(ctx context.Context, payload []byte) error {
		time.Sleep(50 * time.Millisecond)
		processed.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Publish several tasks.
	for i := range 5 {
		q.Publish(context.Background(), "drain", []byte(fmt.Sprintf("task-%d", i)))
	}

	// Give workers time to pick up tasks.
	time.Sleep(20 * time.Millisecond)

	// Close should wait for in-flight tasks.
	err = q.Close()
	if err != nil {
		t.Fatal(err)
	}

	if got := processed.Load(); got == 0 {
		t.Error("expected at least some tasks to be processed before close returned")
	}
}

func TestMemoryConcurrentPublishClose(t *testing.T) {
	// Verify that concurrent Publish and Close do not panic (race condition regression test).
	for range 50 {
		q := NewMemoryQueue(testConfig())
		err := q.Subscribe("race", func(ctx context.Context, payload []byte) error {
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup

		// Spawn publishers.
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 100 {
					_ = q.Publish(context.Background(), "race", []byte("data"))
				}
			}()
		}

		// Concurrently close.
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = q.Close()
		}()

		wg.Wait()
	}
}
