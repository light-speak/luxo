package queue

import (
	"context"
	"fmt"
	corequeue "github.com/light-speak/luxo/pkg/lux/queue"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// jsClient abstracts the JetStream operations used by NATSQueue,
// enabling mock-based testing without a real NATS server.
type jsClient interface {
	// CreateStream creates a stream or reopens an identical configuration.
	CreateStream(ctx context.Context, cfg jetstream.StreamConfig) (jsStream, error)
	// Publish publishes a message to a subject.
	Publish(ctx context.Context, subject string, payload []byte) error
	Flush(time.Duration) error
	FlushWithContext(context.Context) error
	serverJetStreamEnabled() bool
}

// jsStream abstracts the stream-level operations needed for Subscribe.
type jsStream interface {
	// CreateOrUpdateConsumer creates or updates a consumer on the stream.
	CreateOrUpdateConsumer(ctx context.Context, cfg jetstream.ConsumerConfig) (jsConsumer, error)
}

// jsConsumer abstracts the consumer-level operations.
type jsConsumer interface {
	// Consume starts consuming messages with the given callback.
	Consume(handler jetstream.MessageHandler, renewal *deliveryRenewal) (consumeContext, error)
}

// consumeContext separates iterator creation from registered delivery.
type consumeContext interface {
	Start(int)
	Stop()
	Done() <-chan struct{}
}

// realJSClient wraps jetstream.JetStream to satisfy jsClient.
type realJSClient struct {
	js jetstream.JetStream
}

func (r *realJSClient) CreateStream(ctx context.Context, cfg jetstream.StreamConfig) (jsStream, error) {
	s, err := r.js.CreateStream(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &realJSStream{s: s}, nil
}

func (r *realJSClient) Publish(ctx context.Context, subject string, payload []byte) error {
	_, err := r.js.Publish(ctx, subject, payload)
	return err
}

func (r *realJSClient) Flush(timeout time.Duration) error {
	return r.js.Conn().FlushTimeout(timeout)
}

func (r *realJSClient) FlushWithContext(ctx context.Context) error {
	return r.js.Conn().FlushWithContext(ctx)
}

func (r *realJSClient) serverJetStreamEnabled() bool {
	enabled, _ := r.js.Conn().ConnectedServerJetStream()
	return enabled
}

// realJSStream wraps jetstream.Stream to satisfy jsStream.
type realJSStream struct {
	s jetstream.Stream
}

func (r *realJSStream) CreateOrUpdateConsumer(ctx context.Context, cfg jetstream.ConsumerConfig) (jsConsumer, error) {
	c, err := r.s.CreateOrUpdateConsumer(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &realJSConsumer{c: c}, nil
}

// realJSConsumer wraps jetstream.Consumer to satisfy jsConsumer.
type realJSConsumer struct {
	c jetstream.Consumer
}

func (r *realJSConsumer) Consume(handler jetstream.MessageHandler, renewal *deliveryRenewal) (consumeContext, error) {
	return newBatchConsumer(r.c, renewal, handler), nil
}

// NATSQueue implements Queue using NATS JetStream.
// Each queue name maps to a JetStream stream with a durable pull consumer.
// Failed tasks are retried with exponential backoff capped at one minute.
// After MaxRetries deliveries, only transfer to {queue}.dlq is retried; the
// original remains unacknowledged until JetStream confirms that publication.
// Transfer is at-least-once: an uncertain publish/ack may duplicate a DLQ entry.
type NATSQueue struct {
	conn            *nats.Conn // nil if we don't own the connection
	js              jsClient
	cfg             Config
	mu              sync.Mutex
	consumers       []consumeContext
	subscriptions   map[string]struct{}
	once            sync.Once
	closed          bool
	reports         *renewalReports
	reportsRejected atomic.Uint64
	closeErr        error           // Published to all Close callers by once.
	streams         map[string]bool // cache of known streams to avoid ensureStream on every Publish
}

var _ Queue = (*NATSQueue)(nil)

// NewNATSQueue connects to NATS and creates a JetStream context.
// The returned NATSQueue owns the connection and will close it on Close().
func NewNATSQueue(url string, cfg Config) (*NATSQueue, error) {
	cfg.SetDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	conn, err := nats.Connect(url)
	if err != nil {
		return nil, fmt.Errorf("queue nats connect: %w", err)
	}
	js, err := jetstream.New(conn)
	if err != nil {
		// nats.go v1.53.1 only returns option errors; none are passed here. Keep propagation for upgrades.
		conn.Close()
		return nil, fmt.Errorf("queue jetstream: %w", err)
	}
	return &NATSQueue{
		conn:    conn,
		js:      &realJSClient{js: js},
		cfg:     cfg,
		streams: make(map[string]bool),
	}, nil
}

// NewNATSQueueFromConn creates a NATSQueue from an existing NATS connection.
// The caller retains ownership of the connection; Close will NOT close it.
func NewNATSQueueFromConn(conn *nats.Conn, cfg Config) (*NATSQueue, error) {
	cfg.SetDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if conn == nil {
		return nil, fmt.Errorf("queue: NATS connection is required")
	}
	js, err := jetstream.New(conn)
	if err != nil {
		// nats.go v1.53.1 only returns option errors; none are passed here. Keep propagation for upgrades.
		return nil, fmt.Errorf("queue jetstream: %w", err)
	}
	return &NATSQueue{
		// conn left nil — we don't own it, so Close won't close it.
		js:      &realJSClient{js: js},
		cfg:     cfg,
		streams: make(map[string]bool),
	}, nil
}

// streamName uses an injective escape so subject separators never alias underscores.
func streamName(queue string) string {
	extra := strings.Count(queue, ".") + strings.Count(queue, "_")
	if extra == 0 {
		return "luxo_q_" + queue
	}
	buf := make([]byte, len("luxo_q_")+len(queue)+extra)
	offset := copy(buf, "luxo_q_")
	for i := range len(queue) {
		switch queue[i] {
		case '.':
			buf[offset], buf[offset+1] = '_', 'd'
			offset += 2
		case '_':
			buf[offset], buf[offset+1] = '_', '_'
			offset += 2
		default:
			buf[offset] = queue[i]
			offset++
		}
	}
	return string(buf)
}

// ensureStream creates or reopens an identically configured stream.
// Conflicting persisted configurations are never silently overwritten.
// Results are cached so subsequent calls for the same queue skip the RPC.
func (n *NATSQueue) ensureStream(ctx context.Context, queue string) (jsStream, error) {
	n.mu.Lock()
	if n.streams[queue] {
		n.mu.Unlock()
		// Stream known to exist; return nil stream since caller may not need it.
		return nil, nil
	}
	n.mu.Unlock()

	name := streamName(queue)
	stream, err := n.js.CreateStream(ctx, jetstream.StreamConfig{
		Name:     name,
		Subjects: []string{queue},
		// Retain tasks until explicitly acknowledged.
		Retention: jetstream.WorkQueuePolicy,
		// Reject new tasks at capacity instead of deleting unacknowledged work.
		Discard: jetstream.DiscardNew,
	})
	if err != nil {
		return nil, err
	}

	n.mu.Lock()
	n.streams[queue] = true
	n.mu.Unlock()
	return stream, nil
}

// CheckReady verifies the broker acknowledges the queue's connection.
func (n *NATSQueue) CheckReady(ctx context.Context) error {
	n.mu.Lock()
	closed := n.closed
	n.mu.Unlock()
	if closed {
		return fmt.Errorf("queue: closed")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return n.js.FlushWithContext(ctx)
}

// CheckDirectJetStreamReady verifies an ordinary connection to a server that
// advertises JetStream. Use it when the queue connects directly to its managed
// broker. The flag describes the server, while routed JetStream may be remote.
func (n *NATSQueue) CheckDirectJetStreamReady(ctx context.Context) error {
	if err := n.CheckReady(ctx); err != nil {
		return err
	}
	if !n.js.serverJetStreamEnabled() {
		return fmt.Errorf("queue: direct NATS server does not advertise JetStream")
	}
	return nil
}

// getOrCreateStream ensures the stream exists and returns the stream handle.
// Unlike ensureStream, this always returns a usable stream (needed for Subscribe).
func (n *NATSQueue) getOrCreateStream(ctx context.Context, queue string) (jsStream, error) {
	name := streamName(queue)
	stream, err := n.js.CreateStream(ctx, jetstream.StreamConfig{
		Name:      name,
		Subjects:  []string{queue},
		Retention: jetstream.WorkQueuePolicy,
		Discard:   jetstream.DiscardNew,
	})
	if err != nil {
		return nil, err
	}

	n.mu.Lock()
	n.streams[queue] = true
	n.mu.Unlock()
	return stream, nil
}

// Publish sends a task to the named queue via JetStream.
func (n *NATSQueue) Publish(ctx context.Context, queue string, payload []byte) error {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return fmt.Errorf("queue: closed")
	}
	n.mu.Unlock()

	// Ensure stream exists (cached after first call).
	if _, err := n.ensureStream(ctx, queue); err != nil {
		return fmt.Errorf("queue publish ensure stream: %w", err)
	}

	// Copy payload to prevent caller mutation.
	cp := make([]byte, len(payload))
	copy(cp, payload)

	if err := n.js.Publish(ctx, queue, cp); err != nil {
		return fmt.Errorf("queue publish: %w", err)
	}
	return nil
}

// Subscribe creates a durable consumer and starts pulling messages.
// The handler is called within the business delivery budget; failures use a
// delayed nak. Exhausted tasks only retry DLQ transfer, including after restart.
func (n *NATSQueue) Subscribe(queue string, handler Handler) error {
	if handler == nil {
		return fmt.Errorf("queue: handler is required")
	}
	if err := n.reserveSubscription(queue); err != nil {
		return err
	}
	registered := false
	defer func() {
		if !registered {
			n.mu.Lock()
			delete(n.subscriptions, queue)
			n.mu.Unlock()
		}
	}()
	consumeCtx, err := n.createSubscription(queue, handler)
	if err != nil {
		return err
	}
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		consumeCtx.Stop()
		consumeCtx.Start(1)
		return fmt.Errorf("queue: closed")
	}
	n.consumers = append(n.consumers, consumeCtx)
	consumeCtx.Start(n.cfg.Concurrency)
	registered = true
	n.mu.Unlock()
	return nil
}

func (n *NATSQueue) reserveSubscription(queue string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return fmt.Errorf("queue: closed")
	}
	if _, exists := n.subscriptions[queue]; exists {
		return fmt.Errorf("queue: %q already subscribed", queue)
	}
	if n.subscriptions == nil {
		n.subscriptions = make(map[string]struct{})
	}
	n.subscriptions[queue] = struct{}{}
	return nil
}

func (n *NATSQueue) createSubscription(queue string, handler Handler) (consumeContext, error) {
	ctx := context.Background()
	stream, err := n.getOrCreateStream(ctx, queue)
	if err != nil {
		return nil, fmt.Errorf("queue subscribe ensure stream: %w", err)
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:   streamName(queue) + "_worker",
		AckPolicy: jetstream.AckExplicitPolicy,
		// Business attempts are bounded; broker redelivery remains available for DLQ transfer.
		MaxDeliver: -1,
		BackOff:    n.retrySchedule(),
	})
	if err != nil {
		return nil, fmt.Errorf("queue subscribe consumer: %w", err)
	}
	renewal := newDeliveryRenewal(n.retryDelay(1), n.reportRenewal)
	consumeCtx, err := consumer.Consume(func(msg jetstream.Msg) {
		renewal.handle(msg, func(ctx context.Context, held jetstream.Msg) {
			n.handleDelivery(ctx, queue, handler, held)
		})
	}, renewal)
	if err != nil {
		return nil, fmt.Errorf("queue subscribe consume: %w", err)
	}
	return &renewingConsumer{consumeContext: consumeCtx, renewal: renewal, done: make(chan struct{})}, nil
}

