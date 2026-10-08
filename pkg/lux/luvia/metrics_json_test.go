package luvia

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
)

func TestLatencyPercentilesPreserveInput(t *testing.T) {
	data := []float64{100, 1, 4, 2, 3}
	before := slices.Clone(data)
	if got := latencyPercentiles(data); got != [3]float64{3, 100, 100} {
		t.Fatalf("percentiles = %v", got)
	}
	if !slices.Equal(data, before) {
		t.Fatal("changed retry bucket samples")
	}
	if got := latencyPercentiles(nil); got != [3]float64{} {
		t.Fatal(got)
	}
}

func BenchmarkLatencyPercentiles(b *testing.B) {
	for _, size := range []struct {
		name  string
		count int
	}{{"small", 5}, {"medium", 128}, {"cap", maxLatencySamples}} {
		data := make([]float64, size.count)
		for i := range data {
			data[i] = float64((i * 7919) % (size.count + 1))
		}
		b.Run(size.name+"/previous", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				for _, p := range [...]float64{0.5, 0.95, 0.99} {
					values := slices.Clone(data)
					sort.Float64s(values)
					percentileBenchmarkSink = percentile(values, p)
				}
			}
		})
		b.Run(size.name+"/current", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				percentileBenchmarkSink = latencyPercentiles(data)[2]
			}
		})
	}
}

var percentileBenchmarkSink float64

func TestStaticMetricsJSON(t *testing.T) {
	mc := &MetricsCollector{apiKey: "key\"\\", projectID: "project", instanceID: "node", nodeType: "gateway"}
	bucket := &metricBucket{apiName: "read", timestamp: time.Unix(0, 0).UTC(), totalCount: 2, errCount: 1, totalMs: 6, latencies: []float64{2, 4}}
	body, err := mc.encodeMetrics(map[metricBucketKey]*metricBucket{{apiName: "read"}: bucket})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		API     string `json:"$api"`
		APIKey  string `json:"apiKey"`
		Buckets []struct {
			APIName    string  `json:"apiName"`
			TotalCount int     `json:"totalCount"`
			Avg        float64 `json:"avgMs"`
		} `json:"buckets"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.API != "ingestMetrics" || got.APIKey != mc.apiKey || len(got.Buckets) != 1 || got.Buckets[0].Avg != 3 || got.Buckets[0].TotalCount != 2 {
		t.Fatalf("body=%s", body)
	}
}

func TestStaticTraceJSON(t *testing.T) {
	mc := &MetricsCollector{}
	body, err := mc.encodeTraces([]api.TraceRecord{{APIName: "read", StatusCode: 500, Duration: 1500 * time.Microsecond, Timestamp: time.Unix(0, 123).UTC(), Spans: "quoted\"text", Selection: "id"}})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Traces []map[string]json.RawMessage `json:"traces"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Traces) != 1 || string(got.Traces[0]["status"]) != `"ERROR"` || string(got.Traces[0]["duration"]) != "1.5" {
		t.Fatalf("body=%s", body)
	}
	if _, ok := got.Traces[0]["clientName"]; ok {
		t.Fatal("empty optional field emitted")
	}
}

func TestStaticObservabilityJSONRejectsInvalidText(t *testing.T) {
	mc := &MetricsCollector{apiKey: string([]byte{0xff})}
	if body, err := mc.encodeMetrics(nil); err == nil || body != nil {
		t.Fatal("invalid key accepted")
	}
	mc.apiKey = "valid"
	if body, err := mc.encodeTraces([]api.TraceRecord{{Spans: string([]byte{0xff})}}); err == nil || body != nil {
		t.Fatal("invalid span accepted")
	}
}

func BenchmarkStaticMetricsJSON(b *testing.B) {
	mc := &MetricsCollector{apiKey: "key", projectID: "project", instanceID: "instance", nodeType: "gateway"}
	buckets := map[metricBucketKey]*metricBucket{{apiName: "read"}: {apiName: "read", timestamp: time.Unix(0, 0).UTC(), totalCount: 2, totalMs: 6, latencies: []float64{2, 4}}}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := mc.encodeMetrics(buckets); err != nil {
			b.Fatal(err)
		}
	}
}

