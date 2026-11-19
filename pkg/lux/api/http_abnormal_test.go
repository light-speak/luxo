package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

type exitingHTTPMetrics struct {
	mockTraceMetrics
	exit bool
}

func (m *exitingHTTPMetrics) Record(name string, duration time.Duration, failed bool) {
	m.mockMetrics.Record(name, duration, failed)
	if m.exit {
		runtime.Goexit()
	}
}

type exitingHTTPWriter struct{ *httptest.ResponseRecorder }

func (w exitingHTTPWriter) Write([]byte) (int, error) { runtime.Goexit(); return 0, nil }

func TestHTTPAbnormalExitReleasesBufferAndRecordsHandler(t *testing.T) {
	for _, binaryMode := range []bool{false, true} {
		for _, fault := range []string{"handler", "collector", "writer", "error-data", "panic-control"} {
			t.Run(httpAbnormalMode(binaryMode)+"/"+fault, func(t *testing.T) {
				testHTTPAbnormalExit(t, binaryMode, fault)
			})
		}
	}
}

func httpAbnormalMode(binaryMode bool) string {
	if binaryMode {
		return "binary"
	}
	return "json"
}

func testHTTPAbnormalExit(t *testing.T, binaryMode bool, fault string) {
	t.Helper()
	metrics := &exitingHTTPMetrics{exit: fault == "collector"}
	router := newHTTPAbnormalRouter(t, metrics)
	replacement := &mockMetrics{}
	var original *ResponseBuf
	router.Handle("value", func(_ context.Context, req *Request) error {
		original = req.Buf
		original.Identity = "private-owner"
		original.B = codec.AppendSvarint(original.B, 42)
		switch fault {
		case "handler":
			router.SetMetricsCollector(replacement)
			runtime.Goexit()
		case "error-data":
			return errors.BadRequest.WithData(exitingWSErrorData{})
		case "panic-control":
			panic("private-panic")
		}
		return nil
	})
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	serveHTTPAbnormalCall(t, router, binaryMode, fault)
	if original == nil || original.Identity != nil {
		t.Error("response buffer retained its owner after abnormal exit")
	}
	checkHTTPAbnormalMetrics(t, fault, metrics, replacement)
	metrics.exit = false
	router.SetMetricsCollector(metrics)
	fault = ""
	healthy := httptest.NewRecorder()
	router.ServeHTTP(healthy, httpAbnormalRequest(binaryMode))
	if healthy.Code != 200 || len(metrics.calls) != 2 || metrics.calls[1].isError {
		t.Error("later healthy request failed", healthy.Code, metrics.calls)
	}
}

func serveHTTPAbnormalCall(t *testing.T, router *Router, binaryMode bool, fault string) {
	t.Helper()
	var writer http.ResponseWriter = httptest.NewRecorder()
	if fault == "writer" {
		writer = exitingHTTPWriter{httptest.NewRecorder()}
	}
	done := make(chan struct{})
	go func() { defer close(done); router.ServeHTTP(writer, httpAbnormalRequest(binaryMode)) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("abnormal handler did not unwind")
	}
}

func checkHTTPAbnormalMetrics(t *testing.T, fault string, metrics *exitingHTTPMetrics, replacement *mockMetrics) {
	t.Helper()
	wantError := fault == "handler" || fault == "error-data" || fault == "panic-control"
	if len(metrics.calls) != 1 || metrics.calls[0].isError != wantError {
		t.Error("handler metrics missing, duplicated or misclassified", metrics.calls)
	}
	if len(replacement.calls) != 0 {
		t.Error("abnormal completion used a different collector snapshot")
	}
	if fault == "handler" && (len(metrics.traces) != 1 || metrics.traces[0].StatusCode != 500) {
		t.Error("aborted handler did not export its failed trace", metrics.traces)
	}
}

func newHTTPAbnormalRouter(t *testing.T, metrics MetricsRecorder) *Router {
	t.Helper()
	router := NewRouter()
	router.SetMetricsCollector(metrics)
	if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "value", ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	return router
}

func TestHTTPAbnormalExitFinishesDebugSpan(t *testing.T) {
	router := newHTTPAbnormalRouter(t, nil)
	ctx, trace := WithDebugTrace(context.Background(), "aborted-handler")
	request := httpAbnormalRequest(true).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		router.executeHTTPHandler(httptest.NewRecorder(), request, &Request{API: "value"},
			func(context.Context, *Request) error { runtime.Goexit(); return nil }, true, nil, trace, time.Time{})
	}()
	<-done
	spans := trace.Snapshot().Spans
	if len(spans) != 1 || spans[0].Name != "handler.execute" {
		t.Fatal("aborted handler span was not completed", spans)
	}
}

func httpAbnormalRequest(binaryMode bool) *http.Request {
	body := []byte(`{"$api":"value"}`)
	if binaryMode {
		body = []byte{1, 0, 0}
	}
	request := httptest.NewRequest(http.MethodPost, "/luvia", bytes.NewReader(body))
	if binaryMode {
		request.Header.Set("X-Luxo-Mode", "binary")
	}
	return request
}

type terminalHTTPMetrics chan bool

func (m terminalHTTPMetrics) Record(_ string, _ time.Duration, failed bool) { m <- failed }

func TestHTTPAbnormalExitOverTCP(t *testing.T) {
	for _, binaryMode := range []bool{false, true} {
		t.Run(httpAbnormalMode(binaryMode), func(t *testing.T) {
			metrics := make(terminalHTTPMetrics, 2)
			router := newHTTPAbnormalRouter(t, metrics)
			var calls atomic.Int64
			router.Handle("value", func(_ context.Context, req *Request) error {
				if calls.Add(1) == 1 {
					runtime.Goexit()
				}
				req.Buf.B = codec.AppendSvarint(req.Buf.B, 42)
				return nil
			})
			server := httptest.NewServer(router)
			defer server.Close()
			client := server.Client()
			client.Timeout = 3 * time.Second
			if response, err := client.Do(httpAbnormalTCPRequest(binaryMode, server.URL)); err == nil {
				response.Body.Close()
				t.Fatal("aborted handler returned a successful HTTP response")
			}
			if !<-metrics {
				t.Fatal("aborted TCP call was not counted as failure")
			}
			response, err := client.Do(httpAbnormalTCPRequest(binaryMode, server.URL))
			if err != nil {
				t.Fatal("server did not recover for the next connection", err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			want := `{"data":42}`
			if binaryMode {
				want = string([]byte{84})
			}
			if err != nil || response.StatusCode != 200 || string(body) != want || <-metrics {
				t.Fatal("later TCP call failed", response.StatusCode, body, err)
			}
		})
	}
}

func httpAbnormalTCPRequest(binaryMode bool, url string) *http.Request {
	request := httpAbnormalRequest(binaryMode)
	request.URL.Scheme = "http"
	request.URL.Host = url[len("http://"):]
	request.RequestURI = ""
	return request
}
