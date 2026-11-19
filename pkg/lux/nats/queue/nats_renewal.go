package queue

import (
	"context"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// deliveryRenewal uses one scheduler per subscription, never one per task.
// Broker delivery remains at-least-once, including uncertain progress writes.
type deliveryRenewal struct {
	mu       sync.Mutex
	active   map[*renewedMessage]struct{}
	interval time.Duration
	report   func(error)
}

type renewedMessage struct {
	jetstream.Msg
	owner    *deliveryRenewal
	ctx      context.Context
	cancel   context.CancelCauseFunc
	finished bool
	capacity chan struct{}
}

func newDeliveryRenewal(ackWait time.Duration, report func(error)) *deliveryRenewal {
	return &deliveryRenewal{report: report, active: make(map[*renewedMessage]struct{}), interval: max(ackWait/3, time.Nanosecond)}
}

func (r *deliveryRenewal) hold(msg jetstream.Msg) *renewedMessage {
	ctx, cancel := context.WithCancelCause(context.Background())
	entry := &renewedMessage{Msg: msg, owner: r, ctx: ctx, cancel: cancel}
	r.mu.Lock()
	r.active[entry] = struct{}{}
	r.mu.Unlock()
	return entry
}

func (m *renewedMessage) release() {
	m.owner.mu.Lock()
	delete(m.owner.active, m)
	m.owner.mu.Unlock()
	m.cancel(nil)
	if m.capacity != nil {
		<-m.capacity
	}
}

func (r *deliveryRenewal) handle(msg jetstream.Msg, handle func(context.Context, jetstream.Msg)) {
	entry, ok := msg.(*renewedMessage)
	if !ok {
		entry = r.hold(msg)
	}
	defer entry.release()
	handle(entry.ctx, entry)
}

func (r *deliveryRenewal) run(done <-chan struct{}) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			r.renew()
		}
	}
}

func (r *deliveryRenewal) renew() { r.renewAndReport(r.report) }

func (r *deliveryRenewal) renewAndReport(report func(error)) {
	for _, err := range r.renewActive() {
		report(err)
	}
}

func (r *deliveryRenewal) renewActive() []error {
	var failures []error
	r.mu.Lock()
	defer r.mu.Unlock()
	for entry := range r.active {
		if entry.finished || entry.ctx.Err() != nil {
			continue
		}
		if err := entry.Msg.InProgress(); err != nil {
			entry.cancel(err)
			failures = append(failures, err)
		}
	}
	return failures
}

func (m *renewedMessage) Ack() error {
	m.owner.mu.Lock()
	defer m.owner.mu.Unlock()
	if err := context.Cause(m.ctx); err != nil {
		return err
	}
	m.finished = true
	return m.Msg.Ack()
}

func (m *renewedMessage) NakWithDelay(delay time.Duration) error {
	m.owner.mu.Lock()
	defer m.owner.mu.Unlock()
	if err := context.Cause(m.ctx); err != nil {
		return err
	}
	m.finished = true
	return m.Msg.NakWithDelay(delay)
}

// Renewal continues during graceful shutdown until accepted handlers finish.
type renewingConsumer struct {
	consumeContext
	renewal *deliveryRenewal
	done    chan struct{}
}

func (c *renewingConsumer) Start(workers int) {
	go func() { c.renewal.run(c.consumeContext.Done()); close(c.done) }()
	c.consumeContext.Start(workers)
}

func (c *renewingConsumer) Done() <-chan struct{} { return c.done }
