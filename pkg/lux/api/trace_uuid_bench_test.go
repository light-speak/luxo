package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/light-speak/luxo/pkg/lux"
)

// Exact pre-fix success path, retained only for a scoped local comparison.
func traceMiddlewareBeforeUUIDErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get("X-Request-Id")
		if traceID == "" {
			traceID = uuid.Must(uuid.NewV7()).String()
		}
		ctx := context.WithValue(r.Context(), traceKey{}, traceID)
		w.Header().Set("X-Trace-Id", traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

var uuidTraceIDResult string

type uuidTraceWriter struct{ header http.Header }

func (w *uuidTraceWriter) Header() http.Header         { return w.header }
func (w *uuidTraceWriter) WriteHeader(int)             {}
func (w *uuidTraceWriter) Write(p []byte) (int, error) { return len(p), nil }

func BenchmarkTraceUUID(b *testing.B) {
	for _, supplied := range []bool{false, true} {
		name := "generated"
		if supplied {
			name = "supplied"
		}
		for _, previous := range []bool{true, false} {
			version := "current"
			if previous {
				version = "before"
			}
			b.Run(name+"/"+version, func(b *testing.B) {
				request := httptest.NewRequest(http.MethodPost, "/luvia", nil)
				if supplied {
					request.Header.Set("X-Request-Id", "supplied")
				}
				var next http.Handler = http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { uuidTraceIDResult = TraceID(r.Context()) })
				if previous {
					next = traceMiddlewareBeforeUUIDErrors(next)
				} else {
					next = TraceMiddleware(next)
				}
				writer := &uuidTraceWriter{header: make(http.Header)}
				b.ReportAllocs()
				for b.Loop() {
					next.ServeHTTP(writer, request)
				}
			})
		}
	}
}

func traceSessionBeforeEmptyID(ctx context.Context, traceID string, sampled, exposeHeaders, databaseDetails bool) (context.Context, *DebugTraceSession) {
	trace := &DebugTraceSession{
		traceID: traceID, started: time.Now(), spans: make([]DebugSpan, 0, 12),
		headSampled: sampled, exposeHeaders: exposeHeaders, databaseDetails: databaseDetails,
	}
	if TraceID(ctx) == "" {
		ctx = context.WithValue(ctx, traceKey{}, traceID)
	}
	ctx = context.WithValue(ctx, debugTraceKey{}, trace)
	return lux.WithDatabaseTraceSink(ctx, trace), trace
}

var uuidTraceContextResult context.Context
var uuidTraceSessionResult *DebugTraceSession

func BenchmarkTraceUUIDSession(b *testing.B) {
	b.Run("before", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			uuidTraceContextResult, uuidTraceSessionResult = traceSessionBeforeEmptyID(context.Background(), "valid", false, true, true)
		}
	})
	b.Run("current", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			uuidTraceContextResult, uuidTraceSessionResult = WithDebugTrace(context.Background(), "valid")
		}
	})
}
