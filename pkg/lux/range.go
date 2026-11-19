package lux

import "errors"

// MaxRangeElements bounds a materialized Int range to 512 KiB of element data.
const MaxRangeElements = 65_536

// ErrRangeTooLarge rejects a range before allocating storage beyond its limit.
var ErrRangeTooLarge = errors.New("lux: integer range is too large (maximum 65536 elements)")

// IntRange materializes the inclusive range without overflowing its size or endpoint.
func IntRange(start, end int64) ([]int64, error) {
	if start > end {
		return nil, nil
	}
	span := uint64(end) - uint64(start)
	// The inclusive count must fit the limit before conversion or allocation.
	if span >= MaxRangeElements {
		return nil, ErrRangeTooLarge
	}
	result := make([]int64, int(span)+1)
	for index := range result {
		result[index] = start + int64(index)
	}
	return result, nil
}
