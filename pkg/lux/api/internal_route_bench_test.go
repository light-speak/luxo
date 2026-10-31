package api

import (
	"context"
	"fmt"
	"testing"
)

// Compare only the RPC startup snapshot. Public request dispatch still uses the
// original handler table; this benchmark is not an end-to-end latency result.
func BenchmarkInternalRouteExport(b *testing.B) {
	for _, count := range []int{1, 16, 128} {
		for _, internal := range []bool{false, true} {
			previous, current := NewRouter(), NewRouter()
			fn := func(context.Context, *Request) error { return nil }
			for i := range count {
				name := fmt.Sprintf("route%d", i)
				previous.Handle(name, fn)
				if internal && i%2 == 0 {
					current.HandleInternal(name, fn)
				} else {
					current.Handle(name, fn)
				}
			}
			for _, version := range []struct {
				name string
				copy func() map[string]HandlerFunc
			}{
				{"previous", func() map[string]HandlerFunc {
					handlers := make(map[string]HandlerFunc, len(previous.handlers))
					for name, route := range previous.handlers {
						handlers[name] = route.call
					}
					return handlers
				}},
				{"current", current.ExportHandlers},
			} {
				b.Run(fmt.Sprintf("routes=%d/mixed=%v/%s", count, internal, version.name), func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						localRouteSnapshot = version.copy()
					}
					if len(localRouteSnapshot) != count {
						b.Fatal("RPC snapshot lost a route")
					}
				})
			}
		}
	}
}
