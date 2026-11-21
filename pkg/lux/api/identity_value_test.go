package api

import (
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/auth"
)

func identityValueForTest(t *testing.T, raw string) auth.ClaimValue {
	t.Helper()
	cfg := &auth.Config{Secret: "typed-identity-test", Expires: time.Hour}
	token, err := auth.Sign(cfg, func(dst []byte) ([]byte, error) { return append(dst, `{"value":`+raw+`}`...), nil })
	if err != nil {
		t.Fatal(err)
	}
	claims, err := auth.Verify(cfg, token)
	if err != nil {
		t.Fatal(err)
	}
	return claims.Get("value")
}

func TestIdentityTypedValues(t *testing.T) {
	date, err := ReadIdentityDateTime(identityValueForTest(t, `"2026-01-02T03:04:05.123456789Z"`))
	if err != nil || date.Nanosecond() != 123456789 {
		t.Fatal(date, err)
	}
	id, err := ReadIdentityUUID(identityValueForTest(t, `"00112233-4455-6677-8899-aabbccddeeff"`))
	if err != nil || id.String() != "00112233-4455-6677-8899-aabbccddeeff" {
		t.Fatal(id, err)
	}
	decimal, err := ReadIdentityDecimal(identityValueForTest(t, `"12345678901234567890.123456789"`))
	if err != nil || decimal.String() != "12345678901234567890.123456789" {
		t.Fatal(decimal, err)
	}
	data, err := ReadIdentityBytes(identityValueForTest(t, `"AAEC"`))
	if err != nil || string(data) != "\x00\x01\x02" {
		t.Fatal(data, err)
	}
	claim := identityValueForTest(t, `{"n":9007199254740993}`)
	raw, err := ReadIdentityJSON(claim)
	if err != nil || string(raw) != claim.Raw() {
		t.Fatal(string(raw), err)
	}
	raw[0] = 'x'
	if claim.Raw()[0] != '{' {
		t.Fatal("mutable JSON escaped immutable claims")
	}
	ints, err := ReadIdentityIntArray(identityValueForTest(t, `[0,9223372036854775807]`))
	if err != nil || len(ints) != 2 || ints[1] != 9223372036854775807 {
		t.Fatal(ints, err)
	}
	texts, err := ReadIdentityStringArray(identityValueForTest(t, `["","hi"]`))
	if err != nil || len(texts) != 2 || texts[0] != "" {
		t.Fatal(texts, err)
	}
}

func TestIdentityTypedValuesRejectWrongKinds(t *testing.T) {
	for _, raw := range []string{`null`, `false`, `42`, `{}`} {
		value := identityValueForTest(t, raw)
		if _, err := ReadIdentityDateTime(value); err == nil {
			t.Errorf("date accepted %s", raw)
		}
		if _, err := ReadIdentityUUID(value); err == nil {
			t.Errorf("uuid accepted %s", raw)
		}
		if _, err := ReadIdentityDecimal(value); err == nil {
			t.Errorf("decimal accepted %s", raw)
		}
		if _, err := ReadIdentityBytes(value); err == nil {
			t.Errorf("bytes accepted %s", raw)
		}
		if _, err := ReadIdentityIntArray(value); err == nil {
			t.Errorf("int array accepted %s", raw)
		}
		if _, err := ReadIdentityStringArray(value); err == nil {
			t.Errorf("string array accepted %s", raw)
		}
	}
	if _, err := ReadIdentityJSON(auth.ClaimValue{}); err == nil {
		t.Fatal("missing JSON accepted")
	}
	if _, err := ReadIdentityJSON(identityValueForTest(t, `null`)); err == nil {
		t.Fatal("null non-null JSON accepted")
	}
}

func TestIdentityBuiltinArrays(t *testing.T) {
	bools, err := ReadIdentityBooleanArray(identityValueForTest(t, `[false,true]`))
	if err != nil || len(bools) != 2 || bools[0] || !bools[1] {
		t.Fatal(bools, err)
	}
	floats, err := ReadIdentityFloatArray(identityValueForTest(t, `[0,1.5]`))
	if err != nil || len(floats) != 2 || floats[1] != 1.5 {
		t.Fatal(floats, err)
	}
	durations, err := ReadIdentityDurationArray(identityValueForTest(t, `[-9223372036854775808,9223372036854775807]`))
	if err != nil || len(durations) != 2 || durations[0] != time.Duration(-9223372036854775808) || durations[1] != time.Duration(9223372036854775807) {
		t.Fatal(durations, err)
	}
	dates, err := ReadIdentityDateTimeArray(identityValueForTest(t, `["2026-01-02T03:04:05.123456789Z"]`))
	if err != nil || len(dates) != 1 || dates[0].Nanosecond() != 123456789 {
		t.Fatal(dates, err)
	}
}

