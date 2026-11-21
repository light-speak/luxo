package rpc

import (
	"bytes"
	"context"
	"io"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
)

type exitingRPCMetrics struct {
	rpcMetricsRecorder
	exit bool
}

func (m *exitingRPCMetrics) Record(name string, duration time.Duration, failed bool) {
	m.rpcMetricsRecorder.Record(name, duration, failed)
	if m.exit {
		runtime.Goexit()
	}
}

type exitingRPCWriter struct{}

func (exitingRPCWriter) Write([]byte) (int, error) { runtime.Goexit(); return 0, nil }

func TestRPCAbnormalExitReleasesBufferAndRecordsHandler(t *testing.T) {
	for _, fault := range []string{"handler", "collector", "writer", "panic-control"} {
		t.Run(fault, func(t *testing.T) { testRPCAbnormalExit(t, fault) })
	}
}

func testRPCAbnormalExit(t *testing.T, fault string) {
	t.Helper()
	router := api.NewRouter()
	metrics := &exitingRPCMetrics{exit: fault == "collector"}
	replacement := &rpcMetricsRecorder{}
	router.SetMetricsCollector(metrics)
	if err := router.Registry.Register("value", 1); err != nil {
		t.Fatal(err)
	}
	var original *api.ResponseBuf
	router.Handle("value", func(_ context.Context, req *api.Request) error {
		original = req.Buf
		original.Identity = "private-owner"
		original.B = append(original.B, 84)
		if fault == "handler" {
			router.SetMetricsCollector(replacement)
			runtime.Goexit()
		}
		if fault == "panic-control" {
			panic("private-panic")
		}
		return nil
	})
	server := NewServer(router)
	defer server.Close()
	payload := encodeRequestEnvelope(requestKindCall, "", encodeCallRequest(1, nil, nil))
	var writer io.Writer = io.Discard
	if fault == "writer" {
		writer = exitingRPCWriter{}
	}
	runRPCAbnormalCall(t, server, writer, payload)
	if original == nil || original.Identity != nil {
		t.Error("response buffer retained its owner after abnormal exit")
	}
	wantError := fault == "handler" || fault == "panic-control"
	if len(metrics.calls) != 1 || metrics.calls[0].isError != wantError {
		t.Error("handler metrics missing, duplicated or misclassified", metrics.calls)
	}
	if len(replacement.calls) != 0 {
		t.Error("abnormal completion used a different collector snapshot")
	}
	fault = ""
	metrics.exit = false
	router.SetMetricsCollector(metrics)
	if err := server.processRequest(io.Discard, payload); err != nil {
		t.Fatal("later healthy request failed", err)
	}
	if len(metrics.calls) != 2 || metrics.calls[1].isError {
		t.Error("later healthy request was miscounted", metrics.calls)
	}
}

func runRPCAbnormalCall(t *testing.T, server *Server, writer io.Writer, payload []byte) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); _ = server.processRequest(writer, payload) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("abnormal handler did not unwind")
	}
}

type terminalRPCMetrics chan bool

func (m terminalRPCMetrics) Record(_ string, _ time.Duration, failed bool) { m <- failed }

func TestRPCAbnormalExitOverTCP(t *testing.T) {
	router := api.NewRouter()
	if err := router.Registry.Register("value", 1); err != nil {
		t.Fatal(err)
	}
	metrics := make(terminalRPCMetrics, 2)
	router.SetMetricsCollector(metrics)
	var calls atomic.Int64
	router.Handle("value", func(_ context.Context, req *api.Request) error {
		if calls.Add(1) == 1 {
			runtime.Goexit()
		}
		req.Buf.B = append(req.Buf.B, 84)
		return nil
	})
	server := NewServer(router)
	address := startTestRPCServer(t, server)
	payload := encodeRequestEnvelope(requestKindCall, "", encodeCallRequest(1, nil, nil))
	first := dialTestRPCConn(t, address)
	if err := initiateHandshake(t.Context(), first, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(first, payload); err != nil {
		t.Fatal(err)
	}
	if body, err := ReadFrame(first, nil); err == nil {
		t.Fatal("aborted call returned an LRPC success frame", body)
	}
	if !readAbnormalRPCMetric(t, metrics) {
		t.Fatal("aborted TCP call was not counted as failure")
	}
	second := dialTestRPCConn(t, address)
	if err := initiateHandshake(t.Context(), second, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(second, payload); err != nil {
		t.Fatal(err)
	}
	body, err := ReadFrame(second, nil)
	if err != nil || !bytes.Equal(body, []byte{statusOK, 84}) || readAbnormalRPCMetric(t, metrics) {
		t.Fatal("later TCP call failed", body, err)
	}
}

func readAbnormalRPCMetric(t *testing.T, metrics terminalRPCMetrics) bool {
	t.Helper()
	select {
	case failed := <-metrics:
		return failed
	case <-time.After(3 * time.Second):
		t.Fatal("handler completion was not recorded")
		return false
	}
}

func TestRPCAbnormalExitFinishesDebugSpan(t *testing.T) {
	router := api.NewRouter()
	if err := router.Registry.Register("value", 1); err != nil {
		t.Fatal(err)
	}
	var trace *api.DebugTraceSession
	router.Handle("value", func(ctx context.Context, _ *api.Request) error {
		trace = api.DebugTrace(ctx)
		runtime.Goexit()
		return nil
	})
	server := NewServer(router)
	defer server.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.processCallContext(context.Background(), io.Discard,
			requestEnvelope{body: encodeCallRequest(1, nil, nil), traceID: "aborted-handler"})
	}()
	<-done
	for _, span := range trace.Snapshot().Spans {
		if span.Name == "service.handler" {
			return
		}
	}
	t.Fatal("aborted handler span was not completed")
}
