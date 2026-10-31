package schema

import (
	"bytes"
	"math"
	"testing"
)

func TestJSONRowsMinimumSize(t *testing.T) {
	columns := []typedColumn{{field: &Field{JSONPrefix: []byte(`"id":`)}}}
	for _, sample := range []struct {
		count, limit, want int
		invalid            bool
	}{
		{0, 2, 2, false}, {1, 10, 10, false}, {128, 1153, 1153, false},
		{-1, 100, 0, true}, {0, 1, 0, true}, {1, 2, 0, true}, {1, 9, 0, true},
		{128, 1152, 0, true}, {math.MaxInt, math.MaxInt, 0, true},
	} {
		got, err := jsonRowsMinimumSize(sample.count, columns, sample.limit)
		if (err != nil) != sample.invalid || (!sample.invalid && got != sample.want) {
			t.Fatalf("%+v: got %d, %v", sample, got, err)
		}
	}
	if size, err := jsonRowsMinimumSize(2, nil, 7); err != nil || size != 7 {
		t.Fatalf("empty-column internal sizing: %d, %v", size, err)
	}
}

func TestGrowJSONRowsPreservesPrefixAndReusesCapacity(t *testing.T) {
	columns := []typedColumn{{field: &Field{JSONPrefix: []byte(`"id":`)}}}
	for _, capacity := range []int{7, 2048} {
		dst := append(make([]byte, 0, capacity), "prefix:"...)
		got, err := growJSONRows(dst, 128, columns)
		if err != nil || !bytes.Equal(got, dst) || cap(got)-len(got) < 1153 {
			t.Fatalf("capacity=%d: %q, cap=%d, %v", capacity, got, cap(got), err)
		}
		if capacity == 2048 && &got[0] != &dst[0] {
			t.Fatal("replaced a sufficiently sized caller buffer")
		}
	}
	dst := []byte("prefix:")
	got, err := growJSONRows(dst, math.MaxInt, columns)
	if err == nil || !bytes.Equal(got, dst) || &got[0] != &dst[0] {
		t.Fatalf("overflow mutated the original prefix: %q, %v", got, err)
	}
	got, err = appendJSONRows(dst, math.MaxInt, columns)
	if err == nil || !bytes.Equal(got, dst) {
		t.Fatalf("row append ignored capacity failure: %q, %v", got, err)
	}
}

func TestGrowJSONRowsDefersUnvalidatedBlobCapacity(t *testing.T) {
	for _, field := range []Field{
		{Type: FieldJSON}, {Type: FieldJSON, Nullable: true},
		{Type: FieldModel}, {Type: FieldModel, Nullable: true},
		{Type: FieldInt, IsList: true}, {Type: FieldString, IsList: true, Nullable: true},
	} {
		field.JSONPrefix = []byte(`"payload":`)
		for _, blobFirst := range []bool{false, true} {
			columns := []typedColumn{{field: &Field{Type: FieldInt, JSONPrefix: []byte(`"id":`)}}, {field: &field}}
			if blobFirst {
				columns[0], columns[1] = columns[1], columns[0]
			}
			dst := []byte("prefix:")
			got, err := growJSONRows(dst, 128, columns)
			if err != nil || !bytes.Equal(got, dst) || &got[0] != &dst[0] || cap(got) != cap(dst) {
				t.Fatalf("reserved output before blob validation: %+v, first=%t, cap=%d, %v", field, blobFirst, cap(got), err)
			}
		}
	}
}

func TestColumnarJSONCapacityPreservesOutput(t *testing.T) {
	for _, fields := range []int{1, 16, 128} {
		for _, rows := range []int{0, 1, 128} {
			for _, large := range []bool{false, true} {
				model, data, want := columnarJSONCapacityFixture(t, fields, rows, large)
				dst := []byte("prefix:")
				got, err := BinaryListToJSON(dst, data, model)
				if err != nil || !bytes.Equal(got, append(bytes.Clone(dst), want...)) {
					t.Fatalf("fields=%d rows=%d large=%t: %v", fields, rows, large, err)
				}
				got, err = BinaryListToJSON(dst, append(bytes.Clone(data), 1), model)
				if err == nil || !bytes.Equal(got, dst) {
					t.Fatal("capacity optimization accepted trailing data or changed prefix")
				}
			}
		}
	}
}
