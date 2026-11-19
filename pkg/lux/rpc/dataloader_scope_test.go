package rpc_test

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/auth"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/dataloader"
	"github.com/light-speak/luxo/pkg/lux/rpc"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

func TestLRPCBackgroundLoadsKeepVerifiedTenant(t *testing.T) {
	type tenantKey struct{}
	type result struct {
		value string
		err   error
	}
	cfg := &auth.Config{Secret: "loader-tenant-audit", Expires: time.Hour}
	var batchCalls atomic.Int32
	loader := dataloader.New(func(ctx context.Context, keys []int64, fields []*selection.Field) (map[int64]string, error) {
		batchCalls.Add(1)
		tenant, _ := ctx.Value(tenantKey{}).(string)
		values := make(map[int64]string, len(keys))
		for _, key := range keys {
			values[key] = tenant
		}
		return values, nil
	}, dataloader.Config{Wait: time.Second, MaxBatch: 2})
	background := make(chan result, 1)
	scopes := make(chan (<-chan struct{}), 2)
	router := api.NewRouter()
	router.InternalRequestContext = func(ctx context.Context, token string) (context.Context, error) {
		claims, err := auth.Verify(cfg, token)
		if err != nil {
			return nil, err
		}
		tenant, valid := claims.Get("tenant").String()
		if !valid || claims.Subject() != "User" {
			return nil, fmt.Errorf("invalid tenant identity")
		}
		return dataloader.NewScope(context.WithValue(ctx, tenantKey{}, tenant))
	}
	router.HandleInternal("background", func(ctx context.Context, req *api.Request) error {
		scopes <- ctx.Done()
		started := make(chan struct{})
		go func() {
			close(started)
			value, err := loader.Load(ctx, 42, nil)
			background <- result{value, err}
		}()
		<-started
		req.Buf.B = codec.AppendBool(req.Buf.B, true)
		return nil
	})
	router.HandleInternal("lookup", func(ctx context.Context, req *api.Request) error {
		scopes <- ctx.Done()
		value, err := loader.Load(ctx, 42, nil)
		if err != nil {
			return err
		}
		req.Buf.B, err = codec.AppendString(req.Buf.B, value)
		return err
	})
	for index, name := range []string{"background", "lookup"} {
		if err := router.Registry.Register(name, index+1); err != nil {
			t.Fatal(err)
		}
	}
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer(router)
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Close()
		if err := <-finished; err != nil {
			t.Error(err)
		}
	})
	client := rpc.NewClient(listener.Addr().String())
	t.Cleanup(client.Close)
	sign := func(tenant string) string {
		token, err := auth.SignForSubject(cfg, "User", func(dst []byte) ([]byte, error) {
			dst = append(dst, `{"tenant":"`...)
			dst = append(dst, tenant...)
			return append(dst, `"}`...), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := client.CallWithMaskContext(ctx, sign("tenant-a"), 1, nil, []byte{0}); err != nil {
		t.Fatal(err)
	}
	data, err := client.CallWithMaskContext(ctx, sign("tenant-b"), 2, nil, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	decoder := codec.NewDecoder(data)
	second := decoder.ReadString()
	if err := decoder.Finish(); err != nil {
		t.Fatal(err)
	}
	var first result
	select {
	case first = <-background:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if firstScope, secondScope := <-scopes, <-scopes; firstScope == nil || firstScope != secondScope {
		t.Fatal("probe did not reuse one connection cancellation scope")
	}
	t.Logf("verified tenants share one reused LRPC connection; batch calls=%d; background=%q lookup=%q", batchCalls.Load(), first.value, second)
	if batchCalls.Load() != 2 {
		t.Fatalf("request scopes were not isolated: %d batches", batchCalls.Load())
	}
	if first.err != nil || first.value != "tenant-a" || second != "tenant-b" {
		t.Fatalf("verified tenant context crossed requests: background=%q lookup=%q err=%v", first.value, second, first.err)
	}
}
