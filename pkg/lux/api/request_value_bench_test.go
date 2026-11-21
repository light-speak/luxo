package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func BenchmarkStaticParamSlot(b *testing.B) {
	for _, method := range []struct {
		name string
		set  func(*Request, int, any)
	}{
		{"previous", (*Request).setParamSlotBeforeStatic},
		{"static", (*Request).SetParamSlot},
	} {
		b.Run(method.name, func(b *testing.B) {
			var req Request
			b.ReportAllocs()
			for b.Loop() {
				req.paramSlots[0] = nil
				method.set(&req, 0, int64(42))
			}
		})
	}
}

func BenchmarkStaticValueParams(b *testing.B) {
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	amount := decimal.RequireFromString("12345678901234567890.123456789")
	benchmarkStaticValue(b, "Duration", "42", time.Duration(42), (*Request).ParamDuration)
	benchmarkStaticValue(b, "UUID", `"550e8400-e29b-41d4-a716-446655440000"`, id, (*Request).ParamUUID)
	benchmarkStaticValue(b, "Decimal", `"12345678901234567890.123456789"`, amount, (*Request).ParamDecimal)
	benchmarkStaticValue(b, "Bytes", `"AAEC/w=="`, []byte{0, 1, 2, 255}, (*Request).ParamBytes)
	for _, raw := range []struct{ name, value string }{
		{"short", `{"n":123456789012345678901234567890,"d":1.234567890123456789}`},
		{"72KB", `"` + strings.Repeat("東京", 12000) + `"`},
	} {
		benchmarkStaticValue(b, "RawJSON/"+raw.name, raw.value, json.RawMessage(raw.value), (*Request).ParamRawJSON)
	}
	for _, count := range []int{0, 1, 128} {
		benchmarkStaticList(b, "Float", "1.5", 1.5, count, (*Request).ParamFloatArray)
		benchmarkStaticList(b, "Bool", "true", true, count, (*Request).ParamBoolArray)
		benchmarkStaticList(b, "DateTime", `"1969-12-31T23:59:59.123456789Z"`, time.Date(1969, 12, 31, 23, 59, 59, 123456789, time.UTC), count, (*Request).ParamDateTimeArray)
		benchmarkStaticList(b, "Duration", "42", time.Duration(42), count, (*Request).ParamDurationArray)
		benchmarkStaticList(b, "Decimal", `"12345678901234567890.123456789"`, amount, count, (*Request).ParamDecimalArray)
		benchmarkStaticList(b, "Bytes", `"AAEC/w=="`, []byte{0, 1, 2, 255}, count, (*Request).ParamBytesArray)
		benchmarkStaticList(b, "RawJSON", `{"n":123456789012345678901234567890}`, json.RawMessage(`{"n":123456789012345678901234567890}`), count, (*Request).ParamRawJSONArray)
	}
}

func benchmarkStaticList[T any](b *testing.B, name, raw string, value T, count int, read func(*Request, string) ([]T, error)) {
	values, tokens := make([]T, count), make([]string, count)
	for i := range values {
		values[i], tokens[i] = value, raw
	}
	benchmarkStaticValue(b, fmt.Sprintf("%sArray/%d", name, count), "["+strings.Join(tokens, ",")+"]", values, read)
}

func benchmarkStaticValue[T any](b *testing.B, name, raw string, value T, read func(*Request, string) (T, error)) {
	for _, binary := range []bool{false, true} {
		mode := "JSON"
		if binary {
			mode = "Binary"
		}
		req := staticValueRequest(raw, value, binary)
		b.Run(name+"/"+mode+"/previous", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var out T // Generated handlers start with a fresh destination per request.
				if err := req.LegacyParamJSON("v", &out); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(name+"/"+mode+"/static", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				out, err := read(req, "v")
				if err != nil {
					b.Fatal(err)
				}
				_ = out
			}
		})
	}
}
