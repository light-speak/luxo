package slice

import (
	"sort"
	"strings"
)

// === Luxo Collection standard library (compiled from .luxo expressions) ===
// All functions use generics — zero reflection, type-safe.

// Map transforms each element using fn.
func Map[T any, R any](items []T, fn func(T) R) []R {
	result := make([]R, len(items))
	for i, item := range items {
		result[i] = fn(item)
	}
	return result
}

// Filter returns elements where fn returns true.
func Filter[T any](items []T, fn func(T) bool) []T {
	var result []T
	for _, item := range items {
		if fn(item) {
			result = append(result, item)
		}
	}
	return result
}

// SortBy sorts items by the value returned by fn.
func SortBy[T any](items []T, fn func(T) string) []T {
	result := make([]T, len(items))
	copy(result, items)
	sort.Slice(result, func(i, j int) bool {
		return fn(result[i]) < fn(result[j])
	})
	return result
}

// SortByInt sorts items by an int value returned by fn.
func SortByInt[T any](items []T, fn func(T) int64) []T {
	result := make([]T, len(items))
	copy(result, items)
	sort.Slice(result, func(i, j int) bool {
		return fn(result[i]) < fn(result[j])
	})
	return result
}

// SumOf returns the sum of values returned by fn.
func SumOf[T any](items []T, fn func(T) float64) float64 {
	var sum float64
	for _, item := range items {
		sum += fn(item)
	}
	return sum
}

// SumOfInt returns the sum of int values returned by fn.
func SumOfInt[T any](items []T, fn func(T) int64) int64 {
	var sum int64
	for _, item := range items {
		sum += fn(item)
	}
	return sum
}

// Any returns true if fn returns true for any element.
func Any[T any](items []T, fn func(T) bool) bool {
	for _, item := range items {
		if fn(item) {
			return true
		}
	}
	return false
}

// All returns true if fn returns true for all elements.
func All[T any](items []T, fn func(T) bool) bool {
	for _, item := range items {
		if !fn(item) {
			return false
		}
	}
	return true
}

// None returns true if fn returns false for all elements.
func None[T any](items []T, fn func(T) bool) bool {
	return !Any(items, fn)
}

// Find returns the first element where fn returns true, or zero value.
func Find[T any](items []T, fn func(T) bool) (T, bool) {
	for _, item := range items {
		if fn(item) {
			return item, true
		}
	}
	var zero T
	return zero, false
}

// JoinToString joins elements converted to string by fn, separated by sep.
func JoinToString[T any](items []T, fn func(T) string, sep string) string {
	strs := make([]string, len(items))
	for i, item := range items {
		strs[i] = fn(item)
	}
	return strings.Join(strs, sep)
}

// GroupBy groups elements by the key returned by fn.
func GroupBy[T any, K comparable](items []T, fn func(T) K) map[K][]T {
	result := make(map[K][]T)
	for _, item := range items {
		key := fn(item)
		result[key] = append(result[key], item)
	}
	return result
}

// Unique returns deduplicated elements (preserving order).
func Unique[T comparable](items []T) []T {
	seen := make(map[T]bool, len(items))
	var result []T
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}

// Flatten flattens a slice of slices.
func Flatten[T any](items [][]T) []T {
	var result []T
	for _, sub := range items {
		result = append(result, sub...)
	}
	return result
}

// Count returns the number of elements where fn returns true.
func Count[T any](items []T, fn func(T) bool) int {
	n := 0
	for _, item := range items {
		if fn(item) {
			n++
		}
	}
	return n
}

// First returns the first element or zero value.
func First[T any](items []T) (T, bool) {
	if len(items) == 0 {
		var zero T
		return zero, false
	}
	return items[0], true
}

// Last returns the last element or zero value.
func Last[T any](items []T) (T, bool) {
	if len(items) == 0 {
		var zero T
		return zero, false
	}
	return items[len(items)-1], true
}
