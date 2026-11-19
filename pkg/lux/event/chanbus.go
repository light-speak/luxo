package event

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/light-speak/luxo/internal/diagnostics"
	"github.com/light-speak/luxo/internal/panicerror"
)

var (
	// ErrHandlerRequired rejects subscriptions without an executable listener.
	ErrHandlerRequired = errors.New("event: handler is required")
	// ErrBusClosed is returned when publishing or subscribing after shutdown.
	ErrBusClosed = errors.New("event: bus is closed")
	// ErrBufferFull is returned when a non-blocking in-process publish cannot be accepted.
	ErrBufferFull = errors.New("event: buffer is full")
	// ErrDiagnosticShutdown marks a reporting failure after business dispatch drained.
	ErrDiagnosticShutdown = diagnostics.ErrDrain
)

// ChanBus implements Bus using Go channels.
// Zero external dependencies — used in embedded (single-process) mode.
// Payloads use static owned snapshots — no serialization round trip.
type ChanBus struct {
	mu       sync.RWMutex
	subs     map[string][]Handler
	bufSize  int
	channels map[string]chan Snapshot
	done     chan struct{}
	once     sync.Once
	wg       sync.WaitGroup
	reports  diagnostics.Events
	closeErr error
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
		channels: make(map[string]chan Snapshot),
		done:     make(chan struct{}),
	}
}

var _ Bus = (*ChanBus)(nil)

// Emit publishes an event without blocking. Accepted events are drained during Close.
func (b *ChanBus) Emit(ctx context.Context, name string, payload any) error {
	owned, err := captureSnapshot(payload)
	if err != nil {
		return err
	}
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
	case ch <- owned:
		b.mu.RUnlock()
		return nil
	default:
		b.mu.RUnlock()
		return ErrBufferFull
	}
}

// On registers a handler. Starts a goroutine to consume events.
func (b *ChanBus) On(name string, handler Handler) error {
	if handler == nil {
		return ErrHandlerRequired
	}
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
		ch := make(chan Snapshot, b.bufSize)
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

// dispatchState preserves the next listener and owned payload across Goexit.
type dispatchState struct {
	payload  Snapshot
	handlers []Handler
	next     int
}

// dispatch reads from the channel and calls all handlers for the event.
func (b *ChanBus) dispatch(name string, ch <-chan Snapshot) {
	b.dispatchFrom(name, ch, dispatchState{})
}

func (b *ChanBus) dispatchFrom(name string, ch <-chan Snapshot, state dispatchState) {
	drained := false
	defer func() {
		if drained {
			b.wg.Done()
			return
		}
		// Only an abnormal exit replaces the dispatcher. Transfer the existing
		// wait-group reference so Close still waits for every accepted event.
		b.reports.Submit(name, "dispatch terminated before returning; resuming remaining listeners", nil)
		go b.dispatchFrom(name, ch, state)
	}()
	b.dispatchListeners(name, &state)
	state = dispatchState{}
	for payload := range ch {
		b.mu.RLock()
		handlers := b.subs[name]
		b.mu.RUnlock()
		state = dispatchState{payload: payload, handlers: handlers}
		b.dispatchListeners(name, &state)
		state = dispatchState{}
	}
	drained = true
}

func (b *ChanBus) dispatchListeners(name string, state *dispatchState) {
	for state.next < len(state.handlers) {
		h := state.handlers[state.next]
		state.next++ // A terminated listener is failed, never invoked a second time.
		// The final listener takes ownership; all earlier listeners get copies.
		var value any
		if state.next == len(state.handlers) {
			value = state.payload.takePayload()
		} else {
			value = state.payload.copyPayload()
		}
		if err := InvokeHandler(h, context.Background(), value); err != nil {
			b.reports.Submit(name, "handler error", err)
		}
	}
}

// InvokeHandler calls handler with panic recovery to prevent one handler from killing the dispatcher.
// Returns the handler's error, or a panic-wrapped error if the handler panicked.
func InvokeHandler(h Handler, ctx context.Context, payload any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicerror.New("event handler panic: ", r)
		}
	}()
	return h(ctx, payload)
}

// Close shuts down all dispatchers and channels.
// Waits for all accepted events and in-flight handlers before returning.
func (b *ChanBus) Close() error {
	b.once.Do(func() {
		close(b.done)
		b.mu.Lock()
		for _, ch := range b.channels {
			close(ch)
		}
		b.mu.Unlock()
		b.wg.Wait()
		b.closeErr = b.reports.Drain(2 * time.Second)
	})
	return b.closeErr
}

// DiagnosticStats counts dropped reports and formatter/output failures, never
// discarded business events. Close preserves diagnostic drain failures.
func (b *ChanBus) DiagnosticStats() (dropped, failures uint64) { return b.reports.Stats() }
