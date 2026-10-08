package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestUUIDCanonicalSharedCases(t *testing.T) {
	data, err := os.ReadFile("../codec/testdata/protocol-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Input     string  `json:"input"`
			Canonical *string `json:"canonical"`
		} `json:"uuidCanonicalCases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil || len(fixture.Cases) != 11 {
		t.Fatalf("invalid shared UUID fixture: %v", err)
	}
	for _, item := range fixture.Cases {
		for _, list := range []bool{false, true} {
			meta := ParamMeta{Name: "value", Type: "UUID", FieldID: 1, IsList: list}
			raw, _ := json.Marshal(item.Input)
			if list {
				raw = append(append([]byte{'['}, raw...), ']')
			}
			params := map[string]json.RawMessage{"value": raw}
			if item.Canonical != nil {
				var expected any = *item.Canonical
				if list {
					expected = []any{expected}
				}
				assertJSONParameterWire(t, meta, params, map[string]any{"value": expected})
				continue
			}
			registry := NewAPIRegistry()
			registry.Register("echo", 1)
			registry.RegisterParams("echo", []ParamMeta{meta})
			req := &Request{API: "echo", Params: params}
			if err := registry.prepareJSONRequest(req); err == nil {
				t.Errorf("accepted malformed UUID %q list=%t", item.Input, list)
			}
		}
	}
}

type requestJSONMarshalerProbe struct{ calls *int }

func (probe requestJSONMarshalerProbe) MarshalJSON() ([]byte, error) {
	*probe.calls++
	return []byte(`{"ok":true}`), nil
}

func TestBinaryJSONRequestRejectsImplicitSerialization(t *testing.T) {
	for _, list := range []bool{false, true} {
		meta := []ParamMeta{{Name: "document", Type: "JSON", FieldID: 1, IsList: list}}
		calls := 0
		for _, value := range []any{map[string]any{"ok": true}, struct{ OK bool }{true},
			requestJSONMarshalerProbe{&calls}, "text", int64(42), true, []byte(`{"ok":true}`), nil,
		} {
			input := value
			if list {
				input = []any{value}
			}
			body, err := EncodeBinaryRequest(1, nil, map[string]any{"document": input}, meta)
			if err == nil || body != nil {
				t.Errorf("arbitrary Go value encoded as JSON: list=%v, type=%T", list, value)
			}
		}
		if calls != 0 {
			t.Errorf("request encoding invoked MarshalJSON %d times", calls)
		}
	}
}

func TestBinaryJSONRequestPreservesDocumentTokens(t *testing.T) {
	for _, list := range []bool{false, true} {
		meta := []ParamMeta{{Name: "document", Type: "JSON", FieldID: 1, IsList: list}}
		registry := NewAPIRegistry()
		registry.Register("read", 1)
		registry.RegisterParams("read", meta)
		for _, raw := range []string{`123456789012345678901234567890`, `1.234567890123456789`, `"東京"`, `{"a":1,"a":2}`, `[]`, `true`} {
			var input any = json.RawMessage(raw)
			if list {
				input = []any{input}
			}
			body := mustEncodeBinaryRequest(t, 1, nil, map[string]any{"document": input}, meta)
			req, err := registry.ParseBinaryRequest(body)
			if err != nil {
				t.Fatal(err)
			}
			if list {
				values, err := req.ParamRawJSONArray("document")
				if err != nil || len(values) != 1 || string(values[0]) != raw {
					t.Fatalf("JSON list changed: %q, %v", values, err)
				}
			} else if value, err := req.ParamRawJSON("document"); err != nil || string(value) != raw {
				t.Fatalf("JSON document changed: %q, %v", value, err)
			}
		}
	}
}

func TestRequiredJSONRejectsNullInBothRequestModes(t *testing.T) {
	meta := []ParamMeta{{Name: "document", Type: "JSON", FieldID: 1}}
	registry := NewAPIRegistry()
	registry.Register("read", 1)
	registry.RegisterParams("read", meta)
	for _, raw := range []string{"null", " \t\r\nnull \t\r\n"} {
		if body, err := EncodeBinaryRequest(1, nil, map[string]any{"document": json.RawMessage(raw)}, meta); err == nil || body != nil {
			t.Errorf("encoded non-null JSON parameter as null: %q", raw)
		}
		// Build an untrusted wire request independently of the checked encoder.
		body := codec.AppendBytes([]byte{1, 0, 1}, []byte(raw))
		body = append(body, 0)
		if _, err := registry.ParseBinaryRequest(body); err == nil {
			t.Errorf("Binary accepted non-null JSON parameter as null: %q", raw)
		}
		req := &Request{API: "read", Params: map[string]json.RawMessage{"document": []byte(raw)}}
		if err := registry.prepareJSONRequest(req); err == nil {
			t.Errorf("JSON accepted non-null JSON parameter as null: %q", raw)
		}
	}
}

func TestJSONParameterNullScope(t *testing.T) {
	for _, list := range []bool{false, true} {
		meta := []ParamMeta{{Name: "document", Type: "JSON", FieldID: 1, Nullable: true, IsList: list}}
		registry := NewAPIRegistry()
		registry.Register("read", 1)
		registry.RegisterParams("read", meta)
		body := mustEncodeBinaryRequest(t, 1, nil, map[string]any{"document": nil}, meta)
		req, err := registry.ParseBinaryRequest(body)
		if err != nil || !req.ParamIsNull("document") {
			t.Fatalf("nullable JSON rejected null: %v", err)
		}
	}
	meta := []ParamMeta{{Name: "document", Type: "JSON", FieldID: 1, IsList: true}}
	registry := NewAPIRegistry()
	registry.Register("read", 1)
	registry.RegisterParams("read", meta)
	body := mustEncodeBinaryRequest(t, 1, nil, map[string]any{"document": []json.RawMessage{[]byte("null"), []byte(`{"nested":null}`)}}, meta)
	req, err := registry.ParseBinaryRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	values, err := req.ParamRawJSONArray("document")
	if err != nil || len(values) != 2 || string(values[0]) != "null" || string(values[1]) != `{"nested":null}` {
		t.Fatalf("JSON list null changed: %q, %v", values, err)
	}
}

func TestRequiredJSONNullNeverReachesHTTPHandler(t *testing.T) {
	router := NewRouterWithOptions(RouterOptions{})
	router.Schema.RegisterAPI(&schema.API{Name: "read", ID: 1, ReturnType: "Int", Params: []schema.Param{
		{Name: "document", ID: 1, Type: schema.FieldJSON},
	}})
	router.Registry.RegisterSchemaAPIs(router.Schema)
	called := false
	router.Handle("read", func(_ context.Context, _ *Request) error { called = true; return nil })
	for _, binaryMode := range []bool{false, true} {
		body := []byte(`{"$api":"read","document":null}`)
		if binaryMode {
			body = append(codec.AppendBytes([]byte{1, 0, 1}, []byte(" \tnull\r\n")), 0)
		}
		request := httptest.NewRequest("POST", "/luvia", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if binaryMode {
			request.Header.Set("X-Luxo-Mode", "binary")
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != 400 || called {
			t.Fatalf("null reached handler: binary=%t, status=%d, called=%t", binaryMode, response.Code, called)
		}
	}
}

func TestJSONTextMetadataRequiresStrings(t *testing.T) {
	for _, kind := range []string{"String", "Enum", "DateTime", "UUID", "Decimal"} {
		for _, list := range []bool{false, true} {
			registry := NewAPIRegistry()
			registry.Register("echo", 1)
			registry.RegisterParams("echo", []ParamMeta{{Name: "value", Type: kind, FieldID: 1, IsList: list}})
			for _, token := range []string{"0", "-1", "9007199254740993", "true", "{}", "[]", "null", `"\uD800"`, "\"\xff\""} {
				raw := token
				if list {
					raw = "[" + token + "]"
				}
				req := &Request{API: "echo", Params: map[string]json.RawMessage{"value": []byte(raw)}}
				if err := registry.prepareJSONRequest(req); err == nil {
					t.Errorf("%s list=%t accepted %q", kind, list, raw)
				}
			}
		}
	}
}

func TestJSONTypedParametersPreserveCanonicalWire(t *testing.T) {
	for _, item := range []struct {
		kind, raw string
		value     any
	}{
		{"String", `"\u6771京"`, "東京"}, {"Enum", `"\u0041CTIVE"`, "ACTIVE"},
		{"Boolean", " \tfalse\r\n", false},
		{"DateTime", `"1969-12-31T23:59:59.999Z"`, "1969-12-31T23:59:59.999Z"},
		{"UUID", `"550e8400-e29b-41d4-a716-446655440000"`, "550e8400-e29b-41d4-a716-446655440000"},
		{"Decimal", `"12345678901234567890.12345678901234567890"`, "12345678901234567890.12345678901234567890"},
	} {
		for _, list := range []bool{false, true} {
			meta := ParamMeta{Name: "value", Type: item.kind, FieldID: 1, IsList: list, Nullable: true}
			raw, value := item.raw, item.value
			if list {
				raw, value = "["+raw+"]", []any{value}
			}
			for _, state := range []string{"value", "null", "missing", "empty"} {
				params, expected := map[string]json.RawMessage{}, map[string]any{}
				switch state {
				case "value":
					params["value"], expected["value"] = []byte(raw), value
				case "null":
					params["value"], expected["value"] = []byte("null"), nil
				case "empty":
					if !list {
						continue
					}
					params["value"], expected["value"] = []byte("[]"), []any{}
				}
				assertJSONParameterWire(t, meta, params, expected)
			}
		}
	}
}

func TestJSONTypedParametersRejectInvalidValues(t *testing.T) {
	for _, kind := range []string{"String", "Boolean", "Unknown"} {
		for _, list := range []bool{false, true} {
			meta := ParamMeta{Name: "value", Type: kind, IsList: list}
			for _, raw := range []string{"", "null", "[null]", "42", "\vfalse", `"broken`,
				`"\uD800"`, `"x"true`, `["x",null]`, "[true,false,]", "[]{}", "{\"x\":true}"} {
				if value, present, err := decodeJSONTypedParam([]byte(raw), meta); err == nil || present || value != nil {
					t.Errorf("%s list=%t accepted %q: %v, %v", kind, list, raw, value, err)
				}
			}
		}
	}
}

