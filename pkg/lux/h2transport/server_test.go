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

func TestCandidateStandardClient(t *testing.T) {
	s, err := NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 || r.Header.Get("Authorization") != "Bearer test" {
			t.Error("lost HTTP metadata")
		}
		w.Header().Set("Content-Type", "application/x-luxo")
		if _, err := io.Copy(w, r.Body); err != nil {
			t.Error(err)
		}
	}))
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
			t.Error("closed server leaked resource budget")
		}
	})
	transport := &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, _, addr string, _ *tls.Config) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for _, size := range []int{0, 1, 4096, 131072} {
		body := bytes.Repeat([]byte{'x'}, size)
		request, _ := http.NewRequest(http.MethodPost, "http://"+ln.Addr().String()+"/", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer test")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(size, err)
		}
		got, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || !bytes.Equal(body, got) || response.StatusCode != 200 {
			t.Fatal(size, err)
		}
	}
}

type scriptedListener struct {
	accept func() (net.Conn, error)
	closed bool
}

func (l *scriptedListener) Accept() (net.Conn, error) { return l.accept() }
func (l *scriptedListener) Close() error              { l.closed = true; return nil }
func (l *scriptedListener) Addr() net.Addr            { return nil }

func TestServeRejectsDuplicateOrClosedListener(t *testing.T) {
	for _, closed := range []bool{false, true} {
		s, err := NewServer(http.NotFoundHandler())
		if err != nil {
			t.Fatal(err)
		}
		s.closed = closed
		if !closed {
			s.listener = &scriptedListener{}
		}
		listener := &scriptedListener{accept: func() (net.Conn, error) {
			t.Fatal("rejected listener called Accept")
			return nil, io.EOF
		}}
		if err := s.Serve(listener); !errors.Is(err, net.ErrClosed) || !listener.closed {
			t.Fatal("rejected listener was not closed", err)
		}
	}
}

func TestServeContinuesAfterConnectionLimit(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxConnections = 1
	s, err := NewServerWithLimits(http.NotFoundHandler(), limits)
	if err != nil {
		t.Fatal(err)
	}
	first, rejected := &faultConn{}, &faultConn{}
	_, cancel, err := s.admitConnection(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = s.Close(); s.workers.Done() }()
	accepts := 0
	listener := &scriptedListener{accept: func() (net.Conn, error) {
		accepts++
		if accepts == 1 {
			return rejected, nil
		}
		return nil, io.EOF
	}}
	if err := s.Serve(listener); !errors.Is(err, io.EOF) || accepts != 2 {
		t.Fatal("limit stopped accept loop", err, accepts)
	}
	if !rejected.closed.Load() || first.closed.Load() || len(s.connections) != 1 {
		t.Fatal("limit rejection changed existing admission")
	}
}

func TestServeStopsWhenClosedDuringAccept(t *testing.T) {
	s, err := NewServer(http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	conn := &faultConn{}
	accepts := 0
	listener := &scriptedListener{accept: func() (net.Conn, error) {
		accepts++
		if accepts > 1 {
			t.Fatal("closed server continued accepting")
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		return conn, nil
	}}
	if err := s.Serve(listener); !errors.Is(err, net.ErrClosed) {
		t.Fatal("close during accept was ignored", err)
	}
	if !conn.closed.Load() || !listener.closed || len(s.connections) != 0 {
		t.Fatal("closed accept retained resources")
	}
}

func TestBoundedWindowSlowUploadReleasesBudget(t *testing.T) {
	limits := DefaultLimits()
	limits.BodyTimeout = 200 * time.Millisecond
	var calls atomic.Int32
	s, err := NewServerWithLimits(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}), limits)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			served <- acceptErr
			return
		}
		served <- s.ServeConn(t.Context(), conn)
	}()
	t.Cleanup(func() { _ = listener.Close(); _ = s.Close(); <-finished })
	client, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	framer := beginIncompleteWindowUpload(t, client)
	for {
		_, err = framer.ReadFrame()
		if err != nil {
			break
		}
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		t.Fatal("client deadline fired before server stopped incomplete upload", err)
	}
	serverErr := <-served
	if !errors.As(serverErr, &timeout) || !timeout.Timeout() {
		t.Fatal("server must end upload on its read deadline", serverErr)
	}
	if calls.Load() != 0 || s.streams.used.Load() != 0 || s.budget.used.Load() != 0 {
		t.Fatal("incomplete upload executed or leaked resources", calls.Load(), s.streams.used.Load(), s.budget.used.Load())
	}
}

func beginIncompleteWindowUpload(t *testing.T, client net.Conn) *http2.Framer {
	t.Helper()
	if _, err := client.Write(openingBytes(t)); err != nil {
		t.Fatal(err)
	}
	framer := http2.NewFramer(client, client)
	frame, err := framer.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	settings, ok := frame.(*http2.SettingsFrame)
	if !ok {
		t.Fatal("missing server settings", frame)
	}
	if window, ok := settings.Value(http2.SettingInitialWindowSize); !ok || window != 2<<20 {
		t.Fatal("test did not use expanded stream window", window)
	}
	if err := framer.WriteSettingsAck(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(headerWire(t, requestFields(), false)); err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 16<<10)
	for range 4 {
		if err := framer.WriteData(1, false, chunk); err != nil {
			t.Fatal(err)
		}
	}
	return framer
}
