package rpc

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/cache"
	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

type originRPCFixture struct {
	entered chan struct{}
	release chan struct{}
	calls   [4]atomic.Int32
}

func newOriginRPCRouter(t *testing.T, options api.ResponseOriginOptions) (*api.Router, *originRPCFixture) {
	t.Helper()
	backend := cache.NewMemory()
	t.Cleanup(func() { _ = backend.Close() })
	binding, err := api.NewResponseCache(backend, api.CacheNamespace{Project: "p", Environment: "test", Service: "s", Revision: "r"}, options)
	if err != nil {
		t.Fatal(err)
	}
	router := api.NewRouterWithOptions(api.RouterOptions{ResponseCache: binding})
	fixture := &originRPCFixture{entered: make(chan struct{}), release: make(chan struct{})}
	for index, name := range []string{"held", "probe", "hot", "plain"} {
		definition := &schema.API{ID: index + 1, Name: name, ReturnType: "Int"}
		if index < 3 {
			definition.CacheDependencies = &schema.CacheDependencies{}
			definition.CachePolicy = &schema.CachePolicy{Mode: "ttl", TTL: time.Minute, Scope: "private"}
			router.RequireCache(name)
		}
		if err := router.Schema.RegisterAPI(definition); err != nil {
			t.Fatal(err)
		}
		router.Handle(name, fixture.handler(index))
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	return router, fixture
}

func (f *originRPCFixture) handler(index int) api.HandlerFunc {
	return func(ctx context.Context, req *api.Request) error {
		execute := func() error {
			f.calls[index].Add(1)
			if index == 0 {
				close(f.entered)
				select {
				case <-f.release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			req.Buf.B = codec.AppendSvarint(req.Buf.B, 42)
			return nil
		}
		if index == 3 {
			return execute()
		}
		return api.CacheResponsePolicy(ctx, req, "", execute)
	}
}

func requireOriginRPCValue(t *testing.T, client *Client, id int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	body, err := client.CallWithMaskContext(ctx, "", id, nil, []byte{0})
	if err != nil || !bytes.Equal(body, codec.AppendSvarint(nil, 42)) {
		t.Fatalf("API %d: payload=%x error=%v", id, body, err)
	}
}

func holdOriginRPC(t *testing.T, fixture *originRPCFixture, address string) func() {
	t.Helper()
	client := NewClient(address)
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() {
		defer close(done)
		body, err := client.CallWithMaskContext(ctx, "", 1, nil, []byte{0})
		if err != nil || !bytes.Equal(body, codec.AppendSvarint(nil, 42)) {
			t.Errorf("in-flight request corrupted by overload: payload=%x error=%v", body, err)
		}
	}()
	var release sync.Once
	finish := func() { release.Do(func() { close(fixture.release) }); <-done }
	t.Cleanup(finish)
	select {
	case <-fixture.entered:
	case <-ctx.Done():
		t.Fatal("origin request never entered")
	}
	return finish
}

func TestCacheOriginRPCOverloadPreservesErrorAndConnection(t *testing.T) {
	for _, queued := range []bool{false, true} {
		name, options := "immediate", api.ResponseOriginOptions{MaxConcurrent: 1}
		if queued {
			name, options.MaxWaiting, options.WaitTimeout = "waiting", 1, 20*time.Millisecond
		}
		t.Run(name, func(t *testing.T) {
			router, fixture := newOriginRPCRouter(t, options)
			address := startTestRPCServer(t, NewServer(router))
			client := NewClient(address)
			t.Cleanup(client.Close)
			requireOriginRPCValue(t, client, 3)
			connection, err := client.pool.GetContext(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			client.pool.Put(connection)
			finish := holdOriginRPC(t, fixture, address)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			body, err := client.CallWithMaskContext(ctx, "", 2, nil, []byte{0})
			failure, ok := errors.AsType[*luxerrors.AppError](err)
			if !ok || failure.Code != 503 || failure.Name != "CacheOriginBusy" || len(body) != 0 {
				t.Fatalf("overload lost RPC identity: payload=%x error=%v", body, err)
			}
			requireOriginRPCValue(t, client, 3)
			requireOriginRPCValue(t, client, 4)
			requireOriginRPCCalls(t, fixture, [4]int32{1, 0, 1, 1})
			finish()
			requireOriginRPCValue(t, client, 2)
			requireOriginRPCCalls(t, fixture, [4]int32{1, 1, 1, 1})
			requireOriginRPCConnection(t, client, connection)
		})
	}
}

func requireOriginRPCCalls(t *testing.T, fixture *originRPCFixture, want [4]int32) {
	t.Helper()
	for index, expected := range want {
		if got := fixture.calls[index].Load(); got != expected {
			t.Errorf("origin %d executed %d times, want %d", index, got, expected)
		}
	}
}

func requireOriginRPCConnection(t *testing.T, client *Client, connection net.Conn) {
	t.Helper()
	reused, err := client.pool.GetContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	client.pool.Put(reused)
	if reused != connection {
		t.Error("application overload discarded the reusable RPC connection")
	}
}
