package h2transport

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCandidateTLSConfigRejected(t *testing.T) {
	s, _ := NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, config := range []*tls.Config{nil, {MaxVersion: tls.VersionTLS11}, {GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) { return nil, nil }}} {
		if err := s.ServeTLS(nil, config); err == nil {
			t.Fatal("unsafe or unsupported TLS configuration accepted")
		}
	}
}

func TestCandidateTLSALPN(t *testing.T) {
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cert := fixture.TLS.Certificates[0]
	fixture.Close()
	config := &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"http/1.1"}}
	s, _ := NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || r.TLS.NegotiatedProtocol != "h2" {
			t.Error("missing negotiated TLS metadata")
		}
		_, _ = w.Write([]byte("ok"))
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.ServeTLS(ln, config) }()
	t.Cleanup(func() { _ = s.Close(); <-done })
	transport := &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // Test certificate only.
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	res, err := client.Post("https://"+ln.Addr().String(), "application/x-luxo", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil || string(body) != "ok" || res.ProtoMajor != 2 || config.NextProtos[0] != "http/1.1" {
		t.Fatal("TLS exchange or caller configuration changed", err)
	}
	// A TLS peer without h2 must never reach the HTTP/2 frame reader.
	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err := conn.Read(b[:]); err == nil {
		t.Fatal("non-h2 peer accepted")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("non-h2 peer was not closed")
	}
}
