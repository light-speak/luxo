package luvia

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/light-speak/luxo/pkg/lux/api"
)

func TestMetricBucketOwnsRetainedName(t *testing.T) {
	backing := strings.Repeat("api", 1<<18)
	name := backing[3:6]
	mc := &MetricsCollector{buckets: make(map[metricBucketKey]*metricBucket)}
	mc.Record(name, time.Millisecond, false)
	for key, bucket := range mc.buckets {
		if key.apiName != name || bucket.apiName != name {
			t.Fatal("retained name changed")
		}
		// Compare storage only in this test: a short substring must not pin its parent.
		if unsafe.StringData(key.apiName) == unsafe.StringData(name) {
			t.Fatal("bucket key retained the large source string")
		}
		if unsafe.StringData(bucket.apiName) != unsafe.StringData(key.apiName) {
			t.Fatal("bucket and key duplicated the same owned name")
		}
	}
	mc.Record(name, time.Millisecond, true)
	var total, failures int64
	for _, bucket := range mc.buckets {
		total += bucket.totalCount
		failures += bucket.errCount
	}
	if total != 2 || failures != 1 {
		t.Fatal("bucket accounting changed")
	}
}

func TestRetainedTraceOwnsText(t *testing.T) {
	fields := []struct {
		name string
		set  func(*api.TraceRecord, string)
		get  func(api.TraceRecord) string
	}{
		{"traceId", func(r *api.TraceRecord, s string) { r.TraceID = s }, func(r api.TraceRecord) string { return r.TraceID }},
		{"apiName", func(r *api.TraceRecord, s string) { r.APIName = s }, func(r api.TraceRecord) string { return r.APIName }},
		{"clientName", func(r *api.TraceRecord, s string) { r.ClientName = s }, func(r api.TraceRecord) string { return r.ClientName }},
		{"clientVersion", func(r *api.TraceRecord, s string) { r.ClientVersion = s }, func(r api.TraceRecord) string { return r.ClientVersion }},
		{"spans", func(r *api.TraceRecord, s string) { r.Spans = s }, func(r api.TraceRecord) string { return r.Spans }},
		{"selection", func(r *api.TraceRecord, s string) { r.Selection = s }, func(r api.TraceRecord) string { return r.Selection }},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			backing := strings.Repeat("text", 1<<18)
			text := backing[4:8]
			record := api.TraceRecord{HeadSampled: true}
			field.set(&record, text)
			mc := &MetricsCollector{}
			mc.RecordTrace(record)
			if len(mc.traces) != 1 || mc.traceBytes != len(text) {
				t.Fatal("retention or byte accounting changed")
			}
			stored := field.get(mc.traces[0])
			if stored != text {
				t.Fatal("trace text changed")
			}
			if unsafe.StringData(stored) == unsafe.StringData(text) {
				t.Fatal("trace retained the large source string")
			}
		})
	}
}

func TestRejectedTraceDoesNotCopyText(t *testing.T) {
	for _, name := range []string{"sampled-out", "closed", "oversized"} {
		t.Run(name, func(t *testing.T) {
			mc := &MetricsCollector{}
			record := api.TraceRecord{TraceID: "excluded", APIName: "read"}
			if name != "sampled-out" {
				record.HeadSampled = true
			}
			mc.closed = name == "closed"
			if name == "oversized" {
				record.Spans = strings.Repeat("x", maxTraceTextBytes+1)
			}
			if allocations := testing.AllocsPerRun(100, func() { mc.RecordTrace(record) }); allocations != 0 {
				t.Fatal("rejected trace copied text", allocations)
			}
			if len(mc.traces) != 0 {
				t.Fatal("rejected trace was retained")
			}
		})
	}
}

