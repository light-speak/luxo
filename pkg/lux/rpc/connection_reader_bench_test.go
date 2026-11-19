package rpc

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

func BenchmarkRPCConnectionReaderWarm(b *testing.B) {
	for _, sample := range []struct {
		name string
		size int
	}{{"8B", 8}, {"4KiB", 4096}} {
		b.Run(sample.name, func(b *testing.B) {
			router := api.NewRouter()
			if err := router.Registry.Register("echo", 1); err != nil {
				b.Fatal(err)
			}
			if err := router.Registry.RegisterParams("echo", []api.ParamMeta{{Name: "value", Type: "Bytes", FieldID: 1}}); err != nil {
				b.Fatal(err)
			}
			router.Handle("echo", func(_ context.Context, req *api.Request) error {
				value, err := req.ParamBytes("value")
				if err != nil {
					return err
				}
				req.Buf.B = codec.AppendBytes(req.Buf.B, value)
				return nil
			})
			client := NewClient(startTestRPCServer(b, NewServer(router)))
			defer client.Close()
			value := bytes.Repeat([]byte{42}, sample.size)
			var encoder codec.Encoder
			encoder.WriteFieldBytes(1, value)
			encoder.WriteEnd()
			params := encoder.Bytes()
			want := codec.AppendBytes(nil, value)
			call := func() {
				got, err := client.Call(1, params)
				if err != nil || !bytes.Equal(got, want) {
					b.Fatalf("response: %x %v", got, err)
				}
			}
			for range 32 {
				call()
			}
			connection, err := client.pool.Get()
			if err != nil {
				b.Fatal(err)
			}
			client.pool.Put(connection)
			b.ReportAllocs()
			for b.Loop() {
				call()
			}
			reused, err := client.pool.Get()
			if err != nil {
				b.Fatal(err)
			}
			client.pool.Put(reused)
			if reused != connection {
				b.Fatal("warmed connection replaced")
			}
		})
	}
}

// Includes net.Pipe, handshake, first dispatch, and complete connection teardown;
// excludes TCP establishment and Server construction in both revisions.
func BenchmarkRPCConnectionReaderCold(b *testing.B) {
	router := api.NewRouter()
	if err := router.Registry.Register("ping", 1); err != nil {
		b.Fatal(err)
	}
	router.Handle("ping", func(_ context.Context, req *api.Request) error { req.Buf.B = append(req.Buf.B, 84); return nil })
	server := NewServer(router)
	defer server.Close()
	payload := encodeRequestEnvelope(requestKindCall, "", encodeCallRequest(1, nil, nil))
	var response []byte
	b.ReportAllocs()
	for b.Loop() {
		conn, peer := net.Pipe()
		if !server.registerConnection(conn) {
			b.Fatal("registration")
		}
		done := make(chan struct{})
		go func() { server.handleConn(conn); close(done) }()
		if err := initiateHandshake(context.Background(), peer, time.Second); err != nil {
			b.Fatal(err)
		}
		if err := WriteFrame(peer, payload); err != nil {
			b.Fatal(err)
		}
		var err error
		response, err = ReadFrame(peer, response)
		if err != nil || !bytes.Equal(response, []byte{statusOK, 84}) {
			b.Fatal(response, err)
		}
		peer.Close()
		<-done
	}
}
