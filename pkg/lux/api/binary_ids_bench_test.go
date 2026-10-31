package api

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

var binaryIDRequestSink []byte

// Run this same harness against the current source and a frozen source overlay
// with only the classified ID checks removed. Setup never enters the timer.
func BenchmarkBinaryRequestIDBounds(b *testing.B) {
	for _, item := range []struct {
		name              string
		apiID, parameters int
		maximum, selected bool
	}{
		{name: "empty", apiID: 1},
		{name: "one", apiID: 1, parameters: 1},
		{name: "maximum", apiID: codec.MaxAPIID, parameters: 1, maximum: true},
		{name: "sixteen", apiID: 1, parameters: 16},
		{name: "selected", apiID: 1, parameters: 16, selected: true},
	} {
		b.Run(item.name, func(b *testing.B) {
			registry := NewAPIRegistry()
			registry.Register("read", item.apiID)
			meta, values := binaryIDParameters(item.parameters, item.maximum)
			registry.RegisterParams("read", meta)
			var mask []byte
			if item.selected {
				s, model, selection := selectionFixture(b, 16, 16, 0)
				s.RegisterAPI(&schema.API{ID: item.apiID, Name: "read", ReturnType: model.Name})
				if err := registry.SetSchema(s); err != nil {
					b.Fatal(err)
				}
				mask = selection
			}
			body, err := EncodeBinaryRequest(item.apiID, mask, values, meta)
			if err != nil {
				b.Fatal(err)
			}
			b.Run("encode", func(b *testing.B) { benchmarkIDEncode(b, item.apiID, mask, values, meta, body) })
			b.Run("decode", func(b *testing.B) { benchmarkIDDecode(b, registry, body, item.parameters) })
			b.Run("omitted", func(b *testing.B) { benchmarkIDEncode(b, item.apiID, mask, nil, meta, nil) })
		})
	}
}

func binaryIDParameters(count int, maximum bool) ([]ParamMeta, map[string]any) {
	meta := make([]ParamMeta, count)
	values := make(map[string]any, count)
	for i := range count {
		id := i + 1
		if maximum {
			id = codec.MaxParameterID
		}
		name := fmt.Sprintf("p%d", i)
		meta[i] = ParamMeta{Name: name, Type: "Int", FieldID: id}
		values[name] = int64(i + 1)
	}
	return meta, values
}

func benchmarkIDEncode(b *testing.B, id int, mask []byte, values map[string]any, meta []ParamMeta, expected []byte) {
	b.Helper()
	got, err := EncodeBinaryRequest(id, mask, values, meta)
	if err != nil || (expected != nil && !bytes.Equal(got, expected)) {
		b.Fatalf("wire changed: %x / %x / %v", got, expected, err)
	}
	b.ReportAllocs()
	for b.Loop() {
		binaryIDRequestSink, err = EncodeBinaryRequest(id, mask, values, meta)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkIDDecode(b *testing.B, registry *APIRegistry, body []byte, count int) {
	b.Helper()
	var req Request
	if err := registry.ParseBinaryRequestInto(body, &req); err != nil || req.API != "read" {
		b.Fatalf("request changed: %s / %v", req.API, err)
	}
	for i := range count {
		if value, err := req.ParamInt(fmt.Sprintf("p%d", i)); err != nil || value != int64(i+1) {
			b.Fatalf("parameter changed: %d / %v", value, err)
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := registry.ParseBinaryRequestInto(body, &req); err != nil {
			b.Fatal(err)
		}
	}
}
