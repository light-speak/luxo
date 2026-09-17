package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"nhooyr.io/websocket"
)

// Freeze the previous write path. It is not a correct cancellation-isolation
// implementation; both sides use the same WebSocket library and memory network.
func formerWSWrite(ws *wsConn, ctx context.Context, kind websocket.MessageType, data []byte) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return ws.conn.Write(ctx, kind, data)
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

func benchmarkWSWrite(b *testing.B, kind websocket.MessageType, stream bool, size int, impl string) {
	ws, peer := benchmarkWebSocketPair(b)
	ctx := ws.ctx
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
			if impl == "isolated" {
				err = ws.write(ctx, kind, payload)
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
