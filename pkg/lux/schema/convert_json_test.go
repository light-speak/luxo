package schema

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/light-speak/luxo/internal/testutil"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

func TestBinaryJSONConversionRejectsInvalidJSON(t *testing.T) {
	for i, raw := range [][]byte{nil, {}, []byte(" "), []byte("{"), []byte("01"),
		[]byte("NaN"), []byte("null true"), []byte(`{"n":1e}`), {'"', 0xff, '"'},
		[]byte(`"\uD800"`), []byte(`{"\uDC00":1}`), []byte(`{"x":["\uDBFF"]}`)} {
		for _, list := range []bool{false, true} {
			for _, nullable := range []bool{false, true} {
				for name, convert := range rawJSONConverters(t, raw, list, nullable) {
					t.Run(fmt.Sprintf("case=%d/list=%t/nullable=%t/%s", i, list, nullable, name), func(t *testing.T) {
						prefix := []byte("prefix")
						got, err := convert(prefix)
						if err == nil || !bytes.Equal(got, prefix) {
							t.Fatalf("invalid JSON became a successful value: %q / %v", got, err)
						}
					})
				}
			}
		}
	}
}

func TestBinaryJSONConversionPreservesRawValues(t *testing.T) {
	for _, raw := range []string{"null", "-0", "1e+9999", "1e-9999", "123456789012345678901234567890.123456789",
		`{"n":9223372036854775807,"n":9223372036854775808,"text":"精度 café","items":[-0,null,1E+9999]}`,
		`{"\uD800\uDC00":["\uDBFF\uDFFF","\\uD800","�"]}`} {
		for _, list := range []bool{false, true} {
			for _, nullable := range []bool{false, true} {
				for name, convert := range rawJSONConverters(t, []byte(raw), list, nullable) {
					got, err := convert([]byte("prefix"))
					if err != nil || string(got) != "prefix"+rawJSONExpected(raw, list)[name] {
						t.Errorf("%s/list=%t/nullable=%t changed raw JSON %s: %s / %v", name, list, nullable, raw, got, err)
					}
				}
			}
		}
	}
}

func rawJSONExpected(raw string, list bool) map[string]string {
	value := raw
	if list {
		value = "[null," + raw + "]"
	}
	row := `{"value":` + value + `}`
	return map[string]string{
		"scalar": raw, "scalar-list": "[null," + raw + "]",
		"row": row, "column": "[" + row + "]", "nested": `{"child":` + row + `}`,
		"page": `{"items":[` + row + `],"total":1,"page":1,"pageSize":10}`,
	}
}

func rawJSONConverters(t testing.TB, raw []byte, list, nullable bool) map[string]func([]byte) ([]byte, error) {
	s, model, row, column := rawJSONModelFixture(t, raw, list, nullable)
	parent := &Model{Name: "Parent", Fields: []Field{{ID: 1, Name: "child", Type: FieldModel, TypeName: model.Name, Relation: true}}}
	s.RegisterModel(parent)
	nested := append(codec.AppendBytes([]byte{0, 1}, row), 0)
	scalar := codec.AppendBytes(nil, raw)
	scalars := codec.AppendBytes(codec.AppendBytes([]byte{2}, []byte("null")), raw)
	page := append(bytes.Clone(column), 2, 2, 20)
	return map[string]func([]byte) ([]byte, error){
		"scalar":      func(dst []byte) ([]byte, error) { return BinaryScalarToJSON(dst, scalar, "JSON") },
		"scalar-list": func(dst []byte) ([]byte, error) { return BinaryScalarListToJSON(dst, scalars, "JSON") },
		"row":         func(dst []byte) ([]byte, error) { return BinaryToJSON(dst, row, model, s) },
		"column":      func(dst []byte) ([]byte, error) { return BinaryListToJSON(dst, column, model, s) },
		"page":        func(dst []byte) ([]byte, error) { return BinaryPaginatedListToJSON(dst, page, model, s) },
		"nested":      func(dst []byte) ([]byte, error) { return BinaryToJSON(dst, nested, parent, s) },
	}
}

func rawJSONModelFixture(t testing.TB, raw []byte, list, nullable bool) (*Schema, *Model, []byte, []byte) {
	s := New()
	model := &Model{Name: "Opaque", Fields: []Field{{ID: 1, Name: "value", Type: FieldJSON, IsList: list, Nullable: nullable}}}
	s.RegisterModel(model)
	row := []byte{0, 1}
	if nullable {
		row = append(row, 1)
	}
	value := raw
	if list {
		value = codec.AppendBytes(codec.AppendBytes([]byte{2}, []byte("null")), raw)
		row = append(row, value...)
	} else {
		row = codec.AppendBytes(row, value)
	}
	row = append(row, 0)
	var column codec.ColumnarWriter
	column.SetCount(1)
	if nullable {
		column.WriteColumnBytesPtr(1, []*[]byte{&value})
	} else {
		column.WriteColumnBytes(1, [][]byte{value})
	}
	return s, model, row, testutil.Bytes(t)(column.Bytes())
}
