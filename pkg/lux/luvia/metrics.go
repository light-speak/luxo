package luvia

import (
	"bytes"
	"fmt"
	"math"
	"net/http"
	"os"
	"slices"
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
	metricBucketSecs  = int64((5 * time.Minute) / time.Second)
)

type metricBucketKey struct {
	apiName    string
	bucketUnix int64
}

// MetricsCollector aggregates request metrics in memory and periodically
// flushes them to a Luxo Studio instance via the ingestMetrics API.
type MetricsCollector struct {
	studioURL  string
	apiKey     string
	projectID  string
	instanceID string
	nodeType   string

	mu         sync.Mutex
	buckets    map[metricBucketKey]*metricBucket
	traces     []api.TraceRecord
	traceBytes int
	traceHead  int
	dropped    ObservationDrops
	done       chan struct{}
	closed     bool
	worker     sync.WaitGroup
	closeWait  sync.WaitGroup

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
	studioURL, err := studioURLFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[studio] %v\n", err)
		return nil
	}
	projectID, err := studioProjectIDFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[studio] %v\n", err)
		return nil
	}
	mc := &MetricsCollector{
		studioURL:       studioURL,
		apiKey:          apiKey,
		projectID:       projectID,
		instanceID:      gatewayInstanceID(),
		nodeType:        gatewayNodeType(),
		buckets:         make(map[metricBucketKey]*metricBucket),
		traces:          make([]api.TraceRecord, 0, 128),
		traceSampleRate: traceSampleRateFromEnv(),
		done:            make(chan struct{}),
		client:          newStudioHTTPClient(),
	}
	mc.worker.Go(mc.flushLoop)
	return mc
}

func traceSampleRateFromEnv() float64 {
	value := os.Getenv("LUXO_TRACE_SAMPLE_RATE")
	if value == "" {
		return 0.1
	}
	rate, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(rate) || rate < 0 || rate > 1 {
		fmt.Fprintf(os.Stderr, "[traces] invalid LUXO_TRACE_SAMPLE_RATE %q, using 0.1\n", value)
		return 0.1
	}
	return rate
}

// Record adds a request measurement to the current bucket.
func (mc *MetricsCollector) Record(apiName string, duration time.Duration, isError bool) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if mc.closed {
		return
	}
	if len(apiName) > maxMetricNameBytes {
		mc.dropped.Measurements++
		return
	}

	now := time.Now().Unix()
	key := metricBucketKey{apiName: apiName, bucketUnix: now - now%metricBucketSecs}

	b, ok := mc.buckets[key]
	if !ok {
		b = &metricBucket{apiName: apiName, timestamp: time.Unix(key.bucketUnix, 0).UTC()}
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
	if len(mc.buckets) > maxPendingMetricBuckets {
		mc.trimMetricBuckets()
	}
}

// RecordTrace queues a sampled request trace for the Studio exporter.
// Errors bypass sampling; successful requests obey LUXO_TRACE_SAMPLE_RATE.
// Both remain subject to the bounded pending queue.
func (mc *MetricsCollector) RecordTrace(record api.TraceRecord) {
	if record.StatusCode < http.StatusBadRequest && !record.HeadSampled && !shouldSampleTrace(record.TraceID, mc.traceSampleRate) {
		return
	}
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if mc.closed {
		return
	}
	size := traceTextBytes(record)
	if size > maxTraceTextBytes {
		mc.dropped.Traces++
		return
	}
	if len(mc.traces) == maxPendingTraces || mc.traceBytes+size > maxPendingTraceBytes {
		if mc.replaceOldestTrace(record, size) {
			return
		}
	}
	if mc.traceHead != 0 {
		mc.normalizeTraces()
	}
	mc.traces = append(mc.traces, record)
	mc.traceBytes += size
	if len(mc.traces) > maxPendingTraces || mc.traceBytes > maxPendingTraceBytes {
		mc.trimTraces()
	}
}

// ShouldTrace performs deterministic head sampling before request execution.
func (mc *MetricsCollector) ShouldTrace(traceID string) bool {
	return shouldSampleTrace(traceID, mc.traceSampleRate)
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

// Close stops accepting records, joins the exporter, and attempts a final flush.
// Concurrent callers wait for the same drain. Failed final exports remain pending;
// shutdown does not promise delivery when Studio is unavailable.
func (mc *MetricsCollector) Close() {
	mc.mu.Lock()
	if mc.closed {
		mc.mu.Unlock()
		mc.closeWait.Wait()
		return
	}
	mc.closeWait.Add(1)
	mc.closed = true
	mc.mu.Unlock()
	close(mc.done)
	mc.worker.Wait()
	mc.flush()
	mc.closeWait.Done()
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
	mc.buckets = make(map[metricBucketKey]*metricBucket)
	mc.mu.Unlock()

	body, err := mc.encodePendingMetrics(old)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[metrics] encode failed: %v\n", err)
		return
	}
	if len(old) == 0 {
		return
	}

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
	mc.normalizeTraces()
	old := mc.traces
	mc.traces = make([]api.TraceRecord, 0, 128)
	mc.traceBytes = 0
	mc.mu.Unlock()

	body, valid, err := mc.encodePendingTraces(old)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[traces] encode failed: %v\n", err)
		return
	}
	old = valid
	if len(old) == 0 {
		return
	}

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
	mc.normalizeTraces()
	mc.traces = append(records, mc.traces...)
	mc.traceBytes = 0
	for _, record := range mc.traces {
		mc.traceBytes += traceTextBytes(record)
	}
	mc.trimTraces()
}

func (mc *MetricsCollector) reenqueue(buckets map[metricBucketKey]*metricBucket) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	for key, previous := range buckets {
		current, exists := mc.buckets[key]
		if !exists {
			mc.buckets[key] = previous
			mc.trimMetricBuckets()
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

func latencyPercentiles(data []float64) [3]float64 {
	// Preserve sample order for retries, but copy and sort only once per batch.
	sorted := slices.Clone(data)
	sort.Float64s(sorted)
	return [3]float64{percentile(sorted, 0.50), percentile(sorted, 0.95), percentile(sorted, 0.99)}
}

// percentile reads a nearest-rank value from an already sorted sample.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	// Nearest-rank keeps high percentiles meaningful for small buckets. The
	// previous floor((n-1)*p) formula made p95 ignore the slowest request until
	// a bucket contained at least 20 samples, allowing p95 to fall below avg.
	idx := int(math.Ceil(float64(len(sorted))*p)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
