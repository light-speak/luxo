package api

import (
	"github.com/light-speak/luxo/internal/testutil"

	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"nhooyr.io/websocket"
)

func TestTypedStreamDoesNotSendFailedEncoding(t *testing.T) {
	sub := &StreamSub{Ch: make(chan []byte, 1)}
	raw := &Stream{sub: sub, ctx: context.Background()}
	typed := NewTypedStream(raw, func(int, []byte, bool) ([]byte, error) {
		return []byte("partial"), codec.ErrNonFiniteFloat
	})
	if err := typed.Send(1); !errors.Is(err, codec.ErrNonFiniteFloat) || len(sub.Ch) != 0 {
		t.Fatalf("failed encoding was sent: messages=%d, error=%v", len(sub.Ch), err)
	}
}

func TestStreamDispatchDoesNotSendFailedEncoding(t *testing.T) {
	for _, kind := range []string{"raw", "raw-matcher", "raw-mask", "event", "prepared"} {
		t.Run(kind, func(t *testing.T) {
			hub := NewStreamHub()
			var mask []byte
			if kind == "raw-mask" {
				mask = []byte{1, 1}
			}
			sub := hub.Subscribe("watch", nil, nil, mask, nil)
			defer hub.Unsubscribe("watch", sub)
			encode := func([]byte, bool) ([]byte, error) { return []byte("partial"), codec.ErrNonFiniteFloat }
			var err error
			switch kind {
			case "raw", "raw-matcher", "raw-mask":
				var matcher StreamMatcher
				if kind == "raw-matcher" {
					matcher = func([]byte, *StreamParams, any) bool { t.Fatal("matched invalid encoding"); return true }
				}
				err = hub.Dispatch("watch", nil, matcher, func(any, []byte) ([]byte, error) { return encode(nil, true) })
			case "event":
				err = hub.DispatchEvent("watch", nil, nil, encode)
			case "prepared":
				err = hub.DispatchPreparedEvent("watch", nil, encode)
			}
			if !errors.Is(err, codec.ErrNonFiniteFloat) || len(sub.Ch) != 0 {
				t.Fatalf("failed encoding was sent: messages=%d, error=%v", len(sub.Ch), err)
			}
			if err := hub.DispatchPreparedEvent("watch", nil, func([]byte, bool) ([]byte, error) { return []byte("good"), nil }); err != nil {
				t.Fatal(err)
			}
			if string(<-sub.Ch) != "good" {
				t.Fatal("stream did not recover")
			}
		})
	}
}

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

func TestStreamEncodingFailureDoesNotCachePartialData(t *testing.T) {
	var encodings streamEncodings
	mask := []byte{1}
	data, err := cachedStreamEncoding(&encodings, mask, true, func([]byte, bool) ([]byte, error) {
		return []byte("partial"), codec.ErrNonFiniteFloat
	})
	if !errors.Is(err, codec.ErrNonFiniteFloat) || data != nil || encodings.hasFirst || len(encodings.rest) != 0 {
		t.Fatalf("cached failed encoding: %q, %v, %+v", data, err, encodings)
	}
	for range 2 {
		data, err = cachedStreamEncoding(&encodings, mask, true, func([]byte, bool) ([]byte, error) {
			if encodings.hasFirst {
				t.Fatal("did not reuse successful encoding")
			}
			return []byte("valid"), nil
		})
		if err != nil || string(data) != "valid" || !encodings.hasFirst || len(encodings.rest) != 0 {
			t.Fatalf("failed recovery: %q, %v, %+v", data, err, encodings)
		}
	}
}

func TestStreamSingleProjectionDoesNotAllocateCache(t *testing.T) {
	hub := NewStreamHub()
	sub := hub.SubscribeMode("watch", nil, nil, []byte{1}, true, nil)
	defer hub.Unsubscribe("watch", sub)
	payload := []byte("valid")
	encode := func([]byte, bool) ([]byte, error) { return payload, nil }
	allocs := testing.AllocsPerRun(100, func() {
		if err := hub.DispatchPreparedEvent("watch", nil, encode); err != nil {
			t.Fatal(err)
		}
		<-sub.Ch
	})
	if allocs != 0 {
		t.Fatalf("single projection allocated cache: %g", allocs)
	}
}

func TestStreamDispatchMatcherReusesDefaultEncoding(t *testing.T) {
	hub := NewStreamHub()
	encoded, matched := 0, 0
	matcher := func(data []byte, _ *StreamParams, _ any) bool {
		matched++
		if string(data) != "default" {
			t.Errorf("matcher received projected data: %q", data)
		}
		return true
	}
	encode := func(_ any, mask []byte) ([]byte, error) {
		encoded++
		if len(mask) == 0 {
			return []byte("default"), nil
		}
		return []byte("projected"), nil
	}
	if err := hub.Dispatch("watch", nil, matcher, encode); err != nil || encoded != 0 || matched != 0 {
		t.Fatalf("empty topic performed work: encoded=%d matched=%d err=%v", encoded, matched, err)
	}
	for _, mask := range [][]byte{nil, nil, {1, 1}} {
		sub := hub.Subscribe("watch", nil, nil, mask, nil)
		defer hub.Unsubscribe("watch", sub)
	}
	if err := hub.Dispatch("watch", nil, matcher, encode); err != nil {
		t.Fatal(err)
	}
	if encoded != 2 || matched != 3 {
		t.Fatalf("default encoding was not shared: encoded=%d matched=%d", encoded, matched)
	}
	for index, sub := range hub.subs["watch"] {
		want := "default"
		if index == 2 {
			want = "projected"
		}
		select {
		case data := <-sub.Ch:
			if string(data) != want {
				t.Errorf("subscriber %d received %q, want %q", index, data, want)
			}
		default:
			t.Errorf("subscriber %d did not receive the event", index)
		}
	}
}

