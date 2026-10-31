package h2transport

import (
	"context"
	"net"
	"net/http"
	"testing"

	"golang.org/x/net/http2"
)

// Includes initial URI validation and the HTTP compatibility bridge, not socket
// I/O or business decoding. Keep both stages in the comparison to count ownership.
func BenchmarkValidatedHTTPBridge(b *testing.B) {
	for _, path := range []string{"/", "//a%20b?q=1", "/?"} {
		b.Run(path, func(b *testing.B) {
			server, client := net.Pipe()
			defer server.Close()
			defer client.Close()
			c := &candidateConnection{conn: server, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })}
			fields := requestFields()
			fields[3].Value = path
			frame := &http2.MetaHeadersFrame{HeadersFrame: &http2.HeadersFrame{FrameHeader: http2.FrameHeader{StreamID: 1}}, Fields: fields}
			b.ReportAllocs()
			for b.Loop() {
				head, err := parseRequestHead(frame)
				if err != nil {
					b.Fatal(err)
				}
				_, err = c.execute(&requestTask{ctx: context.Background(), request: &receivedRequest{head: head}})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
