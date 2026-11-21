// Package diagnostics keeps best-effort reporting independent of business work.
package diagnostics

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

var ErrAborted = errors.New("diagnostic formatting or output terminated before returning")

var ErrDrain = errors.New("event diagnostic shutdown failed")

// Reporter owns one worker and a bounded number of pending diagnostic records.
// Its formatter and writer run only on that worker. Neither can be forcibly
// interrupted; Close reports its deadline while retaining at most one worker.
type Reporter struct {
	mu       sync.Mutex
	closed   bool
	queue    chan error
	done     chan struct{}
	writer   io.Writer
	format   func(error) string
	err      error
	dropped  atomic.Uint64
	failures atomic.Uint64
}

// New is internal wiring: callers supply a nonnegative fixed capacity and a
// concrete formatter. Applications cannot configure either through this API.
func New(writer io.Writer, capacity int, format func(error) string) *Reporter {
	r := &Reporter{queue: make(chan error, capacity), done: make(chan struct{}), writer: writer, format: format}
	go r.run()
	return r
}

func (r *Reporter) Submit(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		select {
		case r.queue <- err:
			return
		default:
		}
	}
	r.dropped.Add(1)
}

func (r *Reporter) run() {
	finished := false
	defer func() {
		if finished {
			close(r.done)
			return
		}
		// A custom error or writer may panic or call Goexit. Lose only that
		// diagnostic, preserve its failure, and continue the remaining records.
		recover()
		r.failed(ErrAborted)
		go r.run()
	}()
	for failure := range r.queue {
		line := r.format(failure)
		n, err := io.WriteString(r.writer, line)
		if err == nil && n != len(line) {
			err = io.ErrShortWrite
		}
		if err != nil {
			r.failed(err)
		}
	}
	finished = true
}

func (r *Reporter) failed(err error) {
	r.failures.Add(1)
	if r.err == nil {
		r.err = err
	}
}

func (r *Reporter) Close(ctx context.Context) error {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.queue)
	}
	r.mu.Unlock()
	select {
	case <-r.done:
		return r.err
	default:
	}
	select {
	case <-r.done:
		return r.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Reporter) Stats() (dropped, failures uint64) {
	return r.dropped.Load(), r.failures.Load()
}

// Pending excludes the record currently being formatted or written.
func (r *Reporter) Pending() int { return len(r.queue) }

// Line preserves a UTF-8 boundary when clipping oversized diagnostic messages.
func Line(prefix, message string) string { return prefix + clip(message) + "\n" }

func clip(message string) string {
	const maxBytes = 4096
	if len(message) <= maxBytes {
		return message
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(message[end]) {
		end--
	}
	return message[:end]
}
