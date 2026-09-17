package api

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"nhooyr.io/websocket"
)

func TestWSNativeFinishDrainsAcceptedMessages(t *testing.T) {
	for _, binary := range []bool{false, true} {
		for _, failure := range []bool{false, true} {
			t.Run(fmt.Sprintf("binary=%t/failure=%t", binary, failure), func(t *testing.T) {
				router := finiteStreamRouter(t, failure)
				server := httptest.NewServer(router)
				defer server.Close()
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
				defer cancel()
				conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.CloseNow()
				subscribeAndReadFinite(t, ctx, conn, binary, 1)
				requireFiniteTerminal(t, ctx, conn, binary, 1, failure)
				// Finishing one stream must not close the multiplexed connection.
				writeWSFrame(t, ctx, conn, websocket.MessageText, sequenceRequest(websocket.MessageText, "getUser", 99))
				_, response, err := conn.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				assertSequenceResponse(t, websocket.MessageText, "getUser", 99, response)
			})
		}
	}
}

func subscribeAndReadFinite(t *testing.T, ctx context.Context, conn *websocket.Conn, binary bool, id uint64) {
	t.Helper()
	mode := websocket.MessageText
	request := []byte(fmt.Sprintf(`{"$id":%d,"$sub":"watchFinite"}`, id))
	ack := []byte(fmt.Sprintf(`{"$sub":%d,"ok":true}`, id))
	if binary {
		mode = websocket.MessageBinary
		request = append(codec.AppendVarint([]byte{BinaryFrameSubscribe}, id), 72, 0, 0)
		ack = codec.AppendVarint([]byte{BinaryFrameSubscribeSuccess}, id)
	}
	writeWSFrame(t, ctx, conn, mode, request)
	requireWSFrame(t, ctx, conn, mode, ack)
	for i := range 64 {
		want := []byte(fmt.Sprintf(`{"$stream":%d,"data":%d}`, id, i))
		if binary {
			want = append(codec.AppendVarint([]byte{BinaryFrameStream}, id), byte(i*2))
		}
		requireWSFrame(t, ctx, conn, mode, want)
	}
}

func TestWSFinishedStreamReleasesConnectionRegistration(t *testing.T) {
	for _, binary := range []bool{false, true} {
		for _, failure := range []bool{false, true} {
			t.Run(fmt.Sprintf("binary=%t/failure=%t", binary, failure), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx, conn := newInMemoryWebSocket(t, finiteStreamRouter(t, failure))
					for i := range 2 {
						subscribeAndReadFinite(t, ctx, conn, binary, uint64(i+1))
						requireFiniteTerminal(t, ctx, conn, binary, uint64(i+1), failure)
						// Wait for handler and writer exit, without a sleep or network poll.
						synctest.Wait()
					}
				})
			})
		}
	}
}

func requireFiniteTerminal(t *testing.T, ctx context.Context, conn *websocket.Conn, binary bool, id uint64, failure bool) {
	t.Helper()
	mode := websocket.MessageText
	want := []byte(fmt.Sprintf(`{"$end":%d,"ok":true}`, id))
	if binary {
		mode = websocket.MessageBinary
		want = codec.AppendVarint([]byte{0x15}, id)
	}
	if !failure {
		requireWSFrame(t, ctx, conn, mode, want)
		return
	}
	kind, body, err := conn.Read(ctx)
	if err != nil || kind != mode {
		t.Fatalf("terminal kind=%v err=%v", kind, err)
	}
	if binary {
		prefix := codec.AppendVarint([]byte{0x16}, id)
		if !strings.HasPrefix(string(body), string(prefix)) {
			t.Fatalf("terminal prefix=%x", body)
		}
		decoded, err := DecodeBinaryError(body[len(prefix):], 0)
		if err != nil || decoded.Name != "Conflict" || decoded.Code != 409 {
			t.Fatalf("terminal error=%v decode=%v", decoded, err)
		}
		return
	}
	if !strings.Contains(string(body), fmt.Sprintf(`"$end":%d`, id)) ||
		!strings.Contains(string(body), `"error":"Conflict"`) || !strings.Contains(string(body), `"code":409`) {
		t.Fatalf("terminal error=%s", body)
	}
}

func finiteStreamRouter(t *testing.T, failure bool) *Router {
	t.Helper()
	router := testWSRouter()
	router.Registry.Register("watchFinite", 72)
	router.Registry.RegisterParams("watchFinite", nil)
	router.Schema.RegisterAPI(&schema.API{ID: 72, Name: "watchFinite", ReturnType: "Int", Stream: true})
	router.HandleStreamNative("watchFinite", func(_ context.Context, _ *StreamParams, _ any, stream *Stream) error {
		for i := range 64 {
			payload := []byte(fmt.Sprint(i))
			if stream.Binary() {
				payload = []byte{byte(i * 2)}
			}
			if err := stream.Send(payload); err != nil {
				t.Errorf("enqueue %d: %v", i, err)
				return err
			}
		}
		if failure {
			return luxerrors.Conflict
		}
		return nil
	})
	return router
}

