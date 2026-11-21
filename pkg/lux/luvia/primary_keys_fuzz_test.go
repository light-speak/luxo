package luvia

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func FuzzPrimaryKeysCompleteParent(f *testing.F) {
	for _, columnar := range []bool{false, true} {
		for _, keyLast := range []bool{false, true} {
			valid := primaryKeysTestMessage(columnar, keyLast)
			f.Add(valid, columnar)
			f.Add(valid[:len(valid)-1], columnar)
			f.Add(append(bytes.Clone(valid), 0), columnar)
		}
	}
	registry, model := primaryKeysTestSchema()
	f.Fuzz(func(t *testing.T, data []byte, columnar bool) {
		if len(data) > 4096 {
			return
		}
		want, valid := primaryKeysReference(data, columnar)
		got, ok := primaryKeysTestExtract(data, columnar, model, registry)
		if ok != valid || ok && (got.count != want.count || !bytes.Equal(got.values, want.values)) {
			t.Fatalf("key extraction disagrees with independent framing reader: ok=%v want=%v data=%x", ok, valid, data)
		}
		if columnar {
			ids := ExtractIDColumn(data, 1, model)
			if (ids != nil) != valid || valid && len(ids) != want.count {
				t.Fatalf("Int column extraction disagrees with framing oracle: %x", data)
			}
			offset := 0
			for _, id := range ids {
				expected, size := binary.Varint(want.values[offset:])
				if size <= 0 || id != expected {
					t.Fatal("Int column value changed")
				}
				offset += size
			}
		} else {
			id, ok := ExtractID(data, 1, map[int]codec.FieldSkipType{2: codec.SkipVarint})
			expected, _ := binary.Varint(want.values)
			if ok != valid || valid && id != expected {
				t.Fatalf("Int row extraction disagrees with framing oracle: %x", data)
			}
		}
	})
}

// The oracle uses only encoding/binary and a fixed two-Int schema. It does not
// call the production decoder, skip helpers or key extraction implementation.
func primaryKeysReference(data []byte, columnar bool) (BinaryKeys, bool) {
	offset, count := 0, uint64(1)
	if columnar {
		value, size := binary.Uvarint(data)
		if size <= 0 || value == 0 || value > codec.MaxColumnarRecords {
			return BinaryKeys{}, false
		}
		offset, count = size, value
	}
	arena, size := binary.Uvarint(data[offset:])
	if size <= 0 || arena > codec.MaxArenaSize {
		return BinaryKeys{}, false
	}
	offset += size
	if arena > uint64(len(data)-offset) {
		return BinaryKeys{}, false
	}
	var keys BinaryKeys
	for offset < len(data) {
		id, size := binary.Uvarint(data[offset:])
		if size <= 0 {
			return BinaryKeys{}, false
		}
		offset += size
		if id == 0 {
			return keys, keys.count != 0 && offset == len(data)
		}
		if id > 2 || id == 1 && keys.count != 0 {
			return BinaryKeys{}, false
		}
		start := offset
		for range count {
			_, size = binary.Uvarint(data[offset:])
			if size <= 0 {
				return BinaryKeys{}, false
			}
			offset += size
		}
		if id == 1 {
			keys = BinaryKeys{count: int(count), values: data[start:offset]}
		}
	}
	return BinaryKeys{}, false
}
