package h2transport

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeConnClosedAndCanceled(t *testing.T) {
	for _, closed := range []bool{false, true} {
		s, err := NewServer(http.NotFoundHandler())
		if err != nil {
			t.Fatal(err)
		}
		server, client := net.Pipe()
		t.Cleanup(func() { _ = client.Close() })
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if closed {
			_ = s.Close()
		}
		if err := s.ServeConn(ctx, server); err == nil {
			t.Fatal("expected rejection")
		}
		if _, err := client.Write([]byte{0}); err == nil {
			t.Fatal("rejected connection remained open")
		}
	}
}

func TestServeConnShutdownCancelsOpening(t *testing.T) {
	s, err := NewServer(http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = s.Close() })
	done := make(chan error, 1)
	go func() { done <- s.ServeConn(t.Context(), server) }()
	// The opening read proves the connection has been registered.
	if err := client.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("P")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("opening connection survived shutdown")
	}
	if len(s.connections) != 0 {
		t.Fatal("connection registration leaked")
	}
}

func TestServeConnContextCancellation(t *testing.T) {
	s, err := NewServer(http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = s.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.ServeConn(ctx, server) }()
	if err := client.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("P")); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled connection remained blocked")
	}
	if err := s.ServeConn(t.Context(), nil); err == nil {
		t.Fatal("nil connection accepted")
	}
}

func TestServeConnDeadlineFailureReleasesAdmission(t *testing.T) {
	s, err := NewServer(http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	conn := &faultConn{failDeadline: 1}
	if err := s.ServeConn(t.Context(), conn); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("deadline failure was not propagated", err)
	}
	if !conn.closed.Load() || len(s.connections) != 0 {
		t.Fatal("failed deadline left connection admitted")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal("failed deadline leaked a worker", err)
	}
	if s.budget.used.Load() != 0 || s.streams.used.Load() != 0 {
		t.Fatal("failed deadline retained resource budget")
	}
}

func TestServeConnNilContextClosesConnection(t *testing.T) {
	s, err := NewServer(http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	conn := &faultConn{}
	if err := s.ServeConn(nil, conn); err == nil || !conn.closed.Load() {
		t.Fatal("invalid context retained connection", err)
	}
	if len(s.connections) != 0 {
		t.Fatal("invalid context admitted connection")
	}
}

func TestServeConnLimitPreservesExistingAdmission(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxConnections = 1
	s, err := NewServerWithLimits(http.NotFoundHandler(), limits)
	if err != nil {
		t.Fatal(err)
	}
	first := &faultConn{}
	ctx, cancel, err := s.admitConnection(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = first.Close(); s.workers.Done() }()
	second := &faultConn{}
	if err := s.ServeConn(t.Context(), second); !errors.Is(err, errConnectionLimit) {
		t.Fatal("connection limit was not enforced", err)
	}
	if !second.closed.Load() || first.closed.Load() || ctx.Err() != nil {
		t.Fatal("rejection disturbed existing connection")
	}
	if len(s.connections) != 1 || s.connections[first] == nil {
		t.Fatal("rejection changed admitted connections")
	}
}
