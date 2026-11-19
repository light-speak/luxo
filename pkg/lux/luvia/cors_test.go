package luvia

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/api"
)

func TestCORSProtocolRequestHeaders(t *testing.T) {
	t.Setenv("CORS_ORIGIN", "https://app.example")
	handler := testCORSMiddleware(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("preflight dispatched business") }))
	for _, name := range []string{"Content-Type", "Authorization", "X-Luxo-Mode", "X-Request-Id", "X-Luxo-Client", "X-Luxo-Client-Version", api.DebugTraceRequestHeader, api.DebugTraceKeyHeader, api.DebugTraceEnvelopeHeader} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, "/luvia", nil)
			request.Header.Set("Origin", "https://app.example")
			request.Header.Set("Access-Control-Request-Method", "POST")
			request.Header.Set("Access-Control-Request-Headers", strings.ToLower(name))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNoContent || !corsHeaderContains(response.Header().Get("Access-Control-Allow-Headers"), name) {
				t.Fatal("protocol header rejected by browser preflight", name, response.Header())
			}
			if corsHeaderContains(response.Header().Get("Access-Control-Allow-Headers"), "X-Unregistered") {
				t.Fatal("arbitrary request headers reflected")
			}
		})
	}
}

func TestCORSProtocolResponseHeaders(t *testing.T) {
	t.Setenv("CORS_ORIGIN", "*")
	for _, status := range []int{http.StatusOK, http.StatusForbidden} {
		handler := testCORSMiddleware(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		request := httptest.NewRequest(http.MethodPost, "/luvia", nil)
		request.Header.Set("Origin", "https://app.example")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		for _, name := range []string{"Content-Encoding", "X-Luxo-Mode", "X-Trace-Id", api.TraceIDFailureHeader, api.DebugTraceResponseHeader, "Server-Timing"} {
			if !corsHeaderContains(response.Header().Get("Access-Control-Expose-Headers"), name) {
				t.Error("response header hidden from browser", status, name)
			}
		}
		if response.Code != status || response.Header().Get("Access-Control-Allow-Credentials") != "" || corsHeaderContains(response.Header().Get("Access-Control-Expose-Headers"), "Authorization") {
			t.Fatal("business status or credential boundary changed", response)
		}
	}
}

func corsHeaderContains(list, name string) bool {
	for _, value := range strings.Split(list, ",") {
		if strings.EqualFold(strings.TrimSpace(value), name) {
			return true
		}
	}
	return false
}

func TestCORSResponseHeaderScope(t *testing.T) {
	t.Setenv("CORS_ORIGIN", "https://app.example")
	handler := testCORSMiddleware(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, method := range []string{http.MethodPost, http.MethodOptions} {
		for _, origin := range []string{"", "https://app.example"} {
			request := httptest.NewRequest(method, "/luvia", nil)
			if origin != "" {
				request.Header.Set("Origin", origin)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			preflight := method == http.MethodOptions
			for _, name := range []string{"Access-Control-Allow-Methods", "Access-Control-Allow-Headers", "Access-Control-Max-Age"} {
				if (response.Header().Get(name) != "") != preflight {
					t.Error("preflight header leaked to ordinary response", method, origin, name)
				}
			}
			if (response.Header().Get("Access-Control-Expose-Headers") != "") != (!preflight && origin != "") {
				t.Error("exposed headers outside ordinary CORS response", method, origin)
			}
			if response.Header().Get("Access-Control-Allow-Origin") != "https://app.example" {
				t.Fatal("configured origin changed")
			}
		}
	}
}

func testCORSMiddleware(t testing.TB, next http.Handler) http.Handler {
	t.Helper()
	policy, err := api.ParseOriginPolicy(envOr("CORS_ORIGIN", "*"))
	if err != nil {
		t.Fatal(err)
	}
	return corsMiddleware(next, policy)
}

func buildTestMux(t testing.TB, gateway *Gateway, version string) (*http.ServeMux, string) {
	t.Helper()
	mux, port, err := gateway.buildMux(version)
	if err != nil {
		t.Fatal(err)
	}
	return mux, port
}

func TestCORSOriginListsAndRejections(t *testing.T) {
	t.Setenv("CORS_ORIGIN", "https://first.example, https://second.example, null")
	calls := 0
	handler := testCORSMiddleware(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusAccepted)
	}))
	for _, method := range []string{http.MethodPost, http.MethodOptions} {
		for _, values := range [][]string{nil, {"https://first.example"}, {"https://second.example:443"}, {"null"}, {"https://evil.example"}, {""}, {"null", "null"}} {
			request := httptest.NewRequest(method, "/luvia", nil)
			if values != nil {
				request.Header["Origin"] = values
			}
			response := httptest.NewRecorder()
			response.Header().Set("Vary", "Accept-Encoding")
			before := calls
			handler.ServeHTTP(response, request)
			allowed := values == nil || len(values) == 1 && values[0] != "" && values[0] != "https://evil.example"
			want := http.StatusForbidden
			if allowed {
				want = http.StatusAccepted
				if method == http.MethodOptions {
					want = http.StatusNoContent
				}
			}
			if response.Code != want || (calls != before) != (allowed && method == http.MethodPost) {
				t.Fatal(method, values, response.Code, calls, before)
			}
			if !corsHeaderContains(strings.Join(response.Header().Values("Vary"), ","), "Origin") || response.Header().Values("Vary")[0] != "Accept-Encoding" {
				t.Fatal("Vary lost", response.Header())
			}
			if (!allowed || values == nil) && response.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("unmatched origin exposed", response.Header())
			}
		}
	}
}

func TestInvalidOriginStopsGatewayStartup(t *testing.T) {
	for _, configured := range []string{"https://app.example/path", "*,bad", "null,", "https://app.example:65536"} {
		t.Setenv("CORS_ORIGIN", configured)
		gateway := New()
		mux, _, err := gateway.buildMux("origin-test")
		if err == nil || mux != nil || !strings.Contains(err.Error(), "CORS_ORIGIN") {
			t.Fatal("invalid configuration produced mux", err)
		}
		if err := gateway.serve("origin-test", nil); err == nil || !strings.Contains(err.Error(), "CORS_ORIGIN") || gateway.server != nil {
			t.Fatal("invalid configuration reached listener", err)
		}
	}
}
