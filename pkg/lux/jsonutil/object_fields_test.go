package jsonutil

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestVisitUniqueObject(t *testing.T) {
	for _, raw := range []string{`{"id":1,"id":2}`, `{"id":1,"\u0069d":2}`, `{"nested":{"id":1,"id":2}}`} {
		if err := VisitUniqueObject([]byte(raw), func(_, _ []byte) error { return nil }); err == nil {
			t.Errorf("accepted duplicate fields: %s", raw)
		}
	}
	var got string
	if err := VisitUniqueObject([]byte(`{"id":9007199254740993}`), func(_, value []byte) error { got = string(value); return nil }); err != nil || got != "9007199254740993" {
		t.Fatalf("valid unique object: %s %v", got, err)
	}
	if err := VisitUniqueObject([]byte(`{}`), nil); err == nil {
		t.Fatal("nil visitor accepted")
	}
}

func TestVisitObjectFields(t *testing.T) {
	raw := []byte(` { "id": "overridden", "\u0069d": 9223372036854775807, "data": {"n":123456789012345678901234567890}, "items": [] } `)
	var slots [3][]byte
	calls := 0
	err := VisitObject(raw, func(name, value []byte) error {
		calls++
		switch string(name) {
		case "id":
			slots[0] = value
		case "data":
			slots[1] = value
		case "items":
			slots[2] = value
		default:
			t.Fatalf("unexpected name: %q", name)
		}
		return nil
	})
	if err != nil || calls != 4 {
		t.Fatalf("collect fields: calls=%d err=%v", calls, err)
	}
	for i, want := range []string{"9223372036854775807", `{"n":123456789012345678901234567890}`, "[]"} {
		if string(slots[i]) != want {
			t.Fatalf("slot %d = %q, want %s", i, slots[i], want)
		}
	}
	// Values must refer to the immutable caller input, never pooled decoder data.
	for range 10 {
		if _, err := ParseObject([]byte(`{"key":"different content"}`)); err != nil {
			t.Fatal(err)
		}
	}
	if string(slots[0]) != "9223372036854775807" {
		t.Fatal("pool reuse changed retained field")
	}
	index := bytes.Index(raw, slots[0])
	raw[index] = '8'
	if slots[0][0] != '8' {
		t.Fatal("field copied instead of borrowing caller input")
	}
}

func TestVisitObjectNames(t *testing.T) {
	long := strings.Repeat("x", 4096)
	for _, raw := range []string{`{}`, " \n{\t }\r ", `{"":null}`, `{"東京":1,"line\nkey":2,"` + long + `\u0061":3}`} {
		want, err := ParseObject([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		got := make(map[string]string)
		if err := VisitObject([]byte(raw), func(name, value []byte) error {
			got[string(name)] = string(value)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("field count: %d != %d", len(got), len(want))
		}
		for name, value := range want {
			if got[name] != string(value) {
				t.Fatalf("field %q: %q != %q", name, got[name], value)
			}
		}
	}
}

func TestVisitObjectRejectsMalformed(t *testing.T) {
	for _, raw := range []string{
		"", "null", "[]", "true", "42", `"object"`, "{", `{"id"}`, `{"id":}`, `{"id":1,}`,
		`{"id":1} {}`, `{"id":1}garbage`, `{"id":01}`, `{"id":NaN}`,
		`{"\uD800":1}`, `{"id":"\uD800","id":42}`, `{"id":{"\uDFFF":1},"id":42}`,
		"{\"id\":\"\xff\",\"id\":42}", "{\"\xff\":1}",
	} {
		if err := VisitObject([]byte(raw), func(_, _ []byte) error { return nil }); err == nil {
			t.Errorf("accepted malformed object: %q", raw)
		}
	}
	if err := VisitObject([]byte(`{}`), nil); err == nil {
		t.Fatal("accepted nil visitor")
	}
}

func TestVisitObjectStopsOnVisitorError(t *testing.T) {
	want := errors.New("unknown field")
	calls := 0
	err := VisitObject([]byte(`{"unknown":1,"later":2}`), func(_, _ []byte) error {
		calls++
		return want
	})
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("visitor error: %v, calls=%d", err, calls)
	}
}

func FuzzVisitObject(f *testing.F) {
	for _, raw := range []string{`{}`, `{"x":1,"\u0078":2}`, `{"key":{"n":12345678901234567890}}`, `{"x":"\uD800"}`, `{"a":[]}tail`} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		want, wantErr := ParseObject(raw)
		got := make(map[string][]byte)
		err := VisitObject(raw, func(name, value []byte) error {
			got[string(name)] = bytes.Clone(value)
			return nil
		})
		if (err == nil) != (wantErr == nil) {
			t.Fatalf("parser disagreement: %q: %v / %v", raw, err, wantErr)
		}
		if err != nil {
			return
		}
		if len(got) != len(want) {
			t.Fatalf("different field count: %q", raw)
		}
		for name, value := range want {
			if !bytes.Equal(value, got[name]) {
				t.Fatalf("different final value for %q: %q", name, raw)
			}
		}
	})
}
