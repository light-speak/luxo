package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestWebSocketUpgradeHeaderVariants(t *testing.T) {
	server := httptest.NewServer(NewRouter())
	t.Cleanup(server.Close)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	for _, test := range []struct {
		name                string
		upgrade, connection []string
		allowed             bool
	}{
		{"no_upgrade", nil, []string{"Upgrade"}, false},
		{"lowercase", []string{"websocket"}, []string{"Upgrade"}, true},
		{"mixed_case", []string{"WebSocket"}, []string{"upGRade"}, true},
		{"uppercase", []string{"WEBSOCKET"}, []string{"UPGRADE"}, true},
		{"spaces", []string{" \tWebSocket\t "}, []string{"keep-alive, Upgrade"}, true},
		{"protocol_list", []string{"h2c, websocket"}, []string{"keep-alive, Upgrade"}, true},
		{"repeated_headers", []string{"h2c", "WebSocket"}, []string{"keep-alive", "Upgrade"}, true},
		{"prefix", []string{"websocketx"}, []string{"Upgrade"}, false},
		{"suffix", []string{"xwebsocket"}, []string{"Upgrade"}, false},
		{"quoted", []string{`"websocket"`}, []string{"Upgrade"}, false},
		{"versioned", []string{"websocket/13"}, []string{"Upgrade"}, false},
		{"unicode_case", []string{"websocKet"}, []string{"Upgrade"}, false},
		{"missing_connection", []string{"websocket"}, nil, false},
		{"wrong_connection", []string{"websocket"}, []string{"keep-alive"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			client := &http.Client{Transport: upgradeHeaderTransport{transport, test.upgrade, test.connection}}
			connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &websocket.DialOptions{HTTPClient: client})
			if connection != nil {
				connection.CloseNow()
			}
			if (err == nil) != test.allowed {
				t.Fatal("upgrade outcome differs", test.upgrade, test.connection, err)
			}
		})
	}
}

type upgradeHeaderTransport struct {
	base                http.RoundTripper
	upgrade, connection []string
}

func (transport upgradeHeaderTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header["Upgrade"] = transport.upgrade
	request.Header["Connection"] = transport.connection
	return transport.base.RoundTrip(request)
}

func TestWebSocketRejectedHandshakeReleasesConnectionSlot(t *testing.T) {
	for _, sample := range []struct {
		name, method, version string
		keys                  []string
		status                int
		message               string
	}{
		{"method", "POST", "13", []string{"dGhlIHNhbXBsZSBub25jZQ=="}, 405, "method is not GET"},
		{"version", "GET", "", []string{"dGhlIHNhbXBsZSBub25jZQ=="}, 400, "unsupported WebSocket protocol version"},
		{"missing key", "GET", "13", nil, 400, "missing Sec-WebSocket-Key"},
		{"invalid key", "GET", "13", []string{"bad"}, 400, "invalid Sec-WebSocket-Key"},
		{"multiple keys", "GET", "13", []string{"dGhlIHNhbXBsZSBub25jZQ==", "dGhlIHNhbXBsZSBub25jZQ=="}, 400, "multiple Sec-WebSocket-Key"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			router := NewRouter()
			finished := make(chan struct{}, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { router.ServeHTTP(w, r); finished <- struct{}{} }))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, sample.method, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Upgrade", "websocket")
			request.Header.Set("Connection", "Upgrade")
			request.Header.Set("Sec-WebSocket-Version", sample.version)
			request.Header["Sec-Websocket-Key"] = sample.keys
			if !IsWebSocketUpgrade(request) {
				t.Fatal("fixture bypasses websocket.Accept")
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil || response.StatusCode != sample.status || !strings.Contains(string(body), sample.message) {
				t.Fatalf("handshake: %d %q / %v", response.StatusCode, body, readErr)
			}
			requireUpgradeHandlerFinished(t, ctx, finished)
			if router.wsConnections.Load() != 0 {
				t.Fatal("rejected handshake leaked connection capacity")
			}
			conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal("valid handshake failed after rejection", err)
			}
			if router.wsConnections.Load() != 1 {
				conn.CloseNow()
				t.Fatal("successful handshake did not reserve its slot")
			}
			conn.CloseNow()
			requireUpgradeHandlerFinished(t, ctx, finished)
			if router.wsConnections.Load() != 0 {
				t.Fatal("closed websocket leaked connection capacity")
			}
		})
	}
}

func requireUpgradeHandlerFinished(t *testing.T, ctx context.Context, finished <-chan struct{}) {
	t.Helper()
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("websocket handler did not finish", ctx.Err())
	}
}
