package luvia

import (
	"bytes"
	"fmt"
	"testing"
)

var mergedDestinationSink []byte

// Both paths use the same current merge implementation and validated bytes.
// This isolates temporary output allocation/copying, not historical correctness.
func BenchmarkMergeDestination(b *testing.B) {
	for _, columnar := range []bool{false, true} {
		for _, rows := range []int{1, 128} {
			if !columnar && rows != 1 {
				continue
			}
			primary, _, _ := primaryKeysBenchmarkFixture(rows, 16, columnar, false)
			blobs := make([][]byte, rows)
			for i := range blobs {
				blobs[i] = []byte{4, 0, 1, 2, 0}
			}
			for _, direct := range []bool{false, true} {
				name := fmt.Sprintf("columnar=%v/rows=%d/direct=%v", columnar, rows, direct)
				b.Run(name, func(b *testing.B) {
					benchmarkMergeDestination(b, primary, blobs, columnar, direct)
				})
			}
		}
	}
}

func benchmarkMergeDestination(b *testing.B, primary []byte, blobs [][]byte, columnar, direct bool) {
	rows := []ExtendResult{{FieldID: 17, Data: blobs[0]}}
	columns := []ExtendColumnResult{{FieldID: 17, Blobs: blobs}}
	want := Merge(primary, rows)
	if columnar {
		want = MergeColumnar(primary, columns)
	}
	dst := make([]byte, 0, len(want))
	merge := func() []byte {
		if columnar {
			if direct {
				return AppendMergedColumnar(dst[:0], primary, columns)
			}
			return append(dst[:0], MergeColumnar(primary, columns)...)
		}
		if direct {
			return AppendMerged(dst[:0], primary, rows)
		}
		return append(dst[:0], Merge(primary, rows)...)
	}
	if !bytes.Equal(merge(), want) {
		b.Fatal("merge changed valid wire")
	}
	b.ReportAllocs()
	for b.Loop() {
		mergedDestinationSink = merge()
	}
}
