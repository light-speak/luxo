package arith

import (
	"errors"
	"math"
	"math/big"
	"math/rand/v2"
	"testing"
)

func TestCheckedInt64Boundaries(t *testing.T) {
	for _, test := range []struct {
		name        string
		operation   func(int64, int64) (int64, error)
		left, right int64
		want        int64
		failure     error
	}{
		{"add", Add, 40, 2, 42, nil}, {"add max", Add, math.MaxInt64, 1, 0, ErrOverflow},
		{"add min", Add, math.MinInt64, -1, 0, ErrOverflow}, {"add opposite", Add, math.MinInt64, math.MaxInt64, -1, nil},
		{"subtract", Sub, 40, -2, 42, nil}, {"subtract min", Sub, math.MinInt64, 1, 0, ErrOverflow},
		{"subtract max", Sub, math.MaxInt64, -1, 0, ErrOverflow}, {"subtract same", Sub, math.MinInt64, math.MinInt64, 0, nil},
		{"multiply", Mul, -7, -6, 42, nil}, {"multiply max", Mul, math.MaxInt64, 2, 0, ErrOverflow},
		{"multiply min", Mul, math.MinInt64, -1, 0, ErrOverflow}, {"multiply min safe", Mul, math.MinInt64, 1, math.MinInt64, nil},
		{"multiply zero", Mul, math.MinInt64, 0, 0, nil}, {"multiply high", Mul, math.MaxInt64, math.MaxInt64, 0, ErrOverflow},
		{"divide", Quo, -5, 2, -2, nil}, {"divide zero", Quo, 1, 0, 0, ErrDivisionByZero},
		{"divide min", Quo, math.MinInt64, -1, 0, ErrOverflow}, {"divide min safe", Quo, math.MinInt64, 1, math.MinInt64, nil},
		{"remainder", Rem, -5, 2, -1, nil}, {"remainder zero", Rem, 1, 0, 0, ErrDivisionByZero},
		{"remainder min", Rem, math.MinInt64, -1, 0, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.operation(test.left, test.right)
			if got != test.want || !errors.Is(err, test.failure) {
				t.Fatal(got, err, test.want, test.failure)
			}
		})
	}
	for _, value := range []int64{0, -42, 42, math.MaxInt64, math.MinInt64 + 1, math.MinInt64} {
		got, err := Neg(value)
		if value == math.MinInt64 {
			if got != 0 || !errors.Is(err, ErrOverflow) {
				t.Fatal(got, err)
			}
		} else if err != nil || got != -value {
			t.Fatal(value, got, err)
		}
	}
}

func TestCheckedInt64AgainstExactOracle(t *testing.T) {
	random := rand.New(rand.NewPCG(42, 73))
	values := []int64{0, 1, -1, 2, -2, math.MinInt64, math.MaxInt64, math.MinInt64 + 1, math.MaxInt64 - 1, 1 << 32, -(1 << 32)}
	for range 2048 {
		values = append(values, int64(random.Uint64()))
	}
	for _, pair := range [][2]int64{{math.MinInt64, -1}, {math.MinInt64, math.MinInt64}, {math.MaxInt64, math.MaxInt64}} {
		checkExactOperations(t, pair[0], pair[1])
	}
	for index, left := range values {
		for _, right := range values[:11] {
			checkExactOperations(t, left, right)
		}
		checkExactOperations(t, left, values[(index+17)%len(values)])
	}
}

func checkExactOperations(t *testing.T, left, right int64) {
	t.Helper()
	for _, test := range []struct {
		checked func(int64, int64) (int64, error)
		exact   func(*big.Int, *big.Int) *big.Int
	}{
		{Add, new(big.Int).Add}, {Sub, new(big.Int).Sub}, {Mul, new(big.Int).Mul},
	} {
		expected := test.exact(big.NewInt(left), big.NewInt(right))
		got, err := test.checked(left, right)
		if !expected.IsInt64() {
			if got != 0 || !errors.Is(err, ErrOverflow) {
				t.Fatalf("%d, %d: overflow yielded %d, %v", left, right, got, err)
			}
		} else if err != nil || got != expected.Int64() {
			t.Fatalf("%d, %d: got %d, %v; want %s", left, right, got, err, expected)
		}
	}
}
