package jsonutil

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"io"
	"strings"
	"testing"
)

func FuzzJSONContainerDepth(f *testing.F) {
	for _, raw := range []string{`null`, `"[{\\\"}]"`, `{"a":[],"a":{}}`, `"\uD800"`, "\"\xff\""} {
		f.Add([]byte(raw))
	}
	for _, depth := range []int{127, 128, 129, 130} {
		f.Add([]byte(strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth)))
		f.Add([]byte(strings.Repeat(`{"x":`, depth) + `"escaped\\\"[]"` + strings.Repeat("}", depth)))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 32768 {
			t.Skip()
		}
		want := jsontext.Value(raw).IsValid(jsontext.AllowDuplicateNames(true))
		if want {
			want = referenceJSONDepth(t, raw) <= 128
		}
		if got := ValidUTF8JSON(raw); got != want {
			t.Fatalf("valid=%v, want %v: %q", got, want, raw)
		}
	})
}

// Token streaming retains duplicate names and avoids rounding wide numbers.
// This oracle uses the independent standard-library parser, not our scanner.
func referenceJSONDepth(t *testing.T, raw []byte) int {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	depth, maximum := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return maximum
		}
		if err != nil {
			t.Fatal(err)
		}
		if delimiter, ok := token.(json.Delim); ok {
			if delimiter == '[' || delimiter == '{' {
				depth++
				maximum = max(maximum, depth)
			} else {
				depth--
			}
		}
	}
}
