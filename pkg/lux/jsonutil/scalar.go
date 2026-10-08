package jsonutil

import (
	"bytes"
	"encoding/base64"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/light-speak/luxo/pkg/lux/datetime"
)

var (
	errExpectedInteger = errors.New("expected a signed 64-bit JSON integer")
	errExpectedBoolean = errors.New("expected JSON boolean")
	errExpectedString  = errors.New("expected JSON string")
	errArrayLimit      = errors.New("array size exceeds limit")
	errExpectedBase64  = errors.New("expected a base64 JSON string")
)

// ParseInt64 reads one non-null JSON integer without reflection or rounding.
func ParseInt64(raw []byte) (int64, error) {
	// JSON tokens normally have no boundary whitespace. Only the slow case
	// needs Trim; control characters not in JSON whitespace still fail below.
	if len(raw) > 0 && (raw[0] <= ' ' || raw[len(raw)-1] <= ' ') {
		raw = bytes.Trim(raw, " \t\r\n")
	}
	digits := raw
	if len(digits) > 0 && digits[0] == '-' {
		digits = digits[1:]
	}
	if len(digits) == 0 || digits[0] < '0' || digits[0] > '9' || (digits[0] == '0' && len(digits) != 1) {
		return 0, errExpectedInteger
	}
	return strconv.ParseInt(string(raw), 10, 64)
}

// ParseBool reads one non-null JSON boolean without reflection or coercion.
func ParseBool(raw []byte) (bool, error) {
	switch string(bytes.Trim(raw, " \t\r\n")) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errExpectedBoolean
	}
}

// ParseString reads a complete strict JSON string into independently owned text.
// It never replaces invalid UTF-8 or unpaired Unicode escapes with U+FFFD.
func ParseString(raw []byte) (string, error) {
	raw = bytes.Trim(raw, " \t\r\n")
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return "", errExpectedString
	}
	if bytes.IndexByte(raw, '\\') < 0 {
		if !jsontext.Value(raw).IsValid() {
			return "", errExpectedString
		}
		return string(raw[1 : len(raw)-1]), nil
	}
	var scratch [128]byte
	value, err := jsontext.AppendUnquote(scratch[:0], raw)
	if err != nil {
		return "", err
	}
	return string(value), nil
}

// ParseBase64 reads strict JSON text directly into independently owned bytes.
// Numeric arrays and null cannot stand in for Base64 strings.
func ParseBase64(raw []byte) ([]byte, error) {
	return parseBase64(raw, false)
}

// ParseBase64Array checks the size before storing each decoded value. Empty
// arrays and empty byte strings remain non-nil; errors return no partial list.
func ParseBase64Array(raw []byte, limit int) ([][]byte, error) {
	count := 0
	return ParseArray(raw, func(value []byte) ([]byte, error) {
		if count >= limit {
			return nil, errArrayLimit
		}
		count++
		return parseBase64(value, true)
	})
}

// validated is true only for a complete value from the strict array reader.
func parseBase64(raw []byte, validated bool) ([]byte, error) {
	raw = bytes.Trim(raw, " \t\r\n")
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return nil, errExpectedBase64
	}
	text := raw[1 : len(raw)-1]
	if bytes.IndexByte(text, '\\') >= 0 {
		var scratch [128]byte
		unquoted, err := jsontext.AppendUnquote(scratch[:0], raw)
		if err != nil {
			return nil, err
		}
		text = unquoted
	} else if !validated && !jsontext.Value(raw).IsValid() {
		return nil, errExpectedBase64
	}
	value := make([]byte, base64.StdEncoding.DecodedLen(len(text)))
	n, err := base64.StdEncoding.Decode(value, text)
	if err != nil {
		return nil, err
	}
	return value[:n], nil
}

// ParseDateTime avoids allocating an intermediate owned string for time.Parse.
func ParseDateTime(raw []byte) (time.Time, error) {
	var scratch [128]byte
	value, err := jsontext.AppendUnquote(scratch[:0], bytes.Trim(raw, " \t\r\n"))
	if err != nil {
		return time.Time{}, err
	}
	return datetime.ParseRFC3339(string(value))
}

// ParseInt64Array uses the token reader's exact signed-integer conversion.
func ParseInt64Array(raw []byte) ([]int64, error) {
	return parseArrayReader(raw, func(reader *Reader) (int64, error) {
		token, err := reader.ReadToken()
		if err != nil {
			return 0, err
		}
		if token.Kind() != '0' {
			return 0, errExpectedInteger
		}
		return token.Int()
	})
}

// ParseFloat64ArrayLimit uses the already validated numeric token instead of
// running a second JSON syntax scan for every Float. Underflow remains invalid.
func ParseFloat64ArrayLimit(raw []byte, limit int) ([]float64, error) {
	count := 0
	return parseArrayReader(raw, func(reader *Reader) (float64, error) {
		if count >= limit {
			return 0, errArrayLimit
		}
		count++
		start := reader.InputOffset()
		token, err := reader.ReadToken()
		if err != nil {
			return 0, err
		}
		if token.Kind() != '0' {
			return 0, fmt.Errorf("expected a JSON number")
		}
		value, err := token.Float()
		if err == nil && value == 0 && nonzeroMantissa(raw[start:reader.InputOffset()]) {
			return 0, fmt.Errorf("JSON number underflows float64")
		}
		return value, err
	})
}

// ParseStringArray packs short strings into bounded, independently owned blocks.
func ParseStringArray(raw []byte) ([]string, error) {
	var text strings.Builder
	return parseArrayReader(raw, func(reader *Reader) (string, error) {
		return readOwnedString(reader, &text, min(len(raw), ownedTextBlockSize))
	})
}

// ParseStringArrayLimit rejects an excess item before storing it. The bounded
// parser retains the same strict validation and independently owned text blocks.
func ParseStringArrayLimit(raw []byte, limit int) ([]string, error) {
	return ParseStringValuesLimit[string](raw, limit)
}

// ParseStringValuesLimit decodes directly into a named string slice, without an
// intermediate []string. Schema-specific membership is checked by generated code.
func ParseStringValuesLimit[T ~string](raw []byte, limit int) ([]T, error) {
	var text strings.Builder
	count := 0
	return parseArrayReader(raw, func(reader *Reader) (T, error) {
		if count >= limit {
			return "", errArrayLimit
		}
		count++
		value, err := readOwnedString(reader, &text, min(len(raw), ownedTextBlockSize))
		return T(value), err
	})
}
