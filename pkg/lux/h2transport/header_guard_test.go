package h2transport

import (
	"bytes"
	"io"
	"testing"
	"testing/iotest"

	"golang.org/x/net/http2"
)

func TestHeaderContinuationFloodRejected(t *testing.T) {
	var wire bytes.Buffer
	w := http2.NewFramer(&wire, nil)
	if err := w.WriteHeaders(http2.HeadersFrameParam{StreamID: 1}); err != nil {
		t.Fatal(err)
	}
	for range 65 {
		if err := w.WriteContinuation(1, false, nil); err != nil {
			t.Fatal(err)
		}
	}
	r := newRequestFramer(&wire)
	if _, err := r.ReadFrame(); err != http2.ConnectionError(http2.ErrCodeEnhanceYourCalm) {
		t.Fatal("unbounded header fragments", err)
	}
}

func TestHeaderGuardFragmentedReader(t *testing.T) {
	r := newRequestFramer(iotest.OneByteReader(bytes.NewReader(headerWire(t, requestFields(), true))))
	if _, err := r.ReadFrame(); err != nil {
		t.Fatal(err)
	}
}

func TestHeaderGuardEmptyReadPreservesFrameState(t *testing.T) {
	source := bytes.NewReader(headerWire(t, requestFields(), true))
	g := &headerGuard{source: source}
	for _, consumed := range []int{0, 4, 5} {
		if consumed != 0 {
			if _, err := io.ReadFull(g, make([]byte, consumed)); err != nil {
				t.Fatal(err)
			}
		}
		before, remaining := *g, source.Len()
		if n, err := g.Read(nil); n != 0 || err != nil {
			t.Fatal(n, err)
		}
		if *g != before || source.Len() != remaining {
			t.Fatal("empty read consumed input or changed framing state")
		}
	}
}

func TestHeaderGuardCompressedBudget(t *testing.T) {
	var wire bytes.Buffer
	w := http2.NewFramer(&wire, nil)
	block := make([]byte, 16384)
	_ = w.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: block})
	for range 4 {
		_ = w.WriteContinuation(1, false, block)
	}
	g := &headerGuard{source: &wire}
	if _, err := io.Copy(io.Discard, g); err != http2.ConnectionError(http2.ErrCodeEnhanceYourCalm) {
		t.Fatal(err)
	}
	if n, err := g.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatal("terminal guard resumed")
	}
}
