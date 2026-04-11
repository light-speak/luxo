package event

import (
	"context"
	"sync"
)

// ChanBus implements Bus using Go channels.
// Zero external dependencies — used in embedded (single-process) mode.
type ChanBus struct {
	mu       sync.RWMutex
	subs     map[string][]Handler
	bufSize  int
	channels map[string]chan message
	done     chan struct{}
	once     sync.Once
}

type message struct {
	ctx     context.Context
	payload []byte
}

// NewChanBus creates a channel-based event bus.
// bufSize is the channel buffer size per event (default 256).
func NewChanBus(bufSize int) *ChanBus {
	if bufSize <= 0 {
		bufSize = 256
	}
	return &ChanBus{
		subs:     make(map[string][]Handler),
		bufSize:  bufSize,
		channels: make(map[string]chan message),
		done:     make(chan struct{}),
	}
}

var _ Bus = (*ChanBus)(nil)

// Emit publishes an event. Non-blocking — drops if buffer is full or bus is closed.
func (b *ChanBus) Emit(ctx context.Context, name string, payload []byte) error {
	select {
	case <-b.done:
		return nil // bus closed
	default:
	}

	b.mu.RLock()
	ch, ok := b.channels[name]
	b.mu.RUnlock()

	if !ok {
		return nil // no subscribers
	}

	select {
	case ch <- message{ctx: ctx, payload: payload}:
	default:
		// buffer full, drop
	}
	return nil
}

// On registers a handler. Starts a goroutine to consume events.
func (b *ChanBus) On(name string, handler Handler) error {
	b.mu.Lock()
	b.subs[name] = append(b.subs[name], handler)

	if _, ok := b.channels[name]; !ok {
		ch := make(chan message, b.bufSize)
		b.channels[name] = ch
		// Start dispatcher for this event
		go b.dispatch(name, ch)
	}
	b.mu.Unlock()
	return nil
}

// dispatch reads from the channel and calls all handlers for the event.
func (b *ChanBus) dispatch(name string, ch chan message) {
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			b.mu.RLock()
			handlers := b.subs[name]
			b.mu.RUnlock()

			for _, h := range handlers {
				safeCall(h, msg.ctx, msg.payload)
			}
		case <-b.done:
			return
		}
	}
}

// safeCall calls handler with panic recovery to prevent one handler from killing the dispatcher.
func safeCall(h Handler, ctx context.Context, payload []byte) {
	defer func() { recover() }()
	h(ctx, payload)
}

// Close shuts down all dispatchers and channels.
func (b *ChanBus) Close() {
	b.once.Do(func() {
		close(b.done)
		b.mu.Lock()
		for _, ch := range b.channels {
			close(ch)
		}
		b.mu.Unlock()
	})
}
