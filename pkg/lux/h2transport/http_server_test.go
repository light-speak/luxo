package h2transport

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

func TestHTTPServerRejectsTLSWithoutH2ALPN(t *testing.T) {
	for _, protocols := range [][]string{nil, {"http/1.1"}} {
		t.Run(strings.Join(protocols, ","), func(t *testing.T) {
			var calls atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				_, _ = io.WriteString(w, "unexpected HTTP/2 business dispatch")
			})
			server, err := NewServer(handler)
			if err != nil {
				t.Fatal(err)
			}
			https := httptest.NewUnstartedServer(handler)
			if err := server.ConfigureHTTPServer(https.Config); err != nil {
				t.Fatal(err)
			}
			https.TLS = https.Config.TLSConfig.Clone()
			https.StartTLS()
			t.Cleanup(func() { _ = server.Close(); https.Close() })
			conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", https.Listener.Addr().String(), &tls.Config{InsecureSkipVerify: true, NextProtos: protocols})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			if conn.ConnectionState().NegotiatedProtocol == "h2" {
				t.Fatal("probe unexpectedly negotiated h2")
			}
			assertTLSWithoutH2Rejected(t, conn, https.URL, &calls)
		})
	}
}

func assertTLSWithoutH2Rejected(t *testing.T, conn *tls.Conn, endpoint string, calls *atomic.Int32) {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	transport := &http2.Transport{}
	peer, err := transport.NewClientConn(conn)
	if err == nil {
		t.Cleanup(func() { _ = peer.Close() })
		request, createErr := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil)
		if createErr != nil {
			t.Fatal(createErr)
		}
		response, requestErr := peer.RoundTrip(request)
		err = requestErr
		if response != nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}
	if err == nil || calls.Load() != 0 {
		t.Fatalf("TLS peer negotiated %q but reached HTTP/2 business: calls=%d err=%v", conn.ConnectionState().NegotiatedProtocol, calls.Load(), err)
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("non-h2 TLS peer timed out instead of being rejected")
	}
}

func TestPriorKnowledgeRejectsTLSBeforeHandoff(t *testing.T) {
	server, err := NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("non-h2 TLS peer reached business handler")
	}))
	if err != nil {
		t.Fatal(err)
	}
	configured := &http.Server{}
	if err := server.ConfigureHTTPServer(configured); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("PRI", "*", nil)
	request.Proto, request.ProtoMajor, request.ProtoMinor = "HTTP/2.0", 2, 0
	request.TLS = &tls.ConnectionState{NegotiatedProtocol: "http/1.1"}
	response := httptest.NewRecorder()
	configured.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || response.Header().Get("Connection") != "close" {
		t.Fatalf("invalid TLS handoff was not rejected and closed: status=%d headers=%v", response.Code, response.Header())
	}
}

func TestHTTPServerIgnoresObsoleteHTTP2Upgrade(t *testing.T) {
	for _, settings := range []string{"AAMAAABk", "invalid-settings"} {
		t.Run(settings, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor != 1 {
					t.Fatal("obsolete Upgrade bypassed HTTP/1 ownership")
				}
				_, _ = io.WriteString(w, "HTTP/1.1")
			})
			server, err := NewServer(handler)
			if err != nil {
				t.Fatal(err)
			}
			configured := &http.Server{}
			if err := server.ConfigureHTTPServer(configured); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("request"))
			request.Header.Set("Connection", "Upgrade, HTTP2-Settings")
			request.Header.Set("Upgrade", "h2c")
			request.Header.Set("HTTP2-Settings", settings)
			response := httptest.NewRecorder()
			configured.Handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "HTTP/1.1" {
				t.Fatalf("obsolete negotiation escaped normal handler: status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
}

func TestHTTPServerProtocolDispatch(t *testing.T) {
	type contextKey int
	const (
		baseKey contextKey = iota
		connKey
	)
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleartext", true: "tls"}[secure], func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Context().Value(baseKey) != "listener" || r.Context().Value(connKey) != "connection" {
					t.Errorf("%s did not inherit the configured HTTP server context", r.Proto)
				}
				if r.ProtoMajor == 2 {
					if _, ok := w.(*response); !ok {
						t.Error("HTTP/2 bypassed Luxo transport")
					}
				}
				_, _ = io.WriteString(w, r.Proto)
			})
			s, err := NewServer(handler)
			if err != nil {
				t.Fatal(err)
			}
			httpServer := httptest.NewUnstartedServer(handler)
			httpServer.Config.BaseContext = func(net.Listener) context.Context {
				return context.WithValue(context.Background(), baseKey, "listener")
			}
			httpServer.Config.ConnContext = func(ctx context.Context, _ net.Conn) context.Context {
				return context.WithValue(ctx, connKey, "connection")
			}
			if err := s.ConfigureHTTPServer(httpServer.Config); err != nil {
				t.Fatal(err)
			}
			if secure {
				httpServer.TLS = httpServer.Config.TLSConfig.Clone()
				httpServer.StartTLS()
			} else {
				httpServer.Start()
			}
			t.Cleanup(func() { _ = s.Close(); httpServer.Close() })
			for _, h2 := range []bool{false, true} {
				var transport http.RoundTripper
				if h2 {
					h := &http2.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
					if !secure {
						h.AllowHTTP = true
						h.DialTLSContext = func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
							return (&net.Dialer{}).DialContext(ctx, network, addr)
						}
					}
					transport = h
					t.Cleanup(h.CloseIdleConnections)
				} else {
					h := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
					transport = h
					t.Cleanup(h.CloseIdleConnections)
				}
				client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
				res, err := client.Get(httpServer.URL)
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(res.Body)
				_ = res.Body.Close()
				want := "HTTP/1.1"
				if h2 {
					want = "HTTP/2.0"
				}
				if err != nil || string(data) != want {
					t.Fatalf("response %q: %v", data, err)
				}
			}
		})
	}
}

