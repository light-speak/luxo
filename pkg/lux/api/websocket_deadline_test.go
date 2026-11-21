package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"nhooyr.io/websocket"
)

func TestWSCancelledWriteAfterReservationReleasesBudget(t *testing.T) {
	var total atomic.Int64
	ws := &wsConn{ctx: t.Context(), totalWriteBytes: &total, limits: WebSocketLimits{WriteTimeout: time.Minute}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ws.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- ws.writeBinary(ctx, []byte("payload")) }()
	deadline := time.Now().Add(time.Second)
	for ws.writeBytes.Load() == 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	reserved := ws.writeBytes.Load()
	cancel()
	ws.mu.Unlock()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled writer remained blocked")
	}
	if reserved != 7 || ws.writeBytes.Load() != 0 || total.Load() != 0 {
		t.Fatal("write did not reserve and release its capacity", reserved)
	}
	// A nil socket makes any accidental write or connection close fail the test.
}

func TestWSExpiredQueuedWriteReleasesBudget(t *testing.T) {
	accepted := make(chan *wsConn, 1)
	release := make(chan struct{})
	var total atomic.Int64
	ctx, peer := newInMemoryWebSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		accepted <- &wsConn{conn: conn, ctx: r.Context(), totalWriteBytes: &total, limits: WebSocketLimits{WriteTimeout: 10 * time.Millisecond}}
		<-release
	}))
	defer close(release)
	ws := <-accepted
	ws.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- ws.writeBinary(ctx, []byte("payload")) }()
	// Wait for the queued writer's timer to close the peer before releasing the lock.
	_, _, readErr := peer.Read(ctx)
	ws.mu.Unlock()
	if readErr == nil || ctx.Err() != nil {
		t.Fatal("writer deadline did not close the connection", readErr, ctx.Err())
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("expired writer remained blocked")
	}
	if ws.writeBytes.Load() != 0 || total.Load() != 0 {
		t.Fatal("expired queued write retained buffer capacity")
	}
}

func TestWSWriteDeadlineStaleCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var d wsWriteDeadline
		defer d.close()
		ctx := d.arm(t.Context(), time.Now().Add(time.Second))
		d.disarm()
		d.expire()
		if ctx.Err() != nil {
			t.Fatal("disarmed callback cancelled writer")
		}
		if d.arm(t.Context(), time.Now().Add(2*time.Second)) != ctx {
			t.Fatal("context not reused")
		}
		d.expire()
		time.Sleep(time.Second)
		if ctx.Err() != nil {
			t.Fatal("stale callback cancelled newer write")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if ctx.Err() != context.Canceled {
			t.Fatal("deadline not enforced")
		}
		d.disarm()
		d.expire()
		if d.arm(t.Context(), time.Now().Add(time.Second)).Err() == nil {
			t.Fatal("expired connection revived")
		}
	})
}

func TestWSWriteDeadlineCloseBeforeArm(t *testing.T) {
	var d wsWriteDeadline
	d.close()
	if d.arm(t.Context(), time.Now().Add(time.Second)).Err() == nil {
		t.Fatal("closed writer reopened")
	}
	d.disarm()
	d.close()
}

func TestWSCancelledQueuedWriteDoesNotCloseSocket(t *testing.T) {
	// Use real time: a deliberately held mutex cannot advance synctest's clock.
	ws := &wsConn{ctx: t.Context()}
	ws.mu.Lock()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(started)
		ws.lockWrite(ctx, time.Millisecond)
		ws.mu.Unlock()
		close(done)
	}()
	<-started
	// conn is deliberately nil: touching it would panic and fail this test.
	time.Sleep(10 * time.Millisecond)
	ws.mu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter stuck")
	}
}

func TestWSTokenExpiryCancelsOperationsAndReleasesBudget(t *testing.T) {
	for name, kind := range map[string]websocket.MessageType{"JSON": websocket.MessageText, "Binary": websocket.MessageBinary} {
		for _, stream := range []bool{false, true} {
			operation := "call"
			if stream {
				operation = "stream"
			}
			t.Run(name+"/"+operation, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) { testWSTokenExpiryOperation(t, kind, stream) })
			})
		}
	}
}

