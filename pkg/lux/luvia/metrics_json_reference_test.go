package luvia

import "github.com/light-speak/luxo/pkg/lux/api"

// Whole-batch reference encoders are test-only. Production exporters batch by
// encoded bytes and isolate records; benchmarks of these helpers do not measure
// that lifecycle. Keep historical JSON/scalar comparisons separate from export.
func (mc *MetricsCollector) encodeMetrics(buckets map[metricBucketKey]*metricBucket) ([]byte, error) {
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

func (mc *MetricsCollector) encodeTraces(records []api.TraceRecord) ([]byte, error) {
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
