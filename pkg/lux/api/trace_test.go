package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTraceMiddlewareGeneratesID(t *testing.T) {
	var gotTraceID string
	handler := TraceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceID = TraceID(r.Context())
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if gotTraceID == "" {
		t.Error("trace ID should be generated")
	}
	if w.Header().Get("X-Trace-Id") == "" {
		t.Error("X-Trace-Id response header should be set")
	}
	if w.Header().Get("X-Trace-Id") != gotTraceID {
		t.Error("response header should match context value")
	}
}

func TestTraceMiddlewareReusesRequestId(t *testing.T) {
	var gotTraceID string
	handler := TraceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceID = TraceID(r.Context())
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Request-Id", "custom-trace-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if gotTraceID != "custom-trace-123" {
		t.Errorf("got %q, want custom-trace-123", gotTraceID)
	}
	if w.Header().Get("X-Trace-Id") != "custom-trace-123" {
		t.Error("response header should use incoming request ID")
	}
}

func TestTraceIDEmptyContext(t *testing.T) {
	id := TraceID(context.Background())
	if id != "" {
		t.Errorf("got %q, want empty string", id)
	}
}