// Mirrors HEAD 1e0a1b2's map encoder, with only the control API name aligned.
func previousMetricsJSON(mc *MetricsCollector, buckets map[metricBucketKey]*metricBucket) ([]byte, error) {
	var values []map[string]any
	for _, bucket := range buckets {
		avg := 0.0
		if bucket.totalCount > 0 {
			avg = bucket.totalMs / float64(bucket.totalCount)
		}
		var ranks [3]float64
		for i, p := range [...]float64{0.5, 0.95, 0.99} {
			data := slices.Clone(bucket.latencies)
			sort.Float64s(data)
			ranks[i] = percentile(data, p)
		}
		values = append(values, map[string]any{
			"apiName": bucket.apiName, "timestamp": bucket.timestamp.Format(time.RFC3339),
			"totalCount": bucket.totalCount, "errCount": bucket.errCount, "avgMs": avg,
			"p50Ms": ranks[0], "p95Ms": ranks[1], "p99Ms": ranks[2],
		})
	}
	return json.Marshal(map[string]any{"$api": "ingestMetrics", "apiKey": mc.apiKey,
		"projectId": mc.projectID, "instanceId": mc.instanceID, "nodeType": mc.nodeType, "buckets": values})
}

func metricsEncodingFixture(count int) (*MetricsCollector, map[metricBucketKey]*metricBucket) {
	mc := &MetricsCollector{apiKey: "key", projectID: "project", instanceID: "node", nodeType: "service"}
	buckets := make(map[metricBucketKey]*metricBucket, count)
	for i := range count {
		name := "读取\"" + strconv.Itoa(i)
		buckets[metricBucketKey{apiName: name}] = &metricBucket{apiName: name,
			timestamp: time.Unix(0, 0).UTC(), totalCount: 5, errCount: 1, totalMs: 110,
			latencies: []float64{100, 1, 2, 3, 4}}
	}
	return mc, buckets
}

