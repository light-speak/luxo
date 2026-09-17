package codec

import (
	"math"
	"strconv"
	"testing"
)

func TestColumnarProjectionBeforeAllocation(t *testing.T) {
	for _, count := range []uint64{1, 128, MaxColumnarRecords} {
		header := AppendVarint(nil, count)
		header = append(header, 0)
		for _, suffix := range [][]byte{nil, {0}, {1, 0}, {0, 1, 2, 3, 4}, {0x80, 0, 1, 2}, {0x80, 0x80, 0, 1}} {
			data := append(append([]byte(nil), header...), suffix...)
			allocs := testing.AllocsPerRun(10, func() {
				reader := NewColumnarReader(data)
				if reader.Err() == nil {
					t.Fatalf("accepted impossible rows before allocation: %x", data)
				}
			})
			if allocs != 0 {
				t.Fatalf("invalid projection allocated %v times", allocs)
			}
		}
	}
	for _, data := range [][]byte{{0, 0, 0}, {1, 0, 1, 0, 0}, {2, 0, 1, 0, 0, 0}} {
		reader := NewColumnarReader(data)
		if reader.Err() != nil {
			t.Fatalf("valid projection rejected: %x / %v", data, reader.Err())
		}
		if reader.Count() > 0 {
			if !reader.NextColumn() {
				t.Fatal("missing valid column")
			}
			if len(reader.ReadColumnIntPtr()) != reader.Count() {
				t.Fatal("null cells lost")
			}
		}
		if reader.NextColumn() || reader.Err() != nil {
			t.Fatal("invalid end marker")
		}
	}
}

func TestArrayMetadataRejectsUnsignedOverflow(t *testing.T) {
	for _, count := range []uint64{MaxArrayElements + 1, 1 << 31, 1 << 32, 1 << 63, math.MaxUint64} {
		data := AppendVarint(nil, count)
		t.Run(strconv.FormatUint(count, 10), func(t *testing.T) {
			if got, n := ReadArrayHeader(data, 0); got != 0 || n != 0 {
				t.Errorf("invalid array header accepted: count=%d, size=%d", got, n)
			}
			for name, read := range map[string]func(*Decoder){
				"length": func(d *Decoder) { d.ReadArrayLength() },
				"skip":   func(d *Decoder) { d.SkipArray(SkipVarint) },
				"int":    func(d *Decoder) { d.ReadIntArray() },
				"float":  func(d *Decoder) { d.ReadFloatArray() },
				"string": func(d *Decoder) { d.ReadStringArray() },
				"bool":   func(d *Decoder) { d.ReadBoolArray() },
				"bytes":  func(d *Decoder) { d.ReadBytesArray() },
				"uuid":   func(d *Decoder) { d.ReadUUIDArray() },
			} {
				t.Run(name, func(t *testing.T) {
					defer func() {
						if failure := recover(); failure != nil {
							t.Errorf("array metadata caused a panic: %v", failure)
						}
					}()
					decoder := NewDecoder(data)
					read(decoder)
					if decoder.Err() == nil {
						t.Error("array decoder accepted an oversized unsigned count")
					}
				})
			}
		})
	}
}

func TestColumnSkipsRejectMalformedValuesAfterValidHeader(t *testing.T) {
	for _, tt := range []struct {
		name    string
		payload []byte
		skip    func(*ColumnarReader)
	}{
		{"bool", []byte{2, 0}, (*ColumnarReader).SkipColumnBool},
		{"intPtr", []byte{1, 0x80, 0x80}, (*ColumnarReader).SkipColumnIntPtr},
		{"floatPtr", []byte{2, 0}, (*ColumnarReader).SkipColumnFloatPtr},
		{"boolPtr", []byte{2, 0}, (*ColumnarReader).SkipColumnBoolPtr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := append([]byte{1, 0, 1}, tt.payload...)
			reader := NewColumnarReader(data)
			if reader.Err() != nil || !reader.NextColumn() {
				t.Fatal("valid header rejected", reader.Err())
			}
			tt.skip(reader)
			if reader.Err() == nil {
				t.Fatal("malformed value accepted")
			}
			if reader.Offset() > len(data) {
				t.Fatal("cursor escaped payload")
			}
		})
	}
}

func TestArrayHeaderPreservesValidCounts(t *testing.T) {
	for _, count := range []int{0, 1, 127, 128, MaxArrayElements} {
		data := AppendArrayHeader([]byte{42}, count)
		if got, n := ReadArrayHeader(data, 1); got != count || n != len(data)-1 {
			t.Errorf("header %d changed: %d, %d", count, got, n)
		}
		decoder := NewDecoder(data[1:])
		if got := decoder.ReadArrayLength(); got != count || decoder.Err() != nil {
			t.Errorf("valid count changed: %d, %v", got, decoder.Err())
		}
	}
	for _, data := range [][]byte{nil, {0x80}, {0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 2}} {
		if count, n := ReadArrayHeader(data, 0); count != 0 || n != 0 {
			t.Errorf("invalid varint accepted: %x", data)
		}
	}
}

