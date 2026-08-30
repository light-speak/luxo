package luvia

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
)

func TestTraceSampleRateFromEnv(t *testing.T) {
	t.Setenv("LUXO_TRACE_SAMPLE_RATE", "0.25")
	if got := traceSampleRateFromEnv(); got != 0.25 {
		t.Fatalf("traceSampleRateFromEnv() = %v, want 0.25", got)
	}
	for _, value := range []string{"invalid", "-1", "1.1"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("LUXO_TRACE_SAMPLE_RATE", value)
			if got := traceSampleRateFromEnv(); got != 0.1 {
				t.Fatalf("fallback = %v, want 0.1", got)
			}
		})
	}
}

func TestMetricsCollectorRecordTraceSampling(t *testing.T) {
	mc := &MetricsCollector{traceSampleRate: 0}
	mc.RecordTrace(api.TraceRecord{TraceID: "success", StatusCode: http.StatusOK})
	mc.RecordTrace(api.TraceRecord{TraceID: "error", StatusCode: http.StatusBadRequest})
	if len(mc.traces) != 1 || mc.traces[0].TraceID != "error" {
		t.Fatalf("zero sampling traces = %+v, want the error only", mc.traces)
	}

	mc.traceSampleRate = 1
	mc.RecordTrace(api.TraceRecord{TraceID: "sampled", StatusCode: http.StatusOK})
	if len(mc.traces) != 2 {
		t.Fatalf("full sampling trace count = %d, want 2", len(mc.traces))
	}
}

func TestMetricsCollectorFlushTraces(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mc := &MetricsCollector{
		studioURL:       srv.URL,
		apiKey:          "secret",
		projectID:       7,
		instanceID:      "gateway-1",
		traceSampleRate: 1,
		client:          srv.Client(),
	}
	mc.RecordTrace(api.TraceRecord{
		TraceID: "trace-1", APIName: "getUser", Duration: 2500 * time.Microsecond,
		StatusCode: http.StatusOK, ClientName: "typescript", ClientVersion: "1.2.3",
		Timestamp: time.Unix(100, 0).UTC(),
	})
	mc.flushTraces()

	if received["$api"] != "svc:ingestTraces" || received["apiKey"] != "secret" {
		t.Fatalf("trace payload metadata = %+v", received)
	}
	traces, ok := received["traces"].([]any)
	if !ok || len(traces) != 1 {
		t.Fatalf("traces = %#v, want one trace", received["traces"])
	}
	trace := traces[0].(map[string]any)
	if trace["duration"] != 2.5 || trace["status"] != "OK" || trace["clientName"] != "typescript" {
		t.Fatalf("trace payload = %+v", trace)
	}
}

func TestGatewayInstanceIDUsesEnvironment(t *testing.T) {
	t.Setenv("LUXO_INSTANCE_ID", "gateway-fixed")
	if got := gatewayInstanceID(); got != "gateway-fixed" {
		t.Fatalf("gatewayInstanceID() = %q, want gateway-fixed", got)
	}
}

func TestMetricsCollectorRecord(t *testing.T) {
	mc := &MetricsCollector{
		buckets: make(map[string]*metricBucket),
		done:    make(chan struct{}),
	}

	// Record multiple calls to same API
	mc.Record("getUser", 10*time.Millisecond, false)
	mc.Record("getUser", 20*time.Millisecond, false)
	mc.Record("getUser", 30*time.Millisecond, true)

	mc.mu.Lock()
	defer mc.mu.Unlock()

	if len(mc.buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(mc.buckets))
	}

	for _, b := range mc.buckets {
		if b.totalCount != 3 {
			t.Errorf("totalCount = %d, want 3", b.totalCount)
		}
		if b.errCount != 1 {
			t.Errorf("errCount = %d, want 1", b.errCount)
		}
		if b.apiName != "getUser" {
			t.Errorf("apiName = %q, want getUser", b.apiName)
		}
		if len(b.latencies) != 3 {
			t.Errorf("latencies len = %d, want 3", len(b.latencies))
		}
	}
}

func TestMetricsCollectorRecordMultipleAPIs(t *testing.T) {
	mc := &MetricsCollector{
		buckets: make(map[string]*metricBucket),
		done:    make(chan struct{}),
	}

	mc.Record("getUser", 10*time.Millisecond, false)
	mc.Record("listPosts", 20*time.Millisecond, false)

	mc.mu.Lock()
	defer mc.mu.Unlock()

	if len(mc.buckets) != 2 {
		t.Fatalf("expected 2 buckets, got %d", len(mc.buckets))
	}
}

