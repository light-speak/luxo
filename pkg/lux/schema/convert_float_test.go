package schema

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/light-speak/luxo/internal/testutil"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

func TestBinaryJSONConversionRejectsInvalidFloat(t *testing.T) {
	s := New()
	m := &Model{Name: "Measured", Fields: []Field{{ID: 1, Name: "value", Type: FieldFloat}}}
	s.RegisterModel(m)
	parent := &Model{Name: "Parent", Fields: []Field{{ID: 1, Name: "child", Type: FieldModel, TypeName: m.Name, Relation: true}}}
	s.RegisterModel(parent)
	for _, bits := range []uint64{0x7ff0000000000000, 0xfff0000000000000, 0x7ff8000000000001} {
		value := binary.LittleEndian.AppendUint64(nil, bits)
		row := append(append([]byte{0, 1}, value...), 0)
		column := append(append([]byte{1, 0, 1}, value...), 0)
		nested := append(codec.AppendBytes([]byte{0, 1}, row), 0)
		for name, convert := range map[string]func([]byte) ([]byte, error){
			"scalar": func(dst []byte) ([]byte, error) { return BinaryScalarToJSON(dst, value, "Float") },
			"scalar-list": func(dst []byte) ([]byte, error) {
				return BinaryScalarListToJSON(dst, append([]byte{1}, value...), "Float")
			},
			"row":    func(dst []byte) ([]byte, error) { return BinaryToJSON(dst, row, m, s) },
			"column": func(dst []byte) ([]byte, error) { return BinaryListToJSON(dst, column, m, s) },
			"page": func(dst []byte) ([]byte, error) {
				return BinaryPaginatedListToJSON(dst, append(column, 2, 2, 20), m, s)
			},
			"nested": func(dst []byte) ([]byte, error) { return BinaryToJSON(dst, nested, parent, s) },
		} {
			t.Run(name, func(t *testing.T) {
				prefix := []byte("prefix")
				got, err := convert(prefix)
				if err == nil || !bytes.Equal(got, prefix) {
					t.Fatalf("invalid Float became JSON success: %q, %v", got, err)
				}
			})
		}
	}
}

func TestJSONConversionRejectsBrokenFrames(t *testing.T) {
	s := New()
	m := &Model{Name: "Measured", Fields: []Field{{ID: 1, Name: "value", Type: FieldFloat}}}
	s.RegisterModel(m)
	for _, raw := range [][]byte{nil, {0}, {0, 1}, {0, 0, 42}, {0, 1, 0}} {
		if got, err := BinaryToJSON(nil, raw, m); err == nil || len(got) != 0 {
			t.Fatalf("bad row accepted: %x → %q, %v", raw, got, err)
		}
	}
	for _, raw := range [][]byte{nil, {0}, {0, 0}, {1, 0}, {1, 0, 0x80}, {1, 0, 0, 42}} {
		if got, err := BinaryListToJSON(nil, raw, m); err == nil || len(got) != 0 {
			t.Fatalf("bad column accepted: %x → %q, %v", raw, got, err)
		}
	}
}

func TestJSONConversionPropagatesNestedFloatErrors(t *testing.T) {
	value := binary.LittleEndian.AppendUint64(nil, 0x7ff0000000000000)
	for _, relation := range []bool{false, true} {
		for _, nullable := range []bool{false, true} {
			s := New()
			s.RegisterModel(&Model{Name: "Measured", Fields: []Field{{ID: 1, Name: "value", Type: FieldFloat}}})
			field := Field{ID: 1, Name: "values", Type: FieldFloat, IsList: true, Nullable: nullable}
			blob := append([]byte{1}, value...)
			if relation {
				field.Type, field.TypeName, field.Relation = FieldModel, "Measured", true
				blob = append(append([]byte{1, 0, 1}, value...), 0)
			}
			m := &Model{Name: "Parent", Fields: []Field{field}}
			s.RegisterModel(m)
			w := &codec.ColumnarWriter{}
			w.SetCount(1)
			if nullable {
				w.WriteColumnBytesPtr(1, []*[]byte{&blob})
			} else {
				w.WriteColumnBytes(1, [][]byte{blob})
			}
			data := testutil.Bytes(t)(w.Bytes())
			if got, err := BinaryListToJSON(nil, data, m, s); err == nil || len(got) != 0 {
				t.Fatalf("nested bad list: %q, %v", got, err)
			}
			page := append(data, 2, 2, 20)
			if got, err := BinaryPaginatedListToJSON(nil, page, m, s); err == nil || len(got) != 0 {
				t.Fatalf("nested bad page: %q, %v", got, err)
			}
		}
	}
}
