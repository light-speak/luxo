package queue

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func testConfig() Config {
	return Config{
		MaxRetries:  3,
		RetryDelay:  10 * time.Millisecond, // fast retries for tests
		Concurrency: 2,
	}
}

type blockingMemoryDiagnostic struct {
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (e *blockingMemoryDiagnostic) Error() string {
	e.once.Do(func() { close(e.entered) })
	<-e.release
	return "business failure"
}

func TestMemoryDiagnosticDoesNotBlockDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := mustMemoryQueue(t, Config{Concurrency: 1, MaxRetries: 1})
		defer q.Close()
		release := make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(release) })
		failure := &blockingMemoryDiagnostic{entered: make(chan struct{}), release: release}
		delivered := make(chan struct{})
		if err := q.Subscribe("tasks", func(_ context.Context, payload []byte) error {
			if string(payload) == "first" {
				return failure
			}
			close(delivered)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, payload := range []string{"first", "second"} {
			if err := q.Publish(t.Context(), "tasks", []byte(payload)); err != nil {
				t.Fatal(err)
			}
		}
		<-failure.entered
		synctest.Wait()
		select {
		case <-delivered:
		default:
			t.Error("diagnostic formatting blocked the next business task")
		}
		once.Do(func() { close(release) })
		if err := q.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestMemorySubscribeRejectsNilHandler(t *testing.T) {
	q := mustMemoryQueue(t, testConfig())
	defer q.Close()
	if err := q.Subscribe("tasks", nil); err == nil {
		t.Error("nil handler registered")
	}
	if len(q.queues) != 0 {
		t.Fatal("invalid subscription reserved a queue and started workers")
	}
	if err := q.Subscribe("tasks", func(context.Context, []byte) error { return nil }); err != nil {
		t.Fatalf("invalid subscription blocked valid registration: %v", err)
	}
}

func TestMemoryPublishCanceledBeforeEnqueue(t *testing.T) {
	q := mustMemoryQueue(t, testConfig())
	defer q.Close()
	q.queues["test"] = &memQueue{ch: make(chan []byte, 1)}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := q.Publish(ctx, "test", []byte("must not enqueue")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Publish error = %v, want canceled", err)
	}
	if len(q.queues["test"].ch) != 0 {
		t.Fatal("canceled publication enqueued a task")
	}
}

func TestMemoryBlockedPublishObservesContext(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprintf("deadline=%v", deadline), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				q := mustMemoryQueue(t, testConfig())
				defer q.Close()
				q.queues["test"] = &memQueue{ch: make(chan []byte, 1)}
				q.queues["test"].ch <- []byte("original")
				ctx, cancel := context.WithCancel(t.Context())
				want := context.Canceled
				if deadline {
					cancel()
					ctx, cancel = context.WithTimeout(t.Context(), time.Second)
					want = context.DeadlineExceeded
				}
				defer cancel()
				result := make(chan error, 1)
				go func() { result <- q.Publish(ctx, "test", []byte("blocked")) }()
				synctest.Wait()
				if deadline {
					time.Sleep(time.Second)
				} else {
					cancel()
				}
				synctest.Wait()
				select {
				case err := <-result:
					if !errors.Is(err, want) {
						t.Fatalf("Publish error = %v, want %v", err, want)
					}
				default:
					t.Fatal("blocked publication ignored canceled context")
				}
				if string(<-q.queues["test"].ch) != "original" || len(q.queues["test"].ch) != 0 {
					t.Fatal("canceled publication changed buffered task")
				}
			})
		})
	}
}

func TestMemoryBlockedPublishResumesWhenCapacityReturns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := mustMemoryQueue(t, testConfig())
		defer q.Close()
		buffer := make(chan []byte, 1)
		q.queues["test"] = &memQueue{ch: buffer}
		buffer <- []byte("original")
		payload := []byte("pending")
		result := make(chan error, 1)
		go func() { result <- q.Publish(t.Context(), "test", payload) }()
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("full queue did not block publication: %v", err)
		default:
		}
		if got := string(<-buffer); got != "original" {
			t.Fatal(got)
		}
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		payload[0] = 'X'
		if got := string(<-buffer); got != "pending" {
			t.Fatalf("slow publication did not retain its own payload: %q", got)
		}
		q.work.Done() // This fixture consumes without a worker; acknowledge its task.
	})
}

func TestMemoryCloseInterruptsRetryDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := mustMemoryQueue(t, Config{Concurrency: 1, MaxRetries: 3, RetryDelay: time.Hour})
		defer q.Close()
		var calls atomic.Int32
		if err := q.Subscribe("retry", func(context.Context, []byte) error {
			calls.Add(1)
			return errors.New("rejected")
		}); err != nil {
			t.Fatal(err)
		}
		if err := q.Publish(t.Context(), "retry", []byte("task")); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		start := time.Now()
		if err := q.Close(); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 1 || time.Since(start) != 0 {
			t.Fatalf("shutdown retried or waited for backoff: calls=%d elapsed=%v", calls.Load(), time.Since(start))
		}
	})
}

func TestMemoryCloseReportsBlockedHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := mustMemoryQueue(t, Config{Concurrency: 1, MaxRetries: 1})
		defer q.Close()
		entered, release := make(chan struct{}), make(chan struct{})
		defer func() { close(release); synctest.Wait() }()
		if err := q.Subscribe("blocked", func(context.Context, []byte) error {
			close(entered)
			<-release
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := q.Publish(t.Context(), "blocked", nil); err != nil {
			t.Fatal(err)
		}
		<-entered
		start := time.Now()
		err := q.Close()
		if err == nil || !strings.Contains(err.Error(), "shutdown timed out") || time.Since(start) != shutdownTimeout {
			t.Fatalf("blocked handler shutdown: err=%v elapsed=%v", err, time.Since(start))
		}
	})
}

func TestMemoryFullDeadLetterQueuePreservesBufferedTask(t *testing.T) {
	q := mustMemoryQueue(t, testConfig())
	defer q.Close()
	buffer := make(chan []byte, 1)
	q.queues["failed.dlq"] = &memQueue{ch: buffer}
	buffer <- []byte("original")
	q.sendToDLQ("failed", []byte("cannot enqueue"))
	if got := string(<-buffer); got != "original" || len(buffer) != 0 {
		t.Fatalf("full development DLQ changed the accepted task: %q", got)
	}
}

func BenchmarkMemoryPublishReady(b *testing.B) {
	for _, cancellable := range []bool{false, true} {
		for _, size := range []int{32, 4096} {
			b.Run(fmt.Sprintf("cancel=%v/bytes=%d", cancellable, size), func(b *testing.B) {
				ctx := context.Background()
				if cancellable {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					defer cancel()
					_ = ctx.Done()
				}
				q := mustMemoryQueue(b, testConfig())
				defer q.Close()
				channel := make(chan []byte, 1)
				q.queues["test"] = &memQueue{ch: channel}
				payload := make([]byte, size)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if err := q.Publish(ctx, "test", payload); err != nil {
						b.Fatal(err)
					}
					if len(<-channel) != size {
						b.Fatal("publication changed payload size")
					}
					q.work.Done() // Acknowledge the task consumed by this benchmark.
				}
			})
		}
	}
}

func TestMemoryPublishSubscribe(t *testing.T) {
	q := mustMemoryQueue(t, testConfig())
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
	q := mustMemoryQueue(t, testConfig())
	defer q.Close()

	err := q.Publish(context.Background(), "noqueue", []byte("hello"))
	if err == nil {
		t.Error("expected error publishing to queue with no subscriber")
	}
}

func TestMemoryDoubleSubscribe(t *testing.T) {
	q := mustMemoryQueue(t, testConfig())
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
	q := mustMemoryQueue(t, testConfig())
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
	q := mustMemoryQueue(t, testConfig())
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
	q := mustMemoryQueue(t, testConfig())
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
	q := mustMemoryQueue(t, testConfig())
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
	q := mustMemoryQueue(t, cfg)
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
	q := mustMemoryQueue(t, testConfig())
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
	q := mustMemoryQueue(t, testConfig())
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	// Second close should be no-op.
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryPublishAfterClose(t *testing.T) {
	q := mustMemoryQueue(t, testConfig())
	q.Subscribe("test", func(ctx context.Context, payload []byte) error { return nil })
	q.Close()

	err := q.Publish(context.Background(), "test", []byte("late"))
	if err == nil {
		t.Error("expected error publishing to closed queue")
	}
}

func TestMemoryPublishObservesShutdownAfterQueueLookup(t *testing.T) {
	q := mustMemoryQueue(t, testConfig())
	q.queues["test"] = &memQueue{ch: make(chan []byte)}
	close(q.done)

	err := q.Publish(context.Background(), "test", []byte("late"))
	if err == nil || err.Error() != "queue: closed" {
		t.Fatalf("Publish error = %v, want queue closed", err)
	}
}

func TestMemorySubscribeAfterClose(t *testing.T) {
	q := mustMemoryQueue(t, testConfig())
	q.Close()

	err := q.Subscribe("test", func(ctx context.Context, payload []byte) error { return nil })
	if err == nil {
		t.Error("expected error subscribing to closed queue")
	}
}

func TestMemoryGracefulShutdown(t *testing.T) {
	// Verify Close() waits for in-flight tasks.
	q := mustMemoryQueue(t, testConfig())

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
	// Every successful publish must be processed before graceful shutdown completes.
	for range 50 {
		q := mustMemoryQueue(t, testConfig())
		var accepted, processed atomic.Int32
		err := q.Subscribe("race", func(ctx context.Context, payload []byte) error {
			processed.Add(1)
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
					if q.Publish(context.Background(), "race", []byte("data")) == nil {
						accepted.Add(1)
					}
				}
			}()
		}

		// Concurrently close.
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := q.Close(); err != nil {
				t.Error(err)
			}
		}()

		wg.Wait()
		if accepted.Load() != processed.Load() {
			t.Fatalf("successful publication lost during close: accepted=%d processed=%d", accepted.Load(), processed.Load())
		}
	}
}

