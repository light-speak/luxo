package jsonutil

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseRawArray(t *testing.T) {
	input := []byte(" \n[null, 123456789012345678901234567890, {\"n\":0.123456789012345678901}, \"\\u6771京\"] \t")
	want := []string{"null", "123456789012345678901234567890", `{"n":0.123456789012345678901}`, `"\u6771京"`}
	values, err := ParseRawArray(input, 4)
	if err != nil || len(values) != len(want) {
		t.Fatalf("raw array: %v", err)
	}
	for range 3 {
		if _, err := ParseRawArray([]byte(`["overwrite"]`), 1); err != nil {
			t.Fatal(err)
		}
	}
	for i := range values {
		if string(values[i]) != want[i] {
			t.Fatalf("raw token changed at %d: %q", i, values[i])
		}
	}
	if values, err := ParseRawArray([]byte("[]"), 0); err != nil || values == nil || len(values) != 0 {
		t.Fatalf("empty raw array: %v / %v", values, err)
	}
	for _, raw := range []string{"", "null", "{}", "[", "[1,]", "[]true", `["\uD800"]`, "[0,1]"} {
		if values, err := ParseRawArray([]byte(raw), 1); err == nil || values != nil {
			t.Errorf("invalid or oversized raw array accepted: %q", raw)
		}
	}
}

func TestParseBase64(t *testing.T) {
	for _, raw := range []string{`"AQI="`, `"\u0041QI="`, " \n\"AQI=\"\t", `"A\r\nQI="`} {
		input := []byte(raw)
		value, err := ParseBase64(input)
		clear(input)
		if err != nil || !bytes.Equal(value, []byte{1, 2}) {
			t.Errorf("%s: %x / %v", raw, value, err)
		}
	}
	if value, err := ParseBase64([]byte(`""`)); err != nil || value == nil || len(value) != 0 {
		t.Fatalf("empty bytes: %x / %v", value, err)
	}
	for _, raw := range []string{"", "null", "[]", "[1,2]", `"@@@"`, `"AQI="true`, `"\uD800"`, "\"\xff\"", "\"A\nQI=\"", `"a"b"`} {
		if value, err := ParseBase64([]byte(raw)); err == nil || value != nil {
			t.Errorf("accepted invalid bytes: %s / %x", raw, value)
		}
	}
}

func TestParseBase64Array(t *testing.T) {
	input := []byte(`["AQI=","\u0041QI=","", "AQI="]`)
	values, err := ParseBase64Array(input, 4)
	clear(input)
	if err != nil || len(values) != 4 || !bytes.Equal(values[0], []byte{1, 2}) || !bytes.Equal(values[1], values[0]) || len(values[2]) != 0 {
		t.Fatalf("base64 array: %x / %v", values, err)
	}
	values[0][0] = 3
	if values[1][0] != 1 || values[3][0] != 1 {
		t.Fatal("base64 values share mutable storage")
	}
	if values, err := ParseBase64Array([]byte("[]"), 0); err != nil || values == nil || len(values) != 0 {
		t.Fatalf("empty base64 array: %x / %v", values, err)
	}
	for _, raw := range []string{"[", "[null]", `[[1]]`, `["AQI=","AQI="]`, `["AQI=","@@@"]`, `["AQI=","\uD800"]`} {
		if values, err := ParseBase64Array([]byte(raw), 1); err == nil || values != nil {
			t.Errorf("accepted invalid base64 array: %s / %x", raw, values)
		}
	}
}

func TestParseRawArrayBorrowsOnlyInput(t *testing.T) {
	input := []byte(`[0, {"name":"` + strings.Repeat("東京", 12000) + `"}, 9223372036854775807]`)
	original := bytes.Clone(input)
	values, err := ParseRawArray(input, 3)
	if err != nil || len(values) != 3 || !bytes.Equal(input, original) {
		t.Fatalf("raw array changed input or failed: %v", err)
	}
	input[1] = '9'
	if string(values[0]) != "9" {
		t.Fatal("raw value did not borrow the caller's input")
	}
	for range 3 {
		_, _ = ParseRawArray(bytes.Repeat([]byte{' '}, len(input)), 3)
	}
	if string(values[2]) != "9223372036854775807" || !bytes.Equal(values[1], original[4:len(original)-22]) {
		t.Fatal("raw values borrowed the reusable reader buffer")
	}
}

