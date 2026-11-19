package api

import (
	"context"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/google/uuid"
)

func TestTraceMiddlewareUUIDFailure(t *testing.T) {
	if !traceUUIDFailureChild(t) {
		return
	}
	uuid.DisableRandPool()
	uuid.SetRand(iotest.ErrReader(io.ErrUnexpectedEOF))
	t.Cleanup(func() { uuid.SetRand(nil) })
	handler := TraceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("business"))
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/luvia", nil))
	if response.Code != http.StatusCreated || response.Body.String() != "business" || response.Header().Get("X-Luxo-Trace-ID-Failed") != "1" || response.Header().Get("X-Trace-Id") != "" {
		t.Fatal("trace ID failure changed business response or lacked marker", response)
	}
	request := httptest.NewRequest(http.MethodPost, "/luvia", nil)
	request.Header.Set("X-Request-Id", "existing")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || response.Header().Get("X-Trace-Id") != "existing" || response.Header().Get("X-Luxo-Trace-ID-Failed") != "" {
		t.Fatal("incoming request ID consulted failed randomness", response)
	}
	for _, envelope := range []bool{false, true} {
		testTraceIDFailureWithDebugResponse(t, envelope)
	}
	uuid.SetRand(nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/luvia", nil))
	if response.Header().Get("X-Trace-Id") == "" || response.Header().Get("X-Luxo-Trace-ID-Failed") != "" {
		t.Fatal("trace generation did not recover", response)
	}
}

// Keep the global UUID test reader out of the parent test process.
func traceUUIDFailureChild(t *testing.T) bool {
	t.Helper()
	if os.Getenv("LUXO_TRACE_UUID_FAILURE_TEST") != "1" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command(executable, "-test.run=^TestTraceMiddlewareUUIDFailure$", "-test.v")
		command.Env = append(os.Environ(), "LUXO_TRACE_UUID_FAILURE_TEST=1")
		if directory := flag.Lookup("test.gocoverdir"); directory != nil && directory.Value.String() != "" {
			command.Args = append(command.Args, "-test.gocoverdir="+directory.Value.String())
		}
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatal(string(output), err)
		}
		return false
	}
	return true
}

func testTraceIDFailureWithDebugResponse(t *testing.T, envelope bool) {
	t.Helper()
	router := NewRouter()
	router.SetDevMode(true)
	router.Handle("ping", func(_ context.Context, req *Request) error { req.Buf.AppendInt(7); return nil })
	request := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(`{"$api":"ping"}`))
	request.Header.Set(DebugTraceRequestHeader, "true")
	if envelope {
		request.Header.Set(DebugTraceEnvelopeHeader, "true")
	}
	response := httptest.NewRecorder()
	TraceMiddleware(router).ServeHTTP(response, request)
	body := response.Body.Bytes()
	var trace DebugTraceSnapshot
	var err error
	if envelope {
		body, trace, err = DecodeDebugResponseEnvelope(body)
	} else {
		trace, err = DecodeDebugTraceHeader(response.Header().Get(DebugTraceResponseHeader))
	}
	if err != nil || !trace.EncodingFailed || response.Code != http.StatusOK || string(body) != `{"data":7}` {
		t.Fatal("diagnostic ID failure changed payload", string(body), trace, err)
	}
}
