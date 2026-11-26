package h2transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

func TestHandlerBodylessResponsesKeepRealConnectionUsable(t *testing.T) {
	metadata := strings.Repeat("~", 15<<10)
	server, conn := newHandlerTestTCP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(*response); !ok {
			t.Error("request bypassed the dedicated HTTP/2 writer")
		}
		switch r.URL.Path {
		case "/head":
			w.Header().Set("X-Response-Metadata", metadata)
			_, _ = io.WriteString(w, "body")
		case "/no-content":
			w.Header().Set("Content-Length", "4")
			w.WriteHeader(http.StatusNoContent)
			_, _ = io.WriteString(w, "body")
		case "/reset-content":
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusResetContent)
			_, _ = io.WriteString(w, "body")
		case "/not-modified":
			w.Header().Set("ETag", `"beta"`)
			if r.Header.Get("If-None-Match") == `"beta"` {
				w.Header().Set("Content-Length", "24")
				w.WriteHeader(http.StatusNotModified)
				return
			}
			_, _ = io.WriteString(w, strings.Repeat("x", 24))
		case "/healthy":
			_, _ = io.WriteString(w, "healthy")
		}
	}))
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, http2.ClientPreface); err != nil {
		t.Fatal(err)
	}
	peer := http2.NewFramer(conn, conn)
	peer.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	if err := peer.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	for i, sample := range []handlerPeerExpectation{
		{method: http.MethodHead, path: "/head", status: http.StatusOK, length: "4", metadata: metadata},
		{method: http.MethodGet, path: "/no-content", status: http.StatusNoContent},
		{method: http.MethodGet, path: "/not-modified", status: http.StatusNotModified, length: "24", etag: `"beta"`},
		{method: http.MethodGet, path: "/reset-content", status: http.StatusResetContent, length: "0"},
		{method: http.MethodGet, path: "/healthy", status: http.StatusOK, length: "7", body: "healthy"},
	} {
		t.Run(strings.TrimPrefix(sample.path, "/"), func(t *testing.T) {
			assertHandlerPeerResponse(t, peer, uint32(2*i+1), sample)
		})
	}
	ping := [8]byte{1}
	if err := peer.WritePing(false, ping); err != nil {
		t.Fatal(err)
	}
	frame := readHandlerPeerFrame(t, peer)
	if reply, ok := frame.(*http2.PingFrame); !ok || !reply.IsAck() || reply.Data != ping {
		t.Fatalf("connection did not acknowledge ping: %v", frame)
	}
	if server.budget.used.Load() != 0 || server.streams.used.Load() != 0 {
		t.Fatal("live connection retained response or stream capacity", server.budget.used.Load(), server.streams.used.Load())
	}
}

type handlerPeerExpectation struct {
	method, path, length, etag, metadata, body string
	status                                     int
}

func assertHandlerPeerResponse(t *testing.T, peer *http2.Framer, id uint32, sample handlerPeerExpectation) {
	t.Helper()
	head := readHandlerPeerHeaders(t, peer, id, sample.method, sample.path)
	if head.PseudoValue("status") != strconv.Itoa(sample.status) || head.StreamEnded() != (sample.body == "") {
		t.Fatalf("status=%q end-stream=%t", head.PseudoValue("status"), head.StreamEnded())
	}
	headers := make(http.Header)
	for _, field := range head.RegularFields() {
		headers.Add(field.Name, field.Value)
	}
	if headers.Get("Content-Length") != sample.length || headers.Get("ETag") != sample.etag || headers.Get("X-Response-Metadata") != sample.metadata {
		t.Fatalf("Content-Length=%q ETag=%q metadata bytes=%d", headers.Get("Content-Length"), headers.Get("ETag"), len(headers.Get("X-Response-Metadata")))
	}
	if sample.body == "" {
		return
	}
	frame := readHandlerPeerFrame(t, peer)
	data, ok := frame.(*http2.DataFrame)
	if !ok || data.StreamID != id || !data.StreamEnded() || string(data.Data()) != sample.body {
		t.Fatalf("healthy GET did not finish with its body: %v", frame)
	}
}