func TestWSStreamCompletionAndAbortLifetimes(t *testing.T) {
	for _, result := range []error{nil, luxerrors.Conflict} {
		synctest.Test(t, func(t *testing.T) {
			hub := NewStreamHub()
			sub, producer, writer := hub.subscribeWS(t.Context(), "watch", nil, nil, nil, false)
			hub.finish("watch", sub, result)
			if producer.Err() == nil || writer.Err() != nil {
				t.Fatalf("producer=%v writer=%v", producer.Err(), writer.Err())
			}
			// Completion removed the producer, but cancellation must still abort
			// a draining writer without changing the already-published result.
			hub.Unsubscribe("watch", sub)
			if writer.Err() == nil || sub.result != result {
				t.Fatalf("writer=%v result=%v", writer.Err(), sub.result)
			}
			hub.Unsubscribe("watch", sub)
		})
	}
}

func TestWSStreamCancellationAndSlowConsumerStopBothContexts(t *testing.T) {
	for _, scenario := range []string{"unsubscribe", "slow", "disconnect"} {
		t.Run(scenario, func(t *testing.T) {
			hub := NewStreamHub()
			parent, disconnect := context.WithCancel(t.Context())
			defer disconnect()
			sub, producer, writer := hub.subscribeWS(parent, "watch", nil, nil, nil, false)
			switch scenario {
			case "unsubscribe":
				hub.Unsubscribe("watch", sub)
			case "slow":
				for range 65 {
					hub.DispatchEncoded("watch", nil, nil)
				}
				if !errors.Is(sub.result, ErrStreamFull) {
					t.Fatalf("slow result=%v", sub.result)
				}
			case "disconnect":
				disconnect()
			}
			if producer.Err() == nil || writer.Err() == nil {
				t.Fatalf("producer=%v writer=%v", producer.Err(), writer.Err())
			}
			hub.Unsubscribe("watch", sub)
		})
	}
}

func TestWSStreamLimitCleansBothContexts(t *testing.T) {
	hub := NewStreamHub()
	hub.subs["watch"] = make([]*StreamSub, MaxSubscribersPerAPI)
	sub, producer, writer := hub.subscribeWS(t.Context(), "watch", nil, nil, nil, false)
	if sub != nil || producer.Err() == nil || writer.Err() == nil {
		t.Fatalf("sub=%v producer=%v writer=%v", sub, producer.Err(), writer.Err())
	}
}

func TestWSSubscriptionACKFailureCleansRegistration(t *testing.T) {
	for _, binary := range []bool{false, true} {
		t.Run(fmt.Sprint(binary), func(t *testing.T) {
			router := finiteStreamRouter(t, false)
			ctx, conn := newInMemoryWebSocket(t, router)
			conn.CloseNow()
			ws := &wsConn{conn: conn, ctx: ctx}
			other := router.Streams.Subscribe("other", nil, nil, nil, nil)
			defer router.Streams.Unsubscribe("other", other)
			var items connectionSubscriptions
			items.add(0, "other", other)
			if binary {
				router.handleWSStreamBinary(ctx, ws, []byte{BinaryFrameSubscribe, 1, 72, 0, 0}, &items)
			} else {
				router.handleWSText(ctx, ws, []byte(`{"$id":1,"$sub":"watchFinite"}`), &items)
			}
			if router.Streams.SubCount("watchFinite") != 0 || len(items.items) != 1 || items.items[0].sub != other {
				t.Fatal("failed ACK retained a subscription or removed an unrelated one")
			}
		})
	}
}

func TestWSWriterExitStopsProducer(t *testing.T) {
	for _, binary := range []bool{false, true} {
		for _, failedWrite := range []bool{false, true} {
			t.Run(fmt.Sprintf("binary=%t/writeFailure=%t", binary, failedWrite), func(t *testing.T) {
				router := testWSRouter()
				ctx, conn := newInMemoryWebSocket(t, router)
				conn.CloseNow()
				sub, producer, writer := router.Streams.subscribeWS(ctx, "watch", nil, nil, nil, binary)
				var items connectionSubscriptions
				items.add(72, "watch", sub)
				if failedWrite {
					sub.Ch <- []byte("0")
				} else {
					sub.stopWriting()
				}
				router.writeSubscription(writer, &wsConn{conn: conn, ctx: ctx}, "watch", 72, sub, &items)
				if producer.Err() == nil || writer.Err() == nil || router.Streams.SubCount("watch") != 0 || len(items.items) != 0 {
					t.Fatal("writer exit did not clean up its producer")
				}
				if !errors.Is(sub.result, context.Canceled) {
					t.Fatalf("result=%v", sub.result)
				}
			})
		}
	}
}
