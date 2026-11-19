package schema

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
	"github.com/shopspring/decimal"
)

// JSONToBinary converts one structured JSON value into its native Luxo message.
func (s *Schema) JSONToBinary(typeName string, raw json.RawMessage) ([]byte, error) {
	if declaration := s.Sealeds[typeName]; declaration != nil {
		var enc codec.Encoder
		if err := s.writeJSONSealed(&enc, declaration, raw, 1); err != nil {
			return nil, addJSONContext(err, declaration.Name, "", jsonModelContext, true)
		}
		return enc.Bytes(), nil
	}
	model := s.LookupModel(typeName)
	if model == nil {
		return nil, fmt.Errorf("schema: unknown structured type %q", typeName)
	}
	var enc codec.Encoder
	if err := s.writeJSONObject(&enc, model, raw, 1); err != nil {
		return nil, err
	}
	return enc.Bytes(), nil
}

func (s *Schema) writeJSONObject(enc *codec.Encoder, model *Model, raw json.RawMessage, depth int) error {
	if depth > codec.MaxModelDepth {
		return codec.ErrModelDepth
	}
	values, err := s.jsonObjectValues(model, raw, depth)
	if err != nil {
		return addJSONContext(err, model.Name, "", jsonModelContext, depth == 1)
	}
	return s.writeJSONObjectValues(enc, model, values, depth)
}

func (s *Schema) writeJSONObjectValues(enc *codec.Encoder, model *Model, values map[string]json.RawMessage, depth int) error {
	arenaLength, err := jsonArenaLength(values, model.Fields)
	if err != nil {
		return addJSONContext(err, model.Name, "", jsonModelContext, depth == 1)
	}
	enc.WriteVarint(uint64(arenaLength))
	for i := range model.Fields {
		field := &model.Fields[i]
		value, present := values[field.Name]
		if !present {
			continue
		}
		if err := s.writeJSONField(enc, field, value, depth); err != nil {
			return addJSONContext(err, model.Name, field.Name, jsonFieldContext, depth == 1)
		}
	}
	enc.WriteEnd()
	return nil
}

