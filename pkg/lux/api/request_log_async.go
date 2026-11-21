package api

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
)

const requestLogQueueCapacity = 256
const maxRequestLogBytes = 16 * 1024

type asyncRequestLogs struct {
	mu       sync.Mutex
	closed   bool
	queue    chan *[]byte
	done     chan struct{}
	writer   io.Writer
	err      error // Written by the worker; read only after done closes.
	dropped  atomic.Uint64
	failures atomic.Uint64
}

func newAsyncRequestLogs(w io.Writer, capacity int) *asyncRequestLogs {
	l := &asyncRequestLogs{queue: make(chan *[]byte, capacity), done: make(chan struct{}), writer: w}
	go l.run()
	return l
}

// submit transfers ownership, including on rejection. It never waits for I/O.
func (l *asyncRequestLogs) submit(buf *[]byte) {
	l.mu.Lock()
	if !l.closed && len(*buf) <= maxRequestLogBytes {
		select {
		case l.queue <- buf:
			l.mu.Unlock()
			return
		default:
		}
	}
	l.mu.Unlock()
	l.dropped.Add(1)
	putRequestLogBuffer(buf)
}

func (l *asyncRequestLogs) run() {
	defer close(l.done)
	for buf := range l.queue {
		n, err := l.writer.Write(*buf)
		if err == nil && n != len(*buf) {
			err = io.ErrShortWrite
		}
		if err != nil {
			l.failures.Add(1)
			if l.err == nil {
				l.err = err
			}
		}
		putRequestLogBuffer(buf)
	}
}

func (l *asyncRequestLogs) close(ctx context.Context) error {
	l.mu.Lock()
	if !l.closed {
		l.closed = true
		close(l.queue)
	}
	l.mu.Unlock()
	select {
	case <-l.done:
		return l.err
	default:
	}
	select {
	case <-l.done:
		return l.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// CloseRequestLogs stops accepting logs and drains accepted records. Stop serving
// requests first. A deadline bounds waiting, not an arbitrary blocked Writer.
// The router does not own and never closes the configured Writer.
func (rt *Router) CloseRequestLogs(ctx context.Context) error {
	if rt.asyncLogs == nil {
		return nil
	}
	return rt.asyncLogs.close(ctx)
}

// RequestLogStats reports queue/size rejection and output failures, not sampling.
func (rt *Router) RequestLogStats() (dropped, failures uint64) {
	if rt.asyncLogs == nil {
		return 0, 0
	}
	return rt.asyncLogs.dropped.Load(), rt.asyncLogs.failures.Load()
}

func (rt *Router) writeLogBuffer(buf *[]byte) {
	if rt.asyncLogs != nil {
		rt.asyncLogs.submit(buf)
		return
	}
	_, _ = rt.logWriter.Write(*buf)
	putRequestLogBuffer(buf)
}

// allowLogParts bounds allocation before formatting untrusted text. The fixed
// reserve covers timestamps, colors, delimiters and numeric fields.
func (rt *Router) allowLogParts(parts ...string) bool {
	if rt.asyncLogs == nil {
		return true
	}
	remaining := maxRequestLogBytes - 256
	for _, part := range parts {
		if len(part) > remaining {
			rt.asyncLogs.dropped.Add(1)
			return false
		}
		remaining -= len(part)
	}
	return true
}
