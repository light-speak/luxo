package jsonutil

import (
	"bytes"
	"encoding/base64"
	"encoding/json/jsontext"
	"errors"
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
	var text strings.Builder
	count := 0
	return parseArrayReader(raw, func(reader *Reader) (string, error) {
		if count >= limit {
			return "", errArrayLimit
		}
		count++
		return readOwnedString(reader, &text, min(len(raw), ownedTextBlockSize))
	})
}
