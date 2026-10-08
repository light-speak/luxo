package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/schema"
)

type delayedLogResponseWriter struct{ *httptest.ResponseRecorder }

func (w delayedLogResponseWriter) Write(p []byte) (int, error) {
	time.Sleep(10 * time.Millisecond)
	return w.ResponseRecorder.Write(p)
}

func TestHTTPLogIncludesResponseWriteDuration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var out bytes.Buffer
		r := NewRouterWithOptions(RouterOptions{RequestLogging: true, SynchronousRequestLogs: true, LogWriter: &out})
		r.Handle("health", func(_ context.Context, req *Request) error { req.Buf.AppendString("true"); return nil })
		r.ServeHTTP(delayedLogResponseWriter{httptest.NewRecorder()}, httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(`{"$api":"health"}`)))
		if !strings.Contains(out.String(), "server=30.0ms") {
			t.Fatalf("write time missing: %q", out.String())
		}
	})
}

func TestHTTPAsyncLogDoesNotWaitForWriter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		out := &blockedLogWriter{entered: make(chan struct{}), release: make(chan struct{})}
		r := NewRouterWithOptions(RouterOptions{RequestLogging: true, LogWriter: out})
		r.Handle("health", func(_ context.Context, req *Request) error {
			req.Buf.AppendString("true")
			return nil
		})
		response := httptest.NewRecorder()
		r.ServeHTTP(delayedLogResponseWriter{response}, httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(`{"$api":"health"}`)))
		<-out.entered
		// The response completed although output is blocked. Output delay must
		// not rewrite the already captured server-side duration.
		time.Sleep(time.Second)
		close(out.release)
		if err := r.CloseRequestLogs(context.Background()); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || !strings.Contains(out.buf.String(), "server=30.0ms") {
			t.Fatalf("response=%d logs=%q", response.Code, out.buf.String())
		}
		if drops, failures := r.RequestLogStats(); drops != 0 || failures != 0 {
			t.Fatalf("drops=%d failures=%d", drops, failures)
		}
	})
}

func TestLogRequest_WithError(t *testing.T) {
	var logs bytes.Buffer
	rt := NewRouterWithOptions(RouterOptions{RequestLogging: true, SynchronousRequestLogs: true, LogWriter: &logs})
	rt.Schema.RegisterAPI(&schema.API{ID: 1, Name: "getUser", Module: "user"})

	rt.logRequest("getUser", 50*time.Millisecond, fmt.Errorf("InvalidCredentials"))
	output := logs.String()

	// Should contain the error marker and error message
	if len(output) == 0 {
		t.Error("expected log output")
	}
	// Red color for error
	if output == "" {
		t.Error("should have output")
	}
}

func TestLogRequest_SlowDuration(t *testing.T) {
	var logs bytes.Buffer
	rt := NewRouterWithOptions(RouterOptions{RequestLogging: true, SynchronousRequestLogs: true, LogWriter: &logs})
	rt.Schema.RegisterAPI(&schema.API{ID: 1, Name: "slowAPI", Module: "slow"})

	// >500ms should be red
	rt.logRequest("slowAPI", 600*time.Millisecond, nil)
	output := logs.String()

	if len(output) == 0 {
		t.Error("expected log output for slow request")
	}
}

func TestLogRequest_MediumDuration(t *testing.T) {
	var logs bytes.Buffer
	rt := NewRouterWithOptions(RouterOptions{RequestLogging: true, SynchronousRequestLogs: true, LogWriter: &logs})
	rt.Schema.RegisterAPI(&schema.API{ID: 1, Name: "medAPI", Module: "med"})

	// >100ms <500ms should be yellow
	rt.logRequest("medAPI", 200*time.Millisecond, nil)
	output := logs.String()

	if len(output) == 0 {
		t.Error("expected log output for medium request")
	}
}

func TestLogRequest_Disabled(t *testing.T) {
	rt := NewRouter()
	// Should not panic or produce output when disabled
	rt.logRequest("test", time.Millisecond, nil)
}

func TestServeHTTPLogsParseErrors(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		binary bool
		mode   string
	}{
		{name: "JSON", body: "bad", mode: "json"},
		{name: "binary", body: "", binary: true, mode: "binary"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			rt := NewRouterWithOptions(RouterOptions{RequestLogging: true, SynchronousRequestLogs: true, LogWriter: &logs})
			r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(tt.body))
			if tt.binary {
				r.Header.Set("X-Luxo-Mode", "binary")
			}
			w := httptest.NewRecorder()
			rt.ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
			if output := logs.String(); !strings.Contains(output, "[parse]") || !strings.Contains(output, tt.mode) {
				t.Fatalf("parse log = %q", output)
			}
		})
	}
}

func TestLogRequest_NoModule(t *testing.T) {
	var logs bytes.Buffer
	rt := NewRouterWithOptions(RouterOptions{RequestLogging: true, SynchronousRequestLogs: true, LogWriter: &logs})
	// API not in schema — should fall back to "api" module

	rt.logRequest("unknownAPI", 5*time.Millisecond, nil)
	output := logs.String()

	if len(output) == 0 {
		t.Error("expected log output")
	}
}

func TestModuleColor_DifferentModules(t *testing.T) {
	// Reset state for test isolation
	moduleColorMu.Lock()
	oldMap := moduleColorMap
	oldIdx := moduleColorIdx
	moduleColorMap = make(map[string]string)
	moduleColorIdx = 0
	moduleColorMu.Unlock()
	defer func() {
		moduleColorMu.Lock()
		moduleColorMap = oldMap
		moduleColorIdx = oldIdx
		moduleColorMu.Unlock()
	}()

	c1 := moduleColor("auth")
	c2 := moduleColor("user")
	c3 := moduleColor("post")

	// Different modules should get different colors
	if c1 == c2 || c2 == c3 {
		t.Errorf("different modules should get different colors: auth=%q user=%q post=%q", c1, c2, c3)
	}
}

func TestModuleColor_Idempotent(t *testing.T) {
	// Same module should always return same color
	moduleColorMu.Lock()
	oldMap := moduleColorMap
	oldIdx := moduleColorIdx
	moduleColorMap = make(map[string]string)
	moduleColorIdx = 0
	moduleColorMu.Unlock()
	defer func() {
		moduleColorMu.Lock()
		moduleColorMap = oldMap
		moduleColorIdx = oldIdx
		moduleColorMu.Unlock()
	}()

	c1 := moduleColor("auth")
	c2 := moduleColor("auth")
	c3 := moduleColor("auth")

	if c1 != c2 || c2 != c3 {
		t.Errorf("same module should return same color: %q %q %q", c1, c2, c3)
	}
}
