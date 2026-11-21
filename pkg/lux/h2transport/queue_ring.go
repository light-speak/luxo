package h2transport

func (q *dataQueue) refresh(index int) {
	s := &q.slots[index]
	ready := s.submitted && !s.cancelled && q.busy != index && (s.window > 0 || s.offset == len(s.body))
	if !ready {
		q.unlink(index)
		return
	}
	if s.queued {
		return
	}
	if q.head == 0 {
		q.head = index
		s.next, s.prev = index, index
	} else {
		tail := q.slots[q.head].prev
		s.next, s.prev = q.head, tail
		q.slots[tail].next = index
		q.slots[q.head].prev = index
	}
	s.queued = true
	q.ready++
}

func (q *dataQueue) unlink(index int) {
	s := &q.slots[index]
	if !s.queued {
		return
	}
	if q.ready == 1 {
		q.head = 0
	} else {
		q.slots[s.prev].next = s.next
		q.slots[s.next].prev = s.prev
		if q.head == index {
			q.head = s.next
		}
	}
	s.next, s.prev, s.queued = 0, 0, false
	q.ready--
}

func (q *dataQueue) release(index int) {
	s := &q.slots[index]
	q.unlink(index)
	body, submitted, id := s.body, s.submitted, s.id
	q.retained -= cap(body)
	delete(q.streams, s.id)
	*s = dataSlot{next: q.free}
	q.free = index
	if submitted {
		q.recycle(body)
	}
	if q.released != nil {
		q.released(id)
	}
}

// changeInitial validates every live stream before changing any of them.
func (q *dataQueue) changeInitial(previous, next uint32) error {
	if err := q.validateInitial(previous, next); err != nil {
		return err
	}
	q.commitInitial(previous, next)
	return nil
}

// commitInitial is called only after validating the entire settings change.
func (q *dataQueue) commitInitial(previous, next uint32) {
	delta := sendWindow(int64(next) - int64(previous))
	for _, index := range q.streams {
		q.slots[index].window += delta
		q.refresh(index)
	}
}

func (q *dataQueue) validateInitial(previous, next uint32) error {
	if q.closed {
		return errQueueClosed
	}
	if int64(previous) > maxWindow || int64(next) > maxWindow {
		return errInvalidWindow
	}
	for _, index := range q.streams {
		candidate := q.slots[index].window
		if err := candidate.changeInitial(previous, next); err != nil {
			return err
		}
	}
	return nil
}
