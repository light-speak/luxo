package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

var canonicalIntegerSink any

func BenchmarkBinaryJSONRawRequest(b *testing.B) {
	for _, sample := range []struct{ name, raw string }{
		{"small", `{"ok":true}`},
		{"precision", `{"integer":123456789012345678901234567890,"fraction":1.234567890123456789}`},
		{"unicode1KiB", `"` + strings.Repeat("中文", 171) + `"`},
		{"unicode72KiB", `"` + strings.Repeat("中文", 12288) + `"`},
		{"escaped128", `"` + strings.Repeat(`\uD800\uDC00`, 128) + `"`},
	} {
		b.Run(sample.name, func(b *testing.B) {
			document := json.RawMessage(sample.raw)
			meta := []ParamMeta{{Name: "document", Type: "JSON", FieldID: 1}}
			params := map[string]any{"document": document}
			// Independent request golden: API 1, empty mask, field 1, document, end.
			expected := codec.AppendBytes([]byte{1, 0, 1}, document)
			expected = append(expected, 0)
			b.ReportAllocs()
			for b.Loop() {
				body, err := EncodeBinaryRequest(1, nil, params, meta)
				if err != nil || !bytes.Equal(body, expected) {
					b.Fatal("raw JSON request changed", err)
				}
			}
		})
	}
}

func BenchmarkBinaryJSONDocuments(b *testing.B) {
	meta := []ParamMeta{{Name: "values", Type: "JSON", FieldID: 1, IsList: true}}
	for _, count := range []int{0, 1, 128} {
		documents := make([]json.RawMessage, count)
		boxed := make([]any, count)
		for index := range documents {
			documents[index] = []byte(`{"n":123456789012345678901234567890,"d":1.234567890123456789,"text":"東京"}`)
			boxed[index] = documents[index]
		}
		var expected []byte
		for _, item := range []struct {
			name  string
			value any
		}{{"boxed", boxed}, {"typed", documents}} {
			params := map[string]any{"values": item.value}
			b.Run(fmt.Sprintf("%d/%s", count, item.name), func(b *testing.B) {
				body, err := EncodeBinaryRequest(9, nil, params, meta)
				if err != nil {
					b.Fatal(err)
				}
				if expected == nil {
					expected = body
				}
				if !bytes.Equal(body, expected) {
					b.Fatal("typed and boxed documents produced different wire")
				}
				b.ReportAllocs()
				for b.Loop() {
					body, err := EncodeBinaryRequest(9, nil, params, meta)
					if err != nil || !bytes.Equal(body, expected) {
						b.Fatal("JSON document request changed", err)
					}
				}
			})
		}
	}
}

