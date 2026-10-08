package luvia

import (
	"net/http"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/light-speak/luxo/pkg/lux/api"
)

// Preserve the exact collector layout before retention accounting was added.
type retentionBaselineCollector struct {
	studioURL, apiKey, projectID, instanceID, nodeType string
	mu                                                 sync.Mutex
	buckets                                            map[metricBucketKey]*metricBucket
	traces                                             []api.TraceRecord
	done                                               chan struct{}
	closed                                             bool
	worker, closeWait                                  sync.WaitGroup
	traceSampleRate                                    float64
	client                                             *http.Client
}

var retentionOldSink *retentionBaselineCollector
var retentionNewSink *MetricsCollector

// Allocation only: no environment lookup, worker startup or HTTP client creation.
func BenchmarkObservationCollectorAllocation(b *testing.B) {
	b.Run("previous", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			retentionOldSink = &retentionBaselineCollector{}
		}
		b.ReportMetric(float64(unsafe.Sizeof(retentionBaselineCollector{})), "layout-B")
	})
	b.Run("current", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			retentionNewSink = &MetricsCollector{}
		}
		b.ReportMetric(float64(unsafe.Sizeof(MetricsCollector{})), "layout-B")
	})
}

func BenchmarkObservationParallelRecord(b *testing.B) {
	for _, previous := range []bool{true, false} {
		name := "current"
		if previous {
			name = "previous"
		}
		b.Run(name, func(b *testing.B) {
			mc := &MetricsCollector{buckets: make(map[metricBucketKey]*metricBucket)}
			for range maxLatencySamples {
				mc.Record("read", time.Millisecond, false)
			}
			record := (*MetricsCollector).Record
			if previous {
				record = (*MetricsCollector).retentionBaselineRecord
			}
			b.SetParallelism(8)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					record(mc, "read", time.Millisecond, false)
				}
			})
		})
	}
}

// The previous implementation drops new entries; current keeps the newest.
// These are deliberately non-equivalent overflow semantics.
func BenchmarkObservationTraceOverflow(b *testing.B) {
	for _, previous := range []bool{true, false} {
		name := "current"
		if previous {
			name = "previous"
		}
		b.Run(name, func(b *testing.B) {
			mc := &MetricsCollector{traceSampleRate: 1, traces: make([]api.TraceRecord, maxPendingTraces)}
			record := (*MetricsCollector).RecordTrace
			if previous {
				record = (*MetricsCollector).retentionBaselineTrace
			}
			b.ReportAllocs()
			for b.Loop() {
				record(mc, api.TraceRecord{TraceID: "new", StatusCode: 200})
			}
			if len(mc.traces) != maxPendingTraces {
				b.Fatal("unbounded queue")
			}
		})
	}
}

func BenchmarkObservationMetricOverflow(b *testing.B) {
	mc := &MetricsCollector{buckets: make(map[metricBucketKey]*metricBucket, maxPendingMetricBuckets+1)}
	for i := range maxPendingMetricBuckets {
		mc.buckets[metricBucketKey{bucketUnix: int64(i + 1)}] = &metricBucket{totalCount: 1}
	}
	old := &metricBucket{totalCount: 1}
	key := metricBucketKey{bucketUnix: 0}
	b.ReportAllocs()
	for b.Loop() {
		mc.buckets[key] = old
		mc.trimMetricBuckets()
	}
	if len(mc.buckets) != maxPendingMetricBuckets || mc.dropped.MetricBuckets != uint64(b.N) {
		b.Fatal("overflow accounting changed")
	}
}

func BenchmarkObservationPoisonIsolation(b *testing.B) {
	mc := &MetricsCollector{}
	good := api.TraceRecord{TraceID: "good", Spans: "[]"}
	bad := api.TraceRecord{TraceID: "bad", Spans: "\xff"}
	var records [2]api.TraceRecord
	b.ReportAllocs()
	for b.Loop() {
		records[0], records[1] = bad, good
		_, valid, err := mc.encodePendingTraces(records[:])
		if err != nil || len(valid) != 1 || valid[0].TraceID != "good" {
			b.Fatal("isolation failed")
		}
	}
	if mc.DroppedObservations().InvalidTraces != uint64(b.N) {
		b.Fatal("loss accounting changed")
	}
}
