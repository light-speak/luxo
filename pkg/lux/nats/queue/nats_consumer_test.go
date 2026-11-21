package queue

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

type iteratorResult struct {
	message jetstream.Msg
	err     error
}
type testMessages struct {
	results chan iteratorResult
	stopped chan struct{}
	once    sync.Once
	calls   atomic.Int32
	stops   atomic.Int32
}

func newTestMessages() *testMessages {
	return &testMessages{results: make(chan iteratorResult, 4), stopped: make(chan struct{})}
}
func (m *testMessages) Next(...jetstream.NextOpt) (jetstream.Msg, error) {
	m.calls.Add(1)
	select {
	case result := <-m.results:
		return result.message, result.err
	case <-m.stopped:
		return nil, jetstream.ErrMsgIteratorClosed
	}
}
func (m *testMessages) Stop()  { m.once.Do(func() { m.stops.Add(1); close(m.stopped) }) }
func (m *testMessages) Drain() { m.Stop() }

func TestMessageConsumerRecoversWithoutReplayingMessage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		messages := newTestMessages()
		for range 3 {
			messages.results <- iteratorResult{message: &mockMsg{}}
		}
		var calls atomic.Int32
		c := newActiveMessageConsumer(messages, func(jetstream.Msg) { calls.Add(1); runtime.Goexit() })
		synctest.Wait()
		c.Stop()
		c.Stop()
		synctest.Wait()
		if calls.Load() != 3 || messages.stops.Load() != 1 {
			t.Fatalf("calls=%d stops=%d", calls.Load(), messages.stops.Load())
		}
	})
}

func TestMessageConsumerStopDuringGoexitDoesNotRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		messages := newTestMessages()
		messages.results <- iteratorResult{message: &mockMsg{}}
		messages.results <- iteratorResult{message: &mockMsg{}}
		entered, release := make(chan struct{}), make(chan struct{})
		c := newActiveMessageConsumer(messages, func(jetstream.Msg) { close(entered); <-release; runtime.Goexit() })
		<-entered
		c.Stop()
		close(release)
		synctest.Wait()
		if messages.calls.Load() != 1 {
			t.Fatal("stopped consumer restarted", messages.calls.Load())
		}
	})
}

func TestMessageConsumerIteratorErrorBackoff(t *testing.T) {
	for _, stop := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			messages := newTestMessages()
			messages.results <- iteratorResult{err: errors.New("iterator failure")}
			messages.results <- iteratorResult{message: &mockMsg{}}
			received := make(chan struct{}, 1)
			c := newActiveMessageConsumer(messages, func(jetstream.Msg) { received <- struct{}{} })
			synctest.Wait()
			if messages.calls.Load() != 1 {
				t.Fatal("iterator error retried without backoff")
			}
			if !stop {
				<-received
				synctest.Wait()
			}
			c.Stop()
			synctest.Wait()
			want := int32(1)
			if !stop {
				want = 3
			}
			if messages.calls.Load() != want {
				t.Fatalf("iterator calls=%d want=%d", messages.calls.Load(), want)
			}
		})
	}
}

func TestNATSQueueCloseWaitsForRunningHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		messages := newTestMessages()
		messages.results <- iteratorResult{message: &mockMsg{}}
		entered, release := make(chan struct{}), make(chan struct{})
		consumer := newActiveMessageConsumer(messages, func(jetstream.Msg) { close(entered); <-release })
		q := newTestNATSQueue(newMockJSClient(), Config{})
		q.consumers = []consumeContext{consumer}
		<-entered
		closed := make(chan error, 1)
		go func() { closed <- q.Close() }()
		synctest.Wait()
		select {
		case err := <-closed:
			close(release)
			t.Fatalf("Close returned while handler was running: %v", err)
		default:
		}
		close(release)
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
	})
}

func TestNATSQueueCloseTimesOutWithoutClaimingCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		messages := newTestMessages()
		messages.results <- iteratorResult{message: &mockMsg{}}
		entered, release := make(chan struct{}), make(chan struct{})
		consumer := newActiveMessageConsumer(messages, func(jetstream.Msg) { close(entered); <-release; runtime.Goexit() })
		q := newTestNATSQueue(newMockJSClient(), Config{})
		q.consumers = []consumeContext{consumer}
		<-entered
		start := time.Now()
		err := q.Close()
		if err == nil || time.Since(start) != 10*time.Second {
			t.Fatalf("error=%v elapsed=%v", err, time.Since(start))
		}
		if q.Close() != err {
			t.Fatal("Close lost original error")
		}
		select {
		case <-consumer.Done():
			t.Fatal("unfinished handler reported done")
		default:
		}
		close(release)
		<-consumer.Done()
	})
}