func TestWSTokenExpiryRejectsStaleSubscriptionWriter(t *testing.T) {
	for name, kind := range map[string]websocket.MessageType{"JSON": websocket.MessageText, "Binary": websocket.MessageBinary} {
		for _, queued := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/queued=%v", name, queued), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) { testWSStaleExpiryWriter(t, kind, queued) })
			})
		}
	}
}

func testWSStaleExpiryWriter(t *testing.T, kind websocket.MessageType, queued bool) {
	t.Helper()
	router := testWSRouter()
	ctx, peer := newInMemoryWebSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		operation, cancel := context.WithCancelCause(r.Context())
		defer cancel(context.Canceled)
		ws := &wsConn{conn: conn, ctx: r.Context(), operationCtx: operation, totalWriteBytes: &router.wsWriteBytes}
		defer ws.writeDeadline.close()
		// Model the interval after the parent and producer are cancelled, before
		// cancellation has reached the sibling writer. It must not emit data or 500.
		writer, stopWriter := context.WithCancel(context.WithoutCancel(operation))
		defer stopWriter()
		sub := &StreamSub{Ch: make(chan []byte, 1), Binary: kind == websocket.MessageBinary, result: context.Canceled}
		if queued {
			sub.Ch <- []byte(`42`)
		}
		close(sub.Ch)
		cancel(errWebSocketTokenExpired)
		router.writeSubscription(writer, ws, "watchTest", 1, sub, &connectionSubscriptions{})
		if ws.writeBytes.Load() != 0 || router.wsWriteBytes.Load() != 0 {
			t.Error("expired writer retained write capacity")
		}
		conn.Close(WebSocketTokenExpired, "access token expired")
	}))
	gotKind, frame, err := peer.Read(ctx)
	if websocket.CloseStatus(err) != WebSocketTokenExpired {
		t.Fatalf("expired writer leaked a frame before close: kind=%v frame=%q err=%v", gotKind, frame, err)
	}
}

func TestWSExpiryAfterReservationReleasesBudget(t *testing.T) {
	var total atomic.Int64
	operation, cancel := context.WithCancel(t.Context())
	defer cancel()
	ws := &wsConn{ctx: t.Context(), operationCtx: operation, totalWriteBytes: &total, limits: WebSocketLimits{WriteTimeout: time.Minute}}
	ws.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- ws.writeBinary(t.Context(), []byte("payload")) }()
	deadline := time.Now().Add(time.Second)
	for ws.writeBytes.Load() == 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	reserved := ws.writeBytes.Load()
	cancel()
	ws.mu.Unlock()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("expired writer remained queued")
	}
	if reserved != 7 || ws.writeBytes.Load() != 0 || total.Load() != 0 {
		t.Fatal("expired writer retained its reservation", reserved)
	}
	// conn is nil: cancellation must prevent any transport write or close.
}

func testWSTokenExpiryOperation(t *testing.T, kind websocket.MessageType, stream bool) {
	t.Helper()
	rt := testWSRouter()
	started := make(chan context.Context, 1)
	finished := make(chan struct{})
	wait := func(ctx context.Context) error { started <- ctx; <-ctx.Done(); close(finished); return ctx.Err() }
	rt.Handle("getUser", func(ctx context.Context, _ *Request) error { return wait(ctx) })
	rt.HandleStreamNative("watchTest", func(ctx context.Context, _ *StreamParams, _ any, _ *Stream) error { return wait(ctx) })
	if err := rt.Registry.Register("watchTest", 11); err != nil {
		t.Fatal(err)
	}
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt.ServeHTTP(w, r.WithContext(WithWebSocketTokenExpiry(r.Context(), time.Now().Add(2*time.Second))))
	})
	ctx, conn := newInMemoryWebSocket(t, wrapped)
	frame := sequenceRequest(kind, "getUser", 1)
	if stream {
		frame = []byte(`{"$id":1,"$sub":"watchTest"}`)
		if kind == websocket.MessageBinary {
			frame = []byte{BinaryFrameSubscribe, 1, 11, 0, 0}
		}
	}
	writeWSFrame(t, ctx, conn, kind, frame)
	if stream {
		if _, _, err := conn.Read(ctx); err != nil {
			t.Fatal("subscription ACK", err)
		}
	}
	business := <-started
	readDone := make(chan error, 1)
	go func() { _, _, err := conn.Read(ctx); readDone <- err }()
	time.Sleep(2 * time.Second)
	<-finished
	if context.Cause(business) != errWebSocketTokenExpired {
		t.Fatalf("cancellation cause: %v", context.Cause(business))
	}
	if err := <-readDone; websocket.CloseStatus(err) != WebSocketTokenExpired {
		t.Fatalf("expiry close: %v", err)
	}
	synctest.Wait()
	if rt.wsCalls.Load() != 0 || rt.wsWriteBytes.Load() != 0 || rt.receiveBytes.Load() != 0 || rt.wsConnections.Load() != 0 || rt.Streams.SubCount("watchTest") != 0 {
		t.Fatal("expiry retained work or capacity")
	}
}

