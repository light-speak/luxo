package event

import (
	"context"
	"fmt"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/nats-io/nats.go"
)

// NATSBus implements Bus using NATS messaging.
// Used in multi-service mode for cross-process event delivery.
// Serializes payloads to JSON at the wire boundary.
type NATSBus struct {
	conn *nats.Conn
	subs []*nats.Subscription
	mu   sync.Mutex
	once sync.Once
}

var _ Bus = (*NATSBus)(nil)

// bgCtx is a cached context.Background() to avoid per-message allocation.
var bgCtx = context.Background()

// NewNATSBus connects to NATS and returns a bus.
func NewNATSBus(url string) (*NATSBus, error) {
	conn, err := nats.Connect(url)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	return &NATSBus{conn: conn}, nil
}

// Emit serializes payload to JSON and publishes to NATS.
func (b *NATSBus) Emit(ctx context.Context, name string, payload any) error {
	data, err := sonic.Marshal(payload)
	if err != nil {
		return fmt.Errorf("nats marshal: %w", err)
	}
	return b.conn.Publish(name, data)
}

// On subscribes to a NATS subject. Handler receives []byte (raw JSON from wire).
func (b *NATSBus) On(name string, handler Handler) error {
	sub, err := b.conn.Subscribe(name, func(msg *nats.Msg) {
		safeCall(handler, bgCtx, msg.Data)
	})
	if err != nil {
		return fmt.Errorf("nats subscribe: %w", err)
	}
	b.mu.Lock()
	b.subs = append(b.subs, sub)
	b.mu.Unlock()
	return nil
}

// OnQueue subscribes with a queue group. Only one member of the group
// receives each message — used for competing consumers across pods.
func (b *NATSBus) OnQueue(name string, group string, handler Handler) error {
	sub, err := b.conn.QueueSubscribe(name, group, func(msg *nats.Msg) {
		safeCall(handler, bgCtx, msg.Data)
	})
	if err != nil {
		return fmt.Errorf("nats queue subscribe: %w", err)
	}
	b.mu.Lock()
	b.subs = append(b.subs, sub)
	b.mu.Unlock()
	return nil
}

// Close drains all subscriptions and closes the NATS connection.
func (b *NATSBus) Close() {
	b.once.Do(func() {
		b.mu.Lock()
		for _, sub := range b.subs {
			sub.Drain()
		}
		b.mu.Unlock()
		b.conn.Close()
	})
}
