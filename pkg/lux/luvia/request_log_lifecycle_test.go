package luvia

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/api"
)

type failedRequestLogWriter struct{}

func (failedRequestLogWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestFailedGatewayStartupPreservesLogDrainError(t *testing.T) {
	t.Setenv("DEPLOY_MODE", "invalid")
	t.Setenv("GRACEFUL_TIMEOUT", "30s")
	g := NewWithOptions(api.RouterOptions{RequestLogging: true, RequestLogSampleEvery: 1, LogWriter: failedRequestLogWriter{}})
	g.Router.Handle("health", func(_ context.Context, req *api.Request) error {
		req.Buf.AppendString("true")
		return nil
	})
	g.Router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(`{"$api":"health"}`)))
	err := g.serve("test", nil)
	if !errors.Is(err, io.ErrClosedPipe) || !strings.Contains(err.Error(), "DEPLOY_MODE") || !strings.Contains(err.Error(), "request log shutdown") {
		t.Fatal("startup or drain error lost", err)
	}
	dropped, failures := g.Router.RequestLogStats()
	if dropped != 0 || failures != 1 {
		t.Fatal("accepted log not drained and accounted for", dropped, failures)
	}
	if err := g.Router.CloseRequestLogs(t.Context()); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("repeated close lost the original writer error", err)
	}
}

func TestFailedGatewayStartupDrainsRequestLogs(t *testing.T) {
	t.Setenv("DEPLOY_MODE", "invalid")
	var out bytes.Buffer
	g := NewWithOptions(api.RouterOptions{RequestLogging: true, RequestLogSampleEvery: 1, LogWriter: &out})
	g.Router.Handle("health", func(_ context.Context, req *api.Request) error { req.Buf.AppendString("true"); return nil })
	g.Router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(`{"$api":"health"}`)))
	if err := g.serve("test", nil); err == nil {
		t.Fatal("expected invalid deployment error")
	}
	if !strings.Contains(out.String(), "health") {
		t.Fatalf("logs not drained: %q", out.String())
	}
}