func TestStreamDispatchSeparatesProjectionAndMode(t *testing.T) {
	hub := NewStreamHub()
	for _, choice := range []struct {
		mask byte
		mode bool
	}{{1, true}, {2, true}, {2, false}, {3, true}, {2, true}, {2, false}} {
		sub := hub.SubscribeMode("watch", nil, nil, []byte{1, choice.mask}, choice.mode, nil)
		defer hub.Unsubscribe("watch", sub)
	}
	calls := make(map[string]int)
	for round := range 2 {
		err := hub.DispatchPreparedEvent("watch", nil, func(mask []byte, binary bool) ([]byte, error) {
			key := fmt.Sprintf("%t/%x", binary, mask)
			calls[key]++
			return []byte(fmt.Sprintf("%d/%s", round, key)), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, sub := range hub.subs["watch"] {
			want := fmt.Sprintf("%d/%t/%x", round, sub.Binary, sub.FieldMask)
			select {
			case data := <-sub.Ch:
				if string(data) != want {
					t.Errorf("projection, mode or event crossed: got %q, want %q", data, want)
				}
			default:
				t.Errorf("missing event %q", want)
			}
		}
	}
	if len(calls) != 4 {
		t.Fatalf("encoded %d combinations, want 4", len(calls))
	}
	for key, count := range calls {
		if count != 2 {
			t.Errorf("%s encoded %d times, want once per event", key, count)
		}
	}
}

func TestSlowStreamReleasesOutsideHubLockOnce(t *testing.T) {
	hub := NewStreamHub()
	writerCtx, stopWriter := context.WithCancel(t.Context())
	defer stopWriter()
	released := 0
	prepared := preparedStream{release: func() {
		released++
		if writerCtx.Err() != context.Canceled {
			t.Error("release ran before stopping the writer")
		}
		if !hub.mu.TryLock() {
			t.Error("release ran while holding the hub lock")
			return
		}
		defer hub.mu.Unlock()
		if len(hub.subs["watch"]) != 1 {
			t.Error("release ran before removing the slow subscriber")
		}
	}}
	slow := hub.subscribePreparedMode("watch", nil, nil, nil, true, nil, stopWriter, prepared)
	for range cap(slow.Ch) {
		slow.Ch <- []byte("queued")
	}
	fast := hub.Subscribe("watch", nil, nil, nil, nil)
	defer hub.Unsubscribe("watch", fast)
	hub.DispatchEncoded("watch", []byte("next"), nil)
	if released != 1 || !slow.closed || !errors.Is(slow.result, ErrStreamFull) || hub.SubCount("watch") != 1 {
		t.Fatalf("slow subscriber was not released: released=%d closed=%t result=%v", released, slow.closed, slow.result)
	}
	if len(fast.Ch) != 1 || string(<-fast.Ch) != "next" {
		t.Fatal("slow subscriber cleanup interrupted the healthy subscriber")
	}
	hub.removeSlowSubscribers("watch", []*StreamSub{slow})
	hub.Unsubscribe("watch", slow)
	if released != 1 || hub.SubCount("watch") != 1 {
		t.Fatal("repeated cleanup released twice or removed the healthy subscriber")
	}
}

func TestStreamEncodingFailureStillRemovesSlowSubscribers(t *testing.T) {
	hub := NewStreamHub()
	cancelled := false
	slow := hub.Subscribe("watch", nil, nil, []byte{1}, func() { cancelled = true })
	for range cap(slow.Ch) {
		slow.Ch <- []byte("queued")
	}
	fast := hub.Subscribe("watch", nil, nil, []byte{2}, nil)
	defer hub.Unsubscribe("watch", fast)
	err := hub.DispatchPreparedEvent("watch", nil, func(mask []byte, _ bool) ([]byte, error) {
		if mask[0] == 1 {
			return []byte("valid"), nil
		}
		return []byte("partial"), codec.ErrNonFiniteFloat
	})
	if !errors.Is(err, codec.ErrNonFiniteFloat) || !cancelled || hub.SubCount("watch") != 1 || len(fast.Ch) != 0 {
		t.Fatalf("error cleanup failed: %v, cancelled=%t, subscribers=%d, queued=%d", err, cancelled, hub.SubCount("watch"), len(fast.Ch))
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

	hub.Dispatch("test", "rawData", nil, func(data any, fieldMask []byte) ([]byte, error) {
		return []byte(data.(string) + ":encoded"), nil
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

func TestStreamHubDispatchEventUsesTransportMode(t *testing.T) {
	hub := NewStreamHub()
	jsonSub := hub.SubscribeMode("watch", nil, nil, nil, false, nil)
	binarySub := hub.SubscribeMode("watch", nil, nil, []byte{1}, true, nil)

	hub.DispatchEvent("watch", []byte{9}, nil, func(mask []byte, binary bool) ([]byte, error) {
		if binary {
			return append([]byte{2}, mask...), nil
		}
		return []byte(`{"id":1}`), nil
	})

	if got := string(<-jsonSub.Ch); got != `{"id":1}` {
		t.Fatalf("json payload = %q", got)
	}
	if got := <-binarySub.Ch; !bytes.Equal(got, []byte{2, 1}) {
		t.Fatalf("binary payload = %v", got)
	}
}

func TestStreamHubDispatchEventMatcherRejects(t *testing.T) {
	hub := NewStreamHub()
	sub := hub.SubscribeMode("watch", nil, nil, nil, false, nil)
	encoded := false
	hub.DispatchEvent("watch", []byte{9}, func([]byte, *StreamParams, any) bool {
		return false
	}, func([]byte, bool) ([]byte, error) {
		encoded = true
		return []byte("unexpected"), nil
	})

	if encoded {
		t.Fatal("encoder called for rejected subscriber")
	}
	select {
	case data := <-sub.Ch:
		t.Fatalf("rejected subscriber received %q", data)
	default:
	}
}

func TestStreamHubDispatchEventCancelsSlowSubscriber(t *testing.T) {
	hub := NewStreamHub()
	cancelled := make(chan struct{}, 1)
	sub := hub.SubscribeMode("watch", nil, nil, nil, false, func() {
		cancelled <- struct{}{}
	})
	for range cap(sub.Ch) {
		sub.Ch <- []byte("queued")
	}

	hub.DispatchEvent("watch", nil, nil, func([]byte, bool) ([]byte, error) {
		return []byte("overflow"), nil
	})

	select {
	case <-cancelled:
	default:
		t.Fatal("slow subscriber was not cancelled")
	}
	if hub.SubCount("watch") != 0 {
		t.Fatal("slow subscriber was not removed")
	}
}

func TestDispatchPreparedEventMatchesTypedDataAndCachesEncoding(t *testing.T) {
	hub := NewStreamHub()
	first := hub.SubscribeMode("watch", map[string]any{"id": int64(1)}, nil, []byte{1}, true, nil)
	second := hub.SubscribeMode("watch", map[string]any{"id": int64(2)}, nil, []byte{1}, true, nil)
	third := hub.SubscribeMode("watch", map[string]any{"id": int64(3)}, nil, []byte{2}, false, nil)
	var matches, encodes int
	hub.DispatchPreparedEvent("watch", func(params *StreamParams, identity any) bool {
		matches++
		return params.Int("id") != 2
	}, func(mask []byte, binary bool) ([]byte, error) {
		encodes++
		return append([]byte{byte(encodes)}, mask...), nil
	})
	if matches != 3 {
		t.Fatalf("matcher calls = %d, want 3", matches)
	}
	if encodes != 2 {
		t.Fatalf("encoding variants = %d, want 2", encodes)
	}
	if len(first.Ch) != 1 || len(second.Ch) != 0 || len(third.Ch) != 1 {
		t.Fatalf("unexpected deliveries: first=%d second=%d third=%d", len(first.Ch), len(second.Ch), len(third.Ch))
	}
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
	if hub.SubCount("test") != 0 {
		t.Fatal("slow subscriber was not removed")
	}

	hub.Unsubscribe("test", sub)
}

func TestStreamParams(t *testing.T) {
	p := &StreamParams{values: map[string]any{
		"roomId": int64(42),
		"name":   "test",
		"rate":   float64(3.14),
		"active": true,
		"ttl":    "2s",
		"uuid":   "550e8400-e29b-41d4-a716-446655440000",
	}}

	if p.Int("roomId") != 42 {
		t.Errorf("Int(roomId) = %d", p.Int("roomId"))
	}
	if p.String("name") != "test" {
		t.Errorf("String(name) = %q", p.String("name"))
	}
	if p.Float("rate") != 3.14 || !p.Boolean("active") || p.Duration("ttl") != int64(2*time.Second) {
		t.Fatal("typed stream parameter accessors returned incorrect values")
	}
	if got := p.UUID("uuid"); got == [16]byte{} {
		t.Fatal("UUID accessor did not parse canonical UUID")
	}
	if _, ok := p.LookupInt("rate"); ok {
		t.Fatal("fractional float must not be accepted as Int")
	}
	if p.Int("missing") != 0 {
		t.Error("missing should be 0")
	}

	// nil params
	var nilP *StreamParams
	if nilP.Int("x") != 0 || nilP.String("x") != "" || nilP.Float("x") != 0 || nilP.Boolean("x") || nilP.Duration("x") != 0 || nilP.UUID("x") != [16]byte{} || nilP.Get("x") != nil {
		t.Error("nil params should return zero values")
	}
}

func TestStreamHub_NoSubscribers(t *testing.T) {
	hub := NewStreamHub()
	// Should not panic
	hub.DispatchEncoded("nonexistent", []byte("data"), nil)
	hub.Dispatch("nonexistent", "data", nil, func(data any, mask []byte) ([]byte, error) { return nil, nil })
}

func TestRouterOpenInternalStreamUsesCanonicalSubscriptionLifecycle(t *testing.T) {
	rt := NewRouter()
	rt.Schema.RegisterAPI(&schema.API{ID: 71, Name: "watchInternal", Stream: true})
	rt.Registry.Register("watchInternal", 71)
	rt.Registry.RegisterParams("watchInternal", []ParamMeta{{Name: "id", Type: "Int", FieldID: 1}})
	mask := testutil.Bytes(t)(codec.AppendSelectionMask(nil, []byte{1}, nil))
	requestData, err := EncodeBinaryRequest(71, mask, map[string]any{"id": int64(42)}, rt.Registry.ParamOrder("watchInternal"))
	if err != nil {
		t.Fatal(err)
	}
	req, err := rt.Registry.ParseBinaryRequest(requestData)
	if err != nil {
		t.Fatal(err)
	}

	stream, err := rt.OpenInternalStream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if rt.Streams.SubCount("watchInternal") != 1 {
		t.Fatal("internal stream was not registered")
	}
	rt.Streams.DispatchEncoded("watchInternal", []byte("payload"), nil)
	if got := <-stream.Messages(); string(got) != "payload" {
		t.Fatalf("stream payload = %q", got)
	}
	stream.Close()
	if rt.Streams.SubCount("watchInternal") != 0 {
		t.Fatal("internal stream was not removed on close")
	}
}

func TestRouterOpenInternalNativeStreamInvokesHandler(t *testing.T) {
	rt := NewRouter()
	rt.Registry.Register("watchNativeInternal", 72)
	rt.Registry.RegisterParams("watchNativeInternal", nil)
	rt.HandleStreamNative("watchNativeInternal", func(ctx context.Context, params *StreamParams, identity any, stream *Stream) error {
		if err := stream.Send([]byte("native")); err != nil {
			t.Errorf("native stream send: %v", err)
		}
		return nil
	})
	req, err := rt.Registry.ParseBinaryRequest([]byte{72, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := rt.OpenInternalStream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	select {
	case got := <-stream.Messages():
		if string(got) != "native" {
			t.Fatalf("native payload = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("native stream handler was not invoked")
	}
	deadline := time.Now().Add(time.Second)
	for rt.Streams.SubCount("watchNativeInternal") != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if rt.Streams.SubCount("watchNativeInternal") != 0 {
		t.Fatal("completed native handler did not close its subscription")
	}
}

func TestRouterOpenInternalStreamRejectsRegularAPI(t *testing.T) {
	rt := NewRouter()
	rt.Registry.Register("regular", 73)
	rt.Registry.RegisterParams("regular", nil)
	req, err := rt.Registry.ParseBinaryRequest([]byte{73, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.OpenInternalStream(context.Background(), req); err == nil {
		t.Fatal("regular API must not open an internal stream")
	}
}

func TestTypedStreamEncodesUsingSubscriberSelectionAndMode(t *testing.T) {
	hub := NewStreamHub()
	mask := []byte{1, 2}
	sub := hub.SubscribeMode("typed", nil, nil, mask, false, nil)
	raw := &Stream{sub: sub, ctx: context.Background()}
	typed := NewTypedStream(raw, func(value int64, gotMask []byte, binary bool) ([]byte, error) {
		if value != 42 || !bytes.Equal(gotMask, mask) || binary {
			t.Fatalf("encoder received value=%d mask=%v binary=%v", value, gotMask, binary)
		}
		return []byte("encoded"), nil
	})
	if err := typed.Send(42); err != nil {
		t.Fatal(err)
	}
	if got := <-sub.Ch; string(got) != "encoded" {
		t.Fatalf("typed stream payload = %q", got)
	}
	if typed.Context() != raw.Context() {
		t.Fatal("typed stream context does not match raw stream context")
	}
	if !bytes.Equal(raw.FieldMask(), mask) || raw.Binary() {
		t.Fatalf("stream metadata mask=%v binary=%v", raw.FieldMask(), raw.Binary())
	}
	params := &StreamParams{binary: []byte{1, 0}}
	if !bytes.Equal(params.Binary(), []byte{1, 0}) {
		t.Fatalf("binary params = %v", params.Binary())
	}
	var nilParams *StreamParams
	if nilParams.Binary() != nil {
		t.Fatal("nil stream params must return nil binary data")
	}
}

func TestStreamHub_UnsubscribeNonExistent(t *testing.T) {
	hub := NewStreamHub()
	sub := &StreamSub{Ch: make(chan []byte, 1)}
	// Should not panic
	hub.Unsubscribe("nonexistent", sub)
}

func TestWritePumpsStopOnContextDone(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, *StreamSub)
	}{
		{
			name: "JSON",
			run: func(ctx context.Context, sub *StreamSub) {
				WritePumpJSON(ctx, nil, 1, sub)
			},
		},
		{
			name: "Binary",
			run: func(ctx context.Context, sub *StreamSub) {
				WritePumpBinary(ctx, nil, 11, sub)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			sub := &StreamSub{Ch: make(chan []byte)}
			// Release a regressed pump too, so a failed assertion cannot leak it.
			defer close(sub.Ch)
			done := make(chan struct{})
			go func() {
				test.run(ctx, sub)
				close(done)
			}()
			timer := time.NewTimer(time.Second)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
				t.Fatal("write pump did not stop after context cancellation")
			}
		})
	}
}

// --- WebSocket stream integration tests ---

func TestWSJSON_Subscribe(t *testing.T) {
	rt := testWSRouter()
	rt.Registry.Register("watchTest", 11)
	rt.Registry.RegisterParams("watchTest", []ParamMeta{{Name: "roomId", Type: "Int", FieldID: 1}})
	rt.Schema.APIs["watchTest"].Params = []schema.Param{{ID: 1, Name: "roomId", Type: schema.FieldInt, HasDefault: true}}
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
	conn.Write(ctx, websocket.MessageText, []byte(`{"$id":1,"$sub":"watchTest","roomId":1}`))
	_, ack, err := conn.Read(ctx)
	if err != nil || string(ack) != `{"$sub":1,"ok":true}` {
		t.Fatalf("subscription acknowledgement = %s, %v", ack, err)
	}

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

func TestWSJSONSubscribeUsesCanonicalSelectionParsing(t *testing.T) {
	rt := testWSRouter()
	rt.Schema.RegisterAPI(&schema.API{ID: 12, Name: "watchUser", Module: "user", ReturnType: "User", Stream: true})
	rt.Registry.Register("watchUser", 12)
	rt.Registry.RegisterParams("watchUser", nil)
	srv := httptest.NewServer(rt)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"$id":1,"$sub":"watchUser","$select":"name{"}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if rt.Streams.SubCount("watchUser") != 0 {
		t.Fatal("malformed selection must not create a subscription")
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"$id":2,"$sub":"watchUser","$select":"name"}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)

	rt.Streams.mu.RLock()
	subs := rt.Streams.subs["watchUser"]
	if len(subs) != 1 || !bytes.Equal(subs[0].FieldMask, []byte{1, 2}) {
		t.Fatalf("subscriptions = %#v", subs)
	}
	rt.Streams.mu.RUnlock()
}

func TestWSJSON_Unsubscribe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rt := testWSRouter()
		rt.Registry.Register("watchTest", 11)
		rt.Registry.RegisterParams("watchTest", nil)
		ctx, conn := newInMemoryWebSocket(t, rt)

		// Subscribe then unsubscribe
		writeWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$id":1,"$sub":"watchTest"}`))
		requireWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$sub":1,"ok":true}`))
		if rt.Streams.SubCount("watchTest") != 1 {
			t.Fatal("should have 1 sub")
		}

		writeWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$unsub":1}`))
		synctest.Wait()
		if got := rt.Streams.SubCount("watchTest"); got != 0 {
			t.Errorf("should have 0 subs after unsub, got %d", got)
		}
	})
}

func TestWSBinary_Subscribe(t *testing.T) {
	rt := testWSRouter()
	registerTestStream(rt, "watchBinary")
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

	// Binary subscribe: [frame][subscription ID=1][API ID=50][mask=0][param field1=roomId=123][0x00]
	var reqBuf []byte
	reqBuf = append(reqBuf, BinaryFrameSubscribe, 1)
	reqBuf = codec.AppendVarint(reqBuf, 50) // API ID
	reqBuf = codec.AppendVarint(reqBuf, 0)  // mask
	var enc codec.Encoder
	enc.WriteFieldInt(1, 123) // roomId=123
	enc.WriteEnd()
	reqBuf = append(reqBuf, enc.Bytes()...)

	conn.Write(ctx, websocket.MessageBinary, reqBuf)
	_, ack, err := conn.Read(ctx)
	if err != nil || !bytes.Equal(ack, []byte{BinaryFrameSubscribeSuccess, 1}) {
		t.Fatalf("subscription acknowledgement = %v, %v", ack, err)
	}
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

	if data[0] != BinaryFrameStream {
		t.Errorf("first byte should be stream frame, got %x", data[0])
	}
}

func TestWSBinarySubscribePreservesExplicitNullParam(t *testing.T) {
	rt := testWSRouter()
	registerTestStream(rt, "watchNullable")
	rt.Registry.Register("watchNullable", 52)
	rt.Registry.RegisterParams("watchNullable", []ParamMeta{{Name: "query", Type: "String", FieldID: 1, Nullable: true}})
	srv := httptest.NewServer(rt)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	frame := []byte{BinaryFrameSubscribe, 1, 52, 0, 1, 0, 0}
	if err := conn.Write(ctx, websocket.MessageBinary, frame); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	rt.Streams.mu.RLock()
	subs := rt.Streams.subs["watchNullable"]
	_, present := subs[0].Params.values["query"]
	rt.Streams.mu.RUnlock()
	if !present {
		t.Fatal("explicit null parameter became absent")
	}
}

func TestWSBinary_Unsubscribe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rt := testWSRouter()
		registerTestStream(rt, "watchBin2")
		rt.Registry.Register("watchBin2", 51)
		ctx, conn := newInMemoryWebSocket(t, rt)

		// Subscribe
		writeWSFrame(t, ctx, conn, websocket.MessageBinary, []byte{BinaryFrameSubscribe, 1, 51, 0, 0})
		requireWSFrame(t, ctx, conn, websocket.MessageBinary, []byte{BinaryFrameSubscribeSuccess, 1})
		if rt.Streams.SubCount("watchBin2") != 1 {
			t.Fatal("should have 1 sub")
		}

		// Unsubscribe: [frame][subscription ID=1]
		writeWSFrame(t, ctx, conn, websocket.MessageBinary, []byte{BinaryFrameUnsubscribe, 1})
		synctest.Wait()
		if got := rt.Streams.SubCount("watchBin2"); got != 0 {
			t.Errorf("should have 0 subs, got %d", got)
		}
	})
}

func TestWSBinaryUnsubscribeRejectsTrailingBytes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rt := testWSRouter()
		registerTestStream(rt, "watchStrict")
		rt.Registry.Register("watchStrict", 53)
		ctx, conn := newInMemoryWebSocket(t, rt)

		writeWSFrame(t, ctx, conn, websocket.MessageBinary, []byte{BinaryFrameSubscribe, 1, 53, 0, 0})
		requireWSFrame(t, ctx, conn, websocket.MessageBinary, []byte{BinaryFrameSubscribeSuccess, 1})
		writeWSFrame(t, ctx, conn, websocket.MessageBinary, []byte{BinaryFrameUnsubscribe, 1, 99})
		synctest.Wait()
		if rt.Streams.SubCount("watchStrict") != 1 {
			t.Fatal("non-canonical unsubscribe frame must be rejected")
		}

		// A rejected frame must leave the connection usable for a valid unsubscribe.
		writeWSFrame(t, ctx, conn, websocket.MessageBinary, []byte{BinaryFrameUnsubscribe, 1})
		synctest.Wait()
		if got := rt.Streams.SubCount("watchStrict"); got != 0 {
			t.Errorf("should have 0 subs after valid unsubscribe, got %d", got)
		}
	})
}