func TestJSONTypedParametersOwnValues(t *testing.T) {
	for _, raw := range []string{`"東京"`, `"\u6771京"`, `["東京","\u6771京"]`} {
		input := []byte(raw)
		list := input[0] == '['
		value, present, err := decodeJSONTypedParam(input, ParamMeta{Name: "value", Type: "String", IsList: list})
		if err != nil || !present {
			t.Fatal(err)
		}
		clear(input)
		for range 3 {
			if _, _, err := decodeJSONTypedParam([]byte(`["overwrite"]`), ParamMeta{Type: "String", IsList: true}); err != nil {
				t.Fatal(err)
			}
		}
		if list {
			for _, text := range value.([]string) {
				if text != "東京" {
					t.Fatal("array string changed after input/reader reuse")
				}
			}
		} else if value != "東京" {
			t.Fatal("string changed after input reuse")
		}
	}
}

func TestJSONTypedListCountBoundary(t *testing.T) {
	for _, token := range []string{`""`, "true"} {
		meta := ParamMeta{Name: "value", Type: "String", IsList: true}
		if token == "true" {
			meta.Type = "Boolean"
		}
		for _, count := range []int{codec.MaxArrayElements, codec.MaxArrayElements + 1} {
			raw := []byte("[" + strings.Repeat(token+",", count-1) + token + "]")
			value, present, err := decodeJSONTypedParam(raw, meta)
			if count > codec.MaxArrayElements {
				if err == nil || present || value != nil || !strings.Contains(err.Error(), "exceeds limit") {
					t.Fatalf("oversized %s list: %v", meta.Type, err)
				}
			} else {
				items, ok := binaryListValues(value)
				if err != nil || !present || !ok || len(items) != count {
					t.Fatalf("inclusive %s list limit: %v", meta.Type, err)
				}
			}
		}
	}
}

