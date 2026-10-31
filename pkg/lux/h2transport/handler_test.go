package h2transport

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

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
