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

// Pool bounds idle, open (including dialing), and waiting LRPC connections.
type Pool struct {
	addr     string
	maxIdle  int
	timeout  time.Duration
	conns    chan net.Conn
	closed   atomic.Bool
	once     sync.Once
	options  PoolOptions
	open     atomic.Int64
	waiters  atomic.Int64
	wake     chan struct{}
	lifetime context.Context
	cancel   context.CancelFunc
}

// NewPool creates a connection pool for the given address.
func NewPool(addr string, maxIdle int) *Pool {
	if maxIdle <= 0 {
		maxIdle = 32
	}
	options := DefaultPoolOptions()
	options.MaxIdle = maxIdle
	options.MaxOpen = max(32, maxIdle)
	return newPool(addr, options)
}

// Get returns an idle connection or dials a new one.
func (p *Pool) Get() (net.Conn, error) {
	return p.GetContext(context.Background())
}

// GetContext returns an idle connection or dials with context support.
func (p *Pool) GetContext(ctx context.Context) (net.Conn, error) {
	conn, err, ready := p.tryAcquire(ctx)
	if !ready {
		conn, err = p.wait(ctx)
	}
	if conn == nil && err == nil {
		return p.dialReserved(ctx)
	}
	return conn, err
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
	tracked, ok := conn.(*poolConn)
	if !ok {
		if !reserve(&p.open, p.options.MaxOpen) {
			conn.Close()
			return
		}
		tracked = &poolConn{Conn: conn, pool: p}
	} else if tracked.pool != p {
		tracked.Close()
		return
	}
	if !tracked.state.CompareAndSwap(connBorrowed, connIdle) {
		return
	}
	select {
	case p.conns <- tracked:
		p.signal()
		if p.closed.Load() {
			p.closeIdle()
		}
	default:
		tracked.Close()
	}
}

// Close rejects new borrows and closes idle connections. Borrowed connections
// remain owned by their callers and are closed when returned. Concurrent Put
// calls participate in draining late returns. Close is idempotent and does
// not wait for active calls that began borrowing before shutdown.
func (p *Pool) Close() {
	p.once.Do(func() {
		p.closed.Store(true)
		p.cancel()
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
