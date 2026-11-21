package h2transport

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

func TestShutdownDeadlineForcesConnectionCancellation(t *testing.T) {
	server, peer := net.Pipe()
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, _ := NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	c := newCandidateConnection(ctx, server, s.handler)
	s.connections[server] = &serverConnection{cancel: cancel, active: c}
	s.workers.Add(1)
	defer s.workers.Done()
	deadline, stop := context.WithCancel(context.Background())
	stop()
	if err := s.Shutdown(deadline); !errors.Is(err, context.Canceled) || ctx.Err() == nil {
		t.Fatal("shutdown failed to cancel blocked connection", err)
	}
	if _, err := peer.Write([]byte("x")); err == nil {
		t.Fatal("socket remained open")
	}
}

func TestDrainCommandAndRepeatedShutdown(t *testing.T) {
	server, peer := net.Pipe()
	defer server.Close()
	defer peer.Close()
	c := newCandidateConnection(context.Background(), server, http.NotFoundHandler())
	c.receiver.lastStream = 7
	c.openingComplete = true
	if err := c.beginDrain(); err != nil {
		t.Fatal(err)
	}
	if err := c.beginDrain(); err != nil {
		t.Fatal(err)
	}
	command, ok, err := c.inbox.poll()
	if err != nil || !ok || command.kind != drainCommand || command.streamID != 7 {
		t.Fatal(command, err)
	}
	if _, ok, err = c.inbox.poll(); ok || err != nil {
		t.Fatal("duplicate drain", err)
	}
	if err := c.receive(receiveHead(t, 9, "", true), nil); err == nil {
		t.Fatal("admitted a new stream while draining")
	}
	s, _ := NewServer(http.NotFoundHandler())
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPeerGracefulGoAwayStartsDrain(t *testing.T) {
	server, peer := net.Pipe()
	defer server.Close()
	defer peer.Close()
	c := newCandidateConnection(context.Background(), server, http.NotFoundHandler())
	if err := c.dispatchFrame(&http2.GoAwayFrame{}); err != nil || !c.draining {
		t.Fatal("peer graceful close must preserve accepted streams", err)
	}
	if err := c.dispatchFrame(&http2.GoAwayFrame{ErrCode: http2.ErrCodeProtocol}); err == nil {
		t.Fatal("peer connection error ignored")
	}
}

func TestShutdownDrainsAcceptedResponse(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	s, _ := NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			t.Error("accepted handler canceled before graceful completion")
			return
		}
		_, _ = w.Write([]byte("completed"))
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- s.Serve(ln) }()
	t.Cleanup(func() { _ = s.Close(); <-served })
	tr := &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, _, address string, _ *tls.Config) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}}
	t.Cleanup(tr.CloseIdleConnections)
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	result := make(chan string, 1)
	go func() {
		res, err := client.Post("http://"+ln.Addr().String(), "application/x-luxo", nil)
		if err != nil {
			result <- err.Error()
			return
		}
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			result <- err.Error()
			return
		}
		result <- string(body)
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("handler not entered")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	drained := make(chan error, 1)
	go func() { drained <- s.Shutdown(ctx) }()
	// Listener termination proves shutdown has started, without a timing sleep.
	select {
	case err := <-served:
		served <- err
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-drained:
		t.Fatal("returned before handler completed", err)
	default:
	}
	close(release)
	if got := <-result; got != "completed" {
		t.Fatal(got)
	}
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
}

func TestShutdownCancelsUndrainableConnections(t *testing.T) {
	for _, mode := range []string{"opening", "closed-mailbox", "full-mailbox"} {
		t.Run(mode, func(t *testing.T) {
			s, err := NewServer(http.NotFoundHandler())
			if err != nil {
				t.Fatal(err)
			}
			conn := &faultConn{}
			ctx, cancel, err := s.admitConnection(t.Context(), conn)
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			if mode != "opening" {
				c := newCandidateConnection(ctx, conn, s.handler)
				c.openingComplete = true
				c.inbox, _ = newControlMailbox(1)
				if mode == "closed-mailbox" {
					c.inbox.close()
				} else if err := c.inbox.put(controlCommand{kind: pingCommand}); err != nil {
					t.Fatal(err)
				}
				s.connections[conn].active = c
			}
			go func() {
				<-ctx.Done()
				s.mu.Lock()
				delete(s.connections, conn)
				s.mu.Unlock()
				s.workers.Done()
			}()
			deadline, stop := context.WithTimeout(t.Context(), time.Second)
			defer stop()
			if err := s.Shutdown(deadline); err != nil {
				t.Fatal("shutdown did not release undrainable connection", err)
			}
			if ctx.Err() == nil || !conn.closed.Load() || len(s.connections) != 0 {
				t.Fatal("shutdown retained connection")
			}
		})
	}
}

func TestCloseBetweenAdmissionAndConnectionStart(t *testing.T) {
	s, err := NewServer(http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	conn := &faultConn{}
	ctx, cancel, err := s.admitConnection(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.runConnection(ctx, cancel, conn); !errors.Is(err, net.ErrClosed) {
		t.Fatal("closed server started connection", err)
	}
	if len(s.connections) != 0 || !conn.closed.Load() || ctx.Err() == nil {
		t.Fatal("closed server retained admitted connection")
	}
	deadline, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if err := s.Shutdown(deadline); err != nil {
		t.Fatal("closed server retained worker", err)
	}
}