func readHandlerPeerHeaders(t *testing.T, peer *http2.Framer, id uint32, method, path string) *http2.MetaHeadersFrame {
	t.Helper()
	var block bytes.Buffer
	encoder := hpack.NewEncoder(&block)
	for _, field := range []hpack.HeaderField{
		{Name: ":method", Value: method}, {Name: ":scheme", Value: "http"},
		{Name: ":authority", Value: "example.test"}, {Name: ":path", Value: path},
		{Name: "if-none-match", Value: `"beta"`},
	} {
		if err := encoder.WriteField(field); err != nil {
			t.Fatal(err)
		}
	}
	if err := peer.WriteHeaders(http2.HeadersFrameParam{StreamID: id, BlockFragment: block.Bytes(), EndHeaders: true, EndStream: true}); err != nil {
		t.Fatal(err)
	}
	frame := readHandlerPeerFrame(t, peer)
	head, ok := frame.(*http2.MetaHeadersFrame)
	if !ok || head.StreamID != id || head.Truncated {
		t.Fatalf("wanted response headers for stream %d: %v", id, frame)
	}
	return head
}

func readHandlerPeerFrame(t *testing.T, peer *http2.Framer) http2.Frame {
	t.Helper()
	for {
		frame, err := peer.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		switch settings := frame.(type) {
		case *http2.SettingsFrame:
			if !settings.IsAck() {
				if err := peer.WriteSettingsAck(); err != nil {
					t.Fatal(err)
				}
			}
		case *http2.WindowUpdateFrame:
		default:
			return frame
		}
	}
}

func TestWorkerGoexitReleasesResponseAndAbortsStream(t *testing.T) {
	for _, transfer := range []bool{false, true} {
		t.Run(map[bool]string{false: "copied", true: "transferred"}[transfer], func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if transfer {
					buf := &api.ResponseBuf{B: make([]byte, 4, 16)}
					copy(buf.B, "data")
					if !w.(*response).TakeBinaryResponse(buf) {
						t.Error("response transfer failed")
					}
				} else if _, err := w.Write([]byte("data")); err != nil {
					t.Error(err)
				}
				runtime.Goexit()
			})
			c, task, _ := workerFixture(t, handler)
			c.budget = &byteBudget{limit: 64}
			finished := make(chan struct{})
			go func() { defer close(finished); c.work() }()
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("Goexit did not finish worker")
			}
			if !task.done || c.budget.used.Load() != 0 || task.response != nil {
				t.Errorf("Goexit retained task or response: done=%t bytes=%d", task.done, c.budget.used.Load())
			}
			command, ok, err := c.inbox.poll()
			if err != nil || !ok || command.kind != abortStreamCommand || command.streamID != task.id || command.code != http2.ErrCodeInternal {
				t.Errorf("Goexit did not abort only its stream: %+v %v", command, err)
			}
			c.outputFinished(task.id)
			if len(c.tasks) != 0 || task.ctx.Err() == nil {
				t.Error("aborted task remained registered")
			}
		})
	}
}

func TestHandlerGoexitKeepsPeerAndWorkersUsable(t *testing.T) {
	entered := make(chan struct{})
	hold, release := context.WithCancel(t.Context())
	defer release()
	server, peer := newHandlerTestPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hold":
			close(entered)
			select {
			case <-hold.Done():
			case <-r.Context().Done():
				return
			}
		case "/exit":
			_, _ = w.Write([]byte("private partial response"))
			runtime.Goexit()
		}
		_, _ = io.WriteString(w, "healthy")
	}))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	held := make(chan error, 1)
	go func() { held <- readHealthyHandlerPeer(ctx, peer, "/hold") }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("unrelated stream did not start")
	}
	assertGoexitStreamResets(t, ctx, peer)
	if err := readHealthyHandlerPeer(ctx, peer, "/healthy"); err != nil {
		t.Fatal("Goexit exhausted workers or closed the peer", err)
	}
	release()
	if err := <-held; err != nil {
		t.Fatal("Goexit canceled an unrelated active stream", err)
	}
	_ = peer.Close()
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if server.budget.used.Load() != 0 || server.streams.used.Load() != 0 {
		t.Fatal("Goexit leaked capacity", server.budget.used.Load(), server.streams.used.Load())
	}
}

