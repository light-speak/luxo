package event

import (
	"context"
	"errors"
	"fmt"
	"github.com/light-speak/luxo/internal/diagnostics"
	coreevent "github.com/light-speak/luxo/pkg/lux/event"
	"sync"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/nats-io/nats.go"
)

var ErrBinaryPayloadRequired = errors.New("event: NATS payload must use Luxo binary or []byte")

// NATSBus implements Bus using NATS messaging.
// Used in multi-service mode for cross-process event delivery.
// Generated events use Luxo binary; custom payloads must be pre-encoded.
type NATSBus struct {
	conn     *nats.Conn
	subs     []*busSubscription
	mu       sync.RWMutex
	once     sync.Once
	closed   bool
	closeErr error
	reports  diagnostics.Events
}

var _ coreevent.Bus = (*NATSBus)(nil)

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

// CheckReady verifies the broker acknowledges this bus's connection.
func (b *NATSBus) CheckReady(ctx context.Context) error {
	b.mu.RLock()
	closed := b.closed
	b.mu.RUnlock()
	if closed {
		return coreevent.ErrBusClosed
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return b.conn.FlushWithContext(ctx)
}

// Emit publishes a Luxo binary or pre-encoded payload to NATS.
func (b *NATSBus) Emit(ctx context.Context, name string, payload any) error {
	data, err := natsPayloadBytes(payload)
	if err != nil {
		return err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return coreevent.ErrBusClosed
	}
	return b.conn.Publish(name, data)
}

func natsPayloadBytes(payload any) ([]byte, error) {
	switch value := payload.(type) {
	case []byte:
		return value, nil
	case codec.LuxoMarshaler:
		data, err := value.MarshalLuxo()
		if err != nil {
			return nil, err
		}
		return data, nil
	default:
		return nil, fmt.Errorf("%w: %T", ErrBinaryPayloadRequired, payload)
	}
}

// On subscribes to a NATS subject. Handler receives the raw wire bytes.
func (b *NATSBus) On(name string, handler coreevent.Handler) error {
	return b.subscribe(name, "", handler)
}

// OnQueue subscribes with a queue group. Only one member receives each message.
func (b *NATSBus) OnQueue(name string, group string, handler coreevent.Handler) error {
	return b.subscribe(name, group, handler)
}

func (b *NATSBus) subscribe(name, group string, handler coreevent.Handler) error {
	if handler == nil {
		return coreevent.ErrHandlerRequired
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return coreevent.ErrBusClosed
	}
	var sub *nats.Subscription
	var err error
	if group == "" {
		sub, err = b.conn.SubscribeSync(name)
	} else {
		sub, err = b.conn.QueueSubscribeSync(name, group)
	}
	if err != nil {
		return fmt.Errorf("nats subscribe: %w", err)
	}
	subscription := &busSubscription{sub: sub, handler: b.messageHandler(name, handler), done: make(chan struct{}), bus: b}
	b.subs = append(b.subs, subscription)
	go subscription.run()
	return nil
}

// Close rejects new work, drains accepted deliveries, then closes the connection.
func (b *NATSBus) Close() error {
	b.once.Do(b.close)
	return b.closeErr
}

func (b *NATSBus) close() {
	b.mu.Lock()
	b.closed = true
	subscriptions := b.subs
	b.subs = nil
	b.mu.Unlock()

	for _, subscription := range subscriptions {
		if err := subscription.sub.Drain(); err != nil {
			b.closeErr = errors.Join(b.closeErr, fmt.Errorf("nats event drain: %w", err))
			b.unsubscribe(subscription)
		}
	}
	// Flush after UNSUB requests: this covers outbound-only buses as well as
	// the server boundary for draining subscriptions. A failed barrier is not success.
	if err := b.conn.FlushTimeout(nats.DefaultTimeout); err != nil {
		b.closeErr = errors.Join(b.closeErr, fmt.Errorf("nats event flush: %w", err))
		for _, subscription := range subscriptions {
			b.unsubscribe(subscription)
		}
	}
	for _, subscription := range subscriptions {
		<-subscription.done
	}
	if b.conn.IsClosed() {
		b.closeErr = errors.Join(b.closeErr, nats.ErrConnectionClosed)
	}
	b.conn.Close()
	if err := b.reports.Drain(nats.DefaultTimeout); err != nil {
		b.closeErr = errors.Join(b.closeErr, err)
	}
}

func (b *NATSBus) unsubscribe(subscription *busSubscription) {
	if err := subscription.sub.Unsubscribe(); err != nil && !errors.Is(err, nats.ErrBadSubscription) {
		b.closeErr = errors.Join(b.closeErr, fmt.Errorf("nats event unsubscribe: %w", err))
	}
}

// natsMessageHandler preserves failure diagnostics without retrying a delivery.
func (b *NATSBus) messageHandler(name string, handler coreevent.Handler) nats.MsgHandler {
	return func(msg *nats.Msg) {
		if err := coreevent.InvokeHandler(handler, bgCtx, msg.Data); err != nil {
			b.reports.Submit(name, "handler error", err)
		}
	}
}

// DiagnosticStats counts failed or dropped reports independently of deliveries.
func (b *NATSBus) DiagnosticStats() (dropped, failures uint64) { return b.reports.Stats() }
