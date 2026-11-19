package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"unsafe"

	"nhooyr.io/websocket"
)

// Freeze the previous write path. It is not a correct cancellation-isolation
// implementation; both sides use the same WebSocket library and memory network.
func formerWSWrite(ws *wsConn, ctx context.Context, kind websocket.MessageType, data []byte) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return ws.conn.Write(ctx, kind, data)
}

// Immediate pre-budget baseline, including subscription cancellation isolation.
func unboundedWSWrite(ws *wsConn, ctx context.Context, kind websocket.MessageType, data []byte) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return ws.conn.Write(ws.ctx, kind, data)
	}
}

func BenchmarkWSWriteBudgets(b *testing.B) {
	for _, kind := range []websocket.MessageType{websocket.MessageText, websocket.MessageBinary} {
		for _, size := range []int{8, 1024} {
			for _, impl := range []string{"unbounded", "isolated"} {
				b.Run(fmt.Sprintf("kind=%d/bytes=%d/%s", kind, size, impl), func(b *testing.B) {
					benchmarkWSWrite(b, kind, false, size, impl)
				})
			}
		}
	}
}

func BenchmarkWSWriteCancellationIsolation(b *testing.B) {
	for _, kind := range []websocket.MessageType{websocket.MessageText, websocket.MessageBinary} {
		for _, stream := range []bool{false, true} {
			for _, size := range []int{8, 1024} {
				for _, impl := range []string{"former", "isolated"} {
					b.Run(fmt.Sprintf("kind=%d/stream=%t/bytes=%d/%s", kind, stream, size, impl), func(b *testing.B) {
						benchmarkWSWrite(b, kind, stream, size, impl)
					})
				}
			}
		}
	}
}

func BenchmarkWSAuthenticationLifetime(b *testing.B) {
	for _, kind := range []websocket.MessageType{websocket.MessageText, websocket.MessageBinary} {
		for _, size := range []int{8, 1024} {
			for _, authenticated := range []bool{false, true} {
				b.Run(fmt.Sprintf("kind=%d/bytes=%d/auth=%v", kind, size, authenticated), func(b *testing.B) {
					impl := "isolated"
					if authenticated {
						impl = "authenticated"
					}
					benchmarkWSWrite(b, kind, true, size, impl)
				})
			}
		}
	}
}

var authenticationConnectionSink *wsConn

func BenchmarkWSAuthenticationStateAllocation(b *testing.B) {
	for _, authenticated := range []bool{false, true} {
		b.Run(fmt.Sprintf("auth=%v", authenticated), func(b *testing.B) {
			ctx, cancel := context.WithCancel(b.Context())
			defer cancel()
			b.ReportAllocs()
			for b.Loop() {
				ws := &wsConn{ctx: ctx}
				if authenticated {
					ws.operationCtx = ctx
				}
				authenticationConnectionSink = ws
			}
			b.ReportMetric(float64(unsafe.Sizeof(wsConn{})), "state-bytes")
		})
	}
}

func BenchmarkWSAuthenticationCanceledWrite(b *testing.B) {
	ctx, cancel := context.WithCancel(b.Context())
	cancel()
	ws := &wsConn{ctx: b.Context()}
	b.ReportAllocs()
	for b.Loop() {
		if err := ws.writeBinary(ctx, nil); err != context.Canceled {
			b.Fatal(err)
		}
	}
}

func BenchmarkWSAuthenticationExpiredParent(b *testing.B) {
	for _, kind := range []websocket.MessageType{websocket.MessageText, websocket.MessageBinary} {
		b.Run(kind.String(), func(b *testing.B) { benchmarkWSExpiredParent(b, kind) })
	}
}

