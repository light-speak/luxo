package api

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"sync"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

// Metadata identifies the JSON category before reading it. In particular,
// DateTime must not pass through float64 and become a numeric Unix timestamp.
func decodeJSONTypedParam(raw []byte, meta ParamMeta) (any, bool, error) {
	var value any
	var err error
	switch meta.Type {
	case "String", "Enum", "DateTime", "UUID", "Decimal":
		if meta.IsList {
			value, err = jsonutil.ParseStringArrayLimit(raw, codec.MaxArrayElements)
		} else {
			value, err = jsonutil.ParseString(raw)
		}
	case "Boolean":
		if meta.IsList {
			value, err = decodeJSONBoolList(raw)
		} else {
			value, err = jsonutil.ParseBool(raw)
		}
	default:
		return nil, false, fmt.Errorf("param %s: unknown type %s", meta.Name, meta.Type)
	}
	if err != nil {
		return nil, false, fmt.Errorf("param %s: invalid %s: %w", meta.Name, meta.Type, err)
	}
	return value, true, nil
}

func decodeJSONBoolList(raw []byte) ([]any, error) {
	count := 0
	return jsonutil.ParseArray(raw, func(value []byte) (any, error) {
		if count == codec.MaxArrayElements {
			return nil, fmt.Errorf("array size exceeds limit %d", codec.MaxArrayElements)
		}
		count++
		parsed, err := jsonutil.ParseBool(value)
		return parsed, err
	})
}

func decodeJSONBytesParam(raw json.RawMessage, meta ParamMeta) (any, bool, error) {
	if !meta.IsList {
		value, err := jsonutil.ParseBase64(raw)
		if err != nil {
			return nil, false, fmt.Errorf("param %s: expected base64 bytes: %w", meta.Name, err)
		}
		return value, true, nil
	}
	values, err := jsonutil.ParseBase64Array(raw, codec.MaxArrayElements)
	if err != nil {
		return nil, false, fmt.Errorf("param %s: expected a list of base64 bytes: %w", meta.Name, err)
	}
	return values, true, nil
}

func decodeJSONFloatParam(raw []byte, meta ParamMeta) (any, error) {
	if !meta.IsList {
		value, err := jsonutil.ParseFloat64(raw)
		if err != nil {
			return nil, fmt.Errorf("param %s: %w", meta.Name, err)
		}
		return value, nil
	}
	raw = bytes.Trim(raw, " \t\r\n")
	if len(raw) < 2 || raw[0] != '[' || raw[len(raw)-1] != ']' {
		return nil, fmt.Errorf("param %s: expected a number array", meta.Name)
	}
	body := bytes.Trim(raw[1:len(raw)-1], " \t\r\n")
	if len(body) == 0 {
		return []any{}, nil
	}
	count := bytes.Count(body, []byte{','}) + 1
	if count > codec.MaxArrayElements {
		return nil, fmt.Errorf("param %s: array size exceeds limit %d", meta.Name, codec.MaxArrayElements)
	}
	values := make([]any, 0, count)
	for token := range bytes.SplitSeq(body, []byte{','}) {
		value, err := jsonutil.ParseFloat64(token)
		if err != nil {
			return nil, fmt.Errorf("param %s[%d]: %w", meta.Name, len(values), err)
		}
		values = append(values, value)
	}
	return values, nil
}

type rawJSONArrayDecoder struct {
	input   bytes.Buffer
	decoder jsontext.Decoder
}

var rawJSONArrayDecoders = sync.Pool{New: func() any { return new(rawJSONArrayDecoder) }}

func acquireRawJSONArrayDecoder(raw []byte) *rawJSONArrayDecoder {
	reader := rawJSONArrayDecoders.Get().(*rawJSONArrayDecoder)
	reader.input = *bytes.NewBuffer(raw)
	reader.decoder.Reset(&reader.input, jsontext.AllowDuplicateNames(true))
	return reader
}

func (reader *rawJSONArrayDecoder) reset() {
	// Reset both owners so the pool never keeps the request's backing storage.
	reader.input = bytes.Buffer{}
	reader.decoder.Reset(&reader.input)
}

func releaseRawJSONArrayDecoder(reader *rawJSONArrayDecoder) {
	reader.reset()
	rawJSONArrayDecoders.Put(reader)
}

// Preserve opaque JSON tokens while requiring valid Unicode in keys and values.
// No numeric conversion, reflection, or per-element JSON marshaling is needed.
func encodeRawJSONArray(enc *codec.Encoder, meta ParamMeta, raw []byte) error {
	reader := acquireRawJSONArrayDecoder(raw)
	defer releaseRawJSONArrayDecoder(reader)
	dec := &reader.decoder
	start, err := dec.ReadToken()
	if err != nil || start.Kind() != '[' {
		return fmt.Errorf("param %s: expected a JSON array", meta.Name)
	}
	var small [8][]byte
	values := small[:0]
	for dec.PeekKind() != ']' {
		value, err := dec.ReadValue()
		if err != nil {
			return fmt.Errorf("param %s[%d]: invalid JSON: %w", meta.Name, len(values), err)
		}
		if len(values) == codec.MaxArrayElements {
			return fmt.Errorf("param %s: array size exceeds limit %d", meta.Name, codec.MaxArrayElements)
		}
		// ReadValue is borrowed until the next decoder call. Retain a slice of
		// the immutable request instead, identified by the consumed byte range.
		end := int(dec.InputOffset())
		values = append(values, raw[end-len(value):end])
	}
	// PeekKind validated the closing delimiter. Only that delimiter and JSON
	// whitespace may remain; do not parse or ignore a second top-level value.
	if !bytes.Equal(bytes.Trim(raw[dec.InputOffset():], " \t\r\n"), []byte("]")) {
		return fmt.Errorf("param %s: unexpected data after JSON array", meta.Name)
	}
	enc.WriteArrayHeader(len(values))
	for _, value := range values {
		enc.WriteBytes(value)
	}
	return nil
}
