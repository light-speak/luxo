package rpc

import (
	"fmt"
	"io"
	"testing"
)

// Frozen immediate baseline: one envelope allocation, then two frame writes.
func prepareUnframedReference(params []byte) []byte {
	size := 3 + callBodySize(1, nil, params)
	dst := make([]byte, 0, size)
	dst = appendEnvelopePrefix(dst, requestKindCall, 0, "")
	return appendCallBody(dst, 1, nil, params)
}

func BenchmarkLRPCPreparedFrame(b *testing.B) {
	for _, size := range []int{0, 128, 4096, 65536} {
		params := make([]byte, size)
		b.Run(fmt.Sprintf("%d/before", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := WriteFrame(io.Discard, prepareUnframedReference(params)); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("%d/after", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := writePreparedFrame(io.Discard, prepareCallFrame(requestKindCall, "", 1, nil, params)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Test-only candidate: a leased connection owns bounded request scratch space.
func prepareReusedFrameCandidate(storage, params []byte) []byte {
	size := 3 + callBodySize(1, nil, params)
	if cap(storage) < size+4 {
		storage = make([]byte, 4, size+4)
	} else {
		storage = storage[:4]
		clear(storage)
	}
	storage = appendEnvelopePrefix(storage, requestKindCall, 0, "")
	return appendCallBody(storage, 1, nil, params)
}

func BenchmarkLRPCConnectionFrameReuseCandidate(b *testing.B) {
	for _, size := range []int{0, 128, 4096, 65536} {
		params := make([]byte, size)
		b.Run(fmt.Sprintf("%d/before", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := writePreparedFrame(io.Discard, prepareCallFrame(requestKindCall, "", 1, nil, params)); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("%d/reuse", size), func(b *testing.B) {
			var storage []byte
			b.ReportAllocs()
			for b.Loop() {
				frame := prepareReusedFrameCandidate(storage, params)
				if err := writePreparedFrame(io.Discard, frame); err != nil {
					b.Fatal(err)
				}
				storage = retainReadFrame(frame)
			}
		})
	}
}
