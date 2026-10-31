package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func TestDebugTraceRejectsInvalidOptionalText(t *testing.T) {
	snapshot := DebugTraceSnapshot{TraceID: "valid", Spans: []DebugSpan{{DebugSpanMeta: DebugSpanMeta{
		Name: "service.call", Category: "service", Service: "bad\xff",
	}}}}
	if data, err := EncodeDebugTrace(snapshot); !errors.Is(err, codec.ErrInvalidUTF8) || data != nil {
		t.Fatalf("invalid optional service name: %x / %v", data, err)
	}
}

func TestDebugTraceTextErrorPaths(t *testing.T) {
	for _, corrupt := range []func(*DebugSpanMeta){
		func(s *DebugSpanMeta) { s.Name = "\xff" }, func(s *DebugSpanMeta) { s.Category = "\xff" },
		func(s *DebugSpanMeta) { s.Service = "\xff" }, func(s *DebugSpanMeta) { s.Operation = "\xff" },
		func(s *DebugSpanMeta) { s.Field = "\xff" }, func(s *DebugSpanMeta) { s.FieldPath = "\xff" },
		func(s *DebugSpanMeta) { s.Dependency = "\xff" }, func(s *DebugSpanMeta) { s.Selection = "\xff" },
		func(s *DebugSpanMeta) { s.DatabaseBackend = "\xff" }, func(s *DebugSpanMeta) { s.DatabaseName = "\xff" },
		func(s *DebugSpanMeta) { s.DatabaseOperation = "\xff" }, func(s *DebugSpanMeta) { s.DatabaseResource = "\xff" },
		func(s *DebugSpanMeta) { s.Statement = "\xff" }, func(s *DebugSpanMeta) { s.Fingerprint = "\xff" },
		func(s *DebugSpanMeta) { s.ErrorCode = "\xff" },
	} {
		span := DebugSpan{DebugSpanMeta: DebugSpanMeta{Name: "stage", Category: "service"}}
		corrupt(&span.DebugSpanMeta)
		snapshot := DebugTraceSnapshot{TraceID: "valid", Spans: []DebugSpan{span}}
		if data, err := EncodeDebugTrace(snapshot); data != nil || err == nil {
			t.Fatal("partial Binary trace published")
		}
		if data, err := DebugTraceJSON(snapshot); data != "" || err == nil {
			t.Fatal("partial JSON trace published")
		}
		if data, err := EncodeDebugTraceHeader(snapshot); data != "" || err == nil {
			t.Fatal("partial trace header published")
		}
		if data, err := EncodeDebugResponseEnvelope([]byte("business"), snapshot); data != nil || err == nil {
			t.Fatal("partial debug envelope published")
		}
		decoded, err := DecodeDebugTrace(EncodeDebugTraceResult(snapshot))
		if err != nil || !decoded.EncodingFailed || len(decoded.Spans) != 0 {
			t.Fatalf("failure marker: %+v / %v", decoded, err)
		}
	}
	if data, err := EncodeDebugTrace(DebugTraceSnapshot{TraceID: "bad\xff"}); data != nil || err == nil {
		t.Fatal("invalid trace ID accepted")
	}
}

func TestDebugTraceFailureMergeAndSampling(t *testing.T) {
	ctx, trace := WithDebugTrace(context.Background(), "parent")
	_, call := trace.StartSpan(ctx, DebugSpanMeta{Name: "call", Category: "service"})
	trace.MergeSpan(call, DebugTraceSnapshot{EncodingFailed: true})
	trace.FinishSpan(call)
	snapshot := trace.Snapshot()
	decoded, err := DecodeDebugTrace(EncodeDebugTraceResult(snapshot))
	if err != nil || !decoded.EncodingFailed || len(decoded.Spans) != 1 {
		t.Fatalf("merge lost failure: %+v / %v", decoded, err)
	}
	text, err := DebugTraceJSON(snapshot)
	if err != nil || !strings.Contains(text, `"encodingFailed":true`) || !json.Valid([]byte(text)) {
		t.Fatalf("sample lost failure: %s / %v", text, err)
	}
	trace.record(DebugSpan{DebugSpanMeta: DebugSpanMeta{Name: "stage", Category: "service", Statement: "private\xff"}})
	req := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	record := newTraceRecord(req, "ok", nil, time.Now(), time.Millisecond, nil)
	if record.StatusCode != 200 || record.Spans != failedDebugTraceJSON {
		t.Fatalf("sample changed business result: %+v", record)
	}
}

