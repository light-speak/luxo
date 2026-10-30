package h2transport

import "sync/atomic"

// byteBudget bounds transport-owned capacities, not application allocations,
// kernel buffers or Go runtime memory. It is shared by all server connections.
type byteBudget struct {
	limit int64
	used  atomic.Int64
}

func (b *byteBudget) take(size int) bool {
	if b == nil {
		return true
	}
	for {
		used := b.used.Load()
		if size < 0 || int64(size) > b.limit-used {
			return false
		}
		if b.used.CompareAndSwap(used, used+int64(size)) {
			return true
		}
	}
}

func (b *byteBudget) give(size int) {
	if b != nil {
		b.used.Add(-int64(size))
	}
}
