package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/shopspring/decimal"
)

// JSONToBinary converts one structured JSON value into its native Luxo message.
func (s *Schema) JSONToBinary(typeName string, raw json.RawMessage) ([]byte, error) {
	model := s.structuredModel(typeName)
	if model == nil {
		return nil, fmt.Errorf("schema: unknown structured type %q", typeName)
	}
	var enc codec.Encoder
	if err := s.writeJSONObject(&enc, model, raw); err != nil {
		return nil, err
	}
	return enc.Bytes(), nil
}

func (s *Schema) structuredModel(typeName string) *Model {
	if model := s.Models[typeName]; model != nil {
		return model
	}
	if declaration := s.Types[typeName]; declaration != nil {
		return declaration.AsModel()
	}
	return nil
}

func (s *Schema) writeJSONObject(enc *codec.Encoder, model *Model, raw json.RawMessage) error {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return fmt.Errorf("%s: expected object: %w", model.Name, err)
	}
	if values == nil {
		return fmt.Errorf("%s: expected object", model.Name)
	}
	if field := unknownJSONField(values, model); field != "" {
		return fmt.Errorf("%s: unknown field %q", model.Name, field)
	}
	arenaLength, err := jsonArenaLength(values, model.Fields)
	if err != nil {
		return fmt.Errorf("%s: %w", model.Name, err)
	}
	enc.WriteVarint(uint64(arenaLength))
	for i := range model.Fields {
		field := &model.Fields[i]
		value, present := values[field.Name]
		if !present {
			continue
		}
		if err := s.writeJSONField(enc, field, value); err != nil {
			return fmt.Errorf("%s.%s: %w", model.Name, field.Name, err)
		}
	}
	enc.WriteEnd()
	return nil
}

func unknownJSONField(values map[string]json.RawMessage, model *Model) string {
	unknown := ""
	for name := range values {
		if modelHasField(model, name) {
			continue
		}
		if unknown == "" || name < unknown {
			unknown = name
		}
	}
	return unknown
}

func modelHasField(model *Model, name string) bool {
	if model.FieldByName(name) != nil {
		return true
	}
	for i := range model.Fields {
		if model.Fields[i].Name == name {
			return true
		}
	}
	return false
}

func jsonArenaLength(values map[string]json.RawMessage, fields []Field) (int, error) {
	length := 0
	for i := range fields {
		field := &fields[i]
		raw, present := values[field.Name]
		if !present || field.IsList || (field.Type != FieldString && field.Type != FieldEnum) || isJSONNull(raw) {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, fmt.Errorf("%s must be a string", field.Name)
		}
		length += len(value)
	}
	return length, nil
}

func (s *Schema) writeJSONField(enc *codec.Encoder, field *Field, raw json.RawMessage) error {
	enc.WriteFieldHeader(field.ID)
	if isJSONNull(raw) {
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
		return s.writeJSONList(enc, field, raw)
	}
	return s.writeJSONScalar(enc, field, raw)
}

func (s *Schema) writeJSONList(enc *codec.Encoder, field *Field, raw json.RawMessage) error {
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return fmt.Errorf("expected array: %w", err)
	}
	enc.WriteArrayHeader(len(values))
	element := *field
	element.IsList = false
	element.Nullable = false
	for i := range values {
		if isJSONNull(values[i]) {
			return fmt.Errorf("element %d must not be null", i)
		}
		if err := s.writeJSONScalar(enc, &element, values[i]); err != nil {
			return fmt.Errorf("element %d: %w", i, err)
		}
	}
	return nil
}

func (s *Schema) writeJSONScalar(enc *codec.Encoder, field *Field, raw json.RawMessage) error {
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
		return s.writeJSONModel(enc, field, raw)
	case FieldUUID:
		return writeJSONUUID(enc, raw)
	case FieldDecimal:
		return writeJSONDecimal(enc, raw)
	case FieldJSON:
		enc.WriteBytes(raw)
		return nil
	default:
		return fmt.Errorf("unsupported field type %s", field.Type)
	}
}

func writeJSONInt(enc *codec.Encoder, raw json.RawMessage) error {
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("expected integer: %w", err)
	}
	enc.WriteInt(value)
	return nil
}

func writeJSONFloat(enc *codec.Encoder, raw json.RawMessage) error {
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("expected number: %w", err)
	}
	enc.WriteFloat(value)
	return nil
}

func writeJSONString(enc *codec.Encoder, raw json.RawMessage) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("expected string: %w", err)
	}
	enc.WriteString(value)
	return nil
}

func writeJSONBool(enc *codec.Encoder, raw json.RawMessage) error {
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("expected boolean: %w", err)
	}
	enc.WriteBool(value)
	return nil
}

func writeJSONDateTime(enc *codec.Encoder, raw json.RawMessage) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("expected RFC3339 string: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return fmt.Errorf("expected RFC3339 string: %w", err)
	}
	enc.WriteInt(parsed.Unix())
	return nil
}

func writeJSONBytes(enc *codec.Encoder, raw json.RawMessage) error {
	var value []byte
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("expected base64 bytes: %w", err)
	}
	enc.WriteBytes(value)
	return nil
}

func (s *Schema) writeJSONEnum(enc *codec.Encoder, field *Field, raw json.RawMessage) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("expected enum string: %w", err)
	}
	if enum := s.Enums[field.TypeName]; enum != nil && !containsString(enum.Values, value) {
		return fmt.Errorf("unknown %s value %q", field.TypeName, value)
	}
	enc.WriteString(value)
	return nil
}

func (s *Schema) writeJSONModel(enc *codec.Encoder, field *Field, raw json.RawMessage) error {
	model := s.structuredModel(field.TypeName)
	if model == nil {
		return fmt.Errorf("unknown structured type %q", field.TypeName)
	}
	return s.writeJSONObject(enc, model, raw)
}

func writeJSONUUID(enc *codec.Encoder, raw json.RawMessage) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
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
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("expected decimal string: %w", err)
	}
	if _, err := decimal.NewFromString(value); err != nil {
		return fmt.Errorf("expected decimal string: %w", err)
	}
	enc.WriteString(value)
	return nil
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
