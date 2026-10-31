package h2transport

import (
	"context"
	"net"
	"net/http"
	"testing"
)

func TestGlobalAdmissionAcrossConnections(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	slots := &byteBudget{limit: 1}
	a := newCandidateConnection(context.Background(), left, http.NotFoundHandler())
	b := newCandidateConnection(context.Background(), right, http.NotFoundHandler())
	a.slots, b.slots = slots, slots
	if err := a.receive(receiveHead(t, 1, "", false), nil); err != nil {
		t.Fatal(err)
	}
	if err := b.receive(receiveHead(t, 1, "", false), nil); err == nil {
		t.Fatal("global stream limit bypassed")
	}
	a.outputFinished(1)
	if slots.used.Load() != 0 {
		t.Fatal("admission leaked")
	}
	if err := b.receive(receiveHead(t, 3, "", false), nil); err != nil {
		t.Fatal(err)
	}
	b.outputFinished(3)
}

func TestSharedReceiveBudget(t *testing.T) {
	b := &byteBudget{limit: 2048}
	a, _ := newRequestReceiver(2, 2048, 4096)
	c, _ := newRequestReceiver(2, 2048, 4096)
	a.budget, c.budget = b, b
	ra, rc := &receivedRequest{}, &receivedRequest{}
	a.streams[1], c.streams[1] = ra, rc
	if !a.appendBody(ra, make([]byte, 2048)) || c.appendBody(rc, []byte("x")) {
		t.Fatal("global limit bypassed")
	}
	a.release(1)
	if b.used.Load() != 2048 {
		t.Fatal("idle capacity uncharged")
	}
	a.close()
	if !c.appendBody(rc, []byte("x")) {
		t.Fatal("close leaked budget")
	}
	c.close()
	if b.used.Load() != 0 {
		t.Fatal("budget leaked")
	}
}
