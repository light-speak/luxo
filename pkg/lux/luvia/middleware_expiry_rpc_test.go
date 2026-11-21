package luvia

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/auth"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"nhooyr.io/websocket"
)

func TestWebSocketExpiryCancelsForwardedRPC(t *testing.T) {
	auth.ResetConfig()
	auth.ResetVerifyCache()
	t.Cleanup(auth.ResetConfig)
	t.Cleanup(auth.ResetVerifyCache)
	t.Setenv("JWT_SECRET", "expiry-forwarding-test-secret")
	cfg, err := auth.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []websocket.MessageType{websocket.MessageText, websocket.MessageBinary} {
		t.Run(kind.String(), func(t *testing.T) { testForwardedExpiry(t, cfg, kind) })
	}
}

func expiryRouter(t *testing.T) *api.Router {
	t.Helper()
	router := New().Router
	if err := router.Schema.RegisterAPI(&schema.API{ID: 7, Name: "wait", ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	return router
}

func testForwardedExpiry(t *testing.T, cfg *auth.Config, kind websocket.MessageType) {
	t.Helper()
	started := make(chan struct{})
	finished := make(chan error, 1)
	release := make(chan struct{})
	defer close(release)
	service := expiryRouter(t)
	service.Handle("wait", func(ctx context.Context, _ *api.Request) error {
		if identity := Identity(ctx); identity == nil || identity.ID() != 42 {
			return luxerrors.Unauthorized
		}
		close(started)
		select {
		case <-ctx.Done():
			finished <- ctx.Err()
			return ctx.Err()
		case <-release:
			return context.Canceled
		}
	})
	client := headerBoundaryRPCClient(t, service)
	gateway := expiryRouter(t)
	gateway.Handle("wait", func(ctx context.Context, req *api.Request) error {
		data, err := client.CallWithMaskContext(ctx, BearerToken(ctx), 7, nil, nil)
		if err != nil {
			return err
		}
		req.Buf.B = append(req.Buf.B, data...)
		return nil
	})
	if err := gateway.Registry.RegisterSchemaAPIs(gateway.Schema); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Prepare(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(AuthMiddleware(cfg, gateway))
	defer server.Close()
	token, err := auth.SignWithExpiry(cfg, testClaimsJSON(`{"id":42}`), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"?token="+url.QueryEscape(token), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	request := []byte(`{"$id":1,"$api":"wait"}`)
	if kind == websocket.MessageBinary {
		request = []byte{api.BinaryFrameCallRequest, 1, 7, 0, 0}
	}
	if err := conn.Write(ctx, kind, request); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("service did not receive authenticated request", ctx.Err())
	}
	_, _, err = conn.Read(ctx)
	if websocket.CloseStatus(err) != api.WebSocketTokenExpired {
		t.Fatal("unexpected expiry result", err)
	}
	select {
	case err := <-finished:
		if err != context.Canceled {
			t.Fatal("remote cancellation", err)
		}
	case <-ctx.Done():
		t.Fatal("expiry did not cancel remote work", ctx.Err())
	}
}

func TestWebSocketExpiryCancelsForwardedStreamRPC(t *testing.T) {
	auth.ResetConfig()
	auth.ResetVerifyCache()
	t.Cleanup(auth.ResetConfig)
	t.Cleanup(auth.ResetVerifyCache)
	t.Setenv("JWT_SECRET", "expiry-stream-forwarding-test-secret")
	cfg, err := auth.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []websocket.MessageType{websocket.MessageText, websocket.MessageBinary} {
		t.Run(kind.String(), func(t *testing.T) { testForwardedStreamExpiry(t, cfg, kind) })
	}
}

func testForwardedStreamExpiry(t *testing.T, cfg *auth.Config, kind websocket.MessageType) {
	t.Helper()
	service := expiryRouter(t)
	service.Schema.APIs["wait"].Stream = true
	stopped := make(chan error, 1)
	release := make(chan struct{})
	defer close(release)
	service.HandleStreamNative("wait", func(ctx context.Context, _ *api.StreamParams, _ any, stream *api.Stream) error {
		if identity := Identity(ctx); identity == nil || identity.ID() != 42 {
			return luxerrors.Unauthorized
		}
		if err := stream.Send([]byte{84}); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			stopped <- ctx.Err()
			return ctx.Err()
		case <-release:
			return context.Canceled
		}
	})
	client := headerBoundaryRPCClient(t, service)
	gateway := expiryRouter(t)
	gateway.Schema.APIs["wait"].Stream = true
	if err := gateway.Registry.RegisterSchemaAPIs(gateway.Schema); err != nil {
		t.Fatal(err)
	}
	gateway.HandleStreamForwarded("wait", func(ctx context.Context, _ *api.Request, _ any) (api.PreparedStreamSource, error) {
		remote, err := client.SubscribeContext(ctx, BearerToken(ctx), 7, nil, nil)
		if err != nil {
			return api.PreparedStreamSource{}, err
		}
		return api.PreparedStreamSource{Close: remote.Close, Run: func(ctx context.Context, stream *api.Stream) error {
			return relayExpiryStream(ctx, stream, remote.Messages(), remote.Errors())
		}}, nil
	})
	if err := gateway.Prepare(); err != nil {
		t.Fatal(err)
	}
	peer, ctx := connectExpiryStream(t, cfg, gateway, kind)
	if _, frame, err := peer.Read(ctx); err != nil {
		t.Fatal("stream data", err)
	} else if kind == websocket.MessageBinary && !bytes.Equal(frame, []byte{api.BinaryFrameStream, 1, 84}) {
		t.Fatalf("stream Binary data: %x", frame)
	} else if kind == websocket.MessageText && string(frame) != `{"$stream":1,"data":42}` {
		t.Fatalf("stream JSON data: %s", frame)
	}
	if _, frame, err := peer.Read(ctx); websocket.CloseStatus(err) != api.WebSocketTokenExpired {
		t.Fatalf("expiry leaked frame %q or wrong close: %v", frame, err)
	}
	select {
	case err := <-stopped:
		if err != context.Canceled {
			t.Fatal("remote stream cancellation", err)
		}
	case <-ctx.Done():
		t.Fatal("remote stream survived expiry", ctx.Err())
	}
	for gateway.Streams.SubCount("wait") != 0 || service.Streams.SubCount("wait") != 0 {
		select {
		case <-ctx.Done():
			t.Fatal("expiry retained a subscription", ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
}

func relayExpiryStream(ctx context.Context, stream *api.Stream, messages <-chan []byte, failures <-chan error) error {
	for {
		select {
		case value, open := <-messages:
			if !open {
				return <-failures
			}
			if !stream.Binary() {
				if !bytes.Equal(value, []byte{84}) {
					return fmt.Errorf("unexpected remote Int: %x", value)
				}
				value = []byte("42")
			}
			if err := stream.Send(value); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func connectExpiryStream(t *testing.T, cfg *auth.Config, gateway *api.Router, kind websocket.MessageType) (*websocket.Conn, context.Context) {
	t.Helper()
	server := httptest.NewServer(AuthMiddleware(cfg, gateway))
	t.Cleanup(server.Close)
	token, err := auth.SignWithExpiry(cfg, testClaimsJSON(`{"id":42}`), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	t.Cleanup(cancel)
	peer, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"?token="+url.QueryEscape(token), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { peer.CloseNow() })
	frame := []byte(`{"$id":1,"$sub":"wait"}`)
	if kind == websocket.MessageBinary {
		frame = []byte{api.BinaryFrameSubscribe, 1, 7, 0, 0}
	}
	if err := peer.Write(ctx, kind, frame); err != nil {
		t.Fatal(err)
	}
	if _, frame, err := peer.Read(ctx); err != nil {
		t.Fatal("subscription ACK", err)
	} else if kind == websocket.MessageBinary && !bytes.Equal(frame, []byte{api.BinaryFrameSubscribeSuccess, 1}) {
		t.Fatalf("subscription ACK: %x", frame)
	} else if kind == websocket.MessageText && string(frame) != `{"$sub":1,"ok":true}` {
		t.Fatalf("subscription ACK: %s", frame)
	}
	return peer, ctx
}