func TestWSJSON_DisconnectCleansUpSubs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rt := testWSRouter()
		registerTestStream(rt, "watchCleanup")
		rt.Registry.Register("watchCleanup", 54)
		rt.Registry.RegisterParams("watchCleanup", nil)
		ctx, conn := newInMemoryWebSocket(t, rt)

		// Subscribe
		writeWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$id":1,"$sub":"watchCleanup"}`))
		requireWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$sub":1,"ok":true}`))
		if rt.Streams.SubCount("watchCleanup") != 1 {
			t.Fatal("should have 1 sub")
		}

		// Close connection
		if err := conn.Close(websocket.StatusNormalClosure, "bye"); err != nil {
			t.Fatalf("close: %v", err)
		}
		synctest.Wait()

		// Subs should be cleaned up
		if got := rt.Streams.SubCount("watchCleanup"); got != 0 {
			t.Errorf("should have 0 subs after disconnect, got %d", got)
		}
	})
}

func TestWSBinary_UnknownFieldID(t *testing.T) {
	rt := testWSRouter()
	registerTestStream(rt, "watchUnknown")
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

	// Binary subscribe with unknown field ID 99 is rejected as malformed.
	var reqBuf []byte
	reqBuf = append(reqBuf, BinaryFrameSubscribe, 1)
	reqBuf = codec.AppendVarint(reqBuf, 60) // API ID
	reqBuf = codec.AppendVarint(reqBuf, 0)  // no mask
	var enc codec.Encoder
	enc.WriteFieldInt(1, 42)  // known: roomId=42
	enc.WriteFieldInt(99, 77) // unknown field ID 99 — should trigger break
	enc.WriteEnd()
	reqBuf = append(reqBuf, enc.Bytes()...)

	conn.Write(ctx, websocket.MessageBinary, reqBuf)
	time.Sleep(50 * time.Millisecond)

	if rt.Streams.SubCount("watchUnknown") != 0 {
		t.Errorf("malformed subscription should be rejected, got %d", rt.Streams.SubCount("watchUnknown"))
	}
}

