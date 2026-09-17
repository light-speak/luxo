package rpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

var errPoolClosed = errors.New("pool closed")

// Pool manages reusable TCP connections to a remote service.
// Zero-alloc Get/Put on the hot path (channel + atomic bool).
type Pool struct {
	addr    string
	maxIdle int
	timeout time.Duration
	conns   chan net.Conn
	closed  atomic.Bool
	once    sync.Once
}

// NewPool creates a connection pool for the given address.
func NewPool(addr string, maxIdle int) *Pool {
	if maxIdle <= 0 {
		maxIdle = 32
	}
	return &Pool{
		addr:    addr,
		maxIdle: maxIdle,
		timeout: 5 * time.Second,
		conns:   make(chan net.Conn, maxIdle),
	}
}

// Get returns an idle connection or dials a new one.
func (p *Pool) Get() (net.Conn, error) {
	return p.GetContext(context.Background())
}

// GetContext returns an idle connection or dials with context support.
func (p *Pool) GetContext(ctx context.Context) (net.Conn, error) {
	if p.closed.Load() {
		return nil, errPoolClosed
	}
	select {
	case conn := <-p.conns:
		return conn, nil // conns stays open and Put never enqueues nil.
	default:
	}
	return p.dialContext(ctx)
}

// Put returns a connection to the pool. Closes it if pool is full or closed.
// Close never closes conns. The first check rejects shutdown returns cheaply;
// the post-send check covers a shutdown racing with successful publication.
func (p *Pool) Put(conn net.Conn) {
	if conn == nil {
		return
	}
	if p.closed.Load() {
		conn.Close()
		return
	}
	select {
	case p.conns <- conn:
		if p.closed.Load() {
			p.closeIdle()
		}
	default:
		conn.Close()
	}
}

// Close rejects new borrows and closes idle connections. Borrowed connections
// remain owned by their callers and are closed when returned. Concurrent Put
// calls participate in draining late returns. Close is idempotent and does
// not wait for active calls that began borrowing before shutdown.
func (p *Pool) Close() {
	p.once.Do(func() {
		p.closed.Store(true)
		p.closeIdle()
	})
}

// Receivers own each removed connection exclusively, so concurrent drains do
// not double-close it. FIFO puts each published return within at most capacity
// receives; a finite budget prevents ongoing returns from extending a drain.
// Keep shutdown-only traversal out of the normal return path's stack frame.
//
//go:noinline
func (p *Pool) closeIdle() {
	for range cap(p.conns) {
		select {
		case conn := <-p.conns:
			conn.Close()
		default:
			return
		}
	}
}

// dialContext dials with context support for cancellation and deadline propagation.
func (p *Pool) dialContext(ctx context.Context) (net.Conn, error) {
	d := &net.Dialer{Timeout: p.timeout}
	conn, err := d.DialContext(ctx, "tcp", p.addr)
	if err != nil {
		return nil, err
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	if err := initiateHandshake(ctx, conn, p.timeout); err != nil {
		conn.Close()
		return nil, fmt.Errorf("rpc: handshake: %w", err)
	}
	return conn, nil
}
