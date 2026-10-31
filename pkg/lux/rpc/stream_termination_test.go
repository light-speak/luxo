package rpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestPreparedStreamBusinessErrorBeforeACK(t *testing.T) {
	router := api.NewRouter()
	router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "watch", Stream: true, ReturnType: "Int"})
	router.Registry.RegisterSchemaAPIs(router.Schema)
	router.HandleStreamPrepared("watch", func(context.Context, *api.Request, any) (api.PreparedStreamHandler, error) {
		return nil, fmt.Errorf("prepare: %w", luxerrors.New("PrecisionRejected", 422, "error.precision").WithData(rpcPrecisionData{}))
	})
	client := NewClient(startTestRPCServer(t, NewServer(router)))
	defer client.Close()
	sub, err := client.SubscribeContext(t.Context(), "", 1, nil, nil)
	if sub != nil {
		sub.Close()
		t.Fatal("failed preparation acknowledged a subscription")
	}
	requireRPCPrecisionData(t, err)
	if router.Streams.SubCount("watch") != 0 {
		t.Fatal("failed preparation retained a subscription")
	}
}

func TestRPCPreparedStreamACKFailureDoesNotStartHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router := api.NewRouter()
		router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "watch", Stream: true, ReturnType: "Int"})
		router.Registry.RegisterSchemaAPIs(router.Schema)
		var started atomic.Int32
		router.HandleStreamPrepared("watch", func(context.Context, *api.Request, any) (api.PreparedStreamHandler, error) {
			return func(context.Context, *api.Stream) error { started.Add(1); return nil }, nil
		})
		serverConn, clientConn := net.Pipe()
		defer serverConn.Close()
		clientConn.Close()
		NewServer(router).processStream(serverConn, requestEnvelope{body: []byte{1, 0, 0}})
		synctest.Wait()
		if started.Load() != 0 || router.Streams.SubCount("watch") != 0 {
			t.Fatal("failed RPC ACK started or retained a subscription")
		}
	})
}

func TestPreparedStreamUnexpectedFailureIsInternal(t *testing.T) {
	router := api.NewRouter()
	router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "watch", Stream: true, ReturnType: "Int"})
	router.Registry.RegisterSchemaAPIs(router.Schema)
	router.HandleStreamPrepared("watch", func(context.Context, *api.Request, any) (api.PreparedStreamHandler, error) {
		return nil, errors.New("private preparation detail")
	})
	client := NewClient(startTestRPCServer(t, NewServer(router)))
	defer client.Close()
	sub, err := client.SubscribeContext(t.Context(), "", 1, nil, nil)
	if sub != nil {
		sub.Close()
		t.Fatal("unexpected preparation failure acknowledged")
	}
	appErr, ok := errors.AsType[*luxerrors.AppError](err)
	if !ok || appErr.Code != 500 || !appErr.Internal || appErr.Message != "error.internal" || appErr.Data != nil {
		t.Fatalf("unexpected failure was not sanitized: %+v", err)
	}
}

