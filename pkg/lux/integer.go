package lux

import (
	"math"
	"time"

	"github.com/light-speak/luxo/internal/arith"
)

var (
	ErrIntegerOverflow = arith.ErrOverflow
	ErrDivisionByZero  = arith.ErrDivisionByZero
)

func AddInt(left, right int64) (int64, error) { return arith.Add(left, right) }
func SubInt(left, right int64) (int64, error) { return arith.Sub(left, right) }
func MulInt(left, right int64) (int64, error) { return arith.Mul(left, right) }
func DivInt(left, right int64) (int64, error) { return arith.Quo(left, right) }
func ModInt(left, right int64) (int64, error) { return arith.Rem(left, right) }
func NegInt(value int64) (int64, error)       { return arith.Neg(value) }

func AddDuration(left, right time.Duration) (time.Duration, error) {
	value, err := arith.Add(int64(left), int64(right))
	return time.Duration(value), err
}

func SubDuration(left, right time.Duration) (time.Duration, error) {
	value, err := arith.Sub(int64(left), int64(right))
	return time.Duration(value), err
}

func NegDuration(value time.Duration) (time.Duration, error) {
	result, err := arith.Neg(int64(value))
	return time.Duration(result), err
}

func DurationFromUnits(value int64, unit time.Duration) (time.Duration, error) {
	result, err := arith.Mul(value, int64(unit))
	return time.Duration(result), err
}

// SubDateTime subtracts even MinInt64 nanoseconds without overflowing its negation.
func SubDateTime(value time.Time, amount time.Duration) time.Time {
	if amount == time.Duration(math.MinInt64) {
		return value.Add(time.Duration(math.MaxInt64)).Add(time.Nanosecond)
	}
	return value.Add(-amount)
}