type hostilePanicPayload struct{ called *bool }

func (p hostilePanicPayload) String() string {
	*p.called = true
	panic("must not format panic payload")
}

func TestPanicRecoveryDoesNotInvokeFormatter(t *testing.T) {
	called := false
	err := InvokeHandler(func(context.Context, []byte) error {
		panic(hostilePanicPayload{&called})
	}, t.Context(), nil)
	if called || err == nil {
		t.Fatal("panic recovery invoked a user formatter")
	}
}

func TestMemoryCloseUnblocksPublishersAndDrainsAcceptedTasks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := mustMemoryQueue(t, Config{Concurrency: 1, MaxRetries: 1})
		gate := make(chan struct{})
		var release sync.Once
		defer release.Do(func() { close(gate) })
		var processed atomic.Int32
		if err := q.Subscribe("work", func(context.Context, []byte) error {
			if processed.Add(1) == 1 {
				<-gate
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := q.Publish(t.Context(), "work", []byte{1}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		capacity := cap(q.queues["work"].ch)
		for range capacity {
			if err := q.Publish(t.Context(), "work", []byte{1}); err != nil {
				t.Fatal(err)
			}
		}
		published := make(chan error, 1)
		go func() { published <- q.Publish(t.Context(), "work", []byte{2}) }()
		synctest.Wait()
		select {
		case <-published:
			t.Fatal("full queue did not block")
		default:
		}
		closed := make(chan error, 1)
		go func() { closed <- q.Close() }()
		if err := <-published; err == nil {
			t.Fatal("blocked publisher accepted after closure")
		}
		release.Do(func() { close(gate) })
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
		if got := processed.Load(); got != int32(capacity+1) {
			t.Fatalf("accepted tasks lost: %d", got)
		}
		if len(q.queues["work"].ch) != 0 {
			t.Fatal("shutdown retained tasks")
		}
	})
}

func TestMemoryCloseDrainsDeadLettersFromInflightTasks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := mustMemoryQueue(t, Config{Concurrency: 1, MaxRetries: 1})
		gate := make(chan struct{})
		var release sync.Once
		defer release.Do(func() { close(gate) })
		var delivered atomic.Int32
		if err := q.Subscribe("work", func(context.Context, []byte) error {
			<-gate
			return errors.New("business failure")
		}); err != nil {
			t.Fatal(err)
		}
		if err := q.Subscribe("work"+DeadLetterSuffix, func(_ context.Context, payload []byte) error {
			if string(payload) != "accepted" {
				t.Error("dead-letter payload changed")
			}
			delivered.Add(1)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := q.Publish(t.Context(), "work", []byte("accepted")); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		closed := make(chan error, 1)
		go func() { closed <- q.Close() }()
		synctest.Wait()
		release.Do(func() { close(gate) })
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
		if delivered.Load() != 1 || len(q.queues["work"+DeadLetterSuffix].ch) != 0 {
			t.Fatalf("shutdown abandoned dead letter: delivered=%d queued=%d", delivered.Load(), len(q.queues["work"+DeadLetterSuffix].ch))
		}
	})
}

func TestMemoryConcurrentClosePreservesTimeoutResult(t *testing.T) {
	// Use the real clock: sync.Once mutex contention is not a durable
	// synctest wait and prevents its virtual shutdown timer from advancing.
	q := mustMemoryQueue(t, Config{Concurrency: 1, MaxRetries: 1})
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		close(release)
		_ = q.Close()
		q.wg.Wait()
	}()
	if err := q.Subscribe("blocked", func(context.Context, []byte) error {
		close(entered)
		<-release
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.Publish(t.Context(), "blocked", nil); err != nil {
		t.Fatal(err)
	}
	<-entered
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- q.Close() }()
	}
	for range 2 {
		if err := <-results; err == nil || !strings.Contains(err.Error(), "shutdown timed out") {
			t.Errorf("concurrent closer lost the shutdown failure: %v", err)
		}
	}
	if err := q.Close(); err == nil || !strings.Contains(err.Error(), "shutdown timed out") {
		t.Fatalf("repeated closer lost the shutdown failure: %v", err)
	}
}

