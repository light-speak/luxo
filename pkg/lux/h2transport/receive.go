package h2transport

import (
	"errors"
	"slices"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

var errReceiveConfig = errors.New("invalid HTTP/2 request receive budget")

type receiveWindow struct{ available, pending uint32 }

// Credit is returned only for bytes copied into bounded storage or discarded.
// The connection coordinator must enqueue every update, or close on failure.
type receiveCredit struct{ connection, stream uint32 }

func (w *receiveWindow) consume(size uint32) bool {
	if size > w.available {
		return false
	}
	w.available -= size
	return true
}

func (w *receiveWindow) release(size uint32, force bool) uint32 {
	w.pending += size
	if w.pending < 32768 && !force {
		return 0
	}
	credit := w.pending
	w.available += credit
	w.pending = 0
	return credit
}

type receivedRequest struct {
	head     requestHead
	body     []byte
	trailers []hpack.HeaderField
	window   receiveWindow
	complete bool
}

// All state belongs to one reader. Completed requests remain charged until the
// business consumer explicitly releases them. This is not a goroutine/task queue.
type requestReceiver struct {
	budget *byteBudget
	// One idle buffer per connection, at most 64KiB. Active plus idle capacity
	// stays within limit; no global pool or extra synchronization is required.
	spare                                []byte
	streams                              map[uint32]*receivedRequest
	window                               receiveWindow
	lastStream, initialWindow            uint32
	maxStreams, maxBody, limit, buffered int
	closed                               bool
}

func newRequestReceiver(maxStreams, maxBody, maxBuffered int) (*requestReceiver, error) {
	if maxStreams < 1 || maxStreams > 65535 || maxBody < 1 || maxBuffered < maxBody {
		return nil, errReceiveConfig
	}
	return &requestReceiver{streams: make(map[uint32]*receivedRequest), initialWindow: 65535, window: receiveWindow{available: 65535}, maxStreams: maxStreams, maxBody: maxBody, limit: maxBuffered}, nil
}

func (r *requestReceiver) open(frame *http2.MetaHeadersFrame) (*receivedRequest, error) {
	id := frame.StreamID
	if r.closed {
		return nil, errQueueClosed
	}
	if id == 0 || id&1 == 0 || id <= r.lastStream {
		return nil, http2.ConnectionError(http2.ErrCodeProtocol)
	}
	r.lastStream = id
	if len(r.streams) >= r.maxStreams {
		return nil, http2.StreamError{StreamID: id, Code: http2.ErrCodeRefusedStream}
	}
	head, err := parseRequestHead(frame)
	if err != nil {
		return nil, err
	}
	if head.contentLength > int64(r.maxBody) {
		return nil, http2.StreamError{StreamID: id, Code: http2.ErrCodeEnhanceYourCalm}
	}
	req := &receivedRequest{head: head, window: receiveWindow{available: r.initialWindow}, complete: frame.StreamEnded()}
	r.streams[id] = req
	if req.complete {
		return req, nil
	}
	return nil, nil
}

// Existing streams may carry a final trailer block, never another request head.
func (r *requestReceiver) headers(frame *http2.MetaHeadersFrame) (*receivedRequest, receiveCredit, error) {
	if r.closed {
		return nil, receiveCredit{}, errQueueClosed
	}
	req := r.streams[frame.StreamID]
	if req == nil {
		request, err := r.open(frame)
		return request, receiveCredit{}, err
	}
	if req.complete {
		return r.rejectData(frame.StreamID, 0, http2.ErrCodeStreamClosed)
	}
	if !validTrailers(frame) || req.head.contentLength >= 0 && int64(len(req.body)) != req.head.contentLength {
		return r.rejectData(frame.StreamID, 0, http2.ErrCodeProtocol)
	}
	req.trailers = slices.Clone(frame.Fields)
	req.complete = true
	return req, receiveCredit{connection: r.window.release(0, true)}, nil
}

func validTrailers(frame *http2.MetaHeadersFrame) bool {
	if frame.Truncated || !frame.StreamEnded() || len(frame.PseudoFields()) != 0 {
		return false
	}
	for _, field := range frame.Fields {
		switch field.Name {
		case "content-length", "host", "connection", "proxy-connection", "keep-alive", "transfer-encoding", "upgrade", "te":
			return false
		}
	}
	return true
}

func (r *requestReceiver) data(frame *http2.DataFrame) (*receivedRequest, receiveCredit, error) {
	id, size := frame.StreamID, frame.Length
	if r.closed {
		return nil, receiveCredit{}, errQueueClosed
	}
	if id == 0 || id&1 == 0 || id > r.lastStream {
		return nil, receiveCredit{}, http2.ConnectionError(http2.ErrCodeProtocol)
	}
	if !r.window.consume(size) {
		return nil, receiveCredit{}, http2.ConnectionError(http2.ErrCodeFlowControl)
	}
	req := r.streams[id]
	if req == nil || req.complete {
		return r.rejectData(id, size, http2.ErrCodeStreamClosed)
	}
	if !req.window.consume(size) {
		return r.rejectData(id, size, http2.ErrCodeFlowControl)
	}
	if !r.appendBody(req, frame.Data()) {
		return r.rejectData(id, size, http2.ErrCodeEnhanceYourCalm)
	}
	if req.head.contentLength >= 0 && (int64(len(req.body)) > req.head.contentLength || frame.StreamEnded() && int64(len(req.body)) != req.head.contentLength) {
		return r.rejectData(id, size, http2.ErrCodeProtocol)
	}
	credit := receiveCredit{connection: r.window.release(size, frame.StreamEnded())}
	if frame.StreamEnded() {
		req.complete = true
		return req, credit, nil
	}
	credit.stream = req.window.release(size, false)
	return nil, credit, nil
}

func (r *requestReceiver) rejectData(id, size uint32, code http2.ErrCode) (*receivedRequest, receiveCredit, error) {
	// Completed bodies may already be in use by business code; only that consumer
	// can release their budget. Incomplete rejected bodies have no consumer.
	if req := r.streams[id]; req != nil && !req.complete {
		r.release(id)
	}
	return nil, receiveCredit{connection: r.window.release(size, true)}, http2.StreamError{StreamID: id, Code: code}
}

func (r *requestReceiver) appendBody(req *receivedRequest, data []byte) bool {
	if len(data) > r.maxBody-len(req.body) {
		return false
	}
	needed := len(req.body) + len(data)
	if len(req.body) == 0 && cap(req.body) == 0 && needed > 0 && cap(r.spare) >= needed {
		req.body, r.spare = r.spare[:0], nil
		r.buffered += cap(req.body)
	}
	if needed > cap(req.body) {
		// Discard idle storage before growth so it cannot consume live budget.
		r.budget.give(cap(r.spare))
		r.spare = nil
		grown := cap(req.body) + min(cap(req.body), r.maxBody-cap(req.body))
		capacity := min(r.maxBody, max(needed, max(1024, grown)))
		// Include both old and new buffers while growing, rather than charging len.
		if capacity > r.limit-r.buffered {
			capacity = needed
		}
		if capacity > r.limit-r.buffered {
			return false
		}
		if !r.budget.take(capacity) {
			return false
		}
		body := make([]byte, len(req.body), capacity)
		copy(body, req.body)
		r.budget.give(cap(req.body))
		r.buffered += capacity - cap(req.body)
		req.body = body
	}
	req.body = append(req.body, data...)
	return true
}

func (r *requestReceiver) release(id uint32) {
	if req := r.streams[id]; req != nil {
		r.buffered -= cap(req.body)
		if capacity := cap(req.body); capacity > 0 && capacity <= 64<<10 && capacity > cap(r.spare) {
			r.budget.give(cap(r.spare))
			r.spare = req.body[:0]
		} else {
			r.budget.give(cap(req.body))
		}
		req.body = nil
		delete(r.streams, id)
	}
}

func (r *requestReceiver) close() {
	r.closed = true
	r.budget.give(r.buffered + cap(r.spare))
	clear(r.streams)
	r.buffered = 0
	r.spare = nil
}
