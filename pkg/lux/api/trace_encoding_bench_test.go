package api

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func BenchmarkDebugTraceTextEncoding(b *testing.B) {
	for _, count := range []int{1, 16} {
		for _, size := range []int{0, 1026} {
			snapshot := DebugTraceSnapshot{TraceID: "trace-benchmark", Duration: time.Millisecond}
			for i := range count {
				snapshot.Spans = append(snapshot.Spans, DebugSpan{ID: uint32(i + 1), DebugSpanMeta: DebugSpanMeta{
					Name: "database.query", Category: "database", Service: "task", Operation: "getTask",
					DatabaseBackend: "postgresql", DatabaseName: "taskflow", Statement: strings.Repeat("東京", size/6),
				}})
			}
			b.Run(fmt.Sprintf("spans=%d/text=%d", count, size), func(b *testing.B) { benchmarkTraceEncoding(b, snapshot) })
		}
	}
}

func benchmarkTraceEncoding(b *testing.B, snapshot DebugTraceSnapshot) {
	for _, before := range []bool{true, false} {
		b.Run(fmt.Sprintf("binary/before=%v", before), func(b *testing.B) {
			b.ReportAllocs()
			var data []byte
			var err error
			for b.Loop() {
				if before {
					data = encodeDebugTraceBeforeTextErrors(snapshot)
				} else {
					data, err = EncodeDebugTrace(snapshot)
				}
			}
			decoded, decodeErr := DecodeDebugTrace(data)
			if err != nil || decodeErr != nil || len(decoded.Spans) != len(snapshot.Spans) || decoded.Spans[0].Statement != snapshot.Spans[0].Statement {
				b.Fatal("invalid benchmark trace")
			}
		})
		b.Run(fmt.Sprintf("json/before=%v", before), func(b *testing.B) {
			b.ReportAllocs()
			var data string
			var err error
			for b.Loop() {
				if before {
					data = debugTraceJSONBeforeTextErrors(snapshot)
				} else {
					data, err = DebugTraceJSON(snapshot)
				}
			}
			if err != nil || len(data) == 0 {
				b.Fatal("invalid benchmark JSON")
			}
		})
	}
}
