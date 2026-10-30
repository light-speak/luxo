package h2transport

import "errors"

var (
	errQueueConfig       = errors.New("invalid experimental DATA queue configuration")
	errQueueFull         = errors.New("HTTP/2 DATA queue budget exhausted")
	errQueueClosed       = errors.New("HTTP/2 DATA queue closed")
	errStreamUnavailable = errors.New("HTTP/2 stream unavailable for DATA submission")
	errWriteInFlight     = errors.New("HTTP/2 DATA write still in flight")
	errInvalidTicket     = errors.New("invalid HTTP/2 DATA completion ticket")
	errTicketExhausted   = errors.New("HTTP/2 DATA completion identity exhausted")
	errInvalidFrameSize  = errors.New("invalid HTTP/2 DATA frame limit")
)

type dataSlot struct {
	id                           uint32
	window                       sendWindow
	body                         []byte
	offset                       int
	next, prev                   int
	queued, submitted, cancelled bool
}

type dataChunk struct {
	streamID uint32
	data     []byte
	end      bool
	ticket   uint64
}

// dataQueue schedules complete unary response bodies, one per registered stream.
// Only the connection writer may call its methods. Producer handoff, HEADERS,
// stream validation and network deadlines belong to the connection layer.
// Byte accounting includes the capacity of submitted buffers until completion.
// It excludes producer buffers, metadata and the downstream socket write buffer.
type dataQueue struct {
	slots                   []dataSlot
	streams                 map[uint32]int
	free, head, ready, busy int
	retained, limit         int
	sequence                uint64
	closed                  bool
	recycle                 func([]byte)
	released                func(uint32)
}

func newDataQueue(maxStreams, maxBytes int, recycle func([]byte)) (*dataQueue, error) {
	// This experimental allocation limit is not an HTTP/2 stream-ID limit.
	if maxStreams < 1 || maxStreams > 65535 || maxBytes < 1 || recycle == nil {
		return nil, errQueueConfig
	}
	q := &dataQueue{slots: make([]dataSlot, maxStreams+1), streams: make(map[uint32]int, maxStreams), free: 1, limit: maxBytes, recycle: recycle}
	for i := 1; i < maxStreams; i++ {
		q.slots[i].next = i + 1
	}
	return q, nil
}

func (q *dataQueue) open(id uint32, window sendWindow) error {
	if q.closed {
		return errQueueClosed
	}
	if id == 0 || id&1 == 0 || id > uint32(maxWindow) || int64(window) < -maxWindow || int64(window) > maxWindow {
		return errStreamUnavailable
	}
	if _, exists := q.streams[id]; exists {
		return errStreamUnavailable
	}
	if q.free == 0 {
		return errQueueFull
	}
	index := q.free
	q.free = q.slots[index].next
	q.slots[index] = dataSlot{id: id, window: window}
	q.streams[id] = index
	return nil
}

// submit transfers exclusive ownership on success only. The buffer must start
// at its backing allocation's beginning with its full capacity, not a clipped
// view. The producer must stop using it until recycle returns it to its owner.
// No body copy is performed. recycle must be a nonblocking internal recycler.
func (q *dataQueue) submit(id uint32, body []byte) error {
	if q.closed {
		return errQueueClosed
	}
	index := q.streams[id]
	if index == 0 || q.slots[index].submitted || q.slots[index].cancelled {
		return errStreamUnavailable
	}
	if cap(body) > q.limit-q.retained {
		return errQueueFull
	}
	s := &q.slots[index]
	s.body, s.submitted = body, true
	q.retained += cap(body)
	q.refresh(index)
	return nil
}

// next reserves credit and lends one DATA slice to the writer. complete must
// follow only after the writer no longer references the slice. A network write
// failure requires close before complete; possibly sent data cannot be retried.
func (q *dataQueue) next(connection *sendWindow, frameSize int) (dataChunk, bool, error) {
	if q.closed {
		return dataChunk{}, false, errQueueClosed
	}
	if q.busy != 0 {
		return dataChunk{}, false, errWriteInFlight
	}
	if frameSize < 1 || frameSize > 1<<24-1 {
		return dataChunk{}, false, errInvalidFrameSize
	}
	if q.sequence == ^uint64(0) {
		return dataChunk{}, false, errTicketExhausted
	}
	for remaining := q.ready; remaining > 0; remaining-- {
		index := q.head
		s := &q.slots[index]
		count := min(frameSize, len(s.body)-s.offset)
		if count > 0 {
			count = int(min(int64(count), max(0, int64(*connection)), max(0, int64(s.window))))
			if count == 0 {
				q.head = s.next
				continue
			}
		}
		// count was checked against both windows; zero-byte END needs no credit.
		*connection -= sendWindow(count)
		s.window -= sendWindow(count)
		start := s.offset
		s.offset += count
		q.unlink(index)
		q.busy = index
		q.sequence++
		return dataChunk{s.id, s.body[start:s.offset:s.offset], s.offset == len(s.body), q.sequence}, true, nil
	}
	return dataChunk{}, false, nil
}

func (q *dataQueue) complete(ticket uint64) error {
	if q.busy == 0 || ticket != q.sequence {
		return errInvalidTicket
	}
	q.completeCurrent()
	return nil
}

// completeCurrent is only used after a validated ticket or by the synchronous
// writer immediately after next. Neither path can interleave another dequeue.
func (q *dataQueue) completeCurrent() {
	index := q.busy
	q.busy = 0
	s := &q.slots[index]
	if s.cancelled || s.offset == len(s.body) {
		q.release(index)
	} else {
		q.refresh(index)
	}
}

func (q *dataQueue) cancel(id uint32) error {
	index := q.streams[id]
	if index == 0 {
		return errStreamUnavailable
	}
	q.cancelKnown(index)
	return nil
}

func (q *dataQueue) cancelKnown(index int) {
	q.unlink(index)
	q.slots[index].cancelled = true
	if q.busy != index {
		q.release(index)
	}
}

func (q *dataQueue) close() {
	q.closed = true
	for id := range q.streams {
		_ = q.cancel(id)
	}
}

func (q *dataQueue) increase(id uint32, increment uint32) error {
	if q.closed {
		return errQueueClosed
	}
	index := q.streams[id]
	if index == 0 || q.slots[index].cancelled {
		return errStreamUnavailable
	}
	if err := q.slots[index].window.increase(increment); err != nil {
		return err
	}
	q.refresh(index)
	return nil
}