func benchmarkWSExpiredParent(b *testing.B, kind websocket.MessageType) {
	ws, peer := benchmarkWebSocketPair(b)
	operation, cancel := context.WithCancel(ws.ctx)
	ws.operationCtx = operation
	writer, stopWriter := context.WithCancel(context.WithoutCancel(operation))
	defer stopWriter()
	cancel()
	payload := []byte("12345678")
	received := make(chan int, 1)
	go drainBenchmarkMessages(b, peer, kind, payload, received)
	b.ReportAllocs()
	written, attempts := 0, 0
	for b.Loop() {
		batch := 0
		for range 64 {
			if err := ws.write(writer, kind, payload); err == nil {
				written++
				batch++
			} else if err != context.Canceled {
				b.Fatal(err)
			}
			attempts++
		}
		if batch > 0 {
			if got := <-received; got != written {
				b.Fatalf("received %d messages, wrote %d", got, written)
			}
		}
	}
	b.ReportMetric(64, "attempts/op")
	b.ReportMetric(float64(written)/float64(attempts)*64, "writes/op")
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(attempts), "ns/attempt")
	// The empty sentinel belongs to benchmark transport cleanup, not business I/O.
	if err := ws.conn.Write(ws.ctx, kind, nil); err != nil {
		b.Fatal(err)
	}
	if got := <-received; got != written {
		b.Fatalf("received %d messages, wrote %d", got, written)
	}
}

func benchmarkWSWrite(b *testing.B, kind websocket.MessageType, stream bool, size int, impl string) {
	ws, peer := benchmarkWebSocketPair(b)
	var total atomic.Int64
	ws.totalWriteBytes = &total
	ws.limits = normalizeWSLimits(WebSocketLimits{})
	ctx := ws.ctx
	if impl == "authenticated" {
		operation, cancel := context.WithCancel(ctx)
		defer cancel()
		ws.operationCtx = operation
		ctx = operation
	}
	if stream {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
	}
	// Real write pumps already select on Done before reaching the write method.
	_ = ctx.Done()
	payload := bytes.Repeat([]byte("x"), size)
	received := make(chan int, 1)
	go drainBenchmarkMessages(b, peer, kind, payload, received)
	b.ReportAllocs()
	count := 0
	for b.Loop() {
		for range 64 {
			var err error
			if impl == "isolated" || impl == "authenticated" {
				err = ws.write(ctx, kind, payload)
			} else if impl == "unbounded" {
				err = unboundedWSWrite(ws, ctx, kind, payload)
			} else {
				err = formerWSWrite(ws, ctx, kind, payload)
			}
			if err != nil {
				b.Fatal(err)
			}
			count++
		}
		// Drain every batch before ending its measurement, including allocations.
		if got := <-received; got != count {
			b.Fatalf("received %d messages, wrote %d", got, count)
		}
	}
	b.ReportMetric(64, "messages/op")
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(count), "ns/message")
	if err := ws.write(ws.ctx, kind, nil); err != nil {
		b.Fatal(err)
	}
	if got := <-received; got != count {
		b.Fatalf("received %d messages, wrote %d", got, count)
	}
}

func benchmarkWebSocketPair(b *testing.B) (*wsConn, *websocket.Conn) {
	b.Helper()
	connected := make(chan *wsConn, 1)
	done := make(chan struct{})
	server := httptest.NewTestServer(b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			b.Error(err)
			connected <- nil
			return
		}
		defer conn.CloseNow()
		connected <- &wsConn{conn: conn, ctx: r.Context()}
		<-done
	}))
	b.Cleanup(func() { close(done) })
	peer, _, err := websocket.Dial(b.Context(), "ws://example.com", &websocket.DialOptions{HTTPClient: server.Client()})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { peer.CloseNow() })
	ws := <-connected
	if ws == nil {
		b.Fatal("WebSocket accept failed")
	}
	b.Cleanup(ws.writeDeadline.close)
	return ws, peer
}

func drainBenchmarkMessages(b *testing.B, peer *websocket.Conn, kind websocket.MessageType, payload []byte, received chan<- int) {
	count := 0
	defer func() { received <- count }()
	for {
		actualKind, data, err := peer.Read(b.Context())
		if err != nil || actualKind != kind {
			b.Errorf("read: kind=%v err=%v", actualKind, err)
			return
		}
		if len(data) == 0 {
			return
		}
		if !bytes.Equal(data, payload) {
			b.Error("WebSocket payload changed")
			return
		}
		count++
		if count%64 == 0 {
			received <- count
		}
	}
}
