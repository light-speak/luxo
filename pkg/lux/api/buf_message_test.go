package api

import (
	"bytes"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func TestResponseBufMessageFraming(t *testing.T) {
	for _, size := range []int{0, 1, 127, 128, 255, 16383, 16384, 65536} {
		for _, capacity := range []int{0, size + 32} {
			buf := ResponseBuf{B: make([]byte, 0, capacity), Identity: "test"}
			buf.B = append(buf.B, 42)
			mark := buf.BeginMessage()
			payload := bytes.Repeat([]byte{0xa5}, size)
			buf.B = append(buf.B, payload...)
			buf.EndMessage(mark)
			want := codec.AppendBytes([]byte{42}, payload)
			if !bytes.Equal(buf.B, want) || buf.Identity != "test" {
				t.Fatalf("size=%d capacity=%d: framing or identity changed", size, capacity)
			}
		}
	}
}

func TestResponseBufNestedMessageFraming(t *testing.T) {
	var buf ResponseBuf
	outer := buf.BeginMessage()
	buf.B = append(buf.B, 42)
	inner := buf.BeginMessage()
	payload := bytes.Repeat([]byte{7}, 128)
	buf.B = append(buf.B, payload...)
	buf.EndMessage(inner)
	buf.B = append(buf.B, 43)
	buf.EndMessage(outer)
	child := append(codec.AppendBytes([]byte{42}, payload), 43)
	if !bytes.Equal(buf.B, codec.AppendBytes(nil, child)) {
		t.Fatalf("nested frame = %x", buf.B)
	}
}

func TestResponseBufMessageNoAllocation(t *testing.T) {
	buf := ResponseBuf{B: make([]byte, 0, 1024)}
	payload := make([]byte, 256)
	if allocations := testing.AllocsPerRun(100, func() {
		buf.B = buf.B[:0]
		mark := buf.BeginMessage()
		buf.B = append(buf.B, payload...)
		buf.EndMessage(mark)
	}); allocations != 0 {
		t.Fatalf("preallocated framing: %g allocations", allocations)
	}
}

func TestResponseBufArenaHeader(t *testing.T) {
	for _, size := range []int{0, 1, 127, 128, 16383, 16384, 1 << 20} {
		buf := ResponseBuf{B: make([]byte, 0, 32)}
		outer := buf.BeginMessage()
		mark := buf.BeginArenaHeader()
		buf.B = append(buf.B, 1, 84, 0)
		buf.EndArenaHeader(mark, size)
		buf.EndMessage(outer)
		payload := codec.AppendVarint(nil, uint64(size))
		payload = append(payload, 1, 84, 0)
		if !bytes.Equal(buf.B, codec.AppendBytes(nil, payload)) {
			t.Fatalf("arena=%d frame=%x", size, buf.B)
		}
	}
}
