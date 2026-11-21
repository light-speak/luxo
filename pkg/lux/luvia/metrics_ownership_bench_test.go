package luvia

import (
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
)

func BenchmarkMetricTextOwnership(b *testing.B) {
	for _, fresh := range []bool{false, true} {
		name := "existing-bucket"
		if fresh {
			name = "new-bucket"
		}
		b.Run(name, func(b *testing.B) {
			mc := &MetricsCollector{buckets: make(map[metricBucketKey]*metricBucket)}
			for range maxLatencySamples {
				mc.Record("read", time.Millisecond, false)
			}
			b.ReportAllocs()
			for b.Loop() {
				mc.Record("read", time.Millisecond, false)
				if fresh {
					clear(mc.buckets)
				}
			}
		})
	}
}

func BenchmarkTraceTextOwnership(b *testing.B) {
	for _, sample := range []struct {
		name string
		size int
	}{{"empty", 0}, {"short", 32}, {"9KiB", 9 << 10}, {"near-limit", maxTraceTextBytes - 128}} {
		b.Run(sample.name, func(b *testing.B) {
			record := api.TraceRecord{HeadSampled: true}
			if sample.size != 0 {
				record.Timestamp = time.Now()
				record.TraceID, record.APIName = "trace-0123456789", "getUser"
				record.ClientName, record.ClientVersion = "typescript", "1.0.0-beta.1"
				record.Selection, record.Spans = "id,name", strings.Repeat("x", sample.size)
			}
			mc := &MetricsCollector{traces: make([]api.TraceRecord, 0, 1)}
			b.ReportAllocs()
			for b.Loop() {
				mc.RecordTrace(record)
				mc.traces[0] = api.TraceRecord{}
				mc.traces = mc.traces[:0]
				mc.traceBytes = 0
			}
		})
	}
}
