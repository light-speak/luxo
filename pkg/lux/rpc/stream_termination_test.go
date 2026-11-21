package rpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
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

func TestRPCStreamSetupCancellationPreservesContextError(t *testing.T) {
	for _, tc := range []struct {
		name        string
		awaitingACK bool
		deadline    bool
	}{
		{name: "cancelWrite"},
		{name: "cancelACK", awaitingACK: true},
		{name: "deadlineWrite", deadline: true},
		{name: "deadlineACK", awaitingACK: true, deadline: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				conn, peer := net.Pipe()
				defer peer.Close()
				client := NewClient("unused")
				defer client.Close()
				client.pool.Put(conn)
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() {
					sub, err := client.SubscribeContext(ctx, "", 1, nil, nil)
					if sub != nil {
						sub.Close()
					}
					done <- err
				}()
				if tc.awaitingACK {
					if _, err := ReadFrame(peer, nil); err != nil {
						t.Fatal(err)
					}
				}
				synctest.Wait()
				want := context.Canceled
				if tc.deadline {
					time.Sleep(time.Second)
					want = context.DeadlineExceeded
				} else {
					cancel()
				}
				if err := <-done; !errors.Is(err, want) {
					t.Fatalf("setup cancellation lost context error: %v", err)
				}
			})
		})
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
		NewServer(router).processStream(t.Context(), serverConn, requestEnvelope{body: []byte{1, 0, 0}})
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

func TestRPCStreamRejectsEmptyDataFrame(t *testing.T) {
	client, peer := net.Pipe()
	defer peer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	streamCtx, cancelStream := context.WithCancel(ctx)
	sub := &Subscription{messages: make(chan []byte, 4), errors: make(chan error, 1), conn: client, cancel: cancelStream}
	defer sub.Close()
	done := make(chan struct{})
	go func() { defer close(done); sub.readLoop(streamCtx) }()
	for _, frame := range [][]byte{{statusStream, 0}, {statusStream}} {
		if err := WriteFrame(peer, frame); err != nil {
			t.Fatal(err)
		}
	}
	// A bad frame must end the dedicated stream before a later completion frame.
	completionErr := WriteFrame(peer, []byte{statusStreamComplete})
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("malformed stream did not terminate")
	}
	var messages [][]byte
	for data := range sub.Messages() {
		messages = append(messages, data)
	}
	if len(messages) != 1 || !bytes.Equal(messages[0], []byte{0}) {
		t.Errorf("empty payload reached consumer: %v", messages)
	}
	if completionErr == nil || sub.Err() == nil {
		t.Errorf("empty data frame became normal completion: completion=%v terminal=%v", completionErr, sub.Err())
	}
	if err, open := <-sub.Errors(); !open || err != sub.Err() {
		t.Fatalf("terminal error missing: %v open=%v", err, open)
	}
	if _, open := <-sub.Errors(); open {
		t.Fatal("terminal error reported more than once")
	}
}

