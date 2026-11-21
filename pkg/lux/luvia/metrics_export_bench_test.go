package luvia

import (
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
)

type observationBenchmarkTransport struct{ bytes, requests int64 }

func (r *observationBenchmarkTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	n, err := io.Copy(io.Discard, request.Body)
	_ = request.Body.Close()
	if err != nil {
		return nil, err
	}
	r.bytes += n
	r.requests++
	return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Header: make(http.Header), Request: request}, nil
}

func BenchmarkObservationExport(b *testing.B) {
	for _, sample := range []struct {
		name  string
		count int
		large bool
	}{{"trace1", 1, false}, {"trace128", 128, false}, {"trace1001", 1001, false}, {"trace128-large", 128, true}} {
		b.Run(sample.name, func(b *testing.B) {
			transport := &observationBenchmarkTransport{}
			mc := newTraceCollector("http://studio.invalid", &http.Client{Transport: transport, Timeout: 10 * time.Second})
			records := make([]api.TraceRecord, sample.count)
			for i := range records {
				records[i] = observationTestTrace(strconv.Itoa(i))
				if sample.large {
					records[i].Spans = strings.Repeat("中文\"\\\n", 1024)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				mc.traces = append(mc.traces[:0], records...)
				mc.flushTraces()
				if len(mc.traces) != 0 || mc.DroppedObservations() != (ObservationDrops{}) {
					b.Fatal("valid trace export failed")
				}
			}
		})
	}
}

func BenchmarkMetricObservationExport(b *testing.B) {
	for _, count := range []int{1, 128} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			transport := &observationBenchmarkTransport{}
			mc := newTraceCollector("http://studio.invalid", &http.Client{Transport: transport, Timeout: 10 * time.Second})
			buckets := make(map[metricBucketKey]*metricBucket, count)
			for i := range count {
				name := "read" + strconv.Itoa(i)
				buckets[metricBucketKey{apiName: name}] = &metricBucket{apiName: name,
					timestamp: time.Unix(100, 0).UTC(), totalCount: 128, totalMs: 128, latencies: []float64{1}}
			}
			b.ReportAllocs()
			for b.Loop() {
				mc.buckets = maps.Clone(buckets)
				mc.flushMetrics()
				if len(mc.buckets) != 0 || mc.DroppedObservations() != (ObservationDrops{}) {
					b.Fatal("valid metrics export failed")
				}
			}
		})
	}
}
