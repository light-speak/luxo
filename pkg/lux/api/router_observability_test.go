package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

func TestRouterDebugLogsParamStructureWithoutValues(t *testing.T) {
	const secret = "DO_NOT_LOG_THIS_SECRET"
	var logs bytes.Buffer
	rt := NewRouterWithOptions(RouterOptions{
		RequestLogging:      true,
		DebugParamStructure: true,
		LogWriter:           &logs,
	})
	rt.Registry.Register("login", 1)
	rt.Registry.RegisterParams("login", []ParamMeta{
		{Name: "username", Type: "String", FieldID: 1},
		{Name: "password", Type: "String", FieldID: 2},
	})
	rt.Handle("login", func(context.Context, *Request) error {
		return fmt.Errorf("invalid credentials")
	})

	var enc codec.Encoder
	body := codec.AppendVarint(nil, 1)
	body = codec.AppendVarint(body, 0)
	enc.WriteFieldString(1, "admin")
	enc.WriteFieldString(2, secret)
	enc.WriteEnd()
	body = append(body, enc.Bytes()...)
	req := httptest.NewRequest(http.MethodPost, "/luvia", bytes.NewReader(body))
	req.Header.Set("X-Luxo-Mode", "binary")
	rt.ServeHTTP(httptest.NewRecorder(), req)

	if err := rt.CloseRequestLogs(context.Background()); err != nil {
		t.Fatal(err)
	}
	output := logs.String()
	if strings.Contains(output, secret) {
		t.Fatalf("debug log leaked parameter value: %s", output)
	}
	if !strings.Contains(output, "password String present=true") {
		t.Fatalf("debug log missing parameter structure: %s", output)
	}
}

func TestRouterDebugLogsJSONParamStructureWithoutValues(t *testing.T) {
	const secret = "DO_NOT_LOG_THIS_JSON_SECRET"
	var logs bytes.Buffer
	rt := NewRouterWithOptions(RouterOptions{
		RequestLogging:      true,
		DebugParamStructure: true,
		LogWriter:           &logs,
	})
	rt.Registry.RegisterParams("login", []ParamMeta{
		{Name: "username", Type: "String", FieldID: 1},
		{Name: "password", Type: "String", FieldID: 2},
	})
	rt.Handle("login", func(context.Context, *Request) error {
		return fmt.Errorf("invalid credentials")
	})
	req := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(
		`{"$api":"login","username":"admin","password":"`+secret+`"}`,
	))
	rt.ServeHTTP(httptest.NewRecorder(), req)

	if err := rt.CloseRequestLogs(context.Background()); err != nil {
		t.Fatal(err)
	}
	output := logs.String()
	if strings.Contains(output, secret) {
		t.Fatalf("debug log leaked JSON parameter value: %s", output)
	}
	if !strings.Contains(output, "password String present=true") {
		t.Fatalf("debug log missing JSON parameter structure: %s", output)
	}
}

func TestRouterLoggingDisabledWritesNothing(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"success", `{"$api":"health"}`, http.StatusOK},
		{"parse_error", `{`, http.StatusBadRequest},
		{"handler_error", `{"$api":"failing"}`, http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			rt := NewRouterWithOptions(RouterOptions{LogWriter: &logs, DebugParamStructure: true})
			rt.Handle("health", func(_ context.Context, req *Request) error {
				req.Buf.AppendString("true")
				return nil
			})
			rt.Handle("failing", func(context.Context, *Request) error { return luxerrors.New("Internal", 500, "failed") })
			req := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(test.body))
			response := httptest.NewRecorder()
			rt.ServeHTTP(response, req)
			if response.Code != test.status {
				t.Fatalf("status=%d, want %d: %s", response.Code, test.status, response.Body.String())
			}
			if logs.Len() != 0 {
				t.Fatalf("disabled logger wrote %q", logs.String())
			}
		})
	}
}

func TestRouterLogsStableErrorNameWithoutCause(t *testing.T) {
	const secret = "DO_NOT_LOG_THIS_ERROR_CAUSE"
	var logs bytes.Buffer
	rt := NewRouterWithOptions(RouterOptions{RequestLogging: true, SynchronousRequestLogs: true, LogWriter: &logs})
	rt.Handle("failing", func(context.Context, *Request) error {
		return luxerrors.Wrap(fmt.Errorf("database rejected %s", secret))
	})
	req := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(`{"$api":"failing"}`))
	rt.ServeHTTP(httptest.NewRecorder(), req)

	if strings.Contains(logs.String(), secret) {
		t.Fatalf("request log leaked error cause: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "Internal") {
		t.Fatalf("request log missing stable error name: %s", logs.String())
	}
}

func BenchmarkRouterObservabilityDisabled(b *testing.B) {
	benchmarkRouterObservability(b, RouterOptions{})
}

func BenchmarkRouterObservabilityEnabled(b *testing.B) {
	benchmarkRouterObservability(b, RouterOptions{RequestLogging: true, SynchronousRequestLogs: true, LogWriter: io.Discard})
}

func BenchmarkRouterObservabilityAsync(b *testing.B) {
	benchmarkRouterObservability(b, RouterOptions{RequestLogging: true, LogWriter: io.Discard})
}

func BenchmarkRouterObservabilitySampled(b *testing.B) {
	benchmarkRouterObservability(b, RouterOptions{RequestLogging: true, RequestLogSampleEvery: 100, LogWriter: io.Discard})
}

func benchmarkRouterObservability(b *testing.B, options RouterOptions) {
	b.Helper()
	rt := NewRouterWithOptions(options)
	rt.Handle("health", func(_ context.Context, req *Request) error {
		req.Buf.AppendString("true")
		return nil
	})
	body := []byte(`{"$api":"health"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		req := httptest.NewRequest(http.MethodPost, "/luvia", bytes.NewReader(body))
		rt.ServeHTTP(httptest.NewRecorder(), req)
	}
	b.StopTimer()
	if err := rt.CloseRequestLogs(context.Background()); err != nil {
		b.Fatal(err)
	}
	drops, _ := rt.RequestLogStats()
	b.ReportMetric(float64(drops)/float64(b.N), "drops/request")
}
