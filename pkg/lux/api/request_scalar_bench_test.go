package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

var (
	paramIntSink     int64
	paramStringSink  string
	paramBoolSink    bool
	paramDateSink    time.Time
	paramIntsSink    []int64
	paramStringsSink []string
	paramUUIDsSink   []uuid.UUID
)

func BenchmarkRequestStaticJSONParams(b *testing.B) {
	cases := []struct {
		name, raw string
		read      func(*Request) error
	}{
		{"Int/small", "42", benchmarkReadInt},
		{"Int/max", "9223372036854775807", benchmarkReadInt},
		{"Bool", "true", benchmarkReadBool},
		{"String/short", `"hello"`, benchmarkReadString},
		{"String/Chinese72KB", `"` + strings.Repeat("東京", 12000) + `"`, benchmarkReadString},
		{"String/escaped", `"\ud83d\ude42\n\""`, benchmarkReadString},
		{"String/escaped72KB", `"` + strings.Repeat(`\u6771\u4eac`, 6000) + `"`, benchmarkReadString},
		{"DateTime", `"2026-09-17T12:34:56Z"`, benchmarkReadDate},
		{"IntArray/empty", "[]", benchmarkReadInts},
		{"IntArray/128", "[" + strings.Repeat("42,", 127) + "42]", benchmarkReadInts},
		{"StringArray/empty", "[]", benchmarkReadStrings},
		{"StringArray/128", "[" + strings.Repeat(`"東京",`, 127) + `"東京"]`, benchmarkReadStrings},
		{"StringArray/one", `["hello"]`, benchmarkReadStrings},
		{"StringArray/unique128", benchmarkStringArray(128), benchmarkReadStrings},
		{"StringArray/escaped128", "[" + strings.Repeat(`"\u6771\u4eac",`, 127) + `"\u6771\u4eac"]`, benchmarkReadStrings},
		{"StringArray/alternating128", "[" + strings.Repeat(`"東京","大阪",`, 63) + `"東京","大阪"]`, benchmarkReadStrings},
		{"StringArray/long128", "[" + strings.Repeat(`"`+strings.Repeat("x", 1024)+`",`, 127) + `"` + strings.Repeat("x", 1024) + `"]`, benchmarkReadStrings},
		{"UUIDArray/128", "[" + strings.Repeat(`"550e8400-e29b-41d4-a716-446655440000",`, 127) + `"550e8400-e29b-41d4-a716-446655440000"]`, benchmarkReadUUIDs},
		{"UUIDArray/escaped128", "[" + strings.Repeat(`"\u003550e8400-e29b-41d4-a716-446655440000",`, 127) + `"\u003550e8400-e29b-41d4-a716-446655440000"]`, benchmarkReadUUIDs},
	}
	for _, item := range cases {
		b.Run(item.name, func(b *testing.B) {
			req := &Request{Params: map[string]json.RawMessage{"v": []byte(item.raw)}}
			if err := item.read(req); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := item.read(req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchmarkStringArray(count int) string {
	values := make([]string, count)
	for i := range values {
		values[i] = `"東京` + strconv.Itoa(i) + `"`
	}
	return "[" + strings.Join(values, ",") + "]"
}

func benchmarkReadInt(req *Request) (err error) {
	paramIntSink, err = req.ParamInt("v")
	return err
}

func benchmarkReadString(req *Request) (err error) {
	paramStringSink, err = req.ParamString("v")
	return err
}

func benchmarkReadBool(req *Request) (err error) {
	paramBoolSink, err = req.ParamBool("v")
	return err
}

func benchmarkReadDate(req *Request) (err error) {
	paramDateSink, err = req.ParamDateTime("v")
	return err
}

func benchmarkReadInts(req *Request) (err error) {
	paramIntsSink, err = req.ParamIntArray("v")
	return err
}

func benchmarkReadStrings(req *Request) (err error) {
	paramStringsSink, err = req.ParamStringArray("v")
	return err
}

func benchmarkReadUUIDs(req *Request) (err error) {
	paramUUIDsSink, err = req.ParamUUIDArray("v")
	return err
}

func BenchmarkJSONRequestMetadata(b *testing.B) {
	for _, item := range []struct{ name, body string }{
		{"small", `{"$api":"test","value":42}`},
		{"select", `{"$api":"test","$select":"id, owner { name }","value":42}`},
		{"escaped", `{"$api":"\u0074est","$select":"\u0069d, owner { name }","value":42}`},
		{"page", `{"$api":"test","page":9223372036854775807,"pageSize":20,"value":42}`},
		{"paddedPage", `{"$api":"test","page": 9223372036854775807 ,"pageSize": 20 ,"value":42}`},
		{"unicode72000", `{"$api":"test","$select":"id","page":1,"value":"` + strings.Repeat("東京", 12000) + `"}`},
	} {
		b.Run(item.name, func(b *testing.B) {
			reader := strings.NewReader(item.body)
			request := &http.Request{Body: io.NopCloser(reader)}
			b.ReportAllocs()
			for b.Loop() {
				reader.Reset(item.body)
				if req, err := ParseRequest(request); err != nil || req.API != "test" {
					b.Fatalf("parse failed: %v / %v", req, err)
				}
			}
		})
	}
}

func BenchmarkRequestPaginationBoundary(b *testing.B) {
	for _, sample := range []struct {
		name, body string
		paginated  bool
	}{
		{"ordinary", `{"$api":"read","value":42}`, false},
		{"paginated", `{"$api":"read","page":3,"pageSize":7}`, true},
		{"defaults", `{"$api":"read"}`, true},
	} {
		registry := paginationBenchmarkRegistry(b, sample.paginated)
		for _, mode := range []string{"localJSON", "canonicalJSON", "binary"} {
			b.Run(sample.name+"/"+mode, func(b *testing.B) {
				benchmarkPaginationMode(b, registry, sample.body, mode)
			})
		}
	}
}

func benchmarkPaginationMode(b *testing.B, registry *APIRegistry, body, mode string) {
	reader := strings.NewReader(body)
	request := &http.Request{Body: io.NopCloser(reader)}
	prepare := registry.prepareLocalJSONRequest
	if mode == "canonicalJSON" {
		prepare = registry.prepareJSONRequest
	}
	raw, err := ParseRequest(request)
	if err != nil {
		b.Fatal(err)
	}
	if err := registry.prepareJSONRequest(raw); err != nil {
		b.Fatal(err)
	}
	wire := raw.BinaryRequest()
	var reused Request
	b.ReportAllocs()
	for b.Loop() {
		if mode == "binary" {
			err = registry.ParseBinaryRequestInto(wire, &reused)
		} else {
			reader.Reset(body)
			var req *Request
			req, err = ParseRequest(request)
			if err == nil {
				err = prepare(req)
			}
		}
		if err != nil {
			b.Fatal(err)
		}
	}
}

func paginationBenchmarkRegistry(b *testing.B, paginated bool) *APIRegistry {
	b.Helper()
	params := []schema.Param{{ID: 1, Name: "value", Type: schema.FieldInt}}
	if paginated {
		params = []schema.Param{
			{ID: 1, Name: "page", Type: schema.FieldInt, HasDefault: true},
			{ID: 2, Name: "pageSize", Type: schema.FieldInt, HasDefault: true},
		}
	}
	s := schema.New()
	if err := s.RegisterAPI(&schema.API{ID: 1, Name: "read", Params: params, Paginated: paginated, DefaultPageSize: 25}); err != nil {
		b.Fatal(err)
	}
	registry := NewAPIRegistry()
	if err := registry.RegisterSchemaAPIs(s); err != nil {
		b.Fatal(err)
	}
	return registry
}