func TestJSONBytesParametersRequireBase64Strings(t *testing.T) {
	for _, list := range []bool{false, true} {
		registry := NewAPIRegistry()
		registry.Register("echo", 1)
		registry.RegisterParams("echo", []ParamMeta{{Name: "value", Type: "Bytes", FieldID: 1, IsList: list}})
		for _, token := range []string{"null", "true", "42", "[]", "[1,2]", "{}", `"***"`, `"\uD800"`, "\"\xff\""} {
			raw := token
			if list {
				raw = `["AQI=",` + token + "]"
			}
			req := &Request{API: "echo", Params: map[string]json.RawMessage{"value": []byte(raw)}}
			if err := registry.prepareJSONRequest(req); err == nil {
				t.Errorf("Bytes list=%t accepted %q", list, raw)
			}
		}
	}
}

func TestJSONBytesParametersPreserveOwnedValues(t *testing.T) {
	for _, plain := range [][]byte{{}, {0, 127, 128, 255}, bytes.Repeat([]byte{0, 255, 127}, 24000)} {
		encoded := base64.StdEncoding.EncodeToString(plain)
		for _, list := range []bool{false, true} {
			raw := []byte(`"` + encoded + `"`)
			if list {
				raw = []byte(`["` + encoded + `",""]`)
			}
			meta := ParamMeta{Name: "value", Type: "Bytes", IsList: list}
			got, present, err := decodeJSONBytesParam(raw, meta)
			clear(raw)
			if err != nil || !present {
				t.Fatalf("Bytes list=%t: %v", list, err)
			}
			if list {
				values := got.([][]byte)
				if len(values) != 2 || !bytes.Equal(values[0], plain) || values[1] == nil || len(values[1]) != 0 {
					t.Fatal("list changed bytes or empty value")
				}
			} else if value := got.([]byte); value == nil || !bytes.Equal(value, plain) {
				t.Fatal("scalar changed bytes or empty value")
			}
		}
	}
}

