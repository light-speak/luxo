package codec

import (
	"encoding/binary"
	"math"
	"testing"
)

func FuzzArrayMetadataBounds(f *testing.F) {
	for _, count := range []uint64{0, 1, 128, MaxArrayElements, MaxArrayElements + 1, 1 << 32, 1 << 63, math.MaxUint64} {
		f.Add(AppendVarint(nil, count))
	}
	f.Add([]byte{0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		count, n := binary.Uvarint(data)
		valid := n > 0 && count <= MaxArrayElements
		got, consumed := ReadArrayHeader(data, 0)
		if valid {
			if got != int(count) || consumed != n {
				t.Fatalf("valid header changed: %d/%d → %d/%d", count, n, got, consumed)
			}
		} else if got != 0 || consumed != 0 {
			t.Fatalf("invalid unsigned header accepted: %d, %d", got, consumed)
		}
		decoder := NewDecoder(data)
		length := decoder.ReadArrayLength()
		if (decoder.Err() == nil) != valid || length != got {
			t.Fatalf("decoder disagrees with unsigned oracle: %d, %v", length, decoder.Err())
		}
	})
}

func FuzzArenaPayloadBounds(f *testing.F) {
	for _, size := range []uint64{0, 1, 128, MaxArenaSize, MaxArenaSize + 1, math.MaxUint64} {
		f.Add(append(AppendVarint(nil, size), 0))
	}
	f.Add([]byte{0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		size, n := binary.Uvarint(data)
		valid := n > 0 && size <= MaxArenaSize && size <= uint64(len(data)-n)
		decoder := NewDecoder(data)
		got := decoder.ReadArenaSize()
		if (decoder.Err() == nil) != valid || valid && got != int(size) || !valid && got != 0 {
			t.Fatalf("row arena differs from unsigned oracle: %d, %v", got, decoder.Err())
		}
		column := NewColumnarReader(append([]byte{0}, data...))
		if (column.Err() == nil) != valid || valid && column.ArenaSize() != int(size) || !valid && column.ArenaSize() != 0 {
			t.Fatalf("column arena differs from unsigned oracle: %d, %v", column.ArenaSize(), column.Err())
		}
	})
}

func FuzzColumnarProjectionBounds(f *testing.F) {
	for _, data := range [][]byte{{0, 0, 0}, {1, 0, 1, 0, 0}, {1, 0, 0, 0, 0}, {1, 0, 0x80, 0, 0}, {1, 0, 0x81, 0, 0, 0}, {1, 0, 0x80, 0x80, 0x80}, {2, 0, 0x81, 1, 0, 0}} {
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		valid := validColumnarProjectionHeader(data)
		reader := NewColumnarReader(data)
		if (reader.Err() == nil) != valid {
			t.Fatalf("projection differs from unsigned oracle: %x / %v", data, reader.Err())
		}
	})
}

// Independent standard-library oracle; this checks the header, not cell values.
func validColumnarProjectionHeader(data []byte) bool {
	count, n := binary.Uvarint(data)
	if n <= 0 || count > MaxColumnarRecords {
		return false
	}
	data = data[n:]
	arena, n := binary.Uvarint(data)
	if n <= 0 || arena > MaxArenaSize {
		return false
	}
	data = data[n:]
	if arena > uint64(len(data)) {
		return false
	}
	if count == 0 {
		return true
	}
	id, n := binary.Uvarint(data)
	return n > 0 && id != 0 && len(data) > n && count <= uint64(len(data)-n-1)
}
