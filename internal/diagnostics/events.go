package diagnostics

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type eventFailure struct {
	name, operation string
	cause           error
}

func (e eventFailure) Error() string {
	message := "event " + clip(e.name)
	if e.cause == nil {
		return message + ": " + e.operation
	}
	return message + " " + e.operation + ": " + clip(e.cause.Error())
}

// Events starts reporting only on the first failure. Successful delivery does
// not touch this state. The bus closes it after all business handlers finish.
type Events struct {
	mu       sync.Mutex
	reporter *Reporter
	closed   bool
	rejected atomic.Uint64
}

func (e *Events) Submit(name, operation string, cause error) {
	e.mu.Lock()
	if e.closed {
		e.rejected.Add(1)
		e.mu.Unlock()
		return
	}
	if e.reporter == nil {
		e.reporter = New(os.Stderr, 16, func(err error) string { return err.Error() + "\n" })
	}
	reporter := e.reporter
	e.mu.Unlock()
	reporter.Submit(eventFailure{name: name, operation: operation, cause: cause})
}

func (e *Events) Close(ctx context.Context) error {
	reporter := e.stop()
	if reporter == nil {
		return nil
	}
	return reporter.Close(ctx)
}

// Drain creates a deadline only when reporting actually started.
func (e *Events) Drain(timeout time.Duration) error {
	reporter := e.stop()
	if reporter == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := reporter.Close(ctx); err != nil {
		return errors.Join(ErrDrain, err)
	}
	return nil
}

func (e *Events) stop() *Reporter {
	e.mu.Lock()
	e.closed = true
	reporter := e.reporter
	e.mu.Unlock()
	return reporter
}

func (e *Events) Stats() (dropped, failures uint64) {
	e.mu.Lock()
	reporter := e.reporter
	e.mu.Unlock()
	dropped = e.rejected.Load()
	if reporter != nil {
		rejected, failed := reporter.Stats()
		dropped += rejected
		failures = failed
	}
	return
}