func TestJSONBytesParametersRejectIncompleteValues(t *testing.T) {
	for _, raw := range []string{"", "null", "true", "[]", `"`, `"AQI="false`, `"AQI=" "AA=="`,
		`"AQI=%%"`, `"AQI"`, `"\q"`, `"\uD800"`, "\"AQI=\n\"", "\"\xff\""} {
		got, present, err := decodeJSONBytesParam([]byte(raw), ParamMeta{Name: "value", Type: "Bytes"})
		if err == nil || present || got != nil {
			t.Errorf("invalid scalar exposed a value: %q: %v / %v", raw, got, err)
		}
	}
	meta := ParamMeta{Name: "value", Type: "Bytes", IsList: true}
	for _, raw := range []string{"", "null", `"AQI="`, "{}", "[", `["AQI=",]`, `["AQI=",null]`,
		`["AQI=","AQI=%%"]`, `[]true`, `["AQI="] []`, `["AQI=","\uD800"]`} {
		if got, present, err := decodeJSONBytesParam([]byte(raw), meta); err == nil || present || got != nil {
			t.Errorf("invalid list exposed partial values: %q: %v / %v", raw, got, err)
		}
	}
}

func TestJSONBytesParametersPreserveNullableAndMissing(t *testing.T) {
	for _, list := range []bool{false, true} {
		meta := ParamMeta{Name: "value", Type: "Bytes", FieldID: 1, IsList: list, Nullable: true}
		for _, present := range []bool{false, true} {
			params := map[string]json.RawMessage{}
			expected := map[string]any{}
			if present {
				params["value"], expected["value"] = []byte("null"), nil
			}
			assertJSONParameterWire(t, meta, params, expected)
		}
	}
}

func TestJSONBytesListValuesAreIndependent(t *testing.T) {
	for _, raw := range []string{`["AQI=","AQI="]`, `["\u0041QI=","\u0041QI="]`} {
		got, present, err := decodeJSONBytesParam([]byte(raw), ParamMeta{Name: "values", Type: "Bytes", IsList: true})
		if err != nil || !present {
			t.Fatal(err)
		}
		values := got.([][]byte)
		values[0][0] = 255
		if !bytes.Equal(values[1], []byte{1, 2}) {
			t.Fatal("byte strings share mutable storage")
		}
	}
}