func TestDebugTraceHTTPEncodingFailurePreservesResponse(t *testing.T) {
	for _, envelope := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			router := NewRouter()
			router.SetDevMode(true)
			router.Handle("ping", func(ctx context.Context, req *Request) error {
				DebugTrace(ctx).record(DebugSpan{DebugSpanMeta: DebugSpanMeta{Name: "bad\n\xff", Category: "service"}})
				if fail {
					return errors.New("business failure")
				}
				req.Buf.AppendInt(7)
				return nil
			})
			req := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(`{"$api":"ping"}`))
			req.Header.Set(DebugTraceRequestHeader, "true")
			if envelope {
				req.Header.Set(DebugTraceEnvelopeHeader, "true")
			}
			w := httptest.NewRecorder()
			TraceMiddleware(router).ServeHTTP(w, req)
			body := w.Body.Bytes()
			var snapshot DebugTraceSnapshot
			var err error
			if envelope {
				body, snapshot, err = DecodeDebugResponseEnvelope(body)
			} else {
				snapshot, err = DecodeDebugTraceHeader(w.Header().Get(DebugTraceResponseHeader))
			}
			if err != nil || !snapshot.EncodingFailed {
				t.Fatalf("missing diagnostic failure: %+v / %v", snapshot, err)
			}
			if fail {
				if !json.Valid(body) || !strings.Contains(string(body), `"code":500`) {
					t.Fatalf("business error changed: %s", body)
				}
			} else if w.Code != 200 || string(body) != `{"data":7}` {
				t.Fatalf("business result changed: %d / %s", w.Code, body)
			}
			if fail && w.Code != 500 {
				t.Fatalf("business error status changed: %d", w.Code)
			}
			if w.Header().Get("Server-Timing") != "" {
				t.Fatal("unsafe timing header published")
			}
		}
	}
}

func TestDebugTraceFailureMarkerValidation(t *testing.T) {
	header, encodeErr := EncodeDebugTraceHeader(DebugTraceSnapshot{TraceID: "header"})
	decoded, decodeErr := DecodeDebugTraceHeader(header)
	if encodeErr != nil || decodeErr != nil || decoded.TraceID != "header" {
		t.Fatalf("valid header: %+v / %v / %v", decoded, encodeErr, decodeErr)
	}
	for _, data := range [][]byte{{3, 5, 1, 0}, {4, 5, 1}, {4, 5, 2, 0}, {4, 5, 0, 0}, {4, 5, 1, 0, 0}} {
		if _, err := DecodeDebugTrace(data); err == nil {
			t.Fatalf("malformed failure accepted: %x", data)
		}
	}
	if validServerTiming("bad\x7f") || validServerTiming("bad\n") || validServerTiming("bad\xff") || !validServerTiming("stage\tvalue") {
		t.Fatal("timing header validation")
	}
}

func TestDebugTraceJSONControlCharacters(t *testing.T) {
	snapshot := DebugTraceSnapshot{Spans: []DebugSpan{{DebugSpanMeta: DebugSpanMeta{Name: "stage\x7f", Category: "service"}}}}
	if data, err := DebugTraceJSON(snapshot); err != nil || !json.Valid([]byte(data)) {
		t.Fatalf("trace text is not JSON: %s / %v", data, err)
	}
}

