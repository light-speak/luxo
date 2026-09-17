package codec

import (
	"math/bits"
	"testing"
)

func FuzzElementPayloadArithmetic(f *testing.F) {
	for _, width := range []int64{0, 1, 8, 16, -1, 1<<63 - 1} {
		f.Add([]byte{0, 1, 2}, int64(1), int64(2), width)
	}
	f.Fuzz(func(t *testing.T, data []byte, rawOff, rawCount, rawWidth int64) {
		off, count, width := int(rawOff), int(rawCount), int(rawWidth)
		want := false
		if off >= 0 && off <= len(data) && count >= 0 && width > 0 {
			high, low := bits.Mul64(uint64(count), uint64(width))
			want = high == 0 && low <= uint64(len(data)-off)
		}
		if got := HasElementBytes(data, off, count, width); got != want {
			t.Fatalf("bounds differ: size=%d off=%d count=%d width=%d got=%v want=%v", len(data), off, count, width, got, want)
		}
	})
}
