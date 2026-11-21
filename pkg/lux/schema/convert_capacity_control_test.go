package schema

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func capacityJSONControl(t testing.TB, keySize, badRow int) (*Model, []byte, []byte) {
	t.Helper()
	name := strings.Repeat("k", keySize)
	model := &Model{Name: "CapacityControl", Fields: []Field{{ID: 1, Name: name, Type: FieldJSON}}}
	if err := New().RegisterModel(model); err != nil {
		t.Fatal(err)
	}
	values := make([][]byte, 128)
	for row := range values {
		values[row] = []byte(`{"n":123456789012345678901234567890,"s":"東京"}`)
	}
	if badRow >= 0 {
		values[badRow] = []byte(`"\uD800"`)
	}
	var writer codec.ColumnarWriter
	writer.SetCount(len(values))
	writer.WriteColumnBytes(1, values)
	data, err := writer.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	row := `{"` + name + `":` + string(values[0]) + `}`
	want := []byte("[" + strings.Repeat(row+",", 127) + row + "]")
	return model, data, want
}

func TestColumnarJSONCapacityRejectsInvalidRawValues(t *testing.T) {
	for _, keySize := range []int{1, 512} {
		for _, badRow := range []int{-1, 0, 64, 127} {
			model, data, want := capacityJSONControl(t, keySize, badRow)
			prefix := []byte("prefix:")
			got, err := BinaryListToJSON(prefix, data, model)
			if badRow >= 0 {
				if err == nil || !bytes.Equal(got, prefix) {
					t.Fatalf("key=%d row=%d: invalid raw JSON escaped rollback: %q, %v", keySize, badRow, got, err)
				}
			} else if err != nil || !bytes.Equal(got, append(bytes.Clone(prefix), want...)) {
				t.Fatalf("valid raw JSON changed: %v", err)
			}
		}
	}
}

func BenchmarkColumnarJSONCapacityRawControl(b *testing.B) {
	for _, keySize := range []int{1, 512} {
		for _, badRow := range []int{-1, 0, 127} {
			model, data, want := capacityJSONControl(b, keySize, badRow)
			b.Run(fmt.Sprintf("key=%d/badRow=%d", keySize, badRow), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					got, err := BinaryListToJSON(nil, data, model)
					if badRow >= 0 {
						if err == nil || len(got) != 0 {
							b.Fatal("invalid raw JSON accepted")
						}
					} else if err != nil || !bytes.Equal(got, want) {
						b.Fatalf("valid raw JSON changed: %v", err)
					}
				}
			})
		}
	}
}
