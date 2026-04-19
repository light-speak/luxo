package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

func TestRPCRoundTrip(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("ping", func(ctx context.Context, req *api.Request) error {
		req.Buf.B = codec.AppendVarint(req.Buf.B, 1)
		req.Buf.B = codec.AppendString(req.Buf.B, "pong")
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("ping", 1)
	rt.Registry.RegisterParams("ping", nil)

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19876")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19876")
	defer client.Close()

	resp, err := client.Call(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp) == 0 {
		t.Fatal("empty response")
	}
	t.Logf("RPC response: %d bytes", len(resp))
}

func TestRPCWithParams(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("greet", func(ctx context.Context, req *api.Request) error {
		name, _ := req.ParamString("name")
		req.Buf.B = codec.AppendVarint(req.Buf.B, 1)
		req.Buf.B = codec.AppendString(req.Buf.B, "hello "+name)
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("greet", 2)
	rt.Registry.RegisterParams("greet", []api.ParamMeta{
		{Name: "name", Type: "String", FieldID: 1},
	})

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19877")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19877")
	defer client.Close()

	params := EncodeParams(ParamField{FieldID: 1, Value: "world"})
	resp, err := client.Call(2, params)
	if err != nil {
		t.Fatal(err)
	}

	// Decode response
	dec := codec.NewDecoder(resp)
	dec.NextField()
	msg := dec.ReadString()
	if msg != "hello world" {
		t.Fatalf("got %q, want 'hello world'", msg)
	}
}

func TestRPCError(t *testing.T) {
	rt := api.NewRouter()
	rt.Registry.Register("missing", 99)
	rt.Registry.RegisterParams("missing", nil)
	// No handler registered for "missing"

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19878")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19878")
	defer client.Close()

	_, err := client.Call(99, nil)
	if err == nil {
		t.Fatal("should error for missing handler")
	}
	t.Logf("error: %v", err)
}

func BenchmarkRPC_Ping(b *testing.B) {
	rt := api.NewRouter()
	rt.Handle("ping", func(ctx context.Context, req *api.Request) error {
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("ping", 1)
	rt.Registry.RegisterParams("ping", nil)

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19879")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19879")
	defer client.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, err := client.Call(1, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRPC_Echo(b *testing.B) {
	rt := api.NewRouter()
	rt.Handle("echo", func(ctx context.Context, req *api.Request) error {
		name, _ := req.ParamString("name")
		req.Buf.B = codec.AppendVarint(req.Buf.B, 1)
		req.Buf.B = codec.AppendString(req.Buf.B, "hello "+name)
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("echo", 1)
	rt.Registry.RegisterParams("echo", []api.ParamMeta{
		{Name: "name", Type: "String", FieldID: 1},
	})

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19880")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19880")
	defer client.Close()

	params := EncodeParams(ParamField{FieldID: 1, Value: "world"})

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, err := client.Call(1, params)
		if err != nil {
			b.Fatal(err)
		}
	}
}
