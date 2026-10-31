package codec

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
)

type payloadReaderCase struct {
	name   string
	width  int
	array  func(*Decoder) int
	column func(*ColumnarReader) int
}

func payloadReaderCases() []payloadReaderCase {
	return []payloadReaderCase{
		{"int", 1, func(d *Decoder) int { return len(d.ReadIntArray()) }, func(r *ColumnarReader) int { return len(r.ReadColumnInt()) }},
		{"float", 8, func(d *Decoder) int { return len(d.ReadFloatArray()) }, func(r *ColumnarReader) int { return len(r.ReadColumnFloat()) }},
		{"string", 1, func(d *Decoder) int { return len(d.ReadStringArray()) }, func(r *ColumnarReader) int { return len(r.ReadColumnString()) }},
		{"bool", 1, func(d *Decoder) int { return len(d.ReadBoolArray()) }, func(r *ColumnarReader) int { return len(r.ReadColumnBool()) }},
		{"bytes", 1, func(d *Decoder) int { return len(d.ReadBytesArray()) }, func(r *ColumnarReader) int { return len(r.ReadColumnBytes()) }},
		{"uuid", 16, func(d *Decoder) int { return len(d.ReadUUIDArray()) }, func(r *ColumnarReader) int { return len(r.ReadColumnUUID()) }},
		{"int-nullable", 1, nil, func(r *ColumnarReader) int { return len(r.ReadColumnIntPtr()) }},
		{"float-nullable", 1, nil, func(r *ColumnarReader) int { return len(r.ReadColumnFloatPtr()) }},
		{"string-nullable", 1, nil, func(r *ColumnarReader) int { return len(r.ReadColumnStringPtr()) }},
		{"bool-nullable", 1, nil, func(r *ColumnarReader) int { return len(r.ReadColumnBoolPtr()) }},
		{"bytes-nullable", 1, nil, func(r *ColumnarReader) int { return len(r.ReadColumnBytesPtr()) }},
		{"uuid-nullable", 1, nil, func(r *ColumnarReader) int { return len(r.ReadColumnUUIDPtr()) }},
		{"string-arena", 1, nil, func(r *ColumnarReader) int { var off int; return len(r.ReadColumnStringArena(nil, &off)) }},
		{"string-nullable-arena", 1, nil, func(r *ColumnarReader) int { var off int; return len(r.ReadColumnStringPtrArena(nil, &off)) }},
	}
}

func TestElementPayloadPreflight(t *testing.T) {
	for _, tc := range payloadReaderCases() {
		t.Run(tc.name, func(t *testing.T) {
			const count = 3
			data := append([]byte{42}, make([]byte, count*tc.width)...)
			for end := 1; end < len(data); end++ {
				r := ColumnarReader{buf: data[:end], off: 1, count: count}
				if tc.column(&r) != 0 || r.off != 1 || r.err == nil || !strings.Contains(r.err.Error(), "insufficient element bytes") {
					t.Fatalf("column consumed short payload: end=%d offset=%d err=%v", end, r.off, r.err)
				}
				if tc.array != nil {
					array := append(AppendArrayHeader([]byte{42}, count), data[1:end]...)
					d := Decoder{buf: array, off: 1}
					if tc.array(&d) != 0 || d.off != 2 || d.err == nil || !strings.Contains(d.err.Error(), "insufficient element bytes") {
						t.Fatalf("array consumed short payload: end=%d offset=%d err=%v", end, d.off, d.err)
					}
				}
			}
		})
	}
}

func TestElementPayloadValidCounts(t *testing.T) {
	for _, tc := range payloadReaderCases() {
		for _, count := range []int{0, 1, 128} {
			payload := make([]byte, count*tc.width)
			column := append(append([]byte{42}, payload...), 99)
			r := ColumnarReader{buf: column, off: 1, count: count}
			if got := tc.column(&r); got != count || r.err != nil || r.off != len(column)-1 {
				t.Fatalf("%s column changed: count=%d got=%d offset=%d err=%v", tc.name, count, got, r.off, r.err)
			}
			if tc.array != nil {
				array := append(AppendArrayHeader([]byte{42}, count), payload...)
				array = append(array, 99)
				d := Decoder{buf: array, off: 1}
				if got := tc.array(&d); got != count || d.err != nil || d.off != len(array)-1 {
					t.Fatalf("%s array changed: count=%d got=%d offset=%d err=%v", tc.name, count, got, d.off, d.err)
				}
			}
		}
	}
}

