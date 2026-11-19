package lux

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestCheckedIntegerPublicBoundary(t *testing.T) {
	for _, test := range []struct {
		call        func(int64, int64) (int64, error)
		left, right int64
		want        int64
		failure     error
	}{
		{AddInt, 40, 2, 42, nil}, {AddInt, math.MaxInt64, 1, 0, ErrIntegerOverflow},
		{SubInt, 40, -2, 42, nil}, {SubInt, math.MinInt64, 1, 0, ErrIntegerOverflow},
		{MulInt, -7, -6, 42, nil}, {MulInt, math.MaxInt64, 2, 0, ErrIntegerOverflow},
		{DivInt, -5, 2, -2, nil}, {DivInt, 1, 0, 0, ErrDivisionByZero},
		{ModInt, -5, 2, -1, nil}, {ModInt, 1, 0, 0, ErrDivisionByZero},
	} {
		got, err := test.call(test.left, test.right)
		if got != test.want || !errors.Is(err, test.failure) {
			t.Fatal(got, err, test)
		}
	}
	if got, err := NegInt(42); err != nil || got != -42 {
		t.Fatal(got, err)
	}
	if got, err := NegInt(math.MinInt64); got != 0 || !errors.Is(err, ErrIntegerOverflow) {
		t.Fatal(got, err)
	}
}

func TestCheckedDurationBoundary(t *testing.T) {
	for _, test := range []struct {
		call        func(time.Duration, time.Duration) (time.Duration, error)
		left, right time.Duration
		want        time.Duration
		failure     error
	}{
		{AddDuration, 40, 2, 42, nil}, {AddDuration, time.Duration(math.MaxInt64), 1, 0, ErrIntegerOverflow},
		{SubDuration, 40, -2, 42, nil}, {SubDuration, time.Duration(math.MinInt64), 1, 0, ErrIntegerOverflow},
	} {
		got, err := test.call(test.left, test.right)
		if got != test.want || !errors.Is(err, test.failure) {
			t.Fatal(got, err)
		}
	}
	if got, err := NegDuration(42); err != nil || got != -42 {
		t.Fatal(got, err)
	}
	if got, err := NegDuration(time.Duration(math.MinInt64)); got != 0 || !errors.Is(err, ErrIntegerOverflow) {
		t.Fatal(got, err)
	}
	if got, err := DurationFromUnits(2, time.Second); err != nil || got != 2*time.Second {
		t.Fatal(got, err)
	}
	if got, err := DurationFromUnits(math.MaxInt64, time.Second); got != 0 || !errors.Is(err, ErrIntegerOverflow) {
		t.Fatal(got, err)
	}
}

func TestSubtractDateTimeMinimumDuration(t *testing.T) {
	start := time.Unix(0, 0).UTC()
	for _, test := range []struct {
		amount time.Duration
		want   time.Time
	}{
		{0, start}, {8, time.Unix(-1, 999999992)}, {-8, time.Unix(0, 8)},
		{time.Duration(math.MinInt64), time.Unix(9223372036, 854775808)},
	} {
		if got := SubDateTime(start, test.amount); !got.Equal(test.want) {
			t.Fatal(test.amount, got, test.want)
		}
	}
}
