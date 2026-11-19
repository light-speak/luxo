// Package arith implements checked signed arithmetic shared by analysis and runtime.
package arith

import (
	"errors"
	"math"
	"math/bits"
)

var (
	ErrOverflow       = errors.New("integer arithmetic overflow")
	ErrDivisionByZero = errors.New("integer division by zero")
)

func Add(left, right int64) (int64, error) {
	value := left + right
	if (left^value)&(right^value) < 0 {
		return 0, ErrOverflow
	}
	return value, nil
}

func Sub(left, right int64) (int64, error) {
	value := left - right
	if (left^right)&(left^value) < 0 {
		return 0, ErrOverflow
	}
	return value, nil
}

func Mul(left, right int64) (int64, error) {
	high, low := bits.Mul64(uint64(left), uint64(right))
	// Convert the unsigned high word into a signed two's-complement product.
	high -= uint64(left>>63) & uint64(right)
	high -= uint64(right>>63) & uint64(left)
	value := int64(low)
	if high != uint64(value>>63) {
		return 0, ErrOverflow
	}
	return value, nil
}

func Neg(value int64) (int64, error) {
	if value == math.MinInt64 {
		return 0, ErrOverflow
	}
	return -value, nil
}

func Quo(left, right int64) (int64, error) {
	if right == 0 {
		return 0, ErrDivisionByZero
	}
	if left == math.MinInt64 && right == -1 {
		return 0, ErrOverflow
	}
	return left / right, nil
}

func Rem(left, right int64) (int64, error) {
	if right == 0 {
		return 0, ErrDivisionByZero
	}
	// MinInt64 % -1 is zero: the remainder itself is representable.
	return left % right, nil
}
