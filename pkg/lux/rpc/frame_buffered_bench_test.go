package rpc

import (
	"bufio"
	"bytes"
	"testing"
)

// Reader construction plus one frame, not connection setup or network timing.
func BenchmarkLRPCBufferedColdRead(b *testing.B) {
	wire := []byte{0, 0, 0, 1, 42}
	b.Run("direct", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := ReadFrame(bytes.NewReader(wire), nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("buffered8k", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			reader := bufio.NewReaderSize(bytes.NewReader(wire), 8192)
			if _, err := ReadFrame(reader, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
}
