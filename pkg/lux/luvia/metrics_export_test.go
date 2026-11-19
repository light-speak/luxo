package luvia

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
)

func TestTraceExportPartialFailureKeepsOnlyUnacknowledged(t *testing.T) {
	var requests atomic.Int32
	received := make(chan []string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids := exportTraceIDs(t, r.Body)
		received <- ids
		if requests.Add(1) == 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	mc := newTraceCollector(server.URL, server.Client())
	for i := range 2501 {
		mc.RecordTrace(withHeadSample(observationTestTrace(strconv.Itoa(i))))
		if i == 1500 {
			mc.RecordTrace(withHeadSample(observationTestTrace("bad\xff")))
		}
	}
	mc.flushTraces()
	if len(mc.traces) != 1501 || mc.DroppedObservations().InvalidTraces != 1 {
		t.Fatal("partial failure requeued acknowledged or invalid records", len(mc.traces), mc.DroppedObservations())
	}
	for i, record := range mc.traces {
		if record.TraceID != strconv.Itoa(i+1000) {
			t.Fatal("retry order changed", i, record.TraceID)
		}
	}
	mc.flushTraces()
	close(received)
	wantStarts := []string{"0", "1000", "1000", "2000"}
	i := 0
	for ids := range received {
		if i >= len(wantStarts) || len(ids) == 0 || len(ids) > maxExportTraceRecords || ids[0] != wantStarts[i] {
			t.Fatal("wrong batch sequence", i, ids)
		}
		i++
	}
	if i != 4 || len(mc.traces) != 0 || mc.DroppedObservations().InvalidTraces != 1 {
		t.Fatal("retry lost records or recounted failures", i, mc.DroppedObservations())
	}
}

func withHeadSample(record api.TraceRecord) api.TraceRecord {
	record.HeadSampled = true
	return record
}

func exportTraceIDs(t *testing.T, body io.Reader) []string {
	t.Helper()
	var payload struct{ Traces []struct{ TraceID string } }
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		t.Error(err)
		return nil
	}
	ids := make([]string, len(payload.Traces))
	for i, record := range payload.Traces {
		ids[i] = record.TraceID
	}
	return ids
}

type retainedObservationTransport struct{ bodies []io.ReadCloser }

