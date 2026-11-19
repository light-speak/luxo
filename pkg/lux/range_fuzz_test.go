package lux

import (
	"math"
	"testing"
)

func FuzzIntRangeEndpoints(f *testing.F) {
	for _, start := range []int64{math.MinInt64, -256, -1, 0, 1, math.MaxInt64 - 256, math.MaxInt64} {
		for _, span := range []uint8{0, 1, 255} {
			f.Add(start, span)
		}
	}
	f.Fuzz(func(t *testing.T, start int64, span uint8) {
		// Keep fuzz allocations bounded while reaching both signed endpoints.
		if start > math.MaxInt64-int64(span) {
			start = math.MaxInt64 - int64(span)
		}
		end := start + int64(span)
		values, err := IntRange(start, end)
		if err != nil || len(values) != int(span)+1 {
			t.Fatal("invalid materialized length", start, end, len(values), err)
		}
		for index, value := range values {
			if value != start+int64(index) {
				t.Fatal("range endpoint wrapped", start, end, index, value)
			}
		}
		if span != 0 {
			values, err = IntRange(end, start)
			if err != nil || values != nil {
				t.Fatal("reversed range is not empty", values, err)
			}
		}
	})
}
