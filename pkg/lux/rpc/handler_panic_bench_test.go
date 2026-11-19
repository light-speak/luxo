package rpc

import (
	"context"
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/api"
)

// Frozen pre-fix recovery boundary; only used for the local paired benchmark.
func beforeHandlerPanicFormatting(ctx context.Context, fn api.HandlerFunc, req *api.Request) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return fn(ctx, req)
}

var handlerPanicBenchmarkSink error

func BenchmarkHandlerPanicFormatting(b *testing.B) {
	server := NewServer(api.NewRouter())
	ctx := context.Background()
	businessError := stderrors.New("business failure")
	for _, sample := range []struct {
		name    string
		handler api.HandlerFunc
	}{
		{"Success", func(context.Context, *api.Request) error { return nil }},
		{"BusinessError", func(context.Context, *api.Request) error { return businessError }},
		{"StringPanic", func(context.Context, *api.Request) error { panic("boom") }},
		{"OpaquePanic", func(context.Context, *api.Request) error { panic(struct{ ID int }{42}) }},
	} {
		b.Run(sample.name, func(b *testing.B) {
			handler := sample.handler
			for _, version := range []struct {
				name string
				run  func() error
			}{
				{"Before", func() error { return beforeHandlerPanicFormatting(ctx, handler, nil) }},
				{"After", func() error { return server.callHandler(ctx, handler, nil) }},
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