func TestSkipArrayRejectsTruncatedElements(t *testing.T) {
	for _, data := range [][]byte{{1}, {2, 0}, {1, 0x80}} {
		decoder := NewDecoder(data)
		decoder.SkipArray(SkipVarint)
		if decoder.Err() == nil {
			t.Errorf("truncated array accepted: %x", data)
		}
		if decoder.Offset() > len(data) {
			t.Errorf("array cursor escaped payload: %d > %d", decoder.Offset(), len(data))
		}
	}
}

func TestFieldIDsRejectHostIndexOverflow(t *testing.T) {
	maxIndex := uint64(^uint(0) >> 1)
	for _, id := range []uint64{1, 127, maxIndex, maxIndex + 1, math.MaxUint64} {
		valid := id <= maxIndex
		data := AppendVarint(nil, id)
		decoder := NewDecoder(data)
		if got := decoder.NextField(); got != valid || (decoder.Err() == nil) != valid {
			t.Errorf("row accepted invalid ID %d: next=%v, err=%v", id, got, decoder.Err())
		}
		column := NewColumnarReader(append(append([]byte{1, 0}, data...), 0, 0))
		if got := column.NextColumn(); got != valid || (column.Err() == nil) != valid {
			t.Errorf("column accepted invalid ID %d: next=%v, err=%v", id, got, column.Err())
		}
		if valid && (decoder.FieldID() != int(id) || column.FieldID() != int(id)) {
			t.Errorf("field ID changed: %d", id)
		}
	}
	for _, data := range [][]byte{{0x80}, {0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 2}} {
		column := NewColumnarReader(append([]byte{1, 0}, data...))
		if column.NextColumn() || column.Err() == nil {
			t.Errorf("column accepted malformed ID: %x", data)
		}
	}
}

func TestSkippedArenaPreservesBounds(t *testing.T) {
	for _, size := range []uint64{0, 1, MaxArenaSize, MaxArenaSize + 1, 1 << 63, math.MaxUint64} {
		data := append(AppendVarint(nil, size), 0)
		decoder := NewDecoder(data)
		decoder.SkipArenaHeader()
		if valid := size <= 1; (decoder.Err() == nil) != valid {
			t.Errorf("skipped arena %d: %v", size, decoder.Err())
		}
	}
}

func TestArenaSizeCannotExceedRemainingPayload(t *testing.T) {
	for _, size := range []uint64{0, 1, 8, 128, 1 << 20, MaxArenaSize, MaxArenaSize + 1, math.MaxUint64} {
		for _, remaining := range []int{0, 1, 8, 127, 128} {
			body := append(AppendVarint(nil, size), make([]byte, remaining)...)
			valid := size <= MaxArenaSize && size <= uint64(remaining)
			decoder := NewDecoder(body)
			got := decoder.ReadArenaSize()
			if (decoder.Err() == nil) != valid || valid && got != int(size) || !valid && got != 0 {
				t.Errorf("row arena size=%d remaining=%d accepted=%d, %v", size, remaining, got, decoder.Err())
			}
			// Isolate arena bounds from the nonempty projection check.
			column := NewColumnarReader(append([]byte{0}, body...))
			if (column.Err() == nil) != valid || valid && column.ArenaSize() != int(size) || !valid && column.ArenaSize() != 0 {
				t.Errorf("column arena size=%d remaining=%d accepted=%d, %v", size, remaining, column.ArenaSize(), column.Err())
			}
		}
	}
}

func TestColumnRequiresExplicitEndMarker(t *testing.T) {
	for _, data := range [][]byte{{0, 0}, {1, 0, 1, 0}, {1, 0, 1, 2}} {
		for _, terminated := range []bool{false, true} {
			body := append([]byte(nil), data...)
			if terminated {
				body = append(body, 0)
			}
			reader := NewColumnarReader(body)
			for reader.NextColumn() {
				reader.SkipColumnInt()
			}
			if (reader.Err() == nil) != terminated {
				t.Errorf("column end marker not enforced for %x: %v", body, reader.Err())
			}
		}
	}
}

func TestValidMetadataReadDoesNotAllocate(t *testing.T) {
	data := AppendVarint(nil, 128)
	array := append(append([]byte(nil), data...), make([]byte, 128)...)
	allocations := testing.AllocsPerRun(100, func() {
		decoder := Decoder{buf: data}
		if decoder.ReadArrayLength() != 128 || decoder.Err() != nil {
			t.Fatal("valid array length rejected")
		}
		decoder = Decoder{buf: array}
		decoder.SkipArray(SkipVarint)
		if decoder.Err() != nil || decoder.off != len(array) {
			t.Fatal("valid array skip rejected")
		}
		decoder = Decoder{buf: data}
		if !decoder.NextField() || decoder.FieldID() != 128 {
			t.Fatal("valid field ID rejected")
		}
		decoder = Decoder{buf: array}
		decoder.SkipArenaHeader()
		if decoder.Err() != nil {
			t.Fatal("valid arena header rejected")
		}
		column := ColumnarReader{buf: data}
		if !column.NextColumn() || column.FieldID() != 128 {
			t.Fatal("valid column ID rejected")
		}
	})
	if allocations != 0 {
		t.Fatalf("valid metadata reading allocated %v times", allocations)
	}
}
