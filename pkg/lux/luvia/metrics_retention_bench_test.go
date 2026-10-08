package luvia

import (
	"net/http"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
)

func (mc *MetricsCollector) baselineEncodeMetrics(buckets map[metricBucketKey]*metricBucket) ([]byte, error) {
	var w observationJSON
	mc.observationHeader(&w, "ingestMetrics")
	w.buf.AppendString(`,"buckets":[`)
	first := true
	for _, bucket := range buckets {
		if !first {
			w.buf.AppendString(",")
		}
		first = false
		appendMetricBucket(&w, bucket)
	}
	return w.finish("]}")
}

func (mc *MetricsCollector) baselineEncodeTraces(records []api.TraceRecord) ([]byte, error) {
	var w observationJSON
	mc.observationHeader(&w, "ingestTraces")
	w.buf.AppendString(`,"traces":[`)
	for i := range records {
		if i > 0 {
			w.buf.AppendString(",")
		}
		appendTraceRecord(&w, &records[i])
	}
	return w.finish("]}")
}

func BenchmarkObservationBatchRetention(b *testing.B) {
	for _, previous := range []bool{true, false} {
		name := "current"
		if previous {
			name = "previous"
		}
		b.Run(name, func(b *testing.B) {
			mc := &MetricsCollector{}
			b.Run("metric", func(b *testing.B) {
				buckets := map[metricBucketKey]*metricBucket{{apiName: "read"}: {apiName: "read", totalCount: 128, totalMs: 128, latencies: make([]float64, 128)}}
				encode := (*MetricsCollector).encodePendingMetrics
				if previous {
					encode = (*MetricsCollector).baselineEncodeMetrics
				}
				b.ReportAllocs()
				for b.Loop() {
					if _, err := encode(mc, buckets); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("trace", func(b *testing.B) {
				records := []api.TraceRecord{{TraceID: "trace", APIName: "read", Spans: "[]", Timestamp: time.Unix(100, 0)}}
				b.ReportAllocs()
				for b.Loop() {
					if previous {
						if _, err := mc.baselineEncodeTraces(records); err != nil {
							b.Fatal(err)
						}
					} else {
						if _, _, err := mc.encodePendingTraces(records); err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		})
	}
}

// These snapshots preserve the pre-retention implementation as a local control.
func (mc *MetricsCollector) retentionBaselineRecord(apiName string, duration time.Duration, isError bool) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if mc.closed {
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
}

// RecordTrace queues a sampled request trace for the Studio exporter.
// Errors bypass sampling; successful requests obey LUXO_TRACE_SAMPLE_RATE.
// Both remain subject to the bounded pending queue.
func (mc *MetricsCollector) retentionBaselineTrace(record api.TraceRecord) {
	if record.StatusCode < http.StatusBadRequest && !record.HeadSampled && !shouldSampleTrace(record.TraceID, mc.traceSampleRate) {
		return
	}
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if mc.closed || len(mc.traces) >= maxPendingTraces {
		return
	}
	mc.traces = append(mc.traces, record)
}

func BenchmarkObservationRetention(b *testing.B) {
	for _, previous := range []bool{true, false} {
		name := "current"
		if previous {
			name = "previous"
		}
		b.Run(name, func(b *testing.B) {
			b.Run("record", func(b *testing.B) {
				mc := &MetricsCollector{buckets: make(map[metricBucketKey]*metricBucket)}
				for range maxLatencySamples {
					mc.Record("read", time.Millisecond, false)
				}
				record := (*MetricsCollector).Record
				if previous {
					record = (*MetricsCollector).retentionBaselineRecord
				}
				b.ReportAllocs()
				for b.Loop() {
					record(mc, "read", time.Millisecond, false)
				}
			})
			b.Run("trace", func(b *testing.B) {
				mc := &MetricsCollector{traceSampleRate: 1, traces: make([]api.TraceRecord, 0, 1)}
				record := (*MetricsCollector).RecordTrace
				if previous {
					record = (*MetricsCollector).retentionBaselineTrace
				}
				value := api.TraceRecord{TraceID: "trace", APIName: "read", Spans: "[]", StatusCode: http.StatusOK}
				b.ReportAllocs()
				for b.Loop() {
					record(mc, value)
					mc.traces = mc.traces[:0]
					mc.traceBytes = 0
				}
			})
		})
	}
}
