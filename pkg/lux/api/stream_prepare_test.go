package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"nhooyr.io/websocket"
)

func preparedInputRouter(t *testing.T, authorized bool) *Router {
	t.Helper()
	router := NewRouter()
	router.Schema.RegisterAPI(&schema.API{ID: 61, Name: "watch", Stream: true, ReturnType: "Int", Params: []schema.Param{
		{ID: 1, Name: "state", Type: schema.FieldString},
		{ID: 2, Name: "limit", Type: schema.FieldInt, HasDefault: true},
	}})
	router.Registry.RegisterSchemaAPIs(router.Schema)
	router.HandleStreamPrepared("watch", func(_ context.Context, req *Request, _ any) (PreparedStreamHandler, error) {
		if !authorized {
			return nil, luxerrors.Unauthorized
		}
		if req.Params != nil && req.BinaryRequest() != nil {
			t.Error("local JSON subscription was encoded to Binary")
		}
		state, err := req.ParamString("state")
		if err != nil {
			return nil, err
		}
		if state != "OPEN" && state != "CLOSED" {
			return nil, luxerrors.BadRequest
		}
		limit := int64(7)
		if req.HasParam("limit") {
			limit, err = req.ParamInt("limit")
			if err != nil {
				return nil, err
			}
		}
		return func(_ context.Context, stream *Stream) error {
			if stream.Binary() {
				return stream.Send(codec.AppendVarint(nil, uint64(limit<<1)^uint64(limit>>63)))
			}
			return stream.Send([]byte(fmt.Sprint(limit)))
		}, nil
	})
	return router
}

func TestForwardedStreamCanceledBeforePreparation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false
	prepared, err := prepareForwardedStream(ctx, &Request{API: "watch"}, nil,
		func(context.Context, *Request, any) (PreparedStreamSource, error) {
			called = true
			return PreparedStreamSource{}, nil
		})
	if err != context.Canceled || called || prepared.valid() || prepared.release != nil {
		t.Fatalf("canceled subscription acquired a source: called=%v, prepared=%+v, err=%v", called, prepared, err)
	}
}

func TestPreparedStreamRejectsMissingRequest(t *testing.T) {
	router := NewRouter()
	stream, err := router.PrepareInternalStream(t.Context(), nil)
	applicationError, ok := err.(*luxerrors.AppError)
	if stream != nil || !ok || applicationError.Name != "BadRequest" || applicationError.Code != 400 || applicationError.Message != `api: "" is not a stream` {
		t.Fatalf("missing request was not rejected explicitly: stream=%v error=%v", stream, err)
	}
	if len(router.Streams.subs) != 0 {
		t.Fatal("missing request registered a subscription")
	}
}

func TestPreparedStreamChecksBeforeWebSocketACK(t *testing.T) {
	for _, binary := range []bool{false, true} {
		for _, sample := range []struct {
			name, params string
			authorized   bool
			want         int64
			code         int
		}{
			{"default", `"state":"OPEN"`, true, 7, 0},
			{"explicit", `"state":"OPEN","limit":9`, true, 9, 0},
			{"unauthorized", `"state":"OPEN"`, false, 0, 401},
			{"unknown", `"state":"UNKNOWN"`, true, 0, 400},
		} {
			t.Run(fmt.Sprintf("%s/binary=%t", sample.name, binary), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					router := preparedInputRouter(t, sample.authorized)
					ctx, conn := newInMemoryWebSocket(t, router)
					mode, input := preparedSubscribeFrame(t, router, sample.params, binary)
					writeWSFrame(t, ctx, conn, mode, input)
					if sample.code != 0 {
						requirePreparedSubscriptionError(t, ctx, conn, binary, sample.code)
						if router.Streams.SubCount("watch") != 0 {
							t.Fatal("rejected preparation registered a subscription")
						}
						return
					}
					ack := []byte(`{"$sub":1,"ok":true}`)
					data := []byte(fmt.Sprintf(`{"$stream":1,"data":%d}`, sample.want))
					if binary {
						ack = []byte{BinaryFrameSubscribeSuccess, 1}
						data = codec.AppendVarint([]byte{BinaryFrameStream, 1}, uint64(sample.want<<1)^uint64(sample.want>>63))
					}
					requireWSFrame(t, ctx, conn, mode, ack)
					requireWSFrame(t, ctx, conn, mode, data)
				})
			})
		}
	}
}

