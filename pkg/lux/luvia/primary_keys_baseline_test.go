package luvia

import (
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

// Frozen pre-fix key extraction; benchmarks only use valid flat parents.
func ExtractPrimaryKeysBaseline(primary []byte, keyField *schema.Field, model *schema.Model, registry *schema.Schema) (BinaryKeys, bool) {
	if len(primary) == 0 || keyField == nil || model == nil || registry == nil || !isFederationKeyType(keyField.Type) {
		return BinaryKeys{}, false
	}
	dec := codec.NewDecoder(primary)
	dec.SkipArenaHeader()
	for dec.NextField() {
		fieldID := dec.FieldID()
		field := model.FieldByID(fieldID)
		if field == nil {
			return BinaryKeys{}, false
		}
		if fieldID == keyField.ID {
			start := dec.Offset()
			if !skipSchemaRowFieldBaseline(dec, field, registry) {
				return BinaryKeys{}, false
			}
			return BinaryKeys{count: 1, values: primary[start:dec.Offset()]}, true
		}
		if !skipSchemaRowFieldBaseline(dec, field, registry) {
			return BinaryKeys{}, false
		}
	}
	return BinaryKeys{}, false
}

func skipSchemaRowFieldBaseline(dec *codec.Decoder, field *schema.Field, registry *schema.Schema) bool {
	if field.Relation {
		return skipNestedRowFieldBaseline(dec, field, registry)
	}
	if field.IsList {
		element := *field
		element.Nullable = false
		element.IsList = false
		dec.SkipArray(schemaFieldToSkipType(&element))
	} else {
		dec.SkipValue(schemaFieldToSkipType(field))
	}
	return dec.Err() == nil
}

func skipNestedRowFieldBaseline(dec *codec.Decoder, field *schema.Field, registry *schema.Schema) bool {
	nested := registry.Models[field.TypeName]
	if nested == nil {
		if declaration := registry.Types[field.TypeName]; declaration != nil {
			nested = declaration.AsModel()
		}
	}
	if nested == nil {
		return false
	}
	if field.IsList {
		count := dec.ReadArrayLength()
		if dec.Err() != nil {
			return false
		}
		for range count {
			if !skipModelRowBaseline(dec, nested, registry) {
				return false
			}
		}
		return true
	}
	if field.Nullable && !dec.ReadBool() {
		return dec.Err() == nil
	}
	return skipModelRowBaseline(dec, nested, registry)
}

func skipModelRowBaseline(dec *codec.Decoder, model *schema.Model, registry *schema.Schema) bool {
	dec.SkipArenaHeader()
	for dec.NextField() {
		field := model.FieldByID(dec.FieldID())
		if field == nil || !skipSchemaRowFieldBaseline(dec, field, registry) {
			return false
		}
	}
	return dec.Err() == nil
}

func ExtractPrimaryKeyColumnBaseline(data []byte, keyField *schema.Field, model *schema.Model) (BinaryKeys, bool) {
	if len(data) == 0 || keyField == nil || model == nil || !isFederationKeyType(keyField.Type) {
		return BinaryKeys{}, false
	}
	r := codec.NewColumnarReader(data)
	if r.Err() != nil || r.Count() == 0 {
		return BinaryKeys{}, false
	}
	for r.NextColumn() {
		field := model.FieldByID(r.FieldID())
		if field == nil {
			return BinaryKeys{}, false
		}
		start := r.Offset()
		skipColumnarColumnBaseline(r, field)
		if r.Err() != nil {
			return BinaryKeys{}, false
		}
		if field.ID == keyField.ID {
			return BinaryKeys{count: r.Count(), values: data[start:r.Offset()]}, true
		}
	}
	return BinaryKeys{}, false
}

// skipColumnarColumnBaseline advances past all values in a column without allocating.
func skipColumnarColumnBaseline(r *codec.ColumnarReader, f *schema.Field) {
	if f.IsList {
		if f.Nullable {
			r.SkipColumnBytesPtr()
		} else {
			r.SkipColumnBytes()
		}
		return
	}
	switch f.Type {
	case schema.FieldInt, schema.FieldDateTime, schema.FieldDuration:
		if f.Nullable {
			r.SkipColumnIntPtr()
		} else {
			r.SkipColumnInt()
		}
	case schema.FieldFloat:
		if f.Nullable {
			r.SkipColumnFloatPtr()
		} else {
			r.SkipColumnFloat()
		}
	case schema.FieldString, schema.FieldEnum:
		if f.Nullable {
			r.SkipColumnStringPtr()
		} else {
			r.SkipColumnString()
		}
	case schema.FieldBool:
		if f.Nullable {
			r.SkipColumnBoolPtr()
		} else {
			r.SkipColumnBool()
		}
	case schema.FieldUUID:
		if f.Nullable {
			r.SkipColumnUUIDPtr()
		} else {
			r.SkipColumnUUID()
		}
	case schema.FieldModel:
		r.SkipColumnBytes()
	default:
		r.SkipColumnBytes()
	}
}
