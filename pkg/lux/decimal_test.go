package lux

import (
	"errors"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func TestDecimalAlignmentBudget(t *testing.T) {
	for _, operation := range []struct {
		name string
		call func(decimal.Decimal, decimal.Decimal) (decimal.Decimal, error)
		ref  func(decimal.Decimal, decimal.Decimal) decimal.Decimal
	}{{"add", AddDecimal, decimal.Decimal.Add}, {"subtract", SubDecimal, decimal.Decimal.Sub}} {
		t.Run(operation.name, func(t *testing.T) {
			for _, gap := range []int32{-4097, -4096, -1, 0, 1, 4096, 4097} {
				left, right := decimal.New(7, gap), decimal.New(3, 0)
				got, err := operation.call(left, right)
				if gap < -4096 || gap > 4096 {
					if !errors.Is(err, ErrDecimalAlignment) || !got.IsZero() {
						t.Fatal("alignment budget not enforced", gap, err)
					}
					continue
				}
				if err != nil || CompareDecimal(got, operation.ref(left, right)) != 0 {
					t.Fatal("bounded exact arithmetic changed", gap, err)
				}
			}
			for _, exponent := range []int32{-2147483648, 2147483647} {
				value := decimal.New(7, exponent)
				got, err := operation.call(value, decimal.Decimal{})
				if err != nil || CompareDecimal(got, value) != 0 {
					t.Fatal("right zero expanded", err)
				}
				got, err = operation.call(decimal.Decimal{}, value)
				want := value
				if operation.name == "subtract" {
					want = value.Neg()
				}
				if err != nil || CompareDecimal(got, want) != 0 {
					t.Fatal("left zero expanded", err)
				}
			}
		})
	}
}

func TestCompareDecimal(t *testing.T) {
	if got := CompareDecimal(decimal.Decimal{}, decimal.New(0, 100000)); got != 0 {
		t.Fatal("uninitialized zero changed", got)
	}
	for _, sample := range []struct {
		left, right string
		want        int
	}{
		{"0", "0e2147483647", 0}, {"0", "1e-2147483648", -1},
		{"-1e2147483647", "0", -1}, {"1e2147483647", "-1e2147483647", 1},
		{"1e2147483647", "1e-2147483648", 1}, {"1e-2147483648", "1e2147483647", -1},
		{"-1e2147483647", "-1e-2147483648", -1},
		{"1e30", "1000000000000000000000000000000", 0},
		{"1e30", "1000000000000000000000000000001", -1},
		{"1000000000000000000000000000001", "1e30", 1},
		{"9e30", "8000000000000000000000000000000", 1},
		{"-1e30", "-1000000000000000000000000000001", 1},
		{"1.23", "1.2300", 0}, {"-1", "-2", 1},
		{"1e80", "1" + strings.Repeat("0", 80), 0},
		{"1e80", "1" + strings.Repeat("0", 79) + "1", -1},
		{"1" + strings.Repeat("0", 79) + "1", "1e80", 1},
		{"9e80", "8" + strings.Repeat("0", 80), 1},
	} {
		left, right := decimal.RequireFromString(sample.left), decimal.RequireFromString(sample.right)
		if got := CompareDecimal(left, right); got != sample.want {
			t.Errorf("%s vs %s: got %d want %d", sample.left, sample.right, got, sample.want)
		}
	}
}

func TestCompareDecimalAgainstBoundedReference(t *testing.T) {
	random := rand.New(rand.NewPCG(17, 42))
	for range 10000 {
		left := decimal.New(int64(random.Uint64()), int32(random.IntN(201)-100))
		right := decimal.New(int64(random.Uint64()), int32(random.IntN(201)-100))
		if got, want := CompareDecimal(left, right), left.Cmp(right); got != want {
			t.Fatalf("bounded reference differs: got %d want %d", got, want)
		}
		if got := CompareDecimal(right, left); got != -CompareDecimal(left, right) {
			t.Fatal("comparison is not antisymmetric")
		}
	}
}

func TestFormatDecimal(t *testing.T) {
	for _, sample := range []struct{ input, want string }{
		{"0", "0"}, {"1.2300", "1.23"}, {"-123.45678901234567890123456789", "-123.45678901234567890123456789"},
		{"1e1024", "1" + strings.Repeat("0", 1024)},
		{"1e-1024", "0." + strings.Repeat("0", 1023) + "1"},
		{"1e1025", "1e1025"}, {"1e-1025", "1e-1025"},
		{"-12e2147483647", "-12e2147483647"}, {"12e-2147483648", "12e-2147483648"},
		{"0e2147483647", "0"}, {"0e-2147483648", "0"},
	} {
		value := decimal.RequireFromString(sample.input)
		got := FormatDecimal(value)
		if got != sample.want {
			t.Fatalf("%s: unexpected formatted value of length %d", sample.input, len(got))
		}
		parsed, err := decimal.NewFromString(got)
		if err != nil || CompareDecimal(value, parsed) != 0 {
			t.Fatal("decimal precision changed", err)
		}
	}
	if FormatDecimal(decimal.Decimal{}) != "0" {
		t.Fatal("default decimal changed")
	}
}
