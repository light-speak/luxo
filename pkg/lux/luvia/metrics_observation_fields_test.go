package luvia

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/datetime"
)

func observationTestBucket(name string) *metricBucket {
	return &metricBucket{apiName: name, timestamp: time.Unix(100, 0).UTC(), totalCount: 2,
		errCount: 1, totalMs: 2, latencies: []float64{1}}
}

func TestPendingMetricFieldsAreIsolated(t *testing.T) {
	for _, mutate := range []func(*metricBucket){
		func(b *metricBucket) { b.apiName = " " },
		func(b *metricBucket) { b.timestamp = time.Time{} },
		func(b *metricBucket) { b.timestamp = time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC) },
		func(b *metricBucket) { b.totalCount = -1 },
		func(b *metricBucket) { b.errCount = -1 },
		func(b *metricBucket) { b.errCount = 3 },
		func(b *metricBucket) { b.totalMs = -1 },
		func(b *metricBucket) { b.latencies = []float64{-1} },
		func(b *metricBucket) { b.totalMs = math.NaN() },
	} {
		transport := &retainedObservationTransport{}
		mc := newTraceCollector("http://studio.invalid", &http.Client{Transport: transport})
		bad := observationTestBucket("bad")
		mutate(bad)
		buckets := map[metricBucketKey]*metricBucket{{apiName: "first"}: observationTestBucket("first"),
			{apiName: "bad"}: bad, {apiName: "last"}: observationTestBucket("last")}
		mc.exportMetricBuckets(buckets)
		var payload struct{ Buckets []struct{ APIName string } }
		if len(transport.bodies) != 1 {
			t.Fatal("valid metrics were not exported", len(transport.bodies))
		}
		body := transport.bodies[0]
		defer body.Close()
		if err := json.NewDecoder(body).Decode(&payload); err != nil || len(payload.Buckets) != 2 ||
			len(buckets) != 0 || mc.DroppedObservations().InvalidMetricBuckets != 1 {
			t.Fatal("field error poisoned metrics", err, payload, mc.DroppedObservations())
		}
		seen := make(map[string]bool, 2)
		for _, bucket := range payload.Buckets {
			if (bucket.APIName != "first" && bucket.APIName != "last") || seen[bucket.APIName] {
				t.Fatal("invalid metric was exported", bucket.APIName)
			}
			seen[bucket.APIName] = true
		}
	}
}

func TestObservationTimestampBoundaries(t *testing.T) {
	for _, value := range []time.Time{time.Date(0, 1, 1, 0, 0, 0, 1, time.UTC),
		time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC),
		time.Date(2026, 11, 6, 12, 0, 0, 123456789, time.FixedZone("offset", 3600))} {
		var writer observationJSON
		writer.timestamp("", value)
		var text string
		if writer.err != nil || json.Unmarshal(writer.buf.B, &text) != nil {
			t.Fatal("valid date rejected", writer.err)
		}
		parsed, err := datetime.ParseRFC3339(text)
		if err != nil || !parsed.Equal(value) || !strings.HasSuffix(text, "Z") {
			t.Fatal("timestamp changed", text, err)
		}
	}
	for _, value := range []time.Time{{}, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(0, 1, 1, 0, 0, 0, 0, time.FixedZone("outside", 3600))} {
		var writer observationJSON
		writer.timestamp("prefix", value)
		if writer.err == nil || len(writer.buf.B) != 0 {
			t.Fatal("invalid date appended partial output", value)
		}
	}
}

func TestObservationValidationPreservesPriorError(t *testing.T) {
	prior := errors.New("prior")
	writer := observationJSON{err: prior}
	writer.timestamp("prefix", time.Now())
	writer.number("prefix", -1)
	appendTraceRecord(&writer, nil)
	appendMetricBucket(&writer, nil)
	if writer.err != prior || len(writer.buf.B) != 0 {
		t.Fatal("first error was overwritten")
	}
}

func TestMetricUnfittableRecordIsIsolated(t *testing.T) {
	transport := &retainedObservationTransport{}
	mc := newTraceCollector("http://studio.invalid", &http.Client{Transport: transport})
	mc.apiKey = strings.Repeat("x", api.MaxRequestBodySize-1024)
	mc.buckets = make(map[metricBucketKey]*metricBucket)
	mc.Record("read", time.Millisecond, false)
	mc.Record(strings.Repeat("\x01", 4096), time.Millisecond, false)
	mc.flushMetrics()
	if len(transport.bodies) != 1 || len(mc.buckets) != 0 || mc.DroppedObservations().InvalidMetricBuckets != 1 {
		t.Fatal("unfittable metric poisoned the batch", mc.DroppedObservations())
	}
	_ = transport.bodies[0].Close()
}

func TestPendingObservationHeaderFailureCountsLossWithoutSending(t *testing.T) {
	transport := &retainedObservationTransport{}
	mc := newTraceCollector("http://studio.invalid", &http.Client{Transport: transport})
	mc.apiKey = "\xff"
	buckets := map[metricBucketKey]*metricBucket{{apiName: "read"}: observationTestBucket("read")}
	mc.exportMetricBuckets(buckets)
	mc.traces = []api.TraceRecord{observationTestTrace("trace")}
	mc.flushTraces()
	if len(transport.bodies) != 0 || len(buckets) != 0 || len(mc.traces) != 0 {
		t.Fatal("invalid metadata was sent or retained")
	}
	if drops := mc.DroppedObservations(); drops.InvalidTraces != 1 || drops.InvalidMetricBuckets != 1 || drops.Measurements != 2 {
		t.Fatal("header losses changed", drops)
	}
}
