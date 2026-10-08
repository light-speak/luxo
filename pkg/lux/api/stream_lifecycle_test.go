package api

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestNativeStreamPanicFinishesOnlyItsSubscription(t *testing.T) {
	for _, exit := range []struct {
		name string
		run  func()
	}{
		{"panic", func() { panic("private-native-details") }},
		{"nil", func() { panic(nil) }},
		{"goexit", runtime.Goexit},
	} {
		t.Run(exit.name, func(t *testing.T) { testNativeStreamAbnormalExit(t, exit.run) })
	}
}

func testNativeStreamAbnormalExit(t *testing.T, exit func()) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			router := NewRouter()
			produce := func(_ context.Context, stream *Stream) error {
				if err := stream.Send([]byte{42}); err != nil {
					return err
				}
				exit()
				return nil
			}
			if prepared {
				router.HandleStreamPrepared("watch", func(context.Context, *Request, any) (PreparedStreamHandler, error) {
					return produce, nil
				})
			} else {
				router.HandleStreamNative("watch", func(ctx context.Context, _ *StreamParams, _ any, stream *Stream) error {
					return produce(ctx, stream)
				})
			}
			unrelated := router.Streams.Subscribe("other", nil, nil, nil, nil)
			defer router.Streams.Unsubscribe("other", unrelated)
			stream, err := router.OpenInternalStream(t.Context(), &Request{API: "watch"})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			synctest.Wait()
			value, open := <-stream.Messages()
			if !open || len(value) != 1 || value[0] != 42 {
				t.Fatalf("accepted data lost: %v %t", value, open)
			}
			if _, open := <-stream.Messages(); open || stream.Err() != luxerrors.InternalError {
				t.Fatalf("panic did not finish stream: open=%t error=%v", open, stream.Err())
			}
			if router.Streams.SubCount("watch") != 0 || router.Streams.SubCount("other") != 1 {
				t.Fatal("panic leaked or removed another subscription")
			}
		})
	}
}

func TestStreamCancellationStopsWriterBeforeQueueClose(t *testing.T) {
	for _, slow := range []bool{false, true} {
		hub := NewStreamHub()
		sub, producer, writer := hub.subscribeWS(t.Context(), "watch", nil, nil, nil, false)
		cancelProducer := sub.cancel
		observed := false
		sub.cancel = func() {
			observed = true
			// close has published the terminal result and closed Ch, but the
			// producer callback can pause before Unsubscribe resumes. A writer
			// must already be cancelled so this window cannot emit an end frame.
			if writer.Err() != context.Canceled {
				t.Errorf("slow=%t: closed queue exposed a live writer", slow)
			}
			cancelProducer()
		}
		if slow {
			for range cap(sub.Ch) + 1 {
				hub.DispatchEncoded("watch", nil, nil)
			}
		} else {
			hub.Unsubscribe("watch", sub)
		}
		if !observed || producer.Err() == nil || hub.SubCount("watch") != 0 {
			t.Fatal("cancellation did not complete")
		}
	}
}

func TestStreamNormalFinishLeavesWriterToDrain(t *testing.T) {
	hub := NewStreamHub()
	sub, producer, writer := hub.subscribeWS(t.Context(), "watch", nil, nil, nil, false)
	defer sub.stopWriting()
	hub.DispatchEncoded("watch", []byte("1"), nil)
	hub.finish("watch", sub, nil)
	if producer.Err() == nil || writer.Err() != nil || sub.result != nil {
		t.Fatal("normal completion cancelled the draining writer")
	}
	if string(<-sub.Ch) != "1" {
		t.Fatal("accepted data lost during normal completion")
	}
	if _, open := <-sub.Ch; open {
		t.Fatal("normal completion did not close the producer queue")
	}
}

