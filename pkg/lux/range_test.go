package lux

import (
	"errors"
	"math"
	"slices"
	"testing"
)

func TestIntRangeOverflowDoesNotPanic(t *testing.T) {
	defer func() {
		if failure := recover(); failure != nil {
			t.Fatalf("range overflow panicked: %v", failure)
		}
	}()
	values, err := IntRange(0, math.MaxInt64)
	if values != nil || !errors.Is(err, ErrRangeTooLarge) {
		t.Fatal(values, err)
	}
}

func TestIntRangeEndpointBounds(t *testing.T) {
	for _, sample := range []struct {
		start, end int64
		want       []int64
	}{
		{math.MaxInt64, math.MaxInt64, []int64{math.MaxInt64}},
		{math.MaxInt64 - 1, math.MaxInt64, []int64{math.MaxInt64 - 1, math.MaxInt64}},
		{math.MinInt64, math.MinInt64 + 1, []int64{math.MinInt64, math.MinInt64 + 1}},
		{-1, 1, []int64{-1, 0, 1}},
		{1, -1, nil},
	} {
		values, err := IntRange(sample.start, sample.end)
		if err != nil || !slices.Equal(values, sample.want) {
			t.Fatal(sample, values, err)
		}
	}
	for _, bounds := range [][2]int64{{math.MinInt64, math.MaxInt64}, {math.MinInt64, 0}, {-1, math.MaxInt64}} {
		values, err := IntRange(bounds[0], bounds[1])
		if values != nil || !errors.Is(err, ErrRangeTooLarge) {
			t.Fatal(bounds, values, err)
		}
	}
}

func TestIntRangeMaterializationLimit(t *testing.T) {
	const limit = 65_536
	for _, start := range []int64{math.MinInt64, -limit, -1, 0, math.MaxInt64 - limit} {
		values, err := IntRange(start, start+limit-1)
		if err != nil || len(values) != limit || values[0] != start || values[limit-1] != start+limit-1 {
			t.Fatal("maximum legal range rejected", start, len(values), err)
		}
		values, err = IntRange(start, start+limit)
		if values != nil || !errors.Is(err, ErrRangeTooLarge) {
			t.Fatal("oversized range accepted", start, len(values), err)
		}
	}
	if allocations := testing.AllocsPerRun(5, func() {
		_, _ = IntRange(0, limit)
	}); allocations != 0 {
		t.Fatal("oversized range allocated", allocations)
	}
}
