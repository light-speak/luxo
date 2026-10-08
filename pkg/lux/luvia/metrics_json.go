package luvia

import (
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
)

// observationJSON writes only the fixed control-plane schema. Text and numbers
// reuse the runtime's strict scalar encoders, never reflection or round trips.
type observationJSON struct {
	buf api.ResponseBuf
	err error
}

func (w *observationJSON) text(prefix, value string) {
	if w.err != nil {
		return
	}
	w.buf.AppendString(prefix)
	w.err = w.buf.AppendJSONString(value)
}

func (w *observationJSON) number(prefix string, value float64) {
	if w.err != nil {
		return
	}
	w.buf.AppendString(prefix)
	w.err = w.buf.AppendFloat(value)
}

func (w *observationJSON) integer(prefix string, value int64) {
	if w.err != nil {
		return
	}
	w.buf.AppendString(prefix)
	w.buf.AppendInt(value)
}

func (w *observationJSON) finish(suffix string) ([]byte, error) {
	if w.err != nil {
		return nil, w.err
	}
	w.buf.AppendString(suffix)
	return w.buf.B, nil
}

func (mc *MetricsCollector) observationHeader(w *observationJSON, operation string) {
	w.text(`{"$api":`, operation)
	w.text(`,"apiKey":`, mc.apiKey)
	w.text(`,"projectId":`, mc.projectID)
	w.text(`,"instanceId":`, mc.instanceID)
	w.text(`,"nodeType":`, mc.nodeType)
}

func (mc *MetricsCollector) encodeMetrics(buckets map[metricBucketKey]*metricBucket) ([]byte, error) {
	return mc.encodeMetricBatch(buckets, false)
}

func (mc *MetricsCollector) encodePendingMetrics(buckets map[metricBucketKey]*metricBucket) ([]byte, error) {
	body, err := mc.encodeMetricBatch(buckets, true)
	if err != nil {
		var measurements uint64
		for _, b := range buckets {
			measurements += uint64(max(0, b.totalCount))
		}
		mc.addInvalidMetrics(uint64(len(buckets)), measurements)
		clear(buckets)
	}
	return body, err
}

func (mc *MetricsCollector) encodeMetricBatch(buckets map[metricBucketKey]*metricBucket, isolate bool) ([]byte, error) {
	var w observationJSON
	mc.observationHeader(&w, "ingestMetrics")
	if w.err != nil {
		return nil, w.err
	}
	w.buf.AppendString(`,"buckets":[`)
	first := true
	var dropped, measurements uint64
	for key, bucket := range buckets {
		start := len(w.buf.B)
		if !first {
			w.buf.AppendString(",")
		}
		appendMetricBucket(&w, bucket)
		if w.err != nil && isolate {
			w.buf.B, w.err = w.buf.B[:start], nil
			delete(buckets, key)
			dropped++
			measurements += uint64(max(0, bucket.totalCount))
			continue
		}
		first = false
	}
	if dropped != 0 {
		mc.addInvalidMetrics(dropped, measurements)
	}
	return w.finish("]}")
}

func appendMetricBucket(w *observationJSON, b *metricBucket) {
	avg := 0.0
	if b.totalCount > 0 {
		avg = b.totalMs / float64(b.totalCount)
	}
	w.text(`{"apiName":`, b.apiName)
	w.text(`,"timestamp":`, b.timestamp.Format(time.RFC3339))
	w.integer(`,"totalCount":`, b.totalCount)
	w.integer(`,"errCount":`, b.errCount)
	w.number(`,"avgMs":`, avg)
	percentiles := latencyPercentiles(b.latencies)
	w.number(`,"p50Ms":`, percentiles[0])
	w.number(`,"p95Ms":`, percentiles[1])
	w.number(`,"p99Ms":`, percentiles[2])
	w.buf.AppendString("}")
}

func (mc *MetricsCollector) encodeTraces(records []api.TraceRecord) ([]byte, error) {
	body, _, err := mc.encodeTraceBatch(records, false)
	return body, err
}

func (mc *MetricsCollector) encodePendingTraces(records []api.TraceRecord) ([]byte, []api.TraceRecord, error) {
	body, valid, err := mc.encodeTraceBatch(records, true)
	if err != nil {
		mc.addInvalidTraces(uint64(len(records)))
		clear(records)
		return nil, nil, err
	}
	return body, valid, nil
}

func (mc *MetricsCollector) encodeTraceBatch(records []api.TraceRecord, isolate bool) ([]byte, []api.TraceRecord, error) {
	var w observationJSON
	mc.observationHeader(&w, "ingestTraces")
	if w.err != nil {
		return nil, nil, w.err
	}
	w.buf.AppendString(`,"traces":[`)
	n := 0
	for i := range records {
		start := len(w.buf.B)
		if n > 0 {
			w.buf.AppendString(",")
		}
		appendTraceRecord(&w, &records[i])
		if w.err != nil && isolate {
			w.buf.B, w.err = w.buf.B[:start], nil
			continue
		}
		if isolate && n != i {
			records[n] = records[i]
		}
		n++
	}
	if isolate && n < len(records) {
		mc.addInvalidTraces(uint64(len(records) - n))
		clear(records[n:])
	}
	body, err := w.finish("]}")
	return body, records[:n], err
}

func appendTraceRecord(w *observationJSON, r *api.TraceRecord) {
	status := "OK"
	if r.StatusCode >= 400 {
		status = "ERROR"
	}
	w.text(`{"traceId":`, r.TraceID)
	w.text(`,"apiName":`, r.APIName)
	w.number(`,"duration":`, float64(r.Duration.Microseconds())/1000)
	w.text(`,"status":`, status)
	w.integer(`,"statusCode":`, int64(r.StatusCode))
	w.text(`,"timestamp":`, r.Timestamp.Format(time.RFC3339Nano))
	if r.ClientName != "" {
		w.text(`,"clientName":`, r.ClientName)
	}
	if r.ClientVersion != "" {
		w.text(`,"clientVersion":`, r.ClientVersion)
	}
	if r.Spans != "" {
		w.text(`,"spans":`, r.Spans)
	}
	if r.Selection != "" {
		w.text(`,"selection":`, r.Selection)
	}
	w.buf.AppendString("}")
}
