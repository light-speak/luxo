package rpc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
)

func TestHandshakeRoundTrip(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- acceptHandshake(server) }()
	if err := initiateHandshake(t.Context(), client, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestServerHandshakeRejectsBeforeDispatch(t *testing.T) {
	router := api.NewRouter()
	router.Handle("ping", func(context.Context, *api.Request) error {
		t.Error("incompatible connection reached business handler")
		return nil
	})
	router.Registry.Register("ping", 1)
	server := NewServer(router)
	addr := startTestRPCServer(t, server)
	for _, version := range []uint32{0, ProtocolVersion - 1, ProtocolVersion + 1, ^uint32(0)} {
		conn := dialTestRPCConn(t, addr)
		preface := protocolPreface()
		binary.BigEndian.PutUint32(preface[8:], version)
		request := append(preface[:], 0, 0, 0, 5, 1, 0, 0, 1, 0)
		if _, err := conn.Write(request); err != nil {
			t.Fatal(err)
		}
		var ack [12]byte
		if _, err := io.ReadFull(conn, ack[:]); err != nil {
			t.Fatal(err)
		}
		if ack != protocolPreface() {
			t.Fatalf("invalid version rejection preface: %x", ack)
		}
		if _, err := conn.Read(make([]byte, 1)); err == nil {
			t.Fatal("incompatible connection remained open")
		}
	}
}

func TestHandshakeRejectsInvalidPeer(t *testing.T) {
	for _, initiator := range []bool{false, true} {
		for _, shape := range []string{"magic", "version", "truncated", "closed"} {
			role := "server"
			if initiator {
				role = "client"
			}
			t.Run(shape+"/"+role, func(t *testing.T) {
				conn, peer := net.Pipe()
				defer conn.Close()
				done := make(chan struct{})
				go func() { defer close(done); sendInvalidPreface(peer, shape, initiator) }()
				var err error
				if initiator {
					err = initiateHandshake(t.Context(), conn, time.Second)
				} else {
					err = acceptHandshake(conn)
				}
				if err == nil {
					t.Fatal("invalid peer accepted")
				}
				<-done
			})
		}
	}
}

func sendInvalidPreface(peer net.Conn, shape string, initiator bool) {
	defer peer.Close()
	if shape == "closed" {
		return
	}
	if initiator {
		var hello [12]byte
		if _, err := io.ReadFull(peer, hello[:]); err != nil {
			return
		}
	}
	preface := protocolPreface()
	switch shape {
	case "magic":
		preface[0] = 'X'
	case "version":
		preface[11]++
	}
	payload := preface[:]
	if shape == "truncated" {
		payload = payload[:11]
	}
	_, _ = peer.Write(payload)
	if !initiator && shape == "version" {
		_, _ = io.ReadFull(peer, preface[:])
	}
}

func TestHandshakeCancellationAndTimeout(t *testing.T) {
	for _, cancelEarly := range []bool{false, true} {
		conn, peer := net.Pipe()
		ctx, cancel := context.WithCancel(t.Context())
		if cancelEarly {
			cancel()
		}
		done := make(chan error, 1)
		go func() { done <- initiateHandshake(ctx, conn, 20*time.Millisecond) }()
		if err := <-done; err == nil {
			t.Fatal("unresponsive peer accepted")
		}
		cancel()
		conn.Close()
		peer.Close()
	}
	conn, peer := net.Pipe()
	defer conn.Close()
	defer peer.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- initiateHandshake(ctx, conn, time.Second) }()
	var preface [12]byte
	if _, err := io.ReadFull(peer, preface[:]); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestPoolRejectsUnverifiedConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var hello [12]byte
		if _, err := io.ReadFull(conn, hello[:]); err != nil {
			return
		}
		hello[11]++
		_, _ = conn.Write(hello[:])
	}()
	pool := NewPool(listener.Addr().String(), 1)
	defer pool.Close()
	conn, err := pool.GetContext(t.Context())
	if conn != nil {
		conn.Close()
		t.Fatal("unverified connection returned")
	}
	if err == nil || !strings.Contains(err.Error(), "protocol version") {
		t.Fatalf("expected version error: %v", err)
	}
	<-done
}

