package rpc

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
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
	pool      *Pool
	state     atomic.Uint32
	closeOnce sync.Once
	// Only the current borrower accesses these buffers.
	requestBuf   []byte
	responseTail *frameInput
}

func (c *poolConn) activeResponseTail() *frameInput {
	if tail := c.responseTail; tail != nil && (tail.start != tail.end || tail.pending != nil) {
		return tail
	}
	c.responseTail = nil
	return nil
}

func readClientFrame(conn net.Conn, scratch []byte) ([]byte, error) {
	header := (*[4]byte)(scratch)
	if pooled, ok := conn.(*poolConn); ok {
		if tail := pooled.activeResponseTail(); tail != nil {
			return readFrame(tail, nil, header)
		}
		if len(scratch) > len(header) {
			return pooled.readResponse(scratch[:min(len(scratch), len(frameInput{}.window))])
		}
	}
	return readFrame(conn, nil, header)
}

type prefetchedStreamConn struct {
	net.Conn
	input *frameInput
}

func (c *prefetchedStreamConn) Read(p []byte) (int, error) { return c.input.Read(p) }

func streamResponseConn(conn net.Conn) net.Conn {
	if pooled, ok := conn.(*poolConn); ok {
		if tail := pooled.activeResponseTail(); tail != nil {
			pooled.responseTail = nil
			return &prefetchedStreamConn{Conn: conn, input: tail}
		}
	}
	return conn
}

// The completed exchange owns the request storage and can reuse it for a small
// response. Only bytes belonging to a subsequent frame need persistent storage.
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
	if remaining := window[min(4+int(size), n):n]; len(remaining) > 0 {
		tail := &frameInput{source: c.Conn, end: uint8(len(remaining)), pending: readErr}
		copy(tail.window[:], remaining)
		c.responseTail = tail
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
	var err error
	// A racing borrower must wait until the socket and its pool slot are both released.
	c.closeOnce.Do(func() {
		c.state.Store(connClosed)
		err = c.Conn.Close()
		c.pool.open.Add(-1)
		c.pool.signal()
	})
	return err
}
