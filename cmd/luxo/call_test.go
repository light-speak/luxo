package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestCLIRejectsInvalidBinaryResponseBeforePrinting(t *testing.T) {
	s := schema.New()
	s.RegisterModel(&schema.Model{Name: "Measured", Fields: []schema.Field{{ID: 1, Name: "value", Type: schema.FieldFloat}}})
	invalid := binary.LittleEndian.AppendUint64(nil, 0x7ff0000000000000)
	row := append(append([]byte{0, 1}, invalid...), 0)
	column := append(append([]byte{1, 0, 1}, invalid...), 0)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = writer
	t.Cleanup(func() { os.Stdout = previous; writer.Close(); reader.Close() })
	for _, item := range []struct {
		meta schema.API
		data []byte
	}{
		{schema.API{ReturnType: "Float"}, invalid},
		{schema.API{ReturnType: "Float", ReturnList: true}, append([]byte{1}, invalid...)},
		{schema.API{ReturnType: "Float"}, nil},
		{schema.API{ReturnType: "Measured"}, row},
		{schema.API{ReturnType: "Measured", ReturnList: true}, column},
		{schema.API{ReturnType: "Measured", ReturnList: true, Paginated: true}, append(column, 2, 2, 20)},
	} {
		if err := decodeBinaryResponse(item.data, &item.meta, s); err == nil {
			t.Errorf("accepted invalid response: %+v", item.meta)
		}
	}
	writer.Close()
	os.Stdout = previous
	if data, err := io.ReadAll(reader); err != nil || len(data) != 0 {
		t.Fatalf("printed partial response: %q, %v", data, err)
	}
}

func TestCLIDecodesEnumAndScalarListResponses(t *testing.T) {
	s := schema.New()
	s.Enums["State"] = &schema.Enum{Name: "State"}
	for _, list := range []bool{false, true} {
		data, err := codec.AppendString(nil, "OPEN")
		if err != nil {
			t.Fatal(err)
		}
		want := `"OPEN"`
		if list {
			data, want = append([]byte{1}, data...), `["OPEN"]`
		}
		meta := &schema.API{ReturnType: "State", ReturnList: list}
		got, err := decodeBinaryScalarResponse(data, meta, s)
		if err != nil || string(got) != want {
			t.Fatalf("response = %q, %v; want %s", got, err, want)
		}
	}
}

func cliNumberTokens(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("../../pkg/lux/codec/testdata/protocol-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ JSONNumberTokens []string }
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.JSONNumberTokens) == 0 {
		t.Fatal("missing numeric corpus")
	}
	return fixture.JSONNumberTokens
}

func TestCLIJSONModePreservesNumericTokens(t *testing.T) {
	received := make(chan json.RawMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		received <- body["value"]
		w.Write([]byte(`{"data":true}`))
	}))
	defer server.Close()
	previous := callHost
	callHost = server.URL
	t.Cleanup(func() { callHost = previous })
	for _, token := range cliNumberTokens(t) {
		for _, value := range []string{token, `{"n":` + token + `}`, `[` + token + `,null]`} {
			if err := callJSONMode("echoJSON", []string{"value=" + value}); err != nil {
				t.Fatal(err)
			}
			if actual := string(<-received); actual != value {
				t.Errorf("%s became %s", value, actual)
			}
		}
	}
}

func TestCLIInferenceDoesNotTruncateText(t *testing.T) {
	for _, value := range []string{"123abc", "1.2ms", "01", "+1", "", "hello", "NaN", "Infinity"} {
		if actual := inferType(value); actual != value {
			t.Errorf("%q became %#v", value, actual)
		}
	}
	for _, value := range []string{`true`, `false`, `null`, `"123"`} {
		encoded, err := json.Marshal(inferType(value))
		if err != nil || string(encoded) != value {
			t.Errorf("%s became %s: %v", value, encoded, err)
		}
	}
}

