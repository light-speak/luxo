package api

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/auth"
)

func FuzzIdentityIntegerValidity(f *testing.F) {
	for _, raw := range []string{"0", "-1", "9223372036854775807", "-9223372036854775808", "9223372036854775808", "1.0", "1e0", `"0"`, "null", "false", "[]", "{}"} {
		f.Add(raw, false)
		f.Add(raw, true)
	}
	cfg := &auth.Config{Secret: "identity-integer-validity", Expires: time.Hour}
	f.Fuzz(func(t *testing.T, raw string, pointer bool) {
		// A single JSON value prevents fuzz input from injecting sibling claims
		// into the test envelope; malformed envelopes are tested by auth itself.
		if len(raw) > 4096 || !json.Valid([]byte(raw)) {
			return
		}
		want, parseErr := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		valid := parseErr == nil
		token, err := auth.Sign(cfg, func(dst []byte) ([]byte, error) {
			return append(dst, `{"value":`+raw+`,"known":7}`...), nil
		})
		if err != nil {
			if valid {
				t.Fatalf("valid integer rejected: %q: %v", raw, err)
			}
			return
		}
		claims, err := auth.Verify(cfg, token)
		if err != nil {
			t.Fatal(err)
		}
		var identity any = claims
		if pointer {
			identity = &claims
		}
		reader := NewIdentityReader(identity)
		got := reader.Int("value")
		if reader.Allowed(true) != valid || valid && got != want {
			t.Fatalf("%q: got=%d allowed=%v want=%d valid=%v", raw, got, reader.Allowed(true), want, valid)
		}
		if reader.Allowed(reader.Int("known") == 7) != valid {
			t.Fatal("valid read erased prior failure")
		}
		reader.Int("missing")
		if reader.Allowed(!false) {
			t.Fatal("negation authorized a missing field")
		}
		reader = NewIdentityReader(identity)
		if !reader.Allowed(reader.Int("known") == 7) {
			t.Fatal("new evaluation retained old failure")
		}
	})
}