func TestDebugTraceEncodingOwnsOutput(t *testing.T) {
	snapshot := DebugTraceSnapshot{TraceID: "owned", Spans: []DebugSpan{{DebugSpanMeta: DebugSpanMeta{Name: "stage", Category: "service", Statement: "original"}}}}
	first := mustTraceEncode[[]byte](t)(EncodeDebugTrace(snapshot))
	saved := bytes.Clone(first)
	snapshot.Spans[0].Statement = "different"
	second := mustTraceEncode[[]byte](t)(EncodeDebugTrace(snapshot))
	if !bytes.Equal(first, saved) {
		t.Fatal("later encoding modified prior output")
	}
	clear(first)
	decoded, err := DecodeDebugTrace(second)
	if err != nil || decoded.Spans[0].Statement != "different" {
		t.Fatal("outputs share mutable storage")
	}
}

func mustTraceEncode[T string | []byte](t *testing.T) func(T, error) T {
	t.Helper()
	return func(value T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
}

func TestDebugTraceInvalidMetadataIsDiagnosticFailure(t *testing.T) {
	valid := DebugSpan{DebugSpanMeta: DebugSpanMeta{Name: "stage", Category: "service"}}
	for _, snapshot := range []DebugTraceSnapshot{
		{}, {TraceID: "valid", Duration: -1},
		{TraceID: "valid", Spans: []DebugSpan{{}}},
		{TraceID: "valid", Spans: []DebugSpan{{DebugSpanMeta: valid.DebugSpanMeta, Start: -1}}},
		{TraceID: "valid", Spans: []DebugSpan{{DebugSpanMeta: valid.DebugSpanMeta, Duration: -1}}},
	} {
		if data, err := EncodeDebugTrace(snapshot); data != nil || err == nil {
			t.Fatal("invalid metadata encoded")
		}
		result, err := DecodeDebugTrace(EncodeDebugTraceResult(snapshot))
		if err != nil || !result.EncodingFailed {
			t.Fatal("metadata failure not isolated")
		}
		if len(snapshot.Spans) > 0 {
			if data, err := DebugTraceJSON(snapshot); data != "" || err == nil {
				t.Fatal("invalid span persisted")
			}
		}
	}
	valid.Statement = strings.Repeat("x", MaxDebugResponseTraceBytes)
	if data, err := EncodeDebugTrace(DebugTraceSnapshot{TraceID: "valid", Spans: []DebugSpan{valid}}); data != nil || err == nil {
		t.Fatal("oversized trace encoded")
	}
}

func FuzzDebugTraceTextEncoding(f *testing.F) {
	for _, value := range []string{"", "東京\ufeff", "\x7f\x00\n", "bad\xff"} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value string) {
		snapshot := DebugTraceSnapshot{TraceID: "valid", Spans: []DebugSpan{{DebugSpanMeta: DebugSpanMeta{Name: "stage", Category: "service", Service: value}}}}
		binary, binaryErr := EncodeDebugTrace(snapshot)
		text, textErr := DebugTraceJSON(snapshot)
		if !utf8.ValidString(value) {
			if binaryErr == nil || textErr == nil || binary != nil || text != "" {
				t.Fatal("invalid text published")
			}
			return
		}
		if len(value) > MaxDebugResponseTraceBytes/2 {
			return
		}
		if binaryErr != nil || textErr != nil || !json.Valid([]byte(text)) {
			t.Fatalf("valid text rejected: %v / %v", binaryErr, textErr)
		}
		decoded, err := DecodeDebugTrace(binary)
		if err != nil || len(decoded.Spans) != 1 || decoded.Spans[0].Service != value {
			t.Fatal("Binary text changed")
		}
		var spans []struct{ Service string }
		if err := json.Unmarshal([]byte(text), &spans); err != nil || len(spans) != 1 || spans[0].Service != value {
			t.Fatal("JSON text changed")
		}
	})
}
