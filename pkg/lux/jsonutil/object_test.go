package jsonutil

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"reflect"
	"strings"
	"testing"
)

func TestTextBufferOwnsResults(t *testing.T) {
	var text TextBuffer
	input := []byte("東京")
	first := text.Copy(input)
	clear(input)
	reader := BorrowReader([]byte(`"\u6771京"`))
	second, err := text.ReadString(reader)
	reader.Release()
	if err != nil {
		t.Fatal(err)
	}
	for range 100 {
		text.Copy([]byte("replace previous block"))
	}
	if first != "東京" || second != first {
		t.Fatal("text storage reused a borrowed input or mutated previous results")
	}
	reader = BorrowReader([]byte("null"))
	defer reader.Release()
	if _, err := text.ReadString(reader); err == nil {
		t.Fatal("text buffer accepted non-string JSON")
	}
}

func TestTextBufferCapacityRemainsBounded(t *testing.T) {
	for _, hint := range []int{-1, 0, 16, 64, 256, 1000000} {
		text := NewTextBuffer(hint)
		value := text.Copy([]byte("first"))
		for range 100 {
			text.Copy([]byte("additional owned text"))
			if text.text.Len() > ownedTextBlockSize {
				t.Fatalf("text block exceeded its content bound: %d", text.text.Len())
			}
		}
		if value != "first" || text.initialCapacity < 16 || text.initialCapacity > ownedTextBlockSize {
			t.Fatal("capacity hint changed ownership or escaped its bounds")
		}
	}
}

func TestParseObjectPreservesOwnedRawValues(t *testing.T) {
	raw := []byte(` { "n" : 0, "\u006e" : 123456789012345678901234567890, "text":"\uD800\uDC00", "x": [null,-0,1e9999] } `)
	got, err := ParseObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	clear(raw)
	for range 20 {
		if _, err := ParseObject([]byte(`{"other":{"nested":"value"}}`)); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 3 || string(got["n"]) != "123456789012345678901234567890" ||
		string(got["text"]) != `"\uD800\uDC00"` || string(got["x"]) != `[null,-0,1e9999]` {
		t.Fatalf("raw object changed: %s", got)
	}
	got["x"][1] = ' '
	if string(got["text"]) != `"\uD800\uDC00"` {
		t.Fatal("unrelated values share mutable bytes")
	}
}

func TestParseObjectRejectsMalformedAndNonObjects(t *testing.T) {
	for _, raw := range []string{"", "null", "[]", "true", `"text"`, "1", "{", `{"x"}`, `{"x":}`, `{"x":0,}`, `{}true`, `{} {}`, "{}!",
		`{"\uD800":1}`, `{"x":["\uDC00"]}`, `{"x":"\uD800","x":"valid"}`, "{\"x\":\"\xff\"}"} {
		if got, err := ParseObject([]byte(raw)); err == nil || got != nil {
			t.Errorf("accepted invalid object %q: %v", raw, err)
		}
	}
	if got, err := ParseObject([]byte(" \n {} \t")); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty object: %v / %v", got, err)
	}
}

func TestParseObjectDoesNotIncreaseSmallObjectAllocations(t *testing.T) {
	for _, raw := range [][]byte{[]byte(`{"id":42,"name":"text"}`), []byte(`{"$id":1,"$api":"text","value":"hello"}`),
		[]byte(`{"` + strings.Repeat("field", 40) + `":1}`)} {
		before := testing.AllocsPerRun(100, func() { _, _ = parseObjectBeforeUnicode(raw) })
		after := testing.AllocsPerRun(100, func() { _, _ = ParseObject(raw) })
		if after > before {
			t.Errorf("small object allocations increased: %g -> %g", before, after)
		}
	}
}

func TestParseObjectKeysSurviveStorageGrowth(t *testing.T) {
	longKey := strings.Repeat("field", 80)
	raw := []byte(`{"":0,"first":1,"\u0066irst":2,"` + longKey + `":3,"\uD800\uDC00` + longKey + `":4,"last":5}`)
	got, err := ParseObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	clear(raw)
	for key, value := range map[string]string{"": "0", "first": "2", longKey: "3", "\U00010000" + longKey: "4", "last": "5"} {
		if string(got[key]) != value {
			t.Fatalf("key %q: got %q, want %q", key, got[key], value)
		}
	}
}

