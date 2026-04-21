package rpc

import (
	"fmt"
	"net"
	"time"
)

// Pool manages reusable TCP connections to a remote service.
// Zero-alloc Get/Put on the hot path (channel-based).
type Pool struct {
	addr    string
	maxIdle int
	timeout time.Duration

	conns chan net.Conn
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
	case conn, ok := <-p.conns:
		if ok && conn != nil {
			return conn, nil
		}
		return nil, fmt.Errorf("pool closed")
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
	return conn, nil
}