func TestTLSConnectionCancellationKeepsListener(t *testing.T) {
	entered := make(chan struct{}, 1)
	canceled := make(chan error, 1)
	release := make(chan struct{})
	defer close(release)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hold" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		select {
		case entered <- struct{}{}:
		case <-release:
			return
		}
		select {
		case <-r.Context().Done():
			select {
			case canceled <- r.Context().Err():
			case <-release:
			}
		case <-release:
		}
	})
	server, err := NewServer(handler)
	if err != nil {
		t.Fatal(err)
	}
	https := httptest.NewUnstartedServer(handler)
	firstCancel := make(chan context.CancelFunc, 1)
	var connections atomic.Int32
	https.Config.ConnContext = func(ctx context.Context, _ net.Conn) context.Context {
		if connections.Add(1) != 1 {
			return ctx
		}
		ctx, cancel := context.WithCancel(ctx)
		firstCancel <- cancel
		return ctx
	}
	if err := server.ConfigureHTTPServer(https.Config); err != nil {
		t.Fatal(err)
	}
	https.TLS = https.Config.TLSConfig.Clone()
	https.StartTLS()
	t.Cleanup(func() { _ = server.Close(); https.Close() })

	newClient := func() *http.Client {
		transport := &http2.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
		t.Cleanup(transport.CloseIdleConnections)
		return &http.Client{Transport: transport, Timeout: 5 * time.Second}
	}
	first := newClient()
	done := make(chan error, 1)
	go func() {
		response, err := first.Get(https.URL + "/hold")
		if response != nil {
			_ = response.Body.Close()
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("TLS HTTP/2 request did not reach the handler")
	}
	select {
	case cancel := <-firstCancel:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("TLS connection cancellation hook was not called")
	}
	select {
	case err := <-canceled:
		if err != context.Canceled {
			t.Fatalf("request context error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TLS connection context cancellation did not reach the request")
	}
	if err := <-done; err == nil {
		t.Fatal("canceled connection returned a response")
	}

	next, err := newClient().Get(https.URL + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	defer next.Body.Close()
	if next.ProtoMajor != 2 || next.StatusCode != http.StatusNoContent || connections.Load() < 2 {
		t.Fatalf("new TLS connection returned %s %d, accepted %d", next.Proto, next.StatusCode, connections.Load())
	}
}

func TestConfigureHTTPServerRejectsInvalidConfiguration(t *testing.T) {
	s, err := NewServer(http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	for _, server := range []*http.Server{
		nil,
		{TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){}},
		{Protocols: &http.Protocols{}},
		{TLSConfig: &tls.Config{MaxVersion: tls.VersionTLS11}},
		{TLSConfig: &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) { return nil, nil }}},
	} {
		if err := s.ConfigureHTTPServer(server); err == nil {
			t.Fatal("invalid HTTP server configuration accepted")
		}
		if server != nil && server.Handler != nil {
			t.Fatal("failed configuration installed a handler")
		}
	}
	if _, err := NewServer(nil); err == nil {
		t.Fatal("missing business handler accepted")
	}
}

func TestConfigureHTTPServerPreservesCallerTLSConfig(t *testing.T) {
	s, err := NewServer(http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	config := &tls.Config{MinVersion: tls.VersionTLS10, NextProtos: []string{"original"}}
	server := &http.Server{TLSConfig: config}
	if err := s.ConfigureHTTPServer(server); err != nil {
		t.Fatal(err)
	}
	if server.TLSConfig == config || config.MinVersion != tls.VersionTLS10 || len(config.NextProtos) != 1 || config.NextProtos[0] != "original" {
		t.Fatal("caller TLS configuration was mutated")
	}
	if server.TLSConfig.MinVersion != tls.VersionTLS12 || len(server.TLSConfig.NextProtos) != 2 || server.TLSConfig.NextProtos[0] != "h2" || server.TLSConfig.NextProtos[1] != "http/1.1" {
		t.Fatal("protocol dispatch was not configured")
	}
}

func TestPriorKnowledgeRejectsUnavailableHandoff(t *testing.T) {
	s, err := NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("failed handoff reached business handler")
	}))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{}
	if err := s.ConfigureHTTPServer(server); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("PRI", "http://example.test/", nil)
	req.RequestURI, req.Proto = "*", "HTTP/2.0"
	res := httptest.NewRecorder()
	server.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError || len(s.connections) != 0 {
		t.Fatal("failed handoff was accepted", res.Code)
	}
}
