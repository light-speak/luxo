package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/errors"
)

type malformedObservedData struct{}

func (malformedObservedData) AppendJSON(dst []byte) ([]byte, error) { return append(dst, '{'), nil }
func (malformedObservedData) AppendText(dst []byte, _ string) ([]byte, bool) {
	return dst, false
}

type countedObservedError struct {
	calls int
	value *errors.AppError
}

func (*countedObservedError) Error() string { return "private error callback" }
func (e *countedObservedError) As(target any) bool {
	e.calls++
	if e.calls > 1 {
		panic("error callback was invoked more than once")
	}
	if p, ok := target.(**errors.AppError); ok {
		*p = e.value
		return true
	}
	return false
}

func TestHTTPErrorObservationMatchesWireOutcome(t *testing.T) {
	for _, binaryMode := range []bool{false, true} {
		for _, sample := range []struct {
			name string
			err  error
		}{
			{"typed-nil", (*errors.AppError)(nil)},
			{"zero-code", errors.New("private-invalid", 0, "private")},
			{"success-code", errors.New("private-invalid", 200, "private")},
			{"invalid-data", errors.BadRequest.WithData(malformedObservedData{})},
			{"invalid-text", errors.New(string([]byte{0xff}), 400, "private")},
		} {
			t.Run(httpAbnormalMode(binaryMode)+"/"+sample.name, func(t *testing.T) {
				testHTTPErrorObservation(t, binaryMode, sample.err, 500, "Internal")
			})
		}
	}
}

func TestHTTPErrorCallbacksRunOnceBeforeObservation(t *testing.T) {
	for _, binaryMode := range []bool{false, true} {
		t.Run(httpAbnormalMode(binaryMode), func(t *testing.T) {
			err := &countedObservedError{value: errors.New("DeclaredFailure", 418, "declared failure")}
			testHTTPErrorObservation(t, binaryMode, err, 418, "DeclaredFailure")
			if err.calls != 1 {
				t.Fatal("observation reentered the native error callback", err.calls)
			}
		})
	}
}

type countedObservedUnwrap struct {
	calls int
	value *errors.AppError
}

func (*countedObservedUnwrap) Error() string { return "private unwrap callback" }
func (e *countedObservedUnwrap) Unwrap() error {
	e.calls++
	if e.calls > 1 {
		panic("unwrap callback was invoked more than once")
	}
	return e.value
}

func TestHTTPErrorUnwrapRunsOnceBeforeObservation(t *testing.T) {
	for _, binaryMode := range []bool{false, true} {
		t.Run(httpAbnormalMode(binaryMode), func(t *testing.T) {
			err := &countedObservedUnwrap{value: errors.New("DeclaredFailure", 418, "declared failure")}
			testHTTPErrorObservation(t, binaryMode, err, 418, "DeclaredFailure")
			if err.calls != 1 {
				t.Fatal("observation reentered the native unwrap callback", err.calls)
			}
		})
	}
}

func testHTTPErrorObservation(t *testing.T, binaryMode bool, source error, code int, name string) {
	t.Helper()
	var logs bytes.Buffer
	router := NewRouterWithOptions(RouterOptions{RequestLogging: true, RequestLogSampleEvery: 100,
		SynchronousRequestLogs: true, LogWriter: &logs})
	metrics := &mockTraceMetrics{}
	router.SetMetricsCollector(metrics)
	if err := router.Registry.Register("value", 1); err != nil {
		t.Fatal(err)
	}
	router.Handle("value", func(context.Context, *Request) error { return source })
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if p := recover(); p != nil {
			t.Error("error observation panicked instead of completing the error response")
		}
	}()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httpAbnormalRequest(binaryMode))
	checkHTTPObservedErrorResponse(t, response, binaryMode, code, name)
	if len(metrics.calls) != 1 || !metrics.calls[0].isError || len(metrics.traces) != 1 || metrics.traces[0].StatusCode != code {
		t.Error("wire and observation diverged", metrics.calls, metrics.traces)
	}
	if !strings.Contains(logs.String(), name) || strings.Contains(logs.String(), "private") {
		t.Error("completion log did not use the canonical error name", logs.String())
	}
}

