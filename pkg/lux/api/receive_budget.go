package api

import (
	"errors"
	"sync/atomic"
)

var errReceiveCapacity = errors.New("request receive buffer capacity exhausted")

// receiveLease has one owner. Transfer it with a message, never copy an active
// lease to two consumers. It charges retained buffers, not decoded business data.
type receiveLease struct {
	counter *atomic.Int64
	limit   int64
	bytes   int64
}

func (l *receiveLease) acquire(size int) error {
	if l == nil || l.counter == nil || size == 0 {
		return nil
	}
	if !reserveWS(l.counter, int64(size), l.limit) {
		return errReceiveCapacity
	}
	l.bytes += int64(size)
	return nil
}

func (l *receiveLease) discard(size int) {
	if l == nil || l.counter == nil || size == 0 {
		return
	}
	l.counter.Add(-int64(size))
	l.bytes -= int64(size)
}

func (l *receiveLease) release() {
	if l == nil || l.counter == nil {
		return
	}
	l.counter.Add(-l.bytes)
	l.bytes = 0
}

func (rt *Router) newReceiveLease() receiveLease {
	return receiveLease{counter: &rt.receiveBytes, limit: rt.bodyLimits.ReceiveBytesTotal}
}
