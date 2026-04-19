package rpc

import (
	"net"
	"sync"
	"time"
)

// Pool manages reusable TCP connections to a remote service.
// Zero-alloc Get/Put on the hot path (channel-based).
type Pool struct {
	addr    string
	maxIdle int
	timeout time.Duration

	mu    sync.Mutex
	conns chan net.Conn
	count int // total alive connections
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
	select {
	case conn := <-p.conns:
		return conn, nil
	default:
	}
	return p.dial()
}

// Put returns a connection to the pool. Closes it if pool is full.
func (p *Pool) Put(conn net.Conn) {
	select {
	case p.conns <- conn:
	default:
		conn.Close()
		p.mu.Lock()
		p.count--
		p.mu.Unlock()
	}
}

// Close closes all idle connections.
func (p *Pool) Close() {
	close(p.conns)
	for conn := range p.conns {
		conn.Close()
	}
}

func (p *Pool) dial() (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", p.addr, p.timeout)
	if err != nil {
		return nil, err
	}
	// Disable Nagle for low latency
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	p.mu.Lock()
	p.count++
	p.mu.Unlock()
	return conn, nil
}
