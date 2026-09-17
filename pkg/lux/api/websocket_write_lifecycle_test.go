package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"nhooyr.io/websocket"
)

// Pause one real frame after its prefix has reached the peer. Cancellation must
// not leave that frame truncated or terminate an unrelated subscription.
type wsWritePause struct {
	started chan struct{}
	resume  chan struct{}
}

type pausedWSConn struct {
	net.Conn
	pauses chan *wsWritePause
	closed chan struct{}
	once   sync.Once
}

func (c *pausedWSConn) Write(data []byte) (int, error) {
	select {
	case pause := <-c.pauses:
		n, err := c.Conn.Write(data[:len(data)/2])
		close(pause.started)
		if err != nil {
			return n, err
		}
		select {
		case <-pause.resume:
			m, err := c.Conn.Write(data[n:])
			return n + m, err
		case <-c.closed:
			return n, net.ErrClosed
		}
	default:
		return c.Conn.Write(data)
	}
}

func (c *pausedWSConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

type pausedWSResponse struct {
	http.ResponseWriter
	connections chan *pausedWSConn
}

func (w pausedWSResponse) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.ResponseWriter.(http.Hijacker).Hijack()
	if err != nil {
		return nil, nil, err
	}
	if err := rw.Flush(); err != nil {
		conn.Close()
		return nil, nil, err
	}
	paused := &pausedWSConn{Conn: conn, pauses: make(chan *wsWritePause, 1), closed: make(chan struct{})}
	rw.Writer.Reset(paused)
	w.connections <- paused
	return paused, rw, nil
}

func newPausedWebSocket(t *testing.T, router *Router) (context.Context, *websocket.Conn, *pausedWSConn) {
	t.Helper()
	connections := make(chan *pausedWSConn, 1)
	ctx, conn := newInMemoryWebSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		router.ServeHTTP(pausedWSResponse{ResponseWriter: w, connections: connections}, r)
	}))
	return ctx, conn, <-connections
}

func (c *pausedWSConn) pauseNextWrite() *wsWritePause {
	pause := &wsWritePause{started: make(chan struct{}), resume: make(chan struct{})}
	c.pauses <- pause
	return pause
}

func TestWSUnsubscribeDuringWriteKeepsOtherSubscriptions(t *testing.T) {
	for _, binary := range []bool{false, true} {
		for _, phase := range []string{"data", "complete", "error"} {
			t.Run(fmt.Sprintf("binary=%t/%s", binary, phase), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					testUnsubscribeDuringWrite(t, binary, phase)
				})
			})
		}
	}
}

func testUnsubscribeDuringWrite(t *testing.T, binary bool, phase string) {
	t.Helper()
	router := subscriptionIdentityRouter()
	ctx, conn, wire := newPausedWebSocket(t, router)
	subscribeIdentity(t, ctx, conn, binary, 1)
	subscribeIdentity(t, ctx, conn, binary, 2)
	pause := wire.pauseNextWrite()
	sub := firstIdentitySubscription(router)
	if phase == "data" {
		// Pending messages must be discarded after the one already on the wire.
		for range 4 {
			sub.Ch <- identityPayload(binary)
		}
	} else {
		var result error
		if phase == "error" {
			result = luxerrors.Conflict
		}
		router.Streams.finish("watchIdentity", sub, result)
	}
	<-pause.started
	unsubscribeIdentity(t, ctx, conn, binary, 1)
	synctest.Wait()
	if router.Streams.SubCount("watchIdentity") != 1 {
		t.Fatal("cancelling an in-flight frame removed an unrelated subscription")
	}
	close(pause.resume)
	if phase == "data" {
		requireIdentityFrame(t, ctx, conn, binary, 1)
	} else {
		requireFiniteTerminal(t, ctx, conn, binary, 1, phase == "error")
	}
	readIdentityData(t, ctx, conn, router, binary, 2)
	writeWSFrame(t, ctx, conn, websocket.MessageText, sequenceRequest(websocket.MessageText, "getUser", 99))
	_, response, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertSequenceResponse(t, websocket.MessageText, "getUser", 99, response)
}

func firstIdentitySubscription(router *Router) *StreamSub {
	router.Streams.mu.RLock()
	defer router.Streams.mu.RUnlock()
	return router.Streams.subs["watchIdentity"][0]
}

func TestWSCancelledWriteDoesNotTouchSocket(t *testing.T) {
	for _, binary := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		// A cancelled message must return before dereferencing even the socket.
		ws := &wsConn{ctx: t.Context()}
		var err error
		if binary {
			err = ws.writeBinary(ctx, []byte{1})
		} else {
			err = ws.writeText(ctx, []byte("1"))
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled write: %v", err)
		}
	}
}

func TestWSDisconnectDuringWriteReleasesSubscriptions(t *testing.T) {
	for _, binary := range []bool{false, true} {
		t.Run(fmt.Sprint(binary), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				router := subscriptionIdentityRouter()
				ctx, conn, wire := newPausedWebSocket(t, router)
				subscribeIdentity(t, ctx, conn, binary, 1)
				subscribeIdentity(t, ctx, conn, binary, 2)
				pause := wire.pauseNextWrite()
				firstIdentitySubscription(router).Ch <- identityPayload(binary)
				<-pause.started
				conn.CloseNow()
				synctest.Wait()
				select {
				case <-wire.closed:
				default:
					t.Fatal("disconnect retained a blocked socket write")
				}
				if router.Streams.SubCount("watchIdentity") != 0 {
					t.Fatal("disconnect retained subscriptions")
				}
			})
		})
	}
}

func identityPayload(binary bool) []byte {
	if binary {
		return []byte{2}
	}
	return []byte("1")
}

func requireIdentityFrame(t *testing.T, ctx context.Context, conn *websocket.Conn, binary bool, id uint64) {
	t.Helper()
	mode, want := websocket.MessageText, []byte(fmt.Sprintf(`{"$stream":%d,"data":1}`, id))
	if binary {
		mode, want = websocket.MessageBinary, append(codec.AppendVarint([]byte{BinaryFrameStream}, id), 2)
	}
	requireWSFrame(t, ctx, conn, mode, want)
}
