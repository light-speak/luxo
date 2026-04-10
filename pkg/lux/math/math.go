// Package math provides numeric utility functions for the Luxo runtime.
// Only functions that add value over Go's stdlib or built-in min/max.
package math

import (
	gomath "math"
	"math/rand/v2"
)

// Clamp constrains val to [min, max].
func Clamp(val, min, max float64) float64 {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

// ClampInt constrains val to [min, max] for int64.
func ClampInt(val, min, max int64) int64 {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

// AbsInt returns the absolute value of x.
// For math.MinInt64 returns math.MaxInt64 (avoids overflow).
func AbsInt(x int64) int64 {
	if x == gomath.MinInt64 {
		return gomath.MaxInt64
	}
	if x < 0 {
		return -x
	}
	return x
}

// Random returns a pseudo-random float64 in [0, 1).
func Random() float64 {
	return rand.Float64()
}

// RandInt returns a pseudo-random int64 in [min, max].
func RandInt(min, max int64) int64 {
	if min >= max {
		return min
	}
	return min + rand.Int64N(max-min+1)
}