func TestJSONBytesParametersPreserveCanonicalWire(t *testing.T) {
	for _, raw := range []string{`"AQI="`, `"\u0041\u0051I="`, " \t\"AQI=\"\r\n", `"AQ\r\nI="`} {
		for _, list := range []bool{false, true} {
			meta := ParamMeta{Name: "value", Type: "Bytes", FieldID: 1, IsList: list}
			param := raw
			var expected any = []byte{1, 2}
			if list {
				param, expected = "["+raw+",\"\"]", [][]byte{{1, 2}, {}}
			}
			assertJSONParameterWire(t, meta, map[string]json.RawMessage{"value": []byte(param)}, map[string]any{"value": expected})
		}
	}
}

func assertJSONParameterWire(t *testing.T, meta ParamMeta, params map[string]json.RawMessage, expected map[string]any) {
	t.Helper()
	registry := NewAPIRegistry()
	registry.Register("echo", 1)
	registry.RegisterParams("echo", []ParamMeta{meta})
	req := &Request{API: "echo", Params: params}
	if err := registry.prepareJSONRequest(req); err != nil {
		t.Fatal(err)
	}
	want, err := EncodeBinaryRequest(1, nil, expected, []ParamMeta{meta})
	if err != nil || !bytes.Equal(want, req.BinaryRequest()) {
		t.Fatalf("%s list=%t changed canonical bytes for %v: %v", meta.Type, meta.IsList, params, err)
	}
}

func TestJSONBytesListCountBoundary(t *testing.T) {
	for _, count := range []int{codec.MaxArrayElements, codec.MaxArrayElements + 1} {
		raw := []byte("[" + strings.Repeat(`"",`, count-1) + `""]`)
		got, present, err := decodeJSONBytesParam(raw, ParamMeta{Name: "value", Type: "Bytes", IsList: true})
		if count > codec.MaxArrayElements {
			if err == nil || present || got != nil || !strings.Contains(err.Error(), "exceeds limit") {
				t.Fatalf("oversized bytes list: %v", err)
			}
		} else if err != nil || !present || len(got.([][]byte)) != count {
			t.Fatalf("inclusive bytes list limit: %v", err)
		}
	}
}

func TestJSONFloatParametersRejectNonzeroUnderflow(t *testing.T) {
	for _, token := range []string{"1e-400", "-1e-400", "2.4703282292062327e-324", "1e400", `"1"`, "true"} {
		t.Run(token, func(t *testing.T) {
			req := &Request{Params: map[string]json.RawMessage{"value": json.RawMessage(token)}}
			if _, err := req.ParamFloat("value"); err == nil {
				t.Errorf("ParamFloat accepted %s", token)
			}
			for _, list := range []bool{false, true} {
				registry := NewAPIRegistry()
				registry.Register("echo", 1)
				registry.RegisterParams("echo", []ParamMeta{{Name: "value", Type: "Float", FieldID: 1, IsList: list}})
				raw := token
				if list {
					raw = "[1," + token + "]"
				}
				req := &Request{API: "echo", Params: map[string]json.RawMessage{"value": json.RawMessage(raw)}}
				if err := registry.prepareJSONRequest(req); err == nil {
					t.Errorf("canonical float list=%v accepted %s", list, raw)
				}
			}
		})
	}
}

func TestJSONFloatParametersPreserveFiniteBoundaries(t *testing.T) {
	for _, token := range []string{"-0", "0e99999", "-0.00e-99999", "5e-324", "-5e-324", "1.7976931348623157e308"} {
		var expected float64
		if err := json.Unmarshal([]byte(token), &expected); err != nil {
			t.Fatal(err)
		}
		req := &Request{Params: map[string]json.RawMessage{"value": json.RawMessage(token)}}
		got, err := req.ParamFloat("value")
		if err != nil || math.Float64bits(got) != math.Float64bits(expected) {
			t.Fatalf("%s: got %g, err=%v", token, got, err)
		}
	}
}

