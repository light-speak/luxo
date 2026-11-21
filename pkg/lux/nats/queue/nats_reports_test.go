package queue

import (
	"bytes"
	"context"
	"errors"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

type blockedReportWriter struct {
	entered chan struct{}
	release chan struct{}
}

func (w *blockedReportWriter) Write(p []byte) (int, error) {
	select {
	case w.entered <- struct{}{}:
	default:
	}
	<-w.release
	return len(p), nil
}

func TestRenewalReportsBoundedAndCloseDeadline(t *testing.T) {
	writer := &blockedReportWriter{entered: make(chan struct{}, 1), release: make(chan struct{})}
	reports := newRenewalReports(writer, 2)
	reports.Submit(errors.New("first"))
	<-writer.entered
	for range 3 {
		reports.Submit(errors.New("queued"))
	}
	if dropped, _ := reports.Stats(); dropped != 1 {
		t.Fatal("missing capacity rejection", dropped)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if !errors.Is(reports.Close(ctx), context.DeadlineExceeded) {
		t.Fatal("blocked writer ignored close deadline")
	}
	reports.Submit(errors.New("after close"))
	if dropped, _ := reports.Stats(); dropped != 2 {
		t.Fatal("missing closed rejection")
	}
	close(writer.release)
	if err := reports.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

type shortReportWriter struct{}

func (shortReportWriter) Write([]byte) (int, error) { return 0, nil }
func TestRenewalReportsPreserveWriteFailure(t *testing.T) {
	reports := newRenewalReports(shortReportWriter{}, 1)
	reports.Submit(errors.New("offline"))
	if err := reports.Close(t.Context()); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	if _, failures := reports.Stats(); failures != 1 {
		t.Fatal("missing failure count")
	}
}

func TestBlockedRenewalReportDoesNotStopHealthyRenewal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		writer := &blockedReportWriter{entered: make(chan struct{}, 1), release: make(chan struct{})}
		q := newTestNATSQueue(newMockJSClient(), testConfig())
		q.reports = newRenewalReports(writer, 2)
		renewal := newDeliveryRenewal(30*time.Millisecond, q.reportRenewal)
		failed := renewal.hold(&progressMessage{mockMsg: &mockMsg{}, progressErr: errors.New("offline")})
		healthyMessage := &progressMessage{mockMsg: &mockMsg{}}
		healthy := renewal.hold(healthyMessage)
		stop, done := make(chan struct{}), make(chan struct{})
		go func() { renewal.run(stop); close(done) }()
		<-writer.entered
		time.Sleep(100 * time.Millisecond)
		count := healthyMessage.progress.Load()
		close(stop)
		<-done
		failed.release()
		healthy.release()
		close(writer.release)
		if err := q.Close(); err != nil {
			t.Fatal(err)
		}
		if count < 3 {
			t.Fatalf("blocked log stopped healthy renewals: %d", count)
		}
	})
}

func TestQueueRenewalReportStatsAndFailurePropagation(t *testing.T) {
	q := newTestNATSQueue(newMockJSClient(), testConfig())
	q.reports = newRenewalReports(shortReportWriter{}, 2)
	q.reportRenewal(errors.New("offline"))
	if err := q.Close(); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal("report failure lost", err)
	}
	q.reportRenewal(errors.New("late"))
	dropped, failed := q.RenewalReportStats()
	if dropped != 1 || failed != 1 {
		t.Fatal(dropped, failed)
	}
	if err := q.Close(); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal("repeated close lost failure", err)
	}
	closed := newTestNATSQueue(newMockJSClient(), testConfig())
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	closed.reportRenewal(errors.New("late initialization"))
	dropped, failed = closed.RenewalReportStats()
	if dropped != 1 || failed != 0 || closed.reports != nil {
		t.Fatal("closed queue created reporting worker")
	}
}

type countingReportWriter struct{ writes atomic.Uint64 }

