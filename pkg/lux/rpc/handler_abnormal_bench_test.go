package rpc

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/errors"
)

type completionMetrics struct{}

func (completionMetrics) Record(string, time.Duration, bool) {}

func BenchmarkRPCHandlerCompletion(b *testing.B) {
	for _, observed := range []bool{false, true} {
		for _, failed := range []bool{false, true} {
			name := "unobserved/"
			if observed {
				name = "metrics/"
			}
			if failed {
				name += "error"
			} else {
				name += "success"
			}
			b.Run(name, func(b *testing.B) { benchmarkRPCHandlerCompletion(b, observed, failed) })
		}
	}
}

func benchmarkRPCHandlerCompletion(b *testing.B, observed, failed bool) {
	router := api.NewRouter()
	if err := router.Registry.Register("value", 1); err != nil {
		b.Fatal(err)
	}
	if observed {
		router.SetMetricsCollector(completionMetrics{})
	}
	router.Handle("value", func(_ context.Context, req *api.Request) error {
		if failed {
			return errors.BadRequest
		}
		req.Buf.B = append(req.Buf.B, 84)
		return nil
	})
	server := NewServer(router)
	defer server.Close()
	envelope := requestEnvelope{body: encodeCallRequest(1, nil, nil)}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if err := server.processCallContext(ctx, io.Discard, envelope); err != nil {
			b.Fatal(err)
		}
	}
}
