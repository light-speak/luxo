// Package jsonutil provides JSON utility functions for the Luxo runtime.
// It wraps Go's encoding/json with a simpler string-oriented API.
package jsonutil

import (
	"encoding/json"
)

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
