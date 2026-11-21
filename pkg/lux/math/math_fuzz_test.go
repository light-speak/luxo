package math

import (
	gomath "math"
	"testing"
)

func FuzzRandIntBounds(f *testing.F) {
	for _, bounds := range [][2]int64{
		{0, 1}, {1, 0}, {0, 0}, {-1, 1},
		{0, gomath.MaxInt64}, {gomath.MinInt64, -1},
		{gomath.MinInt64, gomath.MaxInt64}, {gomath.MinInt64, 0},
		{-1, gomath.MaxInt64}, {gomath.MinInt64, gomath.MaxInt64 - 1},
		{gomath.MinInt64, gomath.MinInt64 + 1}, {gomath.MaxInt64 - 1, gomath.MaxInt64},
	} {
		f.Add(bounds[0], bounds[1])
	}
	f.Fuzz(func(t *testing.T, min, max int64) {
		got := RandInt(min, max)
		if min >= max {
			if got != min {
				t.Fatalf("RandInt(%d,%d) = %d, want first bound", min, max, got)
			}
		} else if got < min || got > max {
			t.Fatalf("RandInt(%d,%d) = %d outside bounds", min, max, got)
		}
	})
}
