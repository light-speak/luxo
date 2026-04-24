package lux

// IntRange generates a slice of int64 from start to end (inclusive).
// Used by generated code for Luxo range expressions: 0..10
func IntRange(start, end int64) []int64 {
	if start > end {
		return nil
	}
	result := make([]int64, 0, end-start+1)
	for i := start; i <= end; i++ {
		result = append(result, i)
	}
	return result
}
