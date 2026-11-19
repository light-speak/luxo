package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

type tracePairSample struct {
	Traced      bool
	Pair        int
	Order       int
	Comparison  string
	Version     string
	Calls       int
	Nanoseconds int64
	Bytes       uint64
	Allocations uint64
	GCs         uint32
}

func TestRPCTracePairedNetwork(t *testing.T) {
	output := os.Getenv("LUXO_RPC_TRACE_PAIRED_OUTPUT")
	if output == "" {
		t.Skip("manual paired network measurement")
	}
	executable, err := os.Executable()
	if err != nil || filepath.Dir(output) != filepath.Dir(executable) {
		t.Fatal("paired output must stay beside its owned executable", err)
	}
	var samples []tracePairSample
	for _, traced := range []bool{false, true} {
		samples = append(samples, collectTraceNetworkPairs(t, traced)...)
	}
	data, err := json.MarshalIndent(samples, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func tracePairClient(t *testing.T) (*Client, *atomic.Int64) {
	t.Helper()
	router := api.NewRouter()
	if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "ping", ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	calls := new(atomic.Int64)
	router.Handle("ping", func(_ context.Context, req *api.Request) error {
		calls.Add(1)
		req.Buf.B = codec.AppendSvarint(req.Buf.B, 7)
		return nil
	})
	client := NewClient(startTestRPCServer(t, NewServer(router)))
	t.Cleanup(client.Close)
	return client, calls
}

func collectTraceNetworkPairs(t *testing.T, traced bool) []tracePairSample {
	t.Helper()
	client, calls := tracePairClient(t)
	ctx := t.Context()
	if traced {
		ctx, _ = api.WithDebugTrace(ctx, "valid")
	}
	for index := range 256 {
		callTracePair(t, client, ctx, index%2 != 0)
	}
	connection, err := client.pool.Get()
	if err != nil {
		t.Fatal(err)
	}
	client.pool.Put(connection)
	var samples []tracePairSample
	for pair := range 16 {
		for order := range 2 {
			current := (pair+order)%2 != 0
			sample := measureTracePair(t, client, ctx, current)
			sample.Traced, sample.Pair, sample.Order, sample.Comparison = traced, pair, order, "before-current"
			samples = append(samples, sample)
		}
		if pair%2 == 0 {
			for order := range 2 {
				sample := measureTracePair(t, client, ctx, false)
				sample.Traced, sample.Pair, sample.Order, sample.Comparison = traced, pair, order, "before-before"
				samples = append(samples, sample)
			}
		}
		reused, err := client.pool.Get()
		if err != nil || reused != connection {
			t.Fatal("paired run changed TCP connection", err)
		}
		client.pool.Put(reused)
	}
	if count := calls.Load(); count != 256+int64(len(samples))*128 {
		t.Fatal("unexpected server call count", count)
	}
	return samples
}

func callTracePair(t *testing.T, client *Client, ctx context.Context, current bool) {
	t.Helper()
	var body []byte
	var err error
	if current {
		body, err = client.CallWithMaskTargetContext(ctx, "", 1, nil, nil, TraceTarget{})
	} else {
		body, err = client.callBeforeEmptyTraceID(ctx, "", 1, nil, nil, TraceTarget{})
	}
	if err != nil || len(body) != 1 || body[0] != 14 {
		t.Fatal("paired response changed", body, err)
	}
}

func measureTracePair(t *testing.T, client *Client, ctx context.Context, current bool) tracePairSample {
	t.Helper()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	for range 128 {
		callTracePair(t, client, ctx, current)
	}
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	version := "before"
	if current {
		version = "current"
	}
	return tracePairSample{Version: version, Calls: 128, Nanoseconds: int64(elapsed), Bytes: after.TotalAlloc - before.TotalAlloc, Allocations: after.Mallocs - before.Mallocs, GCs: after.NumGC - before.NumGC}
}
