package api

import (
	"context"
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

// Frozen pre-fix recovery boundary; only used for the local paired benchmark.
func beforeHandlerPanicFormatting(fn HandlerFunc, ctx context.Context, req *Request) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = luxerrors.InternalError.WithCause(fmt.Errorf("panic: %v", p))
		}
	}()
	return fn(ctx, req)
}

var handlerPanicBenchmarkSink error

func BenchmarkHandlerPanicFormatting(b *testing.B) {
	router := NewRouter()
	ctx := context.Background()
	req := &Request{Buf: new(ResponseBuf)}
	businessError := stderrors.New("business failure")
	for _, sample := range []struct {
		name    string
		handler HandlerFunc
	}{
		{"Success", func(context.Context, *Request) error { return nil }},
		{"BusinessError", func(context.Context, *Request) error { return businessError }},
		{"StringPanic", func(context.Context, *Request) error { panic("boom") }},
		{"OpaquePanic", func(context.Context, *Request) error { panic(struct{ ID int }{42}) }},
	} {
		b.Run(sample.name, func(b *testing.B) {
			handler := sample.handler
			for _, version := range []struct {
				name string
				run  func() error
			}{
				{"Before", func() error { return beforeHandlerPanicFormatting(handler, ctx, req) }},
				{"After", func() error { return router.callHandler(handler, ctx, req) }},
			} {
				b.Run(version.name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						handlerPanicBenchmarkSink = version.run()
					}
					if sample.name == "Success" && handlerPanicBenchmarkSink != nil {
						b.Fatal("success changed")
					}
					if sample.name == "BusinessError" && handlerPanicBenchmarkSink != businessError {
						b.Fatal("business error identity changed")
					}
					if sample.name == "StringPanic" && handlerPanicBenchmarkSink == nil {
						b.Fatal("panic lost")
					}
				})
			}
		})
	}
}

func BenchmarkHandlerResponseBuffer(b *testing.B) {
	router := NewRouter()
	ctx := context.Background()
	longValue := make([]byte, 4096)
	for _, sample := range []struct {
		name    string
		handler HandlerFunc
		want    error
	}{
		{"Empty", func(context.Context, *Request) error { return nil }, nil},
		{"Int", func(_ context.Context, req *Request) error {
			req.Buf.B = codec.AppendSvarint(req.Buf.B, 42)
			return nil
		}, nil},
		{"Bytes4KiB", func(_ context.Context, req *Request) error {
			req.Buf.B = append(req.Buf.B, longValue...)
			return nil
		}, nil},
		{"BusinessError", func(context.Context, *Request) error { return luxerrors.NotFound }, luxerrors.NotFound},
	} {
		b.Run(sample.name, func(b *testing.B) {
			req := &Request{Buf: &ResponseBuf{B: make([]byte, 0, 4096)}}
			b.ReportAllocs()
			for b.Loop() {
				req.Buf.B = req.Buf.B[:0]
				handlerPanicBenchmarkSink = router.callHandler(sample.handler, ctx, req)
			}
			if handlerPanicBenchmarkSink != sample.want {
				b.Fatal("handler result identity changed", handlerPanicBenchmarkSink)
			}
		})
	}
}

func BenchmarkHandlerDetachedResponseBuffer(b *testing.B) {
	router := NewRouter()
	ctx := context.Background()
	buf := new(ResponseBuf)
	req := &Request{}
	handler := func(_ context.Context, req *Request) error { req.Buf = nil; return nil }
	b.ReportAllocs()
	for b.Loop() {
		req.Buf = buf
		handlerPanicBenchmarkSink = router.callHandler(handler, ctx, req)
	}
	if handlerPanicBenchmarkSink == nil {
		b.Fatal("detached response buffer returned success")
	}
}