func TestWSAuthContextWithoutExpiryReusesParent(t *testing.T) {
	parent := t.Context()
	got, release := webSocketAuthContext(parent, nil)
	if got != parent {
		t.Fatal("anonymous connection acquired an auth context")
	}
	release()
	if parent.Err() != nil {
		t.Fatal("release cancelled the parent")
	}
}

func TestWSAlreadyExpiredUpgradeClosesWithoutWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rt := testWSRouter()
		rt.Handle("getUser", func(context.Context, *Request) error { t.Error("expired identity dispatched work"); return nil })
		wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rt.ServeHTTP(w, r.WithContext(WithWebSocketTokenExpiry(r.Context(), time.Now().Add(-time.Second))))
		})
		ctx, conn := newInMemoryWebSocket(t, wrapped)
		if _, _, err := conn.Read(ctx); websocket.CloseStatus(err) != WebSocketTokenExpired {
			t.Fatalf("expired upgrade close: %v", err)
		}
		synctest.Wait()
		if rt.wsConnections.Load() != 0 {
			t.Fatal("expired upgrade retained connection capacity")
		}
	})
}

func TestClearedWebSocketTokenExpiryPreservesContext(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	bound := WithWebSocketTokenExpiry(parent, time.Now().Add(time.Hour))
	cleared := WithoutWebSocketTokenExpiry(bound)
	if _, ok := cleared.Value(webSocketTokenExpiryKey{}).(time.Time); ok {
		t.Fatal("inherited deadline remains")
	}
	ctx, stop := webSocketAuthContext(cleared, nil)
	defer stop()
	if ctx != cleared {
		t.Fatal("cleared expiry created a timer context")
	}
	cancel()
	if ctx.Err() != context.Canceled {
		t.Fatal("parent cancellation lost")
	}
	if _, ok := bound.Value(webSocketTokenExpiryKey{}).(time.Time); !ok {
		t.Fatal("original context changed")
	}
}

// A transport read counter distinguishes leaving the loop from merely dropping
// the expired call deeper in dispatch. Writes block until the peer reads them.
type observedAuthConn struct {
	net.Conn
	reads atomic.Int64
}

func (c *observedAuthConn) Read(dst []byte) (int, error) {
	c.reads.Add(1)
	return c.Conn.Read(dst)
}

type prefetchedAuthUpgrade struct {
	*httptest.ResponseRecorder
	conn   net.Conn
	buffer *bufio.ReadWriter
}

func (w *prefetchedAuthUpgrade) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, w.buffer, nil
}

func TestWSExpiredAfterBufferedHeaderDropsCompletedMessage(t *testing.T) {
	for _, kind := range []websocket.MessageType{websocket.MessageText, websocket.MessageBinary} {
		for _, expiry := range []string{"already", "during", "valid"} {
			t.Run(kind.String()+"/"+expiry, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) { testWSBufferedAuthBoundary(t, kind, expiry) })
			})
		}
	}
}