// publishDLQ publishes a failed task to the dead-letter queue.
func (n *NATSQueue) publishDLQ(parent context.Context, queue string, payload []byte) error {
	dlq := queue + dlqSuffix
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()

	// Ensure DLQ stream exists.
	if _, err := n.ensureStream(ctx, dlq); err != nil {
		return fmt.Errorf("DLQ stream create: %w", err)
	}
	if err := n.js.Publish(ctx, dlq, payload); err != nil {
		return fmt.Errorf("DLQ publish: %w", err)
	}
	return nil
}

// handleDelivery uses broker metadata so an exhausted task remains exhausted
// after reconnect or worker restart. A lost publish/ack response may duplicate
// the DLQ entry; no local flag is treated as proof of durable transfer.
func (n *NATSQueue) handleDelivery(ctx context.Context, queue string, handler Handler, msg jetstream.Msg) {
	if ctx.Err() != nil {
		return
	}
	delivered, err := natsDeliveryCount(msg.Reply())
	if err != nil {
		n.reportRenewal(queueDiagnostic{queue: queue, operation: "invalid delivery metadata", cause: err})
		n.retryDelivery(queue, msg, 1)
		return
	}
	if delivered <= uint64(n.cfg.MaxRetries) {
		n.invokeDelivery(ctx, queue, handler, msg, delivered)
		return
	}
	n.failedDelivery(ctx, queue, msg, delivered)
}

