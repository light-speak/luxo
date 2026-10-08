package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"

	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

type eventTestParams struct{ limit int64 }

func typedEventTestRouter(t *testing.T) (*Router, *EventStream[eventTestParams]) {
	t.Helper()
	router := NewRouter()
	router.Schema.RegisterAPI(&schema.API{ID: 61, Name: "watch", Stream: true, ReturnType: "Int", Params: []schema.Param{
		{ID: 1, Name: "state", Type: schema.FieldString},
		{ID: 2, Name: "limit", Type: schema.FieldInt, HasDefault: true},
	}})
	router.Registry.RegisterSchemaAPIs(router.Schema)
	topic := RegisterEventStream(router, "watch", func(_ context.Context, req *Request, _ any) (eventTestParams, error) {
		if req.Params != nil && req.BinaryRequest() != nil {
			t.Error("event JSON input converted to Binary")
		}
		state, err := req.ParamString("state")
		if err != nil {
			return eventTestParams{}, err
		}
		if state != "OPEN" {
			return eventTestParams{}, luxerrors.BadRequest
		}
		limit := int64(7)
		if req.HasParam("limit") {
			limit, err = req.ParamInt("limit")
		}
		return eventTestParams{limit: limit}, err
	})
	if err := router.Validate(); err != nil {
		t.Fatal(err)
	}
	return router, topic
}

func TestTypedEventStreamWebSocketPreparation(t *testing.T) {
	for _, binary := range []bool{false, true} {
		for _, invalid := range []bool{false, true} {
			t.Run(fmt.Sprintf("binary=%t/invalid=%t", binary, invalid), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					router, topic := typedEventTestRouter(t)
					ctx, conn := newInMemoryWebSocket(t, router)
					params := `"state":"OPEN"`
					if invalid {
						params = `"state":"UNKNOWN"`
					}
					mode, input := preparedSubscribeFrame(t, router, params, binary)
					writeWSFrame(t, ctx, conn, mode, input)
					if invalid {
						requirePreparedSubscriptionError(t, ctx, conn, binary, 400)
						if len(topic.subs) != 0 {
							t.Fatal("rejected snapshot retained")
						}
						return
					}
					ack, data := []byte(`{"$sub":1,"ok":true}`), []byte(`{"$stream":1,"data":7}`)
					if binary {
						ack, data = []byte{BinaryFrameSubscribeSuccess, 1}, []byte{BinaryFrameStream, 1, 14}
					}
					requireWSFrame(t, ctx, conn, mode, ack)
					if err := topic.Dispatch(func(params *eventTestParams, _ any) bool { return params.limit == 7 }, func(_ []byte, mode bool) ([]byte, error) {
						if mode {
							return []byte{14}, nil
						}
						return []byte("7"), nil
					}); err != nil {
						t.Fatal(err)
					}
					requireWSFrame(t, ctx, conn, mode, data)
				})
			})
		}
	}
}

func openTypedTestStream(t *testing.T, router *Router) *InternalStream {
	t.Helper()
	req := GetRequest()
	req.API = "watch"
	req.paramNames, req.paramCount = []string{"state", "limit"}, 2
	req.SetParamSlot(0, "OPEN")
	stream, err := router.OpenInternalStream(t.Context(), req)
	PutRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stream.Close)
	return stream
}

func TestTypedEventStreamRemovalAndEncoding(t *testing.T) {
	router, topic := typedEventTestRouter(t)
	first, second, third := openTypedTestStream(t, router), openTypedTestStream(t, router), openTypedTestStream(t, router)
	firstEntry := topic.subs[0]
	first.Close()
	if len(topic.subs) != 2 || firstEntry.sub != nil || firstEntry.params.limit != 0 {
		t.Fatal("removed entry retained snapshot")
	}
	third.Close()
	if len(topic.subs) != 1 || topic.subs[0].sub != second.sub {
		t.Fatal("swapped index not updated")
	}
	calls := 0
	encode := func([]byte, bool) ([]byte, error) { calls++; return []byte{14}, nil }
	if err := topic.Dispatch(func(*eventTestParams, any) bool { return false }, encode); err != nil || calls != 0 {
		t.Fatal("unmatched event encoded")
	}
	for range cap(second.sub.Ch) + 1 {
		if err := topic.Dispatch(nil, encode); err != nil {
			t.Fatal(err)
		}
	}
	if !errors.Is(second.Err(), ErrStreamFull) || len(topic.subs) != 0 || router.Streams.SubCount("watch") != 0 {
		t.Fatal("slow subscriber retained")
	}
	if err := topic.Dispatch(nil, encode); err != nil || calls != 65 {
		t.Fatal("empty topic encoded")
	}
	_ = openTypedTestStream(t, router)
	failure := errors.New("encode failed")
	if err := topic.Dispatch(nil, func([]byte, bool) ([]byte, error) { return nil, failure }); err != failure {
		t.Fatal(err)
	}
}

