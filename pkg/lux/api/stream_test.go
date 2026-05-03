package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"nhooyr.io/websocket"
)

func TestStreamHub_SubscribeUnsubscribe(t *testing.T) {
	hub := NewStreamHub()

	sub := hub.Subscribe("watchDanmaku", map[string]any{"roomId": int64(1)}, nil, nil, func() {})
	if hub.SubCount("watchDanmaku") != 1 {
		t.Errorf("sub count = %d, want 1", hub.SubCount("watchDanmaku"))
	}

	hub.Unsubscribe("watchDanmaku", sub)
	if hub.SubCount("watchDanmaku") != 0 {
		t.Errorf("sub count = %d, want 0", hub.SubCount("watchDanmaku"))
	}
}

func TestStreamHub_Broadcast(t *testing.T) {
	hub := NewStreamHub()

	sub1 := hub.Subscribe("announce", nil, nil, nil, func() {})
	sub2 := hub.Subscribe("announce", nil, nil, nil, func() {})

	// Broadcast — no matcher
	hub.DispatchEncoded("announce", []byte("hello"), nil)

	select {
	case data := <-sub1.Ch:
		if string(data) != "hello" {
			t.Errorf("sub1 got %q", data)
		}
	case <-time.After(time.Second):
		t.Error("sub1 timeout")
	}

	select {
	case data := <-sub2.Ch:
		if string(data) != "hello" {
			t.Errorf("sub2 got %q", data)
		}
	case <-time.After(time.Second):
		t.Error("sub2 timeout")
	}

	hub.Unsubscribe("announce", sub1)
	hub.Unsubscribe("announce", sub2)
}

func TestStreamHub_MatcherFilter(t *testing.T) {
	hub := NewStreamHub()

	sub1 := hub.Subscribe("watchDanmaku", map[string]any{"roomId": int64(1)}, nil, nil, func() {})
	sub2 := hub.Subscribe("watchDanmaku", map[string]any{"roomId": int64(2)}, nil, nil, func() {})

	// Matcher: only push to roomId == 1
	matcher := func(data []byte, params *StreamParams, identity any) bool {
		return params.Int("roomId") == 1
	}

	hub.DispatchEncoded("watchDanmaku", []byte("danmaku"), matcher)

	// sub1 should receive (roomId=1 matches)
	select {
	case data := <-sub1.Ch:
		if string(data) != "danmaku" {
			t.Errorf("sub1 got %q", data)
		}
	case <-time.After(time.Second):
		t.Error("sub1 should receive")
	}

	// sub2 should NOT receive (roomId=2 doesn't match)
	select {
	case <-sub2.Ch:
		t.Error("sub2 should NOT receive")
	case <-time.After(50 * time.Millisecond):
		// expected
	}

	hub.Unsubscribe("watchDanmaku", sub1)
	hub.Unsubscribe("watchDanmaku", sub2)
}

func TestStreamHub_DispatchWithEncode(t *testing.T) {
	hub := NewStreamHub()
	sub := hub.Subscribe("test", nil, nil, nil, func() {})

	hub.Dispatch("test", "rawData", nil, func(data any, fieldMask []byte) []byte {
		return []byte(data.(string) + ":encoded")
	})

	select {
	case data := <-sub.Ch:
		if string(data) != "rawData:encoded" {
			t.Errorf("got %q", data)
		}
	case <-time.After(time.Second):
		t.Error("timeout")
	}

	hub.Unsubscribe("test", sub)
}

func TestStreamHub_ChanFull(t *testing.T) {
	hub := NewStreamHub()
	cancelled := make(chan struct{}, 1)
	sub := hub.Subscribe("test", nil, nil, nil, func() {
		select {
		case cancelled <- struct{}{}:
		default:
		}
	})

	// Fill the channel
	for i := 0; i < 64; i++ {
		hub.DispatchEncoded("test", []byte("x"), nil)
	}

	// Next dispatch should not block — and should cancel the slow subscriber
	done := make(chan bool, 1)
	go func() {
		hub.DispatchEncoded("test", []byte("overflow"), nil)
		done <- true
	}()

	select {
	case <-done:
		// good — didn't block
	case <-time.After(time.Second):
		t.Error("dispatch should not block on full chan")
	}

	// Verify subscriber was cancelled (disconnected)
	select {
	case <-cancelled:
		// good — slow subscriber disconnected
	case <-time.After(100 * time.Millisecond):
		t.Error("slow subscriber should have been cancelled")
	}

	hub.Unsubscribe("test", sub)
}

