package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"testing"
	"testing/synctest"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"nhooyr.io/websocket"
)

func subscriptionIdentityRouter() *Router {
	router := testWSRouter()
	router.Registry.Register("watchIdentity", 71)
	router.Registry.RegisterParams("watchIdentity", nil)
	router.Schema.RegisterAPI(&schema.API{ID: 71, Name: "watchIdentity", ReturnType: "Int", Stream: true})
	return router
}

func subscribeIdentity(t *testing.T, ctx context.Context, conn *websocket.Conn, binary bool, id uint64) {
	t.Helper()
	mode, request := websocket.MessageText, []byte(fmt.Sprintf(`{"$id":%d,"$sub":"watchIdentity"}`, id))
	ack := []byte(fmt.Sprintf(`{"$sub":%d,"ok":true}`, id))
	if binary {
		mode = websocket.MessageBinary
		request = append(codec.AppendVarint([]byte{0x10}, id), 71, 0, 0)
		ack = codec.AppendVarint([]byte{0x13}, id)
	}
	writeWSFrame(t, ctx, conn, mode, request)
	requireWSFrame(t, ctx, conn, mode, ack)
}

func unsubscribeIdentity(t *testing.T, ctx context.Context, conn *websocket.Conn, binary bool, id uint64) {
	t.Helper()
	mode, request := websocket.MessageText, []byte(fmt.Sprintf(`{"$unsub":%d}`, id))
	if binary {
		mode, request = websocket.MessageBinary, codec.AppendVarint([]byte{0x11}, id)
	}
	writeWSFrame(t, ctx, conn, mode, request)
}

func readIdentityData(t *testing.T, ctx context.Context, conn *websocket.Conn, router *Router, binary bool, id uint64) {
	t.Helper()
	mode, payload := websocket.MessageText, []byte("1")
	want := []byte(fmt.Sprintf(`{"$stream":%d,"data":1}`, id))
	if binary {
		mode, payload = websocket.MessageBinary, []byte{2}
		want = append(codec.AppendVarint([]byte{0x12}, id), payload...)
	}
	router.Streams.DispatchEncoded("watchIdentity", payload, nil)
	requireWSFrame(t, ctx, conn, mode, want)
}

func TestWSSubscriptionIdentityPreservesUInt64(t *testing.T) {
	for _, binary := range []bool{false, true} {
		for _, id := range []uint64{0, 1, 1<<53 - 1, 1 << 53, math.MaxInt64, math.MaxUint64} {
			t.Run(fmt.Sprintf("binary=%t/%d", binary, id), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					router := subscriptionIdentityRouter()
					ctx, conn := newInMemoryWebSocket(t, router)
					subscribeIdentity(t, ctx, conn, binary, id)
					readIdentityData(t, ctx, conn, router, binary, id)
					unsubscribeIdentity(t, ctx, conn, binary, id)
					synctest.Wait()
					if router.Streams.SubCount("watchIdentity") != 0 {
						t.Fatal("unsubscribe did not match the full UInt64 ID")
					}
				})
			})
		}
	}
}

func TestWSLateUnsubscribeCannotRemoveNewSubscription(t *testing.T) {
	for _, binary := range []bool{false, true} {
		t.Run(strconv.FormatBool(binary), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				router := subscriptionIdentityRouter()
				ctx, conn := newInMemoryWebSocket(t, router)
				subscribeIdentity(t, ctx, conn, binary, 1)
				unsubscribeIdentity(t, ctx, conn, binary, 1)
				subscribeIdentity(t, ctx, conn, binary, 2)
				unsubscribeIdentity(t, ctx, conn, binary, 1)
				synctest.Wait()
				if router.Streams.SubCount("watchIdentity") != 1 {
					t.Fatal("late unsubscribe removed a newer subscription")
				}
				readIdentityData(t, ctx, conn, router, binary, 2)
			})
		})
	}
}