func FuzzRawJSONArray(f *testing.F) {
	for _, raw := range []string{`[]`, `[null,true,-0]`, `[123456789012345678901234567890]`,
		`[{"n":0.12345678901234567890},"\u6771京"]`, `["\uD800"]`, `[1,]`, `[]true`,
		"[\"\xff\"]", `[{"a":1,"a":2}]`} {
		f.Add([]byte(raw), uint8(5))
	}
	f.Fuzz(func(t *testing.T, raw []byte, size uint8) {
		if len(raw) > 65536 {
			return
		}
		original := bytes.Clone(raw)
		limit := int(size)
		var want []json.RawMessage
		valid := ValidUTF8JSON(raw) && json.Unmarshal(raw, &want) == nil && want != nil && len(want) <= limit
		got, err := ParseRawArray(raw, limit)
		if !bytes.Equal(raw, original) || (err == nil) != valid || (!valid && got != nil) {
			t.Fatalf("raw array acceptance or ownership mismatch: %q / %v", raw, err)
		}
		if valid && !slices.EqualFunc(got, want, func(a, b json.RawMessage) bool { return bytes.Equal(a, b) }) {
			t.Fatalf("raw tokens changed: %q / %q", got, want)
		}
	})
}

func TestParseStringArrayLimit(t *testing.T) {
	for _, item := range []struct {
		raw   string
		limit int
		valid bool
	}{
		{"[]", 0, true}, {`["a"]`, 0, false}, {`["a"]`, 1, true},
		{`["a","b"]`, 1, false}, {`["a","\u6771京"]`, 2, true},
		{`["\uD800"]`, 1, false}, {`[null]`, 1, false}, {`["a"]true`, 1, false},
	} {
		values, err := ParseStringArrayLimit([]byte(item.raw), item.limit)
		if (err == nil) != item.valid || (!item.valid && values != nil) {
			t.Errorf("%s limit=%d: %v / %v", item.raw, item.limit, values, err)
		}
		if item.valid {
			want, err := ParseStringArray([]byte(item.raw))
			if err != nil || !slices.Equal(values, want) {
				t.Fatalf("bounded parser changed strings: %v / %v", values, want)
			}
		}
	}
}

