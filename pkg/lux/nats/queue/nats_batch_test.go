package queue

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

type readyBatch struct {
	messages chan jetstream.Msg
	err      error
}

func (b *readyBatch) Messages() <-chan jetstream.Msg { return b.messages }
func (b *readyBatch) Error() error                   { return b.err }

type batchSource struct{ delivered atomic.Int32 }

func (s *batchSource) Fetch(count int, _ ...jetstream.FetchOpt) (jetstream.MessageBatch, error) {
	batch := &readyBatch{messages: make(chan jetstream.Msg, count)}
	for range count {
		batch.messages <- &progressMessage{mockMsg: &mockMsg{}}
	}
	close(batch.messages)
	s.delivered.Add(int32(count))
	return batch, nil
}

func TestBatchIntakeBoundsAndRenewsWaitingMessages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source := &batchSource{}
		renewal := newDeliveryRenewal(30*time.Millisecond, reportRenewalFailure)
		c := newBatchConsumer(source, renewal, func(jetstream.Msg) {})
		// Isolate intake: no worker drains the queue in this test.
		c.messages.initialize(1)
		done := make(chan struct{})
		go func() { c.messages.run(); close(done) }()
		synctest.Wait()
		if got := source.delivered.Load(); got != queuePrefetchLimit {
			t.Fatalf("unbounded intake: %d", got)
		}
		renewal.renew()
		renewal.mu.Lock()
		for message := range renewal.active {
			if message.Msg.(*progressMessage).progress.Load() != 1 {
				t.Error("waiting message not renewed")
			}
		}
		count := len(renewal.active)
		renewal.mu.Unlock()
		if count != queuePrefetchLimit {
			t.Fatal("waiting messages not tracked", count)
		}
		c.messages.Stop()
		<-done
		if len(renewal.active) != 0 {
			t.Fatal("discarded intake retained renewal entries")
		}
	})
}

type fetchBatchFunc func(int) (jetstream.MessageBatch, error)

func (f fetchBatchFunc) Fetch(count int, _ ...jetstream.FetchOpt) (jetstream.MessageBatch, error) {
	return f(count)
}

func TestBatchIntakeReleasesUnusedReservations(t *testing.T) {
	failure := errors.New("batch failed")
	for _, count := range []int{0, 1, queuePullBatch - 1, queuePullBatch} {
		for _, mode := range []string{"complete", "partial-error", "cancelled", "fetch-error"} {
			t.Run(fmt.Sprintf("%d/%s", count, mode), func(t *testing.T) {
				renewal := newDeliveryRenewal(time.Second, reportRenewalFailure)
				var messages *batchMessages
				source := fetchBatchFunc(func(requested int) (jetstream.MessageBatch, error) {
					if requested != queuePullBatch {
						t.Fatal(requested)
					}
					if mode == "fetch-error" {
						return nil, failure
					}
					batch := &readyBatch{messages: make(chan jetstream.Msg, count)}
					for range count {
						batch.messages <- &mockMsg{}
					}
					close(batch.messages)
					if mode == "partial-error" {
						batch.err = failure
					}
					if mode == "cancelled" {
						messages.Stop()
					}
					return batch, nil
				})
				c := newBatchConsumer(source, renewal, func(jetstream.Msg) {})
				messages = c.messages
				messages.initialize(1)
				if !messages.reserveBatch() {
					t.Fatal("reservation failed")
				}
				err := messages.fetchBatch()
				wantsError := mode == "partial-error" || mode == "fetch-error"
				if wantsError != errors.Is(err, failure) {
					t.Fatal("lost batch result", err)
				}
				want := count
				if mode == "cancelled" || mode == "fetch-error" {
					want = 0
				}
				if len(messages.slots) != want || len(renewal.active) != want || len(messages.jobs) != want {
					t.Fatalf("reservation leak: slots=%d active=%d queued=%d want=%d", len(messages.slots), len(renewal.active), len(messages.jobs), want)
				}
				messages.Stop()
				messages.discardWaiting()
				if len(messages.slots) != 0 || len(renewal.active) != 0 {
					t.Fatal("shutdown retained received messages")
				}
			})
		}
	}
}

func TestBatchIntakeCancellationReleasesPartialReservation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newDeliveryRenewal(time.Second, reportRenewalFailure)
		c := newBatchConsumer(&batchSource{}, r, func(jetstream.Msg) {})
		c.messages.initialize(1)
		existing := cap(c.messages.slots) - 3
		for range existing {
			c.messages.slots <- struct{}{}
		}
		result := make(chan bool, 1)
		go func() { result <- c.messages.reserveBatch() }()
		synctest.Wait()
		if len(c.messages.slots) != cap(c.messages.slots) {
			t.Fatal("reservation never reached capacity")
		}
		c.messages.Stop()
		if <-result {
			t.Fatal("cancelled reservation succeeded")
		}
		if len(c.messages.slots) != existing {
			t.Fatal("partial reservation retained slots")
		}
		c.messages.releaseSlots(existing)
	})
}

func TestBatchClosedInputAndDrainRejectFurtherReads(t *testing.T) {
	renewal := newDeliveryRenewal(time.Second, reportRenewalFailure)
	consumer := newBatchConsumer(&batchSource{}, renewal, func(jetstream.Msg) { t.Error("closed intake delivered a message") })
	consumer.messages.initialize(1)
	close(consumer.messages.jobs)
	message, err := consumer.messages.Next()
	if message != nil || !errors.Is(err, jetstream.ErrMsgIteratorClosed) {
		t.Fatalf("closed input: %v %v", message, err)
	}
	consumer.messages.Drain()
	if !errors.Is(consumer.messages.ctx.Err(), context.Canceled) {
		t.Fatal("Drain did not stop intake")
	}
}
