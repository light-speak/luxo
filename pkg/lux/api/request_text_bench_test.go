package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

func jsonTextBoundaryFixtures() []struct{ name, body string } {
	return []struct{ name, body string }{
		{"ascii16", `{"$id":1,"$api":"text","value":"abcdefghijklmnop"}`},
		{"ascii1024", `{"$id":1,"$api":"text","value":"` + strings.Repeat("a", 1024) + `"}`},
		{"unicode24", `{"$id":1,"$api":"text","value":"` + strings.Repeat("東京", 4) + `"}`},
		{"unicode1026", `{"$id":1,"$api":"text","value":"` + strings.Repeat("東京", 171) + `"}`},
		{"unicode72000", `{"$id":1,"$api":"text","value":"` + strings.Repeat("東京", 12000) + `"}`},
	}
}

// Both paths use identical pooled body reads and JSON parsing; HTTP I/O is excluded.
func BenchmarkJSONRequestTextBoundary(b *testing.B) {
	for _, fixture := range jsonTextBoundaryFixtures() {
		for _, before := range []bool{true, false} {
			name := "after"
			parse := ParseRequest
			if before {
				name, parse = "before", parseRequestBeforeText
			}
			b.Run(fixture.name+"/"+name, func(b *testing.B) {
				reader := strings.NewReader(fixture.body)
				req := &http.Request{Body: io.NopCloser(reader)}
				b.ReportAllocs()
				var got *Request
				var err error
				for b.Loop() {
					reader.Reset(fixture.body)
					got, err = parse(req)
				}
				if err != nil || got == nil || got.API != "text" || len(got.Params) != 1 {
					b.Fatalf("parse failed: %v / %v", got, err)
				}
			})
		}
	}
}

// This is only the newly added complete-message scan, not a WebSocket round trip.
func BenchmarkWSTextBoundaryScan(b *testing.B) {
	for _, fixture := range jsonTextBoundaryFixtures() {
		b.Run(fixture.name, func(b *testing.B) {
			data := []byte(fixture.body)
			b.ReportAllocs()
			for b.Loop() {
				if !utf8.Valid(data) {
					b.Fatal("invalid fixture")
				}
			}
		})
	}
}
