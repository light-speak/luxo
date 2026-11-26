package luvia

import (
	"bufio"
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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"nhooyr.io/websocket"
)

func TestGatewayShutdownOwnsHTTP2UpgradeRequests(t *testing.T) {
	for _, mode := range []string{"luxo", "standard"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("APP_HTTP_TRANSPORT", mode)
			server, done, err := startGatewayServer("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, r.Proto)
			}), "", "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = server.Close(); <-done })
			conn, err := net.DialTimeout("tcp", server.Addr, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			_, err = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade, HTTP2-Settings\r\nUpgrade: h2c\r\nHTTP2-Settings: AAMAAABk\r\n\r\n")
			if err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReader(conn)
			response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
			if err != nil {
				t.Fatal(err)
			}
			assertUpgradeShutdown(t, server, conn, reader, response)
		})
	}
}

func TestGatewayStandardProtocolDispatch(t *testing.T) {
	t.Setenv("APP_HTTP_TRANSPORT", "standard")
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleartext", true: "tls"}[secure], func(t *testing.T) {
			cert, key := "", ""
			if secure {
				cert, key = generateTestCert(t)
			}
			server, done, err := startGatewayServer("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, r.Proto)
			}), cert, key)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = server.Close(); <-done })
			for _, h2 := range []bool{false, true} {
				assertStandardProtocol(t, server.Addr, secure, h2)
			}
		})
	}
}

func TestGatewayHTTP2ShutdownStopsNewStreams(t *testing.T) {
	for _, mode := range []string{"luxo", "standard"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("APP_HTTP_TRANSPORT", mode)
			var calls atomic.Int32
			server, done, err := startGatewayServer("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.WriteString(w, r.Proto)
			}), "", "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = server.Close(); <-done })
			conn, err := net.DialTimeout("tcp", server.Addr, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			transport := &http2.Transport{}
			peer, err := transport.NewClientConn(conn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = peer.Close() })
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+server.Addr, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := peer.RoundTrip(request)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || string(body) != "HTTP/2.0" {
				t.Fatalf("prior-knowledge HTTP/2 failed: %q, %v", body, err)
			}
			assertHTTP2PeerStops(t, server, peer, request)
			if calls.Load() != 1 {
				t.Fatal("business handler ran after gateway shutdown")
			}
		})
	}
}

func assertHTTP2PeerStops(t *testing.T, server *gatewayHTTPServer, peer *http2.ClientConn, request *http.Request) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	response, err := peer.RoundTrip(request.Clone(t.Context()))
	if err == nil {
		_ = response.Body.Close()
		t.Fatal("closed gateway accepted another stream on the same HTTP/2 peer")
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		t.Fatal("request timed out instead of being rejected by the closed peer")
	}
}