func TestJSONFloatArrayValidation(t *testing.T) {
	meta := ParamMeta{Name: "values", Type: "Float", IsList: true}
	for _, raw := range []string{"", "null", "1", "[", "[1", "[1,]", "[,1]", "[1,,2]", "[null]", "[[1]]", "[1] 2"} {
		if _, err := decodeJSONFloatParam([]byte(raw), meta); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, raw := range []string{"[]", " [ ] \n", "[-0,5e-324,1.25]"} {
		if _, err := decodeJSONFloatParam([]byte(raw), meta); err != nil {
			t.Errorf("%q: %v", raw, err)
		}
	}
	for _, count := range []int{codec.MaxArrayElements, codec.MaxArrayElements + 1} {
		raw := []byte("[" + strings.Repeat("0,", count-1) + "0]")
		value, err := decodeJSONFloatParam(raw, meta)
		if count > codec.MaxArrayElements {
			if err == nil {
				t.Fatal("accepted oversized float array")
			}
		} else if err != nil || len(value.([]any)) != count {
			t.Fatalf("inclusive array limit: %v", err)
		}
	}
}

func TestJSONFloatCanonicalValues(t *testing.T) {
	for _, list := range []bool{false, true} {
		registry := NewAPIRegistry()
		registry.Register("echo", 1)
		registry.RegisterParams("echo", []ParamMeta{{Name: "value", Type: "Float", FieldID: 1, IsList: list}})
		for _, token := range []string{"-0", "5e-324", "1.25", "0e99999"} {
			raw := token
			if list {
				raw = "[" + token + "]"
			}
			req := &Request{API: "echo", Params: map[string]json.RawMessage{"value": json.RawMessage(raw)}}
			if err := registry.prepareJSONRequest(req); err != nil {
				t.Fatal(err)
			}
			decoded, err := registry.ParseBinaryRequest(req.BinaryRequest())
			if err != nil {
				t.Fatal(err)
			}
			value, _ := decoded.findParam("value")
			if list {
				value = value.([]float64)[0]
			}
			var expected float64
			if err := json.Unmarshal([]byte(token), &expected); err != nil {
				t.Fatal(err)
			}
			if math.Float64bits(value.(float64)) != math.Float64bits(expected) {
				t.Errorf("changed %s", token)
			}
		}
	}
}

func TestJSONParameterPreservesNumericTokens(t *testing.T) {
	values := []string{
		`9007199254740993`, `9223372036854775807`, `-9223372036854775808`,
		`18446744073709551617`, `-18446744073709551617`,
		`0.123456789012345678901234567890`, `1e400`, `1e-400`, `-0`, `-0.0`,
		`{"number":18446744073709551617,"nested":[0.123456789012345678901,1e400]}`,
		`[9007199254740993,{"number":-18446744073709551617}]`,
	}
	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			t.Run("scalar", func(t *testing.T) {
				assertCanonicalJSONParameter(t, value, false, []string{value})
			})
			t.Run("list", func(t *testing.T) {
				assertCanonicalJSONParameter(t, "["+value+",true]", true, []string{value, "true"})
			})
		})
	}
}

func TestJSONParameterPreservesRawStructure(t *testing.T) {
	values := []string{
		`true`, `false`, `""`, `"東京, [x] \" \\ \u0061"`,
		`{ "z":1, "a":2 }`, `{"same":1,"same":2}`, `[]`, `{}`,
		`{"\uD800\uDC00":["\uDBFF\uDFFF","\\uD800","�"]}`,
		`{"padding":"` + strings.Repeat("x", 8192) + `","value":9007199254740993}`,
	}
	for i, value := range values {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			assertCanonicalJSONParameter(t, value, false, []string{value})
			assertCanonicalJSONParameter(t, " \n[ \t"+value+", false ]\r", true, []string{value, "false"})
		})
	}
	assertCanonicalJSONParameter(t, " [ ] \n", true, nil)
	assertCanonicalJSONParameter(t, `[null,true,[],{}]`, true, []string{"null", "true", "[]", "{}"})
}

