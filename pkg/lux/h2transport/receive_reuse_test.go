package h2transport

import "testing"

func TestReceiveBufferReuseOwnership(t *testing.T) {
	r, _ := newRequestReceiver(2, 4096, 8192)
	a := &receivedRequest{}
	r.streams[1] = a
	if !r.appendBody(a, []byte("first")) {
		t.Fatal("append")
	}
	address := &a.body[0]
	b := &receivedRequest{}
	r.streams[3] = b
	if !r.appendBody(b, []byte("second")) || &b.body[0] == address {
		t.Fatal("reused live body")
	}
	r.release(1)
	if a.body != nil || cap(r.spare) != 1024 {
		t.Fatal("release did not transfer ownership")
	}
	c := &receivedRequest{}
	if !r.appendBody(c, []byte("new")) || &c.body[0] != address || string(c.body) != "new" {
		t.Fatal("released storage not reused")
	}
	if string(b.body) != "second" || r.buffered != 2048 || r.spare != nil {
		t.Fatal("ownership or budget changed")
	}
}

func TestReceiveSpareBoundAndPressure(t *testing.T) {
	r, _ := newRequestReceiver(2, 131072, 131072)
	r.streams[1] = &receivedRequest{body: make([]byte, 1, 65537)}
	r.buffered = 65537
	r.release(1)
	if r.spare != nil || r.buffered != 0 {
		t.Fatal("retained oversized body")
	}
	r.spare = make([]byte, 0, 1024)
	r.limit, r.maxBody = 2048, 2048
	a := &receivedRequest{}
	if !r.appendBody(a, make([]byte, 2048)) || r.spare != nil || r.buffered != 2048 {
		t.Fatal("idle storage prevented valid allocation")
	}
	r.spare = make([]byte, 0, 1024)
	r.close()
	if r.spare != nil || r.buffered != 0 {
		t.Fatal("closed receiver retained storage")
	}
}
