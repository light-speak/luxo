package api

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"

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
		prepared, _, err := router.prepareStreamHandler(b.Context(), req)
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
			if len(value) != 1 || value[0] != 18 {
				b.Fatalf("unexpected payload: %x", value)
			}
		}
		stream.Close()
		if err := stream.Err(); err != nil {
			b.Fatal(err)
		}
	}
}

func newPreparedBenchmarkRouter(b testing.TB) *Router {
	router := NewRouter()
	if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "watch", Stream: true, ReturnType: "Int", Params: []schema.Param{
		{ID: 1, Name: "limit", Type: schema.FieldInt},
	}}); err != nil {
		b.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		b.Fatal(err)
	}
	router.HandleStreamPrepared("watch", func(_ context.Context, req *Request, _ any) (PreparedStreamHandler, error) {
		limit, err := req.ParamInt("limit")
		if err != nil {
			return nil, err
		}
		return func(_ context.Context, stream *Stream) error {
			return stream.Send(codec.AppendSvarint(make([]byte, 0, 1), limit))
		}, nil
	})
	if err := router.Validate(); err != nil {
		b.Fatal(err)
	}
	return router
}

// Include registration-free preparation and cancellation, without scheduling a
// producer. A forwarded source has the same no-op release in both revisions.
func BenchmarkInternalStreamClose(b *testing.B) {
	for _, sample := range []string{"Prepared", "Forwarded", "CanceledStart", "Closed"} {
		b.Run(sample, func(b *testing.B) { benchmarkInternalStreamClose(b, sample) })
	}
}

func benchmarkInternalStreamClose(b *testing.B, sample string) {
	router := newClosingBenchmarkRouter(sample)
	req := &Request{API: "watch"}
	if sample == "Closed" {
		benchmarkClosedInternalStream(b, router, req)
		return
	}
	b.ReportAllocs()
	for b.Loop() {
		ctx := b.Context()
		var cancel context.CancelFunc
		if sample == "CanceledStart" {
			ctx, cancel = context.WithCancel(ctx)
		}
		stream, err := router.PrepareInternalStream(ctx, req)
		if err != nil {
			b.Fatal(err)
		}
		if cancel != nil {
			cancel()
			stream.Start()
		}
		stream.Close()
		if stream.Err() != context.Canceled {
			b.Fatal("cancellation result changed")
		}
	}
}

func newClosingBenchmarkRouter(sample string) *Router {
	router := NewRouter()
	run := func(context.Context, *Stream) error { return nil }
	if sample == "Forwarded" || sample == "CanceledStart" {
		router.HandleStreamForwarded("watch", func(context.Context, *Request, any) (PreparedStreamSource, error) {
			return PreparedStreamSource{Run: run, Close: func() {}}, nil
		})
	} else {
		router.HandleStreamPrepared("watch", func(context.Context, *Request, any) (PreparedStreamHandler, error) {
			return run, nil
		})
	}
	return router
}

func benchmarkClosedInternalStream(b *testing.B, router *Router, req *Request) {
	stream, err := router.PrepareInternalStream(b.Context(), req)
	if err != nil {
		b.Fatal(err)
	}
	stream.Close()
	b.ReportAllocs()
	for b.Loop() {
		stream.Close()
	}
}

// Frozen callback boundary with identity extraction outside recovery.
func (rt *Router) beforeIdentityRecovery(ctx context.Context, req *Request, identity any) (prepared preparedStream, err error) {
	defer func() {
		if recover() != nil {
			// Do not format arbitrary panic values or expose them to clients.
			prepared, err = preparedStream{}, luxerrors.InternalError
		}
	}()
	route, registered := rt.streamHandlers[req.API]
	if route.forwardPrepare != nil {
		return prepareForwardedStream(ctx, req, identity, route.forwardPrepare)
	}
	if route.eventPrepare != nil {
		bind, err := route.eventPrepare(ctx, req, identity)
		return preparedStream{bind: bind}, err
	}
	prepare := route.prepare
	if prepare == nil {
		if registered && route.handler == nil {
			return preparedStream{}, fmt.Errorf("api: stream %q has no registered implementation", req.API)
		}
		return preparedStream{}, nil
	}
	handler, err := prepare(ctx, req, identity)
	if err != nil {
		return preparedStream{}, err
	}
	if handler == nil {
		return preparedStream{}, fmt.Errorf("api: stream %q preparation returned no handler", req.API)
	}
	return preparedStream{run: handler}, nil
}

func BenchmarkStreamIdentityRecovery(b *testing.B) {
	for _, sample := range []string{"NoIdentity", "Identity", "Forwarded"} {
		b.Run(sample, func(b *testing.B) {
			router := NewRouter()
			identity := new(int64(42))
			var want any
			if sample != "NoIdentity" {
				want = identity
				router.IdentityExtractor = func(context.Context) any { return identity }
			}
			run := func(context.Context, *Stream) error { return nil }
			if sample == "Forwarded" {
				router.HandleStreamForwarded("watch", func(context.Context, *Request, any) (PreparedStreamSource, error) {
					return PreparedStreamSource{Run: run, Close: func() {}}, nil
				})
			} else {
				router.HandleStreamPrepared("watch", func(context.Context, *Request, any) (PreparedStreamHandler, error) { return run, nil })
			}
			req, ctx := &Request{API: "watch"}, b.Context()
			before := func() (preparedStream, any, error) {
				got := router.identityFromCtx(ctx)
				prepared, err := router.beforeIdentityRecovery(ctx, req, got)
				return prepared, got, err
			}
			after := func() (preparedStream, any, error) { return router.prepareStreamHandler(ctx, req) }
			for _, version := range []struct {
				name string
				run  func() (preparedStream, any, error)
			}{{"Before", before}, {"After", after}} {
				b.Run(version.name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						prepared, got, err := version.run()
						if err != nil || got != want || prepared.run == nil {
							b.Fatal("preparation changed")
						}
					}
				})
			}
		})
	}
}

func BenchmarkStreamSourceReleaseRecovery(b *testing.B) {
	for _, count := range []int{1, 64} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			called := 0
			callbacks := make([]func(), count)
			for i := range callbacks {
				callbacks[i] = func() { called++ }
			}
			before := func() {
				for _, release := range callbacks {
					release()
				}
			}
			after := func() {
				for _, release := range callbacks {
					releaseStreamSource(release)
				}
			}
			for _, version := range []struct {
				name string
				run  func()
			}{{"Before", before}, {"After", after}} {
				b.Run(version.name, func(b *testing.B) {
					called = 0
					b.ReportAllocs()
					for b.Loop() {
						version.run()
					}
					if called != b.N*count {
						b.Fatal("release skipped or repeated")
					}
				})
			}
		})
	}
}
