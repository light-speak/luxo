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

// Two connection-scoped workers share one read token. The worker receiving a
// request dispatches it directly while its peer observes further input/EOF.
// A busy dispatcher permits one queued frame; a second queued frame closes the
// connection instead of blocking the reader and hiding a later disconnect.
type connectionReader struct {
	ctx         context.Context
	cancel      context.CancelFunc
	conn        net.Conn
	server      *Server
	ready       chan bool
	buffers     [2][]byte
	bufferCount int
	workers     sync.WaitGroup
	closeOnce   sync.Once
	mu          sync.Mutex
	busy        bool
	queued      bool
	pending     incomingRequest
}

func (s *Server) serveConnection(conn net.Conn) {
	newConnectionReader(s, conn).serve()
}

func newConnectionReader(s *Server, conn net.Conn) *connectionReader {
	ctx, cancel := context.WithCancel(s.ctx)
	r := &connectionReader{
		ctx: ctx, cancel: cancel, conn: conn, server: s,
		ready: make(chan bool, 1),
	}
	return r
}

func (r *connectionReader) serve() {
	r.ready <- false
	r.workers.Add(1)
	defer r.workers.Wait()
	go r.background()
	r.run()
}

func (r *connectionReader) close() {
	r.cancel()
	r.closeOnce.Do(func() { r.conn.Close() })
}

func (r *connectionReader) background() {
	defer r.workers.Done()
	r.run()
}

func (r *connectionReader) readBuffer() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
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
		select {
		case <-r.ctx.Done():
			return
		case probe := <-r.ready:
			if r.ctx.Err() != nil {
				return
			}
			if probe {
				r.probe()
				return
			}
			if !r.readRequests() {
				return
			}
		}
	}
}

func (r *connectionReader) probe() {
	var probe [1]byte
	r.conn.Read(probe[:])
}

func (r *connectionReader) readRequests() bool {
	for {
		payload, err := ReadFrame(r.conn, r.readBuffer())
		if err != nil {
			return false
		}
		envelope, err := decodeRequestEnvelope(payload)
		request := incomingRequest{payload: payload, envelope: envelope, err: err}
		dispatch, ok := r.claim(request)
		if !ok {
			return false
		}
		stream := err == nil && envelope.kind == requestKindStream
		if dispatch {
			r.ready <- stream
			return r.dispatch(request)
		}
		if stream {
			r.probe()
			return false
		}
	}
}

func (r *connectionReader) claim(request incomingRequest) (bool, bool) {
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
