package luvia

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/h2transport"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"nhooyr.io/websocket"
)

func TestGatewayStandardCloseReleasesListener(t *testing.T) {
	t.Setenv("APP_HTTP_TRANSPORT", "standard")
	server, done, err := startGatewayServer("127.0.0.1:0", http.NotFoundHandler(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if server.h2 != nil {
		t.Fatal("standard mode installed dedicated HTTP/2 transport")
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("standard server did not stop")
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatal("closed listener still owns its address", err)
	}
	_ = listener.Close()
}

func TestGatewayLuxoHTTPTransport(t *testing.T) {
	t.Setenv("APP_HTTP_TRANSPORT", "luxo")
	server, done, err := startGatewayServer("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}), "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(); <-done })
	if server.h2 == nil {
		t.Fatal("Luxo transport not installed")
	}
	transport := &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: time.Second}
	res, err := client.Get("http://" + server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil || string(data) != "ok" || res.ProtoMajor != 2 {
		t.Fatalf("response: %q %v", data, err)
	}
}

func TestGatewayDefaultHTTPTransport(t *testing.T) {
	t.Setenv("APP_HTTP_TRANSPORT", "")
	if err := os.Unsetenv("APP_HTTP_TRANSPORT"); err != nil {
		t.Fatal(err)
	}
	server, done, err := startGatewayServer("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}), "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(); <-done })
	if server.h2 == nil {
		t.Fatal("default gateway did not install Luxo HTTP/2")
	}
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := client.Get("http://" + server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "ok" || response.ProtoMajor != 1 {
		t.Fatalf("HTTP/1 fallback: %q, HTTP/%d, %v", body, response.ProtoMajor, err)
	}
}

func TestGatewayRejectsUnknownHTTPTransport(t *testing.T) {
	t.Setenv("APP_HTTP_TRANSPORT", "unknown")
	server, done, err := startGatewayServer("127.0.0.1:0", http.NotFoundHandler(), "", "")
	if err == nil || server != nil || done != nil {
		t.Fatal("invalid transport accepted")
	}
}

func TestGatewayLuxoBodyLimits(t *testing.T) {
	t.Setenv("APP_HTTP_TRANSPORT", "luxo")
	server, done, err := startGatewayServerWithBodyLimits("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/large" {
			_, _ = w.Write(make([]byte, 65))
			return
		}
		_, _ = io.Copy(w, r.Body)
	}), "", "", api.BodyLimits{RequestBytes: 32, ResponseBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(); <-done })
	transport := &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: time.Second}
	for _, sample := range []struct {
		path    string
		size    int
		success bool
	}{{"/", 32, true}, {"/", 33, false}, {"/large", 1, false}, {"/", 16, true}} {
		res, err := client.Post("http://"+server.Addr+sample.path, "application/x-luxo", bytes.NewReader(make([]byte, sample.size)))
		if err == nil {
			_, err = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
		}
		if (err == nil) != sample.success {
			t.Fatalf("%+v: %v", sample, err)
		}
	}
}

func TestGatewayLuxoTransportPreservesWebSocket(t *testing.T) {
	t.Setenv("APP_HTTP_TRANSPORT", "luxo")
	for _, secure := range []bool{false, true} {
		cert, key := "", ""
		if secure {
			cert, key = generateTestCert(t)
		}
		server, done, err := startGatewayServer("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.CloseNow()
			kind, data, err := conn.Read(r.Context())
			if err != nil {
				t.Error(err)
				return
			}
			if err := conn.Write(r.Context(), kind, data); err != nil {
				t.Error(err)
			}
		}), cert, key)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		scheme := "ws://"
		if secure {
			scheme = "wss://"
		}
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
		conn, _, err := websocket.Dial(ctx, scheme+server.Addr, &websocket.DialOptions{HTTPClient: client})
		if err != nil {
			_ = server.Close()
			cancel()
			t.Fatal(err)
		}
		if err := conn.Write(ctx, websocket.MessageBinary, []byte("binary")); err != nil {
			t.Fatal(err)
		}
		kind, data, err := conn.Read(ctx)
		if err != nil || kind != websocket.MessageBinary || string(data) != "binary" {
			t.Fatalf("websocket response %q: %v", data, err)
		}
		_ = conn.CloseNow()
		if err := server.Shutdown(ctx); err != nil {
			t.Fatal(err)
		}
		cancel()
		if err := <-done; err != http.ErrServerClosed {
			t.Fatal(err)
		}
	}
}

func TestGatewayConfigurationFailureReleasesListener(t *testing.T) {
	for _, tc := range []struct {
		name   string
		server *http.Server
	}{
		{"existing-dispatch", &http.Server{TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){}}},
		{"obsolete-tls", &http.Server{TLSConfig: &tls.Config{MaxVersion: tls.VersionTLS11}}},
		{"dynamic-tls", &http.Server{TLSConfig: &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) { return nil, nil }}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h2, err := h2transport.NewServer(http.NotFoundHandler())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = h2.Close() })
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			server, done, err := startConfiguredGatewayServer(listener, tc.server, h2, false)
			if err == nil || server != nil || done != nil {
				t.Fatalf("configuration failure lost: %v %v %v", server, done, err)
			}
			if _, acceptErr := listener.Accept(); !errors.Is(acceptErr, net.ErrClosed) {
				t.Fatalf("listener not closed: %v", acceptErr)
			}
			rebound, err := net.Listen("tcp", listener.Addr().String())
			if err != nil {
				t.Fatalf("port retained after failure: %v", err)
			}
			_ = rebound.Close()
		})
	}
}
