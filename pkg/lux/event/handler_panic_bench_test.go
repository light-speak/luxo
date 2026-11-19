package event

import (
	"context"
	stderrors "errors"
	"fmt"
	"testing"
)

// Frozen pre-fix recovery boundary; only used for the local paired benchmark.
func beforeHandlerPanicFormatting(fn Handler, ctx context.Context, payload any) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("event handler panic: %v", p)
		}
	}()
	return fn(ctx, payload)
}

var handlerPanicBenchmarkSink error

func BenchmarkHandlerPanicFormatting(b *testing.B) {

	ctx := context.Background()
	businessError := stderrors.New("business failure")
	for _, sample := range []struct {
		name    string
		handler Handler
	}{
		{"Success", func(context.Context, any) error { return nil }},
		{"BusinessError", func(context.Context, any) error { return businessError }},
		{"StringPanic", func(context.Context, any) error { panic("boom") }},
		{"OpaquePanic", func(context.Context, any) error { panic(struct{ ID int }{42}) }},
	} {
		b.Run(sample.name, func(b *testing.B) {
			handler := sample.handler
			for _, version := range []struct {
				name string
				run  func() error
			}{
				{"Before", func() error { return beforeHandlerPanicFormatting(handler, ctx, nil) }},
				{"After", func() error { return InvokeHandler(handler, ctx, nil) }},
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
