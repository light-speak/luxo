package codec

import (
	"bytes"
	"errors"
	"testing"
)

func TestColumnarBytesRejectsInvalidState(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*ColumnarWriter)
		want    error
	}{
		{"zero columns", func(w *ColumnarWriter) { w.SetCount(1) }, ErrEmptyColumnarProjection},
		{"negative count", func(w *ColumnarWriter) { w.SetCount(-1) }, ErrInvalidColumnarCount},
		{"excess count", func(w *ColumnarWriter) { w.SetCount(MaxColumnarRecords + 1) }, ErrInvalidColumnarCount},
		{"maximum count without columns", func(w *ColumnarWriter) { w.SetCount(MaxColumnarRecords) }, ErrEmptyColumnarProjection},
		{"short column", func(w *ColumnarWriter) { w.SetCount(2); w.WriteColumnInt(1, []int64{1}) }, ErrColumnarRowCount},
		{"long column", func(w *ColumnarWriter) { w.SetCount(1); w.WriteColumnInt(1, []int64{1, 2}) }, ErrColumnarRowCount},
		{"empty with values", func(w *ColumnarWriter) { w.WriteColumnInt(1, []int64{1}) }, ErrColumnarRowCount},
		{"zero field", func(w *ColumnarWriter) { w.SetCount(1); w.WriteColumnInt(0, []int64{1}) }, ErrInvalidColumnarField},
		{"negative field", func(w *ColumnarWriter) { w.SetCount(1); w.WriteColumnInt(-1, []int64{1}) }, ErrInvalidColumnarField},
		{"invalid field is sticky", func(w *ColumnarWriter) {
			w.SetCount(1)
			w.WriteColumnInt(0, []int64{1})
			w.WriteColumnBool(1, []bool{true})
		}, ErrInvalidColumnarField},
		{"negative arena", func(w *ColumnarWriter) { w.totalStringLen = -1 }, ErrInvalidColumnarArena},
		{"excess arena", func(w *ColumnarWriter) { w.totalStringLen = MaxArenaSize + 1 }, ErrInvalidColumnarArena},
		{"inconsistent columns", func(w *ColumnarWriter) {
			w.SetCount(2)
			w.WriteColumnInt(1, []int64{1, 2})
			w.WriteColumnBool(2, []bool{true})
			w.WriteColumnInt(3, []int64{3, 4})
		}, ErrColumnarRowCount},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := ColumnarWriter{buf: []byte{42, 43}}
			test.prepare(&writer)
			for range 2 {
				data, err := writer.Bytes()
				if data != nil || !errors.Is(err, test.want) {
					t.Fatalf("invalid output = %x, %v; want nil, %v", data, err, test.want)
				}
				if !bytes.Equal(writer.buf, []byte{42, 43}) {
					t.Fatal("failed validation modified the previous output")
				}
			}
			writer.Reset()
			if data, err := writer.Bytes(); err != nil || !bytes.Equal(data, []byte{0, 0, 0}) {
				t.Fatalf("reset did not restore an empty writer: %x, %v", data, err)
			}
		})
	}
}

func TestColumnarBytesRetryAndNullColumn(t *testing.T) {
	var writer ColumnarWriter
	writer.SetCount(2)
	if data, err := writer.Bytes(); data != nil || !errors.Is(err, ErrEmptyColumnarProjection) {
		t.Fatalf("missing projection: %x, %v", data, err)
	}
	writer.WriteColumnIntPtr(1, []*int64{nil, nil})
	want := []byte{2, 0, 1, 0, 0, 0}
	for range 2 {
		data, err := writer.Bytes()
		if err != nil || !bytes.Equal(data, want) {
			t.Fatalf("null column rejected: %x, %v", data, err)
		}
	}
	writer.SetCount(1)
	if data, err := writer.Bytes(); data != nil || !errors.Is(err, ErrColumnarRowCount) {
		t.Fatalf("changed row count accepted: %x, %v", data, err)
	}
	writer.SetCount(2)
	if data, err := writer.Bytes(); err != nil || !bytes.Equal(data, want) {
		t.Fatalf("restored count rejected: %x, %v", data, err)
	}
}