func BenchmarkCanonicalJSONStructured(b *testing.B) {
	for _, count := range []int{-1, 0, 1, 128} {
		s := schema.New()
		s.RegisterType(&schema.TypeDecl{Name: "Sample", Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "name", Type: schema.FieldString},
		}})
		raw := `{"id":9223372036854775807,"name":"東京"}`
		if count == 0 {
			raw = "[]"
		} else if count > 0 {
			raw = "[" + strings.Repeat(raw+",", count-1) + raw + "]"
		}
		meta := ParamMeta{Name: "value", Type: "Model", TypeName: "Sample", FieldID: 1, IsList: count >= 0}
		req := &Request{API: "echo", Params: map[string]json.RawMessage{"value": []byte(raw)}}
		registry := NewAPIRegistry()
		if err := registry.SetSchema(s); err != nil {
			b.Fatal(err)
		}
		registry.Register("echo", 1)
		registry.RegisterParams("echo", []ParamMeta{meta})
		b.Run(fmt.Sprintf("%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := registry.prepareJSONRequest(req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkCanonicalJSONTyped(b *testing.B) {
	for _, item := range []struct {
		name, kind, token string
	}{
		{"string", "String", `"東京"`},
		{"escaped", "String", `"\u6771\u4eac"`},
		{"boolean", "Boolean", "true"},
		{"date", "DateTime", `"2026-09-17T01:02:03Z"`},
		{"uuid", "UUID", `"550e8400-e29b-41d4-a716-446655440000"`},
		{"decimal", "Decimal", `"12345678901234567890.12345678901234567890"`},
		{"enum", "Enum", `"ACTIVE"`},
		{"unique", "String", `"東京0"`},
		{"long1KB", "String", `"` + strings.Repeat("x", 1024) + `"`},
		{"chinese72KB", "String", `"` + strings.Repeat("東京", 12000) + `"`},
	} {
		for _, count := range []int{-1, 0, 1, 128} {
			if item.name == "chinese72KB" && count >= 0 {
				continue
			}
			raw := item.token
			if count >= 0 {
				raw = "[]"
				if count > 0 {
					raw = "[" + strings.Repeat(item.token+",", count-1) + item.token + "]"
					if item.name == "unique" {
						raw = benchmarkStringArray(count)
					}
				}
			}
			meta := ParamMeta{Name: "value", Type: item.kind, FieldID: 1, IsList: count >= 0}
			req := &Request{API: "echo", Params: map[string]json.RawMessage{"value": []byte(raw)}}
			registry := NewAPIRegistry()
			registry.Register("echo", 1)
			registry.RegisterParams("echo", []ParamMeta{meta})
			b.Run(fmt.Sprintf("%s/%d", item.name, count), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if err := registry.prepareJSONRequest(req); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkCanonicalJSONBytes(b *testing.B) {
	for _, item := range []struct {
		name, raw string
		list      bool
	}{
		{"empty", `""`, false},
		{"small", `"AAECA/8="`, false},
		{"escaped", `"\u0041QI="`, false},
		{"long72KB", `"` + base64.StdEncoding.EncodeToString(make([]byte, 72000)) + `"`, false},
		{"escaped4096", `"` + strings.Repeat(`\u0041`, 4096) + `"`, false},
		{"list0", "[]", true},
		{"list1", `["AQI="]`, true},
		{"list128", "[" + strings.Repeat(`"AQI=",`, 127) + `"AQI="]`, true},
		{"escapedList128", "[" + strings.Repeat(`"\u0041QI=",`, 127) + `"\u0041QI="]`, true},
	} {
		meta := ParamMeta{Name: "value", Type: "Bytes", FieldID: 1, IsList: item.list}
		req := &Request{API: "echo", Params: map[string]json.RawMessage{"value": []byte(item.raw)}}
		registry := NewAPIRegistry()
		registry.Register("echo", 1)
		registry.RegisterParams("echo", []ParamMeta{meta})
		b.Run(item.name+"/decode", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				value, present, err := registry.jsonParamValue(req, meta)
				if err != nil || !present {
					b.Fatalf("decode failed: %v", err)
				}
				canonicalIntegerSink = value
			}
		})
		b.Run(item.name+"/canonical", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := registry.prepareJSONRequest(req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkCanonicalJSONFloat(b *testing.B) {
	for _, count := range []int{0, 1, 16, 128} {
		raw := []byte("1.25")
		meta := ParamMeta{Name: "value", Type: "Float", FieldID: 1, IsList: count != 0}
		if count != 0 {
			raw = []byte("[" + strings.Repeat("1.25,", count-1) + "1.25]")
		}
		req := &Request{Params: map[string]json.RawMessage{"value": raw}}
		var expected []byte
		for _, version := range []struct {
			name   string
			decode func(*Request, ParamMeta) (any, bool, error)
		}{{"before", beforeOpaqueJSONValue}, {"after", NewAPIRegistry().jsonParamValue}} {
			b.Run(fmt.Sprintf("%d/%s", count, version.name), func(b *testing.B) {
				var enc codec.Encoder
				write := func() {
					value, present, err := version.decode(req, meta)
					if err != nil || !present {
						b.Fatalf("param: %v", err)
					}
					enc.Reset()
					if err := encodeBinaryParam(&enc, meta, value); err != nil {
						b.Fatal(err)
					}
				}
				write()
				if version.name == "before" {
					expected = bytes.Clone(enc.Bytes())
				} else if !bytes.Equal(enc.Bytes(), expected) {
					b.Fatal("different output")
				}
				b.ReportAllocs()
				for b.Loop() {
					write()
				}
			})
		}
	}
}

type opaqueJSONBenchmarkCase struct {
	name string
	raw  string
	list bool
}

func BenchmarkCanonicalJSONValue(b *testing.B) {
	object := `{"count":42,"ratio":1.25,"text":"東京"}`
	cases := []opaqueJSONBenchmarkCase{
		{"integer", "42", false}, {"object", object, false}, {"list0", "[]", true},
		{"integerList1", "[42]", true}, {"stringList1", `["東京"]`, true},
	}
	for _, count := range []int{1, 16, 128} {
		cases = append(cases, opaqueJSONBenchmarkCase{
			fmt.Sprintf("list%d", count), "[" + strings.Repeat(object+",", count-1) + object + "]", true,
		})
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) { benchmarkCanonicalJSONValue(b, tc) })
	}
}

func benchmarkCanonicalJSONValue(b *testing.B, tc opaqueJSONBenchmarkCase) {
	meta := ParamMeta{Name: "value", Type: "JSON", FieldID: 1, IsList: tc.list}
	req := &Request{Params: map[string]json.RawMessage{"value": json.RawMessage(tc.raw)}}
	var expected []byte
	for _, version := range []struct {
		name   string
		decode func(*Request, ParamMeta) (any, bool, error)
		encode func(*codec.Encoder, ParamMeta, any) error
	}{{"before", beforeOpaqueJSONValue, encodeOpaqueJSONReference}, {"after", NewAPIRegistry().jsonParamValue, encodeBinaryParam}} {
		b.Run(version.name, func(b *testing.B) {
			var enc codec.Encoder
			write := func() {
				value, present, err := version.decode(req, meta)
				if err != nil || !present {
					b.Fatalf("decode: present %v, error %v", present, err)
				}
				enc.Reset()
				if err := version.encode(&enc, meta, value); err != nil {
					b.Fatal(err)
				}
			}
			write()
			if version.name == "before" {
				expected = bytes.Clone(enc.Bytes())
			} else if !bytes.Equal(enc.Bytes(), expected) {
				b.Fatal("comparison requires identical valid output")
			}
			b.ReportAllocs()
			for b.Loop() {
				write()
			}
		})
	}
}

// Preserve the former reflective encoder only for the non-null JSON benchmark.
// The production request encoder intentionally no longer accepts these values.
func encodeOpaqueJSONReference(enc *codec.Encoder, meta ParamMeta, value any) error {
	if meta.Type != "JSON" || meta.Nullable || meta.FieldID <= 0 {
		return fmt.Errorf("unsupported historical benchmark metadata")
	}
	enc.WriteFieldHeader(meta.FieldID)
	values := []any{value}
	if meta.IsList {
		var ok bool
		values, ok = value.([]any)
		if !ok {
			return fmt.Errorf("expected historical JSON list")
		}
		enc.WriteArrayHeader(len(values))
	}
	for _, item := range values {
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		enc.WriteBytes(raw)
	}
	return nil
}

// Frozen JSON-specific normalization; compare only exactly representable values.
func beforeOpaqueJSONValue(req *Request, meta ParamMeta) (any, bool, error) {
	var value any
	err := json.Unmarshal(req.Params[meta.Name], &value)
	return value, err == nil, err
}

func BenchmarkCanonicalJSONInteger(b *testing.B) {
	for _, count := range []int{0, 1, 16, 128} {
		raw := []byte("9007199254740991")
		meta := ParamMeta{Name: "value", Type: "Duration", IsList: count != 0}
		if count != 0 {
			raw = []byte("[" + strings.Repeat("9007199254740991,", count-1) + "9007199254740991]")
		}
		for _, version := range []struct {
			name   string
			decode func([]byte, ParamMeta) (any, error)
		}{{"before", beforeJSONInteger}, {"after", decodeJSONIntegerParam}} {
			b.Run(fmt.Sprintf("%d/%s", count, version.name), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					value, err := version.decode(raw, meta)
					if err != nil {
						b.Fatal(err)
					}
					canonicalIntegerSink = value
				}
			})
		}
	}
}

func BenchmarkRegisteredJSONIntegerBoundary(b *testing.B) {
	for _, item := range []struct {
		name, raw string
		list      bool
	}{
		{"zero", "0", false},
		{"small", "42", false},
		{"max", "9223372036854775807", false},
		{"min", "-9223372036854775808", false},
		{"padded", " \t\r\n-9223372036854775808 \t\r\n", false},
		{"padded128", "[" + strings.Repeat(" \t-42 \r\n,", 127) + " \t-42 \r\n]", true},
		{"mixed128", "[" + strings.Repeat("0,42,-9223372036854775808,9223372036854775807,", 31) + "0,42,-9223372036854775808,9223372036854775807]", true},
	} {
		b.Run(item.name, func(b *testing.B) {
			raw := []byte(item.raw)
			meta := ParamMeta{Name: "value", Type: "Int", IsList: item.list}
			b.ReportAllocs()
			for b.Loop() {
				value, err := decodeJSONIntegerParam(raw, meta)
				if err != nil {
					b.Fatal(err)
				}
				canonicalIntegerSink = value
			}
		})
	}
}

// Frozen former JSON Duration normalization, only measured on exactly representable inputs.
func beforeJSONInteger(raw []byte, meta ParamMeta) (any, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	if !meta.IsList {
		converted, valid := binaryInt(value)
		if !valid {
			return nil, fmt.Errorf("invalid duration")
		}
		return converted, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("invalid duration array")
	}
	values := make([]int64, len(items))
	for i, item := range items {
		converted, valid := binaryInt(item)
		if !valid {
			return nil, fmt.Errorf("invalid duration item")
		}
		values[i] = converted
	}
	return values, nil
}

func BenchmarkParseBinaryRequestPaginatedDefaults(b *testing.B) {
	registry := NewAPIRegistry()
	registry.Register("browseUsers", 1)
	registry.RegisterParams("browseUsers", []ParamMeta{
		{Name: "page", Type: "Int", FieldID: 1},
		{Name: "pageSize", Type: "Int", FieldID: 2},
	})
	s := schema.New()
	s.RegisterAPI(&schema.API{
		ID: 1, Name: "browseUsers", Paginated: true, DefaultPageSize: 50,
		Params: []schema.Param{
			{ID: 1, Name: "page", Type: schema.FieldInt, HasDefault: true},
			{ID: 2, Name: "pageSize", Type: schema.FieldInt, HasDefault: true},
		},
	})
	if err := registry.SetSchema(s); err != nil {
		b.Fatal(err)
	}
	body := []byte{1, 0, 0}

	b.ReportAllocs()
	for b.Loop() {
		request, err := registry.ParseBinaryRequest(body)
		if err != nil {
			b.Fatal(err)
		}
		if request.PageSize != 50 {
			b.Fatalf("page size = %d, want 50", request.PageSize)
		}
	}
}

func BenchmarkParseBinaryRequestIntoPaginatedDefaults(b *testing.B) {
	registry := NewAPIRegistry()
	registry.Register("browseUsers", 1)
	registry.RegisterParams("browseUsers", []ParamMeta{
		{Name: "page", Type: "Int", FieldID: 1},
		{Name: "pageSize", Type: "Int", FieldID: 2},
	})
	s := schema.New()
	s.RegisterAPI(&schema.API{
		ID: 1, Name: "browseUsers", Paginated: true, DefaultPageSize: 50,
		Params: []schema.Param{
			{ID: 1, Name: "page", Type: schema.FieldInt, HasDefault: true},
			{ID: 2, Name: "pageSize", Type: schema.FieldInt, HasDefault: true},
		},
	})
	if err := registry.SetSchema(s); err != nil {
		b.Fatal(err)
	}
	body := []byte{1, 0, 0}
	request := new(Request)

	b.ReportAllocs()
	for b.Loop() {
		if err := registry.ParseBinaryRequestInto(body, request); err != nil {
			b.Fatal(err)
		}
		if request.PageSize != 50 {
			b.Fatalf("page size = %d, want 50", request.PageSize)
		}
	}
}
