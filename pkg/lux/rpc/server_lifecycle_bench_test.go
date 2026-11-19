package rpc

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func BenchmarkRPCServerLifecycle(b *testing.B) {
	router := api.NewRouter()
	b.Run("create-close", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			NewServer(router).Close()
		}
	})
	for _, check := range []bool{false, true} {
		name := "dispatch"
		if check {
			name = "dispatch-context-check"
		}
		b.Run(name, func(b *testing.B) {
			router := api.NewRouter()
			if err := router.Registry.Register("value", 1); err != nil {
				b.Fatal(err)
			}
			router.Handle("value", func(ctx context.Context, req *api.Request) error {
				if check {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				req.Buf.B = append(req.Buf.B, 84)
				return nil
			})
			server := NewServer(router)
			defer server.Close()
			payload := encodeRequestEnvelope(requestKindCall, "", encodeCallRequest(1, nil, nil))
			if err := server.processRequest(io.Discard, payload); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := server.processRequest(io.Discard, payload); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkRPCResponsePresence(b *testing.B) {
	benchmarkRPCResponseBuffers(b, false)
}

func BenchmarkRPCResponseBufferTrace(b *testing.B) {
	benchmarkRPCResponseBuffers(b, true)
}

func benchmarkRPCResponseBuffers(b *testing.B, traced bool) {
	longValue := make([]byte, 4096)
	for _, sample := range []struct {
		name, kind string
		handler    api.HandlerFunc
	}{
		{"Int", "Int", func(_ context.Context, req *api.Request) error {
			req.Buf.B = codec.AppendSvarint(req.Buf.B, 42)
			return nil
		}},
		{"Zero", "Int", func(_ context.Context, req *api.Request) error {
			req.Buf.B = codec.AppendSvarint(req.Buf.B, 0)
			return nil
		}},
		{"Bytes4KiB", "Bytes", func(_ context.Context, req *api.Request) error {
			req.Buf.B = codec.AppendBytes(req.Buf.B, longValue)
			return nil
		}},
		{"Void", "", func(context.Context, *api.Request) error { return nil }},
	} {
		b.Run(sample.name, func(b *testing.B) {
			router := api.NewRouter()
			if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "value", Internal: true, ReturnType: sample.kind}); err != nil {
				b.Fatal(err)
			}
			if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
				b.Fatal(err)
			}
			router.HandleInternal("value", sample.handler)
			server := NewServer(router)
			defer server.Close()
			envelope := requestEnvelope{body: encodeCallRequest(1, nil, nil)}
			if traced {
				envelope.traceID = "response-buffer-cost"
			}
			ctx := context.Background()
			checkRPCBenchmarkResponse(b, server, ctx, envelope, sample.handler, traced)
			b.ReportAllocs()
			for b.Loop() {
				if err := server.processCallContext(ctx, io.Discard, envelope); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func checkRPCBenchmarkResponse(b *testing.B, server *Server, ctx context.Context, envelope requestEnvelope, handler api.HandlerFunc, traced bool) {
	b.Helper()
	want := &api.ResponseBuf{}
	if err := handler(ctx, &api.Request{Buf: want}); err != nil {
		b.Fatal(err)
	}
	var output bytes.Buffer
	if err := server.processCallContext(ctx, &output, envelope); err != nil {
		b.Fatal(err)
	}
	frame, err := ReadFrame(&output, nil)
	if err != nil {
		b.Fatal(err)
	}
	var body []byte
	if traced {
		var snapshot api.DebugTraceSnapshot
		body, snapshot, err = decodeTracedResponse(frame)
		if snapshot.TraceID != envelope.traceID || len(snapshot.Spans) != 3 || snapshot.Spans[2].Name != "service.handler" {
			b.Fatal("traced benchmark lost its handler span", snapshot)
		}
	} else {
		body, err = decodeCallResponse(frame, nil)
	}
	if err != nil || !bytes.Equal(body, want.B) {
		b.Fatal("benchmark did not return its expected business value", body, err)
	}
}
