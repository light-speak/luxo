package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type blockedLogWriter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	buf     bytes.Buffer
}

func (w *blockedLogWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return w.buf.Write(p)
}

func TestAsyncRequestLogsBoundedDrain(t *testing.T) {
	w := &blockedLogWriter{entered: make(chan struct{}), release: make(chan struct{})}
	l := newAsyncRequestLogs(w, 1)
	l.submit(logBuffer("first"))
	<-w.entered
	l.submit(logBuffer("second"))
	l.submit(logBuffer("dropped"))
	if l.dropped.Load() != 1 {
		t.Fatal("full queue did not count its drop")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("close: %v", err)
	}
	l.submit(logBuffer("closed"))
	close(w.release)
	if err := l.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := w.buf.String(); got != "firstsecond" {
		t.Fatalf("got %q", got)
	}
	if l.dropped.Load() != 2 {
		t.Fatal("closed queue did not count its drop")
	}
}

func logBuffer(s string) *[]byte {
	b := requestLogBuffers.Get().(*[]byte)
	*b = append((*b)[:0], s...)
	return b
}

type shortLogWriter struct{}

func (shortLogWriter) Write(p []byte) (int, error) { return 0, nil }

func TestAsyncRequestLogsWriteFailure(t *testing.T) {
	l := newAsyncRequestLogs(shortLogWriter{}, 1)
	l.submit(logBuffer("line"))
	if err := l.close(context.Background()); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("close: %v", err)
	}
	if l.failures.Load() != 1 {
		t.Fatal("missing write failure count")
	}
}

func TestRouterAsyncRequestLogsSampling(t *testing.T) {
	var out bytes.Buffer
	r := NewRouterWithOptions(RouterOptions{RequestLogging: true, LogWriter: &out, RequestLogSampleEvery: 2})
	for range 4 {
		r.logRequest("success", time.Millisecond, nil)
	}
	r.logRequest("failed", time.Millisecond, errors.New("secret"))
	if err := r.CloseRequestLogs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if bytes.Count(out.Bytes(), []byte("success")) != 2 || !bytes.Contains(out.Bytes(), []byte("failed")) {
		t.Fatalf("logs: %q", out.String())
	}
	if bytes.Contains(out.Bytes(), []byte("secret")) {
		t.Fatal("raw error leaked")
	}
}

func TestAsyncRequestLogsConcurrentClose(t *testing.T) {
	var output bytes.Buffer
	l := newAsyncRequestLogs(&output, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				l.submit(logBuffer("entry"))
			}
		})
	}
	for range 8 {
		wg.Go(func() { _ = l.close(context.Background()) })
	}
	wg.Wait()
	if err := l.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	written := bytes.Count(output.Bytes(), []byte("entry"))
	if written*len("entry") != output.Len() {
		t.Fatal("partial or corrupted log record")
	}
	if uint64(written)+l.dropped.Load() != 800 || l.failures.Load() != 0 {
		t.Fatalf("written=%d dropped=%d failures=%d", written, l.dropped.Load(), l.failures.Load())
	}
}

func TestAsyncRequestLogsOversize(t *testing.T) {
	l := newAsyncRequestLogs(io.Discard, 1)
	b := requestLogBuffers.Get().(*[]byte)
	*b = make([]byte, maxRequestLogBytes+1)
	l.submit(b)
	if err := l.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if l.dropped.Load() != 1 {
		t.Fatal("oversized record was not rejected")
	}
}

