package luvia

import (
	"github.com/light-speak/luxo/pkg/lux/api"
	"slices"
)

const (
	maxPendingMetricBuckets = 256
	maxMetricNameBytes      = 4096
	maxPendingTraceBytes    = 16 << 20
	maxTraceTextBytes       = 256 << 10
)

// ObservationDrops counts exporter losses, not sampling exclusions or business events.
// Counters are cumulative for this collector's lifetime, including final flush losses.
type ObservationDrops struct {
	MetricBuckets        uint64
	Measurements         uint64
	Traces               uint64
	InvalidMetricBuckets uint64
	InvalidTraces        uint64
}

// DroppedObservations returns a synchronized snapshot without resetting counters.
func (mc *MetricsCollector) DroppedObservations() ObservationDrops {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	return mc.dropped
}

// trimMetricBuckets runs only on overflow, never for ordinary existing-bucket updates.
func (mc *MetricsCollector) trimMetricBuckets() {
	for len(mc.buckets) > maxPendingMetricBuckets {
		var oldest metricBucketKey
		first := true
		for key := range mc.buckets {
			if first || key.bucketUnix < oldest.bucketUnix || (key.bucketUnix == oldest.bucketUnix && key.apiName < oldest.apiName) {
				oldest, first = key, false
			}
		}
		mc.dropped.MetricBuckets++
		mc.dropped.Measurements += uint64(max(0, mc.buckets[oldest].totalCount))
		delete(mc.buckets, oldest)
	}
}

func traceTextBytes(r api.TraceRecord) int {
	// Sum before narrowing to int so repeated large strings cannot wrap on 32-bit hosts.
	size := uint64(len(r.TraceID)) + uint64(len(r.APIName)) + uint64(len(r.ClientName)) +
		uint64(len(r.ClientVersion)) + uint64(len(r.Spans)) + uint64(len(r.Selection))
	return int(min(size, maxTraceTextBytes+1))
}

// Stable overflow replaces a ring slot instead of repeatedly growing a sliced queue.
// Variable-size records requiring several evictions use the bounded fallback.
func (mc *MetricsCollector) replaceOldestTrace(record api.TraceRecord, size int) bool {
	remaining := mc.traceBytes - traceTextBytes(mc.traces[mc.traceHead]) + size
	if remaining > maxPendingTraceBytes {
		return false
	}
	mc.traces[mc.traceHead] = record
	mc.traceHead++
	if mc.traceHead == len(mc.traces) {
		mc.traceHead = 0
	}
	mc.traceBytes = remaining
	mc.dropped.Traces++
	return true
}

// Export/retry sees chronological order. Rotation reuses storage and never encodes twice.
func (mc *MetricsCollector) normalizeTraces() {
	if mc.traceHead == 0 {
		return
	}
	slices.Reverse(mc.traces[:mc.traceHead])
	slices.Reverse(mc.traces[mc.traceHead:])
	slices.Reverse(mc.traces)
	mc.traceHead = 0
}

// Dropping the prefix clears references; advancing the slice amortizes queue eviction.
func (mc *MetricsCollector) trimTraces() {
	for len(mc.traces) > maxPendingTraces || mc.traceBytes > maxPendingTraceBytes {
		mc.traceBytes -= traceTextBytes(mc.traces[0])
		mc.traces[0] = api.TraceRecord{}
		mc.traces = mc.traces[1:]
		mc.dropped.Traces++
	}
}

func (mc *MetricsCollector) addInvalidMetrics(buckets, measurements uint64) {
	mc.mu.Lock()
	mc.dropped.MetricBuckets += buckets
	mc.dropped.InvalidMetricBuckets += buckets
	mc.dropped.Measurements += measurements
	mc.mu.Unlock()
}

func (mc *MetricsCollector) addInvalidTraces(count uint64) {
	mc.mu.Lock()
	mc.dropped.Traces += count
	mc.dropped.InvalidTraces += count
	mc.mu.Unlock()
}
