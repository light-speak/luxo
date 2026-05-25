package queue

import (
	"context"
	"fmt"
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
// Failed tasks are retried with exponential backoff; after MaxRetries,
// published to {queue}.dlq.
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
	cfg.validate()
	conn, err := nats.Connect(url)
	if err != nil {
		return nil, fmt.Errorf("queue nats connect: %w", err)
	}
	js, err := jetstream.New(conn)
	if err != nil {
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
	cfg.validate()
	js, err := jetstream.New(conn)
	if err != nil {
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
		// Discard old tasks when storage limit is reached.
		Discard: jetstream.DiscardOld,
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
		Discard:   jetstream.DiscardOld,
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
// The handler is called for each task; ack on nil error, nak on error.
// After MaxRetries, the task is published to {queue}.dlq.
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

	// Build exponential backoff schedule.
	backoff := make([]time.Duration, n.cfg.MaxRetries)
	delay := n.cfg.RetryDelay
	for i := range n.cfg.MaxRetries {
		backoff[i] = delay
		delay *= 2
	}

	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:    name + "_worker",
		AckPolicy:  jetstream.AckExplicitPolicy,
		MaxDeliver: n.cfg.MaxRetries,
		BackOff:    backoff,
	})
	if err != nil {
		return fmt.Errorf("queue subscribe consumer: %w", err)
	}

	consumeCtx, err := consumer.Consume(func(msg jetstream.Msg) {
		if err := safeCall(handler, context.Background(), msg.Data()); err != nil {
			// Check if this is the last delivery attempt.
			meta, metaErr := msg.Metadata()
			if metaErr == nil && int(meta.NumDelivered) >= n.cfg.MaxRetries {
				// Last attempt failed — send to DLQ and ack to remove from stream.
				n.publishDLQ(queue, msg.Data())
				msg.Ack()
				return
			}
			msg.Nak()
			return
		}
		msg.Ack()
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
func (n *NATSQueue) publishDLQ(queue string, payload []byte) {
	dlq := queue + dlqSuffix
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Ensure DLQ stream exists.
	if _, err := n.ensureStream(ctx, dlq); err != nil {
		fmt.Fprintf(os.Stderr, "queue %s: DLQ stream create failed: %v\n", queue, err)
		return
	}
	if err := n.js.Publish(ctx, dlq, payload); err != nil {
		fmt.Fprintf(os.Stderr, "queue %s: DLQ publish failed: %v\n", queue, err)
	}
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
