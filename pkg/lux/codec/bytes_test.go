package codec

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

func TestReadBytesBoundaries(t *testing.T) {
	for _, size := range []int{0, 1, 16, 127, 128, 255, 16383, 16384} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			payload := bytes.Repeat([]byte{'x'}, size)
			wire := AppendBytes([]byte{0xff}, payload)
			end := len(wire)
			wire = append(wire, 0xfe)
			got, n := ReadBytes(wire, 1)
			if !bytes.Equal(got, payload) || n != end-1 {
				t.Fatalf("payload/consumed mismatch: len=%d n=%d", len(got), n)
			}
			if size > 0 {
				got[0] = 'y'
				if wire[end-size] != 'y' {
					t.Fatal("reader must return a view, not a copy")
				}
			}
			got, n = ReadBytes(wire[:end-1], 1)
			if got != nil || n != 0 {
				t.Fatal("truncated payload must consume nothing")
			}
		})
	}
}

func TestReadBytesInvalidOffsets(t *testing.T) {
	for _, off := range []int{-1, math.MinInt, 2, 3, math.MaxInt} {
		t.Run(fmt.Sprint(off), func(t *testing.T) {
			got, n := ReadBytes([]byte{1, 'x'}, off)
			if got != nil || n != 0 {
				t.Fatal("invalid offset must consume nothing")
			}
		})
	}
}

func TestReadBytesLongPrefix(t *testing.T) {
	for _, wire := range [][]byte{
		{0x80}, {0x80, 1},
		binary.AppendUvarint(nil, math.MaxUint64),
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 2},
	} {
		if got, n := ReadBytes(wire, 0); got != nil || n != 0 {
			t.Fatalf("invalid prefix consumed: %x", wire)
		}
	}
	// Non-minimal varints remain valid; this optimization must not change wire rules.
	if got, n := ReadBytes([]byte{0x81, 0, 'x'}, 0); string(got) != "x" || n != 3 {
		t.Fatal("non-minimal length prefix changed")
	}
}

func FuzzReadBytesBounds(f *testing.F) {
	for _, wire := range [][]byte{{}, {0}, {1, 'x'}, {0x81, 0, 'x'}, {0x80}, {0xff, 0xff}} {
		for _, off := range []int{-1, 0, 1, math.MaxInt} {
			f.Add(wire, off)
		}
	}
	f.Fuzz(func(t *testing.T, wire []byte, off int) {
		got, n := ReadBytes(wire, off)
		if uint(off) >= uint(len(wire)) {
			if got != nil || n != 0 {
				t.Fatal("invalid offset consumed")
			}
			return
		}
		length, prefix := binary.Uvarint(wire[off:])
		if prefix <= 0 || length > uint64(len(wire)-off-prefix) {
			if got != nil || n != 0 {
				t.Fatal("invalid payload consumed")
			}
			return
		}
		want := wire[off+prefix : off+prefix+int(length)]
		if !bytes.Equal(got, want) || n != prefix+len(want) {
			t.Fatal("valid payload changed")
		}
	})
}

func BenchmarkReadBytesPrefix(b *testing.B) {
	for _, size := range []int{0, 1, 16, 127, 128, 1024, 16384, 73728} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			wire := AppendBytes([]byte{0xff}, make([]byte, size))
			b.ReportAllocs()
			for b.Loop() {
				got, n := ReadBytes(wire, 1)
				if len(got) != size || n != len(wire)-1 {
					b.Fatal("invalid benchmark payload")
				}
			}
		})
	}
}
