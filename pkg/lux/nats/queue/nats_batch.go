package queue

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	queuePullBatch     = 128
	queuePrefetchLimit = 128
)

type batchFetcher interface {
	Fetch(int, ...jetstream.FetchOpt) (jetstream.MessageBatch, error)
}

// Reserve complete batches before pulling. Received messages enter renewal
// immediately, including queued work; no untracked broker prefetch is retained.
type batchMessages struct {
	source  batchFetcher
	renewal *deliveryRenewal
	ctx     context.Context
	cancel  context.CancelFunc
	jobs    chan jetstream.Msg
	slots   chan struct{}
	once    sync.Once
}

type batchConsumer struct {
	*messageConsumer
	messages *batchMessages
	done     chan struct{}
}

func newBatchConsumer(source batchFetcher, renewal *deliveryRenewal, handler jetstream.MessageHandler) *batchConsumer {
	ctx, cancel := context.WithCancel(context.Background())
	messages := &batchMessages{source: source, renewal: renewal, ctx: ctx, cancel: cancel}
	return &batchConsumer{messageConsumer: newMessageConsumer(messages, handler, renewal.report), messages: messages, done: make(chan struct{})}
}

func (b *batchMessages) initialize(workers int) {
	// Keep the prefetch bound independent from pull batch tuning.
	b.slots = make(chan struct{}, queuePrefetchLimit+workers)
	b.jobs = make(chan jetstream.Msg, queuePrefetchLimit+workers)
}

func (c *batchConsumer) Start(workers int) {
	c.messages.initialize(workers)
	c.messageConsumer.Start(workers)
	go func() {
		c.messages.run()
		<-c.messageConsumer.Done()
		close(c.done)
	}()
}
func (c *batchConsumer) Done() <-chan struct{} { return c.done }

func (b *batchMessages) Next(...jetstream.NextOpt) (jetstream.Msg, error) {
	select {
	case <-b.ctx.Done():
		return nil, jetstream.ErrMsgIteratorClosed
	case msg, ok := <-b.jobs:
		if !ok {
			return nil, jetstream.ErrMsgIteratorClosed
		}
		return msg, nil
	}
}
func (b *batchMessages) Stop()  { b.once.Do(b.cancel) }
func (b *batchMessages) Drain() { b.Stop() }

func (b *batchMessages) run() {
	defer b.discardWaiting()
	for b.reserveBatch() {
		err := b.fetchBatch()
		if err != nil && b.ctx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, nats.ErrTimeout) {
			b.renewal.report(queueDiagnostic{operation: "batch pull failed; retrying", cause: err})
			select {
			case <-b.ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
}

func (b *batchMessages) reserveBatch() bool {
	for i := 0; i < queuePullBatch; i++ {
		select {
		case b.slots <- struct{}{}:
		case <-b.ctx.Done():
			b.releaseSlots(i)
			return false
		}
	}
	return true
}

func (b *batchMessages) fetchBatch() error {
	received := 0
	defer func() { b.releaseSlots(queuePullBatch - received) }()
	// Use the client default long pull expiry; shutdown still cancels immediately.
	// A short per-batch deadline repeatedly recreates subscriptions while idle.
	batch, err := b.source.Fetch(queuePullBatch, jetstream.FetchContext(b.ctx))
	if err != nil {
		return err
	}
	for msg := range batch.Messages() {
		received++
		if b.ctx.Err() != nil {
			b.releaseSlots(1)
			continue
		}
		held := b.renewal.hold(msg)
		held.capacity = b.slots
		b.jobs <- held // Every queued item already owns a reserved capacity slot.
	}
	return batch.Error()
}

func (b *batchMessages) releaseSlots(count int) {
	for range count {
		<-b.slots
	}
}

func (b *batchMessages) discardWaiting() {
	close(b.jobs)
	for msg := range b.jobs {
		msg.(*renewedMessage).release()
	}
}
