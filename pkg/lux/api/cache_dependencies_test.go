package api

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/cache"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

func cacheDependencyRouter(t testing.TB) *Router {
	t.Helper()
	rt := NewRouterWithOptions(RouterOptions{ResponseCache: testResponseCache(t, cache.Nop{}, "s")})
	rt.Schema.RegisterModel(&schema.Model{Name: "User", Module: "s", Fields: []schema.Field{
		{ID: 1, Name: "id", Type: schema.FieldInt},
		{ID: 2, Name: "posts", Type: schema.FieldModel, TypeName: "Post", Relation: true},
		{ID: 3, Name: "postCount", Type: schema.FieldInt, Computed: true},
	}, ComputedSources: map[string]schema.ComputedSource{"postCount": {Model: "Post", Module: "content"}}})
	rt.Schema.RegisterModel(&schema.Model{Name: "Post", Module: "content", Fields: []schema.Field{{ID: 1, Name: "id", Type: schema.FieldInt}}})
	rt.Schema.RegisterAPI(&schema.API{ID: 1, Name: "cached", Module: "s", ReturnType: "User", CachePolicy: testPrivateCachePolicy(time.Minute), CacheDependencies: &schema.CacheDependencies{
		Models: []schema.ComputedSource{{Model: "User", Module: "s"}},
	}})
	rt.RequireCache("cached")
	return rt
}

func TestCacheDependenciesPreparedAtStartup(t *testing.T) {
	rt := cacheDependencyRouter(t)
	rt.Schema.APIs["cached"].CacheDependencies.Models = append(rt.Schema.APIs["cached"].CacheDependencies.Models, schema.ComputedSource{Model: "User", Module: "s"})
	rt.Schema.RegisterAPI(&schema.API{ID: 2, Name: "other", Module: "s", ReturnType: "User", CachePolicy: testPrivateCachePolicy(time.Minute), CacheDependencies: &schema.CacheDependencies{}})
	rt.RequireCache("other")
	if err := rt.Validate(); err != nil {
		t.Fatal(err)
	}
	plan := rt.cachePlans["cached"]
	if plan.selection != rt.cachePlans["other"].selection {
		t.Fatal("same return type rebuilt its selection graph")
	}
	selected, err := selection.Parse("id, postCount, posts { id }")
	if err != nil {
		t.Fatal(err)
	}
	got, err := plan.appendModels(nil, selected)
	want := []schema.ComputedSource{{Model: "User", Module: "s"}, {Model: "Post", Module: "content"}}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("got %v/%v, want %v", got, err, want)
	}
	got, err = plan.appendModels(want[:1:1], selected)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("existing source repeated: %v/%v", got, err)
	}
	got, err = plan.appendModels(nil, []*selection.Field{{Name: "id"}})
	if err != nil || !slices.Equal(got, want[:1]) {
		t.Fatalf("unselected source leaked: %v/%v", got, err)
	}
	if _, err := plan.appendModels(nil, nil); err == nil {
		t.Fatal("structured response accepted missing selection")
	}
	// Caller-owned registration data cannot alter an already prepared plan.
	rt.Schema.APIs["cached"].CacheDependencies.Models[0].Model = "Changed"
	got, err = plan.appendModels(nil, []*selection.Field{{Name: "id"}})
	if err != nil || !slices.Equal(got, want[:1]) {
		t.Fatalf("plan retained mutable registration data: %v/%v", got, err)
	}
}

func TestIntrospectionCannotEnableCacheExecution(t *testing.T) {
	rt := cacheDependencyRouter(t)
	var decoded schema.Schema
	if err := json.Unmarshal([]byte(`{"apis":{"cached":{"id":1,"name":"cached","returnType":"Int","CacheDependencies":{},"cacheDependencies":{}}}}`), &decoded); err != nil {
		t.Fatal(err)
	}
	rt.Schema = &decoded
	if err := rt.Validate(); err == nil || !strings.Contains(err.Error(), "dependency") {
		t.Fatalf("introspection forged checked execution: %v", err)
	}
}