func preparedSubscribeFrame(t *testing.T, router *Router, params string, binary bool) (websocket.MessageType, []byte) {
	t.Helper()
	if !binary {
		return websocket.MessageText, []byte(`{"$id":1,"$sub":"watch",` + params + `}`)
	}
	req, err := ParseRequest(httptest.NewRequest("POST", "/luvia", strings.NewReader(`{"$api":"watch",`+params+`}`)))
	if err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.prepareJSONRequest(req); err != nil {
		t.Fatal(err)
	}
	return websocket.MessageBinary, append([]byte{BinaryFrameSubscribe, 1}, req.BinaryRequest()...)
}

func TestPreparedStreamOwnsSnapshotAndSelection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router := preparedInputRouter(t, true)
		req := &Request{API: "watch", Params: map[string]json.RawMessage{"state": []byte(`"OPEN"`)}, FieldMask: []byte{1, 2}}
		stream, err := router.OpenInternalStream(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		clear(req.FieldMask)
		clear(req.Params["state"])
		PutRequest(req)
		if stream.sub.Params.values != nil || stream.sub.Params.Binary() != nil {
			t.Fatal("prepared stream retained a generic parameter map or Binary copy")
		}
		if len(stream.sub.FieldMask) != 2 || stream.sub.FieldMask[0] != 1 || stream.sub.FieldMask[1] != 2 {
			t.Fatal("subscription selection aliases the released request")
		}
		synctest.Wait()
		value := <-stream.Messages()
		if len(value) != 1 || value[0] != 14 || stream.Err() != nil {
			t.Fatalf("snapshot lost after releasing request: %x %v", value, stream.Err())
		}
	})
}

func TestPreparedStreamRejectsInvalidFactories(t *testing.T) {
	router := preparedInputRouter(t, true)
	if err := router.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, prepare := range []StreamPrepareFunc{
		nil,
		func(context.Context, *Request, any) (PreparedStreamHandler, error) { return nil, nil },
		func(context.Context, *Request, any) (PreparedStreamHandler, error) {
			return nil, luxerrors.Unauthorized
		},
	} {
		router.HandleStreamPrepared("watch", prepare)
		if prepare == nil && router.Validate() == nil {
			t.Fatal("nil preparation accepted at startup")
		}
		if stream, err := router.OpenInternalStream(t.Context(), &Request{API: "watch"}); err == nil || stream != nil {
			t.Fatalf("invalid preparation created a subscription: %v %v", stream, err)
		}
		if router.Streams.SubCount("watch") != 0 {
			t.Fatal("invalid preparation retained a subscriber")
		}
	}
	router.HandleStreamNative("watch", func(context.Context, *StreamParams, any, *Stream) error { return nil })
	if router.streamHandlers["watch"].prepare != nil {
		t.Fatal("explicit route replacement retained a preparation function")
	}
	missing := NewRouter()
	missing.HandleStreamPrepared("watch", func(context.Context, *Request, any) (PreparedStreamHandler, error) { return nil, nil })
	if missing.Validate() == nil {
		t.Fatal("prepared registration without static input metadata accepted")
	}
}

func TestPreparedStreamPanicRejectsSubscription(t *testing.T) {
	for _, kind := range []string{"native", "event", "forwarded"} {
		t.Run(kind, func(t *testing.T) {
			router := preparedInputRouter(t, true)
			switch kind {
			case "native":
				router.HandleStreamPrepared("watch", func(context.Context, *Request, any) (PreparedStreamHandler, error) {
					panic("private-native-details")
				})
			case "event":
				RegisterEventStream(router, "watch", func(context.Context, *Request, any) (int, error) {
					panic("private-event-details")
				})
			case "forwarded":
				router.HandleStreamForwarded("watch", func(context.Context, *Request, any) (PreparedStreamSource, error) {
					panic("private-forward-details")
				})
			}
			stream, err := router.PrepareInternalStream(t.Context(), &Request{API: "watch"})
			if stream != nil || err != luxerrors.InternalError || router.Streams.SubCount("watch") != 0 {
				t.Fatalf("panic accepted or exposed: %v %v", stream, err)
			}
		})
	}
}