func TestRPCEmptyStreamDataKeepsIndependentConnections(t *testing.T) {
	router, contexts := newStreamIsolationRouter(t)
	releaseBadFrame := make(chan struct{})
	if err := router.Schema.RegisterAPI(&schema.API{ID: 3, Name: "broken", Stream: true, ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	router.HandleStreamPrepared("broken", func(context.Context, *api.Request, any) (api.PreparedStreamHandler, error) {
		return func(ctx context.Context, stream *api.Stream) error {
			if err := stream.Send([]byte{0}); err != nil {
				return err
			}
			select {
			case <-releaseBadFrame:
				return stream.Send(nil)
			case <-ctx.Done():
				return ctx.Err()
			}
		}, nil
	})
	client := NewClient(startTestRPCServer(t, NewServer(router)))
	defer client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	healthy := openIsolationStream(t, ctx, client)
	healthyContext := <-contexts
	broken, err := client.SubscribeContext(ctx, "", 3, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer broken.Close()
	// Establish both dedicated streams before capturing the idle unary socket.
	// A subscription may legitimately promote an idle pooled socket to its own.
	unary := requirePooledPing(t, ctx, client)
	close(releaseBadFrame)
	count := 0
	for message := range broken.Messages() {
		if count != 0 || !bytes.Equal(message, []byte{0}) {
			t.Errorf("invalid message reached consumer: %x", message)
		}
		count++
	}
	if count != 1 || broken.Err() == nil {
		t.Fatalf("malformed stream was not isolated: count=%d terminal=%v", count, broken.Err())
	}
	if healthyContext.Err() != nil || requirePooledPing(t, ctx, client) != unary {
		t.Fatal("malformed stream disturbed unrelated connections")
	}
	router.Streams.DispatchEncoded("watch", []byte{99}, nil)
	requireStreamPayload(t, ctx, healthy, 99)
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
			NewServer(router).processStream(t.Context(), conn, envelope)
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

type streamFailingWriter struct{ net.Conn }

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
	conn := &streamFailingWriter{}
	NewServer(router).pumpInternalStream(t.Context(), conn, stream)
}

// Models the interval after a socket deadline fires but before context's timer
// publishes Err/Done. No scheduler timing is required to reproduce the race.
type pendingDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c pendingDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }

type deadlineReadConn struct{ net.Conn }

func (c deadlineReadConn) Write(p []byte) (int, error) { return len(p), nil }

func TestRPCDeadlineBeforeContextNotification(t *testing.T) {
	for _, operation := range []string{"call", "subscribe", "handshake"} {
		for _, reading := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/read=%v", operation, reading), func(t *testing.T) {
				conn, peer := net.Pipe()
				defer conn.Close()
				defer peer.Close()
				ctx := pendingDeadlineContext{context.Background(), time.Now().Add(-time.Second)}
				var transport net.Conn = conn
				if reading {
					transport = deadlineReadConn{conn}
				}
				client := NewClient("unused")
				defer client.Close()
				var err error
				if operation == "handshake" {
					err = initiateHandshake(ctx, transport, time.Second)
				} else {
					client.pool.Put(transport)
					if operation == "call" {
						_, err = client.CallWithMaskContext(ctx, "", 1, nil, nil)
					} else {
						_, err = client.SubscribeContext(ctx, "", 1, nil, nil)
					}
				}
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("deadline notification race lost context identity: %v", err)
				}
				if client.pool.open.Load() != 0 || len(client.pool.conns) != 0 {
					t.Fatal("timed-out connection retained a pool slot")
				}
			})
		}
	}
}

