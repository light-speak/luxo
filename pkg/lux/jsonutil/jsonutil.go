// Package jsonutil provides JSON utility functions for the Luxo runtime.
// It wraps Go's encoding/json with a simpler string-oriented API.
package jsonutil

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"strconv"
)

// ValidUTF8JSON checks one complete JSON value, including UTF-8 and paired
// surrogate escapes, without decoding numbers or changing duplicate-name
// semantics. Empty input is not JSON null.
func ValidUTF8JSON(raw []byte) bool {
	return jsontext.Value(raw).IsValid(jsontext.AllowDuplicateNames(true))
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
