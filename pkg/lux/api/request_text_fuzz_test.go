package api

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"testing"
	"unicode/utf8"
)

func FuzzJSONFilterMetadata(f *testing.F) {
	for _, raw := range []string{`{"field":"id","op":"eq","value":9223372036854775807}`, `{"value":1e9999}`,
		`{"value":"\uD800"}`, `{"field":"id","FIELD":null,"value":{},"VALUE":false}`, `{"value":""}`,
		`{"value":null}`, `{"value":"\u6771京"}`, `{"value":1,"unknown":"\uD800"}`, `{}`, "null", `{}true`} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 65536 {
			return
		}
		previous := Filter{"preserved", "eq", "value"}
		got := previous
		err := got.UnmarshalJSON(raw)
		valid := jsontext.Value(raw).IsValid(jsontext.AllowDuplicateNames(true))
		want, wantErr := formerJSONFilterMetadata(raw)
		if (err == nil) != (valid && wantErr == nil) || (err == nil && got != want) || (err != nil && got != previous) {
			t.Fatalf("filter acceptance/value changed: %q: %+v / %v vs %+v / %v", raw, got, err, want, wantErr)
		}
	})
}

func formerJSONFilterMetadata(data []byte) (Filter, error) {
	var raw struct {
		Field    string          `json:"field"`
		Operator string          `json:"op"`
		Value    json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Filter{}, err
	}
	value := bytes.TrimSpace(raw.Value)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) || value[0] == '{' || value[0] == '[' {
		return Filter{}, fmt.Errorf("invalid scalar")
	}
	decoded := string(value)
	if value[0] == '"' {
		if err := json.Unmarshal(value, &decoded); err != nil {
			return Filter{}, err
		}
	}
	return Filter{raw.Field, raw.Operator, decoded}, nil
}

func FuzzJSONRequestTextBoundary(f *testing.F) {
	for _, fixture := range jsonTextBoundaryFixtures() {
		f.Add([]byte(fixture.body))
	}
	for _, invalid := range invalidQuoteInputs() {
		f.Add([]byte(`{"$api":"text","value":"` + invalid + `"}`))
	}
	for _, body := range []string{"", `{`, `{"$api":"text","value":"\ufffd"}`, `{"$api":"text","raw":123456789012345678901234567890}`,
		`{"$api":"text","value":"\uD800"}`, `{"$api":"text","\uDC00":null}`, `{"\u0024api":"text","value":"\uD800\uDC00"}`,
		`{"$api":"text","raw":{"x":"\uD800","x":"valid"}}`} {
		f.Add([]byte(body))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > MaxJSONBodySize {
			return
		}
		got, err := ParseRequest(makeReq(string(data)))
		if !validRequestTextOracle(data) {
			if got != nil || err != errInvalidJSONUTF8 {
				t.Fatalf("invalid UTF-8 did not fail at the boundary: %v", err)
			}
			return
		}
		before, oldErr := parseRequestBeforeText(makeReq(string(data)))
		if !jsontext.Value(data).IsValid(jsontext.AllowDuplicateNames(true)) {
			if got != nil || err == nil {
				t.Fatal("accepted malformed JSON or Unicode")
			}
			return
		}
		if (err == nil) != (oldErr == nil) {
			t.Fatalf("valid UTF-8 syntax handling changed: %v / %v", err, oldErr)
		}
		if err == nil {
			assertTextRequestParity(t, before, got)
		}
	})
}

// DecodeRune reports malformed single bytes separately from a valid U+FFFD.
func validRequestTextOracle(data []byte) bool {
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			return false
		}
		data = data[size:]
	}
	return true
}

func assertTextRequestParity(t *testing.T, before, got *Request) {
	t.Helper()
	if got.API != before.API || got.Page != before.Page || got.PageSize != before.PageSize || len(got.Params) != len(before.Params) {
		t.Fatal("request envelope changed")
	}
	for name, value := range before.Params {
		if !bytes.Equal(got.Params[name], value) {
			t.Fatalf("parameter %q changed", name)
		}
	}
}
