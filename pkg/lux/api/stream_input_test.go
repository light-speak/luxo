package api

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/shopspring/decimal"
)

// Keep exact runtime values as a baseline for removing the subscription's
// JSON -> Binary -> value conversion. This does not approve that conversion.
func TestStreamInputCanonicalValues(t *testing.T) {
	stamp := time.Unix(-1, 123456789).UTC()
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	amount := decimal.RequireFromString("12345678901234567890.123456789")
	const document = `{"integer":123456789012345678901234567890,"decimal":0.12345678901234567890}`
	for _, sample := range []struct {
		name string
		kind schema.FieldType
		raw  string
		one  any
		list any
	}{
		{"Int", schema.FieldInt, `9223372036854775807`, int64(1<<63 - 1), []int64{1<<63 - 1}},
		{"Duration", schema.FieldDuration, `-9223372036854775808`, time.Duration(-1 << 63), []time.Duration{-1 << 63}},
		{"Float", schema.FieldFloat, `1.5`, 1.5, []float64{1.5}},
		{"String", schema.FieldString, `"\u6771\u4eac"`, "東京", []string{"東京"}},
		{"Boolean", schema.FieldBool, `true`, true, []bool{true}},
		{"DateTime", schema.FieldDateTime, `"1970-01-01T00:59:59.123456789+01:00"`, stamp, []time.Time{stamp}},
		{"UUID", schema.FieldUUID, `"550e8400-e29b-41d4-a716-446655440000"`, id, []uuid.UUID{id}},
		{"Decimal", schema.FieldDecimal, `"12345678901234567890.123456789"`, amount, []decimal.Decimal{amount}},
		{"Bytes", schema.FieldBytes, `"AAH/"`, []byte{0, 1, 255}, [][]byte{{0, 1, 255}}},
		{"JSON", schema.FieldJSON, document, json.RawMessage(document), []json.RawMessage{json.RawMessage(document)}},
		{"Enum", schema.FieldEnum, `"OPEN"`, "OPEN", []string{"OPEN"}},
	} {
		for _, list := range []bool{false, true} {
			name, raw, want := sample.name, sample.raw, sample.one
			if list {
				name, raw, want = name+"List", "["+raw+"]", sample.list
			}
			t.Run(name, func(t *testing.T) {
				router := streamInputRouter(sample.kind, list, false)
				input := map[string]json.RawMessage{"$sub": []byte(`"watchValue"`), "value": []byte(raw)}
				req, values, err := router.prepareWSJSONSubscription(input)
				if err != nil {
					t.Fatal(err)
				}
				binary, err := router.Registry.ParseBinaryRequest(req.BinaryRequest())
				if err != nil || !reflect.DeepEqual(binaryStreamParams(binary)["value"], want) {
					t.Fatalf("binary value differs: %v / %v", binary, err)
				}
				// A subscription outlives both the JSON input and request envelope.
				for _, bytes := range input {
					clear(bytes)
				}
				PutRequest(req)
				if !reflect.DeepEqual(values["value"], want) {
					t.Fatalf("retained value = %#v, want %#v", values["value"], want)
				}
			})
		}
	}
}

func streamInputRouter(kind schema.FieldType, list, nullable bool) *Router {
	router := NewRouter()
	router.Schema.RegisterEnum(&schema.Enum{Name: "State", Values: []string{"OPEN", "CLOSED"}})
	router.Schema.RegisterAPI(&schema.API{ID: 61, Name: "watchValue", Stream: true, Params: []schema.Param{
		{ID: 1, Name: "value", Type: kind, TypeName: "State", IsList: list, Nullable: nullable},
	}})
	router.Registry.RegisterSchemaAPIs(router.Schema)
	router.HandleStream("watchValue", nil)
	return router
}

func TestStreamInputRejectsMalformedValues(t *testing.T) {
	for _, sample := range []struct {
		kind schema.FieldType
		raw  string
	}{
		{schema.FieldInt, `9223372036854775808`}, {schema.FieldInt, `1.5`},
		{schema.FieldFloat, `1e999`}, {schema.FieldString, `"\uD800"`},
		{schema.FieldBool, `"true"`}, {schema.FieldDuration, `"1s"`},
		{schema.FieldDateTime, `"2026-01-01T00:00:00.1234567890Z"`},
		{schema.FieldDateTime, `123456789`}, {schema.FieldBytes, `"???"`},
		{schema.FieldJSON, `{"text":"\uD800"}`},
	} {
		for _, list := range []bool{false, true} {
			router := streamInputRouter(sample.kind, list, false)
			raw := sample.raw
			if list {
				raw = "[" + raw + "]"
			}
			input := map[string]json.RawMessage{"$sub": []byte(`"watchValue"`), "value": []byte(raw)}
			if _, _, err := router.prepareWSJSONSubscription(input); err == nil {
				t.Errorf("%s list=%v accepted %s", sample.kind, list, raw)
			}
		}
	}
}