func TestParseInt64(t *testing.T) {
	for raw, want := range map[string]int64{
		"0": 0, "-0": 0, "42": 42, " \t\r\n-42 \n": -42,
		"9223372036854775807": math.MaxInt64, "-9223372036854775808": math.MinInt64,
	} {
		if got, err := ParseInt64([]byte(raw)); err != nil || got != want {
			t.Errorf("%q: got %d / %v, want %d", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", " ", "-", "+1", "00", "-01", "1.0", "1e0", "0x1", "1_0", "1 2", "1\u00a0",
		"null", "true", `"1"`, "[]", "{}", "9223372036854775808", "-9223372036854775809", "\xff"} {
		if _, err := ParseInt64([]byte(raw)); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestParseInt64OnlyTrimsJSONWhitespace(t *testing.T) {
	for char := byte(0); char <= ' '; char++ {
		valid := char == ' ' || char == '\t' || char == '\r' || char == '\n'
		for _, raw := range [][]byte{{char, '4', '2'}, {'4', '2', char}, {char, '4', '2', char}} {
			got, err := ParseInt64(raw)
			if (err == nil) != valid || (valid && got != 42) {
				t.Errorf("boundary %q: %d / %v, valid=%t", raw, got, err, valid)
			}
		}
		if _, err := ParseInt64([]byte{'4', char, '2'}); err == nil {
			t.Errorf("accepted embedded control character %x", char)
		}
	}
	for _, raw := range []string{"\u008542", "42\u0085", "\u00a042", "42\u00a0", "\u202842", "42\u2029"} {
		if _, err := ParseInt64([]byte(raw)); err == nil {
			t.Errorf("accepted non-JSON whitespace %q", raw)
		}
	}
}

func TestParseBool(t *testing.T) {
	for raw, want := range map[string]bool{"true": true, "false": false, " \tfalse\r\n": false} {
		if got, err := ParseBool([]byte(raw)); err != nil || got != want {
			t.Errorf("%q: got %t / %v", raw, got, err)
		}
	}
	for _, raw := range []string{"", "null", "0", "1", `"true"`, "TRUE", "true false", "[true]", "{}", "true\u00a0"} {
		if _, err := ParseBool([]byte(raw)); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestParseString(t *testing.T) {
	for _, want := range []string{"", "plain", "東京", "\ufffd", "\U0001f642", "\x00\b\f\n\r\t\\\"/", strings.Repeat("東京", 12000)} {
		raw, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(append([]byte(" \t\n"), raw...), '\r', ' ')
		got, err := ParseString(raw)
		clear(raw)
		if err != nil || got != want {
			t.Errorf("text was rejected or aliased input: len=%d err=%v", len(want), err)
		}
	}
	for raw, want := range map[string]string{`"\ud83d\ude42"`: "\U0001f642", `"\u0041\/\u0000"`: "A/\x00",
		`"` + strings.Repeat(`\u0041`, 200) + `"`: strings.Repeat("A", 200)} {
		if got, err := ParseString([]byte(raw)); err != nil || got != want {
			t.Errorf("escaped text rejected: %q / %v", got, err)
		}
	}
	for _, raw := range []string{"", `"`, "null", "1", "false", "[]", `"a" "b"`, `"a"junk"`, `"\uD800"`,
		`"\uDC00"`, `"\uD800x"`, `"\x41"`, `"\q"`, "\"a\nb\"", "\"\xff\"", "\u00a0\"a\""} {
		if got, err := ParseString([]byte(raw)); err == nil || got != "" {
			t.Errorf("accepted or returned partial text for %q: %q / %v", raw, got, err)
		}
	}
}

func TestParseArray(t *testing.T) {
	for raw, want := range map[string][]int64{"[]": {}, " \t[1,-2,9223372036854775807]\n": {1, -2, math.MaxInt64}} {
		got, err := ParseArray([]byte(raw), ParseInt64)
		if err != nil || got == nil || !slices.Equal(got, want) {
			t.Errorf("%q: %v / %v", raw, got, err)
		}
	}
	for _, raw := range []string{"", "null", "1", "{}", "[", "[1", "[1,]", "[1 2]", "[null]", "[1,null]", "[1,1.5]", "[1,{}]", "[]true", "[] []"} {
		if got, err := ParseArray([]byte(raw), ParseInt64); err == nil || got != nil {
			t.Errorf("accepted or returned partial array for %q: %v / %v", raw, got, err)
		}
	}
	raw := []byte(`["first","\ud83d\ude42","` + strings.Repeat("東京", 128) + `"]`)
	got, err := ParseArray(raw, ParseString)
	clear(raw)
	if err != nil || !slices.Equal(got, []string{"first", "\U0001f642", strings.Repeat("東京", 128)}) {
		t.Fatalf("array lost ownership or reused a reader value: %v / %v", got, err)
	}
}

func TestTypedScalarArrays(t *testing.T) {
	for _, raw := range []string{"", "null", "true", "[", "[null]", "[1,]", "[1,null]", "[{}]", "[[1]]", "[1.5]", "[1e0]", "[9223372036854775808]", "[]true"} {
		if got, err := ParseInt64Array([]byte(raw)); err == nil || got != nil {
			t.Errorf("accepted invalid integer array %q: %v / %v", raw, got, err)
		}
	}
	got, err := ParseInt64Array([]byte(`[-9223372036854775808,9223372036854775807,0]`))
	if err != nil || !slices.Equal(got, []int64{math.MinInt64, math.MaxInt64, 0}) {
		t.Fatal(got, err)
	}
	for _, raw := range []string{"[]", `["","one","\ud83d\ude42"]`, "[" + strings.Repeat(`"東京",`, 127) + `"東京"]`, `["` + strings.Repeat("x", 300) + `"]`} {
		data := []byte(raw)
		var want []string
		if err := json.Unmarshal(data, &want); err != nil {
			t.Fatal(err)
		}
		got, err := ParseStringArray(data)
		clear(data)
		if err != nil || got == nil || !slices.Equal(got, want) {
			t.Fatalf("lost string array ownership for %q: %q / %v", raw, got, err)
		}
	}
	for _, raw := range []string{"[null]", `["ok",1]`, `["\uD800"]`, `["\uDC00"]`, "[\"\xff\"]"} {
		if got, err := ParseStringArray([]byte(raw)); err == nil || got != nil {
			t.Errorf("accepted invalid string array %q: %v / %v", raw, got, err)
		}
	}
}

func TestParseDateTime(t *testing.T) {
	for _, value := range []string{"1969-12-31T23:59:59.999Z", "2026-09-17T12:34:56+09:00"} {
		raw := []byte(`"` + value + `"`)
		want, err := time.Parse(time.RFC3339, value)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ParseDateTime(raw)
		clear(raw)
		if err != nil || !got.Equal(want) {
			t.Fatal(got, err)
		}
	}
	for _, raw := range []string{"null", "1", `"bad"`, `"2026-99-17T12:34:56Z"`, `"\uD800"`, `"2026-09-17T12:34:56Z" false`} {
		if _, err := ParseDateTime([]byte(raw)); err == nil {
			t.Errorf("accepted invalid date %q", raw)
		}
	}
}

func TestStringArraySharedStorageRetainsOwnership(t *testing.T) {
	for _, source := range []string{
		`["prefix東京","東京","\u6771\u4eac","","東京"]`,
		`["` + strings.Repeat("x", 256) + `","` + strings.Repeat("x", 256) + `","last"]`,
		`["first",` + strings.Repeat(`"東京","different",`, 128) + `"first"]`,
	} {
		raw := []byte(source)
		var want []string
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		got, err := ParseStringArray(raw)
		clear(raw)
		for range 20 {
			if _, err := ParseStringArray([]byte(`["overwrite","\u0041"]`)); err != nil {
				t.Fatal(err)
			}
		}
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("array lost ownership: %q / %v", got, err)
		}
	}
}

func TestStaticScalarAllocation(t *testing.T) {
	integer, boolean := []byte("9223372036854775807"), []byte("true")
	if count := testing.AllocsPerRun(1000, func() {
		i, err := ParseInt64(integer)
		if err != nil || i != math.MaxInt64 {
			t.Fatal(i, err)
		}
		b, err := ParseBool(boolean)
		if err != nil || !b {
			t.Fatal(b, err)
		}
	}); count != 0 {
		t.Fatalf("static scalar reads allocated %v times", count)
	}
}

func FuzzStaticJSONScalars(f *testing.F) {
	for _, raw := range []string{"null", "-0", "9223372036854775807", "9223372036854775808", "-01", "1e0", "true", `"\ud83d\ude42"`, `"\uD800"`, `"\xff"`, `""`} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 65536 {
			return
		}
		valid := ValidUTF8JSON(raw) && !bytes.Equal(bytes.Trim(raw, " \t\r\n"), []byte("null"))
		compareStaticScalar(t, raw, valid, ParseInt64)
		compareStaticScalar(t, raw, valid, ParseBool)
		compareStaticScalar(t, raw, valid, ParseString)
	})
}

func compareStaticScalar[T comparable](t *testing.T, raw []byte, valid bool, parse func([]byte) (T, error)) {
	t.Helper()
	var want T
	accepted := valid && json.Unmarshal(raw, &want) == nil
	got, err := parse(raw)
	if (err == nil) != accepted || (accepted && got != want) {
		t.Fatalf("scalar %q: got %v / %v, want %v (accepted=%t)", raw, got, err, want, accepted)
	}
}

func FuzzStaticJSONArray(f *testing.F) {
	for _, raw := range []string{`[]`, `["a","b"]`, `[null]`, `["\uD800"]`, `[]true`, `[["a"]]`, `["",null]`,
		`["prefix東京","東京","\u6771\u4eac","","東京"]`, `["a","a","\uD800"]`} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 65536 {
			return
		}
		got, err := ParseArray(raw, ParseString)
		packed, packedErr := ParseStringArray(raw)
		if (err == nil) != (packedErr == nil) || !slices.Equal(got, packed) {
			t.Fatalf("string array readers disagree: %q", raw)
		}
		limit := len(raw) % 17
		bounded, boundedErr := ParseStringArrayLimit(raw, limit)
		validBounded := err == nil && len(got) <= limit
		if (boundedErr == nil) != validBounded || (validBounded && !slices.Equal(bounded, got)) || (!validBounded && bounded != nil) {
			t.Fatalf("bounded string array disagrees: %q limit=%d: %v", raw, limit, boundedErr)
		}
		var values []json.RawMessage
		accepted := ValidUTF8JSON(raw) && json.Unmarshal(raw, &values) == nil && values != nil
		var want []string
		for _, value := range values {
			var s string
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &s) != nil {
				accepted = false
			}
			want = append(want, s)
		}
		if (err == nil) != accepted || (accepted && !slices.Equal(got, want)) {
			t.Fatalf("array %q: got %q / %v, want %q (accepted=%t)", raw, got, err, want, accepted)
		}
		integers, intErr := ParseArray(raw, ParseInt64)
		typed, typedErr := ParseInt64Array(raw)
		if (intErr == nil) != (typedErr == nil) || !slices.Equal(integers, typed) {
			t.Fatalf("integer array readers disagree: %q", raw)
		}
	})
}
