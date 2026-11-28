package api

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
)

const requestLogQueueCapacity = 256
const maxRequestLogBytes = 16 * 1024

var errRequestLogWriterAborted = errors.New("api: request log writer exited without returning")

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
	var current *[]byte
	defer l.finishWorker(&current)
	for current = range l.queue {
		n, err := l.writer.Write(*current)
		if err == nil && n != len(*current) {
			err = io.ErrShortWrite
		}
		if err != nil {
			l.failures.Add(1)
			if l.err == nil {
				l.err = err
			}
		}
		putRequestLogBuffer(current)
		current = nil
	}
}

func (l *asyncRequestLogs) finishWorker(current **[]byte) {
	defer close(l.done)
	if *current == nil {
		return
	}
	// A configured Writer can panic or exit without returning. Stop using it
	// and release every record the worker still owns before reporting failure.
	_ = recover()
	l.failures.Add(1)
	if l.err == nil {
		l.err = errRequestLogWriterAborted
	}
	putRequestLogBuffer(*current)
	l.stopAdmission()
	for pending := range l.queue {
		l.dropped.Add(1)
		putRequestLogBuffer(pending)
	}
}

func (l *asyncRequestLogs) stopAdmission() {
	l.mu.Lock()
	if !l.closed {
		l.closed = true
		close(l.queue)
	}
	l.mu.Unlock()
}

func (l *asyncRequestLogs) close(ctx context.Context) error {
	l.stopAdmission()
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

// RequestLogStats reports rejected or abandoned records and output failures, excluding sampling.
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