func TestIdentityBuiltinArraysMutableAndExact(t *testing.T) {
	ids, err := ReadIdentityUUIDArray(identityValueForTest(t, `["00112233-4455-6677-8899-aabbccddeeff"]`))
	if err != nil || len(ids) != 1 || ids[0].String() != "00112233-4455-6677-8899-aabbccddeeff" {
		t.Fatal(ids, err)
	}
	decimals, err := ReadIdentityDecimalArray(identityValueForTest(t, `["12345678901234567890.123456789"]`))
	if err != nil || len(decimals) != 1 || decimals[0].String() != "12345678901234567890.123456789" {
		t.Fatal(decimals, err)
	}
	bytes, err := ReadIdentityBytesArray(identityValueForTest(t, `["AAEC",""]`))
	if err != nil || len(bytes) != 2 || string(bytes[0]) != "\x00\x01\x02" || len(bytes[1]) != 0 {
		t.Fatal(bytes, err)
	}
	claim := identityValueForTest(t, `[null,{"n":9007199254740993}]`)
	values, err := ReadIdentityJSONArray(claim)
	if err != nil || len(values) != 2 || string(values[0]) != "null" || string(values[1]) != `{"n":9007199254740993}` {
		t.Fatal(values, err)
	}
	values[1][0] = 'x'
	if claim.Raw() != `[null,{"n":9007199254740993}]` {
		t.Fatal("JSON array aliases immutable claims")
	}
}

func checkIdentityArrayReader[T any](t *testing.T, read func(auth.ClaimValue) ([]T, error), invalid string) {
	t.Helper()
	values, err := read(identityValueForTest(t, `[]`))
	if err != nil || len(values) != 0 {
		t.Fatal(values, err)
	}
	for _, raw := range []string{`null`, `{}`, `true`, `42`, invalid} {
		if _, err := read(identityValueForTest(t, raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if _, err := read(auth.ClaimValue{}); err == nil {
		t.Error("accepted missing array")
	}
}

func TestIdentityBuiltinArraysRejectInvalid(t *testing.T) {
	t.Run("Boolean", func(t *testing.T) { checkIdentityArrayReader(t, ReadIdentityBooleanArray, `[true,null]`) })
	t.Run("Float", func(t *testing.T) { checkIdentityArrayReader(t, ReadIdentityFloatArray, `[1,1e999]`) })
	t.Run("Duration", func(t *testing.T) { checkIdentityArrayReader(t, ReadIdentityDurationArray, `[0,9223372036854775808]`) })
	t.Run("DateTime", func(t *testing.T) { checkIdentityArrayReader(t, ReadIdentityDateTimeArray, `[null]`) })
	t.Run("UUID", func(t *testing.T) {
		checkIdentityArrayReader(t, ReadIdentityUUIDArray, `["00112233-4455-6677-8899-aabbccddeefg"]`)
	})
	t.Run("Decimal", func(t *testing.T) { checkIdentityArrayReader(t, ReadIdentityDecimalArray, `[1]`) })
	t.Run("Bytes", func(t *testing.T) { checkIdentityArrayReader(t, ReadIdentityBytesArray, `["!"]`) })
	t.Run("JSON", func(t *testing.T) { checkIdentityArrayReader(t, ReadIdentityJSONArray, `"not an array"`) })
}

func TestIdentityDuration(t *testing.T) {
	for _, sample := range []struct {
		raw  string
		want time.Duration
	}{
		{"0", 0}, {"-1", -1}, {"9223372036854775807", time.Duration(9223372036854775807)},
	} {
		got, err := ReadIdentityDuration(identityValueForTest(t, sample.raw))
		if err != nil || got != sample.want {
			t.Fatalf("%s: %v %v", sample.raw, got, err)
		}
	}
	for _, raw := range []string{`null`, `"1"`, `1.5`, `9223372036854775808`} {
		if _, err := ReadIdentityDuration(identityValueForTest(t, raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if _, err := ReadIdentityDuration(auth.ClaimValue{}); err == nil {
		t.Error("accepted missing duration")
	}
}

func TestIdentityEnumArrayReader(t *testing.T) {
	type role string
	for _, raw := range []string{`null`, `false`, `42`, `"ADMIN"`, `[null]`, `[42]`} {
		if got, err := ReadIdentityEnumArray[role](identityValueForTest(t, raw)); err == nil || got != nil {
			t.Fatalf("accepted %s: %v %v", raw, got, err)
		}
	}
	if got, err := ReadIdentityEnumArray[role](auth.ClaimValue{}); err == nil || got != nil {
		t.Fatal(got, err)
	}
	value := identityValueForTest(t, `["ADMIN","MEMBER"]`)
	got, err := ReadIdentityEnumArray[role](value)
	if err != nil || len(got) != 2 || got[0] != "ADMIN" || got[1] != "MEMBER" {
		t.Fatal(got, err)
	}
	got[0] = "changed"
	again, err := ReadIdentityEnumArray[role](value)
	if err != nil || again[0] != "ADMIN" {
		t.Fatal("claim changed", again, err)
	}
	empty, err := ReadIdentityEnumArray[role](identityValueForTest(t, `[]`))
	if err != nil || len(empty) != 0 {
		t.Fatal(empty, err)
	}
}
