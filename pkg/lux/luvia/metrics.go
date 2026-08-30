package luvia

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
)

// maxLatencySamples caps the number of latency samples per bucket to prevent
// unbounded memory growth under high QPS (10k req/s = ~2.4MB per bucket max).
const (
	maxLatencySamples = 30000
	maxPendingTraces  = 10000
)

// MetricsCollector aggregates request metrics in memory and periodically
// flushes them to a Luxo Studio instance via the ingestMetrics API.
type MetricsCollector struct {
	studioURL  string
	apiKey     string
	projectID  int
	instanceID string
	nodeType   string

	mu      sync.Mutex
	buckets map[string]*metricBucket // key: "apiName:minute"
	traces  []api.TraceRecord
	done    chan struct{}
	closed  bool

	traceSampleRate float64

	client *http.Client // dedicated client with timeout
}

type metricBucket struct {
	apiName    string
	timestamp  time.Time
	totalCount int64
	errCount   int64
	totalMs    float64
	latencies  []float64 // for percentile calculation, capped at maxLatencySamples
}

// NewMetricsCollector creates a collector from environment variables.
// Returns nil if LUXO_STUDIO_URL or LUXO_API_KEY is not set.
func NewMetricsCollector() *MetricsCollector {
	studioURL := os.Getenv("LUXO_STUDIO_URL")
	apiKey := os.Getenv("LUXO_API_KEY")
	if studioURL == "" || apiKey == "" {
		return nil
	}
	projectID := 0
	if v := os.Getenv("LUXO_PROJECT_ID"); v != "" {
		fmt.Sscanf(v, "%d", &projectID)
	}
	mc := &MetricsCollector{
		studioURL:       studioURL,
		apiKey:          apiKey,
		projectID:       projectID,
		instanceID:      gatewayInstanceID(),
		nodeType:        gatewayNodeType(),
		buckets:         make(map[string]*metricBucket),
		traces:          make([]api.TraceRecord, 0, 128),
		traceSampleRate: traceSampleRateFromEnv(),
		done:            make(chan struct{}),
		client:          &http.Client{Timeout: 10 * time.Second},
	}
	go mc.flushLoop()
	return mc
}

func traceSampleRateFromEnv() float64 {
	value := os.Getenv("LUXO_TRACE_SAMPLE_RATE")
	if value == "" {
		return 0.1
	}
	rate, err := strconv.ParseFloat(value, 64)
	if err != nil || rate < 0 || rate > 1 {
		fmt.Fprintf(os.Stderr, "[traces] invalid LUXO_TRACE_SAMPLE_RATE %q, using 0.1\n", value)
		return 0.1
	}
	return rate
}

// Record adds a request measurement to the current bucket.
func (mc *MetricsCollector) Record(apiName string, duration time.Duration, isError bool) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	// 5-minute bucket
	now := time.Now().Truncate(5 * time.Minute)
	key := fmt.Sprintf("%s:%d", apiName, now.Unix())

	b, ok := mc.buckets[key]
	if !ok {
		b = &metricBucket{apiName: apiName, timestamp: now}
		mc.buckets[key] = b
	}
	ms := float64(duration.Microseconds()) / 1000.0
	b.totalCount++
	b.totalMs += ms
	// Cap latency samples to prevent unbounded memory growth
	if len(b.latencies) < maxLatencySamples {
		b.latencies = append(b.latencies, ms)
	}
	if isError {
		b.errCount++
	}
}

// RecordTrace queues a sampled request trace for the Studio exporter.
// Errors are always retained; successful requests obey LUXO_TRACE_SAMPLE_RATE.
func (mc *MetricsCollector) RecordTrace(record api.TraceRecord) {
	if record.StatusCode < http.StatusBadRequest && !shouldSampleTrace(record.TraceID, mc.traceSampleRate) {
		return
	}
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if len(mc.traces) >= maxPendingTraces {
		return
	}
	mc.traces = append(mc.traces, record)
}

func shouldSampleTrace(traceID string, rate float64) bool {
	if rate <= 0 {
		return false
	}
	if rate >= 1 {
		return true
	}
	var hash uint64 = 14695981039346656037
	for i := 0; i < len(traceID); i++ {
		hash ^= uint64(traceID[i])
		hash *= 1099511628211
	}
	return float64(hash%1000000)/1000000 < rate
}

// Close stops the flush loop and performs a final flush.
func (mc *MetricsCollector) Close() {
	mc.mu.Lock()
	if mc.closed {
		mc.mu.Unlock()
		return
	}
	mc.closed = true
	mc.mu.Unlock()

	close(mc.done)
	mc.flush() // final flush
}

func (mc *MetricsCollector) flushLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			mc.flush()
		case <-mc.done:
			return
		}
	}
}