func TestHandlerInvalidResponseHeaderResetsOnlyItsStream(t *testing.T) {
	server, peer := newHandlerTestPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/invalid" {
			w.Header().Set("Content-Length", "1")
		}
		_, _ = io.WriteString(w, "healthy")
	}))
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.test/invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := peer.RoundTrip(request)
	if resp != nil {
		_ = resp.Body.Close()
	}
	var streamErr http2.StreamError
	if !errors.As(err, &streamErr) || streamErr.Code != http2.ErrCodeInternal {
		t.Fatalf("invalid response did not reset only its stream: %v", err)
	}
	if err := readHealthyHandlerPeer(ctx, peer, "/healthy"); err != nil {
		t.Fatal("invalid response closed the connection:", err)
	}
	_ = peer.Close()
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if server.budget.used.Load() != 0 || server.streams.used.Load() != 0 {
		t.Fatal("invalid response retained stream or body capacity")
	}
}

func newHandlerTestPeer(t *testing.T, handler http.Handler) (*Server, *http2.ClientConn) {
	t.Helper()
	server, conn := newHandlerTestTCP(t, handler)
	transport := &http2.Transport{}
	peer, err := transport.NewClientConn(conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	return server, peer
}

func newHandlerTestTCP(t *testing.T, handler http.Handler) (*Server, net.Conn) {
	t.Helper()
	server, err := NewServer(handler)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewUnstartedServer(nil)
	if err := server.ConfigureHTTPServer(httpServer.Config); err != nil {
		t.Fatal(err)
	}
	httpServer.Start()
	t.Cleanup(func() { _ = server.Close(); httpServer.Close() })
	conn, err := net.DialTimeout("tcp", httpServer.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return server, conn
}

func assertGoexitStreamResets(t *testing.T, ctx context.Context, peer *http2.ClientConn) {
	t.Helper()
	for range 16 {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://example.test/exit", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := peer.RoundTrip(request)
		if response != nil {
			_ = response.Body.Close()
		}
		var streamError http2.StreamError
		if !errors.As(err, &streamError) || streamError.Code != http2.ErrCodeInternal {
			t.Fatal("Goexit did not reset its stream with Internal", err)
		}
	}
}

func readHealthyHandlerPeer(ctx context.Context, peer *http2.ClientConn, path string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://example.test"+path, nil)
	if err != nil {
		return err
	}
	response, err := peer.RoundTrip(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK || string(body) != "healthy" {
		return errors.New("unexpected healthy response")
	}
	return nil
}

func TestHandlerBridgeMetadataAndErrors(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	task := &requestTask{ctx: context.Background(), request: &receivedRequest{head: requestHead{method: "POST", authority: "example.test", path: "//a%20b?q=1", fields: []hpack.HeaderField{{Name: "cookie", Value: "a=1"}, {Name: "cookie", Value: "b=2"}}}, trailers: []hpack.HeaderField{{Name: "x-sum", Value: "ok"}}}}
	if !task.request.head.validTarget("http") {
		t.Fatal("invalid fixture target")
	}
	c := newCandidateConnection(context.Background(), server, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "//a b" || r.URL.RawQuery != "q=1" || r.Host != "example.test" || r.Header.Get("Cookie") != "a=1; b=2" || r.Trailer.Get("X-Sum") != "ok" {
			t.Error("metadata changed", r)
		}
	}))
	if result, err := c.execute(task); err != nil || result.status != 200 {
		t.Fatal(result, err)
	}
	c.handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("native failure") })
	if _, err := c.execute(task); !errors.Is(err, errResponse) {
		t.Fatal(err)
	}
	task.ctx, task.cancel = context.WithCancel(context.Background())
	task.cancel()
	if _, err := c.execute(task); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestHandlerBodylessResponsesEndWithHeaders(t *testing.T) {
	for _, sample := range []struct {
		name, method, declaredLength, wireLength string
		status                                   int
		writeErr                                 error
	}{
		{name: "HEAD inferred", method: http.MethodHead, status: http.StatusOK, wireLength: "4"},
		{name: "HEAD declared", method: http.MethodHead, status: http.StatusOK, declaredLength: "24", wireLength: "24"},
		{name: "204", method: http.MethodPost, status: http.StatusNoContent, declaredLength: "4", writeErr: http.ErrBodyNotAllowed},
		{name: "205", method: http.MethodPost, status: http.StatusResetContent, declaredLength: "0", wireLength: "0", writeErr: http.ErrBodyNotAllowed},
		{name: "304", method: http.MethodGet, status: http.StatusNotModified, writeErr: http.ErrBodyNotAllowed},
		{name: "304 declared", method: http.MethodGet, status: http.StatusNotModified, declaredLength: "24", wireLength: "24", writeErr: http.ErrBodyNotAllowed},
	} {
		t.Run(sample.name, func(t *testing.T) {
			c, task, _ := workerFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Response-Metadata", "value")
				if sample.declaredLength != "" {
					w.Header().Set("Content-Length", sample.declaredLength)
				}
				w.WriteHeader(sample.status)
				buffer := &api.ResponseBuf{B: []byte("binary")}
				if w.(*response).TakeBinaryResponse(buffer) || string(buffer.B) != "binary" {
					t.Error("bodyless response took ownership of the caller's buffer")
				}
				n, err := io.WriteString(w, "body")
				if !errors.Is(err, sample.writeErr) || (err == nil && n != 4) || (err != nil && n != 0) {
					t.Errorf("Write = %d, %v", n, err)
				}
			}))
			task.request.head.method = sample.method
			result, err := c.execute(task)
			if err != nil {
				t.Fatal(err)
			}
			defer result.release()
			if len(result.body) != 0 {
				t.Errorf("captured %d bytes for a bodyless response", len(result.body))
			}
			assertBodylessResponseWire(t, result, sample.wireLength)
		})
	}
}

