package api

import (
	"context"
	"fmt"
	"testing"
)

var localRouteSnapshot map[string]HandlerFunc

// Isolate route-map storage from JSON parsing. Both fixtures perform the same
// successful lookup and invocation; the previous map stored the function alone.
func BenchmarkLocalRouteLookup(b *testing.B) {
	for _, count := range []int{1, 16, 128} {
		previous := make(map[string]HandlerFunc, count)
		current := make(map[string]handlerRoute, count)
		calls := 0
		fn := func(context.Context, *Request) error { calls++; return nil }
		for i := range count {
			name := fmt.Sprintf("api%d", i)
			previous[name] = fn
			current[name] = handlerRoute{call: fn}
		}
		name := fmt.Sprintf("api%d", count-1)
		ctx, req := b.Context(), &Request{}
		b.Run(fmt.Sprintf("routes=%d/previous", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := previous[name](ctx, req); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("routes=%d/current", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := current[name].call(ctx, req); err != nil {
					b.Fatal(err)
				}
			}
		})
		if calls == 0 {
			b.Fatal("no calls")
		}
	}
}

// RPC takes this snapshot at initialization, never on each incoming call.
func BenchmarkLocalRouteExport(b *testing.B) {
	for _, count := range []int{1, 16, 128} {
		router := NewRouter()
		previous := make(map[string]HandlerFunc, count)
		fn := func(context.Context, *Request) error { return nil }
		for i := range count {
			name := fmt.Sprintf("api%d", i)
			router.Handle(name, fn)
			previous[name] = fn
		}
		b.Run(fmt.Sprintf("routes=%d/previous", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				localRouteSnapshot = previous
			}
		})
		b.Run(fmt.Sprintf("routes=%d/current", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				localRouteSnapshot = router.ExportHandlers()
			}
		})
	}
}
