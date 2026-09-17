package rpc

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

// This measures one new TCP connection through the first application byte, not
// pool hits. The raw control has no preface; both peers otherwise do equal I/O.
func BenchmarkRPCColdConnection(b *testing.B) {
	for _, verified := range []bool{false, true} {
		name := "raw"
		if verified {
			name = "verified"
		}
		b.Run(name, func(b *testing.B) { benchmarkColdConnection(b, verified) })
	}
}

func benchmarkColdConnection(b *testing.B, verified bool) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			if verified {
				if err := acceptHandshake(conn); err != nil {
					conn.Close()
					continue
				}
			}
			_, _ = conn.Write([]byte{1})
			conn.Close()
		}
	}()
	defer func() { listener.Close(); <-done }()
	addr := listener.Addr().String()
	dialer := net.Dialer{Timeout: time.Second}
	var payload [1]byte
	b.ReportAllocs()
	for b.Loop() {
		conn, err := dialer.DialContext(context.Background(), "tcp", addr)
		if err != nil {
			b.Fatal(err)
		}
		if verified {
			if err := initiateHandshake(context.Background(), conn, time.Second); err != nil {
				conn.Close()
				b.Fatal(err)
			}
		}
		_, err = io.ReadFull(conn, payload[:])
		conn.Close()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRPCRequestEnvelope(b *testing.B) {
	body := encodeCallRequest(1, nil, nil)
	for _, traced := range []bool{false, true} {
		name := "plain"
		payload := encodeRequestEnvelope(requestKindCall, "token", body)
		if traced {
			name = "traced"
			payload = encodeTracedRequestEnvelope(requestKindCall, "token", "trace", "a.b", true, body)
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := decodeRequestEnvelope(payload); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