func TestCLIBinaryJSONPreservesNumericTokens(t *testing.T) {
	for _, token := range cliNumberTokens(t) {
		for _, list := range []bool{false, true} {
			raw := `{"n":` + token + `}`
			if list {
				raw = `[` + raw + `,null]`
			}
			value, err := parseBinaryCLIValue(raw, schema.Param{Type: schema.FieldJSON, IsList: list})
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != raw {
				t.Errorf("%s became %s: %v", raw, encoded, err)
			}
			if _, err := api.EncodeBinaryRequest(1, nil, map[string]any{"value": value},
				[]api.ParamMeta{{Name: "value", FieldID: 1, Type: "JSON", IsList: list}}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestCLIBinaryIntegerArraysUseExactValues(t *testing.T) {
	for _, fieldType := range []schema.FieldType{schema.FieldInt, schema.FieldDuration} {
		param := schema.Param{Type: fieldType, IsList: true}
		value, err := parseBinaryCLIValue(`[9007199254740993,9223372036854775807,-9223372036854775808]`, param)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := api.EncodeBinaryRequest(1, nil, map[string]any{"value": value},
			[]api.ParamMeta{{Name: "value", FieldID: 1, Type: fieldType.String(), IsList: true}})
		if err != nil {
			t.Fatal(err)
		}
		var expected codec.Encoder
		expected.WriteFieldHeader(1)
		expected.WriteArrayHeader(3)
		for _, integer := range []int64{9007199254740993, math.MaxInt64, math.MinInt64} {
			expected.WriteInt(integer)
		}
		expected.WriteEnd()
		if !bytes.Equal(wire, append([]byte{1, 0}, expected.Bytes()...)) {
			t.Errorf("wrong %s bytes: %x", fieldType, wire)
		}
		for _, invalid := range []string{`[9223372036854775808]`, `[-9223372036854775809]`, `[1.0]`, `[1e0]`, `["1"]`, `[null]`, `null`, `[1] true`} {
			if _, err := parseBinaryCLIValue(invalid, param); err == nil {
				t.Errorf("accepted %s %s", fieldType, invalid)
			}
		}
	}
}

func TestCLIBinaryFloatRejectsUnderflowWithoutNarrowingSpecialValues(t *testing.T) {
	for _, token := range []string{"1e-400", "-1e-400", "2.4703282292062327e-324", "0x1p-1075"} {
		if _, err := parseBinaryCLIValue(token, schema.Param{Type: schema.FieldFloat}); err == nil {
			t.Errorf("accepted underflow %s", token)
		}
	}
	for _, token := range []string{"-0", "-0.0e-99999", "-0x0p-99999", "-0_0.0"} {
		value, err := parseBinaryCLIValue(token, schema.Param{Type: schema.FieldFloat})
		if err != nil || math.Float64bits(value.(float64)) != 1<<63 {
			t.Errorf("lost negative zero %s: %v", token, err)
		}
	}
	for _, token := range []string{"NaN", "+Inf", "-Inf"} {
		if _, err := parseBinaryCLIValue(token, schema.Param{Type: schema.FieldFloat}); err != nil {
			t.Fatal(err)
		}
	}
	for _, token := range []string{`[1e-400]`, `["1"]`, `[null]`} {
		if _, err := parseBinaryCLIValue(token, schema.Param{Type: schema.FieldFloat, IsList: true}); err == nil {
			t.Errorf("accepted %s", token)
		}
	}
}

func TestCLIBinaryScalarListsAndNullability(t *testing.T) {
	for _, item := range []struct {
		field schema.FieldType
		raw   string
		want  any
	}{
		{schema.FieldInt, `[]`, []any{}},
		{schema.FieldBool, `[true,false]`, []any{true, false}},
		{schema.FieldFloat, `[1.25,5e-324]`, []any{1.25, math.SmallestNonzeroFloat64}},
		{schema.FieldString, `["", "東京"]`, []any{"", "東京"}},
		{schema.FieldBytes, `["AQI=",""]`, []any{[]byte{1, 2}, []byte{}}},
		{schema.FieldEnum, `["OPEN"]`, []any{"OPEN"}},
		{schema.FieldDateTime, `["2026-09-15T00:00:00Z"]`, []any{"2026-09-15T00:00:00Z"}},
		{schema.FieldUUID, `["01234567-89ab-cdef-0123-456789abcdef"]`, []any{"01234567-89ab-cdef-0123-456789abcdef"}},
		{schema.FieldDecimal, `["1.2300"]`, []any{"1.2300"}},
	} {
		value, err := parseBinaryCLIValue(item.raw, schema.Param{Type: item.field, IsList: true})
		if err != nil || !reflect.DeepEqual(value, item.want) {
			t.Errorf("%s = %#v: %v", item.raw, value, err)
		}
		for _, list := range []bool{false, true} {
			value, err = parseBinaryCLIValue(" null ", schema.Param{Type: item.field, IsList: list, Nullable: true})
			if err != nil || value != nil {
				t.Errorf("nullable %s = %#v: %v", item.field, value, err)
			}
		}
	}
	for _, item := range []struct {
		field schema.FieldType
		raw   string
		list  bool
	}{
		{schema.FieldInt, `wrong`, false}, {schema.FieldFloat, `wrong`, false},
		{schema.FieldJSON, `{"n":`, false}, {schema.FieldInt, `1`, true},
		{schema.FieldBool, `[1]`, true}, {schema.FieldString, `[1]`, true},
		{schema.FieldBytes, `["***"]`, true}, {schema.FieldModel, `["unsupported"]`, true},
		{schema.FieldModel, `unsupported`, false},
	} {
		if _, err := parseBinaryCLIValue(item.raw, schema.Param{Type: item.field, IsList: item.list}); err == nil {
			t.Errorf("accepted %s %s", item.field, item.raw)
		}
	}
	if value, err := parseBinaryCLIValue("true", schema.Param{Type: schema.FieldBool}); err != nil || value != true {
		t.Fatal(value, err)
	}
	if value, err := parseBinaryCLIValue("null", schema.Param{Type: schema.FieldJSON}); err != nil || string(value.(json.RawMessage)) != "null" {
		t.Fatal(value, err)
	}
}

func TestCLIParamSplitsOnlyTheFirstEquals(t *testing.T) {
	for _, item := range [][3]string{{"name", "name", ""}, {"value=a=b", "value", "a=b"}, {"name=", "name", ""}} {
		key, value := parseParam(item[0])
		if key != item[1] || value != item[2] {
			t.Fatal(key, value)
		}
	}
}
