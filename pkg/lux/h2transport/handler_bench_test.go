package h2transport

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

func BenchmarkPriorKnowledgeConnectionLifecycle(b *testing.B) {
	server, err := NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		b.Fatal(err)
	}
	httpServer := httptest.NewUnstartedServer(nil)
	if err := server.ConfigureHTTPServer(httpServer.Config); err != nil {
		b.Fatal(err)
	}
	dispatch := httpServer.Config.Handler
	finished := make(chan struct{}, 1)
	httpServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dispatch.ServeHTTP(w, r)
		finished <- struct{}{}
	})
	httpServer.Start()
	b.Cleanup(func() { _ = server.Close(); httpServer.Close() })
	request, err := http.NewRequestWithContext(b.Context(), http.MethodPost, httpServer.URL, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		runPriorKnowledgeConnection(b, httpServer.Listener.Addr().String(), request)
		<-finished
	}
}

func runPriorKnowledgeConnection(b *testing.B, addr string, request *http.Request) {
	b.Helper()
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		b.Fatal(err)
	}
	transport := &http2.Transport{}
	peer, err := transport.NewClientConn(conn)
	if err != nil {
		b.Fatal(err)
	}
	defer peer.Close()
	response, err := peer.RoundTrip(request)
	if err != nil {
		b.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent || response.ProtoMajor != 2 {
		b.Fatal("connection did not complete its HTTP/2 request")
	}
}

var protocolConfigurationSink *http.Server

func BenchmarkHTTPProtocolConfiguration(b *testing.B) {
	server, err := NewServer(http.NotFoundHandler())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		configured := &http.Server{}
		if err := server.ConfigureHTTPServer(configured); err != nil {
			b.Fatal(err)
		}
		protocolConfigurationSink = configured
	}
}

type protocolBenchmarkWriter struct{ calls int }

func (*protocolBenchmarkWriter) Header() http.Header { return nil }
func (w *protocolBenchmarkWriter) WriteHeader(int)   { w.calls++ }
func (w *protocolBenchmarkWriter) Write(data []byte) (int, error) {
	w.calls++
	return len(data), nil
}

func BenchmarkHTTP1ProtocolDispatch(b *testing.B) {
	server, err := NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		b.Fatal(err)
	}
	configured := &http.Server{}
	if err := server.ConfigureHTTPServer(configured); err != nil {
		b.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, "http://localhost/luvia", nil)
	if err != nil {
		b.Fatal(err)
	}
	writer := &protocolBenchmarkWriter{}
	b.ReportAllocs()
	for b.Loop() {
		configured.Handler.ServeHTTP(writer, request)
	}
	if writer.calls != b.N {
		b.Fatal("protocol dispatch did not reach the HTTP/1 handler")
	}
}

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

func BenchmarkHTTPWorkerDispatch(b *testing.B) {
	server, peer := net.Pipe()
	defer server.Close()
	defer peer.Close()
	c := newCandidateConnection(b.Context(), server, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	head := requestHead{method: "POST", authority: "example.test", path: "/"}
	if !head.validTarget("http") {
		b.Fatal("invalid benchmark target")
	}
	task := &requestTask{id: 1, ctx: b.Context(), request: &receivedRequest{head: head}}
	finished := make(chan struct{})
	go func() { defer close(finished); c.work() }()
	defer func() { close(c.jobs); <-finished }()
	b.ReportAllocs()
	for b.Loop() {
		c.jobs <- task
		command, err := c.inbox.wait(b.Context())
		if err != nil || command.kind != responseCommand || command.response.status != http.StatusNoContent {
			b.Fatal("worker failed dispatch", err)
		}
		command.response.release()
	}
}