func assertBodylessResponseWire(t *testing.T, result *response, wantLength string) {
	t.Helper()
	var wire bytes.Buffer
	q, _ := newDataQueue(1, 4096, func([]byte) {})
	w := newControlWriter(newDataWriter(q, &wire, 4096))
	if err := w.apply(controlCommand{kind: openStreamCommand, streamID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := w.writeResponse(1, result); err != nil {
		t.Fatal(err)
	}
	if err := w.data.buffer.Flush(); err != nil {
		t.Fatal(err)
	}
	reader := http2.NewFramer(nil, &wire)
	reader.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	frame, err := reader.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	head, ok := frame.(*http2.MetaHeadersFrame)
	if !ok || !head.StreamEnded() || wire.Len() != 0 || len(q.streams) != 0 {
		t.Fatalf("bodyless response did not finish with its headers: %v wire=%d streams=%d", frame, wire.Len(), len(q.streams))
	}
	var length string
	for _, field := range head.RegularFields() {
		if field.Name == "content-length" {
			length = field.Value
		}
	}
	if length != wantLength {
		t.Errorf("Content-Length = %q, want %q", length, wantLength)
	}
}

func TestHandlerReusesValidatedTarget(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	for _, path := range []string{"/", "//a%20b?q=1", "/?", "*"} {
		head := requestHead{method: "OPTIONS", authority: "example.test", path: path}
		if !head.validTarget("http") || head.target == nil {
			t.Fatal(path)
		}
		cached := *head.target
		c := &candidateConnection{conn: server, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != cached.Path || r.URL.RawPath != cached.RawPath || r.URL.RawQuery != cached.RawQuery || r.URL.ForceQuery != cached.ForceQuery {
				t.Error("target changed")
			}
			r.URL.Path = "/handler-mutated"
		})}
		if _, err := c.execute(&requestTask{ctx: context.Background(), request: &receivedRequest{head: head}}); err != nil {
			t.Fatal(err)
		}
		if head.target.Path != cached.Path {
			t.Fatal("handler mutated retained metadata")
		}
	}
}

func TestHandlerRejectsUnvalidatedTarget(t *testing.T) {
	c := &candidateConnection{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("unvalidated request reached handler") })}
	if _, err := c.execute(&requestTask{ctx: context.Background(), request: &receivedRequest{}}); !errors.Is(err, errResponse) {
		t.Fatal(err)
	}
}