func TestWSConcurrentSubscriptionIdentities(t *testing.T) {
	for _, binary := range []bool{false, true} {
		t.Run(strconv.FormatBool(binary), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				router := subscriptionIdentityRouter()
				ctx, conn := newInMemoryWebSocket(t, router)
				subscribeIdentity(t, ctx, conn, binary, 1)
				subscribeIdentity(t, ctx, conn, binary, 1<<63)
				if router.Streams.SubCount("watchIdentity") != 2 {
					t.Fatal("distinct IDs must allow concurrent instances of the same API")
				}
				unsubscribeIdentity(t, ctx, conn, binary, 1)
				synctest.Wait()
				readIdentityData(t, ctx, conn, router, binary, 1<<63)
			})
		})
	}
}

func TestWSJSONSubscriptionRejectsInvalidIdentity(t *testing.T) {
	for _, id := range []string{"null", "true", `"1"`, "-1", "-0", "1.0", "1e0", "18446744073709551616", "[]", "{}"} {
		t.Run(id, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				router := subscriptionIdentityRouter()
				ctx, conn := newInMemoryWebSocket(t, router)
				writeWSFrame(t, ctx, conn, websocket.MessageText, fmt.Appendf(nil, `{"$id":%s,"$sub":"watchIdentity"}`, id))
				// A rejected ID cannot reserve zero/one, dispatch, or forge an ACK.
				subscribeIdentity(t, ctx, conn, false, 1)
				if router.Streams.SubCount("watchIdentity") != 1 {
					t.Fatal("invalid identity reached the stream hub")
				}
			})
		})
	}
}

func requireSubscriptionError(t *testing.T, ctx context.Context, conn *websocket.Conn, binary bool, id uint64, code int) {
	t.Helper()
	kind, frame, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if binary {
		gotID, n := codec.ReadVarint(frame, 1)
		if kind != websocket.MessageBinary || len(frame) == 0 || frame[0] != 0x14 || n <= 0 || gotID != id {
			t.Fatalf("unexpected error identity: %x", frame)
		}
		wireErr, err := DecodeBinaryError(frame[n+1:], 0)
		if err != nil || wireErr.Code != code {
			t.Fatalf("error = %v, decode = %v", wireErr, err)
		}
		return
	}
	var envelope struct {
		ID   uint64 `json:"$sub"`
		Code int    `json:"code"`
	}
	if err := json.Unmarshal(frame, &envelope); err != nil || kind != websocket.MessageText || envelope.ID != id || envelope.Code != code {
		t.Fatalf("unexpected error envelope: %s (%v)", frame, err)
	}
}

func TestWSSubscriptionFailedAttemptConsumesIdentity(t *testing.T) {
	for _, binary := range []bool{false, true} {
		t.Run(strconv.FormatBool(binary), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				router := subscriptionIdentityRouter()
				ctx, conn := newInMemoryWebSocket(t, router)
				kind, body := websocket.MessageText, []byte(`{"$id":1,"$sub":"getUser"}`)
				if binary {
					kind, body = websocket.MessageBinary, []byte{0x10, 1, 10, 0, 0}
				}
				writeWSFrame(t, ctx, conn, kind, body)
				requireSubscriptionError(t, ctx, conn, binary, 1, http.StatusBadRequest)
				writeWSFrame(t, ctx, conn, kind, body)
				requireSubscriptionError(t, ctx, conn, binary, 1, http.StatusConflict)
				subscribeIdentity(t, ctx, conn, binary, 2)
			})
		})
	}
}

func TestWSJSONSubscriptionRejectsAmbiguousControl(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router := subscriptionIdentityRouter()
		ctx, conn := newInMemoryWebSocket(t, router)
		subscribeIdentity(t, ctx, conn, false, 1)
		writeWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$id":2,"$sub":"watchIdentity","$unsub":1}`))
		requireSubscriptionError(t, ctx, conn, false, 2, http.StatusBadRequest)
		if router.Streams.SubCount("watchIdentity") != 1 {
			t.Fatal("ambiguous control changed subscriptions")
		}
		readIdentityData(t, ctx, conn, router, false, 1)
	})
}