type failingFlushClient struct {
	*mockJSClient
	err error
}

func (c failingFlushClient) Flush(time.Duration) error { return c.err }
func TestNATSQueueClosePreservesFlushFailure(t *testing.T) {
	failure := errors.New("flush failed")
	q := newTestNATSQueue(newMockJSClient(), Config{})
	q.js = failingFlushClient{newMockJSClient(), failure}
	if err := q.Close(); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := q.Close(); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}

func TestNATSQueueConcurrentCloseGoexitAndFlushFailure(t *testing.T) {
	for _, workers := range []int{1, 4} {
		t.Run(strconv.Itoa(workers), func(t *testing.T) {
			messages := newTestMessages()
			messages.results <- iteratorResult{message: &mockMsg{}}
			entered, release := make(chan struct{}), make(chan struct{})
			consumer := newMessageConsumer(messages, func(jetstream.Msg) {
				close(entered)
				<-release
				runtime.Goexit()
			}, func(error) {})
			consumer.Start(workers)
			q := newTestNATSQueue(newMockJSClient(), Config{})
			failure := errors.New("flush failed after handler exit")
			q.js = failingFlushClient{newMockJSClient(), failure}
			q.consumers = []consumeContext{consumer}
			var once sync.Once
			finish := func() { once.Do(func() { close(release) }) }
			t.Cleanup(func() { finish(); consumer.Stop(); <-consumer.Done(); _ = q.Close() })
			<-entered
			results := make(chan error, 8)
			for range cap(results) {
				go func() { results <- q.Close() }()
			}
			<-consumer.stopped
			select {
			case err := <-results:
				t.Fatalf("Close returned before Goexit: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			finish()
			first := assertConcurrentCloseFailures(t, results, failure)
			if q.Close() != first || messages.stops.Load() != 1 ||
				(consumer.parallel != nil && len(consumer.parallel.slots) != 0) {
				t.Fatalf("workers=%d left shutdown state or worker slots", workers)
			}
		})
	}
}

func assertConcurrentCloseFailures(t *testing.T, results <-chan error, failure error) error {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	var first error
	for range cap(results) {
		select {
		case err := <-results:
			if !errors.Is(err, failure) || (first != nil && first != err) {
				t.Fatalf("concurrent Close changed shutdown error: %v", err)
			}
			first = err
		case <-timer.C:
			t.Fatal("concurrent Close hung after handler Goexit")
		}
	}
	return first
}

func TestMessageConsumerCannotRunBeforeRegistration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		messages := newTestMessages()
		messages.results <- iteratorResult{message: &mockMsg{}}
		var calls atomic.Int32
		c := newMessageConsumer(messages, func(jetstream.Msg) { calls.Add(1) }, reportRenewalFailure)
		synctest.Wait()
		c.Stop()
		c.Start(1)
		synctest.Wait()
		if calls.Load() != 0 {
			t.Fatal("business handler started before registration")
		}
	})
}

func newActiveMessageConsumer(messages jetstream.MessagesContext, handler jetstream.MessageHandler) *messageConsumer {
	c := newMessageConsumer(messages, handler, reportRenewalFailure)
	c.Start(1)
	return c
}

func TestParallelConsumerGoexitPreservesWorkerSlots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		p := newParallelConsumer(func(jetstream.Msg) { calls.Add(1); runtime.Goexit() }, 4, reportRenewalFailure)
		for range 32 {
			p.slots <- struct{}{}
			p.jobs <- &mockMsg{}
		}
		close(p.jobs)
		p.workers.Wait()
		if calls.Load() != 32 {
			t.Fatalf("handled=%d", calls.Load())
		}
	})
}

func TestParallelConsumerCloseWaitsForEveryWorker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		messages := newTestMessages()
		for range 4 {
			messages.results <- iteratorResult{message: &mockMsg{}}
		}
		release := make(chan struct{})
		var entered atomic.Int32
		c := newMessageConsumer(messages, func(jetstream.Msg) { entered.Add(1); <-release; runtime.Goexit() }, reportRenewalFailure)
		c.Start(4)
		synctest.Wait()
		if entered.Load() != 4 {
			t.Fatalf("entered=%d", entered.Load())
		}
		c.Stop()
		synctest.Wait()
		select {
		case <-c.Done():
			t.Fatal("returned before workers exited")
		default:
		}
		close(release)
		<-c.Done()
	})
}