func TestRPCStreamPanicIsolation(t *testing.T) {
	for _, beforeACK := range []bool{false, true} {
		t.Run(fmt.Sprintf("beforeACK=%t", beforeACK), func(t *testing.T) {
			router, contexts := newStreamIsolationRouter(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			registerPanicStream(t, router, beforeACK)
			client := NewClient(startTestRPCServer(t, NewServer(router)))
			t.Cleanup(client.Close)
			other := openIsolationStream(t, ctx, client)
			otherContext := <-contexts
			sub, err := client.SubscribeContext(ctx, "", 3, nil, nil)
			if beforeACK {
				if sub != nil {
					sub.Close()
					t.Fatal("preparation panic acknowledged")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer sub.Close()
				requireStreamPayload(t, ctx, sub, 42)
				if _, open := <-sub.Messages(); open {
					t.Fatal("panic did not close data")
				}
				err = sub.Err()
			}
			appErr, ok := errors.AsType[*luxerrors.AppError](err)
			if !ok || appErr.Code != 500 || appErr.Message != "error.internal" || appErr.Data != nil {
				t.Fatalf("unsanitized terminal error: %v", err)
			}
			if otherContext.Err() != nil || router.Streams.SubCount("panicWatch") != 0 {
				t.Fatal("panic isolation failed")
			}
			router.Streams.DispatchEncoded("watch", []byte{99}, nil)
			requireStreamPayload(t, ctx, other, 99)
			requirePooledPing(t, ctx, client)
		})
	}
}

func registerPanicStream(t *testing.T, router *api.Router, beforeACK bool) {
	t.Helper()
	if err := router.Schema.RegisterAPI(&schema.API{ID: 3, Name: "panicWatch", Stream: true, ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	router.HandleStreamPrepared("panicWatch", func(context.Context, *api.Request, any) (api.PreparedStreamHandler, error) {
		if beforeACK {
			panic("private-prepare-details")
		}
		return func(_ context.Context, stream *api.Stream) error {
			if err := stream.Send([]byte{42}); err != nil {
				return err
			}
			panic("private-producer-details")
		}, nil
	})
}

func TestNativeStreamNormalCompletionDrainsMessages(t *testing.T) {
	router := api.NewRouter()
	router.Registry.Register("watch", 1)
	router.Registry.RegisterParams("watch", nil)
	router.HandleStreamNative("watch", func(_ context.Context, _ *api.StreamParams, _ any, stream *api.Stream) error {
		for i := range 64 {
			if err := stream.Send([]byte{byte(i)}); err != nil {
				t.Errorf("send %d: %v", i, err)
			}
		}
		return nil
	})
	server := NewServer(router)
	client := NewClient(startTestRPCServer(t, server))
	defer client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sub, err := client.SubscribeContext(ctx, "", 1, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	count := 0
	for message := range sub.Messages() {
		if len(message) != 1 || message[0] != byte(count) {
			t.Fatalf("message %d = %v", count, message)
		}
		count++
	}
	if count != 64 {
		t.Fatalf("delivered %d messages, want 64", count)
	}
	if err := <-sub.Errors(); err != nil {
		t.Fatalf("normal completion reported as failure: %v", err)
	}
	if err := sub.Err(); err != nil {
		t.Fatalf("stored completion result: %v", err)
	}
}

func newStreamIsolationClient(t *testing.T) (*Client, *api.Router, <-chan context.Context) {
	t.Helper()
	router, contexts := newStreamIsolationRouter(t)
	client := NewClient(startTestRPCServer(t, NewServer(router)))
	t.Cleanup(client.Close)
	return client, router, contexts
}

func newStreamIsolationRouter(t *testing.T) (*api.Router, <-chan context.Context) {
	t.Helper()
	router := api.NewRouter()
	router.Registry.Register("watch", 1)
	router.Registry.RegisterParams("watch", nil)
	router.Registry.Register("ping", 2)
	router.Registry.RegisterParams("ping", nil)
	contexts := make(chan context.Context, 2)
	router.HandleStreamNative("watch", func(ctx context.Context, _ *api.StreamParams, _ any, stream *api.Stream) error {
		contexts <- ctx
		if err := stream.Send([]byte{42}); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	})
	router.Handle("ping", func(_ context.Context, req *api.Request) error {
		req.Buf.B = append(req.Buf.B, 0, 84)
		return nil
	})
	return router, contexts
}

func TestRPCStreamCancellationKeepsOtherStreamsAndPooledCalls(t *testing.T) {
	client, router, contexts := newStreamIsolationClient(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first := openIsolationStream(t, ctx, client)
	firstContext := <-contexts
	second := openIsolationStream(t, ctx, client)
	secondContext := <-contexts
	if first.conn == second.conn {
		t.Fatal("RPC streams must own dedicated connections")
	}
	unary := requirePooledPing(t, ctx, client)
	if unary == first.conn || unary == second.conn {
		t.Fatal("active stream connection returned to the unary pool")
	}
	first.Close()
	select {
	case <-firstContext.Done():
	case <-ctx.Done():
		t.Fatal("server did not observe stream cancellation")
	}
	if secondContext.Err() != nil {
		t.Fatal("cancellation propagated to another RPC stream")
	}
	if requirePooledPing(t, ctx, client) != unary {
		t.Fatal("stream cancellation discarded the unrelated pooled connection")
	}
	router.Streams.DispatchEncoded("watch", []byte{99}, nil)
	requireStreamPayload(t, ctx, second, 99)
}

func openIsolationStream(t *testing.T, ctx context.Context, client *Client) *Subscription {
	t.Helper()
	sub, err := client.SubscribeContext(ctx, "", 1, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sub.Close)
	requireStreamPayload(t, ctx, sub, 42)
	return sub
}

func requireStreamPayload(t *testing.T, ctx context.Context, sub *Subscription, want byte) {
	t.Helper()
	select {
	case got := <-sub.Messages():
		if !bytes.Equal(got, []byte{want}) {
			t.Fatalf("stream payload=%x, want %x; terminal=%v", got, want, sub.Err())
		}
	case <-ctx.Done():
		t.Fatal("timed out reading the independent RPC stream")
	}
}

func requirePooledPing(t *testing.T, ctx context.Context, client *Client) net.Conn {
	t.Helper()
	data, err := client.CallWithMaskContext(ctx, "", 2, nil, nil)
	if err != nil || !bytes.Equal(data, []byte{0, 84}) {
		t.Fatalf("ping response=%x err=%v", data, err)
	}
	if len(client.pool.conns) != 1 {
		t.Fatal("successful unary call did not return its connection to the pool")
	}
	conn, err := client.pool.GetContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	client.pool.Put(conn)
	return conn
}

func TestRPCStreamCompletionFrameIsExplicitAndEmpty(t *testing.T) {
	for _, trailing := range []bool{false, true} {
		client, server := net.Pipe()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		sub := &Subscription{messages: make(chan []byte, 1), errors: make(chan error, 1), conn: client, cancel: cancel}
		go sub.readLoop(ctx)
		frame := []byte{statusStreamComplete}
		if trailing {
			frame = append(frame, 0)
		}
		if err := WriteFrame(server, frame); err != nil {
			t.Fatal(err)
		}
		err := <-sub.Errors()
		server.Close()
		cancel()
		if (err != nil) != trailing {
			t.Errorf("completion trailing=%t: %v", trailing, err)
		}
	}
}

func TestNativeStreamBusinessErrorPreservesDataAfterMessages(t *testing.T) {
	router := api.NewRouter()
	router.Registry.Register("watch", 1)
	router.Registry.RegisterParams("watch", nil)
	router.HandleStreamNative("watch", func(_ context.Context, _ *api.StreamParams, _ any, stream *api.Stream) error {
		for i := range 64 {
			if err := stream.Send([]byte{byte(i)}); err != nil {
				return err
			}
		}
		return fmt.Errorf("native: %w", luxerrors.New("PrecisionRejected", 422, "error.precision").WithData(rpcPrecisionData{}))
	})
	server := NewServer(router)
	client := NewClient(startTestRPCServer(t, server))
	defer client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sub, err := client.SubscribeContext(ctx, "", 1, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	// Deliberately observe termination first: data must still be available in order.
	requireRPCPrecisionData(t, <-sub.Errors())
	count := 0
	for message := range sub.Messages() {
		if len(message) != 1 || message[0] != byte(count) {
			t.Fatalf("message %d = %v", count, message)
		}
		count++
	}
	if count != 64 {
		t.Fatalf("error lost queued messages: %d", count)
	}
	requireRPCPrecisionData(t, sub.Err())
}

func TestWriteStreamResultPreservesFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
		name string
	}{
		{api.ErrStreamFull, 429, "SlowConsumer"},
		{errors.New("private credential"), 500, "Internal"},
		{luxerrors.Unauthorized, 401, "Unauthorized"},
	} {
		var wire bytes.Buffer
		if err := writeStreamResult(&wire, tc.err); err != nil {
			t.Fatal(err)
		}
		frame, err := ReadFrame(&wire, nil)
		if err != nil || len(frame) == 0 || frame[0] != statusError {
			t.Fatalf("error frame: %x, %v", frame, err)
		}
		decoded := decodeError(frame[1:])
		app, ok := errors.AsType[*luxerrors.AppError](decoded)
		if !ok || app.Code != tc.code || app.Name != tc.name || bytes.Contains(frame, []byte("private")) {
			t.Fatalf("terminal error = %+v", decoded)
		}
		if tc.code == 500 && (!app.Internal || app.Message != "error.internal" || app.Data != nil) {
			t.Fatalf("internal native failure leaked: %+v", app)
		}
	}
	if err := writeStreamResult(&errWriter{}, nil); err == nil {
		t.Fatal("failed completion write was ignored")
	}
	if err := writeStreamResult(&errWriter{}, api.ErrStreamFull); err == nil {
		t.Fatal("failed error write was ignored")
	}
}

func TestSubscriptionErrIsStableAndConcurrent(t *testing.T) {
	client, peer := net.Pipe()
	defer peer.Close()
	sub := &Subscription{messages: make(chan []byte, 1), errors: make(chan error, 1), conn: client, cancel: func() {}}
	if sub.Err() != nil {
		t.Fatal("active subscription has a terminal error")
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 100 {
				if err := sub.Err(); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Errorf("unexpected result: %v", err)
				}
			}
		})
	}
	sub.finish(io.ErrUnexpectedEOF)
	workers.Wait()
	sub.finish(nil)
	if !errors.Is(sub.Err(), io.ErrUnexpectedEOF) {
		t.Fatal("later completion overwrote failure")
	}
}

func TestRPCStreamSetupFailureReleasesSubscription(t *testing.T) {
	for _, scenario := range []string{"authentication", "not-stream", "ack-write"} {
		t.Run(scenario, func(t *testing.T) {
			router := api.NewRouter()
			router.Registry.Register("watch", 1)
			router.Registry.RegisterParams("watch", nil)
			var wire bytes.Buffer
			conn := &prefaceTestConn{writer: &wire}
			envelope := requestEnvelope{body: encodeCallRequest(1, nil, nil)}
			if scenario == "authentication" {
				envelope.token = "unverified"
			}
			if scenario == "ack-write" {
				router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "watch", Stream: true})
				conn.writer = &errWriter{}
			}
			NewServer(router).processStream(conn, envelope)
			if router.Streams.SubCount("watch") != 0 {
				t.Fatal("failed setup leaked subscription")
			}
			if scenario != "ack-write" {
				frame, err := ReadFrame(&wire, nil)
				if err != nil || frame[0] != statusError {
					t.Fatalf("setup failure response: %x, %v", frame, err)
				}
			}
		})
	}
}

type streamFailingWriter struct {
	net.Conn
	release  chan struct{}
	readDone chan struct{}
}

func (c *streamFailingWriter) Read([]byte) (int, error) {
	<-c.release
	close(c.readDone)
	return 0, io.EOF
}

func (*streamFailingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRPCStreamStopsAfterDataWriteFailure(t *testing.T) {
	router := api.NewRouter()
	router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "watch", Stream: true})
	stream, err := router.OpenInternalStream(t.Context(), &api.Request{API: "watch"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	router.Streams.DispatchEncoded("watch", []byte{1}, nil)
	conn := &streamFailingWriter{release: make(chan struct{}), readDone: make(chan struct{})}
	NewServer(router).pumpInternalStream(conn, stream)
	close(conn.release)
	<-conn.readDone
}
