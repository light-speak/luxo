package queue

import (
	"context"
	"fmt"
	corequeue "github.com/light-speak/luxo/pkg/lux/queue"
	"os"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// jsClient abstracts the JetStream operations used by NATSQueue,
// enabling mock-based testing without a real NATS server.
type jsClient interface {
	// CreateOrUpdateStream creates or updates a stream.
	CreateOrUpdateStream(ctx context.Context, cfg jetstream.StreamConfig) (jsStream, error)
	// Publish publishes a message to a subject.
	Publish(ctx context.Context, subject string, payload []byte) error
}

// jsStream abstracts the stream-level operations needed for Subscribe.
type jsStream interface {
	// CreateOrUpdateConsumer creates or updates a consumer on the stream.
	CreateOrUpdateConsumer(ctx context.Context, cfg jetstream.ConsumerConfig) (jsConsumer, error)
}

// jsConsumer abstracts the consumer-level operations.
type jsConsumer interface {
	// Consume starts consuming messages with the given callback.
	Consume(handler jetstream.MessageHandler, opts ...jetstream.PullConsumeOpt) (consumeContext, error)
}

// consumeContext abstracts the Stop method of jetstream.ConsumeContext.
type consumeContext interface {
	Stop()
}

// realJSClient wraps jetstream.JetStream to satisfy jsClient.
type realJSClient struct {
	js jetstream.JetStream
}

func (r *realJSClient) CreateOrUpdateStream(ctx context.Context, cfg jetstream.StreamConfig) (jsStream, error) {
	s, err := r.js.CreateOrUpdateStream(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &realJSStream{s: s}, nil
}

func (r *realJSClient) Publish(ctx context.Context, subject string, payload []byte) error {
	_, err := r.js.Publish(ctx, subject, payload)
	return err
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

func (r *realJSConsumer) Consume(handler jetstream.MessageHandler, opts ...jetstream.PullConsumeOpt) (consumeContext, error) {
	return r.c.Consume(handler, opts...)
}

// NATSQueue implements Queue using NATS JetStream.
// Each queue name maps to a JetStream stream with a durable pull consumer.
// Failed tasks are retried with exponential backoff capped at one minute.
// After MaxRetries deliveries, only transfer to {queue}.dlq is retried; the
// original remains unacknowledged until JetStream confirms that publication.
// Transfer is at-least-once: an uncertain publish/ack may duplicate a DLQ entry.
type NATSQueue struct {
	conn      *nats.Conn // nil if we don't own the connection
	js        jsClient
	cfg       Config
	mu        sync.Mutex
	consumers []consumeContext
	once      sync.Once
	closed    bool
	streams   map[string]bool // cache of known streams to avoid ensureStream on every Publish
}

var _ Queue = (*NATSQueue)(nil)

// NewNATSQueue connects to NATS and creates a JetStream context.
// The returned NATSQueue owns the connection and will close it on Close().
func NewNATSQueue(url string, cfg Config) (*NATSQueue, error) {
	cfg.SetDefaults()
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
	if conn == nil {
		return nil, fmt.Errorf("queue: NATS connection is required")
	}
	cfg.SetDefaults()
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

// streamName converts a queue name to a valid JetStream stream name.
// Dots are replaced with underscores since dots are subject separators in NATS.
func streamName(queue string) string {
	buf := make([]byte, len(queue))
	for i := range len(queue) {
		if queue[i] == '.' {
			buf[i] = '_'
		} else {
			buf[i] = queue[i]
		}
	}
	return "luxo_q_" + string(buf)
}

// ensureStream creates or updates the stream for the given queue name.
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
	stream, err := n.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
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

// getOrCreateStream ensures the stream exists and returns the stream handle.
// Unlike ensureStream, this always returns a usable stream (needed for Subscribe).
func (n *NATSQueue) getOrCreateStream(ctx context.Context, queue string) (jsStream, error) {
	name := streamName(queue)
	stream, err := n.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
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
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return fmt.Errorf("queue: closed")
	}
	n.mu.Unlock()

	ctx := context.Background()

	stream, err := n.getOrCreateStream(ctx, queue)
	if err != nil {
		return fmt.Errorf("queue subscribe ensure stream: %w", err)
	}

	name := streamName(queue)

	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:   name + "_worker",
		AckPolicy: jetstream.AckExplicitPolicy,
		// Business attempts are bounded in handleDelivery. Broker redelivery
		// must remain available until the DLQ transfer is confirmed.
		MaxDeliver: -1,
		BackOff:    n.retrySchedule(),
	})
	if err != nil {
		return fmt.Errorf("queue subscribe consumer: %w", err)
	}

	consumeCtx, err := consumer.Consume(func(msg jetstream.Msg) {
		n.handleDelivery(queue, handler, msg)
	})
	if err != nil {
		return fmt.Errorf("queue subscribe consume: %w", err)
	}

	n.mu.Lock()
	n.consumers = append(n.consumers, consumeCtx)
	n.mu.Unlock()

	return nil
}

// publishDLQ publishes a failed task to the dead-letter queue.
func (n *NATSQueue) publishDLQ(queue string, payload []byte) error {
	dlq := queue + dlqSuffix
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
func (n *NATSQueue) handleDelivery(queue string, handler Handler, msg jetstream.Msg) {
	delivered, err := natsDeliveryCount(msg.Reply())
	if err != nil {
		fmt.Fprintf(os.Stderr, "queue %s: invalid delivery metadata: %v\n", queue, err)
		n.retryDelivery(queue, msg, 1)
		return
	}
	if delivered <= uint64(n.cfg.MaxRetries) {
		if err := corequeue.InvokeHandler(handler, context.Background(), msg.Data()); err == nil {
			n.ackDelivery(queue, msg)
			return
		}
		if delivered < uint64(n.cfg.MaxRetries) {
			n.retryDelivery(queue, msg, delivered)
			return
		}
	}
	if err := n.publishDLQ(queue, msg.Data()); err != nil {
		fmt.Fprintf(os.Stderr, "queue %s: retaining task after failed transfer: %v\n", queue, err)
		n.retryDelivery(queue, msg, delivered)
		return
	}
	n.ackDelivery(queue, msg)
}

func (n *NATSQueue) ackDelivery(queue string, msg jetstream.Msg) {
	if err := msg.Ack(); err != nil {
		fmt.Fprintf(os.Stderr, "queue %s: ack failed; task may be redelivered: %v\n", queue, err)
	}
}

func (n *NATSQueue) retryDelivery(queue string, msg jetstream.Msg, delivery uint64) {
	// BackOff only controls ack timeouts, not an explicit Nak.
	if err := msg.NakWithDelay(n.retryDelay(delivery)); err != nil {
		fmt.Fprintf(os.Stderr, "queue %s: delayed nak failed; awaiting ack timeout: %v\n", queue, err)
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

// Close stops all consumers and closes the NATS connection (if owned).
func (n *NATSQueue) Close() error {
	var closeErr error
	n.once.Do(func() {
		n.mu.Lock()
		n.closed = true
		consumers := n.consumers
		n.consumers = nil
		conn := n.conn
		n.mu.Unlock()

		// Stop all consumers.
		for _, c := range consumers {
			c.Stop()
		}

		// Close NATS connection if we own it.
		if conn != nil {
			if err := conn.Drain(); err != nil {
				closeErr = fmt.Errorf("queue nats drain: %w", err)
			}
		}
	})
	return closeErr
}
