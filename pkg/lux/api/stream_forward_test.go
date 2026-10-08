package api

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/light-speak/luxo/pkg/lux/schema"
)

func forwardedTestRouter() *Router {
	router := NewRouter()
	router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "watch", Stream: true, ReturnType: "Int"})
	router.Registry.RegisterSchemaAPIs(router.Schema)
	return router
}

func TestForwardedStreamResourceOwnership(t *testing.T) {
	for _, start := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			router := forwardedTestRouter()
			var runs, closes atomic.Int32
			router.HandleStreamForwarded("watch", func(context.Context, *Request, any) (PreparedStreamSource, error) {
				return PreparedStreamSource{Run: func(context.Context, *Stream) error { runs.Add(1); return nil }, Close: func() {
					// Re-entering the hub must not deadlock, even with concurrent cleanup.
					_ = router.Streams.SubCount("watch")
					closes.Add(1)
				}}, nil
			})
			if err := router.Validate(); err != nil {
				t.Fatal(err)
			}
			stream, err := router.PrepareInternalStream(t.Context(), &Request{API: "watch"})
			if err != nil {
				t.Fatal(err)
			}
			if runs.Load() != 0 || closes.Load() != 0 {
				t.Fatal("resource ran or closed before ACK")
			}
			if start {
				stream.Start()
				synctest.Wait()
			}
			var workers sync.WaitGroup
			for range 8 {
				workers.Go(stream.Close)
			}
			workers.Wait()
			stream.Start()
			synctest.Wait()
			if stream.run != nil {
				t.Fatal("closed subscription retained runner")
			}
			want := int32(0)
			if start {
				want = 1
			}
			if runs.Load() != want || closes.Load() != 1 || router.Streams.SubCount("watch") != 0 {
				t.Fatalf("runs=%d closes=%d", runs.Load(), closes.Load())
			}
		})
	}
}

func TestForwardedStreamPreparationFailures(t *testing.T) {
	router := forwardedTestRouter()
	failure := errors.New("remote rejection")
	for _, sample := range []struct {
		source PreparedStreamSource
		err    error
	}{
		{PreparedStreamSource{}, nil},
		{PreparedStreamSource{Run: func(context.Context, *Stream) error { return nil }}, nil},
		{PreparedStreamSource{Close: func() {}}, nil},
		{PreparedStreamSource{Close: func() {}}, failure},
	} {
		closed := 0
		source := sample.source
		if source.Close != nil {
			source.Close = func() { closed++ }
		}
		router.HandleStreamForwarded("watch", func(context.Context, *Request, any) (PreparedStreamSource, error) { return source, sample.err })
		if stream, err := router.PrepareInternalStream(t.Context(), &Request{API: "watch"}); err == nil || stream != nil {
			t.Fatal("invalid resource accepted")
		} else if sample.err != nil && !errors.Is(err, failure) {
			t.Fatal(err)
		}
		want := 0
		if source.Close != nil {
			want = 1
		}
		if closed != want {
			t.Fatalf("cleanup count %d want %d", closed, want)
		}
	}
	router.HandleStreamForwarded("watch", nil)
	if router.Validate() == nil {
		t.Fatal("nil forward factory accepted")
	}
}

func TestForwardedStreamACKAndCapacityCleanup(t *testing.T) {
	for _, binary := range []bool{false, true} {
		router := forwardedTestRouter()
		closed := 0
		ctx, cancel := context.WithCancel(t.Context())
		router.HandleStreamForwarded("watch", func(context.Context, *Request, any) (PreparedStreamSource, error) {
			cancel()
			return PreparedStreamSource{Run: func(context.Context, *Stream) error { t.Error("ran after failed ACK"); return nil }, Close: func() { closed++ }}, nil
		})
		var subs connectionSubscriptions
		router.startWSSubscription(ctx, &wsConn{ctx: t.Context()}, &subs, 1, &Request{API: "watch"}, nil, binary)
		if closed != 1 || router.Streams.SubCount("watch") != 0 || len(subs.items) != 0 {
			t.Fatal("failed ACK leaked resource")
		}
		router.Streams.subs["watch"] = make([]*StreamSub, MaxSubscribersPerAPI)
		if stream, err := router.PrepareInternalStream(t.Context(), &Request{API: "watch"}); err == nil || stream != nil || closed != 2 {
			t.Fatal("capacity failure leaked resource")
		}
	}
}
