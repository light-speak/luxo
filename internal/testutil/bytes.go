// Package testutil contains assertions shared by internal test fixtures.
package testutil

import "testing"

// Bytes checks a fixture encoder before its output is passed to another reader.
// The returned function accepts the encoder's two-value result directly.
func Bytes(t testing.TB) func([]byte, error) []byte {
	t.Helper()
	return func(data []byte, err error) []byte {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
}
