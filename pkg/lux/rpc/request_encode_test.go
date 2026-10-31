package rpc

import (
	"bytes"
	"testing"
)

func TestDirectCallEnvelopeMatchesExistingWire(t *testing.T) {
	for _, id := range []int{1, 127, 128, 1 << 21, 1<<31 - 1} {
		for _, size := range []int{0, 1, 127, 128, 4096, 65536} {
			params := bytes.Repeat([]byte{42}, size)
			mask := []byte{1, 2, 3}
			body := encodeCallRequest(id, mask, params)
			for _, token := range []string{"", "token"} {
				for _, kind := range []byte{requestKindCall, requestKindStream} {
					want := encodeRequestEnvelope(kind, token, body)
					got := prepareCallFrame(kind, token, id, mask, params)
					if !bytes.Equal(got[4:], want) {
						t.Fatalf("plain wire mismatch: id=%d size=%d", id, size)
					}
					if len(got) != cap(got) {
						t.Fatal("inexact capacity reservation")
					}
				}
				for _, details := range []bool{false, true} {
					want := encodeTracedRequestEnvelope(requestKindCall, token, "trace", "a.b", details, body)
					got := prepareTracedCallFrame(token, "trace", "a.b", details, id, mask, params)
					if !bytes.Equal(got[4:], want) {
						t.Fatalf("traced wire mismatch: id=%d size=%d", id, size)
					}
					if len(got) != cap(got) {
						t.Fatal("inexact traced capacity reservation")
					}
				}
			}
		}
	}
}

func TestDirectCallEnvelopeOwnsInputs(t *testing.T) {
	params, mask := []byte{3, 4, 0}, []byte{1}
	got := prepareCallFrame(requestKindCall, "", 1, mask, params)
	want := bytes.Clone(got)
	params[0], mask[0] = 9, 9
	if !bytes.Equal(got, want) {
		t.Fatal("request aliases caller buffers")
	}
	if got := prepareCallFrame(requestKindCall, "", 1, nil, nil); !bytes.Equal(got[4:], []byte{1, 0, 0, 1, 0, 0}) {
		t.Fatalf("empty request wire: %x", got)
	}
}

func FuzzDirectCallEnvelope(f *testing.F) {
	f.Add(uint32(128), "token", []byte{1, 2}, []byte{3, 4})
	f.Add(uint32(1), "", []byte{}, []byte{})
	f.Fuzz(func(t *testing.T, id uint32, token string, mask, params []byte) {
		if len(token) > maxBearerTokenSize || len(mask) > 8192 || len(params) > 65536 {
			return
		}
		body := encodeCallRequest(int(id), mask, params)
		want := encodeRequestEnvelope(requestKindCall, token, body)
		got := prepareCallFrame(requestKindCall, token, int(id), mask, params)
		if !bytes.Equal(got[4:], want) || len(got) != cap(got) {
			t.Fatal("plain request changed")
		}
		want = encodeTracedRequestEnvelope(requestKindCall, token, token, token, true, body)
		got = prepareTracedCallFrame(token, token, token, true, int(id), mask, params)
		if !bytes.Equal(got[4:], want) || len(got) != cap(got) {
			t.Fatal("traced request changed")
		}
	})
}