func TestCacheDependenciesPreserveCallerSources(t *testing.T) {
	rt := cacheDependencyRouter(t)
	if err := rt.Validate(); err != nil {
		t.Fatal(err)
	}
	plan := rt.cachePlans["cached"]
	prefix := []schema.ComputedSource{{Model: "Existing", Module: "outside"}}
	got, err := plan.appendModels(prefix, []*selection.Field{{Name: "id"}})
	if err != nil || len(got) != 2 || got[0] != prefix[0] || got[1].Model != "User" {
		t.Fatalf("lost prefix or execution read: %v/%v", got, err)
	}
	got, err = plan.appendModels(prefix, nil)
	if err == nil || !slices.Equal(got, prefix) {
		t.Fatalf("partial result escaped failed collection: %v/%v", got, err)
	}
}

func TestRouterPrepareRetriesFailureAndSharesSuccessfulSnapshot(t *testing.T) {
	rt := cacheDependencyRouter(t)
	binding := rt.responseCache
	rt.responseCache = nil
	if err := rt.Prepare(); err == nil {
		t.Fatal("unconfigured router prepared")
	}
	rt.responseCache = binding
	plans := make(chan *cacheDependencyPlan, 16)
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			if err := rt.Prepare(); err != nil {
				t.Error(err)
				return
			}
			plans <- rt.cachePlans["cached"]
		})
	}
	workers.Wait()
	close(plans)
	first := rt.cachePlans["cached"]
	for plan := range plans {
		if plan == nil || plan != first {
			t.Fatal("concurrent startup rebuilt the snapshot")
		}
	}
}

func TestCacheDependenciesRejectIncompleteStartup(t *testing.T) {
	for _, test := range []struct {
		name    string
		change  func(*Router)
		message string
	}{
		{"missing schema", func(rt *Router) { rt.Schema = nil }, "schema"},
		{"missing API", func(rt *Router) { delete(rt.Schema.APIs, "cached") }, "cached"},
		{"missing analysis", func(rt *Router) { rt.Schema.APIs["cached"].CacheDependencies = nil }, "dependency"},
		{"unknown execution model", func(rt *Router) { rt.Schema.APIs["cached"].CacheDependencies.Models[0].Model = "Unknown" }, "Unknown"},
		{"wrong model owner", func(rt *Router) { rt.Schema.APIs["cached"].CacheDependencies.Models[0].Module = "other" }, "module"},
		{"missing computed source", func(rt *Router) { rt.Schema.Models["User"].ComputedSources = nil }, "postCount"},
		{"unknown return type", func(rt *Router) { rt.Schema.APIs["cached"].ReturnType = "Unknown" }, "Unknown"},
		{"stream", func(rt *Router) { rt.Schema.APIs["cached"].Stream = true }, "stream"},
		{"missing stream handler", func(rt *Router) { rt.RequireStream("updates") }, "stream"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rt := cacheDependencyRouter(t)
			test.change(rt)
			if err := rt.Validate(); err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("bad startup accepted: %v", err)
			}
			if len(rt.cachePlans) != 0 {
				t.Fatal("published partial cache plans")
			}
		})
	}
}

func TestCacheDependenciesAllRequirementsChecked(t *testing.T) {
	rt := cacheDependencyRouter(t)
	rt.RequireCache("zzMissing")
	if err := rt.Validate(); err == nil || !strings.Contains(err.Error(), "zzMissing") {
		t.Fatalf("only checked first cached API: %v", err)
	}
	if len(rt.cachePlans) != 0 {
		t.Fatal("partial plans published")
	}
	delete(rt.requiredCache, "zzMissing")
	rt.Schema.APIs["cached"].ReturnType = "Int"
	if err := rt.Validate(); err != nil {
		t.Fatal(err)
	}
	got, err := rt.cachePlans["cached"].appendModels(nil, nil)
	if err != nil || len(got) != 1 || got[0].Model != "User" {
		t.Fatalf("scalar count lost execution read: %v/%v", got, err)
	}
	for _, name := range []string{"", "String", "Boolean", "Float", "Duration", "DateTime", "UUID", "Decimal", "JSON", "Bytes", "Role"} {
		rt.Schema.RegisterEnum(&schema.Enum{Name: "Role", Values: []string{"ADMIN"}})
		rt.Schema.APIs["cached"].ReturnType = name
		rt.Schema.APIs["cached"].CacheDependencies = &schema.CacheDependencies{}
		if err := rt.Validate(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err = rt.cachePlans["cached"].appendModels(nil, nil)
		if err != nil || len(got) != 0 {
			t.Fatalf("pure response got %v/%v", got, err)
		}
	}
}
