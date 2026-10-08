package luvia

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/api"
)

func TestFailedGatewayStartupDrainsRequestLogs(t *testing.T) {
	t.Setenv("DEPLOY_MODE", "invalid")
	var out bytes.Buffer
	g := NewWithOptions(api.RouterOptions{RequestLogging: true, LogWriter: &out})
	g.Router.Handle("health", func(_ context.Context, req *api.Request) error { req.Buf.AppendString("true"); return nil })
	g.Router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(`{"$api":"health"}`)))
	if err := g.serve("test", nil); err == nil {
		t.Fatal("expected invalid deployment error")
	}
	if !strings.Contains(out.String(), "health") {
		t.Fatalf("logs not drained: %q", out.String())
	}
}
