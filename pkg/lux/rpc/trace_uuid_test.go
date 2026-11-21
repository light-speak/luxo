package rpc

import (
	"context"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestMissingTraceIDPreservesRPC(t *testing.T) {
	router := api.NewRouter()
	if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "ping", ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	router.Handle("ping", func(ctx context.Context, req *api.Request) error {
		if api.DebugTrace(ctx) != nil {
			t.Error("invalid trace metadata crossed the RPC boundary")
		}
		req.Buf.B = codec.AppendSvarint(req.Buf.B, 7)
		return nil
	})
	server := NewServer(router)
	client := NewClient(startTestRPCServer(t, server))
	t.Cleanup(func() { client.Close(); server.Close() })
	for _, mode := range []string{"debug", "sampled", "remote"} {
		t.Run(mode, func(t *testing.T) {
			ctx, trace := missingTraceContext(t.Context(), mode)
			body, err := client.CallWithMaskContext(ctx, "", 1, nil, nil)
			if err != nil {
				t.Fatal("missing diagnostic ID failed business call", err)
			}
			decoder := codec.NewDecoder(body)
			value := decoder.ReadInt()
			if err := decoder.Finish(); err != nil || value != 7 || !trace.Snapshot().EncodingFailed {
				t.Fatal(value, err, trace.Snapshot())
			}
		})
	}
}

func missingTraceContext(ctx context.Context, mode string) (context.Context, *api.DebugTraceSession) {
	switch mode {
	case "sampled":
		return api.WithSampledTrace(ctx, "")
	case "remote":
		return api.WithRemoteTrace(ctx, "", "parent", false)
	default:
		return api.WithDebugTrace(ctx, "")
	}
}
