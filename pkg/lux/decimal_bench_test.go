package lux

import (
	"testing"

	"github.com/shopspring/decimal"
)

func BenchmarkCompareDecimal(b *testing.B) {
	for _, sample := range []struct {
		name        string
		left, right decimal.Decimal
	}{
		{"sameExponent", decimal.New(123, -2), decimal.New(456, -2)},
		{"nearExponent", decimal.New(123, -2), decimal.New(1230, -3)},
		{"largeGap", decimal.New(1, 100000), decimal.New(1, 0)},
		{"preciseEqual", decimal.RequireFromString("1e30"), decimal.RequireFromString("1000000000000000000000000000000")},
	} {
		for _, version := range []struct {
			name string
			call func(decimal.Decimal, decimal.Decimal) int
		}{{"before", decimal.Decimal.Cmp}, {"after", CompareDecimal}} {
			b.Run(sample.name+"/"+version.name, func(b *testing.B) {
				want := sample.left.Cmp(sample.right)
				var got int
				b.ReportAllocs()
				for b.Loop() {
					got = version.call(sample.left, sample.right)
				}
				if got != want {
					b.Fatal("incorrect comparison", got, want)
				}
			})
		}
	}
}

func BenchmarkFormatDecimal(b *testing.B) {
	for _, sample := range []struct {
		name  string
		value decimal.Decimal
	}{
		{"zero", decimal.Zero}, {"small", decimal.New(123, -2)},
		{"precise", decimal.RequireFromString("12345678901234567890.123456789")},
		{"largeExponent", decimal.New(1, 100000)},
	} {
		for _, version := range []struct {
			name string
			call func(decimal.Decimal) string
		}{{"before", decimal.Decimal.String}, {"after", FormatDecimal}} {
			b.Run(sample.name+"/"+version.name, func(b *testing.B) {
				var got string
				b.ReportAllocs()
				for b.Loop() {
					got = version.call(sample.value)
				}
				b.StopTimer()
				parsed, err := decimal.NewFromString(got)
				if err != nil || CompareDecimal(parsed, sample.value) != 0 {
					b.Fatal("incorrect formatting", err)
				}
			})
		}
	}
}

func BenchmarkDecimalAlignment(b *testing.B) {
	for _, operation := range []struct {
		name          string
		before, after func(decimal.Decimal, decimal.Decimal) (decimal.Decimal, error)
	}{
		{"Add", func(a, z decimal.Decimal) (decimal.Decimal, error) { return a.Add(z), nil }, AddDecimal},
		{"Sub", func(a, z decimal.Decimal) (decimal.Decimal, error) { return a.Sub(z), nil }, SubDecimal},
	} {
		for _, sample := range []struct {
			name        string
			left, right decimal.Decimal
		}{
			{"same", decimal.New(123, -2), decimal.New(456, -2)},
			{"near", decimal.New(123, -2), decimal.New(456, -4)},
			{"zero", decimal.Decimal{}, decimal.New(123, -2)},
		} {
			for _, version := range []struct {
				name string
				call func(decimal.Decimal, decimal.Decimal) (decimal.Decimal, error)
			}{{"before", operation.before}, {"after", operation.after}} {
				b.Run(operation.name+"/"+sample.name+"/"+version.name, func(b *testing.B) {
					var got decimal.Decimal
					var err error
					b.ReportAllocs()
					for b.Loop() {
						got, err = version.call(sample.left, sample.right)
					}
					b.StopTimer()
					want, _ := operation.before(sample.left, sample.right)
					if err != nil || CompareDecimal(got, want) != 0 {
						b.Fatal("invalid arithmetic result", err)
					}
				})
			}
		}
	}
}
