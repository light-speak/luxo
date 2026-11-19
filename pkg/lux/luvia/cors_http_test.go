package luvia

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/h2transport"
	"golang.org/x/net/http2"
	"nhooyr.io/websocket"
)

func TestCORSOverHTTPVersions(t *testing.T) {
	t.Setenv("CORS_ORIGIN", "https://app.example")
	handler := testCORSMiddleware(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Trace-Id", "cors-id")
		w.WriteHeader(http.StatusForbidden)
	}))
	for _, version := range []int{1, 2} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			server, client := corsHTTPClient(t, handler, version)
			for _, method := range []string{http.MethodOptions, http.MethodPost} {
				request, err := http.NewRequestWithContext(t.Context(), method, server.URL, nil)
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Origin", "https://app.example")
				request.Header.Set("Access-Control-Request-Method", "POST")
				request.Header.Set("Access-Control-Request-Headers", api.DebugTraceRequestHeader)
				response, err := client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				_, err = io.Copy(io.Discard, response.Body)
				response.Body.Close()
				preflight := method == http.MethodOptions
				allowed := corsHeaderContains(response.Header.Get("Access-Control-Allow-Headers"), api.DebugTraceRequestHeader)
				exposed := corsHeaderContains(response.Header.Get("Access-Control-Expose-Headers"), "X-Trace-Id")
				if err != nil || response.ProtoMajor != version || allowed != preflight || exposed == preflight {
					t.Fatal("HTTP protocol lost CORS headers", response, err)
				}
				want := http.StatusForbidden
				if method == http.MethodOptions {
					want = http.StatusNoContent
				}
				if response.StatusCode != want {
					t.Fatal(response.StatusCode, want)
				}
			}
		})
	}
}

func corsHTTPClient(t *testing.T, handler http.Handler, version int) (*httptest.Server, *http.Client) {
	t.Helper()
	transport, err := h2transport.NewServer(handler)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	if err := transport.ConfigureHTTPServer(server.Config); err != nil {
		t.Fatal(err)
	}
	server.TLS = server.Config.TLSConfig.Clone()
	server.StartTLS()
	t.Cleanup(func() { _ = transport.Close(); server.Close() })
	configuration := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	var clientTransport http.RoundTripper
	if version == 2 {
		value := &http2.Transport{TLSClientConfig: configuration}
		t.Cleanup(value.CloseIdleConnections)
		clientTransport = value
	} else {
		value := &http.Transport{TLSClientConfig: configuration, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
		t.Cleanup(value.CloseIdleConnections)
		clientTransport = value
	}
	return server, &http.Client{Transport: clientTransport, Timeout: 5 * time.Second}
}

func TestConfiguredHTTPAndWebSocketOrigin(t *testing.T) {
	t.Setenv("CORS_ORIGIN", "https://app.example:8443")
	t.Setenv("APP_ENV", "production")
	gw := New()
	mux, _ := buildTestMux(t, gw, "origin-audit")
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	for _, test := range []struct {
		name, origin string
		allowed      bool
	}{
		{"native", "", true},
		{"configured", "https://app.example:8443", true},
		{"different_scheme", "http://app.example:8443", false},
		{"different_port", "https://app.example:8444", false},
		{"different_host", "https://other.example:8443", false},
		{"gateway_host", server.URL, false},
		{"origin_with_path", "https://app.example:8443/path", false},
		{"opaque_origin", "null", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			headers := make(http.Header)
			if test.origin != "" {
				headers.Set("Origin", test.origin)
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodOptions, server.URL+"/luvia", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header = headers.Clone()
			result, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			result.Body.Close()
			if (result.StatusCode == http.StatusNoContent) != test.allowed {
				t.Errorf("HTTP origin %q status %d allowed %v", test.origin, result.StatusCode, test.allowed)
			}
			connection, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/luvia", &websocket.DialOptions{
				HTTPHeader: headers,
			})
			if connection != nil {
				connection.CloseNow()
			}
			status := 0
			if response != nil {
				status = response.StatusCode
			}
			t.Logf("origin=%q status=%d accepted=%t error=%v", test.origin, status, err == nil, err)
			if (err == nil) != test.allowed {
				t.Fatal("WebSocket origin differs from configured HTTP origin")
			}
		})
	}
}

func TestConfiguredOriginSurvivesConcurrentPrepare(t *testing.T) {
	t.Setenv("CORS_ORIGIN", "https://app.example:8443")
	t.Setenv("APP_ENV", "production")
	for range 16 {
		gateway := New()
		prepared := make(chan error, 1)
		go func() { prepared <- gateway.Router.Prepare() }()
		mux, _ := buildTestMux(t, gateway, "origin-prepare-test")
		if err := <-prepared; err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)
		for _, origin := range []string{"https://app.example:8443", "http://app.example:8443"} {
			assertPreparedGatewayOrigin(t, server, origin, origin == "https://app.example:8443")
		}
		server.Close()
	}
}

func assertPreparedGatewayOrigin(t *testing.T, server *httptest.Server, origin string, allowed bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodOptions, server.URL+"/luvia", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", origin)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if (response.StatusCode == http.StatusNoContent) != allowed {
		t.Fatal("HTTP lost configured policy", origin, response.StatusCode)
	}
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/luvia", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{origin}},
	})
	if connection != nil {
		connection.CloseNow()
	}
	if (err == nil) != allowed {
		t.Fatal("prepared WebSocket policy differs from HTTP", origin, allowed, err)
	}
}

func TestCORSOriginPolicyOverHTTPVersions(t *testing.T) {
	t.Setenv("CORS_ORIGIN", "https://app.example, null")
	handler := testCORSMiddleware(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	for _, version := range []int{1, 2} {
		server, client := corsHTTPClient(t, handler, version)
		for _, origin := range []string{"https://APP.example:443", "null", "http://app.example", server.URL, "https://app.example/path"} {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Origin", origin)
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			allowed := origin == "https://APP.example:443" || origin == "null"
			if err != nil || response.ProtoMajor != version || (response.StatusCode == http.StatusAccepted) != allowed {
				t.Fatal(version, origin, response, err)
			}
			if allowed && response.Header.Get("Access-Control-Allow-Origin") != origin ||
				!allowed && response.Header.Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("wrong origin response", response.Header)
			}
		}
	}
}
