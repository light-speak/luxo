package luvia

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/light-speak/luxo/pkg/lux/api"
)

// Match Studio's ingest contract, independently of pending retention capacity.
const maxExportTraceRecords = 1000

type observationBatch struct {
	header, body []byte
	count        int
}

func (mc *MetricsCollector) newObservationBatch(operation, field string) (observationBatch, error) {
	var header observationJSON
	mc.observationHeader(&header, operation)
	header.buf.AppendString(field)
	if header.err != nil {
		return observationBatch{}, header.err
	}
	if len(header.buf.B)+2 > api.MaxRequestBodySize {
		return observationBatch{}, errInvalidObservation
	}
	return observationBatch{header: header.buf.B}, nil
}

func (b *observationBatch) fits(size int) bool {
	length := len(b.body)
	if b.body == nil {
		length = len(b.header)
	}
	if b.count != 0 {
		length++
	}
	return b.count < maxExportTraceRecords && length+size+2 <= api.MaxRequestBodySize
}

func (b *observationBatch) append(record []byte, remaining int) {
	if b.body == nil {
		// Reserve from the first encoded size without scanning or encoding twice.
		// A small per-record margin covers normal identifier/number length changes.
		capacity := min(api.MaxRequestBodySize, len(b.header)+2+min(remaining, maxExportTraceRecords)*(len(record)+33))
		b.body = append(make([]byte, 0, capacity), b.header...)
	}
	if b.count != 0 {
		b.body = append(b.body, ',')
	}
	b.body = append(b.body, record...)
	b.count++
}

func (b *observationBatch) reset() {
	// The HTTP transport may still hold the previous immutable request body.
	b.body, b.count = nil, 0
}

func (mc *MetricsCollector) observationContext() (context.Context, context.CancelFunc) {
	if mc.client.Timeout > 0 {
		return context.WithTimeout(context.Background(), mc.client.Timeout)
	}
	return context.Background(), func() {}
}

func (mc *MetricsCollector) sendObservationBatch(ctx context.Context, body []byte, kind string) bool {
	req, err := http.NewRequestWithContext(ctx, "POST", mc.studioURL+"/luvia", bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mc.apiKey)
	resp, err := mc.client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] flush failed: %v\n", kind, err)
		return false
	}
	// Bound acknowledgement draining while keeping normal connections reusable.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		fmt.Fprintf(os.Stderr, "[%s] flush failed: HTTP %d\n", kind, resp.StatusCode)
		return false
	}
	return true
}
