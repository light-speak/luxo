package luvia

import (
	"bytes"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestPrimaryKeysRequireCompleteParent(t *testing.T) {
	registry, model := primaryKeysTestSchema()
	for _, columnar := range []bool{false, true} {
		t.Run(map[bool]string{false: "row", true: "column"}[columnar], func(t *testing.T) {
			valid := primaryKeysTestMessage(columnar, false)
			cases := map[string][]byte{
				"missing end": valid[:len(valid)-1],
				"trailing":    append(bytes.Clone(valid), 0),
				"bad tail":    append(bytes.Clone(valid[:len(valid)-1]), 2, 0x80),
				"unknown":     append(bytes.Clone(valid[:len(valid)-1]), 99, 0),
				"duplicate":   append(bytes.Clone(valid[:len(valid)-1]), 1, 2, 0),
			}
			for name, data := range cases {
				t.Run(name, func(t *testing.T) {
					if _, ok := primaryKeysTestExtract(data, columnar, model, registry); ok {
						t.Fatal("incomplete or ambiguous parent accepted after finding its key")
					}
				})
			}
			keys, ok := primaryKeysTestExtract(valid, columnar, model, registry)
			if !ok || !bytes.Equal(keys.values, codec.AppendSvarint(nil, 9223372036854775807)) {
				t.Fatal("valid key bytes changed")
			}
		})
	}
}

func TestPrimaryKeysRejectNullableOrListKey(t *testing.T) {
	for _, key := range []schema.Field{
		{ID: 1, Type: schema.FieldInt, Nullable: true},
		{ID: 1, Type: schema.FieldInt, IsList: true},
	} {
		registry, model := primaryKeysTestSchema()
		if _, ok := ExtractPrimaryKeys(primaryKeysTestMessage(false, false), &key, model, registry); ok {
			t.Fatal("invalid row key cardinality accepted")
		}
		if _, ok := ExtractPrimaryKeyColumn(primaryKeysTestMessage(true, false), &key, model); ok {
			t.Fatal("invalid column key cardinality accepted")
		}
	}
}

func TestPrimaryKeyColumnSkipsNullableCells(t *testing.T) {
	for _, field := range []schema.Field{
		{Type: schema.FieldModel}, {Type: schema.FieldBytes},
		{Type: schema.FieldInt, IsList: true}, {Type: schema.FieldUUID},
	} {
		field.ID, field.Nullable = 2, true
		registry, model := primaryKeysTestSchema()
		model.Fields[1] = field
		registry.RegisterModel(model)
		data := []byte{2, 0, 2, 0, 1}
		if field.Type == schema.FieldUUID {
			data = append(data, make([]byte, 16)...)
		} else {
			data = codec.AppendBytes(data, []byte{0, 0})
		}
		data = codec.AppendSvarint(append(data, 1), 9223372036854775807)
		data = codec.AppendSvarint(data, 9223372036854775806)
		data = append(data, 0)
		keys, ok := ExtractPrimaryKeyColumn(data, model.PrimaryKeyField(), model)
		if !ok || keys.count != 2 {
			t.Fatalf("nullable column before key rejected: %v", field)
		}
		bad := bytes.Clone(data)
		bad[3] = 2
		if _, ok := ExtractPrimaryKeyColumn(bad, model.PrimaryKeyField(), model); ok {
			t.Fatal("invalid nullable marker accepted")
		}
	}
}

func primaryKeysTestSchema() (*schema.Schema, *schema.Model) {
	registry := schema.New()
	model := &schema.Model{Name: "Parent", Fields: []schema.Field{
		{ID: 1, Name: "key", Type: schema.FieldInt, PrimaryKey: true},
		{ID: 2, Name: "tail", Type: schema.FieldInt},
	}}
	registry.RegisterModel(model)
	return registry, model
}

func primaryKeysTestMessage(columnar, keyLast bool) []byte {
	var result []byte
	if columnar {
		result = append(result, 1)
	}
	result = append(result, 0)
	if keyLast {
		result = codec.AppendSvarint(append(result, 2), 0)
	}
	result = codec.AppendSvarint(append(result, 1), 9223372036854775807)
	if !keyLast {
		result = codec.AppendSvarint(append(result, 2), 0)
	}
	return append(result, 0)
}

func primaryKeysTestExtract(data []byte, columnar bool, model *schema.Model, registry *schema.Schema) (BinaryKeys, bool) {
	if columnar {
		return ExtractPrimaryKeyColumn(data, model.PrimaryKeyField(), model)
	}
	return ExtractPrimaryKeys(data, model.PrimaryKeyField(), model, registry)
}
