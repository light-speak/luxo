package rpc

// Frozen pre-fix pool for open-pool and sequential closed-pool benchmarks.
// Close must never race with Put here; production uses the corrected pool.

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// poolBeforeConcurrentClose manages reusable TCP connections to a remote service.
// Zero-alloc Get/Put on the hot path (channel + atomic bool).
type poolBeforeConcurrentClose struct {
	addr    string
	maxIdle int
	timeout time.Duration
	conns   chan net.Conn
	closed  atomic.Bool
	once    sync.Once
}

// newPoolBeforeConcurrentClose creates a connection pool for the given address.
func newPoolBeforeConcurrentClose(addr string, maxIdle int) *poolBeforeConcurrentClose {
	if maxIdle <= 0 {
		maxIdle = 32
	}
	return &poolBeforeConcurrentClose{
		addr:    addr,
		maxIdle: maxIdle,
		timeout: 5 * time.Second,
		conns:   make(chan net.Conn, maxIdle),
	}
}

// Get returns an idle connection or dials a new one.
func (p *poolBeforeConcurrentClose) Get() (net.Conn, error) {
	return p.GetContext(context.Background())
}

// GetContext returns an idle connection or dials with context support.
func (p *poolBeforeConcurrentClose) GetContext(ctx context.Context) (net.Conn, error) {
	if p.closed.Load() {
		return nil, fmt.Errorf("pool closed")
	}
	select {
	case conn := <-p.conns:
		if conn != nil {
			return conn, nil
		}
		return nil, fmt.Errorf("pool closed")
	default:
	}
	return p.dialContext(ctx)
}

// Put returns a connection to the pool. Closes it if pool is full or closed.
// Zero overhead: atomic bool check instead of recover.
func (p *poolBeforeConcurrentClose) Put(conn net.Conn) {
	if conn == nil {
		return
	}
	if p.closed.Load() {
		conn.Close()
		return
	}
	select {
	case p.conns <- conn:
	default:
		conn.Close()
	}
}

// Close closes all idle connections. Idempotent — safe to call multiple times.
func (p *poolBeforeConcurrentClose) Close() {
	p.once.Do(func() {
		p.closed.Store(true)
		close(p.conns)
		for conn := range p.conns {
			conn.Close()
		}
	})
}

// dialContext dials with context support for cancellation and deadline propagation.
func (p *poolBeforeConcurrentClose) dialContext(ctx context.Context) (net.Conn, error) {
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