func TestInternalStreamCloseBeforeNativeSend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router := NewRouter()
		release := make(chan struct{})
		result := make(chan error, 1)
		router.HandleStreamNative("watch", func(_ context.Context, _ *StreamParams, _ any, stream *Stream) error {
			<-release
			err, panicked := sendAfterRemoval(stream)
			if panicked {
				t.Error("native handler panicked after its internal subscription closed")
			}
			result <- err
			return err
		})
		stream, err := router.OpenInternalStream(t.Context(), &Request{API: "watch"})
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		synctest.Wait()
		stream.Close()
		close(release)
		synctest.Wait()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("closed native stream send = %v", err)
		}
	})
}

func TestInternalNativeStreamResultAndAcceptedQueue(t *testing.T) {
	want := errors.New("native failed")
	for _, result := range []error{nil, want} {
		synctest.Test(t, func(t *testing.T) {
			router := NewRouter()
			var producer context.Context
			router.HandleStreamNative("watch", func(ctx context.Context, _ *StreamParams, _ any, stream *Stream) error {
				producer = ctx
				for i := range 64 {
					if err := stream.Send([]byte{byte(i)}); err != nil {
						t.Errorf("send: %v", err)
					}
				}
				return result
			})
			stream, err := router.OpenInternalStream(t.Context(), &Request{API: "watch"})
			if err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			count := 0
			for value := range stream.Messages() {
				if len(value) != 1 || value[0] != byte(count) {
					t.Fatalf("message %d: %v", count, value)
				}
				count++
			}
			stream.Close()
			if count != 64 || stream.Err() != result || producer.Err() == nil || router.Streams.SubCount("watch") != 0 {
				t.Fatalf("count=%d result=%v producer=%v", count, stream.Err(), producer.Err())
			}
		})
	}
}

func TestInternalStreamCancellationWinsLateNativeReturn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router := NewRouter()
		router.HandleStreamNative("watch", func(ctx context.Context, _ *StreamParams, _ any, _ *Stream) error {
			<-ctx.Done()
			return errors.New("late failure")
		})
		stream, err := router.OpenInternalStream(t.Context(), &Request{API: "watch"})
		if err != nil {
			t.Fatal(err)
		}
		if stream.Err() != nil {
			t.Fatal("active stream reported a terminal error")
		}
		stream.Close()
		synctest.Wait()
		if !errors.Is(stream.Err(), context.Canceled) {
			t.Fatalf("cancellation was overwritten: %v", stream.Err())
		}
	})
}

func TestInternalStreamParentCancellationIsNotNormalCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router := NewRouter()
		router.HandleStreamNative("watch", func(ctx context.Context, _ *StreamParams, _ any, _ *Stream) error {
			<-ctx.Done()
			return nil
		})
		ctx, cancel := context.WithCancel(t.Context())
		stream, err := router.OpenInternalStream(ctx, &Request{API: "watch"})
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		synctest.Wait()
		for range stream.Messages() {
			t.Fatal("cancelled handler produced data")
		}
		if !errors.Is(stream.Err(), context.Canceled) {
			t.Fatalf("cancellation became successful completion: %v", stream.Err())
		}
	})
}

func TestInternalStreamSlowConsumerIsNotSuccessfulCompletion(t *testing.T) {
	router := NewRouter()
	router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "watch", Stream: true})
	stream, err := router.OpenInternalStream(t.Context(), &Request{API: "watch"})
	if err != nil {
		t.Fatal(err)
	}
	for range 65 {
		router.Streams.DispatchEncoded("watch", nil, nil)
	}
	stream.Close()
	if !errors.Is(stream.Err(), ErrStreamFull) {
		t.Fatalf("slow consumer result: %v", stream.Err())
	}
}

func TestNativeStreamSendAfterRemoval(t *testing.T) {
	for _, slow := range []bool{false, true} {
		hub := NewStreamHub()
		var cancelled atomic.Int32
		sub := hub.Subscribe("watch", nil, nil, nil, func() { cancelled.Add(1) })
		stream := &Stream{sub: sub, ctx: context.Background()}
		if slow {
			for range cap(sub.Ch) {
				if err := stream.Send(nil); err != nil {
					t.Fatal(err)
				}
			}
			hub.DispatchEncoded("watch", nil, nil)
		} else {
			hub.Unsubscribe("watch", sub)
		}
		if err, panicked := sendAfterRemoval(stream); panicked || !errors.Is(err, context.Canceled) {
			t.Fatalf("slow=%t: send after removal = %v, panic=%t", slow, err, panicked)
		}
		hub.Unsubscribe("watch", sub)
		if cancelled.Load() != 1 || hub.SubCount("watch") != 0 {
			t.Fatalf("cancelled=%d, subscribers=%d", cancelled.Load(), hub.SubCount("watch"))
		}
	}
}

