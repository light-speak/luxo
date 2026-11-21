package api

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

type completionMetrics struct{}

func (completionMetrics) Record(string, time.Duration, bool) {}

type completionWriter struct{ header http.Header }

func (w *completionWriter) Header() http.Header       { return w.header }
func (*completionWriter) WriteHeader(int)             {}
func (*completionWriter) Write(b []byte) (int, error) { return len(b), nil }

func BenchmarkHTTPHandlerCompletion(b *testing.B) {
	for _, mode := range []bool{false, true} {
		for _, observed := range []bool{false, true} {
			for _, failed := range []bool{false, true} {
				name := httpAbnormalMode(mode) + "/"
				if observed {
					name += "metrics/"
				} else {
					name += "unobserved/"
				}
				if failed {
					name += "error"
				} else {
					name += "success"
				}
				b.Run(name, func(b *testing.B) { benchmarkHTTPHandlerCompletion(b, mode, observed, failed) })
			}
		}
	}
}

func benchmarkHTTPHandlerCompletion(b *testing.B, binaryMode, observed, failed bool) {
	router := NewRouter()
	if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "value", ReturnType: "Int"}); err != nil {
		b.Fatal(err)
	}
	request := httpAbnormalRequest(binaryMode)
	req := &Request{API: "value"}
	writer := &completionWriter{header: make(http.Header)}
	var metrics MetricsRecorder
	if observed {
		metrics = completionMetrics{}
	}
	handler := func(_ context.Context, req *Request) error {
		if failed {
			return errors.BadRequest
		}
		req.Buf.B = codec.AppendSvarint(req.Buf.B, 42)
		return nil
	}
	b.ReportAllocs()
	for b.Loop() {
		router.executeHTTPHandler(writer, request, req, handler, binaryMode, metrics, nil, time.Time{})
	}
}

func BenchmarkHTTPTracedHandlerCompletion(b *testing.B) {
	for _, mode := range []string{"debug", "sampled"} {
		for _, binaryMode := range []bool{false, true} {
			for _, failed := range []bool{false, true} {
				name := mode + "/" + httpAbnormalMode(binaryMode) + "/success"
				if failed {
					name = mode + "/" + httpAbnormalMode(binaryMode) + "/error"
				}
				b.Run(name, func(b *testing.B) { benchmarkHTTPTracedCompletion(b, binaryMode, failed, mode == "sampled") })
			}
		}
	}
}

type completionTraceMetrics struct {
	completionMetrics
	record TraceRecord
}

func (m *completionTraceMetrics) RecordTrace(record TraceRecord) { m.record = record }

func benchmarkHTTPTracedCompletion(b *testing.B, binaryMode, failed, sampled bool) {
	router := NewRouter()
	if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "value", ReturnType: "Int"}); err != nil {
		b.Fatal(err)
	}
	request := httpAbnormalRequest(binaryMode)
	req := &Request{API: "value"}
	writer := &completionWriter{header: make(http.Header)}
	sampledMetrics := &completionTraceMetrics{}
	var metrics MetricsRecorder = completionMetrics{}
	if sampled {
		metrics = sampledMetrics
	}
	handler := func(_ context.Context, req *Request) error {
		if failed {
			return errors.BadRequest
		}
		req.Buf.B = codec.AppendSvarint(req.Buf.B, 42)
		return nil
	}
	invoke := func() *DebugTraceSession {
		ctx, trace := completionTraceSession(sampled)
		router.executeHTTPHandler(writer, request.WithContext(ctx), req, handler, binaryMode, metrics, trace, time.Time{})
		return trace
	}
	spans := invoke().Snapshot().Spans
	if len(spans) == 0 || spans[0].Name != "handler.execute" {
		b.Fatal("traced benchmark did not complete its handler span", spans)
	}
	if sampled {
		if !sampledMetrics.record.HeadSampled || sampledMetrics.record.Spans == "" || writer.header.Get(DebugTraceResponseHeader) != "" {
			b.Fatal("sampled benchmark did not isolate its exported trace")
		}
	} else if writer.header.Get(DebugTraceResponseHeader) == "" {
		b.Fatal("debug benchmark did not export its trace header")
	}
	b.ReportAllocs()
	for b.Loop() {
		invoke()
	}
}

func completionTraceSession(sampled bool) (context.Context, *DebugTraceSession) {
	if sampled {
		return WithSampledTrace(context.Background(), "handler-completion")
	}
	return WithDebugTrace(context.Background(), "handler-completion")
}

func BenchmarkHTTPErrorLogging(b *testing.B) {
	for _, binaryMode := range []bool{false, true} {
		b.Run(httpAbnormalMode(binaryMode), func(b *testing.B) {
			router := NewRouterWithOptions(RouterOptions{RequestLogging: true, RequestLogSampleEvery: 100,
				SynchronousRequestLogs: true, LogWriter: io.Discard})
			request := httpAbnormalRequest(binaryMode)
			req := &Request{API: "value"}
			writer := &completionWriter{header: make(map[string][]string)}
			handler := func(context.Context, *Request) error { return errors.BadRequest }
			b.ReportAllocs()
			for b.Loop() {
				router.executeHTTPHandler(writer, request, req, handler, binaryMode, nil, nil, time.Now())
			}
		})
	}
}
