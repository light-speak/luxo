package rpc

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

const connBorrowed uint32 = 0

const (
	connIdle uint32 = 1 << iota
	connClosing
	connClosed
	connCloseWaiter
	connStream
)

// One wrapper per physical connection accounts for every direct Close path,
// including subscription termination, cancellation, and failed exchanges.
type poolConn struct {
	net.Conn
	pool  *Pool
	state atomic.Uint32
	// Only the current borrower accesses this buffer.
	requestBuf []byte
}

func readClientFrame(conn net.Conn, scratch []byte) ([]byte, error) {
	header := (*[4]byte)(scratch)
	if pooled, ok := conn.(*poolConn); ok && len(scratch) > len(header) {
		return pooled.readResponse(scratch[:min(len(scratch), len(frameInput{}.window))])
	}
	return readFrame(conn, nil, header)
}

var errTrailingResponseBytes = errors.New("rpc: unexpected bytes after unary response frame")

// A unary borrow owns one request and one response. The reader may reuse the
// request's storage for a small response. Extra bytes invalidate the exchange.
func (c *poolConn) readResponse(window []byte) ([]byte, error) {
	n, readErr := c.Conn.Read(window)
	if n < 4 {
		if readErr == nil {
			_, readErr = io.ReadFull(c.Conn, window[n:4])
		}
		if readErr != nil {
			return nil, partialResponseError(n, readErr)
		}
		n = 4
	}
	size := binary.BigEndian.Uint32(window[:4])
	if size > maxFrameSize {
		return nil, oversizedReadFrame(size)
	}
	if n-4 > int(size) {
		return nil, errTrailingResponseBytes
	}
	if size == 0 {
		return nil, nil
	}
	body := make([]byte, size)
	if copied := copy(body, window[4:n]); copied < len(body) {
		// A complete header consumes its accompanying error, as ReadFull does.
		if readErr == nil || copied == 0 {
			_, readErr = io.ReadFull(c.Conn, body[copied:])
		}
		if readErr != nil {
			return nil, partialResponseError(copied, readErr)
		}
	}
	return body, nil
}

func partialResponseError(received int, err error) error {
	if received > 0 && err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

func (c *poolConn) Close() error {
	state := c.state.Or(connClosing)
	if state&connClosing != 0 {
		if state&connClosed == 0 {
			c.pool.waitForClose(c)
		}
		return nil
	}
	defer c.finishClose(state)
	return c.Conn.Close()
}

func (c *poolConn) finishClose(state uint32) {
	p := c.pool
	p.open.Add(-1)
	if state&connStream != 0 {
		p.streams.Add(-1)
	}
	p.signal()
	if c.state.Or(connClosed)&connCloseWaiter == 0 {
		return
	}
	// A waiter marks the state and registers under this same lock.
	p.closingMu.Lock()
	waiter := p.closingWaiters[c]
	delete(p.closingWaiters, c)
	p.closingMu.Unlock()
	waiter.Done()
}

func (p *Pool) waitForClose(c *poolConn) {
	p.closingMu.Lock()
	if c.state.Or(connCloseWaiter)&connClosed != 0 {
		p.closingMu.Unlock()
		return
	}
	waiter := p.closingWaiters[c]
	if waiter == nil {
		waiter = new(sync.WaitGroup)
		waiter.Add(1)
		if p.closingWaiters == nil {
			p.closingWaiters = make(map[*poolConn]*sync.WaitGroup)
		}
		p.closingWaiters[c] = waiter
	}
	p.closingMu.Unlock()
	waiter.Wait()
}