func TestStreamParams(t *testing.T) {
	p := &StreamParams{values: map[string]any{
		"roomId": int64(42),
		"name":   "test",
		"rate":   float64(3.14),
	}}

	if p.Int("roomId") != 42 {
		t.Errorf("Int(roomId) = %d", p.Int("roomId"))
	}
	if p.String("name") != "test" {
		t.Errorf("String(name) = %q", p.String("name"))
	}
	if p.Int("missing") != 0 {
		t.Error("missing should be 0")
	}

	// nil params
	var nilP *StreamParams
	if nilP.Int("x") != 0 || nilP.String("x") != "" || nilP.Get("x") != nil {
		t.Error("nil params should return zero values")
	}
}

func TestStreamHub_NoSubscribers(t *testing.T) {
	hub := NewStreamHub()
	// Should not panic
	hub.DispatchEncoded("nonexistent", []byte("data"), nil)
	hub.Dispatch("nonexistent", "data", nil, func(data any, mask []byte) []byte { return nil })
}

func TestStreamHub_UnsubscribeNonExistent(t *testing.T) {
	hub := NewStreamHub()
	sub := &StreamSub{Ch: make(chan []byte, 1)}
	// Should not panic
	hub.Unsubscribe("nonexistent", sub)
}

// --- WebSocket stream integration tests ---

func TestWSJSON_Subscribe(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Subscribe
	conn.Write(ctx, websocket.MessageText, []byte(`{"$sub":"watchTest","roomId":1}`))

	// Wait for subscription to be registered
	time.Sleep(50 * time.Millisecond)

	if rt.Streams.SubCount("watchTest") != 1 {
		t.Errorf("sub count = %d, want 1", rt.Streams.SubCount("watchTest"))
	}

	// Push data through StreamHub
	rt.Streams.DispatchEncoded("watchTest", []byte(`{"content":"hello"}`), nil)

	// Should receive stream push
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	got := string(data)
	if !strings.Contains(got, `"$stream"`) {
		t.Errorf("should contain $stream: %s", got)
	}
	if !strings.Contains(got, "hello") {
		t.Errorf("should contain pushed data: %s", got)
	}
}

func TestWSJSON_Unsubscribe(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Subscribe then unsubscribe
	conn.Write(ctx, websocket.MessageText, []byte(`{"$sub":"watchTest"}`))
	time.Sleep(50 * time.Millisecond)

	if rt.Streams.SubCount("watchTest") != 1 {
		t.Fatalf("should have 1 sub")
	}

	conn.Write(ctx, websocket.MessageText, []byte(`{"$unsub":"watchTest"}`))
	time.Sleep(50 * time.Millisecond)

	if rt.Streams.SubCount("watchTest") != 0 {
		t.Errorf("should have 0 subs after unsub, got %d", rt.Streams.SubCount("watchTest"))
	}
}

func TestWSBinary_Subscribe(t *testing.T) {
	rt := testWSRouter()
	rt.Registry.Register("watchBinary", 50)
	rt.Registry.RegisterParams("watchBinary", []ParamMeta{
		{Name: "roomId", Type: "Int", FieldID: 1},
	})

	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Binary subscribe: [0xFE][API ID=50][mask=0][param field1=roomId=123][0x00]
	var reqBuf []byte
	reqBuf = append(reqBuf, 0xFE)
	reqBuf = codec.AppendVarint(reqBuf, 50) // API ID
	reqBuf = codec.AppendVarint(reqBuf, 0)  // mask
	var enc codec.Encoder
	enc.WriteFieldInt(1, 123) // roomId=123
	enc.WriteEnd()
	reqBuf = append(reqBuf, enc.Bytes()...)

	conn.Write(ctx, websocket.MessageBinary, reqBuf)
	time.Sleep(50 * time.Millisecond)

	if rt.Streams.SubCount("watchBinary") != 1 {
		t.Errorf("sub count = %d, want 1", rt.Streams.SubCount("watchBinary"))
	}

	// Push data
	rt.Streams.DispatchEncoded("watchBinary", []byte{0x01, 0x02}, nil)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Should be: [0xFD][API ID=50][payload]
	if data[0] != 0xFD {
		t.Errorf("first byte should be 0xFD (stream push), got %x", data[0])
	}
}

