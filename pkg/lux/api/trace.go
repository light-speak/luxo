package api

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type traceKey struct{}

// TraceMiddleware generates a unique trace ID for each request and injects
// it into the context. Respects an incoming X-Request-Id header.
func TraceMiddleware(next http.Handler) http.Handler {
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

// TraceID returns the trace ID from the context.
// Returns empty string if no trace ID is set.
func TraceID(ctx context.Context) string {
	id, _ := ctx.Value(traceKey{}).(string)
	return id
}
