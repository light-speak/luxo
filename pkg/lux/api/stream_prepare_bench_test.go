package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/schema"
)

// Isolate the synchronous callback boundary from registration and scheduling.
func BenchmarkStreamPreparation(b *testing.B) {
	router := NewRouter()
	run := func(context.Context, *Stream) error { return nil }
	router.HandleStreamPrepared("watch", func(context.Context, *Request, any) (PreparedStreamHandler, error) {
		return run, nil
	})
	req := &Request{API: "watch"}
	b.ReportAllocs()
	for b.Loop() {
		prepared, err := router.prepareStreamHandler(b.Context(), req, nil)
		if err != nil || prepared.run == nil {
			b.Fatal("unexpected preparation result", err)
		}
	}
}

// Measure the whole local preparation/production/drain/close lifecycle. These
// are absolute results, not an equivalent pre-migration performance comparison.
func BenchmarkPreparedStreamLifecycle(b *testing.B) {
	for _, binary := range []bool{false, true} {
		name := "JSON"
		if binary {
			name = "Binary"
		}
		b.Run(name, func(b *testing.B) { benchmarkPreparedStreamLifecycle(b, binary) })
	}
}

func benchmarkPreparedStreamLifecycle(b *testing.B, binary bool) {
	router := newPreparedBenchmarkRouter(b)
	raw := map[string]json.RawMessage{"$sub": []byte(`"watch"`), "limit": []byte(`9`)}
	wire := []byte{1, 0, 1, 18, 0}
	b.ReportAllocs()
	for b.Loop() {
		var req *Request
		var err error
		if binary {
			req, err = router.Registry.ParseBinaryRequest(wire)
		} else {
			req, _, err = router.prepareWSJSONSubscription(raw)
		}
		if err != nil {
			b.Fatal(err)
		}
		stream, err := router.PrepareInternalStream(b.Context(), req)
		PutRequest(req)
		if err != nil {
			b.Fatal(err)
		}
		stream.Start()
		for value := range stream.Messages() {
			if len(value) != 1 || value[0] != 9 {
				b.Fatalf("unexpected payload: %x", value)
			}
		}
		stream.Close()
		if err := stream.Err(); err != nil {
			b.Fatal(err)
		}
	}
}

func newPreparedBenchmarkRouter(b *testing.B) *Router {
	router := NewRouter()
	router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "watch", Stream: true, ReturnType: "Int", Params: []schema.Param{
		{ID: 1, Name: "limit", Type: schema.FieldInt},
	}})
	router.Registry.RegisterSchemaAPIs(router.Schema)
	router.HandleStreamPrepared("watch", func(_ context.Context, req *Request, _ any) (PreparedStreamHandler, error) {
		limit, err := req.ParamInt("limit")
		if err != nil {
			return nil, err
		}
		return func(_ context.Context, stream *Stream) error { return stream.Send([]byte{byte(limit)}) }, nil
	})
	if err := router.Validate(); err != nil {
		b.Fatal(err)
	}
	return router
}
