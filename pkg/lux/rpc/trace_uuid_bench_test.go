package rpc

import (
	"context"
	"strconv"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func BenchmarkRPCUUIDTraceGuard(b *testing.B) {
	for _, traced := range []bool{false, true} {
		b.Run(strconv.FormatBool(traced), func(b *testing.B) {
			router := api.NewRouter()
			if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "ping", ReturnType: "Int"}); err != nil {
				b.Fatal(err)
			}
			router.Handle("ping", func(_ context.Context, req *api.Request) error {
				req.Buf.B = codec.AppendSvarint(req.Buf.B, 7)
				return nil
			})
			if err := router.Registry.Register("ping", 1); err != nil {
				b.Fatal(err)
			}
			if err := router.Registry.RegisterParams("ping", nil); err != nil {
				b.Fatal(err)
			}
			client := NewClient(startTestRPCServer(b, NewServer(router)))
			b.Cleanup(client.Close)
			ctx := b.Context()
			if traced {
				ctx, _ = api.WithDebugTrace(ctx, "valid")
			}
			if _, err := client.CallWithMaskContext(ctx, "", 1, nil, nil); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := client.CallWithMaskContext(ctx, "", 1, nil, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