func TestTraceOwnershipSurvivesOverflowAndRetry(t *testing.T) {
	mc := &MetricsCollector{traces: make([]api.TraceRecord, maxPendingTraces)}
	backing := strings.Repeat("select", 1<<17)
	text := backing[:6]
	mc.RecordTrace(api.TraceRecord{HeadSampled: true, Selection: text})
	mc.normalizeTraces()
	owned := mc.traces[len(mc.traces)-1]
	if owned.Selection != text || unsafe.StringData(owned.Selection) == unsafe.StringData(text) {
		t.Fatal("ring replacement kept unowned text")
	}
	mc.traces, mc.traceBytes = nil, 0
	mc.reenqueueTraces([]api.TraceRecord{owned})
	if len(mc.traces) != 1 || mc.traceBytes != len(text) ||
		unsafe.StringData(mc.traces[0].Selection) != unsafe.StringData(owned.Selection) {
		t.Fatal("retry copied or lost already owned text")
	}
}

func TestEmptyTraceTextDoesNotRetainParent(t *testing.T) {
	backing := strings.Repeat("empty", 1<<18)
	empty := backing[:0]
	if unsafe.StringData(empty) != unsafe.StringData(backing) {
		t.Fatal("fixture must retain its source allocation")
	}
	mc := &MetricsCollector{}
	mc.RecordTrace(api.TraceRecord{HeadSampled: true, TraceID: empty, APIName: empty,
		ClientName: empty, ClientVersion: empty, Spans: empty, Selection: empty})
	for _, text := range []string{mc.traces[0].TraceID, mc.traces[0].APIName, mc.traces[0].ClientName,
		mc.traces[0].ClientVersion, mc.traces[0].Spans, mc.traces[0].Selection} {
		if unsafe.StringData(text) == unsafe.StringData(backing) {
			t.Fatal("zero-byte trace pinned its source allocation")
		}
	}
}

func TestTraceTimestampDoesNotRetainLocation(t *testing.T) {
	backing := strings.Repeat("zone", 1<<18)
	zone := time.FixedZone(backing[:4], -5*60*60)
	timestamp := time.Date(2026, time.November, 6, 12, 34, 56, 123456789, zone)
	mc := &MetricsCollector{}
	mc.RecordTrace(api.TraceRecord{HeadSampled: true, Timestamp: timestamp})
	stored := mc.traces[0].Timestamp
	if !stored.Equal(timestamp) || stored.Nanosecond() != timestamp.Nanosecond() {
		t.Fatal("timestamp instant or precision changed")
	}
	if stored.Location() != time.UTC {
		t.Fatal("trace retained the caller's location and its unbounded name")
	}
}

func TestOwnedTracePreservesExportedValues(t *testing.T) {
	type exported struct {
		TraceID, APIName, ClientName, ClientVersion, Spans, Selection string
		Timestamp                                                     time.Time
		Duration                                                      float64
		StatusCode                                                    int
	}
	received := make(chan exported, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Traces []exported }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Traces) != 1 {
			t.Error("invalid trace export", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- body.Traces[0]
	}))
	defer server.Close()
	const values = "追踪|read|SDK|beta|[{}]|id,name"
	backing := strings.Repeat("parent", 1<<17) + values
	parts := strings.Split(backing[len(backing)-len(values):], "|")
	timestamp := time.Date(2026, time.November, 6, 12, 34, 56, 123456789, time.FixedZone(parts[0], 3600))
	mc := newTraceCollector(server.URL, server.Client())
	mc.RecordTrace(api.TraceRecord{HeadSampled: true, TraceID: parts[0], APIName: parts[1],
		ClientName: parts[2], ClientVersion: parts[3], Spans: parts[4], Selection: parts[5],
		Timestamp: timestamp, Duration: 3 * time.Millisecond, StatusCode: http.StatusOK})
	mc.flushTraces()
	select {
	case got := <-received:
		if got.TraceID != parts[0] || got.APIName != parts[1] || got.ClientName != parts[2] ||
			got.ClientVersion != parts[3] || got.Spans != parts[4] || got.Selection != parts[5] ||
			!got.Timestamp.Equal(timestamp) || got.Timestamp.Nanosecond() != 123456789 ||
			got.Duration != 3 || got.StatusCode != http.StatusOK {
			t.Fatal("owned snapshot changed exported values", got)
		}
	default:
		t.Fatal("trace was not exported")
	}
}
