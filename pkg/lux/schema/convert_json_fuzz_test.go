package schema

import (
	"bytes"
	"encoding/json/jsontext"
	"testing"
)

func FuzzJSONToBinaryUnicode(f *testing.F) {
	for _, raw := range []string{`{}`, `{"name":"\uD800"}`, `{"address":{"city":"\uDC00"}}`,
		`{"metadata":{"\uD800":1}}`, `{"name":"\uD800","name":"valid"}`,
		`{"name":"\uD800\uDC00","metadata":{"n":123456789012345678901234567890}}`,
		`{"addresses":[{"city":"東京"}],"ids":[0,-1]}`, `{}true`, "{\"name\":\"\xff\"}"} {
		f.Add([]byte(raw))
	}
	s := newJSONEncodingSchema()
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 65536 {
			return
		}
		original := bytes.Clone(raw)
		got, err := s.JSONToBinary("Input", raw)
		if !bytes.Equal(raw, original) {
			t.Fatal("conversion mutated the input")
		}
		if err != nil {
			if got != nil {
				t.Fatal("conversion exposed partial output")
			}
			return
		}
		if !jsontext.Value(raw).IsValid(jsontext.AllowDuplicateNames(true)) {
			t.Fatalf("conversion accepted malformed JSON: %q", raw)
		}
		// Input models use inline children, not the response converter's blobs.
		// Generated ReadLuxoInput interoperability is covered in codegen tests.
		if len(got) < 2 {
			t.Fatal("conversion omitted the arena header or end marker")
		}
	})
}

func FuzzBinaryJSONConversion(f *testing.F) {
	for _, raw := range []string{"null", "-0", "1e9999", `{"n":123456789012345678901234567890}`, `{"n":1,"n":2}`, "{", "null true", "\"\xff\"",
		`"\uD800"`, `{"\uDC00":1}`, `"\uD800\uDC00"`} {
		f.Add([]byte(raw), false, false)
		f.Add([]byte(raw), true, true)
	}
	f.Fuzz(func(t *testing.T, raw []byte, list, nullable bool) {
		if len(raw) > 8192 {
			return
		}
		valid := jsontext.Value(raw).IsValid(jsontext.AllowDuplicateNames(true))
		wants := rawJSONExpected(string(raw), list)
		for name, convert := range rawJSONConverters(t, raw, list, nullable) {
			got, err := convert([]byte("prefix"))
			if !valid {
				if err == nil || string(got) != "prefix" {
					t.Fatalf("%s accepted invalid JSON: %q / %v", name, got, err)
				}
			} else if err != nil || string(got) != "prefix"+wants[name] {
				t.Fatalf("%s changed raw JSON: %q / %v", name, got, err)
			}
		}
	})
}
