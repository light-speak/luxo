package luvia

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
)

func TestMetricRetentionKeepsNewestBuckets(t *testing.T) {
	mc := &MetricsCollector{buckets: make(map[metricBucketKey]*metricBucket)}
	for i := range maxPendingMetricBuckets + 10 {
		key := metricBucketKey{apiName: "read", bucketUnix: int64(i)}
		mc.reenqueue(map[metricBucketKey]*metricBucket{key: {totalCount: 2}})
	}
	if len(mc.buckets) != maxPendingMetricBuckets {
		t.Fatal(len(mc.buckets))
	}
	for i := range 10 {
		if _, ok := mc.buckets[metricBucketKey{apiName: "read", bucketUnix: int64(i)}]; ok {
			t.Fatal("old bucket retained")
		}
	}
	if got := mc.DroppedObservations(); got.MetricBuckets != 10 || got.Measurements != 20 {
		t.Fatal(got)
	}
	for i := range maxPendingMetricBuckets + 10 {
		mc.Record(strconv.Itoa(i), time.Millisecond, false)
	}
	if len(mc.buckets) != maxPendingMetricBuckets {
		t.Fatal("record bypassed capacity")
	}
}

func TestTraceRetentionKeepsNewestAndBoundsText(t *testing.T) {
	mc := &MetricsCollector{traceSampleRate: 1}
	for i := range maxPendingTraces + 2 {
		mc.RecordTrace(api.TraceRecord{TraceID: strconv.Itoa(i)})
	}
	mc.normalizeTraces()
	if len(mc.traces) != maxPendingTraces || mc.traces[0].TraceID != "2" {
		t.Fatal("oldest trace not evicted")
	}
	if mc.DroppedObservations().Traces != 2 {
		t.Fatal(mc.DroppedObservations())
	}
	mc.RecordTrace(api.TraceRecord{Spans: strings.Repeat("x", maxTraceTextBytes+1)})
	if mc.DroppedObservations().Traces != 3 {
		t.Fatal("oversized trace not counted")
	}
	payload := strings.Repeat("x", maxTraceTextBytes)
	for range maxPendingTraceBytes/maxTraceTextBytes + 2 {
		mc.RecordTrace(api.TraceRecord{Spans: payload})
	}
	if mc.traceBytes > maxPendingTraceBytes {
		t.Fatal(mc.traceBytes)
	}
}

