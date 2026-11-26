package h2transport

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"testing"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

func TestResponseHeadersBeforeData(t *testing.T) {
	q, _ := newDataQueue(2, 4096, func([]byte) {})
	var wire bytes.Buffer
	w := newControlWriter(newDataWriter(q, &wire, 4096))
	if err := w.apply(controlCommand{kind: openStreamCommand, streamID: 1}); err != nil {
		t.Fatal(err)
	}
	r := &response{status: 200, header: http.Header{"Content-Type": {"application/x-luxo"}}, body: []byte("hello")}
	if err := w.apply(controlCommand{kind: responseCommand, streamID: 1, response: r}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.data.writeBatch(&w.connection, 8, 65536, w.frameSize); err != nil {
		t.Fatal(err)
	}
	f := http2.NewFramer(nil, &wire)
	f.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	head, err := f.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	h := head.(*http2.MetaHeadersFrame)
	if h.PseudoValue("status") != "200" || h.StreamEnded() {
		t.Fatal(h)
	}
	frame, err := f.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	data := frame.(*http2.DataFrame)
	if string(data.Data()) != "hello" || !data.StreamEnded() {
		t.Fatal(data)
	}
}

func TestResponseHPACKFailureDoesNotWriteWireHeaders(t *testing.T) {
	for failAt := 1; failAt <= 3; failAt++ {
		q, _ := testQueue(t, 1, 32)
		if err := q.open(1, 100); err != nil {
			t.Fatal(err)
		}
		var wire bytes.Buffer
		w := newControlWriter(newDataWriter(q, &wire, 4096))
		// Production uses bytes.Buffer; inject an encoder sink failure to verify propagation.
		sink := &faultConn{failWrite: failAt}
		w.encoder = hpack.NewEncoder(sink)
		r := &response{status: 200, header: http.Header{"X-Test": {"value"}}, body: []byte("body")}
		if err := w.writeResponse(1, r); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal(failAt, err)
		}
		if sink.writes != failAt || wire.Len() != 0 || w.data.buffer.Buffered() != 0 || q.ready != 0 {
			t.Fatal("encoding continued or partial response escaped", failAt)
		}
		q.close()
	}
}

func TestResponseCaptureBoundaries(t *testing.T) {
	r := &response{header: make(http.Header), limit: 4}
	r.Header().Set("X-Snapshot", "before")
	r.WriteHeader(201)
	r.Header().Set("X-Snapshot", "after")
	r.WriteHeader(202)
	if r.status != 201 || r.committed.Get("X-Snapshot") != "before" {
		t.Fatal("committed headers changed")
	}
	if n, err := r.Write([]byte("ab")); n != 2 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := r.Write([]byte("cd")); n != 2 || err != nil || cap(r.body) > 4 {
		t.Fatal(n, err)
	}
	if _, err := r.Write([]byte("e")); !errors.Is(err, errResponse) {
		t.Fatal(err)
	}
	if _, err := r.Write(nil); !errors.Is(err, errResponse) {
		t.Fatal(err)
	}
	r = &response{header: make(http.Header), limit: 4}
	if _, err := r.Write(nil); err != nil || r.status != 200 {
		t.Fatal(err)
	}
	r = &response{header: make(http.Header), limit: 4}
	r.WriteHeader(101)
	if _, err := r.Write(nil); !errors.Is(err, errResponse) {
		t.Fatal(err)
	}
}

func TestResponseHeaderValidation(t *testing.T) {
	q, _ := newDataQueue(1, 8, func([]byte) {})
	var wire bytes.Buffer
	w := newControlWriter(newDataWriter(q, &wire, 32))
	for _, header := range []http.Header{
		{"Invalid Name": {"x"}}, {"X-Test": {"bad\nvalue"}},
		{"Content-Length": {"12"}}, {"Connection": {"close"}},
		{"X-Large": {string(bytes.Repeat([]byte{'x'}, 16384))}},
	} {
		if err := w.encodeResponseHead(&response{status: 200, header: header}); !errors.Is(err, errResponse) {
			t.Fatal(header, err)
		}
	}
	if err := w.encodeResponseHead(&response{status: 0}); !errors.Is(err, errResponse) {
		t.Fatal(err)
	}
	if err := w.encodeResponseHead(&response{status: 200, header: http.Header{"Content-Length": {"0"}}}); err != nil {
		t.Fatal(err)
	}
	if err := w.writeResponse(1, nil); err != nil {
		t.Fatal("reset response should be discarded", err)
	}
	_ = q.open(1, 65535)
	if err := w.writeResponse(1, nil); !errors.Is(err, errResponse) {
		t.Fatal(err)
	}
	if err := w.writeResponse(1, &response{status: 100}); err != nil {
		t.Fatal(err)
	}
	if err := w.data.buffer.Flush(); err != nil {
		t.Fatal(err)
	}
	frame, err := http2.NewFramer(nil, &wire).ReadFrame()
	reset, ok := frame.(*http2.RSTStreamFrame)
	if err != nil || !ok || reset.StreamID != 1 || reset.ErrCode != http2.ErrCodeInternal || len(q.streams) != 0 {
		t.Fatal("invalid response did not reset and release the stream", frame, err)
	}
	if err := q.open(3, 65535); err != nil {
		t.Fatal(err)
	}
	if err := w.writeResponse(3, &response{status: 200, body: make([]byte, 9)}); !errors.Is(err, errQueueFull) {
		t.Fatal(err)
	}
}

