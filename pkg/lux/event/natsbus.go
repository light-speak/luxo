package event

import (
	"context"
	"fmt"
	"sync"

	"github.com/nats-io/nats.go"
)

// NATSBus implements Bus using NATS messaging.
// Used in multi-service mode for cross-process event delivery.
type NATSBus struct {
	conn *nats.Conn
	subs []*nats.Subscription
	mu   sync.Mutex
	once sync.Once
}

var _ Bus = (*NATSBus)(nil)

// NewNATSBus connects to NATS and returns a bus.
func NewNATSBus(url string) (*NATSBus, error) {
	conn, err := nats.Connect(url)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	return &NATSBus{conn: conn}, nil
}

// Emit publishes an event to NATS.
func (b *NATSBus) Emit(ctx context.Context, name string, payload []byte) error {
	return b.conn.Publish(name, payload)
}

// On subscribes to a NATS subject. Handler runs in NATS's goroutine pool.
func (b *NATSBus) On(name string, handler Handler) error {
	sub, err := b.conn.Subscribe(name, func(msg *nats.Msg) {
		safeCall(handler, context.Background(), msg.Data)
	})
	if err != nil {
		return fmt.Errorf("nats subscribe: %w", err)
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
