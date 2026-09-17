package codec

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

func TestSkipColumnIntExactBoundaries(t *testing.T) {
	for _, count := range []int{0, 1, 7, 8, 9, 16, 128} {
		for _, kind := range []string{"small", "wide", "mixed"} {
			data := columnIntSkipFixture(count, kind)
			t.Run(fmt.Sprintf("%s/%d", kind, count), func(t *testing.T) {
				checkColumnIntSkip(t, append(bytes.Clone(data), 0xff, 0xff), count)
				for end := range len(data) {
					checkColumnIntSkip(t, data[:end], count)
				}
				allocs := testing.AllocsPerRun(100, func() {
					r := ColumnarReader{buf: data, count: count}
					r.SkipColumnInt()
					if r.Err() != nil || r.Offset() != len(data) {
						t.Fatal("valid column was not consumed exactly")
					}
				})
				if allocs != 0 {
					t.Fatalf("successful skip allocated %v times", allocs)
				}
			})
		}
	}
}

func TestSkipColumnIntOverflowAndOverlong(t *testing.T) {
	for prefix := range 17 {
		for terminal := range 256 {
			data := bytes.Repeat([]byte{42}, prefix)
			data = append(data, bytes.Repeat([]byte{0x80}, 9)...)
			data = append(data, byte(terminal))
			checkColumnIntSkip(t, data, prefix+1)
		}
	}
}

func TestSkipColumnIntWidthTransitions(t *testing.T) {
	for shift := uint(1); shift < 64; shift++ {
		boundary := uint64(1) << shift
		for _, value := range []uint64{boundary - 1, boundary, boundary + 1} {
			data := AppendVarint([]byte{0, 1, 0x7f}, value)
			checkColumnIntSkip(t, append(data, 42), 5)
			for end := 3; end < len(data); end++ {
				checkColumnIntSkip(t, data[:end], 4)
			}
		}
	}
}

func checkColumnIntSkip(t *testing.T, data []byte, count int) {
	t.Helper()
	want, index := columnIntSkipReference(data, count)
	// A nonzero initial offset verifies that the skip does not assume the
	// column starts at the beginning of the containing response buffer.
	padded := append([]byte{0xff, 0xff}, data...)
	r := ColumnarReader{buf: padded, count: count, off: 2}
	r.SkipColumnInt()
	if r.Offset() != want+2 || (r.Err() == nil) != (index == count) {
		t.Fatalf("skip offset=%d err=%v; want offset=%d valid=%v data=%x count=%d", r.Offset(), r.Err(), want+2, index == count, data, count)
	}
	if index != count && r.Err().Error() != fmt.Sprintf("codec: truncated int column at record %d", index) {
		t.Fatalf("wrong failing record: %v", r.Err())
	}
}

func columnIntSkipReference(data []byte, count int) (int, int) {
	off := 0
	for index := range count {
		_, size := binary.Uvarint(data[off:])
		if size <= 0 {
			return off, index
		}
		off += size
	}
	return off, count
}

func columnIntSkipFixture(count int, kind string) []byte {
	var data []byte
	for index := range count {
		value := int64(42)
		if kind == "wide" || kind == "mixed" && index%3 == 0 {
			value = math.MaxInt64
			if index%2 == 0 {
				value = math.MinInt64
			}
		}
		data = AppendSvarint(data, value)
	}
	return data
}

func FuzzSkipColumnIntExactBoundaries(f *testing.F) {
	for _, kind := range []string{"small", "wide", "mixed"} {
		f.Add(columnIntSkipFixture(16, kind), uint8(16))
	}
	f.Add(bytes.Repeat([]byte{0xff}, 10), uint8(1))
	f.Fuzz(func(t *testing.T, data []byte, count uint8) {
		if len(data) > 4096 {
			return
		}
		checkColumnIntSkip(t, data, int(count))
	})
}
