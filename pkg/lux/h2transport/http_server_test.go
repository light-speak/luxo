package h2transport

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

func TestHTTPServerProtocolDispatch(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleartext", true: "tls"}[secure], func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestConfigureHTTPServerRejectsInvalidConfiguration(t *testing.T) {
	s, err := NewServer(http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	for _, server := range []*http.Server{
		nil,
		{TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){}},
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
