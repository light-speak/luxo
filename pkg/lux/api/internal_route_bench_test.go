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

// Compare identity binding with the former direct dispatch. This excludes
// verification, network I/O, registration and generated model encoding.
func BenchmarkRouteResponseIdentity(b *testing.B) {
	type identityKey struct{}
	ctx := context.WithValue(b.Context(), identityKey{}, "verified")
	for _, configured := range []bool{false, true} {
		router := NewRouter()
		if configured {
			router.IdentityExtractor = func(ctx context.Context) any { return ctx.Value(identityKey{}) }
		}
		fn := HandlerFunc(func(_ context.Context, req *Request) error { req.Buf.AppendByte(1); return nil })
		router.Handle("call", fn)
		bound := router.ExportHandlers()["call"]
		for _, candidate := range []struct {
			name string
			call HandlerFunc
		}{{"before", fn}, {"after", bound}} {
			b.Run(fmt.Sprintf("configured=%v/%s", configured, candidate.name), func(b *testing.B) {
				req := &Request{Buf: &ResponseBuf{B: make([]byte, 0, 8)}}
				b.ReportAllocs()
				for b.Loop() {
					req.Buf.B = req.Buf.B[:0]
					if err := candidate.call(ctx, req); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