func (w *countingReportWriter) Write(p []byte) (int, error) { w.writes.Add(1); return len(p), nil }

func TestQueueReportsConcurrentSubmitCloseAccounting(t *testing.T) {
	q := newTestNATSQueue(newMockJSClient(), testConfig())
	writer := &countingReportWriter{}
	q.reports = newRenewalReports(writer, 16)
	start := make(chan struct{})
	var workers sync.WaitGroup
	failure := errors.New("offline")
	for range 32 {
		workers.Go(func() {
			<-start
			for range 64 {
				q.reportRenewal(failure)
				q.RenewalReportStats()
			}
		})
	}
	for range 8 {
		workers.Go(func() {
			<-start
			if err := q.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	close(start)
	workers.Wait()
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	dropped, failed := q.RenewalReportStats()
	if failed != 0 || dropped+writer.writes.Load() != 32*64 {
		t.Fatalf("report accounting lost records: writes=%d dropped=%d failed=%d", writer.writes.Load(), dropped, failed)
	}
}

func TestQueueReportTimeoutRemainsFailureAfterWriterRecovers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newTestNATSQueue(newMockJSClient(), testConfig())
		writer := &blockedReportWriter{entered: make(chan struct{}, 1), release: make(chan struct{})}
		q.reports = newRenewalReports(writer, 2)
		q.reportRenewal(errors.New("offline"))
		<-writer.entered
		first := q.Close()
		close(writer.release)
		if err := q.reports.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(first, context.DeadlineExceeded) {
			t.Fatalf("close hid blocked writer: %v", first)
		}
		if next := q.Close(); next != first {
			t.Fatalf("close changed result after recovery: %v -> %v", first, next)
		}
	})
}

func TestBatchPullFailureUsesBoundedReports(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		writer := &blockedReportWriter{entered: make(chan struct{}, 1), release: make(chan struct{})}
		q := newTestNATSQueue(newMockJSClient(), testConfig())
		q.reports = newRenewalReports(writer, 2)
		defer q.Close()
		defer close(writer.release)
		var pulls atomic.Int32
		source := fetchBatchFunc(func(int) (jetstream.MessageBatch, error) {
			pulls.Add(1)
			return nil, errors.New("offline")
		})
		r := newDeliveryRenewal(time.Second, q.reportRenewal)
		c := newBatchConsumer(source, r, func(jetstream.Msg) {})
		c.Start(1)
		synctest.Wait()
		time.Sleep(350 * time.Millisecond)
		c.Stop()
		<-c.Done()
		if pulls.Load() < 3 || len(writer.entered) != 1 {
			t.Fatalf("pull retries=%d diagnostics=%d", pulls.Load(), len(writer.entered))
		}
		if len(c.messages.slots) != 0 {
			t.Fatal("failed pulls retained reservations")
		}
	})
}

func TestQueueDiagnosticPreservesFailureKind(t *testing.T) {
	var output bytes.Buffer
	reports := newRenewalReports(&output, 2)
	reports.Submit(queueDiagnostic{queue: "tasks", operation: "batch pull failed; retrying", cause: errors.New("offline")})
	reports.Submit(errors.New("lost progress"))
	if err := reports.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := "queue: tasks: batch pull failed; retrying: offline\nqueue: progress renewal failed; delivery may repeat: lost progress\n"
	if output.String() != want {
		t.Fatalf("diagnostic kind lost: %q", output.String())
	}
}

