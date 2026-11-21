package lux

import (
	"math"
	"testing"
)

var rangeResultSink []int64
var rangeErrorSink error

func BenchmarkIntRange(b *testing.B) {
	for _, sample := range []struct {
		name       string
		start, end int64
	}{
		{"empty", 1, 0}, {"single", 42, 42}, {"cross_zero", -64, 63},
		{"items_4096", 0, 4095}, {"maximum", math.MaxInt64, math.MaxInt64},
		{"overflow", math.MinInt64, math.MaxInt64},
	} {
		b.Run(sample.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				rangeResultSink, rangeErrorSink = IntRange(sample.start, sample.end)
			}
		})
	}
}
