package codec

import (
	"fmt"
	"os"
	"testing"
)

var columnIntSkipOffset int
var columnIntSkipError error

func BenchmarkSkipColumnInt(b *testing.B) {
	order := []bool{true, false}
	if os.Getenv("LUXO_KEYS_BENCH_REVERSE") == "1" {
		order = []bool{false, true}
	}
	for _, count := range []int{1, 16, 128} {
		for _, kind := range []string{"small", "wide", "mixed"} {
			data := columnIntSkipFixture(count, kind)
			for _, baseline := range order {
				b.Run(fmt.Sprintf("%s/%d/baseline=%v", kind, count, baseline), func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						r := ColumnarReader{buf: data, count: count}
						if baseline {
							skipColumnIntBaseline(&r)
						} else {
							r.SkipColumnInt()
						}
						columnIntSkipOffset, columnIntSkipError = r.off, r.err
					}
				})
			}
		}
	}
}

// Frozen before the skip optimization. Both benchmark variants use the same
// reader setup and current codec; only the column traversal differs.
func skipColumnIntBaseline(r *ColumnarReader) {
	for i := 0; i < r.count; i++ {
		_, n := ReadSvarint(r.buf, r.off)
		if n <= 0 {
			r.err = fmt.Errorf("codec: truncated int column at record %d", i)
			return
		}
		r.off += n
	}
}
