package queue

import (
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"

	"github.com/light-speak/luxo/internal/diagnostics"
)

type memoryDiagnosticError func() string

func (f memoryDiagnosticError) Error() string { return f() }

type memoryDiagnosticWriter func([]byte) (int, error)

func (f memoryDiagnosticWriter) Write(data []byte) (int, error) { return f(data) }

func TestMemoryDiagnosticFormatting(t *testing.T) {
	for _, sample := range []struct {
		failure memoryDiagnostic
		want    string
	}{
		{memoryDiagnostic{queue: "tasks", attempt: 1, limit: 3, cause: errors.New("rejected")}, "queue tasks: attempt 1/3 failed: rejected\n"},
		{memoryDiagnostic{queue: "tasks", operation: "DLQ buffer full, task dropped"}, "queue tasks: DLQ buffer full, task dropped\n"},
	} {
		if got := formatMemoryDiagnostic(sample.failure); got != sample.want {
			t.Errorf("diagnostic = %q, want %q", got, sample.want)
		}
	}
	line := formatMemoryDiagnostic(memoryDiagnostic{queue: "tasks", cause: errors.New(strings.Repeat("中文", 4096))})
	if len(line) > 4097 || !utf8.ValidString(line) || !strings.HasSuffix(line, "\n") {
		t.Fatalf("oversized diagnostic: bytes=%d valid=%t", len(line), utf8.ValidString(line))
	}
}

func TestMemoryDiagnosticRejectsAfterCleanClose(t *testing.T) {
	q := mustMemoryQueue(t, testConfig())
	if dropped, failed := q.DiagnosticStats(); dropped != 0 || failed != 0 {
		t.Fatal("empty queue has diagnostics", dropped, failed)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q.reportDiagnostic(memoryDiagnostic{operation: "late diagnostic"})
	if dropped, failed := q.DiagnosticStats(); dropped != 1 || failed != 0 || q.reports != nil {
		t.Fatal("closed queue started reporting or lost rejection count", dropped, failed)
	}
}

func TestMemoryDiagnosticBoundedShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := mustMemoryQueue(t, testConfig())
		release := make(chan struct{})
		defer close(release)
		failure := &blockingMemoryDiagnostic{entered: make(chan struct{}), release: release}
		q.reports = diagnostics.New(io.Discard, 16, formatMemoryDiagnostic)
		q.reportDiagnostic(memoryDiagnostic{cause: failure})
		<-failure.entered
		for range 24 {
			q.reportDiagnostic(memoryDiagnostic{operation: "pending"})
		}
		if dropped, failed := q.DiagnosticStats(); dropped != 8 || failed != 0 || q.reports.Pending() != 16 {
			t.Fatal("diagnostic capacity or counters changed", dropped, failed, q.reports.Pending())
		}
		start := time.Now()
		err := q.Close()
		if !errors.Is(err, ErrDiagnosticShutdown) || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != shutdownTimeout {
			t.Fatal("blocked diagnostic lost bounded shutdown", err, time.Since(start))
		}
		if repeated := q.Close(); repeated != err {
			t.Fatal("repeated close replaced the first diagnostic failure", repeated)
		}
		q.reportDiagnostic(memoryDiagnostic{operation: "late"})
		if dropped, _ := q.DiagnosticStats(); dropped != 9 {
			t.Fatal("late diagnostic was not counted", dropped)
		}
	})
}

func TestMemoryDiagnosticsUseRemainingShutdownBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := mustMemoryQueue(t, Config{Concurrency: 1, MaxRetries: 1})
		release := make(chan struct{})
		defer close(release)
		entered := make(chan struct{})
		failure := &blockingMemoryDiagnostic{entered: make(chan struct{}), release: release}
		if err := q.Subscribe("tasks", func(context.Context, []byte) error {
			close(entered)
			time.Sleep(8 * time.Second)
			return failure
		}); err != nil {
			t.Fatal(err)
		}
		if err := q.Publish(t.Context(), "tasks", []byte("accepted")); err != nil {
			t.Fatal(err)
		}
		<-entered
		start := time.Now()
		err := q.Close()
		if !errors.Is(err, ErrDiagnosticShutdown) || time.Since(start) != shutdownTimeout {
			t.Fatal("diagnostics obtained a fresh shutdown budget", err, time.Since(start))
		}
		select {
		case <-failure.entered:
		default:
			t.Fatal("shutdown discarded the first in-flight failure diagnostic")
		}
	})
}

func TestMemoryDiagnosticFailuresPreserveBusinessDelivery(t *testing.T) {
	for _, mode := range []string{"panic", "goexit", "writer"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				q := mustMemoryQueue(t, Config{Concurrency: 1, MaxRetries: 1})
				failure := memoryDiagnosticError(func() string {
					if mode == "panic" {
						panic("diagnostic panic")
					}
					if mode == "goexit" {
						runtime.Goexit()
					}
					return "rejected"
				})
				writerFailure := errors.New("writer failed")
				writer := memoryDiagnosticWriter(func(data []byte) (int, error) {
					if mode == "writer" {
						return 0, writerFailure
					}
					return len(data), nil
				})
				q.reports = diagnostics.New(writer, 16, formatMemoryDiagnostic)
				assertMemoryDiagnosticFailureDelivery(t, q, failure, mode, writerFailure)
			})
		})
	}
}

func assertMemoryDiagnosticFailureDelivery(t *testing.T, q *MemoryQueue, failure error, mode string, writerFailure error) {
	t.Helper()
	delivered := make(chan struct{})
	if err := q.Subscribe("tasks", func(_ context.Context, payload []byte) error {
		if payload[0] == 1 {
			return failure
		}
		close(delivered)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []byte{1, 2} {
		if err := q.Publish(t.Context(), "tasks", []byte{value}); err != nil {
			t.Fatal(err)
		}
	}
	<-delivered
	err := q.Close()
	want, failures := diagnostics.ErrAborted, uint64(1)
	if mode == "writer" {
		want, failures = writerFailure, 2 // The exhausted task also reports its missing DLQ.
	}
	if !errors.Is(err, ErrDiagnosticShutdown) || !errors.Is(err, want) {
		t.Fatal("close discarded diagnostic failure", err)
	}
	if repeated := q.Close(); repeated != err {
		t.Fatal("repeated close changed diagnostic result", repeated)
	}
	if dropped, failed := q.DiagnosticStats(); dropped != 0 || failed != failures {
		t.Fatal("diagnostic failures changed task handling or counters", dropped, failed)
	}
}

func TestMemoryDiagnosticConcurrentFirstFailure(t *testing.T) {
	q := mustMemoryQueue(t, testConfig())
	var first atomic.Pointer[diagnostics.Reporter]
	var calls sync.WaitGroup
	calls.Add(32)
	for range 32 {
		go func() {
			defer calls.Done()
			q.reportDiagnostic(memoryDiagnostic{operation: "concurrent"})
			q.mu.Lock()
			reports := q.reports
			q.mu.Unlock()
			first.CompareAndSwap(nil, reports)
			if first.Load() != reports {
				t.Error("concurrent failure replaced the reporting worker")
			}
		}()
	}
	calls.Wait()
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	if _, failed := q.DiagnosticStats(); failed != 0 {
		t.Fatal("concurrent reporter initialization failed", failed)
	}
}
