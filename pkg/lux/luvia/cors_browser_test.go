package luvia

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestCORSBrowserFixture(t *testing.T) {
	output := os.Getenv("LUXO_CORS_BROWSER_STATE")
	if output == "" {
		t.Skip("manual browser fixture")
	}
	root, err := filepath.Abs("../../../.tmp/audit-cors")
	if err != nil || filepath.Dir(output) != root {
		t.Fatal("browser state must stay inside its owned directory", err)
	}
	t.Setenv("CORS_ORIGIN", envOr("LUXO_CORS_BROWSER_ORIGIN", "*"))
	router := corsBrowserRouter(t)
	var preflights, calls atomic.Int64
	cors := testCORSMiddleware(t, api.TraceMiddleware(router))
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			preflights.Add(1)
		} else if r.Method == http.MethodPost {
			calls.Add(1)
		}
		w.Header().Set("Content-Encoding", "identity")
		w.Header().Set("Server-Timing", "cors;dur=1")
		cors.ServeHTTP(w, r)
	}))
	t.Cleanup(target.Close)
	mux := corsBrowserPage(t, root, target.URL, &preflights, &calls)
	origin := httptest.NewServer(mux)
	t.Cleanup(origin.Close)
	data, err := json.Marshal(map[string]string{"origin": origin.URL, "target": target.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, data, 0600); err != nil {
		t.Fatal(err)
	}
	waitCORSBrowser(t, output+".stop")
}

func corsBrowserRouter(t *testing.T) *api.Router {
	t.Helper()
	router := api.NewRouter()
	router.SetDebugTraceKey("cors-test-key")
	if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "double", ReturnType: "Int", Params: []schema.Param{{ID: 1, Name: "value", Type: schema.FieldInt}}}); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	router.Handle("double", func(_ context.Context, req *api.Request) error {
		value, err := req.ParamInt("value")
		if err != nil {
			return err
		}
		req.Buf.B = codec.AppendSvarint(req.Buf.B, value*2)
		return nil
	})
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	return router
}

func corsBrowserPage(t *testing.T, root, target string, preflights, calls *atomic.Int64) *http.ServeMux {
	t.Helper()
	distribution, err := filepath.Abs("../../../packages/client/dist")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/sdk/", http.StripPrefix("/sdk/", http.FileServer(http.Dir(distribution))))
	mux.Handle("/artifacts/", http.StripPrefix("/artifacts/", http.FileServer(http.Dir(root))))
	mux.HandleFunc("/state", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{"preflights": preflights.Load(), "calls": calls.Load()})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><title>Luxo CORS fixture</title><body>" + target + "</body>"))
	})
	return mux
}

func waitCORSBrowser(t *testing.T, stop string) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(4 * time.Minute)
	defer deadline.Stop()
	for {
		select {
		case <-ticker.C:
			if _, err := os.Stat(stop); err == nil {
				return
			}
		case <-deadline.C:
			t.Fatal("browser fixture deadline exceeded")
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
}