func verifiedTestRPCConn(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn := dialTestRPCConn(t, addr)
	if err := initiateHandshake(t.Context(), conn, time.Second); err != nil {
		t.Fatal(err)
	}
	return conn
}

func acceptTestHandshake(t *testing.T, conn net.Conn) bool {
	t.Helper()
	if err := acceptHandshake(conn); err != nil {
		t.Error(err)
		return false
	}
	return true
}

type prefaceTestConn struct {
	net.Conn
	reader       io.Reader
	writer       io.Writer
	deadlines    []time.Time
	failDeadline int
}

func (c *prefaceTestConn) Read(p []byte) (int, error)  { return c.reader.Read(p) }
func (c *prefaceTestConn) Write(p []byte) (int, error) { return c.writer.Write(p) }
func (c *prefaceTestConn) SetDeadline(deadline time.Time) error {
	c.deadlines = append(c.deadlines, deadline)
	if c.failDeadline == len(c.deadlines) {
		return net.ErrClosed
	}
	return nil
}

type shortPrefaceWriter struct{}

func (shortPrefaceWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestHandshakeIOFailuresAndDeadlineReset(t *testing.T) {
	preface := protocolPreface()
	for _, initiate := range []bool{false, true} {
		for _, fail := range []int{0, 1, 2} {
			conn := &prefaceTestConn{reader: bytes.NewReader(preface[:]), writer: io.Discard, failDeadline: fail}
			var err error
			if initiate {
				err = initiateHandshake(context.Background(), conn, time.Second)
			} else {
				err = acceptHandshake(conn)
			}
			if (err != nil) != (fail != 0) {
				t.Fatalf("initiate=%v deadline=%d: %v", initiate, fail, err)
			}
			if fail == 0 && (len(conn.deadlines) != 2 || !conn.deadlines[1].IsZero()) {
				t.Fatal("handshake deadline retained")
			}
		}
		conn := &prefaceTestConn{reader: bytes.NewReader(preface[:]), writer: shortPrefaceWriter{}}
		var err error
		if initiate {
			err = initiateHandshake(context.Background(), conn, time.Second)
		} else {
			err = acceptHandshake(conn)
		}
		if !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("short preface write: %v", err)
		}
	}
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	conn := &prefaceTestConn{reader: bytes.NewReader(preface[:]), writer: io.Discard}
	if err := initiateHandshake(ctx, conn, 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	if conn.deadlines[0] != deadline {
		t.Fatal("caller deadline was extended")
	}
}

func TestServerHandshakeHasBoundedWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn, peer := net.Pipe()
		defer conn.Close()
		defer peer.Close()
		start := time.Now()
		err := acceptHandshake(conn)
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() {
			t.Fatalf("missing handshake timeout: %v", err)
		}
		if time.Since(start) != handshakeTimeout {
			t.Fatal("unexpected handshake timeout")
		}
	})
}

func TestHandshakeOnlyOnNewConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		for _, count := range []int{2, 1} {
			conn, err := listener.Accept()
			if err != nil {
				done <- err
				return
			}
			err = serveVerifiedRequests(conn, count)
			conn.Close()
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	client := NewClient(listener.Addr().String())
	defer client.Close()
	for range 2 {
		if _, err := client.Call(1, nil); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := client.pool.Get()
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if _, err := client.Call(1, nil); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func serveVerifiedRequests(conn net.Conn, count int) error {
	if err := acceptHandshake(conn); err != nil {
		return err
	}
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	for range count {
		payload, err := ReadFrame(conn, nil)
		if err != nil {
			return err
		}
		if _, err := decodeRequestEnvelope(payload); err != nil {
			return err
		}
		if err := WriteFrame(conn, []byte{statusOK}); err != nil {
			return err
		}
	}
	return nil
}
