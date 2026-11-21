package rpc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"testing"
)

// Frozen pre-fix reader; this intentionally reproduces the len/cap defect.
func readFrameBeforeCapacityReuse(r io.Reader, buf []byte) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(hdr[:])
	if size > maxFrameSize {
		return nil, fmt.Errorf("rpc: frame too large: %d bytes (max %d)", size, maxFrameSize)
	}
	if int(size) > len(buf) {
		buf = make([]byte, size)
	} else {
		buf = buf[:size]
	}
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func BenchmarkLRPCFrameCapacityReuse(b *testing.B) {
	for _, size := range []int{128, 4096, 65536, 131072} {
		wire := make([]byte, size+4)
		binary.BigEndian.PutUint32(wire, uint32(size))
		b.Run(fmt.Sprintf("%d/before", size), func(b *testing.B) {
			var reader bytes.Reader
			var buf []byte
			b.ReportAllocs()
			for b.Loop() {
				reader.Reset(wire)
				got, err := readFrameBeforeCapacityReuse(&reader, buf)
				if err != nil {
					b.Fatal(err)
				}
				buf = got[:0]
			}
		})
		b.Run(fmt.Sprintf("%d/after", size), func(b *testing.B) {
			var reader bytes.Reader
			var buf []byte
			b.ReportAllocs()
			for b.Loop() {
				reader.Reset(wire)
				got, err := ReadFrame(&reader, buf)
				if err != nil {
					b.Fatal(err)
				}
				buf = retainReadFrame(got)
			}
		})
	}
}
