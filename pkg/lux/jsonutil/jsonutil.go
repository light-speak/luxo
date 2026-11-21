// Package jsonutil provides JSON utility functions for the Luxo runtime.
// It wraps Go's encoding/json with a simpler string-oriented API.
package jsonutil

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"strconv"
	"strings"
)

// MaxJSONDepth counts object/array containers from an independent JSON root.
const MaxJSONDepth = 128

var errJSONDepth = fmt.Errorf("JSON nesting exceeds %d levels", MaxJSONDepth)

// CheckJSONDepth checks only a previously validated value's container budget.
// Nil represents an absent previous value. It neither copies nor decodes data.
func CheckJSONDepth(raw []byte) error {
	if len(raw) >= 2*(MaxJSONDepth+1) && !WithinJSONDepth(raw) {
		return errJSONDepth
	}
	return nil
}

// CloneJSONValue owns a JSON field after the enclosing strict reader validated
// syntax and Unicode. Its independent container budget does not include that
// enclosing model, list, or protocol envelope.
func CloneJSONValue(raw []byte) (json.RawMessage, error) {
	if WithinJSONDepth(raw) {
		return bytes.Clone(raw), nil
	}
	return nil, errJSONDepth
}

// ValidUTF8JSON checks one complete JSON value, including UTF-8 and paired
// surrogate escapes, without decoding numbers or changing duplicate-name
// semantics. Empty input is not JSON null. Containers are limited to MaxJSONDepth.
func ValidUTF8JSON(raw []byte) bool {
	return WithinJSONDepth(raw) && jsontext.Value(raw).IsValid(jsontext.AllowDuplicateNames(true))
}

// WithinJSONDepth checks only the container budget; callers must also validate
// syntax and Unicode. It is useful after jsontext has already read a value.
// Scalars consume no depth, and container-like text inside strings is ignored.
func WithinJSONDepth(raw []byte) bool {
	// Every valid container needs two delimiters. Ordinary small values need no
	// extra scan; deeply malformed input is still rejected by the syntax reader.
	if len(raw) < 2*(MaxJSONDepth+1) {
		return true
	}
	// A syntactically valid string root has no containers, regardless of length.
	if raw[0] == '"' {
		return true
	}
	// Raw openings overestimate actual containers because quoted text may also
	// contain brackets. Each real closing reduces the maximum possible depth.
	potential := bytes.Count(raw, []byte{'['}) + bytes.Count(raw, []byte{'{'})
	if potential <= MaxJSONDepth {
		return true
	}
	depth := 0
	for index := 0; index < len(raw); index++ {
		switch raw[index] {
		case '"':
			index = jsonStringEnd(raw, index)
		case '[', '{':
			depth++
			if depth > MaxJSONDepth {
				return false
			}
		case ']', '}':
			depth--
			potential--
			if potential <= MaxJSONDepth {
				return true
			}
		}
	}
	return true
}

func jsonStringEnd(raw []byte, start int) int {
	for index := start + 1; index < len(raw); {
		next := bytes.IndexByte(raw[index:], '"')
		if next < 0 {
			return len(raw)
		}
		end := index + next
		escapes := 0
		for back := end - 1; back > start && raw[back] == '\\'; back-- {
			escapes++
		}
		if escapes%2 == 0 {
			return end
		}
		index = end + 1
	}
	return len(raw)
}

// ParseFloat64 converts one JSON numeric token, rejecting overflow and nonzero
// underflow while preserving signed zero. It never reflects over a destination.
func ParseFloat64(raw []byte) (float64, error) {
	raw = bytes.Trim(raw, " \t\r\n")
	if len(raw) == 0 || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) || !json.Valid(raw) {
		return 0, fmt.Errorf("expected a JSON number")
	}
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil {
		return 0, err
	}
	if value == 0 && nonzeroMantissa(raw) {
		return 0, fmt.Errorf("JSON number underflows float64")
	}
	return value, nil
}

// ParseFloat64Text preserves existing string ownership during numeric parsing.
// The byte entry remains separate so byte callers do not copy before validation.
func ParseFloat64Text(raw string) (float64, error) {
	raw = strings.Trim(raw, " \t\r\n")
	if !validNumberText(raw) {
		return 0, fmt.Errorf("expected a JSON number")
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, err
	}
	if value == 0 && nonzeroMantissa([]byte(raw)) {
		return 0, fmt.Errorf("JSON number underflows float64")
	}
	return value, nil
}

// Validate just the numeric token without borrowing a general JSON decoder or
// copying the string into a decoder-owned byte buffer. Rounding stays in strconv.
func validNumberText(raw string) bool {
	i := 0
	if len(raw) > 0 && raw[0] == '-' {
		i++
	}
	if i == len(raw) {
		return false
	}
	if raw[i] == '0' {
		i++
	} else {
		if raw[i] < '1' || raw[i] > '9' {
			return false
		}
		i = numberTextDigits(raw, i)
	}
	if i < len(raw) && raw[i] == '.' {
		i++
		start := i
		i = numberTextDigits(raw, i)
		if i == start {
			return false
		}
	}
	if i < len(raw) && (raw[i] == 'e' || raw[i] == 'E') {
		i++
		if i < len(raw) && (raw[i] == '+' || raw[i] == '-') {
			i++
		}
		start := i
		i = numberTextDigits(raw, i)
		if i == start {
			return false
		}
	}
	return i == len(raw)
}

func numberTextDigits(raw string, i int) int {
	for i < len(raw) && raw[i] >= '0' && raw[i] <= '9' {
		i++
	}
	return i
}

func nonzeroMantissa(raw []byte) bool {
	for _, c := range raw {
		if c == 'e' || c == 'E' {
			return false
		}
		if c >= '1' && c <= '9' {
			return true
		}
	}
	return false
}

// Parse unmarshals a JSON string into the value pointed to by v.
func Parse(data string, v any) error {
	return json.Unmarshal([]byte(data), v)
}

// ParseTo unmarshals a JSON string into a value of type T and returns it.
func ParseTo[T any](data string) (T, error) {
	var v T
	err := json.Unmarshal([]byte(data), &v)
	return v, err
}

// Stringify marshals v into a compact JSON string.
func Stringify(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// PrettyPrint marshals v into an indented JSON string with 2-space indentation.
func PrettyPrint(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
