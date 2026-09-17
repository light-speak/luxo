package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/pkg/lux/jsonutil"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func parseBinaryCLIValue(raw string, param schema.Param) (any, error) {
	if param.Nullable && strings.TrimSpace(raw) == "null" {
		return nil, nil
	}
	if param.IsList {
		var items []json.RawMessage
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			return nil, fmt.Errorf("expected JSON array: %w", err)
		}
		if items == nil {
			return nil, fmt.Errorf("expected JSON array, got null")
		}
		values := make([]any, len(items))
		for i, item := range items {
			value, err := parseBinaryJSONValue(item, param.Type)
			if err != nil {
				return nil, fmt.Errorf("element %d: %w", i, err)
			}
			values[i] = value
		}
		return values, nil
	}
	if param.Type == schema.FieldJSON {
		value := json.RawMessage(raw)
		if !json.Valid(value) {
			return nil, fmt.Errorf("expected JSON value")
		}
		return value, nil
	}
	return parseBinaryTextValue(raw, param.Type)
}

// Array tokens are already validated by json.Unmarshal; preserve opaque JSON
// and only convert values whose schema explicitly requests a scalar type.
func parseBinaryJSONValue(raw json.RawMessage, fieldType schema.FieldType) (any, error) {
	if fieldType == schema.FieldJSON {
		return raw, nil
	}
	text := strings.TrimSpace(string(raw))
	if text == "null" {
		return nil, fmt.Errorf("expected %s, got null", fieldType)
	}
	switch fieldType {
	case schema.FieldInt, schema.FieldDuration:
		return strconv.ParseInt(text, 10, 64)
	case schema.FieldFloat:
		return jsonutil.ParseFloat64(raw)
	case schema.FieldBool:
		var value bool
		err := json.Unmarshal(raw, &value)
		return value, err
	default:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return parseBinaryTextValue(value, fieldType)
	}
}

func parseBinaryTextValue(raw string, fieldType schema.FieldType) (any, error) {
	switch fieldType {
	case schema.FieldInt, schema.FieldDuration:
		return strconv.ParseInt(raw, 10, 64)
	case schema.FieldFloat:
		return parseBinaryFloatText(raw)
	case schema.FieldBool:
		return strconv.ParseBool(raw)
	case schema.FieldBytes:
		return base64.StdEncoding.DecodeString(raw)
	case schema.FieldString, schema.FieldEnum, schema.FieldDateTime, schema.FieldUUID, schema.FieldDecimal:
		return raw, nil
	default:
		return nil, fmt.Errorf("unsupported CLI type %s", fieldType.String())
	}
}

// Keep the existing explicit CLI float syntax (including hex and special
// values); reject silent underflow without deciding the Binary Float domain.
func parseBinaryFloatText(raw string) (float64, error) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value != 0 {
		return value, err
	}
	mantissa := strings.TrimLeft(raw, "+-")
	exponent := "eE"
	if strings.HasPrefix(mantissa, "0x") || strings.HasPrefix(mantissa, "0X") {
		mantissa, exponent = mantissa[2:], "pP"
	}
	if index := strings.IndexAny(mantissa, exponent); index >= 0 {
		mantissa = mantissa[:index]
	}
	if strings.Trim(mantissa, "0._") != "" {
		return 0, fmt.Errorf("Float underflow: %s", raw)
	}
	return value, nil
}

func parseParam(s string) (string, string) {
	key, value, _ := strings.Cut(s, "=")
	return key, value
}

// JSON mode infers a complete JSON value, never a partially scanned number.
// Non-JSON text stays a string; users can quote numeric-looking strings.
func inferType(value string) any {
	token := strings.TrimLeft(value, " \t\r\n")
	if token == "" || !strings.ContainsRune(`{["tfn-0123456789`, rune(token[0])) {
		return value
	}
	raw := json.RawMessage(value)
	if json.Valid(raw) {
		return raw
	}
	return value
}