func BenchmarkGatewayStandardStartStop(b *testing.B) {
	b.Setenv("APP_HTTP_TRANSPORT", "standard")
	handler := http.NotFoundHandler()
	b.ReportAllocs()
	for b.Loop() {
		server, done, err := startGatewayServer("127.0.0.1:0", handler, "", "")
		if err != nil {
			b.Fatal(err)
		}
		if err := server.Close(); err != nil {
			b.Fatal(err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			b.Fatalf("standard listener did not stop: %v", err)
		}
	}
}

func assertStandardProtocol(t *testing.T, addr string, secure, h2 bool) {
	t.Helper()
	config := &tls.Config{InsecureSkipVerify: true}
	var transport http.RoundTripper
	if h2 {
		h := &http2.Transport{TLSClientConfig: config}
		if !secure {
			h.AllowHTTP = true
			h.DialTLSContext = func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			}
		}
		transport = h
		t.Cleanup(h.CloseIdleConnections)
	} else {
		h := &http.Transport{TLSClientConfig: config, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
		transport = h
		t.Cleanup(h.CloseIdleConnections)
	}
	scheme := "http://"
	if secure {
		scheme = "https://"
	}
	client := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := client.Get(scheme + addr)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	want := "HTTP/1.1"
	if h2 {
		want = "HTTP/2.0"
	}
	if err != nil || string(data) != want || response.Proto != want {
		t.Fatalf("standard protocol secure=%t h2=%t: status=%s body=%q err=%v", secure, h2, response.Proto, data, err)
	}
}

func assertUpgradeShutdown(t *testing.T, server *gatewayHTTPServer, conn net.Conn, reader *bufio.Reader, response *http.Response) {
	t.Helper()
	var framer *http2.Framer
	if response.StatusCode == http.StatusSwitchingProtocols {
		framer = completeUpgradeHandshake(t, conn, reader)
	} else {
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || string(body) != "HTTP/1.1" {
			t.Fatalf("unsupported Upgrade was not handled as HTTP/1.1: status=%d body=%q err=%v", response.StatusCode, body, err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if framer != nil {
		assertUpgradedPeerClosed(t, framer)
		return
	}
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatalf("shutdown left HTTP/1 peer open: %v", err)
	}
}

func completeUpgradeHandshake(t *testing.T, conn net.Conn, reader *bufio.Reader) *http2.Framer {
	t.Helper()
	if _, err := io.WriteString(conn, http2.ClientPreface); err != nil {
		t.Fatal(err)
	}
	framer := http2.NewFramer(conn, reader)
	if err := framer.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	for {
		frame, err := framer.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		switch frame := frame.(type) {
		case *http2.SettingsFrame:
			if !frame.IsAck() {
				if err := framer.WriteSettingsAck(); err != nil {
					t.Fatal(err)
				}
			}
		case *http2.DataFrame:
			if frame.StreamID == 1 && frame.StreamEnded() {
				if string(frame.Data()) != "HTTP/2.0" {
					t.Fatalf("unexpected upgraded response: %q", frame.Data())
				}
				return framer
			}
		}
	}
}

func assertUpgradedPeerClosed(t *testing.T, framer *http2.Framer) {
	t.Helper()
	var headers bytes.Buffer
	encoder := hpack.NewEncoder(&headers)
	for _, field := range []hpack.HeaderField{{Name: ":method", Value: "GET"}, {Name: ":scheme", Value: "http"}, {Name: ":authority", Value: "localhost"}, {Name: ":path", Value: "/after-shutdown"}} {
		if err := encoder.WriteField(field); err != nil {
			t.Fatal(err)
		}
	}
	if err := framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 3, BlockFragment: headers.Bytes(), EndHeaders: true, EndStream: true}); err != nil {
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			t.Fatal("shutdown left upgraded peer blocking writes")
		}
		return
	}
	for {
		frame, err := framer.ReadFrame()
		if err != nil {
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				t.Fatal("shutdown left upgraded peer open without closing or GOAWAY")
			}
			return
		}
		switch frame := frame.(type) {
		case *http2.GoAwayFrame:
			return
		case *http2.HeadersFrame:
			if frame.StreamID == 3 {
				t.Fatal("gateway accepted another upgraded HTTP/2 request after Shutdown returned")
			}
		}
	}
}

func TestGatewayRejectsNilRouteBeforeTransportStartup(t *testing.T) {
	t.Setenv("APP_PORT", "invalid")
	t.Setenv("LUXO_STUDIO_URL", "")
	for _, transport := range []string{"standard", "luxo"} {
		t.Run(transport, func(t *testing.T) {
			t.Setenv("APP_HTTP_TRANSPORT", transport)
			gateway := New()
			gateway.Router.Handle("missing", nil)
			quit := make(chan os.Signal, 1)
			quit <- os.Interrupt
			err := gateway.serve("nil-route-test", quit)
			if err == nil || !strings.Contains(err.Error(), `"missing"`) {
				t.Fatalf("route validation did not precede transport startup: %v", err)
			}
			if gateway.server != nil {
				t.Fatal("invalid route created HTTP server")
			}
		})
	}
}

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

func TestGatewayRejectsIncompleteTLSConfiguration(t *testing.T) {
	for _, mode := range []string{"standard", "luxo"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("APP_HTTP_TRANSPORT", mode)
			for _, sample := range []struct{ cert, key string }{{cert: "cert.pem"}, {key: "key.pem"}} {
				server, done, err := startGatewayServer("127.0.0.1:0", http.NotFoundHandler(), sample.cert, sample.key)
				if server != nil {
					_ = server.Close()
					<-done
				}
				if err == nil || !strings.Contains(err.Error(), "APP_TLS_CERT and APP_TLS_KEY") || server != nil || done != nil {
					t.Errorf("incomplete TLS configuration started a server: cert=%q key=%q err=%v", sample.cert, sample.key, err)
				}
			}
		})
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