func (n *NATSQueue) invokeDelivery(ctx context.Context, queue string, handler Handler, msg jetstream.Msg, delivered uint64) {
	returned := false
	defer func() {
		if !returned {
			n.failedDelivery(ctx, queue, msg, delivered)
		}
	}()
	err := corequeue.InvokeHandler(handler, ctx, msg.Data())
	returned = true
	if ctx.Err() != nil {
		return
	}
	if err == nil {
		n.ackDelivery(queue, msg)
		return
	}
	n.failedDelivery(ctx, queue, msg, delivered)
}

func (n *NATSQueue) failedDelivery(ctx context.Context, queue string, msg jetstream.Msg, delivered uint64) {
	if ctx.Err() != nil {
		return
	}
	if delivered < uint64(n.cfg.MaxRetries) {
		n.retryDelivery(queue, msg, delivered)
		return
	}
	if err := n.publishDLQ(ctx, queue, msg.Data()); err != nil {
		n.reportRenewal(queueDiagnostic{queue: queue, operation: "retaining task after failed transfer", cause: err})
		n.retryDelivery(queue, msg, delivered)
		return
	}
	n.ackDelivery(queue, msg)
}

func (n *NATSQueue) ackDelivery(queue string, msg jetstream.Msg) {
	if err := msg.Ack(); err != nil {
		n.reportRenewal(queueDiagnostic{queue: queue, operation: "ack failed; task may be redelivered", cause: err})
	}
}

