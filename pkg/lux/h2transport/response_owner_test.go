package h2transport

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/api"
)

func TestTransferredBufferSurvivesInFlightCancellation(t *testing.T) {
	budget := &byteBudget{limit: 128}
	r := &response{header: make(http.Header), limit: 128, budget: budget}
	b := &api.ResponseBuf{B: make([]byte, 5, 64)}
	copy(b.B, "owned")
	if !r.TakeBinaryResponse(b) {
		t.Fatal("transfer")
	}
	receiver, _ := newRequestReceiver(1, 128, 128)
	c := &candidateConnection{receiver: receiver, tasks: make(map[uint32]*requestTask)}
	_, cancel := context.WithCancel(context.Background())
	c.tasks[1] = &requestTask{id: 1, cancel: cancel, response: r, dispatched: true, done: true}
	q, _ := newDataQueue(1, 128, func([]byte) {})
	q.released = c.outputFinished
	queueBody(t, q, 1, 128, r.body)
	window := sendWindow(128)
	chunk, ok, err := q.next(&window, 2)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err := q.cancel(1); err != nil {
		t.Fatal(err)
	}
	if budget.used.Load() != 64 || r.owner == nil || string(chunk.data) != "ow" {
		t.Fatal("freed in-flight data")
	}
	if err := q.complete(chunk.ticket); err != nil {
		t.Fatal(err)
	}
	if budget.used.Load() != 0 || r.owner != nil || len(c.tasks) != 0 {
		t.Fatal("canceled transfer leaked")
	}
	r.release()
	if budget.used.Load() != 0 {
		t.Fatal("double release")
	}
}

func TestResponseBufferOwnership(t *testing.T) {
	b := api.GetBuf()
	b.B = append(b.B, "owned"...)
	r := &response{header: make(http.Header), limit: 32}
	if !r.TakeBinaryResponse(b) || &r.body[0] != &b.B[0] {
		t.Fatal("response copied")
	}
	if _, err := r.Write([]byte("extra")); err == nil {
		t.Fatal("write after transfer accepted")
	}
	r.release()
	if r.owner != nil || r.body != nil {
		t.Fatal("ownership retained")
	}
	r.release()
}

func TestResponseOwnershipRejected(t *testing.T) {
	b := api.GetBuf()
	defer api.PutBuf(b)
	b.B = append(b.B, "large"...)
	r := &response{header: make(http.Header), limit: 2}
	if r.TakeBinaryResponse(b) || r.owner != nil {
		t.Fatal("oversized transfer accepted")
	}
	if string(b.B) != "large" {
		t.Fatal("rejected buffer recycled")
	}
}

func TestResponseTransferBudgetRejectionPreservesOwner(t *testing.T) {
	budget := &byteBudget{limit: 8}
	buf := &api.ResponseBuf{B: make([]byte, 4, 16)}
	copy(buf.B, "data")
	r := &response{header: make(http.Header), limit: 32, budget: budget}
	if r.TakeBinaryResponse(buf) || !errors.Is(r.err, errQueueFull) {
		t.Fatal("over-budget transfer accepted", r.err)
	}
	if r.owner != nil || r.body != nil || string(buf.B) != "data" {
		t.Fatal("rejected transfer changed ownership")
	}
	r.release()
	if budget.used.Load() != 0 {
		t.Fatal("rejected transfer changed budget")
	}
}

func TestResponseGrowthBudgetRejectionPreservesAcceptedBody(t *testing.T) {
	budget := &byteBudget{limit: 4}
	r := &response{header: make(http.Header), limit: 16, budget: budget}
	if n, err := r.Write([]byte("data")); n != 4 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := r.Write([]byte("x")); n != 0 || !errors.Is(err, errQueueFull) {
		t.Fatal(n, err)
	}
	if string(r.body) != "data" || budget.used.Load() != 4 {
		t.Fatal("failed growth changed body or budget")
	}
	r.release()
	r.release()
	if budget.used.Load() != 0 {
		t.Fatal("failed growth leaked or double-released budget")
	}
}
