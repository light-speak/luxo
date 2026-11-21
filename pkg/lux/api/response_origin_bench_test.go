package api

import (
	"context"
	"testing"
)

func BenchmarkResponseOriginRun(b *testing.B) {
	for _, limited := range []bool{false, true} {
		name := "unbounded"
		if limited {
			name = "bounded"
		}
		b.Run(name, func(b *testing.B) {
			origin := testResponseOrigin(b, 16, 0, 0)
			ctx, cancel := context.WithCancel(b.Context())
			defer cancel()
			calls := 0
			execute := func() error { calls++; return nil }
			b.ReportAllocs()
			for b.Loop() {
				var err error
				if limited {
					err = origin.run(ctx, execute)
				} else {
					err = ctx.Err()
					if err == nil {
						err = execute()
					}
				}
				if err != nil {
					b.Fatal(err)
				}
			}
			if calls != b.N {
				b.Fatal("origin was not executed exactly once")
			}
		})
	}
}

func BenchmarkResponseOriginContended(b *testing.B) {
	origin := testResponseOrigin(b, 16, 0, 0)
	ctx := b.Context()
	execute := func() error { return nil }
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := origin.run(ctx, execute); err != nil && err != errResponseOriginBusy {
				b.Error(err)
			}
		}
	})
}