func TestHandlerRejectsMalformedAuthorityBeforeBusiness(t *testing.T) {
	for _, authority := range []string{"localhost:bad", "["} {
		head := requestHead{method: "POST", authority: authority, path: "/"}
		if !head.validTarget("http") {
			t.Fatal("fixture must pass initial host-header validation", authority)
		}
		c := &candidateConnection{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("malformed authority reached business handler")
		})}
		result, err := c.execute(&requestTask{ctx: t.Context(), request: &receivedRequest{head: head}})
		if result != nil || err == nil || errors.Is(err, errResponse) {
			t.Fatal("expected request construction error, not recovered panic", authority, result, err)
		}
	}
}

func TestTaskBudgetWaitsForBothOwners(t *testing.T) {
	r, _ := newRequestReceiver(1, 16, 16)
	req := &receivedRequest{body: make([]byte, 3, 8)}
	r.streams[1], r.buffered = req, 8
	ctx, cancel := context.WithCancel(context.Background())
	task := &requestTask{id: 1, ctx: ctx, cancel: cancel, dispatched: true}
	c := &candidateConnection{receiver: r, tasks: map[uint32]*requestTask{1: task}}
	c.outputFinished(1)
	if r.buffered != 8 || ctx.Err() == nil {
		t.Fatal("released a running consumer")
	}
	task.done = true
	c.releaseTask(task)
	if r.buffered != 0 || len(c.tasks) != 0 {
		t.Fatal("completed consumer retained")
	}
	c.cancelTask(99)
	c.outputFinished(99)
}

func TestConnectionExitReleasesUnfinishedTaskResources(t *testing.T) {
	conn := &faultConn{}
	c := newCandidateConnection(t.Context(), conn, http.NotFoundHandler())
	budget, slots := &byteBudget{limit: 32}, &byteBudget{limit: 1}
	c.budget, c.receiver.budget, c.slots = budget, budget, slots
	if !budget.take(8) || !slots.take(1) {
		t.Fatal("fixture budget")
	}
	request := &receivedRequest{body: make([]byte, 3, 8)}
	c.receiver.streams[1], c.receiver.buffered = request, 8
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := &response{header: make(http.Header), limit: 8, budget: budget}
	if _, err := result.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	c.tasks[1] = &requestTask{id: 1, ctx: ctx, cancel: cancel, request: request, response: result, admitted: true}
	if err := c.run(); err == nil {
		t.Fatal("truncated opening was accepted")
	}
	if ctx.Err() == nil || !conn.closed.Load() {
		t.Fatal("unfinished task or connection remained active")
	}
	if len(c.tasks) != 0 || len(c.receiver.streams) != 0 || c.receiver.buffered != 0 {
		t.Fatal("unfinished request retained")
	}
	if budget.used.Load() != 0 || slots.used.Load() != 0 || result.body != nil {
		t.Fatal("connection exit leaked response or admission budget")
	}
}

func TestCancelTaskDoesNotReleaseRunningOwners(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	task := &requestTask{id: 1, ctx: ctx, cancel: cancel, dispatched: true}
	c := &candidateConnection{tasks: map[uint32]*requestTask{1: task}}
	c.cancelTask(1)
	c.cancelTask(1)
	if ctx.Err() == nil || c.tasks[1] != task || task.done || task.outputDone {
		t.Fatal("cancel released or completed a running owner")
	}
}

