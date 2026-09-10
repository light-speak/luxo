package event

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
)

var (
	// ErrBusClosed is returned when publishing or subscribing after shutdown.
	ErrBusClosed = errors.New("event: bus is closed")
	// ErrBufferFull is returned when a non-blocking in-process publish cannot be accepted.
	ErrBufferFull = errors.New("event: buffer is full")
)

// ChanBus implements Bus using Go channels.
// Zero external dependencies — used in embedded (single-process) mode.
// Payloads are passed directly as Go values — zero serialization.
type ChanBus struct {
	mu       sync.RWMutex
	subs     map[string][]Handler
	bufSize  int
	channels map[string]chan any
	done     chan struct{}
	once     sync.Once
	wg       sync.WaitGroup
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
		channels: make(map[string]chan any),
		done:     make(chan struct{}),
	}
}

var _ Bus = (*ChanBus)(nil)

// Emit publishes an event without blocking. Accepted events are drained during Close.
func (b *ChanBus) Emit(ctx context.Context, name string, payload any) error {
	b.mu.RLock()
	select {
	case <-b.done:
		b.mu.RUnlock()
		return ErrBusClosed
	default:
	}

	ch, ok := b.channels[name]
	if !ok {
		b.mu.RUnlock()
		return nil // no subscribers
	}

	select {
	case ch <- payload:
		b.mu.RUnlock()
		return nil
	default:
		b.mu.RUnlock()
		return ErrBufferFull
	}
}

// On registers a handler. Starts a goroutine to consume events.
func (b *ChanBus) On(name string, handler Handler) error {
	b.mu.Lock()
	// Check if bus is closed
	select {
	case <-b.done:
		b.mu.Unlock()
		return ErrBusClosed
	default:
	}
	b.subs[name] = append(b.subs[name], handler)

	if _, ok := b.channels[name]; !ok {
		ch := make(chan any, b.bufSize)
		b.channels[name] = ch
		// Start dispatcher for this event
		b.wg.Add(1)
		go b.dispatch(name, ch)
	}
	b.mu.Unlock()
	return nil
}

// OnQueue registers a queue handler. In single-process mode (ChanBus),
// queue semantics are identical to broadcast — delegates to On.
func (b *ChanBus) OnQueue(name string, group string, handler Handler) error {
	return b.On(name, handler)
}

// dispatch reads from the channel and calls all handlers for the event.
func (b *ChanBus) dispatch(name string, ch <-chan any) {
	defer b.wg.Done()
	for payload := range ch {
		b.mu.RLock()
		handlers := b.subs[name]
		b.mu.RUnlock()

		for _, h := range handlers {
			if err := safeCall(h, bgCtx, payload); err != nil {
				fmt.Fprintf(os.Stderr, "event %s handler error: %v\n", name, err)
			}
		}
	}
}

// safeCall calls handler with panic recovery to prevent one handler from killing the dispatcher.
// Returns the handler's error, or a panic-wrapped error if the handler panicked.
func safeCall(h Handler, ctx context.Context, payload any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("event handler panic: %v", r)
		}
	}()
	return h(ctx, payload)
}

// Close shuts down all dispatchers and channels.
// Waits for all accepted events and in-flight handlers before returning.
func (b *ChanBus) Close() {
	b.once.Do(func() {
		close(b.done)
		b.mu.Lock()
		for _, ch := range b.channels {
			close(ch)
		}
		b.mu.Unlock()
		b.wg.Wait()
	})
}
