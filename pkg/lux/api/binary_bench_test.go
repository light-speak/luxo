package api

import (
	"testing"

	"github.com/light-speak/luxo/pkg/lux/schema"
)

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
	registry.SetSchema(s)
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
