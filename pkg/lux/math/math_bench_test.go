package math

import (
	"fmt"
	gomath "math"
	"testing"
)

var randIntSink int64

func BenchmarkRandInt(b *testing.B) {
	for _, sample := range []struct {
		name     string
		min, max int64
	}{
		{"small", 5, 10}, {"crossZero", -100, 100},
		{"nearMax", gomath.MaxInt64 - 100, gomath.MaxInt64}, {"equal", 7, 7},
	} {
		b.Run(sample.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				randIntSink = RandInt(sample.min, sample.max)
			}
		})
	}
}

func BenchmarkRandIntWide(b *testing.B) {
	for _, bounds := range [][2]int64{
		{0, gomath.MaxInt64}, {gomath.MinInt64, gomath.MaxInt64},
	} {
		b.Run(fmt.Sprintf("%d/%d", bounds[0], bounds[1]), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				randIntSink = RandInt(bounds[0], bounds[1])
			}
		})
	}
}
