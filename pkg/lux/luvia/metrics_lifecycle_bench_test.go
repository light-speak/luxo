package luvia

import (
	"net/http"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
)

// previousRecord retains the pre-shutdown-fix implementation for paired controls.
func (mc *MetricsCollector) previousRecord(name string, duration time.Duration, isError bool) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	now := time.Now().Unix()
	key := metricBucketKey{apiName: name, bucketUnix: now - now%metricBucketSecs}
	b, ok := mc.buckets[key]
	if !ok {
		b = &metricBucket{apiName: name, timestamp: time.Unix(key.bucketUnix, 0).UTC()}
		mc.buckets[key] = b
	}
	ms := float64(duration.Microseconds()) / 1000.0
	b.totalCount++
	b.totalMs += ms
	if len(b.latencies) < maxLatencySamples {
		b.latencies = append(b.latencies, ms)
	}
	if isError {
		b.errCount++
	}
}

func (mc *MetricsCollector) previousRecordTrace(record api.TraceRecord) {
	if record.StatusCode < http.StatusBadRequest && !record.HeadSampled && !shouldSampleTrace(record.TraceID, mc.traceSampleRate) {
		return
	}
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if len(mc.traces) >= maxPendingTraces {
		return
	}
	mc.traces = append(mc.traces, record)
}

func (mc *MetricsCollector) previousClose() {
	mc.mu.Lock()
	if mc.closed {
		mc.mu.Unlock()
		return
	}
	mc.closed = true
	mc.mu.Unlock()
	close(mc.done)
	mc.flush()
}

func BenchmarkMetricsLifecycleComparison(b *testing.B) {
	for _, previous := range []bool{true, false} {
		name := "current"
		if previous {
			name = "previous"
		}
		b.Run(name, func(b *testing.B) {
			benchmarkMetricsLifecycle(b, previous)
		})
	}
}

func benchmarkMetricsLifecycle(b *testing.B, previous bool) {
	b.Run("record-full-sample-bucket", func(b *testing.B) {
		mc := &MetricsCollector{buckets: make(map[metricBucketKey]*metricBucket)}
		for range maxLatencySamples {
			mc.Record("read", time.Millisecond, false)
		}
		record := (*MetricsCollector).Record
		if previous {
			record = (*MetricsCollector).previousRecord
		}
		b.ReportAllocs()
		for b.Loop() {
			record(mc, "read", time.Millisecond, false)
		}
	})
	b.Run("trace-reused-slot", func(b *testing.B) {
		mc := &MetricsCollector{traceSampleRate: 1, traces: make([]api.TraceRecord, 0, 1)}
		record := (*MetricsCollector).RecordTrace
		if previous {
			record = (*MetricsCollector).previousRecordTrace
		}
		b.ReportAllocs()
		for b.Loop() {
			record(mc, api.TraceRecord{StatusCode: http.StatusOK})
			mc.traces = mc.traces[:0]
		}
	})
	b.Run("empty-create-close", func(b *testing.B) {
		closeCollector := (*MetricsCollector).Close
		if previous {
			closeCollector = (*MetricsCollector).previousClose
		}
		b.ReportAllocs()
		for b.Loop() {
			mc := &MetricsCollector{done: make(chan struct{})}
			closeCollector(mc)
		}
	})
}