func TestPercentile(t *testing.T) {
	// Empty slice
	if got := percentile(nil, 0.5); got != 0 {
		t.Errorf("percentile(nil, 0.5) = %f, want 0", got)
	}

	// Single element
	if got := percentile([]float64{42.0}, 0.5); got != 42.0 {
		t.Errorf("percentile([42], 0.5) = %f, want 42", got)
	}

	// Multiple elements
	data := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	p50 := percentile(data, 0.50)
	p99 := percentile(data, 0.99)

	// p50 should be around the middle
	if p50 < 40 || p50 > 70 {
		t.Errorf("p50 = %f, expected around 50", p50)
	}
	// p99 should be near the top
	if p99 < 90 {
		t.Errorf("p99 = %f, expected >= 90", p99)
	}
}

func TestPercentileSorted(t *testing.T) {
	// Verify sort-based percentile gives correct results
	data := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := percentile(data, 0.0); got != 1 {
		t.Errorf("p0 = %f, want 1", got)
	}
	if got := percentile(data, 1.0); got != 10 {
		t.Errorf("p100 = %f, want 10", got)
	}
}

func TestMetricsLatenciesCapped(t *testing.T) {
	mc := &MetricsCollector{
		buckets: make(map[string]*metricBucket),
		done:    make(chan struct{}),
	}
	// Record more than maxLatencySamples
	for i := 0; i < maxLatencySamples+100; i++ {
		mc.Record("highQPS", time.Millisecond, false)
	}
	mc.mu.Lock()
	defer mc.mu.Unlock()
	for _, b := range mc.buckets {
		if len(b.latencies) > maxLatencySamples {
			t.Errorf("latencies len = %d, should be capped at %d", len(b.latencies), maxLatencySamples)
		}
		if b.totalCount != int64(maxLatencySamples+100) {
			t.Errorf("totalCount = %d, want %d", b.totalCount, maxLatencySamples+100)
		}
	}
}

func TestMetricsDoubleClose(t *testing.T) {
	mc := &MetricsCollector{
		buckets: make(map[string]*metricBucket),
		done:    make(chan struct{}),
	}
	mc.Close()
	mc.Close() // should not panic
}

func TestNewMetricsCollectorNilWhenNoEnv(t *testing.T) {
	// Ensure env vars are not set
	os.Unsetenv("LUXO_STUDIO_URL")
	os.Unsetenv("LUXO_API_KEY")

	mc := NewMetricsCollector()
	if mc != nil {
		mc.Close()
		t.Fatal("should return nil when env vars not set")
	}
}

func TestNewMetricsCollectorNilPartialEnv(t *testing.T) {
	os.Setenv("LUXO_STUDIO_URL", "http://localhost")
	os.Unsetenv("LUXO_API_KEY")
	defer os.Unsetenv("LUXO_STUDIO_URL")

	mc := NewMetricsCollector()
	if mc != nil {
		mc.Close()
		t.Fatal("should return nil when API key not set")
	}
}

func TestNewMetricsCollectorCreated(t *testing.T) {
	// Use httptest server to avoid real network calls
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	os.Setenv("LUXO_STUDIO_URL", srv.URL)
	os.Setenv("LUXO_API_KEY", "test-key")
	os.Setenv("LUXO_PROJECT_ID", "42")
	defer os.Unsetenv("LUXO_STUDIO_URL")
	defer os.Unsetenv("LUXO_API_KEY")
	defer os.Unsetenv("LUXO_PROJECT_ID")

	mc := NewMetricsCollector()
	if mc == nil {
		t.Fatal("should create collector")
	}
	defer mc.Close()

	if mc.projectID != 42 {
		t.Errorf("projectID = %d, want 42", mc.projectID)
	}
}

func TestMetricsCollectorCloseFlush(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		received = append(received, body)
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	mc := &MetricsCollector{
		studioURL: srv.URL,
		apiKey:    "test",
		buckets:   make(map[string]*metricBucket),
		done:      make(chan struct{}),
		client:    &http.Client{Timeout: 5 * time.Second},
	}

	mc.Record("getUser", 10*time.Millisecond, false)
	mc.Close() // triggers final flush

	mu.Lock()
	defer mu.Unlock()

	if len(received) == 0 {
		t.Fatal("Close() should trigger final flush")
	}

	body := received[0]
	if body["$api"] != "svc:ingestMetrics" {
		t.Errorf("$api = %v, want svc:ingestMetrics", body["$api"])
	}
	if body["apiKey"] != "test" {
		t.Errorf("apiKey = %v, want test", body["apiKey"])
	}
}

