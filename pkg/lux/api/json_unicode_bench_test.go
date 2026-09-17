package api

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func BenchmarkRawJSONArrayPool(b *testing.B) {
	for _, sample := range []struct{ name, raw string }{
		{"empty", `[]`},
		{"integer", `[42]`},
		{"object", `[{"ok":true}]`},
		{"precision", `[{"n":123456789012345678901234567890,"f":0.123456789012345678901,"e":1e9999}]`},
		{"unicode72KB", `["` + strings.Repeat("中文", 12000) + `"]`},
		{"escaped128", `["` + strings.Repeat(`\uD800\uDC00`, 128) + `"]`},
		{"nested100", `[` + strings.Repeat(`[`, 100) + `0` + strings.Repeat(`]`, 100) + `]`},
		{"list128", `[` + strings.Repeat(`{"name":"café","id":42},`, 127) + `{"name":"café","id":42}]`},
	} {
		benchmarkRawJSONArrayPoolCase(b, sample.name, []byte(sample.raw))
	}
}

func benchmarkRawJSONArrayPoolCase(b *testing.B, name string, raw []byte) {
	meta := ParamMeta{Name: "value", Type: "JSON", IsList: true, FieldID: 1}
	b.Run(name+"/encode", func(b *testing.B) {
		var enc codec.Encoder
		b.ReportAllocs()
		for b.Loop() {
			enc.Reset()
			if err := encodeRawJSONArray(&enc, meta, raw); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run(name+"/canonical", func(b *testing.B) {
		registry := NewAPIRegistry()
		registry.Register("echo", 1)
		registry.RegisterParams("echo", []ParamMeta{meta})
		req := &Request{API: "echo", Params: map[string]json.RawMessage{"value": raw}}
		b.ReportAllocs()
		for b.Loop() {
			if err := registry.prepareJSONRequest(req); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Frozen pre-Unicode encoder, including its permissive decoder options. The
// shared pool/reset and output encoder are unchanged; no validation is added
// to the old path and no work is moved outside the measured loop.
func encodeRawJSONArrayBeforeUnicode(enc *codec.Encoder, meta ParamMeta, raw []byte) error {
	reader := rawJSONArrayDecoders.Get().(*rawJSONArrayDecoder)
	reader.input = *bytes.NewBuffer(raw)
	reader.decoder.Reset(&reader.input, jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
	defer releaseRawJSONArrayDecoder(reader)
	dec := &reader.decoder
	start, err := dec.ReadToken()
	if err != nil || start.Kind() != '[' {
		return fmt.Errorf("param %s: expected a JSON array", meta.Name)
	}
	var small [8][]byte
	values := small[:0]
	for dec.PeekKind() != ']' {
		value, err := dec.ReadValue()
		if err != nil {
			return fmt.Errorf("param %s[%d]: invalid JSON: %w", meta.Name, len(values), err)
		}
		if len(values) == codec.MaxArrayElements {
			return fmt.Errorf("param %s: array size exceeds limit %d", meta.Name, codec.MaxArrayElements)
		}
		end := int(dec.InputOffset())
		values = append(values, raw[end-len(value):end])
	}
	if !bytes.Equal(bytes.Trim(raw[dec.InputOffset():], " \t\r\n"), []byte("]")) {
		return fmt.Errorf("param %s: unexpected data after JSON array", meta.Name)
	}
	enc.WriteArrayHeader(len(values))
	for _, value := range values {
		enc.WriteBytes(value)
	}
	return nil
}

func BenchmarkRawJSONArrayUnicode(b *testing.B) {
	meta := ParamMeta{Name: "values", Type: "JSON", IsList: true}
	for _, sample := range []struct{ name, raw string }{
		{"empty", `[]`},
		{"small", `[{"ok":true}]`},
		{"unicode1KiB", `["` + strings.Repeat("中文", 171) + `"]`},
		{"escaped128", `["` + strings.Repeat(`\uD800\uDC00`, 128) + `"]`},
		{"list128", `[` + strings.Repeat(`{"name":"café","id":42},`, 127) + `{"name":"café","id":42}]`},
	} {
		raw := []byte(sample.raw)
		var before, after codec.Encoder
		if err := encodeRawJSONArrayBeforeUnicode(&before, meta, raw); err != nil {
			b.Fatal(err)
		}
		if err := encodeRawJSONArray(&after, meta, raw); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
			b.Fatalf("fixture %s changed: %v", sample.name, err)
		}
		for _, version := range []struct {
			name   string
			encode func(*codec.Encoder, ParamMeta, []byte) error
		}{{"before", encodeRawJSONArrayBeforeUnicode}, {"strict", encodeRawJSONArray}} {
			b.Run(sample.name+"/"+version.name, func(b *testing.B) {
				var enc codec.Encoder
				b.ReportAllocs()
				for b.Loop() {
					enc.Reset()
					if err := version.encode(&enc, meta, raw); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