func TestMessageConsumerReservesWorkerBeforeReading(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		messages := newTestMessages()
		for range 4 {
			messages.results <- iteratorResult{message: &mockMsg{}}
		}
		release := make(chan struct{})
		var entered atomic.Int32
		c := newMessageConsumer(messages, func(jetstream.Msg) { entered.Add(1); <-release }, reportRenewalFailure)
		c.Start(2)
		synctest.Wait()
		reads, active := messages.calls.Load(), entered.Load()
		c.Stop()
		close(release)
		<-c.Done()
		if reads != 2 || active != 2 {
			t.Fatalf("read %d messages for %d occupied workers; want exactly 2", reads, active)
		}
	})
}

func TestMessageConsumerParallelReadErrorsReleaseSlots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		messages := newTestMessages()
		for range 2 {
			messages.results <- iteratorResult{err: errors.New("read failed")}
		}
		for range 2 {
			messages.results <- iteratorResult{message: &mockMsg{}}
		}
		release := make(chan struct{})
		var entered atomic.Int32
		c := newMessageConsumer(messages, func(jetstream.Msg) { entered.Add(1); <-release }, reportRenewalFailure)
		c.Start(2)
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		active := entered.Load()
		c.Stop()
		close(release)
		<-c.Done()
		if active != 2 {
			t.Fatalf("read errors leaked worker capacity: active=%d", active)
		}
	})
}

// A pull can finish concurrently with Stop and return an already received item.
type stoppedPullMessages struct {
	*testMessages
	entered chan struct{}
	release chan struct{}
	message jetstream.Msg
}

func (m *stoppedPullMessages) Next(...jetstream.NextOpt) (jetstream.Msg, error) {
	close(m.entered)
	<-m.release
	return m.message, nil
}

func TestMessageConsumerDropsPullCompletedAfterStop(t *testing.T) {
	for _, workers := range []int{1, 4} {
		synctest.Test(t, func(t *testing.T) {
			r := newDeliveryRenewal(time.Second, reportRenewalFailure)
			held := r.hold(&mockMsg{})
			held.capacity = make(chan struct{}, 1)
			held.capacity <- struct{}{}
			messages := &stoppedPullMessages{testMessages: newTestMessages(), entered: make(chan struct{}), release: make(chan struct{}), message: held}
			var calls atomic.Int32
			c := newMessageConsumer(messages, func(msg jetstream.Msg) {
				r.handle(msg, func(_ context.Context, _ jetstream.Msg) { calls.Add(1) })
			}, reportRenewalFailure)
			c.Start(workers)
			<-messages.entered
			c.Stop()
			close(messages.release)
			<-c.Done()
			if calls.Load() != 0 || len(r.active) != 0 || len(held.capacity) != 0 {
				t.Fatalf("workers=%d calls=%d active=%d capacity=%d", workers, calls.Load(), len(r.active), len(held.capacity))
			}
		})
	}
}

func TestStoppedParallelDispatchReleasesItsReservedSlot(t *testing.T) {
	c := newMessageConsumer(newTestMessages(), func(jetstream.Msg) { t.Error("stopped consumer dispatched work") }, func(error) { t.Error("unexpected diagnostic") })
	c.parallel = &parallelConsumer{jobs: make(chan jetstream.Msg), slots: make(chan struct{}, 1)}
	c.parallel.slots <- struct{}{}
	c.Stop()
	message := &mockMsg{}
	c.dispatchParallel(message)
	if len(c.parallel.slots) != 0 || message.acked.Load() || message.naked.Load() {
		t.Fatal("late dispatch retained its slot or acknowledged a message")
	}
}

type delayedStopContext struct{ *mockConsumeContext }

func (c delayedStopContext) Stop() { time.Sleep(11 * time.Second); c.mockConsumeContext.Stop() }

func TestNATSQueueRejectsAcknowledgementFlushAfterShutdownDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := newMockJSClient()
		q := newTestNATSQueue(client, Config{})
		q.js = failingFlushClient{client, errAlwaysFail}
		q.consumers = []consumeContext{delayedStopContext{&mockConsumeContext{done: make(chan struct{})}}}
		start := time.Now()
		err := q.Close()
		if err == nil || !strings.Contains(err.Error(), "before acknowledgement flush") || errors.Is(err, errAlwaysFail) || time.Since(start) != 11*time.Second {
			t.Fatalf("late flush or lost timeout: %v elapsed=%v", err, time.Since(start))
		}
		if q.Close() != err {
			t.Fatal("repeated close lost its original error")
		}
	})
}
