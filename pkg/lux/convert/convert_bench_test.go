package convert

import (
	"math"
	"strconv"
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

// The legacy baseline discards parse errors and is not correctness-equivalent.
func BenchmarkStringIntStrict(b *testing.B) {
	for _, input := range []string{"42", "-9223372036854775808"} {
		b.Run(input, func(b *testing.B) {
			b.Run("legacy", func(b *testing.B) {
				var value int64
				b.ReportAllocs()
				for b.Loop() {
					value, _ = strconv.ParseInt(input, 10, 64)
				}
				if value == 0 {
					b.Fatal(value)
				}
			})
			b.Run("strict", func(b *testing.B) {
				var value int64
				var err error
				b.ReportAllocs()
				for b.Loop() {
					value, err = StringToInt(input)
				}
				if err != nil || value == 0 {
					b.Fatal(value, err)
				}
			})
		})
	}
}

func BenchmarkStringFloatStrict(b *testing.B) {
	for _, input := range []string{"3.14", "5e-324", "1.7976931348623157e308"} {
		b.Run(input, func(b *testing.B) {
			b.Run("legacy", func(b *testing.B) {
				var value float64
				b.ReportAllocs()
				for b.Loop() {
					value, _ = strconv.ParseFloat(input, 64)
				}
				if value == 0 {
					b.Fatal(value)
				}
			})
			b.Run("strict", func(b *testing.B) {
				var value float64
				var err error
				b.ReportAllocs()
				for b.Loop() {
					value, err = StringToFloat(input)
				}
				if err != nil || value == 0 {
					b.Fatal(value, err)
				}
			})
		})
	}
}

// This baseline preserves the previous dispatch while omitting finite-value checks.
func legacyToFloat(v any) (float64, error) {
	switch val := v.(type) {
	case float64:
		return val, nil
	case float32:
		return float64(val), nil
	case int:
		return float64(val), nil
	case int64:
		return float64(val), nil
	case string:
		return strconv.ParseFloat(val, 64)
	case bool:
		if val {
			return 1, nil
		}
		return 0, nil
	default:
		return toFloatReflect(v)
	}
}

func BenchmarkGenericFloatStrict(b *testing.B) {
	for _, sample := range []struct {
		name  string
		value any
		want  float64
	}{
		{"float64", 1.25, 1.25}, {"float32", float32(1.25), 1.25}, {"integer-control", int64(42), 42},
		{"text", "3.14", 3.14}, {"maximum-text", "1.7976931348623157e308", math.MaxFloat64},
	} {
		b.Run(sample.name, func(b *testing.B) {
			for _, candidate := range []struct {
				name string
				read func(any) (float64, error)
			}{{"legacy", legacyToFloat}, {"strict", ToFloat}} {
				b.Run(candidate.name, func(b *testing.B) {
					var value float64
					var err error
					b.ReportAllocs()
					for b.Loop() {
						value, err = candidate.read(sample.value)
					}
					if err != nil || value != sample.want {
						b.Fatal(value, err)
					}
				})
			}
		})
	}
}