func TestAsyncParseLogsRejectOversizeBeforeQueueing(t *testing.T) {
	var out bytes.Buffer
	r := NewRouterWithOptions(RouterOptions{RequestLogging: true, RequestLogSampleEvery: 1, LogWriter: &out})
	r.logParseFailure("binary", strings.Repeat("x", maxRequestLogBytes))
	r.logParseFailure(strings.Repeat("m", maxRequestLogBytes-256), "x")
	r.writeParamStructure(0, strings.Repeat("p", maxRequestLogBytes), "String", true)
	r.logRequest(strings.Repeat("a", maxRequestLogBytes), time.Millisecond, nil)
	r.logParseFailure("binary", "invalid request")
	if err := r.CloseRequestLogs(t.Context()); err != nil {
		t.Fatal(err)
	}
	dropped, failures := r.RequestLogStats()
	if dropped != 4 || failures != 0 {
		t.Fatal("oversize rejection not accounted for", dropped, failures)
	}
	if strings.Count(out.String(), "\n") != 1 || !strings.Contains(out.String(), "invalid request") {
		t.Fatal("oversize parse log escaped or valid log was lost")
	}
}

func TestRouterDisabledLogClose(t *testing.T) {
	r := NewRouterWithOptions(RouterOptions{})
	if err := r.CloseRequestLogs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if drops, failures := r.RequestLogStats(); drops != 0 || failures != 0 {
		t.Fatal("disabled log stats")
	}
}

// Keep full-queue measurements separate from successful delivery throughput.
func BenchmarkRequestLogBoundary(b *testing.B) {
	for _, mode := range []string{"disabled", "unsampled", "synchronous", "full"} {
		b.Run(mode, func(b *testing.B) {
			r := &Router{requestLogging: mode != "disabled", logWriter: io.Discard}
			if mode == "unsampled" {
				r.logSampleEvery = ^uint64(0)
			}
			if mode == "full" {
				// No consumer: every submission must be counted as dropped.
				r.asyncLogs = &asyncRequestLogs{queue: make(chan *[]byte)}
			}
			b.ReportAllocs()
			for b.Loop() {
				r.logRequest("getUser", time.Millisecond, nil)
			}
			if mode == "full" && r.asyncLogs.dropped.Load() != uint64(b.N) {
				b.Fatal("missing drop count")
			}
		})
	}
}

func BenchmarkRequestLogDelivered(b *testing.B) {
	l := newAsyncRequestLogs(io.Discard, requestLogQueueCapacity)
	b.Cleanup(func() { _ = l.close(context.Background()) })
	b.ReportAllocs()
	for b.Loop() {
		// Blocking submission is benchmark-only: include the worker throughput,
		// and never claim dropped records as successfully processed records.
		l.queue <- logBuffer("fixed request log\n")
	}
	if err := l.close(context.Background()); err != nil {
		b.Fatal(err)
	}
	if l.dropped.Load() != 0 || l.failures.Load() != 0 {
		b.Fatal("delivery failed")
	}
}

func TestHTTPCompletionSamplingKeepsErrorsAndSequence(t *testing.T) {
	var out bytes.Buffer
	r := &Router{requestLogging: true, logWriter: &out, logSampleEvery: 2}
	started := time.Now()
	r.logHTTPCompletion("absent", 0, time.Time{}, nil)
	r.logHTTPCompletion("skip", 0, started, nil)
	r.logHTTPCompletion("failed", 0, started, errors.New("private"))
	r.logHTTPCompletion("keep", 0, started, nil)
	if r.logSequence.Load() != 2 || strings.Contains(out.String(), "skip") || strings.Contains(out.String(), "absent") {
		t.Fatal("sampling sequence changed", out.String())
	}
	if strings.Count(out.String(), "server=") != 2 || !strings.Contains(out.String(), "failed") || !strings.Contains(out.String(), "keep") || strings.Contains(out.String(), "private") {
		t.Fatal("completion/error log changed", out.String())
	}
}

func BenchmarkHTTPCompletionSampling(b *testing.B) {
	for _, every := range []uint64{0, 1, 100, ^uint64(0)} {
		b.Run(strconv.FormatUint(every, 10), func(b *testing.B) {
			r := &Router{requestLogging: every != 0, logWriter: io.Discard, logSampleEvery: every}
			started := time.Now()
			b.ReportAllocs()
			for b.Loop() {
				r.logHTTPCompletion("getUser", time.Millisecond, started, nil)
			}
		})
	}
}