func TestTypedEventStreamConcurrentRemoval(t *testing.T) {
	router, topic := typedEventTestRouter(t)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 100 {
				stream := openTypedTestStream(t, router)
				if err := topic.Dispatch(nil, func([]byte, bool) ([]byte, error) { return []byte{14}, nil }); err != nil {
					t.Error(err)
				}
				stream.Close()
			}
		})
	}
	workers.Wait()
	if len(topic.subs) != 0 || router.Streams.SubCount("watch") != 0 {
		t.Fatal("concurrent removal retained entries")
	}
}

func TestTypedEventStreamPreparationFailures(t *testing.T) {
	router, topic := typedEventTestRouter(t)
	request := &Request{API: "watch", paramNames: []string{"state"}, paramCount: 1}
	if stream, err := router.PrepareInternalStream(t.Context(), request); err == nil || stream != nil {
		t.Fatal("missing parameter accepted")
	}
	request.SetParamSlot(0, "OPEN")
	for _, binary := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		var subscriptions connectionSubscriptions
		router.startWSSubscription(ctx, &wsConn{ctx: t.Context()}, &subscriptions, 1, request, nil, binary)
		if len(topic.subs) != 0 || router.Streams.SubCount("watch") != 0 || len(subscriptions.items) != 0 {
			t.Fatal("ACK failure retained snapshot")
		}
	}
	router.Streams.subs["watch"] = make([]*StreamSub, MaxSubscribersPerAPI)
	if stream, err := router.PrepareInternalStream(t.Context(), request); err == nil || stream != nil || len(topic.subs) != 0 {
		t.Fatal("capacity rejection retained snapshot")
	}
	delete(router.Streams.subs, "watch")
	RegisterEventStream[eventTestParams](router, "watch", nil)
	if router.Validate() == nil {
		t.Fatal("nil event factory accepted")
	}
	missing := NewRouter()
	RegisterEventStream(missing, "watch", func(context.Context, *Request, any) (eventTestParams, error) { return eventTestParams{}, nil })
	if missing.Validate() == nil {
		t.Fatal("missing schema accepted")
	}
}

func TestTypedEventStreamProjectionAndCleanup(t *testing.T) {
	router, topic := typedEventTestRouter(t)
	streams := []*InternalStream{openTypedTestStream(t, router), openTypedTestStream(t, router), openTypedTestStream(t, router)}
	streams[2].sub.Binary = false
	calls := 0
	encode := func([]byte, bool) ([]byte, error) { calls++; return []byte{14}, nil }
	if err := topic.Dispatch(nil, encode); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("encodings=%d, want two transport modes", calls)
	}
	for _, stream := range streams {
		if stream.sub.Params.values != nil || stream.sub.Params.Binary() != nil {
			t.Fatal("typed event retained dynamic inputs")
		}
		<-stream.Messages()
	}
	for range cap(streams[0].sub.Ch) {
		streams[0].sub.Ch <- []byte{14}
	}
	failure := errors.New("JSON encoding failed")
	if err := topic.Dispatch(nil, func(_ []byte, binary bool) ([]byte, error) {
		if !binary {
			return nil, failure
		}
		return []byte{14}, nil
	}); err != failure {
		t.Fatal(err)
	}
	if !errors.Is(streams[0].Err(), ErrStreamFull) || len(topic.subs) != 2 {
		t.Fatal("encoding error skipped slow consumer cleanup")
	}
}

func TestEventDispatchPanicReleasesHub(t *testing.T) {
	for _, kind := range []string{"typed-match", "typed-encode", "prepared-match", "prepared-encode", "encoded-match"} {
		t.Run(kind, func(t *testing.T) {
			router, topic := typedEventTestRouter(t)
			sub := router.Streams.subscribePreparedMode("watch", nil, nil, nil, true, nil, nil, preparedStream{bind: func(sub *StreamSub) { topic.attach(sub, eventTestParams{}) }})
			encode := func([]byte, bool) ([]byte, error) {
				if kind == "typed-encode" || kind == "prepared-encode" {
					panic("native encoder")
				}
				return nil, nil
			}
			panicked := func() (panicked bool) {
				defer func() { panicked = recover() != nil }()
				switch kind {
				case "typed-match", "typed-encode":
					_ = topic.Dispatch(func(*eventTestParams, any) bool {
						if kind == "typed-match" {
							panic("native matcher")
						}
						return true
					}, encode)
				case "prepared-match", "prepared-encode":
					_ = router.Streams.DispatchPreparedEvent("watch", func(*StreamParams, any) bool {
						if kind == "prepared-match" {
							panic("native matcher")
						}
						return true
					}, encode)
				default:
					router.Streams.DispatchEncoded("watch", nil, func([]byte, *StreamParams, any) bool { panic("native matcher") })
				}
				return false
			}()
			if !panicked {
				t.Fatal("fixture did not execute native panic")
			}
			if !router.Streams.mu.TryLock() {
				t.Fatal("callback panic leaked the hub read lock")
			}
			router.Streams.mu.Unlock()
			router.Streams.Unsubscribe("watch", sub)
			if len(topic.subs) != 0 {
				t.Fatal("snapshot retained after panic and cancellation")
			}
		})
	}
}