func TestDeliveryFailuresUseBoundedReports(t *testing.T) {
	cases := []struct {
		name string
		run  func(*NATSQueue, *mockMsg)
		ack  bool
	}{
		{"ack", func(q *NATSQueue, m *mockMsg) { q.ackDelivery("tasks", m) }, true},
		{"nak", func(q *NATSQueue, m *mockMsg) { q.retryDelivery("tasks", m, 1) }, false},
		{"transfer", func(q *NATSQueue, m *mockMsg) {
			q.js.(*mockJSClient).publishErr = errors.New("offline")
			m.nakErr = nil
			q.failedDelivery(context.Background(), "tasks", m, uint64(q.cfg.MaxRetries))
		}, false},
		{"metadata", func(q *NATSQueue, m *mockMsg) {
			m.nakErr = nil
			q.handleDelivery(context.Background(), "tasks", nil, m)
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writer := &blockedReportWriter{entered: make(chan struct{}, 1), release: make(chan struct{})}
			q := newTestNATSQueue(newMockJSClient(), testConfig())
			q.reports = newRenewalReports(writer, 2)
			defer q.Close()
			defer close(writer.release)
			q.reportRenewal(errors.New("occupy writer"))
			<-writer.entered
			msg := &mockMsg{ackErr: errors.New("ack offline"), nakErr: errors.New("nak offline")}
			tc.run(q, msg)
			if q.reports.Pending() != 1 || msg.acked.Load() != tc.ack || msg.naked.Load() == tc.ack {
				t.Fatalf("diagnostics=%d ack=%v nak=%v", q.reports.Pending(), msg.acked.Load(), msg.naked.Load())
			}
		})
	}
}

func TestBatchWorkerGoexitUsesBoundedReports(t *testing.T) {
	for _, workers := range []int{1, 4} {
		synctest.Test(t, func(t *testing.T) {
			writer := &blockedReportWriter{entered: make(chan struct{}, 1), release: make(chan struct{})}
			q := newTestNATSQueue(newMockJSClient(), testConfig())
			q.reports = newRenewalReports(writer, 2)
			defer q.Close()
			defer close(writer.release)
			r := newDeliveryRenewal(time.Second, q.reportRenewal)
			var c *batchConsumer
			var pulled bool
			source := fetchBatchFunc(func(int) (jetstream.MessageBatch, error) {
				if pulled {
					<-c.messages.ctx.Done()
					return nil, c.messages.ctx.Err()
				}
				pulled = true
				batch := &readyBatch{messages: make(chan jetstream.Msg, 2)}
				batch.messages <- &mockMsg{}
				batch.messages <- &mockMsg{}
				close(batch.messages)
				return batch, nil
			})
			var calls atomic.Int32
			c = newBatchConsumer(source, r, func(msg jetstream.Msg) {
				r.handle(msg, func(context.Context, jetstream.Msg) {
					if calls.Add(1) == 1 {
						runtime.Goexit()
					}
				})
			})
			c.Start(workers)
			synctest.Wait()
			c.Stop()
			<-c.Done()
			if calls.Load() != 2 || len(writer.entered) != 1 || len(r.active) != 0 || len(c.messages.slots) != 0 {
				t.Fatalf("workers=%d calls=%d diagnostics=%d active=%d capacity=%d", workers, calls.Load(), len(writer.entered), len(r.active), len(c.messages.slots))
			}
		})
	}
}

func TestIteratorFailureUsesBoundedReports(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		writer := &blockedReportWriter{entered: make(chan struct{}, 1), release: make(chan struct{})}
		q := newTestNATSQueue(newMockJSClient(), testConfig())
		q.reports = newRenewalReports(writer, 2)
		defer q.Close()
		defer close(writer.release)
		messages := newTestMessages()
		messages.results <- iteratorResult{err: errors.New("offline")}
		messages.results <- iteratorResult{message: &mockMsg{}}
		var calls atomic.Int32
		c := newMessageConsumer(messages, func(jetstream.Msg) { calls.Add(1) }, q.reportRenewal)
		c.Start(1)
		synctest.Wait()
		time.Sleep(150 * time.Millisecond)
		c.Stop()
		<-c.Done()
		if calls.Load() != 1 || len(writer.entered) != 1 {
			t.Fatalf("calls=%d diagnostics=%d", calls.Load(), len(writer.entered))
		}
	})
}