func TestConcurrentHTTPAndDirectLogSampling(t *testing.T) {
	var out bytes.Buffer
	r := NewRouterWithOptions(RouterOptions{RequestLogging: true, LogWriter: &out, RequestLogSampleEvery: 16})
	t.Cleanup(func() { _ = r.CloseRequestLogs(context.Background()) })
	started := time.Now()
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			for i := range 64 {
				if i%2 == 0 {
					r.logHTTPCompletion("sampled-success", time.Millisecond, started, nil)
				} else {
					r.logRequest("sampled-success", time.Millisecond, nil)
				}
			}
			r.logHTTPCompletion("always-error", time.Millisecond, started, errors.New("private-cause"))
		})
	}
	workers.Wait()
	if err := r.CloseRequestLogs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.logSequence.Load() != 1024 || strings.Count(out.String(), "sampled-success") != 64 || strings.Count(out.String(), "always-error") != 16 {
		t.Fatal("concurrent sampling lost or duplicated records", r.logSequence.Load(), out.String())
	}
	if strings.Contains(out.String(), "private-cause") {
		t.Fatal("raw error leaked")
	}
	if dropped, failed := r.RequestLogStats(); dropped != 0 || failed != 0 {
		t.Fatal("fixture must deliver all 80 selected logs", dropped, failed)
	}
}

func TestRequestLogDefaultAndExplicitSampling(t *testing.T) {
	for _, tc := range []struct {
		name  string
		every uint64
		want  int
	}{{"default", 0, 2}, {"full", 1, 200}, {"custom", 10, 20}} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			r := NewRouterWithOptions(RouterOptions{RequestLogging: true, SynchronousRequestLogs: true, LogWriter: &out, RequestLogSampleEvery: tc.every})
			for range 200 {
				r.logRequest("success", time.Millisecond, nil)
			}
			r.logRequest("failed", time.Millisecond, errors.New("failure"))
			if got := bytes.Count(out.Bytes(), []byte("success")); got != tc.want {
				t.Fatalf("success logs: got %d, want %d", got, tc.want)
			}
			if bytes.Count(out.Bytes(), []byte("failed")) != 1 {
				t.Fatal("error was sampled")
			}
		})
	}
}

func TestRequestLogEnvironmentFullOptIn(t *testing.T) {
	for _, tc := range []struct {
		enabled, full string
		want          bool
		every         uint64
	}{
		{"", "", false, 100}, {"true", "", true, 100},
		{"true", "true", true, 1}, {"", "true", false, 1}, {"true", "false", true, 100},
	} {
		t.Setenv("LOG_REQUESTS", tc.enabled)
		t.Setenv("LOG_REQUESTS_FULL", tc.full)
		r := NewRouter()
		if r.requestLogging != tc.want || r.logSampleEvery != tc.every {
			t.Errorf("enabled=%q full=%q: logging=%v every=%d", tc.enabled, tc.full, r.requestLogging, r.logSampleEvery)
		}
		if err := r.CloseRequestLogs(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRouterOptionsEnvironmentPreservesExplicitOverrides(t *testing.T) {
	t.Setenv("LOG_REQUESTS", "true")
	t.Setenv("LOG_REQUESTS_FULL", "true")
	t.Setenv("LOG_LEVEL", "debug")
	options := RouterOptionsFromEnv()
	if !options.RequestLogging || !options.DebugParamStructure || options.RequestLogSampleEvery != 1 {
		t.Fatal("environment logging settings lost")
	}
	options.RequestLogSampleEvery = 10
	options.SynchronousRequestLogs = true
	r := NewRouterWithOptions(options)
	if r.logSampleEvery != 10 {
		t.Fatal("explicit sampling override lost")
	}
}