func TestInvalidObservationDoesNotPoisonBatch(t *testing.T) {
	received := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			API     string            `json:"$api"`
			Buckets []json.RawMessage `json:"buckets"`
			Traces  []json.RawMessage `json:"traces"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		received[body.API] += len(body.Buckets) + len(body.Traces)
	}))
	defer server.Close()
	mc := newTraceCollector(server.URL, server.Client())
	mc.buckets = make(map[metricBucketKey]*metricBucket)
	mc.Record("good", time.Millisecond, false)
	mc.Record("bad\xff", time.Millisecond, false)
	mc.traceSampleRate = 1
	mc.RecordTrace(api.TraceRecord{TraceID: "good"})
	mc.RecordTrace(api.TraceRecord{TraceID: "bad", Spans: "\xff"})
	mc.flush()
	if received["ingestMetrics"] != 1 || received["ingestTraces"] != 1 {
		t.Fatal(received)
	}
	if len(mc.buckets) != 0 || len(mc.traces) != 0 {
		t.Fatal("poison record requeued")
	}
	if got := mc.DroppedObservations(); got.InvalidMetricBuckets != 1 || got.InvalidTraces != 1 {
		t.Fatal(got)
	}
}

func TestInvalidObservationsAreNotRetried(t *testing.T) {
	for _, header := range []bool{false, true} {
		t.Run(strconv.FormatBool(header), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()
			mc := newTraceCollector(server.URL, server.Client())
			mc.buckets = make(map[metricBucketKey]*metricBucket)
			mc.traceSampleRate = 1
			if header {
				mc.instanceID = "\xff"
			}
			mc.Record("bad\xff", time.Millisecond, false)
			mc.RecordTrace(api.TraceRecord{TraceID: "\xff"})
			mc.flush()
			mc.flush()
			if requests != 0 || len(mc.buckets) != 0 || len(mc.traces) != 0 {
				t.Fatal("invalid batch was retried")
			}
			if got := mc.DroppedObservations(); got.InvalidTraces != 1 || got.InvalidMetricBuckets != 1 || got.Measurements != 1 {
				t.Fatal(got)
			}
		})
	}
}

func TestObservationIsolationKeepsValidRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	mc := newTraceCollector(server.URL, server.Client())
	mc.buckets = make(map[metricBucketKey]*metricBucket)
	mc.traceSampleRate = 1
	mc.Record("good", time.Millisecond, false)
	mc.Record("bad\xff", time.Millisecond, false)
	for _, id := range []string{"\xff", "first", "\xff", "last", "\xff"} {
		mc.RecordTrace(api.TraceRecord{TraceID: id})
	}
	mc.flush()
	if len(mc.buckets) != 1 || len(mc.traces) != 2 || mc.traces[0].TraceID != "first" || mc.traces[1].TraceID != "last" {
		t.Fatal("valid retry changed")
	}
	if mc.traceBytes != 9 {
		t.Fatal(mc.traceBytes)
	}
	before := mc.DroppedObservations()
	mc.flush()
	if before != mc.DroppedObservations() {
		t.Fatal("double-counted bad records")
	}
}

func TestObservationRetentionWithInflightFailure(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(503)
	}))
	defer server.Close()
	mc := newTraceCollector(server.URL, server.Client())
	mc.traceSampleRate = 1
	mc.RecordTrace(api.TraceRecord{TraceID: "old"})
	var worker sync.WaitGroup
	worker.Go(mc.flushTraces)
	<-entered
	for range maxPendingTraces {
		mc.RecordTrace(api.TraceRecord{TraceID: "new"})
		_ = mc.DroppedObservations()
	}
	close(release)
	worker.Wait()
	if len(mc.traces) != maxPendingTraces || mc.traces[0].TraceID != "new" || mc.DroppedObservations().Traces != 1 {
		t.Fatal("retry displaced recent observations")
	}
}

func TestOversizedMetricNameDoesNotCreateBucket(t *testing.T) {
	mc := &MetricsCollector{buckets: make(map[metricBucketKey]*metricBucket)}
	mc.Record(strings.Repeat("x", maxMetricNameBytes+1), time.Second, true)
	if len(mc.buckets) != 0 || mc.DroppedObservations().Measurements != 1 {
		t.Fatal("unbounded API name retained")
	}
}

func TestTraceSizeSaturatesBeforeIntegerConversion(t *testing.T) {
	text := strings.Repeat("x", maxTraceTextBytes)
	if got := traceTextBytes(api.TraceRecord{TraceID: text, APIName: text, Spans: text}); got != maxTraceTextBytes+1 {
		t.Fatal(got)
	}
}

func TestConcurrentObservationRetentionAccounting(t *testing.T) {
	mc := &MetricsCollector{traceSampleRate: 1, buckets: make(map[metricBucketKey]*metricBucket)}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 2000 {
				mc.Record("read", time.Millisecond, false)
				mc.RecordTrace(api.TraceRecord{TraceID: "trace"})
				_ = mc.DroppedObservations()
			}
		})
	}
	workers.Wait()
	var total int64
	for _, bucket := range mc.buckets {
		total += bucket.totalCount
	}
	if total != 16000 || len(mc.traces) != maxPendingTraces || mc.DroppedObservations().Traces != 6000 {
		t.Fatal("concurrent records lost or miscounted")
	}
}

func TestSteadyTraceOverflowDoesNotAllocate(t *testing.T) {
	for _, text := range []string{"id", strings.Repeat("x", maxTraceTextBytes)} {
		mc := &MetricsCollector{traceSampleRate: 1}
		record := api.TraceRecord{TraceID: text}
		for range maxPendingTraces {
			mc.RecordTrace(record)
		}
		if got := testing.AllocsPerRun(1, func() {
			for range 20000 {
				mc.RecordTrace(record)
			}
		}); got != 0 {
			t.Fatalf("steady overflow allocated %v times per batch", got)
		}
	}
}

func TestOverflowAllocationProbeDetectsSliceGrowth(t *testing.T) {
	queue := make([]api.TraceRecord, maxPendingTraces)
	allocations := testing.AllocsPerRun(1, func() {
		for range 20000 {
			queue = append(queue, api.TraceRecord{})
			queue[0] = api.TraceRecord{}
			queue = queue[1:]
		}
	})
	if allocations == 0 {
		t.Fatal("allocation probe missed amortized slice growth")
	}
}

func TestFlushRotatedTraceQueuePreservesOrder(t *testing.T) {
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Traces []struct {
				ID string `json:"traceId"`
			} `json:"traces"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		for _, record := range body.Traces {
			received = append(received, record.ID)
		}
	}))
	defer server.Close()
	mc := newTraceCollector(server.URL, server.Client())
	mc.traces = []api.TraceRecord{{TraceID: "a"}, {TraceID: "b"}, {TraceID: "c"}}
	mc.traceBytes = 3
	mc.replaceOldestTrace(api.TraceRecord{TraceID: "d"}, 1)
	mc.flushTraces()
	if !slices.Equal(received, []string{"b", "c", "d"}) || mc.traceHead != 0 || mc.traceBytes != 0 {
		t.Fatal(received)
	}
}

func TestTraceRingVariableSizeFallback(t *testing.T) {
	mc := &MetricsCollector{traceSampleRate: 1}
	large := api.TraceRecord{Spans: strings.Repeat("x", maxTraceTextBytes)}
	mc.RecordTrace(large)
	mc.RecordTrace(api.TraceRecord{TraceID: "a"})
	for range maxPendingTraceBytes/maxTraceTextBytes - 2 {
		mc.RecordTrace(large)
	}
	mc.RecordTrace(api.TraceRecord{Spans: large.Spans[:len(large.Spans)-1]})
	if mc.traceBytes != maxPendingTraceBytes {
		t.Fatal(mc.traceBytes)
	}
	mc.RecordTrace(large)
	if mc.traceHead != 1 {
		t.Fatal("ring did not advance")
	}
	mc.RecordTrace(large)
	if mc.traceHead != 0 || mc.DroppedObservations().Traces != 3 {
		t.Fatal("multi-record fallback changed ordering")
	}
	mc.RecordTrace(api.TraceRecord{TraceID: "z"})
	var total int
	for _, r := range mc.traces {
		total += traceTextBytes(r)
	}
	if total != mc.traceBytes || total > maxPendingTraceBytes {
		t.Fatal("byte accounting changed")
	}
}
