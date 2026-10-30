package api

import (
	"bytes"
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"nhooyr.io/websocket"
)

func TestWSReadDeadlineDoesNotExpireIdleConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rt := testWSRouter()
		rt.wsLimits.ReadTimeout = time.Second
		ctx, conn := newInMemoryWebSocket(t, rt)
		time.Sleep(2 * time.Second)
		writeWSFrame(t, ctx, conn, websocket.MessageText, sequenceRequest(websocket.MessageText, "getUser", 1))
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertSequenceResponse(t, websocket.MessageText, "getUser", 1, data)
	})
}

func TestWSIncompleteMessageExpiresAndReleasesBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rt := testWSRouter()
		rt.wsLimits.ReadTimeout = time.Second
		ctx, conn := newInMemoryWebSocket(t, rt)
		writer, err := conn.Writer(ctx, websocket.MessageBinary)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = writer.Write(bytes.Repeat([]byte{'x'}, 8192)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if rt.receiveBytes.Load() != 0 || rt.wsConnections.Load() != 0 {
			t.Fatal("slow incomplete message retained receive capacity")
		}
		_ = writer.Close()
	})
}

func TestWSUnaryAdmissionRejectsBeforeDispatch(t *testing.T) {
	for _, mode := range []websocket.MessageType{websocket.MessageText, websocket.MessageBinary} {
		t.Run(mode.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rt := testWSRouter()
				rt.wsLimits = normalizeWSLimits(WebSocketLimits{CallsPerConnection: 1, CallsTotal: 1})
				started := make(chan struct{}, 2)
				release := make(chan struct{})
				original := rt.handlers["getUser"].call
				rt.Handle("getUser", func(ctx context.Context, req *Request) error {
					started <- struct{}{}
					select {
					case <-release:
						return original(ctx, req)
					case <-ctx.Done():
						return ctx.Err()
					}
				})
				ctx, conn := newInMemoryWebSocket(t, rt)
				writeWSFrame(t, ctx, conn, mode, sequenceRequest(mode, "getUser", 1))
				<-started
				writeWSFrame(t, ctx, conn, mode, sequenceRequest(mode, "getUser", 2))
				_, data, err := conn.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if mode == websocket.MessageText {
					var raw map[string]json.RawMessage
					if err := json.Unmarshal(data, &raw); err != nil {
						t.Fatal(err)
					}
					if string(raw["code"]) != "503" || string(raw["$id"]) != "2" {
						t.Fatalf("response %s", data)
					}
				} else if id, n := codec.ReadVarint(data, 1); n <= 0 || id != 2 || data[0] != BinaryFrameCallError {
					t.Fatalf("response %x", data)
				} else if wireErr, err := DecodeBinaryError(data[1+n:], 0); err != nil || wireErr.Code != 503 {
					t.Fatalf("binary overload: %+v, %v", wireErr, err)
				}
				select {
				case <-started:
					t.Fatal("overloaded handler executed")
				default:
				}
				close(release)
				_, data, err = conn.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				assertSequenceResponse(t, mode, "getUser", 1, data)
				synctest.Wait()
				if rt.wsCalls.Load() != 0 {
					t.Fatal("permit leaked")
				}
			})
		})
	}
}

func TestWSAdmissionBudgets(t *testing.T) {
	rt := NewRouterWithOptions(RouterOptions{WebSocket: WebSocketLimits{CallsPerConnection: 2, CallsTotal: 2}})
	a, b := &wsConn{}, &wsConn{}
	if !rt.acquireWSCall(a) || !rt.acquireWSCall(a) {
		t.Fatal("initial admission failed")
	}
	if rt.acquireWSCall(a) || rt.acquireWSCall(b) {
		t.Fatal("limit exceeded")
	}
	if b.calls.Load() != 0 {
		t.Fatal("global rejection leaked local permit")
	}
	rt.releaseWSCall(a)
	if !rt.acquireWSCall(b) {
		t.Fatal("released capacity not available")
	}
	rt.releaseWSCall(a)
	rt.releaseWSCall(b)
	if rt.wsCalls.Load() != 0 {
		t.Fatal("global permit leaked")
	}
}

func TestWSInvalidSequenceIsNotAdmitted(t *testing.T) {
	rt := NewRouterWithOptions(RouterOptions{})
	ws := &wsConn{}
	rt.dispatchWSJSON(t.Context(), ws, map[string]json.RawMessage{"$id": []byte(`"1"`)})
	if rt.wsCalls.Load() != 0 || ws.calls.Load() != 0 {
		t.Fatal("invalid sequence admitted")
	}
}