func TestPreparedStreamDoesNotRunBeforeACKOrCapacity(t *testing.T) {
	for _, binary := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			router := preparedInputRouter(t, true)
			var started atomic.Int32
			router.HandleStreamPrepared("watch", func(context.Context, *Request, any) (PreparedStreamHandler, error) {
				return func(context.Context, *Stream) error { started.Add(1); return nil }, nil
			})
			request := &Request{API: "watch"}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			conn := &wsConn{ctx: t.Context()}
			var subscriptions connectionSubscriptions
			router.startWSSubscription(ctx, conn, &subscriptions, 1, request, nil, binary)
			synctest.Wait()
			if started.Load() != 0 || router.Streams.SubCount("watch") != 0 || len(subscriptions.items) != 0 {
				t.Fatal("ACK failure started or retained a prepared stream")
			}
			router.Streams.subs["watch"] = make([]*StreamSub, MaxSubscribersPerAPI)
			if stream, err := router.OpenInternalStream(t.Context(), request); err == nil || stream != nil {
				t.Fatalf("capacity exceeded: stream=%v error=%v", stream, err)
			}
			synctest.Wait()
			if started.Load() != 0 {
				t.Fatal("capacity rejection started a prepared stream")
			}
		})
	}
}

func TestPreparedStreamRejectsInvalidJSONEnvelope(t *testing.T) {
	router := preparedInputRouter(t, true)
	for _, raw := range []map[string]json.RawMessage{
		{"$sub": []byte(`"watch"`)},
		{"$sub": []byte(`"watch"`), "state": []byte(`"OPEN"`), "unexpected": []byte(`1`)},
	} {
		if _, _, err := router.prepareWSJSONSubscription(raw); err == nil {
			t.Fatal("invalid input envelope accepted")
		}
	}
}

func TestStreamRejectsUnknownSelectionBeforePreparation(t *testing.T) {
	for _, forwarded := range []bool{false, true} {
		t.Run(fmt.Sprint(forwarded), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				router := NewRouter()
				if err := router.Schema.RegisterType(&schema.TypeDecl{Name: "Snapshot", Fields: []schema.Field{{ID: 1, Name: "value", Type: schema.FieldInt}}}); err != nil {
					t.Fatal(err)
				}
				if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "watch", ReturnType: "Snapshot", Stream: true}); err != nil {
					t.Fatal(err)
				}
				if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
					t.Fatal(err)
				}
				var prepared atomic.Int32
				if forwarded {
					router.HandleStreamForwarded("watch", func(context.Context, *Request, any) (PreparedStreamSource, error) {
						prepared.Add(1)
						return PreparedStreamSource{}, nil
					})
				} else {
					router.HandleStreamPrepared("watch", func(context.Context, *Request, any) (PreparedStreamHandler, error) {
						prepared.Add(1)
						return nil, nil
					})
				}
				ctx, conn := newInMemoryWebSocket(t, router)
				writeWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$id":1,"$sub":"watch","$select":"missing"}`))
				requireSubscriptionError(t, ctx, conn, false, 1, 400)
				synctest.Wait()
				if prepared.Load() != 0 || router.Streams.SubCount("watch") != 0 {
					t.Fatal("invalid projection prepared or registered a subscription")
				}
			})
		})
	}
}

func TestForwardedStreamCapacityReleasesSourceBeforeACK(t *testing.T) {
	for _, binary := range []bool{false, true} {
		t.Run(fmt.Sprint(binary), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				router := subscriptionIdentityRouter()
				router.Streams.subs["watchIdentity"] = make([]*StreamSub, MaxSubscribersPerAPI)
				identity := new(int64(9007199254740993))
				router.IdentityExtractor = func(context.Context) any { return identity }
				var prepared, released, started atomic.Int32
				router.HandleStreamForwarded("watchIdentity", func(_ context.Context, _ *Request, got any) (PreparedStreamSource, error) {
					prepared.Add(1)
					if got != identity {
						t.Error("router identity was not passed to remote preparation")
					}
					return PreparedStreamSource{
						Run: func(context.Context, *Stream) error { started.Add(1); return nil },
						Close: func() {
							if router.Streams.SubCount("watchIdentity") != MaxSubscribersPerAPI {
								t.Error("rejection changed existing subscribers")
							}
							released.Add(1)
						},
					}, nil
				})
				ctx, conn := newInMemoryWebSocket(t, router)
				kind, body := websocket.MessageText, []byte(`{"$id":1,"$sub":"watchIdentity"}`)
				if binary {
					kind, body = websocket.MessageBinary, []byte{BinaryFrameSubscribe, 1, 71, 0, 0}
				}
				writeWSFrame(t, ctx, conn, kind, body)
				requireSubscriptionError(t, ctx, conn, binary, 1, 503)
				conn.CloseNow()
				synctest.Wait()
				if prepared.Load() != 1 || released.Load() != 1 || started.Load() != 0 {
					t.Fatalf("source lifecycle: prepared=%d released=%d started=%d", prepared.Load(), released.Load(), started.Load())
				}
			})
		})
	}
}

