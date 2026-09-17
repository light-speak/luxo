package codec

import (
	"bytes"
	"fmt"
	"testing"
)

// Frozen finalizer before the error-return migration. Valid inputs only.
func (w *ColumnarWriter) bytesBeforeValidation() []byte {
	w.buf = w.buf[:0]
	w.buf = AppendVarint(w.buf, uint64(w.count))
	w.buf = AppendVarint(w.buf, uint64(w.totalStringLen))
	if w.count > 0 {
		for _, col := range w.columns {
			w.buf = AppendVarint(w.buf, uint64(col.fieldID))
			w.buf = append(w.buf, col.data...)
		}
	}
	w.buf = append(w.buf, 0)
	return w.buf
}

func BenchmarkColumnarBytesValidation(b *testing.B) {
	for _, rows := range []int{0, 1, 128} {
		for _, cols := range []int{1, 16} {
			if rows == 0 && cols != 1 {
				continue
			}
			for _, before := range []bool{true, false} {
				b.Run(fmt.Sprintf("rows=%d/cols=%d/before=%t", rows, cols, before), func(b *testing.B) {
					w := ColumnarWriter{}
					w.SetCount(rows)
					for id := 1; id <= cols; id++ {
						w.WriteColumnInt(id, make([]int64, rows))
					}
					want := bytes.Clone(w.bytesBeforeValidation())
					data, err := w.Bytes()
					if err != nil || !bytes.Equal(data, want) {
						b.Fatal("valid wire changed", err)
					}
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						if before {
							if len(w.bytesBeforeValidation()) != len(want) {
								b.Fatal("invalid baseline")
							}
						} else {
							data, err := w.Bytes()
							if err != nil || len(data) != len(want) {
								b.Fatal("invalid finalizer", err)
							}
						}
					}
				})
			}
		}
	}
}

// Isolate metadata registration from unchanged value encoding and column allocation.
func BenchmarkColumnarRegisterValidation(b *testing.B) {
	for _, cols := range []int{1, 16} {
		for _, before := range []bool{true, false} {
			b.Run(fmt.Sprintf("cols=%d/before=%t", cols, before), func(b *testing.B) {
				w := ColumnarWriter{columns: make([]column, 0, cols), count: 1}
				data := []byte{2}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					w.columns = w.columns[:0]
					for id := 1; id <= cols; id++ {
						if before {
							w.columns = append(w.columns, column{fieldID: id, data: data})
						} else {
							w.appendColumn(id, 1, data)
						}
					}
					if len(w.columns) != cols || w.columnRows < 0 {
						b.Fatal("invalid registration")
					}
				}
			})
		}
	}
}