// Borrow values only while encoding. Canonical metadata names avoid retaining
// input keys, and overwritten values receive resource checks, not business decoding.
func (s *Schema) jsonObjectValues(model *Model, raw []byte, depth int) (map[string]json.RawMessage, error) {
	values := make(map[string]json.RawMessage)
	unknown := ""
	hasUnknown := false
	err := jsonutil.VisitObject(raw, func(name, value []byte) error {
		field := lookupJSONField(model, string(name))
		if field == nil {
			if !hasUnknown || string(name) < unknown {
				unknown = string(name)
				hasUnknown = true
			}
			return nil
		}
		if previous := values[field.Name]; previous != nil {
			if err := jsonutil.CheckJSONValueDepth(previous, func(reader *jsonutil.DepthReader) error {
				return s.checkJSONFieldDepth(reader, field, depth)
			}); err != nil {
				return err
			}
		}
		values[field.Name] = value
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("expected object: %w", err)
	}
	if hasUnknown {
		return nil, fmt.Errorf("unknown field %q", unknown)
	}
	return values, nil
}

func lookupJSONField(model *Model, name string) *Field {
	if field := model.FieldByName(name); field != nil {
		return field
	}
	for i := range model.Fields {
		if model.Fields[i].Name == name {
			return &model.Fields[i]
		}
	}
	return nil
}

func (s *Schema) checkJSONFieldDepth(reader *jsonutil.DepthReader, field *Field, depth int) error {
	if field.IsList {
		return reader.List(codec.MaxArrayElements, func(reader *jsonutil.DepthReader) error {
			return s.checkJSONScalarDepth(reader, field, depth)
		})
	}
	return s.checkJSONScalarDepth(reader, field, depth)
}

func (s *Schema) checkJSONScalarDepth(reader *jsonutil.DepthReader, field *Field, depth int) error {
	if field.Type == FieldSealed {
		return s.checkJSONSealedDepth(reader, s.Sealeds[field.TypeName], depth+1)
	}
	if field.Type != FieldModel || reader.Kind() != '{' {
		return reader.JSON()
	}
	model := s.LookupModel(field.TypeName)
	if model == nil {
		return reader.JSON()
	}
	if depth >= codec.MaxModelDepth {
		return codec.ErrModelDepth
	}
	return reader.Object(func(name []byte) error {
		child := lookupJSONField(model, string(name))
		if child == nil {
			return reader.JSON()
		}
		return s.checkJSONFieldDepth(reader, child, depth+1)
	})
}

func jsonArenaLength(values map[string]json.RawMessage, fields []Field) (int, error) {
	length := 0
	for i := range fields {
		field := &fields[i]
		raw, present := values[field.Name]
		if !present || field.IsList || (field.Type != FieldString && field.Type != FieldEnum) || isJSONNull(raw) {
			continue
		}
		value, err := jsonutil.ParseString(raw)
		if err != nil {
			return 0, fmt.Errorf("%s must be a string", field.Name)
		}
		length += len(value)
	}
	return length, nil
}

func (s *Schema) writeJSONField(enc *codec.Encoder, field *Field, raw json.RawMessage, depth int) error {
	enc.WriteFieldHeader(field.ID)
	if isJSONNull(raw) && (field.Type != FieldJSON || field.IsList || field.Nullable) {
		if !field.Nullable {
			return fmt.Errorf("must not be null")
		}
		enc.WriteNull()
		return nil
	}
	if field.Nullable {
		enc.WritePresent()
	}
	if field.IsList {
		return s.writeJSONList(enc, field, raw, depth)
	}
	return s.writeJSONScalar(enc, field, raw, depth)
}

func (s *Schema) writeJSONList(enc *codec.Encoder, field *Field, raw json.RawMessage, depth int) error {
	values, err := jsonutil.ParseRawArray(raw, codec.MaxArrayElements)
	if err != nil {
		return fmt.Errorf("expected array: %w", err)
	}
	enc.WriteVarint(uint64(len(values))) // ParseRawArray already validated the array bound.
	element := *field
	element.IsList = false
	element.Nullable = false
	for i := range values {
		if field.Type != FieldJSON && isJSONNull(values[i]) {
			return fmt.Errorf("element %d must not be null", i)
		}
		if err := s.writeJSONScalar(enc, &element, values[i], depth); err != nil {
			return addJSONContext(err, "", "", i, false)
		}
	}
	return nil
}

func (s *Schema) writeJSONScalar(enc *codec.Encoder, field *Field, raw json.RawMessage, depth int) error {
	switch field.Type {
	case FieldInt, FieldDuration:
		return writeJSONInt(enc, raw)
	case FieldFloat:
		return writeJSONFloat(enc, raw)
	case FieldString:
		return writeJSONString(enc, raw)
	case FieldBool:
		return writeJSONBool(enc, raw)
	case FieldDateTime:
		return writeJSONDateTime(enc, raw)
	case FieldBytes:
		return writeJSONBytes(enc, raw)
	case FieldEnum:
		return s.writeJSONEnum(enc, field, raw)
	case FieldModel:
		return s.writeJSONModel(enc, field, raw, depth)
	case FieldSealed:
		return s.writeJSONSealedField(enc, field, raw, depth)
	case FieldUUID:
		return writeJSONUUID(enc, raw)
	case FieldDecimal:
		return writeJSONDecimal(enc, raw)
	case FieldJSON:
		if err := jsonutil.CheckJSONDepth(raw); err != nil {
			return err
		}
		enc.WriteBytes(raw)
		return nil
	default:
		return fmt.Errorf("unsupported field type %s", field.Type)
	}
}

func writeJSONInt(enc *codec.Encoder, raw json.RawMessage) error {
	value, err := jsonutil.ParseInt64(raw)
	if err != nil {
		return fmt.Errorf("expected integer: %w", err)
	}
	enc.WriteInt(value)
	return nil
}

func writeJSONFloat(enc *codec.Encoder, raw json.RawMessage) error {
	value, err := jsonutil.ParseFloat64(raw)
	if err != nil {
		return fmt.Errorf("expected number: %w", err)
	}
	return enc.WriteFloat(value)
}

func writeJSONString(enc *codec.Encoder, raw json.RawMessage) error {
	value, err := jsonutil.ParseString(raw)
	if err != nil {
		return fmt.Errorf("expected string: %w", err)
	}
	return enc.WriteString(value)
}

func writeJSONBool(enc *codec.Encoder, raw json.RawMessage) error {
	value, err := jsonutil.ParseBool(raw)
	if err != nil {
		return fmt.Errorf("expected boolean: %w", err)
	}
	enc.WriteBool(value)
	return nil
}

func writeJSONDateTime(enc *codec.Encoder, raw json.RawMessage) error {
	parsed, err := jsonutil.ParseDateTime(raw)
	if err != nil {
		return fmt.Errorf("expected RFC3339 string: %w", err)
	}
	return enc.WriteDateTime(parsed)
}

func writeJSONBytes(enc *codec.Encoder, raw json.RawMessage) error {
	value, err := jsonutil.ParseBase64(raw)
	if err != nil {
		return fmt.Errorf("expected base64 bytes: %w", err)
	}
	enc.WriteBytes(value)
	return nil
}

func (s *Schema) writeJSONEnum(enc *codec.Encoder, field *Field, raw json.RawMessage) error {
	value, err := jsonutil.ParseString(raw)
	if err != nil {
		return fmt.Errorf("expected enum string: %w", err)
	}
	if enum := s.Enums[field.TypeName]; enum != nil && !containsString(enum.Values, value) {
		return fmt.Errorf("unknown %s value %q", field.TypeName, value)
	}
	return enc.WriteString(value)
}

func (s *Schema) writeJSONModel(enc *codec.Encoder, field *Field, raw json.RawMessage, depth int) error {
	model := s.LookupModel(field.TypeName)
	if model == nil {
		return fmt.Errorf("unknown structured type %q", field.TypeName)
	}
	return s.writeJSONObject(enc, model, raw, depth+1)
}

func writeJSONUUID(enc *codec.Encoder, raw json.RawMessage) error {
	value, err := jsonutil.ParseString(raw)
	if err != nil {
		return fmt.Errorf("expected UUID string: %w", err)
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		return fmt.Errorf("expected UUID string: %w", err)
	}
	enc.WriteUUID([16]byte(parsed))
	return nil
}

func writeJSONDecimal(enc *codec.Encoder, raw json.RawMessage) error {
	value, err := jsonutil.ParseString(raw)
	if err != nil {
		return fmt.Errorf("expected decimal string: %w", err)
	}
	if _, err := decimal.NewFromString(value); err != nil {
		return fmt.Errorf("expected decimal string: %w", err)
	}
	return enc.WriteString(value)
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
