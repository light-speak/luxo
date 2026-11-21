package api

import (
	"context"
	"os"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
	"nhooyr.io/websocket"
)

func BenchmarkWSUnaryResponseCompletion(b *testing.B) {
	for _, mode := range []websocket.MessageType{websocket.MessageText, websocket.MessageBinary} {
		for _, name := range []string{"getUser", "failAPI"} {
			b.Run(mode.String()+"/"+name, func(b *testing.B) { benchmarkWSUnaryResponseCompletion(b, mode, name) })
		}
	}
}

func benchmarkWSUnaryResponseCompletion(b *testing.B, mode websocket.MessageType, name string) {
	router := testWSRouter()
	ws, peer := benchmarkWebSocketPair(b)
	frame := sequenceRequest(mode, name, 1)
	call := func() { router.handleWSBinary(ws.ctx, ws, frame) }
	if mode == websocket.MessageText {
		call = func() {
			raw, err := jsonutil.ParseObject(frame)
			if err != nil {
				b.Fatal(err)
			}
			router.handleWSJSON(ws.ctx, ws, raw)
		}
	}
	expected := make(chan []byte, 1)
	go func() {
		kind, data, err := peer.Read(b.Context())
		if err != nil || kind != mode {
			b.Error("response setup failed", kind, err)
		}
		expected <- data
	}()
	call()
	payload := <-expected
	if len(payload) == 0 {
		b.Fatal("missing response")
	}
	received := make(chan int, 1)
	go drainBenchmarkMessages(b, peer, mode, payload, received)
	b.ReportAllocs()
	count := 0
	for b.Loop() {
		for range 64 {
			call()
			count++
		}
		if got := <-received; got != count {
			b.Fatal("response count changed", got, count)
		}
	}
	b.ReportMetric(64, "messages/op")
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(count), "ns/message")
	if err := ws.write(ws.ctx, mode, nil); err != nil {
		b.Fatal(err)
	}
	<-received
}

func BenchmarkWSUnaryInvocationCompletion(b *testing.B) {
	for _, failed := range []bool{false, true} {
		name := "success"
		if failed {
			name = "business-error"
		}
		b.Run(name, func(b *testing.B) {
			router, req := NewRouter(), &Request{}
			ctx := context.Background()
			call := wsUnaryCall{router: router, ctx: ctx}
			handler := func(_ context.Context, req *Request) error {
				req.Buf.AppendInt(42)
				if failed {
					return errors.NotFound
				}
				return nil
			}
			b.ReportAllocs()
			before := os.Getenv("LUXO_WS_INVOKE_BASELINE") == "1"
			for b.Loop() {
				var buf *ResponseBuf
				var err error
				if before {
					buf = GetBuf()
					req.Buf = buf
					err = router.callHandler(handler, ctx, req)
				} else {
					err = completeBenchmarkWSInvocation(&call, req, handler)
				}
				if buf != nil {
					PutBuf(buf)
				}
				if failed != (err != nil) {
					b.Fatal("invocation result changed", err)
				}
			}
		})
	}
}

func completeBenchmarkWSInvocation(call *wsUnaryCall, req *Request, handler HandlerFunc) error {
	call.returned = false
	defer call.finish()
	_, err := call.invoke(req, handler)
	call.returned = true
	return err
}