func TestElementPayloadPreservesFirstError(t *testing.T) {
	want := errors.New("earlier field failed")
	for _, tc := range payloadReaderCases() {
		r := ColumnarReader{buf: bytes.Repeat([]byte{0}, tc.width), count: 1, err: want}
		if tc.column(&r) != 0 || r.err != want || r.off != 0 {
			t.Errorf("%s column did not preserve failure", tc.name)
		}
		if tc.array != nil {
			d := Decoder{buf: []byte{1}, err: want}
			if tc.array(&d) != 0 || d.err != want {
				t.Errorf("%s array did not preserve failure", tc.name)
			}
		}
	}
}

func TestElementPreflightDoesNotAllocate(t *testing.T) {
	for _, tc := range payloadReaderCases() {
		var column ColumnarReader
		allocs := testing.AllocsPerRun(100, func() {
			column = ColumnarReader{count: 128}
			if tc.column(&column) != 0 || column.err == nil {
				t.Fatal("missing column payload accepted")
			}
		})
		if allocs != 0 {
			t.Errorf("%s short column allocated %v times", tc.name, allocs)
		}
		if tc.array != nil {
			var array Decoder
			data := AppendArrayHeader(nil, 128)
			allocs = testing.AllocsPerRun(100, func() {
				array = Decoder{buf: data}
				if tc.array(&array) != 0 || array.err == nil {
					t.Fatal("missing array payload accepted")
				}
			})
			if allocs != 0 {
				t.Errorf("%s short array allocated %v times", tc.name, allocs)
			}
		}
	}
}

func TestElementPreflightKeepsValueValidation(t *testing.T) {
	for _, tc := range payloadReaderCases() {
		if tc.name == "uuid" {
			continue // Every complete fixed-width UUID bit pattern is valid.
		}
		data := []byte{0x80}
		if tc.name == "float" {
			data = appendFixed64Baseline(nil, math.Inf(1))
		}
		r := ColumnarReader{buf: data, count: 1}
		if tc.column(&r) != 0 || r.err == nil || errors.Is(r.err, ErrInsufficientElementBytes) {
			t.Errorf("%s skipped value validation: %v", tc.name, r.err)
		}
		if tc.array != nil {
			d := Decoder{buf: append([]byte{1}, data...)}
			if tc.array(&d) != 0 || d.err == nil || errors.Is(d.err, ErrInsufficientElementBytes) {
				t.Errorf("%s array skipped value validation: %v", tc.name, d.err)
			}
		}
		if strings.Contains(tc.name, "nullable") {
			r = ColumnarReader{buf: []byte{1, 0x80}, count: 1}
			if tc.column(&r) != 0 || r.err == nil || errors.Is(r.err, ErrInsufficientElementBytes) {
				t.Errorf("%s skipped present value validation: %v", tc.name, r.err)
			}
		}
	}
}

func TestElementPayloadArithmeticBounds(t *testing.T) {
	data := make([]byte, 128, 256)
	for _, tc := range []struct {
		off, count, width int
		valid             bool
	}{
		{0, 128, 1, true}, {0, 16, 8, true}, {0, 8, 16, true},
		{1, 128, 1, false}, {1, 16, 8, false}, {1, 8, 16, false},
		{128, 0, 1, true}, {129, 0, 1, false}, {-1, 0, 1, false},
		{0, -1, 1, false}, {0, 0, 0, false}, {0, 0, -1, false},
		{math.MaxInt, 0, 1, false}, {0, math.MaxInt, 16, false}, {0, 2, math.MaxInt, false},
	} {
		if got := HasElementBytes(data, tc.off, tc.count, tc.width); got != tc.valid {
			t.Errorf("payload bounds %+v: got %v", tc, got)
		}
	}
}
