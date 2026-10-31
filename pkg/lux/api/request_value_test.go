package api

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
	"github.com/shopspring/decimal"
)

func TestStaticValueParams(t *testing.T) {
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	at := time.Date(1969, 12, 31, 23, 59, 59, 123456789, time.UTC)
	amount := decimal.RequireFromString("12345678901234567890.123456789")
	testStaticParam(t, "Duration", `-9223372036854775808`, time.Duration(-1<<63), (*Request).ParamDuration, `null`, `"1s"`, `1.0`, `9223372036854775808`)
	testStaticParam(t, "UUID", `"550e8400-e29b-41d4-a716-446655440000"`, id, (*Request).ParamUUID, `null`, `42`, `"invalid"`, `"\uD800"`)
	testStaticParam(t, "Decimal", `"12345678901234567890.123456789"`, amount, (*Request).ParamDecimal, `null`, `1.5`, `"bad"`)
	testStaticParam(t, "Bytes", `"AAEC/w=="`, []byte{0, 1, 2, 255}, (*Request).ParamBytes, `null`, `[0,1]`, `"???"`)
	testStaticParam(t, "RawJSON", `{"n":123456789012345678901234567890,"d":1.234567890123456789}`, json.RawMessage(`{"n":123456789012345678901234567890,"d":1.234567890123456789}`), (*Request).ParamRawJSON, `null`, `{`, `1 2`, `"\uD800"`)
	testStaticParam(t, "FloatArray", `[1.5,-2]`, []float64{1.5, -2}, (*Request).ParamFloatArray, `null`, `[null]`, `[1e999]`, `[1e-999]`, `["1"]`, `[1,`)
	testStaticParam(t, "BoolArray", `[true,false]`, []bool{true, false}, (*Request).ParamBoolArray, `null`, `[null]`, `[1]`, `["true"]`)
	testStaticParam(t, "DateTimeArray", `["1969-12-31T23:59:59.123456789Z"]`, []time.Time{at}, (*Request).ParamDateTimeArray, `null`, `[null]`, `["2026-02-30T00:00:00Z"]`, `["2026-01-01T00:00:00.1234567890Z"]`)
	testStaticParam(t, "DurationArray", `[-9223372036854775808,9223372036854775807]`, []time.Duration{-1 << 63, 1<<63 - 1}, (*Request).ParamDurationArray, `null`, `[null]`, `[1.5]`, `[9223372036854775808]`)
	testStaticParam(t, "DecimalArray", `["12345678901234567890.123456789"]`, []decimal.Decimal{amount}, (*Request).ParamDecimalArray, `null`, `[null]`, `[1.5]`, `["bad"]`)
	testStaticParam(t, "BytesArray", `["","AAEC/w=="]`, [][]byte{{}, {0, 1, 2, 255}}, (*Request).ParamBytesArray, `null`, `[null]`, `[[0]]`, `["???"]`)
	testStaticParam(t, "RawJSONArray", `[null,123456789012345678901234567890,1.234567890123456789]`, []json.RawMessage{[]byte("null"), []byte("123456789012345678901234567890"), []byte("1.234567890123456789")}, (*Request).ParamRawJSONArray, `null`, `[1,]`, `["\uD800"]`, `[] true`)
}

func testStaticParam[T any](t *testing.T, name, raw string, want T, read func(*Request, string) (T, error), invalid ...string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		for _, binary := range []bool{false, true} {
			req := staticValueRequest(raw, want, binary)
			got, err := read(req, "v")
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("binary=%t: got %v, %v; want %v", binary, got, err, want)
			}
			if _, err := read(req, "missing"); !isBadParam(err) {
				t.Fatalf("binary=%t: missing error = %v", binary, err)
			}
			if binary {
				req.paramSlots[0] = struct{}{}
				if _, err := read(req, "v"); !isBadParam(err) {
					t.Fatalf("wrong binary type: %v", err)
				}
				req.markParamNull(0)
				if _, err := read(req, "v"); !isBadParam(err) {
					t.Fatalf("binary null: %v", err)
				}
			}
		}
		for _, raw := range invalid {
			got, err := read(staticValueRequest(raw, nil, false), "v")
			var zero T
			if !isBadParam(err) || !reflect.DeepEqual(got, zero) {
				t.Errorf("invalid %s: got %v, %v", raw, got, err)
			}
		}
	})
}

func isBadParam(err error) bool {
	value, ok := err.(*errors.AppError)
	return ok && value.Code == 400
}

func staticValueRequest(raw string, value any, binary bool) *Request {
	if !binary {
		return &Request{Params: map[string]json.RawMessage{"v": []byte(raw)}}
	}
	req := &Request{BinaryMode: true}
	req.SetBinaryParams([]string{"v"}, 1)
	req.SetParamSlot(0, value)
	return req
}

func TestStaticParamArrayLimit(t *testing.T) {
	for _, raw := range []string{`[]`, `[true]`, `[true,false]`} {
		got, err := parseParamArray([]byte(raw), 2, jsonutil.ParseBool)
		if err != nil || got == nil {
			t.Fatalf("%s: %v, %v", raw, got, err)
		}
	}
	if got, err := parseParamArray([]byte(`[true,false,true]`), 2, jsonutil.ParseBool); err == nil || got != nil {
		t.Fatalf("excess list: %v, %v", got, err)
	}
}

func TestSetParamSlotNullTransitions(t *testing.T) {
	req := &Request{}
	req.SetBinaryParams([]string{"v"}, 1)
	if req.HasParam("v") || req.ParamIsNull("v") {
		t.Fatal("empty slot is present")
	}
	req.SetParamSlot(0, nil)
	if !req.HasParam("v") || !req.ParamIsNull("v") {
		t.Fatal("explicit null lost its presence")
	}
	req.SetParamSlot(0, "value")
	if req.ParamIsNull("v") {
		t.Fatal("value retained the null flag")
	}
	req.SetParamSlot(0, nil)
	if !req.ParamIsNull("v") {
		t.Fatal("null did not replace the existing value")
	}
}

func TestStaticDecimalArrayEscapes(t *testing.T) {
	req := staticValueRequest(`["\u0031.5","1.5"]`, nil, false)
	values, err := req.ParamDecimalArray("v")
	if err != nil || len(values) != 2 || !values[0].Equal(decimal.RequireFromString("1.5")) || !values[1].Equal(values[0]) {
		t.Fatalf("escaped decimals: %v, %v", values, err)
	}
}
