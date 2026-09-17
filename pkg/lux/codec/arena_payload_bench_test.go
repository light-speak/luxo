package codec

import (
	"fmt"
	"testing"
)

// Frozen preflight baselines; neither benchmark path allocates the arena itself.
func (d *Decoder) readArenaBeforePayload() int {
	v, n := ReadVarint(d.buf, d.off)
	if n <= 0 {
		d.err = fmt.Errorf("codec: invalid arena size varint at offset %d", d.off)
		return 0
	}
	d.off += n
	if v > uint64(MaxArenaSize) {
		d.err = fmt.Errorf("codec: arena size %d exceeds limit %d at offset %d", v, MaxArenaSize, d.off-n)
		return 0
	}
	return int(v)
}

func (r *ColumnarReader) readHeaderBeforeArenaPayload() {
	// Read record count
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

	// Read totalStringLen (arena size)
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
	r.arenaSize = int(arenaSize)
}

func newColumnarBeforeArenaPayload(data []byte) *ColumnarReader {
	reader := &ColumnarReader{buf: data}
	reader.readHeaderBeforeArenaPayload()
	return reader
}

func BenchmarkArenaPayloadBoundary(b *testing.B) {
	for _, size := range []int{0, 16, 1026, 65536} {
		row := AppendVarint(nil, uint64(size))
		row = append(row, 1)
		row = append(AppendBytes(row, make([]byte, size)), 0)
		column := append([]byte{1}, row...)
		for _, before := range []bool{true, false} {
			b.Run(fmt.Sprintf("row/size=%d/before=%v", size, before), func(b *testing.B) {
				benchmarkArenaRowHeader(b, row, size, before)
			})
			b.Run(fmt.Sprintf("column/size=%d/before=%v", size, before), func(b *testing.B) {
				benchmarkArenaColumnHeader(b, column, size, before)
			})
		}
	}
}

func benchmarkArenaRowHeader(b *testing.B, data []byte, size int, before bool) {
	var decoder Decoder
	var got int
	b.ReportAllocs()
	for b.Loop() {
		decoder = Decoder{buf: data}
		if before {
			got = decoder.readArenaBeforePayload()
		} else {
			got = decoder.ReadArenaSize()
		}
	}
	if got != size || decoder.Err() != nil {
		b.Fatalf("header failed: %d, %v", got, decoder.Err())
	}
}

func benchmarkArenaColumnHeader(b *testing.B, data []byte, size int, before bool) {
	// Keep readers local, matching generated readers and the production constructor.
	b.ReportAllocs()
	for b.Loop() {
		if before {
			reader := newColumnarBeforeArenaPayload(data)
			if reader.ArenaSize() != size || reader.Count() != 1 || reader.Err() != nil {
				b.Fatal("header failed")
			}
		} else {
			reader := NewColumnarReader(data)
			if reader.ArenaSize() != size || reader.Count() != 1 || reader.Err() != nil {
				b.Fatal("header failed")
			}
		}
	}
}