func TestMetricsEncoderBaselineParity(t *testing.T) {
	mc, buckets := metricsEncodingFixture(1)
	previous, err := previousMetricsJSON(mc, buckets)
	if err != nil {
		t.Fatal(err)
	}
	current, err := mc.encodeMetrics(buckets)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	if err := json.Unmarshal(previous, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(current, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("previous=%s current=%s", previous, current)
	}
}

func BenchmarkMetricsEncodingComparison(b *testing.B) {
	for _, count := range []int{1, 32} {
		mc, buckets := metricsEncodingFixture(count)
		b.Run(strconv.Itoa(count)+"/previous", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := previousMetricsJSON(mc, buckets); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(strconv.Itoa(count)+"/current", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := mc.encodeMetrics(buckets); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Mirrors HEAD 1e0a1b2's trace map encoder with the current control API name.
func previousTracesJSON(mc *MetricsCollector, records []api.TraceRecord) ([]byte, error) {
	traces := make([]map[string]any, 0, len(records))
	for _, record := range records {
		status := "OK"
		if record.StatusCode >= 400 {
			status = "ERROR"
		}
		trace := map[string]any{"traceId": record.TraceID, "apiName": record.APIName,
			"duration": float64(record.Duration.Microseconds()) / 1000, "status": status,
			"statusCode": record.StatusCode, "timestamp": record.Timestamp.Format(time.RFC3339Nano)}
		if record.ClientName != "" {
			trace["clientName"] = record.ClientName
		}
		if record.ClientVersion != "" {
			trace["clientVersion"] = record.ClientVersion
		}
		if record.Spans != "" {
			trace["spans"] = record.Spans
		}
		if record.Selection != "" {
			trace["selection"] = record.Selection
		}
		traces = append(traces, trace)
	}
	return json.Marshal(map[string]any{"$api": "ingestTraces", "apiKey": mc.apiKey,
		"projectId": mc.projectID, "instanceId": mc.instanceID, "nodeType": mc.nodeType, "traces": traces})
}

func traceEncodingFixture(large bool) []api.TraceRecord {
	records := []api.TraceRecord{{TraceID: "first", APIName: "read", StatusCode: 200,
		Duration: time.Millisecond, Timestamp: time.Unix(0, 123).UTC()}}
	if large {
		records = append(records, api.TraceRecord{TraceID: "second", APIName: "读取", StatusCode: 500,
			Duration: 1500 * time.Microsecond, Timestamp: time.Unix(0, 456).UTC(),
			ClientName: "sdk", ClientVersion: "beta.1", Selection: "id,name",
			Spans: strings.Repeat("中文\"\\\n", 1024)})
	}
	return records
}

func TestTraceEncoderBaselineParity(t *testing.T) {
	for _, records := range [][]api.TraceRecord{nil, traceEncodingFixture(false), traceEncodingFixture(true)} {
		mc, _ := metricsEncodingFixture(0)
		previous, err := previousTracesJSON(mc, records)
		if err != nil {
			t.Fatal(err)
		}
		current, err := mc.encodeTraces(records)
		if err != nil {
			t.Fatal(err)
		}
		var before, after map[string]any
		if err := json.Unmarshal(previous, &before); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(current, &after); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatal("trace output changed")
		}
	}
}

const maxTraceJSONReserve = 1 << 20

// Reserve only for large span text. This is a bounded size hint, not validation;
// jsontext still owns escaping and strict UTF-8 checks during the single write.
func traceJSONCapacity(records []api.TraceRecord) int {
	if len(records) > (maxTraceJSONReserve-512)/256 {
		return 0
	}
	size := 512 + len(records)*256
	large := false
	for i := range records {
		text := records[i].Spans
		if len(text) < 1024 {
			continue
		}
		large = true
		if len(text) > maxTraceJSONReserve-size {
			return 0
		}
		size += len(text)
		for j := range len(text) {
			switch text[j] {
			case '"', '\\', '\b', '\f', '\n', '\r', '\t':
				size++
			default:
				if text[j] < 0x20 {
					size += 5
				}
			}
			if size > maxTraceJSONReserve {
				return 0
			}
		}
	}
	if !large {
		return 0
	}
	return size
}

func TestTraceJSONCapacity(t *testing.T) {
	if got := traceJSONCapacity(traceEncodingFixture(false)); got != 0 {
		t.Fatal(got)
	}
	for _, text := range []string{strings.Repeat("中", 1024), strings.Repeat("\x00\n\"\\", 1024)} {
		records := []api.TraceRecord{{Spans: text}}
		capacity := traceJSONCapacity(records)
		mc := &MetricsCollector{}
		body, err := mc.encodeTraces(records)
		if err != nil {
			t.Fatal(err)
		}
		if capacity < len(body) || capacity > maxTraceJSONReserve {
			t.Fatalf("capacity=%d length=%d", capacity, len(body))
		}
	}
	for _, text := range []string{strings.Repeat("x", maxTraceJSONReserve), strings.Repeat("\x00", maxTraceJSONReserve/2)} {
		if got := traceJSONCapacity([]api.TraceRecord{{Spans: text}}); got != 0 {
			t.Fatal(got)
		}
	}
}

func BenchmarkTraceEncodingComparison(b *testing.B) {
	for _, large := range []bool{false, true} {
		mc, _ := metricsEncodingFixture(0)
		records := traceEncodingFixture(large)
		b.Run(strconv.FormatBool(large)+"/previous", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := previousTracesJSON(mc, records); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(strconv.FormatBool(large)+"/current", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := mc.encodeTraces(records); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(strconv.FormatBool(large)+"/reservation-candidate", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var w observationJSON
				if capacity := traceJSONCapacity(records); capacity > 0 {
					w.buf.B = make([]byte, 0, capacity)
				}
				mc.observationHeader(&w, "ingestTraces")
				w.buf.AppendString(`,"traces":[`)
				for i := range records {
					if i > 0 {
						w.buf.AppendString(",")
					}
					appendTraceRecord(&w, &records[i])
				}
				if _, err := w.finish("]}"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