// Recover only in the regression probe so the old panic becomes a test failure.
func sendAfterRemoval(stream *Stream) (err error, panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	err = stream.Send(nil)
	return
}

func TestNativeStreamConcurrentRemoval(t *testing.T) {
	for attempt := range 1000 {
		hub := NewStreamHub()
		ctx, cancel := context.WithCancel(context.Background())
		sub := hub.Subscribe("watch", nil, nil, nil, cancel)
		stream := &Stream{sub: sub, ctx: ctx}
		var workers sync.WaitGroup
		start := make(chan struct{})
		for range 4 {
			workers.Go(func() {
				<-start
				for range 8 {
					if err, panicked := sendAfterRemoval(stream); panicked || (err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrStreamFull)) {
						t.Errorf("concurrent send = %v, panic=%t", err, panicked)
					}
				}
			})
		}
		workers.Go(func() {
			<-start
			if attempt%2 == 0 {
				hub.Unsubscribe("watch", sub)
				return
			}
			for range cap(sub.Ch) + 1 {
				hub.DispatchEncoded("watch", nil, nil)
			}
		})
		workers.Go(func() { <-start; hub.DispatchEncoded("watch", nil, nil) })
		close(start)
		workers.Wait()
		hub.Unsubscribe("watch", sub)
		if hub.SubCount("watch") != 0 || ctx.Err() == nil {
			t.Fatal("removed stream remained active")
		}
	}
}

func TestStreamUnsubscribeReleasesStoredReferences(t *testing.T) {
	hub := NewStreamHub()
	first := hub.Subscribe("watch", nil, nil, nil, nil)
	middle := hub.Subscribe("watch", nil, nil, nil, nil)
	last := hub.Subscribe("watch", nil, nil, nil, nil)
	stored := hub.subs["watch"]
	hub.Unsubscribe("watch", middle)
	if len(hub.subs["watch"]) != 2 || stored[0] != first || stored[1] != last || stored[2] != nil {
		t.Fatal("removed subscription left a reference in the unused slice tail")
	}
	hub.Unsubscribe("watch", first)
	hub.Unsubscribe("watch", last)
	if _, retained := hub.subs["watch"]; retained {
		t.Fatal("empty API subscription entry was retained")
	}
}

func TestNativeStreamCancelledContextDoesNotQueue(t *testing.T) {
	hub := NewStreamHub()
	ctx, cancel := context.WithCancel(context.Background())
	sub := hub.Subscribe("watch", nil, nil, nil, cancel)
	defer hub.Unsubscribe("watch", sub)
	stream := &Stream{sub: sub, ctx: ctx}
	cancel()
	for range 100 {
		if err := stream.Send(nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled send = %v", err)
		}
	}
	if len(sub.Ch) != 0 {
		t.Fatal("cancelled stream queued a new message")
	}
}

func TestNativeStreamSendDoesNotAllocate(t *testing.T) {
	hub := NewStreamHub()
	ctx, cancel := context.WithCancel(context.Background())
	sub := hub.Subscribe("watch", nil, nil, nil, cancel)
	defer hub.Unsubscribe("watch", sub)
	stream := &Stream{sub: sub, ctx: ctx}
	payload := []byte("message")
	if allocs := testing.AllocsPerRun(100, func() {
		if err := stream.Send(payload); err != nil {
			t.Fatal(err)
		}
		<-sub.Ch
	}); allocs != 0 {
		t.Fatalf("native send allocated: %g", allocs)
	}
}
