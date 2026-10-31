package convert

import (
	"math"
	"testing"
)

func BenchmarkFloatToIntBoundary(b *testing.B) {
	for _, sample := range []struct {
		name  string
		input any
		want  int64
	}{
		{"ordinary64", float64(1234.5), 1235},
		{"ordinary32", float32(1234.5), 1235},
		{"maximum", math.Nextafter(0x1p63, 0), math.MaxInt64 - 1023},
		{"minimum", float64(-0x1p63), math.MinInt64},
	} {
		b.Run(sample.name, func(b *testing.B) {
			var value int64
			var err error
			b.ReportAllocs()
			for b.Loop() {
				value, err = ToInt(sample.input)
			}
			if err != nil || value != sample.want {
				b.Fatalf("value=%d err=%v want=%d", value, err, sample.want)
			}
		})
	}
}