func TestWSJSON_StreamNativeHandler(t *testing.T) {
	rt := testWSRouter()
	rt.Registry.Register("watchNative", 66)
	rt.Registry.RegisterParams("watchNative", []ParamMeta{{Name: "matchId", Type: "Int", FieldID: 1}})
	rt.Schema.RegisterAPI(&schema.API{
		ID: 66, Name: "watchNative", Stream: true,
		Params: []schema.Param{{ID: 1, Name: "matchId", Type: schema.FieldInt}},
	})

	// Register a native stream handler that pushes 3 messages then stops
	handlerCalled := make(chan struct{}, 1)
	rt.HandleStreamNative("watchNative", func(ctx context.Context, params *StreamParams, identity any, stream *Stream) error {
		handlerCalled <- struct{}{}
		for i := 0; i < 3; i++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				stream.Send([]byte(`{"score":` + fmt.Sprintf("%d", i) + `}`))
				time.Sleep(10 * time.Millisecond)
			}
		}
		return nil
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
	subMsg := `{"$id":1,"$sub":"watchNative","matchId":1}`
	conn.Write(ctx, websocket.MessageText, []byte(subMsg))
	_, ack, err := conn.Read(ctx)
	if err != nil || string(ack) != `{"$sub":1,"ok":true}` {
		t.Fatalf("subscription acknowledgement = %s, %v", ack, err)
	}

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

// --- Stream.Send / Stream.Context tests ---

func TestStream_Send_Success(t *testing.T) {
	ch := make(chan []byte, 1)
	sub := &StreamSub{Ch: ch}
	ctx := context.Background()
	s := &Stream{sub: sub, ctx: ctx}

	err := s.Send([]byte("hello"))
	if err != nil {
		t.Fatalf("Send should succeed: %v", err)
	}
	got := <-ch
	if string(got) != "hello" {
		t.Errorf("got %q, want hello", got)
	}
}

func TestStream_Send_ContextCancelled(t *testing.T) {
	ch := make(chan []byte) // unbuffered, will block
	sub := &StreamSub{Ch: ch}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	s := &Stream{sub: sub, ctx: ctx}

	err := s.Send([]byte("data"))
	if err == nil {
		t.Fatal("Send should return error when context cancelled")
	}
}

func TestStream_Send_Full(t *testing.T) {
	ch := make(chan []byte, 1)
	ch <- []byte("full") // fill channel
	sub := &StreamSub{Ch: ch}
	ctx := context.Background()
	s := &Stream{sub: sub, ctx: ctx}

	err := s.Send([]byte("overflow"))
	if err != ErrStreamFull {
		t.Fatalf("expected ErrStreamFull, got %v", err)
	}
}

func TestStream_Context(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Stream{ctx: ctx}
	if s.Context() != ctx {
		t.Error("Context() should return the stream's context")
	}
}

// --- Identity helpers ---

type mockClaims struct {
	id   int64
	data map[string]any
}

func (m *mockClaims) ID() int64 { return m.id }
func (m *mockClaims) Int(key string) int64 {
	if v, ok := m.data[key].(int64); ok {
		return v
	}
	return 0
}
func (m *mockClaims) String(key string) string {
	if v, ok := m.data[key].(string); ok {
		return v
	}
	return ""
}

func TestIdentityID(t *testing.T) {
	// With valid IdentityClaims
	claims := &mockClaims{id: 42}
	if got := IdentityID(claims); got != 42 {
		t.Errorf("IdentityID = %d, want 42", got)
	}
	// With nil
	if got := IdentityID(nil); got != 0 {
		t.Errorf("IdentityID(nil) = %d, want 0", got)
	}
	// With non-claims type
	if got := IdentityID("not-claims"); got != 0 {
		t.Errorf("IdentityID(string) = %d, want 0", got)
	}
}

func TestIdentityInt(t *testing.T) {
	claims := &mockClaims{data: map[string]any{"age": int64(30)}}
	if got := IdentityInt(claims, "age"); got != 30 {
		t.Errorf("IdentityInt = %d, want 30", got)
	}
	if got := IdentityInt(nil, "age"); got != 0 {
		t.Errorf("IdentityInt(nil) = %d, want 0", got)
	}
	if got := IdentityInt("not-claims", "age"); got != 0 {
		t.Errorf("IdentityInt(string) = %d, want 0", got)
	}
}

func TestIdentityString(t *testing.T) {
	claims := &mockClaims{data: map[string]any{"role": "admin"}}
	if got := IdentityString(claims, "role"); got != "admin" {
		t.Errorf("IdentityString = %q, want admin", got)
	}
	if got := IdentityString(nil, "role"); got != "" {
		t.Errorf("IdentityString(nil) = %q, want empty", got)
	}
}

// --- StreamParams additional paths ---

func TestStreamParams_Int_Float64(t *testing.T) {
	// When JSON numbers are parsed, they come as float64
	p := &StreamParams{values: map[string]any{"count": float64(7)}}
	if got := p.Int("count"); got != 7 {
		t.Errorf("Int(float64) = %d, want 7", got)
	}
}

func TestStreamParams_Int_WrongType(t *testing.T) {
	p := &StreamParams{values: map[string]any{"count": "not-int"}}
	if got := p.Int("count"); got != 0 {
		t.Errorf("Int(string) = %d, want 0", got)
	}
}

func TestStreamParams_String_Missing(t *testing.T) {
	p := &StreamParams{values: map[string]any{"x": int64(1)}}
	if got := p.String("x"); got != "" {
		t.Errorf("String(int) = %q, want empty", got)
	}
}

func TestStreamParams_Get_Exists(t *testing.T) {
	p := &StreamParams{values: map[string]any{"key": "val"}}
	if got := p.Get("key"); got != "val" {
		t.Errorf("Get = %v, want val", got)
	}
	if got := p.Get("missing"); got != nil {
		t.Errorf("Get(missing) = %v, want nil", got)
	}
}

func TestStreamParamsNativeTypeVariants(t *testing.T) {
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	fixedID := [16]byte(id)
	params := &StreamParams{values: map[string]any{
		"int":             int(1),
		"floatFromInt":    int(2),
		"floatFromInt64":  int64(3),
		"duration":        4 * time.Second,
		"durationFromInt": int64(5),
		"badDuration":     "invalid",
		"nativeUUID":      id,
		"fixedUUID":       fixedID,
		"badUUID":         "invalid",
		"invalid":         struct{}{},
	}}
	if value, ok := params.LookupInt("int"); !ok || value != 1 {
		t.Fatalf("native int = %d, %v", value, ok)
	}
	for name, want := range map[string]float64{"floatFromInt": 2, "floatFromInt64": 3} {
		if value, ok := params.LookupFloat(name); !ok || value != want {
			t.Errorf("%s = %v, %v", name, value, ok)
		}
	}
	if _, ok := params.LookupFloat("invalid"); ok {
		t.Fatal("invalid float was accepted")
	}
	for name, want := range map[string]int64{"duration": int64(4 * time.Second), "durationFromInt": 5} {
		if value, ok := params.LookupDuration(name); !ok || value != want {
			t.Errorf("%s = %v, %v", name, value, ok)
		}
	}
	if _, ok := params.LookupDuration("badDuration"); ok {
		t.Fatal("invalid duration was accepted")
	}
	for name := range map[string][16]byte{"nativeUUID": fixedID, "fixedUUID": fixedID} {
		if value, ok := params.LookupUUID(name); !ok || value != fixedID {
			t.Errorf("%s = %v, %v", name, value, ok)
		}
	}
	if _, ok := params.LookupUUID("badUUID"); ok {
		t.Fatal("invalid UUID was accepted")
	}
	if _, ok := params.LookupUUID("invalid"); ok {
		t.Fatal("unsupported UUID representation was accepted")
	}
}

// --- Dispatch with matcher + fieldMask ---

func TestStreamHub_DispatchWithMatcherAndFieldMask(t *testing.T) {
	hub := NewStreamHub()

	// sub with fieldMask
	sub := hub.Subscribe("test", nil, nil, []byte{0x01}, func() {})

	hub.Dispatch("test", "data", func(data []byte, params *StreamParams, identity any) bool {
		return true
	}, func(data any, fieldMask []byte) ([]byte, error) {
		if fieldMask != nil {
			return []byte("masked:" + data.(string)), nil
		}
		return []byte("full:" + data.(string)), nil
	})

	select {
	case got := <-sub.Ch:
		if string(got) != "masked:data" {
			t.Errorf("expected masked:data, got %q", got)
		}
	case <-time.After(time.Second):
		t.Error("timeout")
	}
	hub.Unsubscribe("test", sub)
}

func TestStreamHub_DispatchChanFull(t *testing.T) {
	hub := NewStreamHub()
	cancelled := make(chan struct{}, 1)
	sub := hub.Subscribe("test", nil, nil, nil, func() {
		cancelled <- struct{}{}
	})

	// Fill the channel
	for i := 0; i < 64; i++ {
		sub.Ch <- []byte("x")
	}

	// Dispatch should trigger cancel on full channel
	hub.Dispatch("test", "overflow", nil, func(data any, mask []byte) ([]byte, error) {
		return []byte("enc"), nil
	})

	select {
	case <-cancelled:
		// good
	case <-time.After(100 * time.Millisecond):
		t.Error("subscriber should be cancelled when channel full")
	}
	if hub.SubCount("test") != 0 {
		t.Fatal("slow subscriber was not removed")
	}
	hub.Unsubscribe("test", sub)
}

func TestStreamHub_UnsubscribeNilCancel(t *testing.T) {
	hub := NewStreamHub()
	// Subscribe with nil cancel
	sub := hub.Subscribe("test", nil, nil, nil, nil)
	// Should not panic
	hub.Unsubscribe("test", sub)
}

func TestStreamHub_DispatchEncodedNilCancel(t *testing.T) {
	hub := NewStreamHub()
	sub := hub.Subscribe("test", nil, nil, nil, nil)
	// Fill channel
	for i := 0; i < 64; i++ {
		sub.Ch <- []byte("x")
	}
	// Dispatch should not panic even with nil cancel
	hub.DispatchEncoded("test", []byte("overflow"), nil)
	if hub.SubCount("test") != 0 {
		t.Fatal("slow subscriber with nil cancel was not removed")
	}
	hub.Unsubscribe("test", sub)
}

func TestStreamHubSubscribeLimit(t *testing.T) {
	hub := NewStreamHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx

	// Fill up to the limit
	for i := 0; i < MaxSubscribersPerAPI; i++ {
		sub := hub.Subscribe("testAPI", nil, nil, nil, cancel)
		if sub == nil {
			t.Fatalf("subscribe should succeed at %d", i)
		}
	}

	// Next one should be rejected
	sub := hub.Subscribe("testAPI", nil, nil, nil, cancel)
	if sub != nil {
		t.Fatal("subscribe should return nil when at capacity")
	}

	// Different API should still work
	sub2 := hub.Subscribe("otherAPI", nil, nil, nil, cancel)
	if sub2 == nil {
		t.Fatal("subscribe to different API should succeed")
	}
}
