package api

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

// Registration only: names and the application binding already exist. The
// request path does not allocate these route holders or their lookup map.
func BenchmarkResponseCacheRouteRegistration(b *testing.B) {
	for _, count := range []int{1, 16, 128} {
		b.Run(fmt.Sprintf("routes=%d", count), func(b *testing.B) {
			binding := cacheDependencyRouter(b).responseCache
			names := make([]string, count)
			for i := range names {
				names[i] = fmt.Sprintf("cached%d", i)
			}
			handler := func(context.Context, *Request) error { return nil }
			b.ReportAllocs()
			for b.Loop() {
				rt := Router{responseCache: binding, handlers: make(map[string]handlerRoute, count)}
				for _, name := range names {
					rt.Handle(name, handler)
				}
				if len(rt.cacheBindings) != count {
					b.Fatal("missing route bindings")
				}
			}
		})
	}
}

// Only validation is timed: backend construction and schema registration are
// excluded. Shared return types should build just one selection graph per run.
func BenchmarkCacheDependencyStartup(b *testing.B) {
	for _, count := range []int{1, 16, 128} {
		b.Run(fmt.Sprintf("apis=%d", count), func(b *testing.B) {
			rt := cacheDependencyRouter(b)
			for i := 1; i < count; i++ {
				name := fmt.Sprintf("cached%d", i)
				rt.Schema.RegisterAPI(&schema.API{ID: i + 1, Name: name, Module: "s", ReturnType: "User", CachePolicy: testPrivateCachePolicy(time.Minute), CacheDependencies: &schema.CacheDependencies{Models: []schema.ComputedSource{{Model: "User", Module: "s"}}}})
				rt.RequireCache(name)
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := rt.Validate(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// This collector is not yet part of CacheResponsePolicy; these absolute local costs
// do not prove complete cache hit/miss performance or invalidation readiness.
func BenchmarkCacheDependencyCollection(b *testing.B) {
	rt := cacheDependencyRouter(b)
	if err := rt.Validate(); err != nil {
		b.Fatal(err)
	}
	plan := rt.cachePlans["cached"]
	for _, selected := range []string{"id", "postCount, posts { id }"} {
		fields, err := selection.Parse(selected)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(selected, func(b *testing.B) {
			dst := make([]schema.ComputedSource, 0, 2)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := plan.appendModels(dst, fields); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkCacheExecutionDependencyCollection(b *testing.B) {
	for _, count := range []int{1, 16, 128} {
		b.Run(fmt.Sprintf("models=%d", count), func(b *testing.B) {
			rt := cacheDependencyRouter(b)
			definition := rt.Schema.APIs["cached"]
			definition.ReturnType = "Int"
			definition.CacheDependencies.Models = nil
			for i := range count {
				name := fmt.Sprintf("Model%d", i)
				rt.Schema.RegisterModel(&schema.Model{Name: name, Module: "s"})
				definition.CacheDependencies.Models = append(definition.CacheDependencies.Models, schema.ComputedSource{Model: name, Module: "s"})
			}
			if err := rt.Validate(); err != nil {
				b.Fatal(err)
			}
			plan, dst := rt.cachePlans["cached"], make([]schema.ComputedSource, 0, count)
			b.ReportAllocs()
			for b.Loop() {
				got, err := plan.appendModels(dst, nil)
				if err != nil || len(got) != count {
					b.Fatalf("got %d, %v", len(got), err)
				}
			}
		})
	}
}

// Both variants start with registered metadata; neither includes listening,
// network traffic or backend construction. The shared case must not rebuild.
func BenchmarkRouterPreparation(b *testing.B) {
	for _, shared := range []bool{false, true} {
		b.Run(fmt.Sprintf("shared=%t", shared), func(b *testing.B) {
			rt := cacheDependencyRouter(b)
			if err := rt.Prepare(); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if !shared {
					rt.prepared = false
				}
				if err := rt.Prepare(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
