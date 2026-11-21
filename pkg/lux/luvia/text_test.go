package luvia

import (
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestPrimaryKeysRejectInvalidTextAfterKey(t *testing.T) {
	for _, kind := range []schema.FieldType{schema.FieldString, schema.FieldEnum, schema.FieldDecimal, schema.FieldBytes} {
		for _, nullable := range []bool{false, true} {
			for _, list := range []bool{false, true} {
				registry := schema.New()
				model := &schema.Model{Name: "Parent", Fields: []schema.Field{{ID: 1, Name: "id", Type: schema.FieldInt}, {ID: 2, Name: "value", Type: kind, Nullable: nullable, IsList: list}}}
				registry.RegisterModel(model)
				value := codec.AppendBytes(nil, []byte{0xff})
				if list {
					value = append([]byte{1}, value...)
				}
				rowValue := append([]byte(nil), value...)
				if nullable {
					rowValue = append([]byte{1}, rowValue...)
				}
				row := append(append([]byte{0, 1, 84, 2}, rowValue...), 0)
				if _, ok := ExtractPrimaryKeys(row, &model.Fields[0], model, registry); ok != (kind == schema.FieldBytes) {
					t.Fatalf("row text/bytes distinction lost: %v nullable=%v list=%v", kind, nullable, list)
				}
				if list {
					value = codec.AppendBytes(nil, value)
				}
				if nullable {
					value = append([]byte{1}, value...)
				}
				column := append(append([]byte{1, 0, 1, 84, 2}, value...), 0)
				// This entry validates only list framing; nested content needs the registry below.
				if !list {
					if _, ok := ExtractPrimaryKeyColumn(column, &model.Fields[0], model); ok != (kind == schema.FieldBytes) {
						t.Fatalf("column text/bytes distinction lost: %v nullable=%v", kind, nullable)
					}
				}
				if ok := skipColumnarResponse(column, model.Fields, registry, 0); ok != (kind == schema.FieldBytes) {
					t.Fatalf("nested column text accepted: %v nullable=%v list=%v", kind, nullable, list)
				}
			}
		}
	}
}
