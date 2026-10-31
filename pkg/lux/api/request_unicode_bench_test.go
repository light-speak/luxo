package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

type jsonUnicodeFixture = struct{ name, body string }

func jsonUnicodeRequestFixtures() []jsonUnicodeFixture {
	return append(jsonTextBoundaryFixtures(),
		jsonUnicodeFixture{"escaped", `{"$id":1,"$api":"text","n\u0061me":"\uD800\uDC00"}`},
		jsonUnicodeFixture{"nested", `{"$id":1,"$api":"text","raw":{"items":[{"id":123456789012345678901234567890,"text":"東京"},{"id":1e9999}]}}`},
	)
}

func BenchmarkJSONRequestUnicode(b *testing.B) {
	for _, fixture := range jsonUnicodeRequestFixtures() {
		before, oldErr := parseRequestBeforeUnicode(makeReq(fixture.body))
		after, err := ParseRequest(makeReq(fixture.body))
		if oldErr != nil || err != nil || len(before.Params) != len(after.Params) {
			b.Fatalf("invalid fixture: %v / %v", oldErr, err)
		}
		for _, version := range []struct {
			name  string
			parse func(*http.Request) (*Request, error)
		}{{"before", parseRequestBeforeUnicode}, {"strict", ParseRequest}} {
			b.Run(fixture.name+"/"+version.name, func(b *testing.B) {
				reader := strings.NewReader(fixture.body)
				req := &http.Request{Body: io.NopCloser(reader)}
				b.ReportAllocs()
				for b.Loop() {
					reader.Reset(fixture.body)
					if got, err := version.parse(req); err != nil || got.API != "text" {
						b.Fatalf("parse failed: %v", err)
					}
				}
			})
		}
	}
}

func parseWSEnvelopeUnicode(data []byte) (map[string]json.RawMessage, bool, error) {
	raw, err := jsonutil.ParseObject(data)
	return raw, isStreamRequest(raw), err
}

func BenchmarkWSJSONEnvelopeUnicode(b *testing.B) {
	fixtures := append(jsonUnicodeRequestFixtures(), jsonUnicodeFixture{"subscribe", `{"$id":1,"$sub":"watch","value":"東京"}`})
	for _, fixture := range fixtures {
		data := []byte(fixture.body)
		old, oldStream, oldErr := parseWSEnvelopeBeforeUnicode(data)
		current, currentStream, err := parseWSEnvelopeUnicode(data)
		if oldErr != nil || err != nil || oldStream != currentStream || len(old) != len(current) {
			b.Fatal("invalid envelope fixture")
		}
		for key, value := range old {
			if !bytes.Equal(value, current[key]) {
				b.Fatal("envelope value changed")
			}
		}
		for _, version := range []struct {
			name  string
			parse func([]byte) (map[string]json.RawMessage, bool, error)
		}{{"before", parseWSEnvelopeBeforeUnicode}, {"strict", parseWSEnvelopeUnicode}} {
			b.Run(fixture.name+"/"+version.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, _, err := version.parse(data); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