func testWSBufferedAuthBoundary(t *testing.T, kind websocket.MessageType, expiry string) {
	t.Helper()
	server, peer := net.Pipe()
	defer server.Close()
	defer peer.Close()
	observed := &observedAuthConn{Conn: server}
	payload := []byte(`{"$id":1,"$api":"probe"}`)
	if kind == websocket.MessageBinary {
		payload = []byte{BinaryFrameCallRequest, 1, 10, 0, 0}
	}
	header, masked := bufferedAuthFrame(kind, payload)
	buffer := bufio.NewReadWriter(bufio.NewReader(bytes.NewReader(header)), bufio.NewWriter(observed))
	if _, err := buffer.Peek(len(header)); err != nil {
		t.Fatal(err)
	}
	writer := &prefetchedAuthUpgrade{ResponseRecorder: httptest.NewRecorder(), conn: observed, buffer: buffer}
	router, calls := bufferedAuthRouter(t)
	deadline := time.Now().Add(time.Hour)
	if expiry == "already" {
		deadline = time.Now().Add(-time.Second)
	}
	if expiry == "during" {
		deadline = time.Now().Add(time.Second)
	}
	request := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	request = request.WithContext(WithWebSocketTokenExpiry(request.Context(), deadline))
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	finished := make(chan struct{})
	go func() { defer close(finished); router.ServeHTTP(writer, request) }()
	synctest.Wait()
	if writer.Code != http.StatusSwitchingProtocols || router.receiveBytes.Load() == 0 || observed.reads.Load() != 1 {
		t.Fatal("fixture missed the pending payload", writer.Code, router.receiveBytes.Load(), observed.reads.Load())
	}
	if expiry == "during" {
		time.Sleep(time.Second)
		synctest.Wait()
	}
	if _, err := peer.Write(masked); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
	checkBufferedAuthDispatch(t, router, observed, calls, expiry == "valid")
	checkBufferedAuthResponse(t, peer, kind, expiry)
	synctest.Wait()
	if router.receiveBytes.Load() != 0 {
		t.Fatal("completed response retained input")
	}
	peer.Close()
	synctest.Wait()
	<-finished
	if router.wsConnections.Load() != 0 || router.wsCalls.Load() != 0 || router.wsWriteBytes.Load() != 0 || router.receiveBytes.Load() != 0 {
		t.Fatal("completed connection retained work or capacity")
	}
}

func bufferedAuthRouter(t *testing.T) (*Router, *atomic.Int64) {
	t.Helper()
	router := NewRouter()
	if err := router.Schema.RegisterAPI(&schema.API{ID: 10, Name: "probe", ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	calls := new(atomic.Int64)
	router.Handle("probe", func(_ context.Context, req *Request) error {
		calls.Add(1)
		req.Buf.B = codec.AppendSvarint(req.Buf.B, 7)
		return nil
	})
	return router, calls
}

func bufferedAuthFrame(kind websocket.MessageType, payload []byte) ([]byte, []byte) {
	mask := [4]byte{0x11, 0x22, 0x33, 0x44}
	header := []byte{0x80 | byte(kind), 0x80 | byte(len(payload))}
	header = append(header, mask[:]...)
	masked := bytes.Clone(payload)
	for i := range masked {
		masked[i] ^= mask[i%len(mask)]
	}
	return header, masked
}

func checkBufferedAuthResponse(t *testing.T, peer net.Conn, kind websocket.MessageType, expiry string) {
	t.Helper()
	var header [2]byte
	if _, err := io.ReadFull(peer, header[:]); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, int(header[1]))
	if _, err := io.ReadFull(peer, payload); err != nil {
		t.Fatal(err)
	}
	if expiry != "valid" {
		if header[0] != 0x88 || len(payload) < 2 || binary.BigEndian.Uint16(payload[:2]) != uint16(WebSocketTokenExpired) || string(payload[2:]) != errWebSocketTokenExpired.Error() {
			t.Fatal("missing expiry close frame", header, payload)
		}
		return
	}
	want := []byte(`{"$id":1,"data":7}`)
	if kind == websocket.MessageBinary {
		want = []byte{BinaryFrameCallSuccess, 1, 14}
	}
	if header[0] != 0x80|byte(kind) || !bytes.Equal(payload, want) {
		t.Fatal("valid authentication control changed", header, payload)
	}
}

func checkBufferedAuthDispatch(t *testing.T, router *Router, observed *observedAuthConn, calls *atomic.Int64, valid bool) {
	t.Helper()
	if valid {
		if observed.reads.Load() != 2 || calls.Load() != 1 || router.receiveBytes.Load() == 0 {
			t.Fatal("valid call did not retain input until its pending response completed", observed.reads.Load(), calls.Load(), router.receiveBytes.Load())
		}
		return
	}
	if observed.reads.Load() != 1 || calls.Load() != 0 || router.receiveBytes.Load() != 0 {
		t.Fatal("expired message dispatched/read more work or retained input", observed.reads.Load(), calls.Load(), router.receiveBytes.Load())
	}
}
