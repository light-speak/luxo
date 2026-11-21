package api

import (
	"encoding/json"
	"testing"
)

func FuzzWSJSONSequence(f *testing.F) {
	for _, token := range []string{"0", "1", "9007199254740993", "18446744073709551615", "18446744073709551616", "", "null", "-0", "+1", "01", "1.0", "1e0", " 1", "1 "} {
		f.Add([]byte(token))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		var want uint64
		err := json.Unmarshal(raw, &want)
		// RawMessage has no surrounding whitespace; require an unsigned integer token.
		valid := err == nil && len(raw) > 0 && raw[0] >= '0' && raw[0] <= '9' && raw[len(raw)-1] >= '0' && raw[len(raw)-1] <= '9'
		got, ok := parseWSJSONSequence(raw)
		if ok != valid || (ok && got != want) {
			t.Fatalf("%q: got (%d,%v), want (%d,%v)", raw, got, ok, want, valid)
		}
	})
}
