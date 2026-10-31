package api

import "sync"

// connectionSubscriptions owns registrations until their write pumps exit.
// Only the connection read loop adds entries or closes the set; write pumps
// remove their own entries concurrently. No lock is taken per stream message.
type connectionSubscriptions struct {
	mu       sync.Mutex
	items    []connectionSubscription
	lastID   uint64
	reserved bool
}

type connectionSubscription struct {
	id      uint64
	apiName string
	sub     *StreamSub
}

// reserve consumes every attempted ID, including failed subscriptions. Strictly
// increasing IDs prevent reuse without an unbounded set of retired IDs. Only
// the connection read loop accesses this watermark; writers touch items only.
func (s *connectionSubscriptions) reserve(id uint64) bool {
	if s.reserved && id <= s.lastID {
		return false
	}
	s.lastID, s.reserved = id, true
	return true
}

func (s *connectionSubscriptions) add(id uint64, apiName string, sub *StreamSub) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, connectionSubscription{id, apiName, sub})
}

func (s *connectionSubscriptions) take(id uint64) (connectionSubscription, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, item := range s.items {
		if item.id == id {
			return s.removeAt(i), true
		}
	}
	return connectionSubscription{}, false
}

func (s *connectionSubscriptions) remove(id uint64, sub *StreamSub) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A late writer must not remove a newer subscription to the same API.
	for i, item := range s.items {
		if item.id == id && item.sub == sub {
			s.removeAt(i)
			return
		}
	}
}

// Registration order is not observable; swap removal avoids shifting the tail.
// The caller holds mu. Clear the unused slot so it cannot retain a StreamSub.
func (s *connectionSubscriptions) removeAt(index int) connectionSubscription {
	item := s.items[index]
	last := len(s.items) - 1
	s.items[index] = s.items[last]
	s.items[last] = connectionSubscription{}
	s.items = s.items[:last]
	return item
}

func (s *connectionSubscriptions) close(hub *StreamHub) {
	s.mu.Lock()
	items := s.items
	s.items = nil
	s.mu.Unlock()
	// Release the connection lock before acquiring the hub lock or cancelling.
	for _, item := range items {
		hub.Unsubscribe(item.apiName, item.sub)
	}
}