func TestContextErrorAfterIOPreservesOtherFailures(t *testing.T) {
	past := pendingDeadlineContext{context.Background(), time.Now().Add(-time.Second)}
	future := pendingDeadlineContext{context.Background(), time.Now().Add(time.Hour)}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name          string
		ctx           context.Context
		ioError, want error
	}{
		{"noDeadline", context.Background(), os.ErrDeadlineExceeded, nil},
		{"earlierSocketTimeout", future, os.ErrDeadlineExceeded, nil},
		{"peerClosed", past, io.EOF, nil},
		{"otherNetworkError", past, &net.OpError{Op: "read", Err: io.EOF}, nil},
		{"wrappedTimeout", past, fmt.Errorf("read: %w", os.ErrDeadlineExceeded), context.DeadlineExceeded},
		{"cancelled", cancelled, os.ErrDeadlineExceeded, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := contextErrorAfterIO(tc.ctx, tc.ioError); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRPCStreamReadDeadlineBeforeContextNotification(t *testing.T) {
	conn, peer := net.Pipe()
	defer peer.Close()
	ctx := pendingDeadlineContext{context.Background(), time.Now().Add(-time.Second)}
	if err := conn.SetDeadline(ctx.deadline); err != nil {
		t.Fatal(err)
	}
	_, cancel := context.WithCancel(ctx)
	defer cancel()
	sub := &Subscription{conn: conn, cancel: cancel, messages: make(chan []byte, 1), errors: make(chan error, 1)}
	sub.readLoop(ctx)
	if err := <-sub.Errors(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stream lost deadline identity: %v", err)
	}
	if _, open := <-sub.Messages(); open {
		t.Fatal("stream was not terminated")
	}
}

func cancellationSourceServer(t *testing.T) (*api.Router, *Client, <-chan struct{}, <-chan struct{}) {
	t.Helper()
	router := api.NewRouter()
	if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "watch", Stream: true, ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	started, stopped := make(chan struct{}), make(chan struct{})
	router.HandleStreamPrepared("watch", func(context.Context, *api.Request, any) (api.PreparedStreamHandler, error) {
		return func(ctx context.Context, _ *api.Stream) error {
			close(started)
			<-ctx.Done()
			close(stopped)
			return ctx.Err()
		}, nil
	})
	client := NewClient(startTestRPCServer(t, NewServer(router)))
	t.Cleanup(client.Close)
	return router, client, started, stopped
}

func TestForwardedCancellationReleasesRealRPCSource(t *testing.T) {
	backend, client, started, stopped := cancellationSourceServer(t)
	deadline, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	ctx, cancel := context.WithCancel(deadline)
	defer cancel()
	gateway := api.NewRouter()
	if err := gateway.Schema.RegisterAPI(&schema.API{ID: 1, Name: "watch", Stream: true, ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	var runs, closes atomic.Int32
	gateway.HandleStreamForwarded("watch", func(context.Context, *api.Request, any) (api.PreparedStreamSource, error) {
		// Keep the remote lifetime independent so local cancellation cannot
		// hide a missing source release.
		remote, err := client.SubscribeContext(deadline, "", 1, nil, nil)
		if err != nil {
			return api.PreparedStreamSource{}, err
		}
		select {
		case <-started:
		case <-deadline.Done():
			remote.Close()
			return api.PreparedStreamSource{}, deadline.Err()
		}
		cancel()
		return api.PreparedStreamSource{
			Run:   func(context.Context, *api.Stream) error { runs.Add(1); return nil },
			Close: func() { closes.Add(1); remote.Close() },
		}, nil
	})
	stream, err := gateway.PrepareInternalStream(ctx, &api.Request{API: "watch"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	stream.Start()
	stream.Close()
	select {
	case <-stopped:
	case <-deadline.Done():
		t.Fatal("remote producer survived cancellation")
	}
	if runs.Load() != 0 || closes.Load() != 1 || gateway.Streams.SubCount("watch") != 0 || backend.Streams.SubCount("watch") != 0 {
		t.Fatalf("forwarding lifecycle leaked: runs=%d closes=%d", runs.Load(), closes.Load())
	}
	if client.pool.open.Load() != 0 || len(client.pool.conns) != 0 {
		t.Fatal("remote subscription connection retained a pool slot")
	}
}

func TestForwardedACKFailureReleasesRealRPCSource(t *testing.T) {
	backend, client, started, stopped := cancellationSourceServer(t)
	deadline, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	gateway := api.NewRouter()
	if err := gateway.Schema.RegisterAPI(&schema.API{ID: 1, Name: "watch", Stream: true, ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Registry.RegisterSchemaAPIs(gateway.Schema); err != nil {
		t.Fatal(err)
	}
	var runs, closes atomic.Int32
	gateway.HandleStreamForwarded("watch", func(context.Context, *api.Request, any) (api.PreparedStreamSource, error) {
		// Only explicit ownership release can stop this independent subscription.
		remote, err := client.SubscribeContext(deadline, "", 1, nil, nil)
		if err != nil {
			return api.PreparedStreamSource{}, err
		}
		select {
		case <-started:
		case <-deadline.Done():
			remote.Close()
			return api.PreparedStreamSource{}, deadline.Err()
		}
		return api.PreparedStreamSource{
			Run:   func(context.Context, *api.Stream) error { runs.Add(1); return nil },
			Close: func() { closes.Add(1); remote.Close() },
		}, nil
	})
	serverConn, peer := net.Pipe()
	defer serverConn.Close()
	peer.Close()
	NewServer(gateway).processStream(t.Context(), serverConn, requestEnvelope{body: encodeCallRequest(1, nil, nil)})
	select {
	case <-stopped:
	case <-deadline.Done():
		t.Fatal("failed ACK retained remote producer")
	}
	if runs.Load() != 0 || closes.Load() != 1 {
		t.Fatalf("failed ACK lifecycle: runs=%d closes=%d", runs.Load(), closes.Load())
	}
	if gateway.Streams.SubCount("watch") != 0 || backend.Streams.SubCount("watch") != 0 {
		t.Fatal("failed ACK retained subscription registration")
	}
	if client.pool.open.Load() != 0 || len(client.pool.conns) != 0 {
		t.Fatal("failed ACK retained remote connection slot")
	}
}

func TestRPCStreamIdentityFailureRejectsBeforeACK(t *testing.T) {
	router := api.NewRouter()
	if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "watch", Stream: true, ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	var extracted, prepared atomic.Int32
	router.IdentityExtractor = func(context.Context) any {
		if extracted.Add(1) == 1 {
			panic("private identity detail")
		}
		return int64(42)
	}
	router.HandleStreamPrepared("watch", func(_ context.Context, _ *api.Request, identity any) (api.PreparedStreamHandler, error) {
		prepared.Add(1)
		if identity != int64(42) {
			return nil, errors.New("identity lost")
		}
		return func(_ context.Context, stream *api.Stream) error { return stream.Send([]byte{84}) }, nil
	})
	client := NewClient(startTestRPCServer(t, NewServer(router)))
	defer client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sub, err := client.SubscribeContext(ctx, "", 1, nil, nil)
	if sub != nil {
		sub.Close()
		t.Fatal("identity failure was acknowledged")
	}
	app, ok := errors.AsType[*luxerrors.AppError](err)
	if !ok || app.Code != 500 || app.Message != "error.internal" || app.Data != nil {
		t.Fatalf("unsafe rejection: %v", err)
	}
	if prepared.Load() != 0 || router.Streams.SubCount("watch") != 0 || client.pool.open.Load() != 0 || len(client.pool.conns) != 0 {
		t.Fatal("rejected identity retained resources or ran preparation")
	}
	sub, err = client.SubscribeContext(ctx, "", 1, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	count := 0
	for message := range sub.Messages() {
		if !bytes.Equal(message, []byte{84}) {
			t.Fatalf("unexpected data: %x", message)
		}
		count++
	}
	if err := <-sub.Errors(); err != nil {
		t.Fatal(err)
	}
	if count != 1 || prepared.Load() != 1 || extracted.Load() != 2 || router.Streams.SubCount("watch") != 0 {
		t.Fatal("later valid subscription did not complete")
	}
}

func TestSlowForwardedCleanupPanicReleasesOtherRPCSources(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	gateway := api.NewRouter()
	if err := gateway.Schema.RegisterAPI(&schema.API{ID: 1, Name: "watch", Stream: true, ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	var sources []api.PreparedStreamSource
	var clients []*Client
	var stops []<-chan struct{}
	var releases atomic.Int32
	for index := range 2 {
		_, client, started, stopped := cancellationSourceServer(t)
		remote, err := client.SubscribeContext(ctx, "", 1, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(remote.Close)
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		clients, stops = append(clients, client), append(stops, stopped)
		sources = append(sources, api.PreparedStreamSource{
			Run: func(context.Context, *api.Stream) error { t.Error("unexpected forwarding start"); return nil },
			Close: func() {
				releases.Add(1)
				remote.Close()
				if index == 0 {
					panic("private cleanup detail")
				}
			},
		})
	}
	next := 0
	gateway.HandleStreamForwarded("watch", func(context.Context, *api.Request, any) (api.PreparedStreamSource, error) {
		source := sources[next]
		next++
		return source, nil
	})
	var capacity int
	for range 2 {
		stream, err := gateway.PrepareInternalStream(ctx, &api.Request{API: "watch"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(stream.Close)
		capacity = cap(stream.Messages())
	}
	for range capacity + 1 {
		gateway.Streams.DispatchEncoded("watch", []byte{1}, nil)
	}
	for index, stopped := range stops {
		select {
		case <-stopped:
		case <-ctx.Done():
			t.Fatalf("remote %d survived cleanup", index)
		}
		if clients[index].pool.open.Load() != 0 || len(clients[index].pool.conns) != 0 {
			t.Fatalf("remote %d retained connection", index)
		}
	}
	if releases.Load() != 2 || gateway.Streams.SubCount("watch") != 0 {
		t.Fatal("batch cleanup incomplete")
	}
}

func TestServerCloseCancelsStreamPreparation(t *testing.T) {
	router := api.NewRouter()
	if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "watch", Stream: true, ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	defer close(release)
	router.HandleStreamPrepared("watch", func(ctx context.Context, _ *api.Request, _ any) (api.PreparedStreamHandler, error) {
		started <- ctx
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return nil, context.Canceled
		}
	})
	server := NewServer(router)
	client := NewClient(startTestRPCServer(t, server))
	defer client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		sub, err := client.SubscribeContext(ctx, "", 1, nil, nil)
		if sub != nil {
			sub.Close()
			t.Error("preparation unexpectedly acknowledged")
			result <- err
			return
		}
		result <- err
	}()
	var preparation context.Context
	select {
	case preparation = <-started:
	case <-ctx.Done():
		t.Fatal("prepare did not start")
	}
	closed := make(chan struct{})
	go func() { server.Close(); close(closed) }()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("close did not cancel stream preparation")
	}
	if preparation.Err() != context.Canceled {
		t.Fatal("preparation not canceled", preparation.Err())
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("closed preparation succeeded")
		}
	case <-ctx.Done():
		t.Fatal("subscription call did not finish")
	}
	if router.Streams.SubCount("watch") != 0 {
		t.Fatal("failed preparation retained subscription")
	}
}
