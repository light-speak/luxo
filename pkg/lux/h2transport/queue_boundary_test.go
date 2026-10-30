package h2transport

import (
	"errors"
	"math/rand/v2"
	"testing"
)

func TestQueueMixedLifecycle(t *testing.T) {
	q, released := testQueue(t, 8, 64)
	random := rand.New(rand.NewPCG(31, 97))
	accepted := 0
	connection := sendWindow(65535)
	var ticket uint64
	for range 10000 {
		id := uint32(random.IntN(8)*2 + 1)
		switch random.IntN(7) {
		case 0:
			_ = q.open(id, sendWindow(random.IntN(8)))
		case 1:
			if err := q.submit(id, make([]byte, random.IntN(9))); err == nil {
				accepted++
			}
		case 2:
			_ = q.cancel(id)
		case 3:
			_ = q.increase(id, uint32(random.IntN(8)))
		case 4:
			if f, ok, err := q.next(&connection, 3); err == nil && ok {
				ticket = f.ticket
			}
		case 5:
			_ = q.complete(ticket)
		case 6:
			_ = q.changeInitial(4, uint32(random.IntN(9)))
		}
		assertQueueAccounting(t, q)
	}
	q.close()
	if q.busy != 0 {
		if err := q.complete(ticket); err != nil {
			t.Fatal(err)
		}
	}
	assertQueueAccounting(t, q)
	if *released != accepted || len(q.streams) != 0 {
		t.Fatal("ownership leak", accepted, *released)
	}
}

func assertQueueAccounting(t *testing.T, q *dataQueue) {
	t.Helper()
	retained, ready := 0, 0
	for _, index := range q.streams {
		s := q.slots[index]
		retained += cap(s.body)
		if s.queued {
			ready++
		}
	}
	if retained != q.retained || retained > q.limit || ready != q.ready {
		t.Fatal("accounting mismatch")
	}
	index := q.head
	seen := make(map[int]bool)
	for range q.ready {
		if index < 1 || index >= len(q.slots) || seen[index] || !q.slots[index].queued {
			t.Fatal("damaged ready ring")
		}
		seen[index] = true
		next := q.slots[index].next
		if q.slots[next].prev != index {
			t.Fatal("damaged reverse link")
		}
		index = next
	}
	if index != q.head || (q.ready == 0 && q.head != 0) {
		t.Fatal("ring did not close")
	}
}

func TestQueueRejectsInvalidState(t *testing.T) {
	for _, cfg := range []struct {
		streams, bytes int
		recycle        func([]byte)
	}{
		{0, 1, func([]byte) {}}, {65536, 1, func([]byte) {}}, {1, 0, func([]byte) {}}, {1, 1, nil},
	} {
		if _, err := newDataQueue(cfg.streams, cfg.bytes, cfg.recycle); !errors.Is(err, errQueueConfig) {
			t.Fatal(err)
		}
	}
	q, _ := testQueue(t, 1, 4)
	for _, tc := range []struct {
		id     uint32
		window sendWindow
	}{{0, 1}, {2, 1}, {1<<31 | 1, 1}, {1, sendWindow(maxWindow + 1)}, {1, sendWindow(-maxWindow - 1)}} {
		if err := q.open(tc.id, tc.window); !errors.Is(err, errStreamUnavailable) {
			t.Fatal(err)
		}
	}
	if err := q.submit(1, nil); !errors.Is(err, errStreamUnavailable) {
		t.Fatal(err)
	}
	if err := q.increase(1, 1); !errors.Is(err, errStreamUnavailable) {
		t.Fatal(err)
	}
	if err := q.cancel(1); !errors.Is(err, errStreamUnavailable) {
		t.Fatal(err)
	}
	queueBody(t, q, 1, 10, []byte("a"))
	if err := q.open(1, 10); !errors.Is(err, errStreamUnavailable) {
		t.Fatal(err)
	}
	if err := q.open(3, 10); !errors.Is(err, errQueueFull) {
		t.Fatal(err)
	}
	if err := q.submit(1, nil); !errors.Is(err, errStreamUnavailable) {
		t.Fatal(err)
	}
	if err := q.increase(1, 0); !errors.Is(err, errInvalidWindow) {
		t.Fatal(err)
	}
	connection := sendWindow(10)
	for _, size := range []int{0, 1 << 24} {
		if _, _, err := q.next(&connection, size); !errors.Is(err, errInvalidFrameSize) {
			t.Fatal(err)
		}
	}
	f, _, _ := q.next(&connection, 1)
	if _, _, err := q.next(&connection, 1); !errors.Is(err, errWriteInFlight) {
		t.Fatal(err)
	}
	if err := q.complete(f.ticket + 1); !errors.Is(err, errInvalidTicket) {
		t.Fatal(err)
	}
	_ = q.cancel(1)
	if err := q.increase(1, 1); !errors.Is(err, errStreamUnavailable) {
		t.Fatal(err)
	}
	_ = q.complete(f.ticket)
	q.sequence = ^uint64(0)
	if _, _, err := q.next(&connection, 1); !errors.Is(err, errTicketExhausted) {
		t.Fatal(err)
	}
	q.close()
	if err := q.increase(1, 1); !errors.Is(err, errQueueClosed) {
		t.Fatal(err)
	}
}

