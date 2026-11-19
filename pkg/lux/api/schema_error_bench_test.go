package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/schema"
)

func schemaErrorBoundary(t testing.TB) (*Router, *atomic.Int32) {
	t.Helper()
	rt := NewRouter()
	if err := rt.Schema.RegisterType(&schema.TypeDecl{Name: "Node", Fields: []schema.Field{
		{ID: 1, Name: "value", Type: schema.FieldInt},
		{ID: 2, Name: "child", Type: schema.FieldModel, TypeName: "Node", Nullable: true, Relation: true},
		{ID: 3, Name: "children", Type: schema.FieldModel, TypeName: "Node", IsList: true, Nullable: true, Relation: true},
		{ID: 4, Name: "payload", Type: schema.FieldJSON, Nullable: true},
	}}); err != nil {
		t.Fatal(err)
	}
	count := new(atomic.Int32)
	for i, list := range []bool{false, true} {
		name := fmt.Sprintf("fail%d", i)
		if err := rt.Schema.RegisterAPI(&schema.API{ID: i + 1, Name: name, ReturnType: "Int", Params: []schema.Param{{ID: 1, Name: "input", Type: schema.FieldModel, TypeName: "Node", IsList: list}}}); err != nil {
			t.Fatal(err)
		}
		rt.Handle(name, func(_ context.Context, _ *Request) error { count.Add(1); return nil })
	}
	if err := rt.Registry.RegisterSchemaAPIs(rt.Schema); err != nil {
		t.Fatal(err)
	}
	return rt, count
}

func BenchmarkSchemaErrorHTTPConsumption(b *testing.B) {
	router, calls := schemaErrorBoundary(b)
	for _, sample := range []struct{ name, raw string }{
		{"model65", schemaDepthJSON(65, 0)},
		{"listModel65", strings.Repeat(`{"children":[`, 64) + `{}` + strings.Repeat(`]}`, 64)},
		{"json129", schemaDepthJSON(1, 129)},
		{"oldModel65", `{"child":` + schemaDepthJSON(64, 128) + `,"child":null}`},
		{"business", `{"value":"x"}`},
	} {
		for i, list := range []bool{false, true} {
			b.Run(fmt.Sprintf("%s/list%t", sample.name, list), func(b *testing.B) {
				raw := sample.raw
				if list {
					raw = `[{},` + raw + `]`
				}
				body := []byte(fmt.Sprintf(`{"$api":"fail%d","input":%s}`, i, raw))
				pre := httptest.NewRecorder()
				router.ServeHTTP(pre, httptest.NewRequest("POST", "/", bytes.NewReader(body)))
				if pre.Code != 400 {
					b.Fatal("invalid error fixture", pre.Code, pre.Body.String())
				}
				want := bytes.Clone(pre.Body.Bytes())
				b.Logf("response-sha256=%x", sha256.Sum256(want))
				b.ReportAllocs()
				for b.Loop() {
					response := httptest.NewRecorder()
					router.ServeHTTP(response, httptest.NewRequest("POST", "/", bytes.NewReader(body)))
					if response.Code != 400 || !bytes.Equal(response.Body.Bytes(), want) {
						b.Fatal("error response changed", response.Code)
					}
				}
			})
		}
	}
	if calls.Load() != 0 {
		b.Fatal("invalid input reached handler", calls.Load())
	}
}