func (r *retainedObservationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	r.bodies = append(r.bodies, request.Body)
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{"data":1}`)), Request: request}, nil
}

func TestObservationExportDoesNotReuseSubmittedBody(t *testing.T) {
	transport := &retainedObservationTransport{}
	mc := newTraceCollector("http://studio.invalid", &http.Client{Transport: transport})
	for i := range 1001 {
		mc.RecordTrace(withHeadSample(observationTestTrace(strconv.Itoa(i))))
	}
	mc.flushTraces()
	if len(transport.bodies) != 2 {
		t.Fatal("wrong body count", len(transport.bodies))
	}
	for i, body := range transport.bodies {
		ids := exportTraceIDs(t, body)
		_ = body.Close()
		want := "0"
		if i == 1 {
			want = "1000"
		}
		if len(ids) == 0 || ids[0] != want {
			t.Fatal("body was mutated after its response", i, ids)
		}
	}
}

func TestPendingTraceFieldsAreIsolated(t *testing.T) {
	for _, mutate := range []func(*api.TraceRecord){
		func(r *api.TraceRecord) { r.TraceID = " " },
		func(r *api.TraceRecord) { r.APIName = "\t" },
		func(r *api.TraceRecord) { r.Duration = -time.Nanosecond },
		func(r *api.TraceRecord) { r.Timestamp = time.Time{} },
		func(r *api.TraceRecord) { r.Timestamp = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) },
	} {
		transport := &retainedObservationTransport{}
		mc := newTraceCollector("http://studio.invalid", &http.Client{Transport: transport})
		bad := observationTestTrace("bad")
		mutate(&bad)
		mc.traces = []api.TraceRecord{observationTestTrace("first"), bad, observationTestTrace("last")}
		mc.flushTraces()
		if len(transport.bodies) != 1 || len(mc.traces) != 0 {
			t.Fatal("valid traces were not exported", len(transport.bodies))
		}
		body := transport.bodies[0]
		ids := exportTraceIDs(t, body)
		_ = body.Close()
		if len(ids) != 2 || ids[0] != "first" || ids[1] != "last" || mc.DroppedObservations().InvalidTraces != 1 {
			t.Fatal("field error poisoned the batch", ids, mc.DroppedObservations())
		}
	}
}

func TestMetricExportPartialFailureKeepsUnacknowledged(t *testing.T) {
	var requests atomic.Int32
	received := make(chan []string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct{ Buckets []struct{ APIName string } }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MaxRequestBodySize)).Decode(&payload); err != nil {
			t.Error(err)
		}
		names := make([]string, len(payload.Buckets))
		for i, bucket := range payload.Buckets {
			names[i] = bucket.APIName
		}
		received <- names
		if requests.Add(1) == 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	mc := newTraceCollector(server.URL, server.Client())
	mc.buckets = make(map[metricBucketKey]*metricBucket)
	for i := range 100 {
		mc.Record(strings.Repeat("\x01", 4092)+strconv.Itoa(i), time.Millisecond, false)
	}
	mc.Record("\xff", time.Millisecond, false)
	mc.flushMetrics()
	first, failed := <-received, <-received
	if len(first)+len(failed) != 100 || len(mc.buckets) != len(failed) || mc.DroppedObservations().InvalidMetricBuckets != 1 {
		t.Fatal("metric acknowledgement boundary changed", len(first), len(failed), len(mc.buckets))
	}
	want := make(map[string]bool, len(failed))
	for _, name := range failed {
		want[name] = true
	}
	for key := range mc.buckets {
		if !want[key.apiName] {
			t.Fatal("acknowledged metric requeued")
		}
	}
	mc.flushMetrics()
	if got := <-received; len(got) != len(failed) || len(mc.buckets) != 0 || mc.DroppedObservations().InvalidMetricBuckets != 1 {
		t.Fatal("metric retry lost data or recounted invalid records")
	}
}

func TestOversizedObservationHeaderIsNotRetried(t *testing.T) {
	mc := newTraceCollector("http://unused.invalid", http.DefaultClient)
	mc.apiKey = strings.Repeat("x", api.MaxRequestBodySize)
	mc.RecordTrace(withHeadSample(observationTestTrace("trace")))
	mc.buckets = make(map[metricBucketKey]*metricBucket)
	mc.Record("read", time.Millisecond, false)
	mc.flush()
	mc.flush()
	drops := mc.DroppedObservations()
	if len(mc.traces) != 0 || len(mc.buckets) != 0 || drops.InvalidTraces != 1 || drops.InvalidMetricBuckets != 1 {
		t.Fatal("oversized metadata retried or changed accounting", drops)
	}
}

func TestObservationUnfittableRecordDoesNotPoisonOtherChunks(t *testing.T) {
	transport := &retainedObservationTransport{}
	mc := newTraceCollector("http://studio.invalid", &http.Client{Transport: transport})
	mc.apiKey = strings.Repeat("x", 1<<20)
	mc.RecordTrace(withHeadSample(observationTestTrace("before")))
	large := withHeadSample(observationTestTrace("large"))
	large.Spans = strings.Repeat("\x01", 250<<10)
	mc.RecordTrace(large)
	mc.RecordTrace(withHeadSample(observationTestTrace("after")))
	mc.flushTraces()
	var ids []string
	for _, body := range transport.bodies {
		ids = append(ids, exportTraceIDs(t, body)...)
		_ = body.Close()
	}
	if len(ids) != 2 || ids[0] != "before" || ids[1] != "after" || mc.DroppedObservations().InvalidTraces != 1 {
		t.Fatal("unfittable record poisoned the stream", ids, mc.DroppedObservations())
	}
}

func TestMetricExportFirstChunkFailureKeepsRemaining(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	mc := newTraceCollector(server.URL, server.Client())
	mc.buckets = make(map[metricBucketKey]*metricBucket)
	for i := range 100 {
		mc.Record(strings.Repeat("\x01", 4092)+strconv.Itoa(i), time.Millisecond, false)
	}
	mc.flushMetrics()
	if len(mc.buckets) != 100 || mc.DroppedObservations() != (ObservationDrops{}) {
		t.Fatal("mid-iteration failure discarded unprocessed metrics", len(mc.buckets), mc.DroppedObservations())
	}
}

type delayedObservationTransport struct{}

func (*delayedObservationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	_, _ = io.Copy(io.Discard, request.Body)
	_ = request.Body.Close()
	select {
	case <-time.After(4 * time.Second):
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header), Request: request}, nil
	case <-request.Context().Done():
		return nil, request.Context().Err()
	}
}

func TestObservationChunksShareExportDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mc := newTraceCollector("http://studio.invalid", &http.Client{Transport: &delayedObservationTransport{}, Timeout: 5 * time.Second})
		for i := range 2501 {
			mc.RecordTrace(withHeadSample(observationTestTrace(strconv.Itoa(i))))
		}
		start := time.Now()
		mc.flushTraces()
		if elapsed := time.Since(start); elapsed != 5*time.Second {
			t.Fatal("one export renewed its deadline for each chunk", elapsed)
		}
		if len(mc.traces) != 1501 {
			t.Fatal("deadline lost unacknowledged records", len(mc.traces))
		}
	})
}

func TestMetricObservationChunksShareExportDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mc := newTraceCollector("http://studio.invalid", &http.Client{Transport: &delayedObservationTransport{}, Timeout: 5 * time.Second})
		mc.buckets = make(map[metricBucketKey]*metricBucket)
		for i := range 100 {
			mc.Record(strings.Repeat("\x01", 4092)+strconv.Itoa(i), time.Millisecond, false)
		}
		start := time.Now()
		mc.flushMetrics()
		if elapsed := time.Since(start); elapsed != 5*time.Second {
			t.Fatal("metric chunks renewed the export deadline", elapsed)
		}
		if len(mc.buckets) == 0 || len(mc.buckets) == 100 {
			t.Fatal("metric deadline lost acknowledgement boundary", len(mc.buckets))
		}
	})
}
