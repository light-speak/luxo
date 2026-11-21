package luvia

import (
	"errors"
	"strings"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/datetime"
)

var errInvalidObservation = errors.New("studio: invalid observation fields")

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
	if value < 0 {
		w.err = errInvalidObservation
		return
	}
	w.buf.AppendString(prefix)
	w.err = w.buf.AppendFloat(value)
}

func (w *observationJSON) timestamp(prefix string, value time.Time) {
	if w.err != nil {
		return
	}
	if value.IsZero() || !datetime.ValidSeconds(value.Unix()) {
		w.err = errInvalidObservation
		return
	}
	w.buf.AppendString(prefix)
	w.buf.AppendString(`"`)
	w.buf.B = value.UTC().AppendFormat(w.buf.B, time.RFC3339Nano)
	w.buf.AppendString(`"`)
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

func (mc *MetricsCollector) discardMetricBuckets(buckets map[metricBucketKey]*metricBucket) {
	var measurements uint64
	for _, bucket := range buckets {
		measurements += uint64(max(0, bucket.totalCount))
	}
	mc.addInvalidMetrics(uint64(len(buckets)), measurements)
	clear(buckets)
}

func appendMetricBucket(w *observationJSON, b *metricBucket) {
	if w.err != nil {
		return
	}
	if strings.TrimSpace(b.apiName) == "" || b.totalCount < 0 || b.errCount < 0 || b.errCount > b.totalCount {
		w.err = errInvalidObservation
		return
	}
	avg := 0.0
	if b.totalCount > 0 {
		avg = b.totalMs / float64(b.totalCount)
	}
	w.text(`{"apiName":`, b.apiName)
	w.timestamp(`,"timestamp":`, b.timestamp)
	w.integer(`,"totalCount":`, b.totalCount)
	w.integer(`,"errCount":`, b.errCount)
	w.number(`,"avgMs":`, avg)
	percentiles := latencyPercentiles(b.latencies)
	w.number(`,"p50Ms":`, percentiles[0])
	w.number(`,"p95Ms":`, percentiles[1])
	w.number(`,"p99Ms":`, percentiles[2])
	w.buf.AppendString("}")
}

func appendTraceRecord(w *observationJSON, r *api.TraceRecord) {
	if w.err != nil {
		return
	}
	if strings.TrimSpace(r.TraceID) == "" || strings.TrimSpace(r.APIName) == "" || r.Duration < 0 {
		w.err = errInvalidObservation
		return
	}
	status := "OK"
	if r.StatusCode >= 400 {
		status = "ERROR"
	}
	w.text(`{"traceId":`, r.TraceID)
	w.text(`,"apiName":`, r.APIName)
	w.number(`,"duration":`, float64(r.Duration.Microseconds())/1000)
	w.text(`,"status":`, status)
	w.integer(`,"statusCode":`, int64(r.StatusCode))
	w.timestamp(`,"timestamp":`, r.Timestamp)
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