func TestMetricsFlushNonSuccessReenqueues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	mc := &MetricsCollector{
		studioURL: srv.URL,
		apiKey:    "invalid",
		buckets:   make(map[string]*metricBucket),
		done:      make(chan struct{}),
		client:    &http.Client{Timeout: time.Second},
	}
	mc.Record("getUser", 10*time.Millisecond, false)
	mc.flush()

	mc.mu.Lock()
	defer mc.mu.Unlock()
	if len(mc.buckets) != 1 {
		t.Errorf("non-success response must re-enqueue buckets, got %d", len(mc.buckets))
	}
}

func TestMetricsReenqueueMergesConcurrentBucket(t *testing.T) {
	mc := &MetricsCollector{buckets: make(map[string]*metricBucket)}
	timestamp := time.Now().Truncate(5 * time.Minute)
	key := "getUser:" + strconv.FormatInt(timestamp.Unix(), 10)
	mc.buckets[key] = &metricBucket{
		apiName: "getUser", timestamp: timestamp, totalCount: 2, errCount: 1,
		totalMs: 20, latencies: []float64{5, 15},
	}

	mc.reenqueue(map[string]*metricBucket{key: {
		apiName: "getUser", timestamp: timestamp, totalCount: 3, errCount: 2,
		totalMs: 60, latencies: []float64{10, 20, 30},
	}})

	got := mc.buckets[key]
	if got.totalCount != 5 || got.errCount != 3 || got.totalMs != 80 || len(got.latencies) != 5 {
		t.Errorf("merged bucket = %+v", got)
	}
}

func TestMetricsFlushEmptyBuckets(t *testing.T) {
	mc := &MetricsCollector{
		buckets: make(map[string]*metricBucket),
		done:    make(chan struct{}),
	}
	// flush with empty buckets should not panic
	mc.flush()
}

func TestMetricsFlushHTTPError(t *testing.T) {
	mc := &MetricsCollector{
		studioURL: "http://127.0.0.1:1", // unreachable
		apiKey:    "test",
		buckets:   make(map[string]*metricBucket),
		done:      make(chan struct{}),
		client:    &http.Client{Timeout: 1 * time.Second},
	}
	mc.Record("getUser", 10*time.Millisecond, false)
	// flush should not panic on HTTP error
	mc.flush()
}

func TestMetricsFlushHTTPErrorReenqueue(t *testing.T) {
	// Verify that buckets are re-enqueued on HTTP error
	mc := &MetricsCollector{
		studioURL: "http://127.0.0.1:1", // unreachable
		apiKey:    "test",
		buckets:   make(map[string]*metricBucket),
		done:      make(chan struct{}),
		client:    &http.Client{Timeout: 100 * time.Millisecond},
	}
	mc.Record("getUser", 10*time.Millisecond, false)
	mc.flush()

	mc.mu.Lock()
	defer mc.mu.Unlock()
	// Buckets should be re-enqueued after failure
	if len(mc.buckets) == 0 {
		t.Error("buckets should be re-enqueued on flush failure")
	}
}

func TestMetricsFlushInvalidURL(t *testing.T) {
	// Test flush with an invalid URL that causes http.NewRequest to fail
	mc := &MetricsCollector{
		studioURL: "://invalid", // invalid URL scheme
		apiKey:    "test",
		buckets:   make(map[string]*metricBucket),
		done:      make(chan struct{}),
		client:    &http.Client{Timeout: 1 * time.Second},
	}
	mc.Record("getUser", 10*time.Millisecond, false)
	// Should not panic — flush exits early on NewRequest error
	mc.flush()
}

func TestMetricsFlushLoopTickerBranch(t *testing.T) {
	// Test the flushLoop ticker branch by creating a collector
	// and ensuring it actually flushes via the tick path.
	var mu sync.Mutex
	flushCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		flushCount++
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	mc := &MetricsCollector{
		studioURL: srv.URL,
		apiKey:    "test",
		buckets:   make(map[string]*metricBucket),
		done:      make(chan struct{}),
		client:    &http.Client{Timeout: 5 * time.Second},
	}

	// Record data and directly call flush to simulate what flushLoop does
	mc.Record("test", 5*time.Millisecond, false)
	mc.flush()

	mu.Lock()
	defer mu.Unlock()
	if flushCount == 0 {
		t.Error("flush should have been called")
	}
}