func TestReceiveCreditMailboxFailureCleansUpConnection(t *testing.T) {
	for _, closed := range []bool{false, true} {
		name, want := "full", errMailboxFull
		if closed {
			name, want = "closed", errQueueClosed
		}
		t.Run(name, func(t *testing.T) {
			conn := &faultConn{}
			c := newCandidateConnection(t.Context(), conn, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("request dispatched after window update failed")
			}))
			c.inbox, _ = newControlMailbox(1)
			budget, slots := &byteBudget{limit: 1024}, &byteBudget{limit: 1}
			c.budget, c.receiver.budget, c.slots = budget, budget, slots
			if err := c.receive(receiveHead(t, 1, "4", false), nil); err != nil {
				t.Fatal(err)
			}
			task := c.tasks[1]
			if closed {
				c.inbox.close()
			}
			if err := c.receive(nil, receiveData(t, 1, true, []byte("body"), nil)); !errors.Is(err, want) {
				t.Fatal(err)
			}
			if task.dispatched || len(c.jobs) != 0 || budget.used.Load() == 0 || slots.used.Load() != 1 {
				t.Fatal("failed request dispatched or ownership lost before cleanup")
			}
			if err := c.run(); err == nil {
				t.Fatal("missing opening accepted")
			}
			if task.ctx.Err() == nil || !conn.closed.Load() || len(c.tasks) != 0 || len(c.receiver.streams) != 0 {
				t.Fatal("failed connection retained a request")
			}
			if budget.used.Load() != 0 || slots.used.Load() != 0 || c.receiver.buffered != 0 {
				t.Fatal("failed window update leaked capacity or admission")
			}
		})
	}
}

func workerFixture(t *testing.T, handler http.Handler) (*candidateConnection, *requestTask, net.Conn) {
	t.Helper()
	server, peer := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = peer.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	c := newCandidateConnection(ctx, server, handler)
	head := requestHead{method: "POST", authority: "example.test", path: "/"}
	if !head.validTarget("http") {
		t.Fatal("invalid fixture target")
	}
	task := &requestTask{id: 1, ctx: ctx, cancel: cancel, dispatched: true, request: &receivedRequest{head: head}}
	c.tasks[1] = task
	c.jobs <- task
	close(c.jobs)
	t.Cleanup(func() { c.outputFinished(1) })
	return c, task, peer
}

func TestWorkerFailureCommands(t *testing.T) {
	for _, tc := range []struct {
		name    string
		code    http2.ErrCode
		handler http.HandlerFunc
	}{
		{"panic", http2.ErrCodeInternal, func(http.ResponseWriter, *http.Request) { panic("native failure") }},
		{"budget", http2.ErrCodeEnhanceYourCalm, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("data")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, task, _ := workerFixture(t, tc.handler)
			c.budget = &byteBudget{limit: 1}
			c.work()
			command, ok, err := c.inbox.poll()
			if err != nil || !ok || command.kind != abortStreamCommand || command.streamID != task.id || command.code != tc.code || command.response != nil {
				t.Fatal("failure became success or wrong termination", command, err)
			}
			if !task.done {
				t.Fatal("failed task remained running")
			}
			c.outputFinished(task.id)
			if len(c.tasks) != 0 || c.budget.used.Load() != 0 {
				t.Fatal("failed task retained resources")
			}
		})
	}
}

func TestWorkerClosedMailboxClosesConnection(t *testing.T) {
	c, task, peer := workerFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("data"))
	}))
	c.budget = &byteBudget{limit: 16}
	c.inbox.close()
	c.work()
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatal("failed mailbox did not close connection", err)
	}
	if !task.done || c.budget.used.Load() != 4 {
		t.Fatal("response ownership lost before connection cleanup")
	}
	c.outputFinished(task.id)
	if len(c.tasks) != 0 || c.budget.used.Load() != 0 {
		t.Fatal("response cleanup leaked budget")
	}
}

func TestDispatchQueueFullRetainsTaskUntilOutputCleanup(t *testing.T) {
	c := newCandidateConnection(t.Context(), &faultConn{}, http.NotFoundHandler())
	c.jobs = make(chan *requestTask)
	c.slots = &byteBudget{limit: 1}
	err := c.receive(receiveHead(t, 1, "", true), nil)
	var streamErr http2.StreamError
	if !errors.As(err, &streamErr) || streamErr.StreamID != 1 || streamErr.Code != http2.ErrCodeRefusedStream {
		t.Fatal("full dispatch queue did not reject stream", err)
	}
	task := c.tasks[1]
	if task == nil || task.dispatched || c.slots.used.Load() != 1 {
		t.Fatal("rejected dispatch lost task ownership")
	}
	c.outputFinished(1)
	if task.ctx.Err() == nil || len(c.tasks) != 0 || len(c.receiver.streams) != 0 || c.slots.used.Load() != 0 {
		t.Fatal("rejected dispatch leaked admission or request")
	}
}
