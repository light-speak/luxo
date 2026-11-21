package rpc

import (
	"bytes"
	"context"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func testConnectionReader(t *testing.T, router *api.Router) (*connectionReader, net.Conn) {
	t.Helper()
	socket, peer := net.Pipe()
	server := NewServer(router)
	reader := newConnectionReader(server, socket)
	finished := make(chan struct{})
	go func() { reader.serve(); close(finished) }()
	t.Cleanup(func() { reader.close(); peer.Close(); <-finished; server.Close() })
	if err := peer.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return reader, peer
}

func TestConnectionReaderKeepsInFlightPayloadOwned(t *testing.T) {
	router := api.NewRouter()
	if err := router.Registry.Register("echo", 1); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterParams("echo", []api.ParamMeta{{Name: "value", Type: "Int", FieldID: 1}}); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	count := 0
	router.Handle("echo", func(ctx context.Context, req *api.Request) error {
		count++
		before := bytes.Clone(req.BinaryRequest())
		if count == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if !bytes.Equal(before, req.BinaryRequest()) {
			t.Error("reader overwrote owned payload")
		}
		req.Buf.B = append(req.Buf.B, req.BinaryRequest()...)
		return nil
	})
	_, peer := testConnectionReader(t, router)
	bodies := make([][]byte, 2)
	for i := range 2 {
		bodies[i] = encodeCallRequest(1, nil, mustEncodeParams(t, ParamField{FieldID: 1, Value: int64(i + 1)}))
	}
	if err := WriteFrame(peer, encodeRequestEnvelope(requestKindCall, "", bodies[0])); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start")
	}
	if err := WriteFrame(peer, encodeRequestEnvelope(requestKindCall, "", bodies[1])); err != nil {
		t.Fatal(err)
	}
	close(release)
	for _, body := range bodies {
		got, err := ReadFrame(peer, nil)
		if err != nil || !bytes.Equal(got, append([]byte{statusOK}, body...)) {
			t.Fatal("ordered response", got, err)
		}
	}
}

func TestConnectionReaderOverflowCancelsAndCloses(t *testing.T) {
	router := api.NewRouter()
	if err := router.Registry.Register("wait", 1); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	var calls atomic.Int32
	router.Handle("wait", func(ctx context.Context, _ *api.Request) error {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	reader, peer := testConnectionReader(t, router)
	payload := encodeRequestEnvelope(requestKindCall, "", encodeCallRequest(1, nil, nil))
	if err := WriteFrame(peer, payload); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start")
	}
	for range 2 {
		if err := WriteFrame(peer, payload); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-reader.ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("overflow did not cancel")
	}
	if _, err := io.ReadAll(peer); err != nil {
		t.Fatal("overflow did not close", err)
	}
	if calls.Load() != 1 {
		t.Fatal("queued business executed on failed connection")
	}
}

func TestConnectionReaderStreamRejectsFurtherClientBytes(t *testing.T) {
	router := api.NewRouter()
	if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "watch", ReturnType: "Int", Stream: true}); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	router.HandleStreamPrepared("watch", func(context.Context, *api.Request, any) (api.PreparedStreamHandler, error) {
		return func(ctx context.Context, _ *api.Stream) error { <-ctx.Done(); return ctx.Err() }, nil
	})
	reader, peer := testConnectionReader(t, router)
	payload := encodeRequestEnvelope(requestKindStream, "", encodeCallRequest(1, nil, nil))
	if err := WriteFrame(peer, payload); err != nil {
		t.Fatal(err)
	}
	ack, err := ReadFrame(peer, nil)
	if err != nil || !bytes.Equal(ack, []byte{statusOK}) {
		t.Fatal("stream ACK", ack, err)
	}
	if _, err := peer.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reader.ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("unexpected byte did not cancel stream")
	}
}

func TestConnectionReaderPartialNextFrameStillCancelsActiveHandler(t *testing.T) {
	router := api.NewRouter()
	if err := router.Registry.Register("wait", 1); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	stopped := make(chan error, 1)
	router.Handle("wait", func(ctx context.Context, _ *api.Request) error {
		close(started)
		<-ctx.Done()
		stopped <- ctx.Err()
		return ctx.Err()
	})
	_, peer := testConnectionReader(t, router)
	if err := WriteFrame(peer, encodeRequestEnvelope(requestKindCall, "", encodeCallRequest(1, nil, nil))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start")
	}
	if _, err := peer.Write([]byte{0, 0}); err != nil {
		t.Fatal(err)
	}
	peer.Close()
	select {
	case err := <-stopped:
		if err != context.Canceled {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("partial next frame hid disconnect")
	}
}

// Close unblocks the read, but the read still owns cleanup until released.
type drainingReaderConn struct {
	net.Conn
	input       *bytes.Reader
	readStarted chan struct{}
	closed      chan struct{}
	release     chan struct{}
	closeOnce   sync.Once
}

func (c *drainingReaderConn) Read(dst []byte) (int, error) {
	if c.input.Len() != 0 {
		return c.input.Read(dst)
	}
	close(c.readStarted)
	<-c.closed
	<-c.release
	return 0, io.EOF
}

func (*drainingReaderConn) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func (c *drainingReaderConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func TestConnectionReaderGoexitWaitsForReadCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for range 32 {
			primary, returnedEarly := exerciseConnectionReaderGoexit(t)
			if primary {
				if returnedEarly {
					t.Fatal("connection owner finished before its reading worker released resources")
				}
				return
			}
		}
		t.Fatal("did not exercise the connection owner's handler role")
	})
}

func exerciseConnectionReaderGoexit(t *testing.T) (bool, bool) {
	t.Helper()
	router := api.NewRouter()
	if err := router.Registry.Register("exit", 1); err != nil {
		t.Fatal(err)
	}
	var frame bytes.Buffer
	if err := WriteFrame(&frame, encodeRequestEnvelope(requestKindCall, "", encodeCallRequest(1, nil, nil))); err != nil {
		t.Fatal(err)
	}
	conn := &drainingReaderConn{input: bytes.NewReader(frame.Bytes()), readStarted: make(chan struct{}),
		closed: make(chan struct{}), release: make(chan struct{})}
	background := make(chan bool, 1)
	router.Handle("exit", func(context.Context, *api.Request) error {
		background <- connectionReaderBackgroundStack()
		<-conn.readStarted
		runtime.Goexit()
		return nil
	})
	server := NewServer(router)
	defer server.Close()
	reader := newConnectionReader(server, conn)
	finished := make(chan struct{})
	go func() { defer close(finished); reader.serve() }()
	isBackground := <-background
	<-conn.readStarted
	synctest.Wait()
	returnedEarly := false
	select {
	case <-finished:
		returnedEarly = true
	default:
	}
	close(conn.release)
	<-finished
	reader.workers.Wait()
	return !isBackground, returnedEarly
}

func connectionReaderBackgroundStack() bool {
	var callers [32]uintptr
	frames := runtime.CallersFrames(callers[:runtime.Callers(2, callers[:])])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, "(*connectionReader).background") {
			return true
		}
		if !more {
			return false
		}
	}
}
