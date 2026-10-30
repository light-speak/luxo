package rpc

import (
	"net"
	"sync/atomic"
)

const (
	connBorrowed uint32 = iota
	connIdle
	connClosed
)

// One wrapper per physical connection accounts for every direct Close path,
// including subscription termination, cancellation, and failed exchanges.
type poolConn struct {
	net.Conn
	pool  *Pool
	state atomic.Uint32
	// Only the current borrower accesses this buffer; Close never mutates it.
	requestBuf []byte
}

func (c *poolConn) Close() error {
	if c.state.Swap(connClosed) == connClosed {
		return nil
	}
	err := c.Conn.Close()
	c.pool.open.Add(-1)
	c.pool.signal()
	return err
}
