package h2transport

import (
	"errors"
	"testing"
)

func testQueue(t *testing.T, streams, bytes int) (*dataQueue, *int) {
	t.Helper()
	released := new(int)
	q, err := newDataQueue(streams, bytes, func([]byte) { *released++ })
	if err != nil {
		t.Fatal(err)
	}
	return q, released
}

func queueBody(t *testing.T, q *dataQueue, id uint32, window int64, body []byte) {
	t.Helper()
	if err := q.open(id, sendWindow(window)); err != nil {
		t.Fatal(err)
	}
	if err := q.submit(id, body); err != nil {
		t.Fatal(err)
	}
}

func TestQueueFairFrames(t *testing.T) {
	q, released := testQueue(t, 2, 32)
	queueBody(t, q, 1, 100, []byte("abcdef"))
	queueBody(t, q, 3, 100, []byte("1234"))
	connection := sendWindow(100)
	for _, want := range []struct {
		id   uint32
		text string
		end  bool
	}{
		{1, "ab", false}, {3, "12", false}, {1, "cd", false}, {3, "34", true}, {1, "ef", true},
	} {
		frame, ok, err := q.next(&connection, 2)
		if err != nil || !ok || frame.streamID != want.id || string(frame.data) != want.text || frame.end != want.end {
			t.Fatalf("frame=%+v err=%v", frame, err)
		}
		if err := q.complete(frame.ticket); err != nil {
			t.Fatal(err)
		}
	}
	if *released != 2 || q.retained != 0 || connection != 90 {
		t.Fatal("incorrect final accounting")
	}
	if _, ok, err := q.next(&connection, 2); ok || err != nil {
		t.Fatal("unexpected pending frame")
	}
}

func TestQueueOwnershipAndBudget(t *testing.T) {
	q, released := testQueue(t, 2, 8)
	body := make([]byte, 3, 8)
	copy(body, "abc")
	queueBody(t, q, 1, 100, body)
	if err := q.open(3, 100); err != nil {
		t.Fatal(err)
	}
	if err := q.submit(3, []byte("x")); !errors.Is(err, errQueueFull) {
		t.Fatal(err)
	}
	if *released != 0 || q.retained != 8 {
		t.Fatal("rejected body taken or capacity undercounted")
	}
	connection := sendWindow(100)
	f, _, _ := q.next(&connection, 1)
	if &f.data[0] != &body[0] {
		t.Fatal("body copied")
	}
	if err := q.cancel(1); err != nil {
		t.Fatal(err)
	}
	if *released != 0 || q.retained != 8 || string(f.data) != "a" {
		t.Fatal("in-flight buffer released early")
	}
	if err := q.complete(f.ticket); err != nil {
		t.Fatal(err)
	}
	if *released != 1 || q.retained != 0 {
		t.Fatal("cancelled buffer not released")
	}
	if err := q.complete(f.ticket); !errors.Is(err, errInvalidTicket) {
		t.Fatal(err)
	}
	if err := q.submit(3, []byte("x")); err != nil {
		t.Fatal(err)
	}
	q.close()
	q.close()
	if *released != 2 {
		t.Fatal("double release")
	}
}

func TestQueueBlockedStreamAndEmptyEnd(t *testing.T) {
	q, _ := testQueue(t, 3, 32)
	queueBody(t, q, 1, 0, []byte("abc"))
	queueBody(t, q, 3, 2, []byte("xy"))
	queueBody(t, q, 5, -10, nil)
	connection := sendWindow(0)
	f, ok, err := q.next(&connection, 10)
	if err != nil || !ok || f.streamID != 5 || !f.end || len(f.data) != 0 {
		t.Fatal("empty END_STREAM incorrectly blocked")
	}
	if err := q.complete(f.ticket); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := q.next(&connection, 10); ok {
		t.Fatal("connection window bypassed")
	}
	if err := connection.increase(10); err != nil {
		t.Fatal(err)
	}
	f, ok, _ = q.next(&connection, 10)
	if !ok || f.streamID != 3 {
		t.Fatal("blocked stream starved ready stream")
	}
	_ = q.complete(f.ticket)
	if _, ok, _ := q.next(&connection, 10); ok {
		t.Fatal("stream window bypassed")
	}
	if err := q.increase(1, 3); err != nil {
		t.Fatal(err)
	}
	f, ok, _ = q.next(&connection, 10)
	if !ok || f.streamID != 1 || string(f.data) != "abc" {
		t.Fatal("window update did not reactivate stream")
	}
	_ = q.complete(f.ticket)
}

func TestQueueCloseDuringWrite(t *testing.T) {
	q, released := testQueue(t, 3, 32)
	queueBody(t, q, 1, 100, []byte("abc"))
	queueBody(t, q, 3, 100, []byte("xyz"))
	if err := q.open(5, 100); err != nil {
		t.Fatal(err)
	}
	connection := sendWindow(100)
	f, _, _ := q.next(&connection, 2)
	q.close()
	if *released != 1 || q.retained != 3 || string(f.data) != "ab" {
		t.Fatal("close invalidated active write")
	}
	if err := q.complete(f.ticket); err != nil {
		t.Fatal(err)
	}
	if *released != 2 || q.retained != 0 || len(q.streams) != 0 {
		t.Fatal("close leaked buffers or streams")
	}
	if err := q.open(7, 100); !errors.Is(err, errQueueClosed) {
		t.Fatal(err)
	}
	if err := q.submit(7, nil); !errors.Is(err, errQueueClosed) {
		t.Fatal(err)
	}
	if _, _, err := q.next(&connection, 10); !errors.Is(err, errQueueClosed) {
		t.Fatal(err)
	}
}
