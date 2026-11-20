package rpc

import (
	"context"
	"net"
	"sync"
)

type incomingRequest struct {
	payload  []byte
	envelope requestEnvelope
	err      error
}

type connectionReadMode uint8

const (
	connectionReadWaiting connectionReadMode = iota
	connectionReadFrames
	connectionReadProbe
)

// Two connection-scoped workers share one read token. The worker receiving a
// request dispatches it directly while its peer observes further input/EOF.
// A busy dispatcher permits one queued frame; a second queued frame closes the
// connection instead of blocking the reader and hiding a later disconnect.
type connectionReader struct {
	ctx         context.Context
	cancel      context.CancelFunc
	conn        net.Conn
	server      *Server
	ready       sync.Cond
	mode        connectionReadMode
	buffers     [2][]byte
	bufferCount int
	workers     sync.WaitGroup
	closeOnce   sync.Once
	mu          sync.Mutex
	busy        bool
	queued      bool
	pending     incomingRequest
	header      [4]byte // The read token protects the header and input.
	input       frameInput
}

func (s *Server) serveConnection(conn net.Conn) {
	newConnectionReader(s, conn).serve()
}

func newConnectionReader(s *Server, conn net.Conn) *connectionReader {
	ctx, cancel := context.WithCancel(s.ctx)
	r := &connectionReader{
		ctx: ctx, cancel: cancel, conn: conn, server: s,
		mode: connectionReadFrames, input: frameInput{source: conn},
	}
	r.ready.L = &r.mu
	return r
}

func (r *connectionReader) serve() {
	r.workers.Add(1)
	defer r.workers.Wait()
	go r.background()
	r.run()
}

func (r *connectionReader) close() {
	r.closeOnce.Do(func() {
		r.cancel()
		r.mu.Lock()
		r.ready.Broadcast()
		r.mu.Unlock()
		r.conn.Close()
	})
}

func (r *connectionReader) background() {
	defer r.workers.Done()
	r.run()
}

func (r *connectionReader) takeBufferLocked() []byte {
	if r.bufferCount == 0 {
		return nil
	}
	r.bufferCount--
	buffer := r.buffers[r.bufferCount]
	r.buffers[r.bufferCount] = nil
	return buffer
}

// The dispatcher holds mu; buffers are published only after business returns.
func (r *connectionReader) recycleLocked(payload []byte) {
	payload = retainReadFrame(payload)
	if payload != nil && r.bufferCount < len(r.buffers) {
		r.buffers[r.bufferCount] = payload
		r.bufferCount++
	}
}

func (r *connectionReader) run() {
	defer r.close()
	for {
		mode, buffer := r.waitForRead()
		if mode == connectionReadWaiting {
			return
		}
		if mode == connectionReadProbe {
			r.probe()
			return
		}
		if !r.readRequests(buffer) {
			return
		}
	}
}

func (r *connectionReader) waitForRead() (connectionReadMode, []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for r.mode == connectionReadWaiting && r.ctx.Err() == nil {
		r.ready.Wait()
	}
	if r.ctx.Err() != nil {
		return connectionReadWaiting, nil
	}
	mode := r.mode
	r.mode = connectionReadWaiting
	if mode == connectionReadFrames {
		return mode, r.takeBufferLocked()
	}
	return mode, nil
}

func (r *connectionReader) probe() {
	r.input.Read(r.header[:1])
}

func (r *connectionReader) readRequests(buffer []byte) bool {
	for {
		payload, err := readFrame(&r.input, buffer, &r.header)
		if err != nil {
			return false
		}
		envelope, err := decodeRequestEnvelope(payload)
		request := incomingRequest{payload: payload, envelope: envelope, err: err}
		stream := err == nil && envelope.kind == requestKindStream
		dispatch, ok := r.claim(request, stream)
		if !ok {
			return false
		}
		if dispatch {
			r.ready.Signal()
			return r.dispatch(request)
		}
		if stream {
			r.probe()
			return false
		}
		r.mu.Lock()
		buffer = r.takeBufferLocked()
		r.mu.Unlock()
	}
}

func (r *connectionReader) claim(request incomingRequest, stream bool) (bool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ctx.Err() != nil || r.queued {
		return false, false
	}
	if r.busy {
		r.pending = request
		r.queued = true
		return false, true
	}
	r.busy = true
	r.mode = connectionReadFrames
	if stream {
		r.mode = connectionReadProbe
	}
	return true, true
}

func (r *connectionReader) dispatch(request incomingRequest) bool {
	for {
		if r.ctx.Err() != nil {
			return false
		}
		if request.err != nil {
			if err := WriteFrame(r.conn, encodeError(400, "BadRequest", request.err.Error())); err != nil {
				return false
			}
		} else if request.envelope.kind == requestKindStream {
			r.server.processStream(r.ctx, r.conn, request.envelope)
			return false
		} else if err := r.server.processCallContext(r.ctx, r.conn, request.envelope); err != nil {
			return false
		}
		r.mu.Lock()
		r.recycleLocked(request.payload)
		if !r.queued {
			r.busy = false
			r.mu.Unlock()
			return true
		}
		request = r.pending
		r.pending = incomingRequest{}
		r.queued = false
		r.mu.Unlock()
	}
}