func TestWSBinary_Unsubscribe(t *testing.T) {
	rt := testWSRouter()
	rt.Registry.Register("watchBin2", 51)

	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Subscribe
	var subBuf []byte
	subBuf = append(subBuf, 0xFE)
	subBuf = codec.AppendVarint(subBuf, 51)
	subBuf = codec.AppendVarint(subBuf, 0)
	subBuf = append(subBuf, 0x00)
	conn.Write(ctx, websocket.MessageBinary, subBuf)
	time.Sleep(50 * time.Millisecond)

	// Unsubscribe: [0xFF][API ID=51]
	var unsubBuf []byte
	unsubBuf = append(unsubBuf, 0xFF)
	unsubBuf = codec.AppendVarint(unsubBuf, 51)
	conn.Write(ctx, websocket.MessageBinary, unsubBuf)
	time.Sleep(50 * time.Millisecond)

	if rt.Streams.SubCount("watchBin2") != 0 {
		t.Errorf("should have 0 subs, got %d", rt.Streams.SubCount("watchBin2"))
	}
}

func TestWSJSON_DisconnectCleansUpSubs(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Subscribe
	conn.Write(ctx, websocket.MessageText, []byte(`{"$sub":"watchCleanup"}`))
	time.Sleep(50 * time.Millisecond)

	if rt.Streams.SubCount("watchCleanup") != 1 {
		t.Fatalf("should have 1 sub")
	}

	// Close connection
	conn.Close(websocket.StatusNormalClosure, "bye")
	time.Sleep(100 * time.Millisecond)

	// Subs should be cleaned up
	if rt.Streams.SubCount("watchCleanup") != 0 {
		t.Errorf("should have 0 subs after disconnect, got %d", rt.Streams.SubCount("watchCleanup"))
	}
}

func TestWSBinary_UnknownFieldID(t *testing.T) {
	rt := testWSRouter()
	rt.Registry.Register("watchUnknown", 60)
	rt.Registry.RegisterParams("watchUnknown", []ParamMeta{
		{Name: "roomId", Type: "Int", FieldID: 1},
	})

	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Binary subscribe with unknown field ID 99 — should stop parsing but still subscribe
	var reqBuf []byte
	reqBuf = append(reqBuf, 0xFE)
	reqBuf = codec.AppendVarint(reqBuf, 60) // API ID
	reqBuf = codec.AppendVarint(reqBuf, 0)  // no mask
	var enc codec.Encoder
	enc.WriteFieldInt(1, 42)  // known: roomId=42
	enc.WriteFieldInt(99, 77) // unknown field ID 99 — should trigger break
	enc.WriteEnd()
	reqBuf = append(reqBuf, enc.Bytes()...)

	conn.Write(ctx, websocket.MessageBinary, reqBuf)
	time.Sleep(50 * time.Millisecond)

	// Should still subscribe (unknown field just stops param parsing)
	if rt.Streams.SubCount("watchUnknown") != 1 {
		t.Errorf("sub count = %d, want 1", rt.Streams.SubCount("watchUnknown"))
	}
}

func TestWSJSON_StreamNativeHandler(t *testing.T) {
	rt := testWSRouter()

	// Register a native stream handler that pushes 3 messages then stops
	handlerCalled := make(chan struct{}, 1)
	rt.HandleStreamNative("watchNative", func(ctx context.Context, params *StreamParams, identity any, stream *Stream) {
		handlerCalled <- struct{}{}
		for i := 0; i < 3; i++ {
			select {
			case <-ctx.Done():
				return
			default:
				stream.Send([]byte(`{"score":` + fmt.Sprintf("%d", i) + `}`))
				time.Sleep(10 * time.Millisecond)
			}
		}
	})

	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Subscribe
	subMsg := `{"$sub":"watchNative","matchId":1}`
	conn.Write(ctx, websocket.MessageText, []byte(subMsg))

	// Wait for handler to be called
	select {
	case <-handlerCalled:
	case <-time.After(1 * time.Second):
		t.Fatal("handler was not called on subscribe")
	}

	// Should receive pushed messages
	for i := 0; i < 3; i++ {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read[%d]: %v", i, err)
		}
		if !strings.Contains(string(data), `"score"`) {
			t.Errorf("expected score in message, got %s", data)
		}
	}
}