func TestMemoryGoexitRetainsRetryAndWorker(t *testing.T) {
	for _, exhaust := range []bool{false, true} {
		t.Run(fmt.Sprint(exhaust), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				q := mustMemoryQueue(t, Config{Concurrency: 1, MaxRetries: 3, RetryDelay: time.Millisecond})
				var attempts, following, dead atomic.Int32
				if err := q.Subscribe("tasks.dlq", func(_ context.Context, payload []byte) error {
					if string(payload) != "exit" {
						t.Error("wrong dead letter")
					}
					dead.Add(1)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := q.Subscribe("tasks", func(_ context.Context, payload []byte) error {
					if string(payload) == "next" {
						following.Add(1)
						return nil
					}
					attempt := attempts.Add(1)
					if exhaust || attempt == 1 {
						runtime.Goexit()
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				for _, payload := range []string{"exit", "next"} {
					if err := q.Publish(t.Context(), "tasks", []byte(payload)); err != nil {
						t.Fatal(err)
					}
				}
				time.Sleep(10 * time.Millisecond)
				synctest.Wait()
				if err := q.Close(); err != nil {
					t.Error(err)
				}
				wantAttempts, wantDead := int32(2), int32(0)
				if exhaust {
					wantAttempts, wantDead = 3, 1
				}
				if attempts.Load() != wantAttempts || following.Load() != 1 || dead.Load() != wantDead {
					t.Errorf("attempts=%d following=%d dead=%d", attempts.Load(), following.Load(), dead.Load())
				}

			})
		})
	}
}

func TestMemoryGoexitDuringCloseDrainsAcceptedTasks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := mustMemoryQueue(t, Config{Concurrency: 1, MaxRetries: 3, RetryDelay: time.Hour})
		entered, release := make(chan struct{}), make(chan struct{})
		var following atomic.Int32
		if err := q.Subscribe("tasks", func(_ context.Context, payload []byte) error {
			if string(payload) == "next" {
				following.Add(1)
				return nil
			}
			close(entered)
			<-release
			runtime.Goexit()
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, payload := range []string{"exit", "next"} {
			if err := q.Publish(t.Context(), "tasks", []byte(payload)); err != nil {
				t.Fatal(err)
			}
		}
		<-entered
		closed := make(chan error, 1)
		go func() { closed <- q.Close() }()
		synctest.Wait()
		close(release)
		synctest.Wait()
		select {
		case err := <-closed:
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("replacement worker did not finish shutdown")
		}
		if following.Load() != 1 {
			t.Fatal("accepted following task was abandoned")
		}
	})
}

func TestMemoryGoexitReplacementPreservesConcurrency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := mustMemoryQueue(t, Config{Concurrency: 4, MaxRetries: 2, RetryDelay: time.Nanosecond})
		var attempts [128]atomic.Int32
		var active, completed atomic.Int32
		if err := q.Subscribe("tasks", func(_ context.Context, payload []byte) error {
			if current := active.Add(1); current > 4 {
				t.Errorf("active handlers=%d", current)
			}
			defer active.Add(-1)
			if attempts[payload[0]].Add(1) == 1 {
				runtime.Goexit()
			}
			completed.Add(1)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for i := range attempts {
			if err := q.Publish(t.Context(), "tasks", []byte{byte(i)}); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if err := q.Close(); err != nil {
			t.Fatal(err)
		}
		if completed.Load() != int32(len(attempts)) || active.Load() != 0 {
			t.Fatalf("completed=%d active=%d", completed.Load(), active.Load())
		}
		for i := range attempts {
			if attempts[i].Load() != 2 {
				t.Errorf("task %d attempts=%d", i, attempts[i].Load())
			}
		}
	})
}

func TestMemoryRetryDelayDoesNotOverflow(t *testing.T) {
	const longest = time.Duration(1<<63 - 1)
	for _, initial := range []time.Duration{time.Second, longest / 2, longest/2 + 1} {
		synctest.Test(t, func(t *testing.T) {
			q := mustMemoryQueue(t, Config{MaxRetries: 3})
			defer q.Close()
			task := memoryTask{active: true, attempt: 1, delay: initial}
			calls := 0
			q.executeTask("overflow", func(context.Context, []byte) error {
				calls++
				want := longest
				if initial <= longest/2 {
					want = initial * 2
				}
				// Inspect the next delay rather than asking the runtime timer to span
				// centuries: its absolute monotonic deadline has a separate upper bound.
				if task.delay != want {
					t.Errorf("next delay=%v want=%v", task.delay, want)
				}
				return nil
			}, &task)
			if calls != 1 {
				t.Fatalf("attempts=%d", calls)
			}
		})
	}
}