func TestObjectKeyStorageIsBoundedAndOwned(t *testing.T) {
	var names strings.Builder
	first := storeOwnedText(&names, []byte("first"), 16)
	for range 1000 {
		if got := storeOwnedText(&names, []byte(strings.Repeat("x", 17)), 16); got != strings.Repeat("x", 17) {
			t.Fatal("stored key changed")
		}
		if names.Len() > ownedTextBlockSize {
			t.Fatal("key block grew beyond its limit")
		}
	}
	if first != "first" {
		t.Fatal("reset mutated a prior key")
	}
}

func TestOwnedTextReusesImmutableBlock(t *testing.T) {
	var block strings.Builder
	raw := []byte("prefix東京")
	first := storeOwnedText(&block, raw, 16)
	clear(raw)
	for _, want := range []string{"prefix東京", "prefix東京", ""} {
		length := block.Len()
		if got := storeOwnedText(&block, []byte(want), 16); got != want || block.Len() != length {
			t.Fatalf("reused text %q: %q, stored length %d -> %d", want, got, length, block.Len())
		}
	}
	text := []byte("prefix東京")
	if count := testing.AllocsPerRun(1000, func() {
		if got := storeOwnedText(&block, text, 16); got != "prefix東京" {
			t.Fatal("reused text changed")
		}
	}); count != 0 {
		t.Fatalf("repeated owned block allocated %v times", count)
	}
	for range 100 {
		storeOwnedText(&block, []byte(strings.Repeat("x", 17)), 16)
		storeOwnedText(&block, []byte("different"), 16)
	}
	if first != "prefix東京" {
		t.Fatal("subsequent writes or resets changed owned text")
	}
}

func TestReadOwnedStringRejectsMalformedInput(t *testing.T) {
	var names strings.Builder
	for _, raw := range []string{`"\uD800"`, `"\q"`, `null`, `"unterminated`} {
		// Unquoting errors propagate even when the reader permits bad escapes.
		reader := BorrowReader([]byte(raw), jsontext.AllowInvalidUTF8(true))
		_, err := readOwnedString(reader, &names, 16)
		reader.Release()
		if err == nil {
			t.Fatalf("accepted invalid name %q", raw)
		}
		if names.Len() != 0 {
			t.Fatal("failed name changed owned storage")
		}
	}
}

func TestJSONReaderClearsInputAndState(t *testing.T) {
	for _, raw := range []string{
		`{"secret":"` + strings.Repeat("x", 65536) + `"}`,
		`{"nested":` + strings.Repeat("[", 100) + `0` + strings.Repeat("]", 100) + `}`,
		`{"secret":`,
	} {
		reader := BorrowReader([]byte(raw))
		_, _ = reader.ReadValue()
		reader.clear()
		if !reflect.ValueOf(*reader).IsZero() {
			t.Fatal("reader retained its input or parsing state")
		}
		reader.Release()
	}
}

func FuzzParseObject(f *testing.F) {
	for _, raw := range []string{`{}`, `{"x":1,"x":2}`, `{"n":1e9999}`, `{"\u006e":"\uD800\uDC00"}`, `{"x":"\uD800"}`, "{", "null", `[]`, "{\"x\":\"\xff\"}"} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 65536 {
			return
		}
		var want map[string]json.RawMessage
		valid := jsontext.Value(raw).IsValid(jsontext.AllowDuplicateNames(true)) && json.Unmarshal(raw, &want) == nil && want != nil
		original := bytes.Clone(raw)
		got, err := ParseObject(raw)
		if !bytes.Equal(original, raw) || (err == nil) != valid || (!valid && got != nil) {
			t.Fatalf("acceptance/ownership mismatch for %q: %v", raw, err)
		}
		if len(got) != len(want) && valid {
			t.Fatal("object length changed")
		}
		if valid {
			for key, value := range want {
				if !bytes.Equal(got[key], value) {
					t.Fatalf("value %q changed: %q / %q", key, got[key], value)
				}
			}
		}
	})
}