func (n *NATSQueue) retryDelivery(queue string, msg jetstream.Msg, delivery uint64) {
	// BackOff only controls ack timeouts, not an explicit Nak.
	if err := msg.NakWithDelay(n.retryDelay(delivery)); err != nil {
		n.reportRenewal(queueDiagnostic{queue: queue, operation: "delayed nak failed; awaiting ack timeout", cause: err})
	}
}

const maxNATSRetryDelay = time.Minute

func (n *NATSQueue) retryDelay(delivery uint64) time.Duration {
	delay := min(n.cfg.RetryDelay, maxNATSRetryDelay)
	for delivery > 1 && delay < maxNATSRetryDelay {
		delay = min(delay*2, maxNATSRetryDelay)
		delivery--
	}
	return delay
}

func (n *NATSQueue) retrySchedule() []time.Duration {
	delay := n.retryDelay(1)
	schedule := []time.Duration{delay}
	for len(schedule) < n.cfg.MaxRetries && delay < maxNATSRetryDelay {
		delay = min(delay*2, maxNATSRetryDelay)
		schedule = append(schedule, delay)
	}
	return schedule
}

// Close stops delivery, waits for handlers, and flushes acknowledgements before
// closing an owned connection. A timeout reports incomplete shutdown explicitly.
func (n *NATSQueue) Close() error {
	n.once.Do(n.close)
	return n.closeErr
}

func (n *NATSQueue) close() {
	deadline := time.Now().Add(10 * time.Second)
	defer n.closeReports(deadline)
	n.mu.Lock()
	n.closed = true
	consumers := n.consumers
	n.consumers = nil
	n.mu.Unlock()
	if n.conn != nil {
		defer n.conn.Close()
	}
	for _, c := range consumers {
		c.Stop()
	}
	if err := waitConsumers(consumers, deadline); err != nil {
		n.closeErr = err
		return
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		n.closeErr = fmt.Errorf("queue: shutdown timed out before acknowledgement flush")
		return
	}
	if err := n.js.Flush(min(remaining, nats.DefaultTimeout)); err != nil {
		n.closeErr = fmt.Errorf("queue nats acknowledgement flush: %w", err)
	}
}

// Already stopped consumers need no shutdown timer allocation.
func waitConsumers(consumers []consumeContext, deadline time.Time) error {
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for _, consumer := range consumers {
		select {
		case <-consumer.Done():
			continue
		default:
		}
		if timer == nil {
			timer = time.NewTimer(time.Until(deadline))
		}
		select {
		case <-consumer.Done():
		case <-timer.C:
			return fmt.Errorf("queue: shutdown timed out waiting for handlers")
		}
	}
	return nil
}
