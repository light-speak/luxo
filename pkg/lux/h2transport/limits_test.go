package h2transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

func TestUnknownLengthBodyLimitKeepsConnectionUsable(t *testing.T) {
	var calls atomic.Int32
	client, endpoint, dials := limitedBodyClient(t, &calls)
	for _, size := range []int{32, 33, 32} {
		body := bytes.Repeat([]byte{'x'}, size)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, io.NopCloser(bytes.NewReader(body)))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if size > 32 {
			if resp != nil {
				_ = resp.Body.Close()
			}
			var streamErr http2.StreamError
			if !errors.As(err, &streamErr) || streamErr.Code != http2.ErrCodeEnhanceYourCalm {
				t.Fatalf("oversized stream was not rejected: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		got, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil || resp.ProtoMajor != 2 || resp.StatusCode != 200 || !bytes.Equal(got, body) {
			t.Fatalf("legal stream failed: status=%d body=%q err=%v", resp.StatusCode, got, readErr)
		}
	}
	if calls.Load() != 2 || dials.Load() != 1 {
		t.Fatalf("calls=%d dials=%d, want 2 calls on one connection", calls.Load(), dials.Load())
	}
}

func limitedBodyClient(t *testing.T, calls *atomic.Int32) (*http.Client, string, *atomic.Int32) {
	t.Helper()
	limits := DefaultLimits()
	limits.MaxRequestBodyBytes = 32
	s, err := NewServerWithLimits(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.ProtoMajor != 2 || r.Header.Get("Content-Length") != "" {
			t.Error("fixture did not send unknown-length HTTP/2 DATA")
		}
		if _, err := io.Copy(w, r.Body); err != nil {
			t.Error(err)
		}
	}), limits)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(ln) }()
	t.Cleanup(func() {
		_ = s.Close()
		<-done
		s.workers.Wait()
		if s.budget.used.Load() != 0 || s.streams.used.Load() != 0 {
			t.Error("server retained budget after shutdown")
		}
	})
	dials := &atomic.Int32{}
	transport := &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, _, addr string, _ *tls.Config) (net.Conn, error) {
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 3 * time.Second}, "http://" + ln.Addr().String(), dials
}

func TestExplicitLimitsValidation(t *testing.T) {
	for _, change := range []func(*Limits){
		func(l *Limits) { l.MaxRequestBodyBytes = 0 },
		func(l *Limits) { l.MaxResponseBodyBytes = 0 },
		func(l *Limits) { l.MaxRequestBodyBytes = int(l.MaxBufferedBytes) + 1 },
		func(l *Limits) { l.MaxResponseBodyBytes = int(l.MaxBufferedBytes) + 1 },
		func(l *Limits) { l.MaxConnections = 0 }, func(l *Limits) { l.MaxConnections = 65536 },
		func(l *Limits) { l.MaxInFlight = 0 }, func(l *Limits) { l.MaxInFlight = 65536 },
		func(l *Limits) { l.MaxBufferedBytes = 1 }, func(l *Limits) { l.MaxBufferedBytes = 1 << 41 },
		func(l *Limits) { l.ControlFramesPerSecond = 0 }, func(l *Limits) { l.HandshakeTimeout = 0 },
		func(l *Limits) { l.BodyTimeout = 0 }, func(l *Limits) { l.ResponseTimeout = 0 },
		func(l *Limits) { l.IdleTimeout = 0 }, func(l *Limits) { l.WriteTimeout = 0 },
	} {
		limits := DefaultLimits()
		change(&limits)
		if _, err := NewServerWithLimits(http.NotFoundHandler(), limits); err == nil {
			t.Fatal("invalid limits accepted", limits)
		}
	}
}
