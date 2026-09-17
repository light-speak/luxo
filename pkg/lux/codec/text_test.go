package codec

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func invalidTextVectors() [][]byte {
	return [][]byte{{0x80}, {0xff}, {0xc0, 0x80}, {0xc1, 0xbf}, {0xc2}, {0xc2, 0x20},
		{0xe0, 0x80, 0x80}, {0xe2, 0x82}, {0xed, 0xa0, 0x80}, {0xed, 0xbf, 0xbf},
		{0xf0, 0x80, 0x80, 0x80}, {0xf0, 0x9f, 0x92}, {0xf4, 0x90, 0x80, 0x80}, {0xf5, 0x80, 0x80, 0x80}}
}

type textReaderCase struct {
	name string
	read func(*Decoder, *ColumnarReader, []byte, *int) string
}

func textReaderCases() []textReaderCase {
	return []textReaderCase{
		{"row", func(d *Decoder, _ *ColumnarReader, _ []byte, _ *int) string { return d.ReadString() }},
		{"row-nullable", func(d *Decoder, _ *ColumnarReader, _ []byte, _ *int) string {
			return textValue(d.ReadStringPtr())
		}},
		{"array", func(d *Decoder, _ *ColumnarReader, _ []byte, _ *int) string {
			return firstTextValue(d.ReadStringArray())
		}},
		{"row-arena", func(d *Decoder, _ *ColumnarReader, a []byte, off *int) string { return d.ReadStringArena(a, off) }},
		{"row-nullable-arena", func(d *Decoder, _ *ColumnarReader, a []byte, off *int) string {
			return textValue(d.ReadStringArenaPtr(a, off))
		}},
		{"column", func(_ *Decoder, r *ColumnarReader, _ []byte, _ *int) string {
			return firstTextValue(r.ReadColumnString())
		}},
		{"column-nullable", func(_ *Decoder, r *ColumnarReader, _ []byte, _ *int) string {
			return textValue(firstTextValue(r.ReadColumnStringPtr()))
		}},
		{"column-arena", func(_ *Decoder, r *ColumnarReader, a []byte, off *int) string {
			return firstTextValue(r.ReadColumnStringArena(a, off))
		}},
		{"column-nullable-arena", func(_ *Decoder, r *ColumnarReader, a []byte, off *int) string {
			return textValue(firstTextValue(r.ReadColumnStringPtrArena(a, off)))
		}},
	}
}

func textValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func firstTextValue[T string | *string](values []T) (value T) {
	if len(values) == 1 {
		return values[0]
	}
	return value
}

func TestStringReadersRejectInvalidUTF8(t *testing.T) {
	for _, raw := range invalidTextVectors() {
		data := AppendBytes([]byte{42}, raw)
		if value, n := ReadString(data, 1); n != 0 || value != "" {
			t.Errorf("wire accepted invalid text %x: %x / %d", raw, value, n)
		}
		for _, tc := range textReaderCases() {
			t.Run(fmt.Sprintf("%s/%x", tc.name, raw), func(t *testing.T) { checkTextReader(t, tc, raw, false) })
		}
	}
}

func TestStringReadersPreserveValidUTF8(t *testing.T) {
	for _, value := range []string{"", "hello", "\x00\n", "\ufeffstart", "\ufffd", "e\u0301", "東京 café", "\U0001f642", "\U0010ffff"} {
		for _, tc := range textReaderCases() {
			checkTextReader(t, tc, []byte(value), true)
		}
	}
}

func checkTextReader(t *testing.T, tc textReaderCase, raw []byte, valid bool) {
	t.Helper()
	data := AppendBytes(nil, raw)
	if strings.Contains(tc.name, "nullable") || tc.name == "array" {
		data = append([]byte{1}, data...)
	}
	for _, arenaSize := range []int{0, len(raw) + 2} {
		d := Decoder{buf: data}
		r := ColumnarReader{buf: data, count: 1}
		arena := bytes.Repeat([]byte{42}, arenaSize)
		arenaOff := 0
		got := tc.read(&d, &r, arena, &arenaOff)
		if valid {
			if got != string(raw) || d.err != nil || r.err != nil {
				t.Fatalf("%s changed valid text: %x -> %x (%v, %v)", tc.name, raw, got, d.err, r.err)
			}
		} else if got != "" || d.err == nil && r.err == nil || arenaOff != 0 || !bytes.Equal(arena, bytes.Repeat([]byte{42}, arenaSize)) {
			t.Fatalf("%s accepted/copied invalid text: %x (%v, %v)", tc.name, got, d.err, r.err)
		}
	}
}

