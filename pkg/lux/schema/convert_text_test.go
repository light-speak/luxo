package schema

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func TestBinaryTextConversionPreservesValidUTF8(t *testing.T) {
	for _, value := range []string{"", "\ufeff東京", "\ufffd", "café", "\U0001f642"} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, list := range []bool{false, true} {
			for _, nullable := range []bool{false, true} {
				for _, kind := range []FieldType{FieldString, FieldEnum} {
					for _, convert := range textModelConverters([]byte(value), kind, list, nullable) {
						got, err := convert(nil)
						if err != nil || !json.Valid(got) || !bytes.Contains(got, encoded) {
							t.Fatalf("valid text changed: %q / %v", got, err)
						}
					}
				}
			}
		}
	}
}

func TestBinaryTextConversionRejectsInvalidUTF8(t *testing.T) {
	for _, raw := range [][]byte{{0xff}, {0xc0, 0x80}, {0xed, 0xa0, 0x80}, {0xe2, 0x82}} {
		for _, list := range []bool{false, true} {
			for _, nullable := range []bool{false, true} {
				for _, kind := range []FieldType{FieldString, FieldEnum} {
					for _, convert := range textModelConverters(raw, kind, list, nullable) {
						prefix := []byte("prefix")
						got, err := convert(prefix)
						if err == nil || !bytes.Equal(got, prefix) {
							t.Fatalf("invalid text became success: %q / %v", got, err)
						}
					}
				}
			}
		}
	}
}

func textModelConverters(raw []byte, kind FieldType, list, nullable bool) []func([]byte) ([]byte, error) {
	s := New()
	model := &Model{Name: "Text", Fields: []Field{{ID: 1, Name: "value", Type: kind, IsList: list, Nullable: nullable}}}
	s.RegisterModel(model)
	parent := &Model{Name: "Parent", Fields: []Field{{ID: 1, Name: "child", Type: FieldModel, TypeName: model.Name, Relation: true}}}
	s.RegisterModel(parent)
	value := codec.AppendBytes(nil, raw)
	if list {
		value = append([]byte{1}, value...)
	}
	row := []byte{0, 1}
	if nullable {
		row = append(row, 1)
	}
	row = append(append(row, value...), 0)
	nested := append(codec.AppendBytes([]byte{0, 1}, row), 0)
	if list {
		value = codec.AppendBytes(nil, value)
	}
	column := []byte{1, 0, 1}
	if nullable {
		column = append(column, 1)
	}
	column = append(append(column, value...), 0)
	page := append(bytes.Clone(column), 2, 2, 20)
	return []func([]byte) ([]byte, error){
		func(dst []byte) ([]byte, error) {
			return BinaryScalarToJSON(dst, codec.AppendBytes(nil, raw), "String")
		},
		func(dst []byte) ([]byte, error) {
			return BinaryScalarListToJSON(dst, codec.AppendBytes([]byte{1}, raw), "String")
		},
		func(dst []byte) ([]byte, error) { return BinaryToJSON(dst, row, model, s) },
		func(dst []byte) ([]byte, error) { return BinaryListToJSON(dst, column, model, s) },
		func(dst []byte) ([]byte, error) { return BinaryPaginatedListToJSON(dst, page, model, s) },
		func(dst []byte) ([]byte, error) { return BinaryToJSON(dst, nested, parent, s) },
	}
}
