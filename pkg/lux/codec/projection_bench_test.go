package codec

import (
	"fmt"
	"testing"
)

// Frozen header implementation after arena validation and before projection
// validation. Both sides decode exactly the same payload and allocate no rows.
func (r *ColumnarReader) readHeaderBeforeProjection() {
	count, n := ReadVarint(r.buf, 0)
	if n <= 0 {
		r.err = fmt.Errorf("codec: invalid columnar record count")
		return
	}
	if count > MaxColumnarRecords {
		r.err = fmt.Errorf("codec: columnar count %d exceeds limit %d", count, MaxColumnarRecords)
		return
	}
	r.count = int(count)
	r.off = n
	arenaSize, an := ReadVarint(r.buf, r.off)
	if an <= 0 {
		r.err = fmt.Errorf("codec: invalid columnar arena size")
		return
	}
	r.off += an
	if arenaSize > uint64(MaxArenaSize) {
		r.err = fmt.Errorf("codec: columnar arena size %d exceeds limit %d", arenaSize, MaxArenaSize)
		return
	}
	if arenaSize > uint64(len(r.buf)-r.off) {
		r.err = ErrInsufficientArenaBytes
		return
	}
	r.arenaSize = int(arenaSize)
}

func BenchmarkColumnarProjectionHeader(b *testing.B) {
	for _, count := range []int{0, 1, 128} {
		for _, fieldID := range []uint64{1, 128} {
			if count == 0 && fieldID != 1 {
				continue
			}
			data := append(AppendVarint(nil, uint64(count)), 0)
			if count > 0 {
				data = AppendVarint(data, fieldID)
				data = append(data, make([]byte, count)...)
			}
			data = append(data, 0)
			for _, before := range []bool{true, false} {
				b.Run(fmt.Sprintf("count=%d/id=%d/before=%t", count, fieldID, before), func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						reader := ColumnarReader{buf: data}
						if before {
							reader.readHeaderBeforeProjection()
						} else {
							reader.readHeader()
						}
						if reader.Err() != nil || reader.Count() != count || reader.ArenaSize() != 0 {
							b.Fatal("invalid header")
						}
					}
				})
			}
		}
	}
}