func TestQueueSettingsAtomicity(t *testing.T) {
	q, _ := testQueue(t, 2, 8)
	queueBody(t, q, 1, 10, []byte("ab"))
	queueBody(t, q, 3, maxWindow, []byte("xy"))
	if err := q.changeInitial(0, 1); !errors.Is(err, errInvalidWindow) {
		t.Fatal(err)
	}
	if q.slots[q.streams[1]].window != 10 {
		t.Fatal("partial SETTINGS commit")
	}
	for _, limits := range [][2]uint32{{1 << 31, 0}, {0, 1 << 31}} {
		if err := q.changeInitial(limits[0], limits[1]); !errors.Is(err, errInvalidWindow) {
			t.Fatal(err)
		}
	}
	if err := q.changeInitial(20, 0); err != nil || q.ready != 1 {
		t.Fatal("negative window not suspended", err)
	}
	if err := q.changeInitial(0, 20); err != nil || q.ready != 2 {
		t.Fatal("stream not reactivated", err)
	}
	q.close()
	if err := q.changeInitial(0, 1); !errors.Is(err, errQueueClosed) {
		t.Fatal(err)
	}
}

func TestQueueStaleCompletionAfterSlotReuse(t *testing.T) {
	q, released := testQueue(t, 1, 8)
	queueBody(t, q, 1, 10, []byte("a"))
	connection := sendWindow(10)
	first, _, _ := q.next(&connection, 10)
	_ = q.complete(first.ticket)
	queueBody(t, q, 3, 10, []byte("b"))
	second, _, _ := q.next(&connection, 10)
	if err := q.complete(first.ticket); !errors.Is(err, errInvalidTicket) || *released != 1 {
		t.Fatal("old completion released new body")
	}
	_ = q.complete(second.ticket)
	if *released != 2 {
		t.Fatal("new body not released")
	}
}

func TestQueueWindowExhaustionBetweenFrames(t *testing.T) {
	q, _ := testQueue(t, 1, 8)
	queueBody(t, q, 1, 1, []byte("ab"))
	connection := sendWindow(10)
	f, _, _ := q.next(&connection, 10)
	if string(f.data) != "a" || f.end {
		t.Fatal("window overrun")
	}
	_ = q.complete(f.ticket)
	if q.ready != 0 || q.retained != 2 {
		t.Fatal("blocked body lost")
	}
	_ = q.increase(1, 1)
	f, ok, _ := q.next(&connection, 10)
	if !ok || string(f.data) != "b" || !f.end {
		t.Fatal("resume failed")
	}
	_ = q.complete(f.ticket)
}

func BenchmarkQueueUnary(b *testing.B) {
	body := make([]byte, 4096)
	q, err := newDataQueue(8, 32768, func([]byte) {})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		connection := sendWindow(65535)
		if err := q.open(1, 65535); err != nil {
			b.Fatal(err)
		}
		if err := q.submit(1, body); err != nil {
			b.Fatal(err)
		}
		f, ok, err := q.next(&connection, 16384)
		if err != nil || !ok {
			b.Fatal(err)
		}
		if err := q.complete(f.ticket); err != nil {
			b.Fatal(err)
		}
	}
}
