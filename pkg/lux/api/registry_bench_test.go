package api

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/schema"
)

var registryBenchmarkSink *APIRegistry

// Catalog construction is excluded. Compare with an overlay that restores only
// the former registration behavior; request codecs and validation stay identical.
func BenchmarkRegistryRegistration(b *testing.B) {
	for _, size := range []struct{ apis, params, types int }{{1, 1, 0}, {16, 16, 0}, {16, 16, 16}, {128, 16, 16}} {
		s, names, params := registryBenchmarkCatalog(b, size.apis, size.params, size.types)
		name := fmt.Sprintf("apis=%d/params=%d/types=%d", size.apis, size.params, size.types)
		b.Run(name+"/individual", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				r := NewAPIRegistry()
				if err := installRegistryBenchmark(r, names, params); err != nil {
					b.Fatal(err)
				}
				registryBenchmarkSink = r
			}
		})
		b.Run(name+"/batch", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				r := NewAPIRegistry()
				if err := r.RegisterSchemaAPIs(s); err != nil {
					b.Fatal(err)
				}
				registryBenchmarkSink = r
			}
		})
		b.Run(name+"/repeat", func(b *testing.B) {
			r := NewAPIRegistry()
			if err := installRegistryBenchmark(r, names, params); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := installRegistryBenchmark(r, names, params); err != nil {
					b.Fatal(err)
				}
			}
			registryBenchmarkSink = r
		})
	}
}

func installRegistryBenchmark(r *APIRegistry, names []string, params [][]ParamMeta) error {
	for i, name := range names {
		if err := r.Register(name, i+1); err != nil {
			return err
		}
		if err := r.RegisterParams(name, params[i]); err != nil {
			return err
		}
	}
	return nil
}

func registryBenchmarkCatalog(tb testing.TB, count, width, types int) (*schema.Schema, []string, [][]ParamMeta) {
	tb.Helper()
	s := schema.New()
	names, metadata := make([]string, count), make([][]ParamMeta, count)
	for i := range count {
		name := fmt.Sprintf("read%d", i)
		names[i] = name
		params := make([]schema.Param, width)
		for j := range width {
			params[j] = schema.Param{Name: fmt.Sprintf("p%d", j), ID: j + 1, Type: schema.FieldInt, TypeName: "Int"}
		}
		definition := &schema.API{Name: name, ID: i + 1, Params: params, ReturnType: "Int"}
		if err := s.RegisterAPI(definition); err != nil {
			tb.Fatal(err)
		}
		metadata[i] = schemaParamMetadata(params)
	}
	for i := range types {
		decl := &schema.TypeDecl{Name: fmt.Sprintf("Payload%d", i), Fields: make([]schema.Field, width)}
		for j := range width {
			decl.Fields[j] = schema.Field{Name: fmt.Sprintf("field%d", j), ID: j + 1, Type: schema.FieldInt}
		}
		if err := s.RegisterType(decl); err != nil {
			tb.Fatal(err)
		}
	}
	return s, names, metadata
}

// This is a warm, reusable-request control, not a complete HTTP/RPC benchmark.
func BenchmarkRegistrySteadyRequest(b *testing.B) {
	for _, width := range []int{1, 16} {
		s, names, params := registryBenchmarkCatalog(b, 16, width, 0)
		r := NewAPIRegistry()
		if err := r.RegisterSchemaAPIs(s); err != nil {
			b.Fatal(err)
		}
		values := make(map[string]any, width)
		raw := make(map[string]json.RawMessage, width)
		for i, param := range params[0] {
			values[param.Name] = int64(i + 1)
			raw[param.Name] = strconv.AppendInt(nil, int64(i+1), 10)
		}
		body, err := EncodeBinaryRequest(1, nil, values, params[0])
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("params=%d/binary", width), func(b *testing.B) {
			var req Request
			if err := r.ParseBinaryRequestInto(body, &req); err != nil || req.API != names[0] {
				b.Fatalf("invalid fixture: %s / %v", req.API, err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := r.ParseBinaryRequestInto(body, &req); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("params=%d/json", width), func(b *testing.B) {
			req := &Request{API: names[0], Params: raw}
			b.ReportAllocs()
			for b.Loop() {
				if err := r.prepareJSONRequest(req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