func (mc *MetricsCollector) flush() {
	mc.flushMetrics()
	mc.flushTraces()
}

func (mc *MetricsCollector) flushMetrics() {
	mc.mu.Lock()
	if len(mc.buckets) == 0 {
		mc.mu.Unlock()
		return
	}
	old := mc.buckets
	mc.buckets = make(map[string]*metricBucket)
	mc.mu.Unlock()

	var bucketList []map[string]any
	for _, b := range old {
		avg := 0.0
		if b.totalCount > 0 {
			avg = b.totalMs / float64(b.totalCount)
		}
		bucketList = append(bucketList, map[string]any{
			"apiName":    b.apiName,
			"timestamp":  b.timestamp.Format(time.RFC3339),
			"totalCount": b.totalCount,
			"errCount":   b.errCount,
			"avgMs":      avg,
			"p50Ms":      percentile(b.latencies, 0.50),
			"p95Ms":      percentile(b.latencies, 0.95),
			"p99Ms":      percentile(b.latencies, 0.99),
		})
	}

	body, _ := json.Marshal(map[string]any{
		"$api":       "svc:ingestMetrics",
		"apiKey":     mc.apiKey,
		"projectId":  mc.projectID,
		"instanceId": mc.instanceID,
		"nodeType":   mc.nodeType,
		"buckets":    bucketList,
	})

	req, err := http.NewRequest("POST", mc.studioURL+"/luvia", bytes.NewReader(body))
	if err != nil {
		mc.reenqueue(old)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mc.apiKey)

	resp, err := mc.client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[metrics] flush failed: %v\n", err)
		mc.reenqueue(old)
		return
	}
	resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		fmt.Fprintf(os.Stderr, "[metrics] flush failed: HTTP %d\n", resp.StatusCode)
		mc.reenqueue(old)
	}
}

func (mc *MetricsCollector) flushTraces() {
	mc.mu.Lock()
	if len(mc.traces) == 0 {
		mc.mu.Unlock()
		return
	}
	old := mc.traces
	mc.traces = make([]api.TraceRecord, 0, 128)
	mc.mu.Unlock()

	traces := make([]map[string]any, 0, len(old))
	for _, record := range old {
		status := "OK"
		if record.StatusCode >= http.StatusBadRequest {
			status = "ERROR"
		}
		trace := map[string]any{
			"traceId":    record.TraceID,
			"apiName":    record.APIName,
			"duration":   float64(record.Duration.Microseconds()) / 1000,
			"status":     status,
			"statusCode": record.StatusCode,
			"timestamp":  record.Timestamp.Format(time.RFC3339Nano),
		}
		if record.ClientName != "" {
			trace["clientName"] = record.ClientName
		}
		if record.ClientVersion != "" {
			trace["clientVersion"] = record.ClientVersion
		}
		traces = append(traces, trace)
	}

	body, _ := json.Marshal(map[string]any{
		"$api":       "svc:ingestTraces",
		"apiKey":     mc.apiKey,
		"projectId":  mc.projectID,
		"instanceId": mc.instanceID,
		"nodeType":   mc.nodeType,
		"traces":     traces,
	})
	req, err := http.NewRequest("POST", mc.studioURL+"/luvia", bytes.NewReader(body))
	if err != nil {
		mc.reenqueueTraces(old)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mc.apiKey)
	resp, err := mc.client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[traces] flush failed: %v\n", err)
		mc.reenqueueTraces(old)
		return
	}
	resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		fmt.Fprintf(os.Stderr, "[traces] flush failed: HTTP %d\n", resp.StatusCode)
		mc.reenqueueTraces(old)
	}
}

func (mc *MetricsCollector) reenqueueTraces(records []api.TraceRecord) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	remaining := maxPendingTraces - len(mc.traces)
	if remaining <= 0 {
		return
	}
	if len(records) > remaining {
		records = records[:remaining]
	}
	mc.traces = append(records, mc.traces...)
}

func (mc *MetricsCollector) reenqueue(buckets map[string]*metricBucket) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	for key, previous := range buckets {
		current, exists := mc.buckets[key]
		if !exists {
			mc.buckets[key] = previous
			continue
		}
		current.totalCount += previous.totalCount
		current.errCount += previous.errCount
		current.totalMs += previous.totalMs
		remaining := maxLatencySamples - len(current.latencies)
		if remaining <= 0 {
			continue
		}
		if remaining > len(previous.latencies) {
			remaining = len(previous.latencies)
		}
		current.latencies = append(current.latencies, previous.latencies[:remaining]...)
	}
}

func percentile(data []float64, p float64) float64 {
	if len(data) == 0 {
		return 0
	}
	// Sort a copy — don't mutate the original
	sorted := make([]float64, len(data))
	copy(sorted, data)
	sort.Float64s(sorted)

	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}
