package luvia

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

// MetricsCollector aggregates request metrics in memory and periodically
// flushes them to a Luxo Studio instance via the ingestMetrics API.
type MetricsCollector struct {
	studioURL  string
	apiKey     string
	projectID  int
	instanceID string

	mu      sync.Mutex
	buckets map[string]*metricBucket // key: "apiName:minute"
	done    chan struct{}
}

type metricBucket struct {
	apiName    string
	timestamp  time.Time
	totalCount int64
	errCount   int64
	totalMs    float64
	latencies  []float64 // for percentile calculation
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
	instanceID, _ := os.Hostname()

	mc := &MetricsCollector{
		studioURL:  studioURL,
		apiKey:     apiKey,
		projectID:  projectID,
		instanceID: instanceID,
		buckets:    make(map[string]*metricBucket),
		done:       make(chan struct{}),
	}
	go mc.flushLoop()
	return mc
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
	b.latencies = append(b.latencies, ms)
	if isError {
		b.errCount++
	}
}

// Close stops the flush loop.
func (mc *MetricsCollector) Close() {
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
		"$api":      "ingestMetrics",
		"projectId": mc.projectID,
		"buckets":   bucketList,
	})

	req, err := http.NewRequest("POST", mc.studioURL+"/luvia", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[metrics] flush failed: %v\n", err)
		return
	}
	resp.Body.Close()
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	// Simple percentile — not sorted, use selection
	n := len(sorted)
	idx := int(float64(n) * p)
	if idx >= n {
		idx = n - 1
	}
	// Partial sort: find k-th smallest
	return quickSelect(sorted, idx)
}

func quickSelect(arr []float64, k int) float64 {
	if len(arr) <= 1 {
		return arr[0]
	}
	pivot := arr[len(arr)/2]
	var lo, hi, eq []float64
	for _, v := range arr {
		switch {
		case v < pivot:
			lo = append(lo, v)
		case v > pivot:
			hi = append(hi, v)
		default:
			eq = append(eq, v)
		}
	}
	if k < len(lo) {
		return quickSelect(lo, k)
	}
	if k < len(lo)+len(eq) {
		return pivot
	}
	return quickSelect(hi, k-len(lo)-len(eq))
}
