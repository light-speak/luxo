package luvia

import (
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

// Match the existing selection depth limit; cyclic schemas must not permit
// unbounded recursion while examining untrusted response frames.
const maxKeyResponseDepth = 32

func skipResponseField(dec *codec.Decoder, field *schema.Field, registry *schema.Schema, columnar bool, depth int) bool {
	if field.Relation {
		return skipNestedRowField(dec, field, registry, depth+1)
	}
	if field.IsList {
		if field.Nullable && !dec.ReadBool() {
			return dec.Err() == nil
		}
		if columnar {
			body := dec.ReadBytes()
			if dec.Err() != nil {
				return false
			}
			child := codec.NewDecoder(body)
			return skipScalarResponseList(child, field) && child.Finish() == nil
		}
		return skipScalarResponseList(dec, field)
	}
	if field.Type == schema.FieldBool {
		if !field.Nullable || dec.ReadBool() {
			dec.ReadBool()
		}
	} else {
		dec.SkipValue(schemaFieldToSkipType(field))
	}
	return dec.Err() == nil
}

func skipScalarResponseList(dec *codec.Decoder, field *schema.Field) bool {
	element := *field
	element.Nullable, element.IsList = false, false
	if field.Type == schema.FieldBool {
		count := dec.ReadArrayLength()
		for range count {
			dec.ReadBool()
			if dec.Err() != nil {
				return false
			}
		}
	} else {
		dec.SkipArray(schemaFieldToSkipType(&element))
	}
	return dec.Err() == nil
}

func skipNestedRowField(dec *codec.Decoder, field *schema.Field, registry *schema.Schema, depth int) bool {
	if depth >= maxKeyResponseDepth {
		return false
	}
	fields, ok := nestedResponseFields(registry, field.TypeName)
	if !ok {
		return false
	}
	if field.Nullable && !dec.ReadBool() {
		return dec.Err() == nil
	}
	body := dec.ReadBytes()
	if dec.Err() != nil {
		return false
	}
	return skipModelResponse(body, fields, registry, field.IsList, depth)
}

func nestedResponseFields(registry *schema.Schema, name string) ([]schema.Field, bool) {
	if model := registry.Models[name]; model != nil {
		return model.Fields, true
	}
	if declaration := registry.Types[name]; declaration != nil {
		// A read-only field view avoids AsModel's per-request maps and copies.
		return declaration.Fields, true
	}
	return nil, false
}

func skipModelResponse(body []byte, fields []schema.Field, registry *schema.Schema, columnar bool, depth int) bool {
	if columnar {
		return skipColumnarResponse(body, fields, registry, depth)
	}
	dec := codec.NewDecoder(body)
	dec.ReadArenaSize()
	for dec.NextField() {
		field := responseFieldByID(fields, dec.FieldID())
		if field == nil {
			return false
		}
		if !skipResponseField(dec, field, registry, false, depth) {
			return false
		}
	}
	return dec.Finish() == nil
}

// Primitive columns use the codec's allocation-free bulk checks. Only nested
// values need recursive schema traversal; their outer bounds are checked first.
func skipColumnarResponse(body []byte, fields []schema.Field, registry *schema.Schema, depth int) bool {
	reader := codec.NewColumnarReader(body)
	if reader.Err() != nil {
		return false
	}
	for {
		columnStart := reader.Offset()
		if !reader.NextColumn() {
			end, size := codec.ReadVarint(body, columnStart)
			return reader.Err() == nil && size > 0 && end == 0 && columnStart+size == len(body)
		}
		field := responseFieldByID(fields, reader.FieldID())
		if field == nil {
			return false
		}
		start := reader.Offset()
		skipColumnarColumn(reader, field)
		if reader.Err() != nil {
			return false
		}
		if field.Relation || field.IsList {
			values := codec.NewDecoder(body[start:reader.Offset()])
			for range reader.Count() {
				if !skipResponseField(values, field, registry, true, depth) {
					return false
				}
			}
			// Both traversals consume the same presence/length framing for
			// exactly Count records; each nested body is checked independently.
		}
	}
}

func responseFieldByID(fields []schema.Field, id int) *schema.Field {
	for i := range fields {
		if fields[i].ID == id {
			return &fields[i]
		}
	}
	return nil
}