func TestRawJSONArrayRejectsMalformedInput(t *testing.T) {
	for _, raw := range []string{
		"", "null", "true", "{}", "1", `"[]"`, "[", "[1", "[1,]", "[,1]", "[1,,2]",
		"[01]", "[1.]", "[+1]", "[NaN]", "[Infinity]", `[{"a":}]`, `["unterminated]`,
		"[1] true", "[1] !", "[1]\x00",
		`["\uD800"]`, `[{"\uDC00":1}]`, `[{"x":["\uDBFF"]}]`, "[\"\xff\"]",
	} {
		t.Run(raw, func(t *testing.T) {
			var enc codec.Encoder
			enc.WriteInt(42)
			before := bytes.Clone(enc.Bytes())
			err := encodeRawJSONArray(&enc, ParamMeta{Name: "values", Type: "JSON", IsList: true}, []byte(raw))
			if err == nil || !strings.Contains(err.Error(), "param values") {
				t.Fatalf("expected a parameter error for %q, got %v", raw, err)
			}
			if !bytes.Equal(enc.Bytes(), before) {
				t.Fatal("invalid array partially modified the encoder")
			}
		})
	}
}

func TestJSONParameterRejectsInvalidUnicode(t *testing.T) {
	for _, raw := range []string{`"\uD800"`, `{"\uDC00":1}`, `{"x":["\uDBFF"]}`, "\"\xff\""} {
		for _, list := range []bool{false, true} {
			registry := NewAPIRegistry()
			registry.Register("echo", 1)
			registry.RegisterParams("echo", []ParamMeta{{Name: "value", Type: "JSON", FieldID: 1, IsList: list}})
			param := raw
			if list {
				param = "[" + raw + "]"
			}
			req := &Request{API: "echo", Params: map[string]json.RawMessage{"value": json.RawMessage(param)}}
			if err := registry.prepareJSONRequest(req); err == nil {
				t.Errorf("canonical JSON list=%t accepted %q", list, raw)
			}
			// Write bytes directly so writer validation cannot hide a read-side gap.
			var enc codec.Encoder
			enc.WriteVarint(1)
			enc.WriteBytes(nil)
			enc.WriteFieldHeader(1)
			if list {
				enc.WriteArrayHeader(1)
			}
			enc.WriteBytes([]byte(raw))
			enc.WriteEnd()
			if got, err := registry.ParseBinaryRequest(enc.Bytes()); err == nil {
				t.Errorf("binary JSON list=%t accepted %q", list, raw)
			} else if got != nil {
				t.Error("invalid JSON exposed a partial request")
			}
		}
	}
}

func TestRawJSONArrayCountBoundaries(t *testing.T) {
	meta := ParamMeta{Name: "values", Type: "JSON", IsList: true}
	for _, count := range []int{8, 9, 127, 128, 255, 256, codec.MaxArrayElements, codec.MaxArrayElements + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			raw := []byte("[" + strings.Repeat("0,", count-1) + "0]")
			var enc codec.Encoder
			err := encodeRawJSONArray(&enc, meta, raw)
			if count > codec.MaxArrayElements {
				if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
					t.Fatalf("oversized array: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			decoded, off := codec.ReadArrayHeader(enc.Bytes(), 0)
			if decoded != count || off == 0 {
				t.Fatalf("count = %d, want %d", decoded, count)
			}
			for range count {
				value, n := codec.ReadBytes(enc.Bytes(), off)
				if string(value) != "0" || n == 0 {
					t.Fatalf("invalid item at offset %d", off)
				}
				off += n
			}
			if off != len(enc.Bytes()) {
				t.Fatal("unexpected trailing bytes")
			}
		})
	}
}