func TestWSRetiredStreamFramesCloseConnection(t *testing.T) {
	for _, frame := range []byte{4, 5, 6, 7, 8} {
		t.Run(strconv.Itoa(int(frame)), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, conn := newInMemoryWebSocket(t, subscriptionIdentityRouter())
				writeWSFrame(t, ctx, conn, websocket.MessageBinary, []byte{frame, 71})
				_, _, err := conn.Read(ctx)
				if websocket.CloseStatus(err) != websocket.StatusProtocolError {
					t.Fatalf("retired wire must fail explicitly: %v", err)
				}
			})
		})
	}
}

func TestWSJSONSubscriptionControlErrors(t *testing.T) {
	for _, apiName := range []string{`""`, "false", "null", "123", "[]", "{}", `"notStream"`, `"unregistered"`} {
		t.Run(apiName, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				router := subscriptionIdentityRouter()
				router.Schema.RegisterAPI(&schema.API{ID: 72, Name: "notStream", ReturnType: "Int"})
				router.Registry.Register("notStream", 72)
				ctx, conn := newInMemoryWebSocket(t, router)
				writeWSFrame(t, ctx, conn, websocket.MessageText, fmt.Appendf(nil, `{"$id":1,"$sub":%s}`, apiName))
				requireSubscriptionError(t, ctx, conn, false, 1, http.StatusBadRequest)
				subscribeIdentity(t, ctx, conn, false, 2)
			})
		})
	}
}

func TestWSJSONSubscriptionAcceptsEscapedAPIName(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router := subscriptionIdentityRouter()
		ctx, conn := newInMemoryWebSocket(t, router)
		writeWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$id":1,"$sub":"\u0077atchIdentity"}`))
		requireWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$sub":1,"ok":true}`))
		readIdentityData(t, ctx, conn, router, false, 1)
	})
}

func TestWSInvalidControlsDoNotCancelSubscription(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router := subscriptionIdentityRouter()
		ctx, conn := newInMemoryWebSocket(t, router)
		subscribeIdentity(t, ctx, conn, false, 1)
		for _, frame := range []string{
			`{"$id":2,"$sub":`, `{"$sub":"watchIdentity"}`,
			`{"$unsub":"watchIdentity"}`, `{"$unsub":true}`, `{"$unsub":1,"extra":0}`,
		} {
			writeWSFrame(t, ctx, conn, websocket.MessageText, []byte(frame))
		}
		synctest.Wait()
		if router.Streams.SubCount("watchIdentity") != 1 {
			t.Fatal("invalid control changed subscriptions")
		}
		readIdentityData(t, ctx, conn, router, false, 1)
	})
}

func TestWSBinaryInvalidAPICorrelatesSubscriptionError(t *testing.T) {
	for _, apiID := range [][]byte{nil, {128}, codec.AppendVarint(nil, math.MaxUint64)} {
		t.Run(fmt.Sprintf("%x", apiID), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, conn := newInMemoryWebSocket(t, subscriptionIdentityRouter())
				body := append(codec.AppendVarint([]byte{0x10}, math.MaxUint64), apiID...)
				writeWSFrame(t, ctx, conn, websocket.MessageBinary, body)
				code := http.StatusBadRequest
				if len(apiID) == 10 {
					code = http.StatusNotFound
				}
				requireSubscriptionError(t, ctx, conn, true, math.MaxUint64, code)
			})
		})
	}
}

func TestWSSubscriptionLimitCorrelatesIdentity(t *testing.T) {
	for _, binary := range []bool{false, true} {
		t.Run(strconv.FormatBool(binary), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				router := subscriptionIdentityRouter()
				router.Streams.subs["watchIdentity"] = make([]*StreamSub, MaxSubscribersPerAPI)
				ctx, conn := newInMemoryWebSocket(t, router)
				kind, body := websocket.MessageText, []byte(`{"$id":18446744073709551615,"$sub":"watchIdentity"}`)
				if binary {
					kind, body = websocket.MessageBinary, append(codec.AppendVarint([]byte{0x10}, math.MaxUint64), 71, 0, 0)
				}
				writeWSFrame(t, ctx, conn, kind, body)
				requireSubscriptionError(t, ctx, conn, binary, math.MaxUint64, http.StatusServiceUnavailable)
			})
		})
	}
}