func TestWSWriteBudgets(t *testing.T) {
	var total atomic.Int64
	a, b := &wsConn{totalWriteBytes: &total}, &wsConn{totalWriteBytes: &total}
	limits := WebSocketLimits{WriteBytesPerConnection: 8, WriteBytesTotal: 10}
	if !a.reserveWrite(8, limits) || a.reserveWrite(1, limits) {
		t.Fatal("local write limit")
	}
	if b.reserveWrite(3, limits) || b.writeBytes.Load() != 0 {
		t.Fatal("global rejection leaked bytes")
	}
	if !b.reserveWrite(2, limits) {
		t.Fatal("exact capacity rejected")
	}
	a.releaseWrite(8)
	b.releaseWrite(2)
	if total.Load() != 0 {
		t.Fatal("write bytes leaked")
	}
}

func TestWSLimitsRejectNegativeConfiguration(t *testing.T) {
	for _, limits := range []WebSocketLimits{
		{CallsPerConnection: -1}, {CallsTotal: -1}, {WriteBytesPerConnection: -1},
		{WriteBytesTotal: -1}, {WriteTimeout: -1},
		{ConnectionsTotal: -1}, {ReadTimeout: -1},
	} {
		if err := NewRouterWithOptions(RouterOptions{WebSocket: limits}).Prepare(); err == nil {
			t.Fatal("negative limit accepted")
		}
	}
}

func TestWSDisconnectReleasesCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rt := testWSRouter()
		started := make(chan struct{})
		rt.Handle("getUser", func(ctx context.Context, req *Request) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		})
		ctx, conn := newInMemoryWebSocket(t, rt)
		writeWSFrame(t, ctx, conn, websocket.MessageText, sequenceRequest(websocket.MessageText, "getUser", 1))
		<-started
		if rt.receiveBytes.Load() == 0 {
			t.Fatal("running handler lost receive reservation")
		}
		conn.CloseNow()
		synctest.Wait()
		if rt.wsCalls.Load() != 0 || rt.wsWriteBytes.Load() != 0 || rt.receiveBytes.Load() != 0 || rt.wsConnections.Load() != 0 {
			t.Fatal("disconnect leaked budget")
		}
	})
}

func TestWSOversizedWriteClosesConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rt := testWSRouter()
		rt.wsLimits = normalizeWSLimits(WebSocketLimits{WriteBytesPerConnection: 1})
		ctx, conn := newInMemoryWebSocket(t, rt)
		writeWSFrame(t, ctx, conn, websocket.MessageText, sequenceRequest(websocket.MessageText, "getUser", 1))
		if _, _, err := conn.Read(ctx); err == nil {
			t.Fatal("oversized response accepted")
		}
		synctest.Wait()
		if rt.wsCalls.Load() != 0 || rt.wsWriteBytes.Load() != 0 {
			t.Fatal("overflow leaked budget")
		}
	})
}

func TestWSSlowWriteDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rt := testWSRouter()
		rt.wsLimits = normalizeWSLimits(WebSocketLimits{WriteTimeout: time.Millisecond})
		ctx, conn, wire := newPausedWebSocket(t, rt)
		pause := wire.pauseNextWrite()
		writeWSFrame(t, ctx, conn, websocket.MessageText, sequenceRequest(websocket.MessageText, "getUser", 1))
		<-pause.started
		time.Sleep(2 * time.Millisecond)
		synctest.Wait()
		select {
		case <-wire.closed:
		default:
			t.Fatal("slow write not closed")
		}
		if rt.wsCalls.Load() != 0 {
			t.Fatal("slow writer retained permit")
		}
	})
}

// A contended sync.Mutex is not durably blocked in synctest. Exercise the
// queued-writer deadline with the real clock instead of advancing fake time.
func TestWSQueuedWriteDeadline(t *testing.T) {
	rt := testWSRouter()
	rt.wsLimits = normalizeWSLimits(WebSocketLimits{WriteTimeout: 100 * time.Millisecond})
	ctx, conn, wire := newPausedWebSocket(t, rt)
	pause := wire.pauseNextWrite()
	writeWSFrame(t, ctx, conn, websocket.MessageText, sequenceRequest(websocket.MessageText, "getUser", 1))
	<-pause.started
	writeWSFrame(t, ctx, conn, websocket.MessageText, sequenceRequest(websocket.MessageText, "getUser", 2))
	select {
	case <-wire.closed:
	case <-time.After(time.Second):
		t.Fatal("queued write deadline not enforced")
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for rt.wsCalls.Load() != 0 || rt.wsWriteBytes.Load() != 0 {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("queued write retained budget")
		}
	}
}