func TestRawJSONArrayDecoderReleasesRequestStorage(t *testing.T) {
	for _, raw := range []string{`[1,2,3]`, `[{"name":"secret","value":`, `[` + strings.Repeat(`[`, 100) + `0` + strings.Repeat(`]`, 100) + `]`} {
		reader := acquireRawJSONArrayDecoder([]byte(raw))
		if _, err := reader.decoder.ReadToken(); err != nil {
			t.Fatal(err)
		}
		if len(reader.decoder.UnreadBuffer()) == 0 {
			t.Fatal("test must exercise a decoder that references request bytes")
		}
		_, _ = reader.decoder.ReadValue()
		reader.reset()
		if reader.input.Cap() != 0 || cap(reader.decoder.UnreadBuffer()) != 0 || reader.decoder.InputOffset() != 0 || reader.decoder.StackPointer() != "" {
			t.Fatal("pooled decoder retained request storage or active parsing state")
		}
		releaseRawJSONArrayDecoder(reader)
	}
}

func TestRawJSONArrayEncodingOwnsPayloadAcrossReaderReuse(t *testing.T) {
	raw := []byte(`[{"count":123456789012345678901234567890},"\u6771京",null]`)
	var original codec.Encoder
	if err := encodeRawJSONArray(&original, ParamMeta{Name: "values"}, raw); err != nil {
		t.Fatal(err)
	}
	want := bytes.Clone(original.Bytes())
	clear(raw)
	for range 3 {
		var enc codec.Encoder
		if err := encodeRawJSONArray(&enc, ParamMeta{Name: "values"}, []byte(`[{"a":1,"a":2},"\ud800\udc00"]`)); err != nil {
			t.Fatalf("reused decoder lost the existing raw JSON syntax policy: %v", err)
		}
		enc.Reset()
		if err := encodeRawJSONArray(&enc, ParamMeta{Name: "values"}, []byte(`["\ud800"]`)); err == nil {
			t.Fatal("reused decoder accepted an unpaired surrogate")
		}
	}
	if !bytes.Equal(original.Bytes(), want) {
		t.Fatal("raw JSON output borrowed input or pooled reader storage")
	}
}

func TestJSONParameterLegacyScalarPaths(t *testing.T) {
	registry := NewAPIRegistry()
	for _, tc := range []struct {
		typeName string
		raw      string
		want     any
	}{
		{"Float", "1.25", 1.25}, {"String", `"東京"`, "東京"}, {"Boolean", "false", false},
		{"JSON", " \nnull\t", nil},
	} {
		req := &Request{Params: map[string]json.RawMessage{"value": json.RawMessage(tc.raw)}}
		got, present, err := registry.jsonParamValue(req, ParamMeta{Name: "value", Type: tc.typeName})
		if err != nil || !present || got != tc.want {
			t.Fatalf("%s = %v, %v, %v; want %v", tc.typeName, got, present, err, tc.want)
		}
	}
	req := &Request{Params: map[string]json.RawMessage{"value": json.RawMessage(`{"broken":`)}}
	if _, _, err := registry.jsonParamValue(req, ParamMeta{Name: "value", Type: "Float"}); err == nil {
		t.Fatal("invalid JSON scalar was accepted")
	}
}

func assertCanonicalJSONParameter(t *testing.T, raw string, list bool, values []string) {
	t.Helper()
	meta := ParamMeta{Name: "value", Type: "JSON", FieldID: 1, IsList: list}
	registry := NewAPIRegistry()
	registry.Register("echo", 1)
	registry.RegisterParams("echo", []ParamMeta{meta})
	req := &Request{API: "echo", Params: map[string]json.RawMessage{"value": json.RawMessage(raw)}}
	if err := registry.prepareJSONRequest(req); err != nil {
		t.Fatal(err)
	}
	var expected codec.Encoder
	expected.WriteFieldHeader(1)
	if list {
		expected.WriteArrayHeader(len(values))
	}
	for _, value := range values {
		expected.WriteBytes([]byte(value))
	}
	expected.WriteEnd()
	if !bytes.Equal(req.binaryParams, expected.Bytes()) {
		t.Fatalf("canonical parameter changed JSON tokens:\n got %.200q\nwant %.200q", req.binaryParams, expected.Bytes())
	}
	if string(req.Params["value"]) != raw {
		t.Fatal("request parameter was mutated")
	}
	if _, err := registry.ParseBinaryRequest(req.BinaryRequest()); err != nil {
		t.Fatalf("canonical request cannot be decoded: %v", err)
	}
}