func TestPreparedInternalStreamStartIsDeferredAndIdempotent(t *testing.T) {
	for _, closeFirst := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			router := preparedInputRouter(t, true)
			var started atomic.Int32
			router.HandleStreamPrepared("watch", func(context.Context, *Request, any) (PreparedStreamHandler, error) {
				return func(context.Context, *Stream) error { started.Add(1); return nil }, nil
			})
			stream, err := router.PrepareInternalStream(t.Context(), &Request{API: "watch"})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			synctest.Wait()
			if started.Load() != 0 {
				t.Fatal("preparation started the handler")
			}
			if closeFirst {
				stream.Close()
			}
			for range 8 {
				go stream.Start()
			}
			synctest.Wait()
			want := int32(1)
			if closeFirst {
				want = 0
			}
			if started.Load() != want {
				t.Fatalf("handler starts=%d want=%d", started.Load(), want)
			}
		})
	}
}

func TestPreparedStreamCancelledBeforeStartTerminates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router := preparedInputRouter(t, true)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		req := &Request{API: "watch", Params: map[string]json.RawMessage{"state": []byte(`"OPEN"`)}}
		stream, err := router.OpenInternalStream(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		synctest.Wait()
		select {
		case _, open := <-stream.Messages():
			if open || stream.Err() != context.Canceled {
				t.Fatalf("cancelled subscription did not terminate: %v %v", open, stream.Err())
			}
		default:
			t.Fatal("cancelled preparation left the consumer waiting indefinitely")
		}
		if router.Streams.SubCount("watch") != 0 {
			t.Fatal("cancelled subscription retained in hub")
		}
	})
}

func TestPreparedStreamDeadlineBeforeStartPreservesResult(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router := preparedInputRouter(t, true)
		var started atomic.Int32
		router.HandleStreamPrepared("watch", func(context.Context, *Request, any) (PreparedStreamHandler, error) {
			return func(context.Context, *Stream) error { started.Add(1); return nil }, nil
		})
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		stream, err := router.PrepareInternalStream(ctx, &Request{API: "watch"})
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		<-ctx.Done()
		for range 8 {
			go stream.Start()
		}
		synctest.Wait()
		select {
		case _, open := <-stream.Messages():
			if open {
				t.Fatal("expired subscription emitted data")
			}
		default:
			t.Fatal("expired subscription did not close")
		}
		stream.Close()
		stream.Start()
		if stream.Err() != context.DeadlineExceeded || started.Load() != 0 || router.Streams.SubCount("watch") != 0 {
			t.Fatalf("deadline lost: result=%v starts=%d subscribers=%d", stream.Err(), started.Load(), router.Streams.SubCount("watch"))
		}
	})
}

func TestPreparedStreamConcurrentStartAndClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router := preparedInputRouter(t, true)
		var started atomic.Int32
		router.HandleStreamPrepared("watch", func(context.Context, *Request, any) (PreparedStreamHandler, error) {
			return func(ctx context.Context, _ *Stream) error {
				started.Add(1)
				<-ctx.Done()
				return ctx.Err()
			}, nil
		})
		for range 128 {
			stream, err := router.PrepareInternalStream(t.Context(), &Request{API: "watch"})
			if err != nil {
				t.Fatal(err)
			}
			before := started.Load()
			for range 8 {
				go stream.Start()
				go stream.Close()
			}
			synctest.Wait()
			if delta := started.Load() - before; delta > 1 {
				t.Fatalf("handler started %d times", delta)
			}
			if _, open := <-stream.Messages(); open || stream.Err() != context.Canceled {
				t.Fatalf("concurrent close did not terminate: open=%v result=%v", open, stream.Err())
			}
			if router.Streams.SubCount("watch") != 0 {
				t.Fatal("closed subscription retained in hub")
			}
		}
	})
}

func requirePreparedSubscriptionError(t *testing.T, ctx context.Context, conn *websocket.Conn, binary bool, code int) {
	t.Helper()
	_, body, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if binary {
		if len(body) < 3 || body[0] != BinaryFrameSubscribeError || body[1] != 1 {
			t.Fatalf("expected rejected ACK, received %x", body)
		}
		decoded, err := DecodeBinaryError(body[2:], 0)
		if err != nil || decoded.Code != code {
			t.Fatalf("error=%v decode=%v", decoded, err)
		}
		return
	}
	var response struct {
		OK   bool `json:"ok"`
		Code int  `json:"code"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.OK || response.Code != code {
		t.Fatalf("expected %d rejected ACK, received %s: %v", code, body, err)
	}
}