func TestTextSkipsValidateWithoutAllocations(t *testing.T) {
	for _, raw := range append(invalidTextVectors(), []byte("hello"), []byte("東京\ufeff"), []byte{}) {
		valid := bytes.Equal([]byte(strings.ToValidUTF8(string(raw), "")), raw)
		for _, nullable := range []bool{false, true} {
			kind := SkipString
			data := AppendBytes(nil, raw)
			if nullable {
				kind = SkipNullString
				data = append([]byte{1}, data...)
			}
			checkTextSkip(t, data, kind, valid)
			if valid {
				if allocs := testing.AllocsPerRun(100, func() {
					d := NewDecoder(data)
					d.SkipValue(kind)
					r := ColumnarReader{buf: data, count: 1}
					if nullable {
						r.SkipColumnStringPtr()
					} else {
						r.SkipColumnString()
					}
				}); allocs != 0 {
					t.Fatalf("text skips allocated: %v", allocs)
				}
			}
		}
	}
}

func checkTextSkip(t *testing.T, data []byte, kind FieldSkipType, valid bool) {
	t.Helper()
	d := NewDecoder(data)
	d.SkipValue(kind)
	if (d.Finish() == nil) != valid {
		t.Fatalf("skip %d accepted/rejected %x: %v", kind, data, d.err)
	}
	array := NewDecoder(append([]byte{1}, data...))
	array.SkipArray(kind)
	if (array.Finish() == nil) != valid {
		t.Fatalf("array skip %d: %x / %v", kind, data, array.err)
	}
	r := ColumnarReader{buf: data, count: 1}
	if kind == SkipNullString {
		r.SkipColumnStringPtr()
	} else {
		r.SkipColumnString()
	}
	if (r.err == nil && r.off == len(data)) != valid {
		t.Fatalf("column skip %d: %x / %v", kind, data, r.err)
	}
}

func TestTextSkipMalformedFramesAndNull(t *testing.T) {
	for _, data := range [][]byte{nil, {0x80}, {2, 'x'}, {1, 0xff}} {
		checkTextSkip(t, data, SkipString, false)
		checkTextSkip(t, append([]byte{1}, data...), SkipNullString, false)
	}
	for _, data := range [][]byte{nil, {2}} {
		checkTextSkip(t, data, SkipNullString, false)
	}
	checkTextSkip(t, []byte{0}, SkipNullString, true)
}

func TestNullableStringFailureHasNoValue(t *testing.T) {
	for _, raw := range [][]byte{{1}, {1, 0x80}, {1, 1, 0xff}} {
		dec := NewDecoder(raw)
		if value := dec.ReadStringPtr(); value != nil || dec.Err() == nil {
			t.Errorf("failed string returned a value: %v, %v", value, dec.Err())
		}
		dec = NewDecoder(raw)
		var off int
		if value := dec.ReadStringArenaPtr(nil, &off); value != nil || dec.Err() == nil {
			t.Errorf("failed arena string returned a value: %v, %v", value, dec.Err())
		}
	}
}

func TestBytesRemainOpaque(t *testing.T) {
	for _, raw := range invalidTextVectors() {
		data := AppendBytes(nil, raw)
		got, n := ReadBytes(data, 0)
		if n != len(data) || !bytes.Equal(got, raw) {
			t.Fatal("binary bytes were treated as text")
		}
		d := NewDecoder(data)
		d.SkipLenPrefixed()
		r := ColumnarReader{buf: data, count: 1}
		r.SkipColumnBytes()
		if d.Finish() != nil || r.err != nil || r.off != len(data) {
			t.Fatal("opaque byte skip rejected non-text bytes")
		}
	}
}