func TestBodylessResponseRejectsInvalidRepresentationLength(t *testing.T) {
	q, _ := testQueue(t, 1, 32)
	var wire bytes.Buffer
	w := newControlWriter(newDataWriter(q, &wire, 32))
	for _, sample := range []struct {
		status, headBytes int
		lengths           []string
	}{
		{status: http.StatusOK, headBytes: -1, lengths: []string{"-1"}},
		{status: http.StatusOK, headBytes: -1, lengths: []string{"4", "5"}},
		{status: http.StatusResetContent, lengths: []string{"4"}},
	} {
		r := &response{status: sample.status, headBytes: sample.headBytes, header: http.Header{"Content-Length": sample.lengths}}
		if err := w.encodeResponseHead(r); !errors.Is(err, errResponse) {
			t.Fatalf("status %d accepted invalid lengths %q: %v", sample.status, sample.lengths, err)
		}
	}
}

func TestHeadResponseCountsWritesWithoutCapturingBody(t *testing.T) {
	budget := &byteBudget{limit: 4}
	r := &response{status: http.StatusOK, headBytes: -1, limit: 4, budget: budget}
	for _, data := range [][]byte{nil, []byte("abc"), []byte("d")} {
		if n, err := r.Write(data); err != nil || n != len(data) {
			t.Fatalf("write length %d: %d, %v", len(data), n, err)
		}
	}
	if n, err := r.Write([]byte("e")); n != 0 || !errors.Is(err, errResponse) {
		t.Fatalf("over-limit HEAD write: %d, %v", n, err)
	}
	if r.headBytes != 4 || r.body != nil || budget.used.Load() != 0 {
		t.Fatal("HEAD changed its length or reserved body storage")
	}
}

func TestResponseContinuation(t *testing.T) {
	q, _ := newDataQueue(1, 8, func([]byte) {})
	_ = q.open(1, 65535)
	var wire bytes.Buffer
	w := newControlWriter(newDataWriter(q, &wire, 32))
	w.frameSize = 3 // Force fragmentation without a huge fixture.
	if err := w.writeResponse(1, &response{status: 200, header: http.Header{"X-Long": {"abcdefghijklmnopqrstuvwxyz"}}}); err != nil {
		t.Fatal(err)
	}
	if err := w.data.buffer.Flush(); err != nil {
		t.Fatal(err)
	}
	f := http2.NewFramer(nil, &wire)
	f.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	frame, err := f.ReadFrame()
	if err != nil || frame.(*http2.MetaHeadersFrame).PseudoValue("status") != "200" {
		t.Fatal(frame, err)
	}
}

func TestResponseFrameWriteFailures(t *testing.T) {
	for _, failWrite := range []int{1, 2} {
		t.Run(map[int]string{1: "headers", 2: "continuation"}[failWrite], func(t *testing.T) {
			q, _ := newDataQueue(1, 8, func([]byte) {})
			if err := q.open(1, 65535); err != nil {
				t.Fatal(err)
			}
			conn := &faultConn{failWrite: failWrite}
			w := newControlWriter(newDataWriter(q, conn, 32))
			w.data.framer = http2.NewFramer(conn, nil)
			w.frameSize = 3
			r := &response{status: 200, header: http.Header{"X-Test": {"fragmented"}}}
			if err := w.writeResponse(1, r); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatal("frame failure was swallowed", err)
			}
			if conn.writes != failWrite {
				t.Fatal("continued writing after failure", conn.writes)
			}
		})
	}
}