func checkHTTPObservedErrorResponse(t *testing.T, response *httptest.ResponseRecorder, binaryMode bool, code int, name string) {
	t.Helper()
	if response.Code != code {
		t.Fatal("unexpected HTTP status", response.Code)
	}
	if binaryMode {
		value, err := DecodeBinaryError(response.Body.Bytes(), 0)
		if err != nil || value.Code != code || value.Name != name {
			t.Fatal("unexpected binary error", value, err)
		}
		return
	}
	var value struct {
		Code int    `json:"code"`
		Name string `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil || value.Code != code || value.Name != name {
		t.Fatal("unexpected JSON error", value)
	}
}

type exitingObservedErrorWriter struct {
	*httptest.ResponseRecorder
	call   *httpHandlerCall
	output **ResponseBuf
}

func (w exitingObservedErrorWriter) Write([]byte) (int, error) {
	*w.output = w.call.buf
	w.call.buf.Identity = "error-output-owner"
	runtime.Goexit()
	return 0, nil
}

func TestHTTPErrorWriteExitReleasesPreparedResponse(t *testing.T) {
	for _, binaryMode := range []bool{false, true} {
		t.Run(httpAbnormalMode(binaryMode), func(t *testing.T) {
			metrics := &mockTraceMetrics{}
			call := httpHandlerCall{router: NewRouter(), req: &Request{API: "value"}, metrics: metrics, buf: GetBuf()}
			request := httpAbnormalRequest(binaryMode)
			var output *ResponseBuf
			writer := exitingObservedErrorWriter{httptest.NewRecorder(), &call, &output}
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer call.finish(request)
				call.fail(writer, request, binaryMode, nil, time.Time{}, errors.BadRequest)
			}()
			<-done
			if output == nil || output.Identity != nil || call.buf != nil {
				t.Fatal("error output retained ownership after writer exit")
			}
			if len(metrics.calls) != 1 || !metrics.calls[0].isError || len(metrics.traces) != 1 || metrics.traces[0].StatusCode != 400 {
				t.Fatal("writer exit duplicated or changed the prepared error", metrics.calls, metrics.traces)
			}
		})
	}
}

func TestHTTPErrorLogFallbackIsAlwaysAnError(t *testing.T) {
	var logs bytes.Buffer
	router := NewRouterWithOptions(RouterOptions{RequestLogging: true, RequestLogSampleEvery: 100,
		SynchronousRequestLogs: true, LogWriter: &logs})
	router.logHTTPFailure("value", time.Millisecond, time.Now(), "")
	if !strings.Contains(logs.String(), "Error") || router.logSequence.Load() != 0 {
		t.Fatal("empty error name was treated as sampled success", logs.String())
	}
	logs.Reset()
	bounded := NewRouterWithOptions(RouterOptions{RequestLogging: true, LogWriter: &logs})
	bounded.logHTTPFailure(strings.Repeat("x", maxRequestLogBytes), time.Millisecond, time.Now(), "Internal")
	if err := bounded.CloseRequestLogs(t.Context()); err != nil {
		t.Fatal(err)
	}
	dropped, failures := bounded.RequestLogStats()
	if logs.Len() != 0 || dropped != 1 || failures != 0 {
		t.Fatal("oversized canonical log bypassed the record limit")
	}
}

func TestSafeLogErrorNameRejectsInvalidNativeErrors(t *testing.T) {
	for _, sample := range []struct {
		source error
		name   string
	}{
		{nil, "Internal"},
		{(*errors.AppError)(nil), "Internal"},
		{errors.New("Invalid", 200, "private"), "Internal"},
		{errors.New("", 400, "private"), "Internal"},
		{errors.BadRequest, "BadRequest"},
	} {
		if got := safeLogErrorName(sample.source); got != sample.name {
			t.Error("unexpected log classification", got, sample.name)
		}
	}
}
