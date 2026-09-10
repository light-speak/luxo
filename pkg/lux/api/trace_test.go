package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

func TestTraceMiddlewareGeneratesID(t *testing.T) {
	var gotTraceID string
	handler := TraceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceID = TraceID(r.Context())
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if gotTraceID == "" {
		t.Error("trace ID should be generated")
	}
	if w.Header().Get("X-Trace-Id") == "" {
		t.Error("X-Trace-Id response header should be set")
	}
	if w.Header().Get("X-Trace-Id") != gotTraceID {
		t.Error("response header should match context value")
	}
}

func TestTraceMiddlewareReusesRequestId(t *testing.T) {
	var gotTraceID string
	handler := TraceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceID = TraceID(r.Context())
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Request-Id", "custom-trace-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if gotTraceID != "custom-trace-123" {
		t.Errorf("got %q, want custom-trace-123", gotTraceID)
	}
	if w.Header().Get("X-Trace-Id") != "custom-trace-123" {
		t.Error("response header should use incoming request ID")
	}
}

func TestTraceIDEmptyContext(t *testing.T) {
	id := TraceID(context.Background())
	if id != "" {
		t.Errorf("got %q, want empty string", id)
	}
}

func TestTraceMiddlewareKeepsDebugTracingDisabledByDefault(t *testing.T) {
	handler := TraceMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if DebugTrace(r.Context()) != nil {
			t.Fatal("debug trace should be opt-in")
		}
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/luvia", nil))
}

func TestDebugTraceConcurrentSpansAndBinaryRoundTrip(t *testing.T) {
	ctx, trace := WithDebugTrace(context.Background(), "trace-concurrent")
	if DebugTrace(ctx) != trace {
		t.Fatal("trace missing from context")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started := trace.Start()
			trace.Finish(started, DebugSpanMeta{Name: "service.call", Category: "service", Service: "task"})
		}()
	}
	wg.Wait()

	encoded := EncodeDebugTrace(trace.Snapshot())
	decoded, err := DecodeDebugTrace(encoded)
	if err != nil {
		t.Fatalf("decode debug trace: %v", err)
	}
	if decoded.TraceID != "trace-concurrent" || len(decoded.Spans) != 8 {
		t.Fatalf("decoded trace = %+v", decoded)
	}
	for _, span := range decoded.Spans {
		if span.Name != "service.call" || span.Category != "service" || span.Service != "task" {
			t.Fatalf("decoded span = %+v", span)
		}
	}
}

func TestDebugTracePreservesExecutionDAGAndFieldPath(t *testing.T) {
	ctx, trace := WithDebugTrace(context.Background(), "trace-dag")
	handlerCtx, handler := trace.StartSpan(ctx, DebugSpanMeta{
		Name: "service.handler", Category: "service", Service: "task", Operation: "getTask",
	})
	relationCtx := WithTraceFieldPath(handlerCtx, "project")
	_, call := trace.StartSpan(relationCtx, DebugSpanMeta{
		Name: "service.call", Category: "service", Service: "project", Operation: "svc:batchLoad:Project",
		Selection: "id,name", Dependency: TraceDependencySequential,
	})
	trace.FinishSpan(call)
	trace.FinishSpan(handler)

	decoded, err := DecodeDebugTrace(EncodeDebugTrace(trace.Snapshot()))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Spans) != 2 {
		t.Fatalf("spans = %+v", decoded.Spans)
	}
	var handlerSpan, callSpan *DebugSpan
	for index := range decoded.Spans {
		span := &decoded.Spans[index]
		switch span.Name {
		case "service.handler":
			handlerSpan = span
		case "service.call":
			callSpan = span
		}
	}
	if handlerSpan == nil || callSpan == nil {
		t.Fatalf("DAG spans = %+v", decoded.Spans)
	}
	if handlerSpan.ID == 0 || callSpan.ParentID != handlerSpan.ID {
		t.Fatalf("DAG IDs = handler %d call parent %d", handlerSpan.ID, callSpan.ParentID)
	}
	if callSpan.FieldPath != "project" || callSpan.Field != "project" || callSpan.Selection != "id,name" || callSpan.Dependency != TraceDependencySequential {
		t.Fatalf("call metadata = %+v", callSpan)
	}
}

func TestDebugTraceNilAndInactivePathsAreNoops(t *testing.T) {
	ctx := context.Background()
	var trace *DebugTraceSession
	if trace.Sampled() || trace.DatabaseDetails() || !trace.Start().IsZero() {
		t.Fatal("nil trace must be inactive")
	}
	trace.Finish(time.Now(), DebugSpanMeta{Name: "ignored"})
	spanCtx, handle := trace.StartSpan(ctx, DebugSpanMeta{Name: "ignored"})
	trace.FinishSpan(handle)
	trace.record(DebugSpan{})
	trace.MergeSpan(handle, DebugTraceSnapshot{Spans: []DebugSpan{{}}})
	if spanCtx != ctx || trace.Snapshot().TraceID != "" {
		t.Fatal("nil trace changed request state")
	}
	if trace.StartDatabaseQuery(ctx, lux.DatabaseTraceMeta{}) != ctx || trace.StartDatabaseAcquire(ctx, lux.DatabasePoolTraceMeta{}) != ctx {
		t.Fatal("nil database trace changed context")
	}
	trace.FinishDatabaseQuery(ctx, lux.DatabaseTraceResult{})
	trace.FinishDatabaseAcquire(ctx, errors.New("ignored"))
	if WithTraceFieldPath(ctx, "owner") != ctx {
		t.Fatal("inactive field path changed context")
	}
	tracedCtx, _ := WithDebugTrace(ctx, "trace-noop")
	if WithTraceFieldPath(tracedCtx, "") != tracedCtx {
		t.Fatal("empty field path changed context")
	}
}

func TestRemoteTraceAndMergePreserveNestedDAG(t *testing.T) {
	rootCtx, root := WithDebugTrace(context.Background(), "trace-merge")
	_, call := root.StartSpan(rootCtx, DebugSpanMeta{Name: "service.call", Category: "service", Service: "task", Field: "task"})
	remoteCtx, remote := WithRemoteTrace(context.Background(), "trace-merge", "task.project", true)
	if TraceID(remoteCtx) != "trace-merge" || TraceFieldPath(remoteCtx) != "task.project" || !remote.DatabaseDetails() {
		t.Fatalf("remote trace context = id %q path %q details %v", TraceID(remoteCtx), TraceFieldPath(remoteCtx), remote.DatabaseDetails())
	}
	serviceCtx, service := remote.StartSpan(remoteCtx, DebugSpanMeta{Name: "service.handler", Category: "service", Service: "project"})
	_, query := remote.StartSpan(serviceCtx, DebugSpanMeta{Name: "database.query", Category: "database"})
	remote.FinishSpan(query)
	remote.FinishSpan(service)
	root.MergeSpan(call, remote.Snapshot())
	root.FinishSpan(call)

	spans := root.Snapshot().Spans
	if len(spans) != 3 {
		t.Fatalf("merged spans = %+v", spans)
	}
	var callSpan, serviceSpan, querySpan *DebugSpan
	for index := range spans {
		span := &spans[index]
		switch span.Name {
		case "service.call":
			callSpan = span
		case "service.handler":
			serviceSpan = span
		case "database.query":
			querySpan = span
		}
	}
	if callSpan == nil || serviceSpan == nil || querySpan == nil || serviceSpan.ParentID != callSpan.ID || querySpan.ParentID != serviceSpan.ID {
		t.Fatalf("merged DAG = %+v", spans)
	}
	if serviceSpan.FieldPath != "task.project" || JoinTraceFieldPath(serviceSpan.FieldPath, "owner") != "task.project.owner" {
		t.Fatalf("merged field path = %q", serviceSpan.FieldPath)
	}
}

func TestMergeSpanRemapsOrphanParentsAndTruncation(t *testing.T) {
	ctx, trace := WithDebugTrace(context.Background(), "trace-remap")
	_, call := trace.StartSpan(ctx, DebugSpanMeta{Name: "service.call", Category: "service"})
	trace.MergeSpan(call, DebugTraceSnapshot{Truncated: true, Spans: []DebugSpan{
		{DebugSpanMeta: DebugSpanMeta{Name: "orphan", Category: "service"}, ID: 10, ParentID: 99},
		{DebugSpanMeta: DebugSpanMeta{Name: "child", Category: "database"}, ID: 11, ParentID: 10},
	}})
	spans := trace.Snapshot().Spans
	if len(spans) != 2 || spans[0].ParentID != call.ID() || spans[1].ParentID != spans[0].ID || !trace.truncated {
		t.Fatalf("remapped spans = %+v truncated=%v", spans, trace.truncated)
	}

	trace.spans = make([]DebugSpan, maxDebugTraceSpans)
	trace.truncated = false
	trace.MergeSpan(call, DebugTraceSnapshot{Spans: []DebugSpan{{DebugSpanMeta: DebugSpanMeta{Name: "overflow", Category: "test"}, ID: 1}}})
	if !trace.truncated || len(trace.spans) != maxDebugTraceSpans {
		t.Fatal("merged span limit was not enforced")
	}
	trace.MergeSpan(DebugSpanHandle{}, DebugTraceSnapshot{Spans: []DebugSpan{{}}})
	trace.MergeSpan(call, DebugTraceSnapshot{})
}

func TestSnapshotOrdersEqualStartsByLongestDuration(t *testing.T) {
	trace := &DebugTraceSession{traceID: "ordered", started: time.Now(), spans: []DebugSpan{
		{DebugSpanMeta: DebugSpanMeta{Name: "short", Category: "test"}, Start: time.Millisecond, Duration: time.Millisecond},
		{DebugSpanMeta: DebugSpanMeta{Name: "long", Category: "test"}, Start: time.Millisecond, Duration: 2 * time.Millisecond},
	}}
	if got := trace.Snapshot().Spans; got[0].Name != "long" || got[1].Name != "short" {
		t.Fatalf("equal-start span order = %+v", got)
	}
}

func TestSampledTraceIsInternalOnly(t *testing.T) {
	ctx, trace := WithSampledTrace(context.Background(), "trace-sampled")
	if DebugTrace(ctx) != trace || !trace.Sampled() {
		t.Fatal("sampled trace was not attached")
	}
	header := http.Header{}
	writeDebugTraceHeaders(header, trace)
	if header.Get(DebugTraceResponseHeader) != "" || header.Get("Server-Timing") != "" {
		t.Fatalf("sampled trace leaked response headers: %v", header)
	}
}

func TestDecodeDebugTraceRejectsMalformedData(t *testing.T) {
	for _, data := range [][]byte{nil, {99, 1}, {1, 1, 'x'}} {
		if _, err := DecodeDebugTrace(data); err == nil {
			t.Fatalf("expected malformed trace error for %v", data)
		}
	}
}

func TestRouterReturnsDebugTimingHeader(t *testing.T) {
	router := NewRouter()
	router.SetDevMode(true)
	metrics := &mockTraceMetrics{}
	router.SetMetricsCollector(metrics)
	router.Handle("ping", func(_ context.Context, req *Request) error {
		req.Buf.AppendInt(7)
		return nil
	})
	req := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(`{"$api":"ping"}`))
	req.Header.Set(DebugTraceRequestHeader, "true")
	w := httptest.NewRecorder()
	TraceMiddleware(router).ServeHTTP(w, req)

	snapshot, err := DecodeDebugTraceHeader(w.Header().Get(DebugTraceResponseHeader))
	if err != nil {
		t.Fatalf("decode trace header: %v", err)
	}
	want := map[string]bool{"request.decode": false, "request.prepare": false, "handler.execute": false, "response.encode": false}
	for _, span := range snapshot.Spans {
		if _, ok := want[span.Name]; ok {
			want[span.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing %s span in %+v", name, snapshot.Spans)
		}
	}
	if w.Header().Get("Server-Timing") == "" {
		t.Fatal("Server-Timing header should be present")
	}
	if len(metrics.traces) != 1 || metrics.traces[0].Spans == "" {
		t.Fatalf("persisted trace spans = %+v", metrics.traces)
	}
}

func TestRouterRequiresDebugKeyOutsideDevelopment(t *testing.T) {
	router := NewRouter()
	router.SetDebugTraceKey("project-secret")
	router.Handle("ping", func(_ context.Context, req *Request) error {
		req.Buf.AppendInt(7)
		return nil
	})

	call := func(key string) string {
		req := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(`{"$api":"ping"}`))
		req.Header.Set(DebugTraceRequestHeader, "true")
		if key != "" {
			req.Header.Set(DebugTraceKeyHeader, key)
		}
		w := httptest.NewRecorder()
		TraceMiddleware(router).ServeHTTP(w, req)
		return w.Header().Get(DebugTraceResponseHeader)
	}

	if traceHeader := call(""); traceHeader != "" {
		t.Fatalf("unauthorized trace header = %q", traceHeader)
	}
	if traceHeader := call("wrong"); traceHeader != "" {
		t.Fatalf("wrong-key trace header = %q", traceHeader)
	}
	if traceHeader := call("project-secret"); traceHeader == "" {
		t.Fatal("authorized request should receive a debug trace")
	}
}

func TestDebugTraceJSONUsesMillisecondsAndAttribution(t *testing.T) {
	got := DebugTraceJSON(DebugTraceSnapshot{Spans: []DebugSpan{{
		DebugSpanMeta: DebugSpanMeta{Name: "service.call", Category: "service", Service: "user", Operation: "getUser", Field: "owner", Selection: "id,name"},
		ID:            7,
		ParentID:      3,
		Start:         time.Millisecond,
		Duration:      2500 * time.Microsecond,
	}}})
	want := `[{"id":7,"parentId":3,"name":"service.call","category":"service","service":"user","operation":"getUser","field":"owner","fieldPath":"","selection":"id,name","dependency":"","start":1,"duration":2.5}]`
	if got != want {
		t.Fatalf("debug trace JSON = %s, want %s", got, want)
	}
}

func TestDebugTraceJSONIncludesDatabaseMetadata(t *testing.T) {
	got := DebugTraceJSON(DebugTraceSnapshot{Spans: []DebugSpan{{
		DebugSpanMeta: DebugSpanMeta{
			Name: "database.query", Category: "database", DatabaseBackend: "postgresql", DatabaseName: "taskflow",
			DatabaseOperation: "SELECT", DatabaseResource: "tasks", Statement: "SELECT id FROM tasks WHERE id = $1",
			Fingerprint: "abc123", ErrorCode: "23505", ArgumentCount: 1, RowsAffected: 0, RowsKnown: true, StatementTruncated: true,
		},
		ID: 9, ParentID: 4, Start: time.Millisecond, Duration: 2 * time.Millisecond,
	}}})
	want := `[{"id":9,"parentId":4,"name":"database.query","category":"database","service":"","operation":"","field":"","fieldPath":"","selection":"","dependency":"","databaseBackend":"postgresql","databaseName":"taskflow","databaseOperation":"SELECT","databaseResource":"tasks","statement":"SELECT id FROM tasks WHERE id = $1","fingerprint":"abc123","argumentCount":1,"rowsAffected":0,"errorCode":"23505","statementTruncated":true,"start":1,"duration":2}]`
	if got != want {
		t.Fatalf("database trace JSON = %s, want %s", got, want)
	}
}

func TestDebugTraceCapsSpansAndMarksTruncation(t *testing.T) {
	_, trace := WithDebugTrace(context.Background(), "trace-cap")
	for i := 0; i < maxDebugTraceSpans+1; i++ {
		trace.record(DebugSpan{DebugSpanMeta: DebugSpanMeta{Name: "span", Category: "test"}})
	}
	snapshot := trace.Snapshot()
	if len(snapshot.Spans) != maxDebugTraceSpans || !snapshot.Truncated {
		t.Fatalf("capped snapshot = len %d truncated %v", len(snapshot.Spans), snapshot.Truncated)
	}
	decoded, err := DecodeDebugTrace(EncodeDebugTrace(snapshot))
	if err != nil || !decoded.Truncated {
		t.Fatalf("decoded truncation = %+v, %v", decoded, err)
	}
}

func TestDebugTraceRecordsDatabaseQueryAndPoolWait(t *testing.T) {
	ctx, trace := WithDebugTrace(context.Background(), "trace-database")
	handlerCtx, handler := trace.StartSpan(ctx, DebugSpanMeta{Name: "service.handler", Category: "service", Service: "task", Operation: "listTasks"})
	databaseTracer := lux.DatabaseTraceSinkFromContext(handlerCtx)
	if databaseTracer == nil || !trace.DatabaseDetails() {
		t.Fatal("debug trace must expose detailed database tracing")
	}

	acquireCtx := databaseTracer.StartDatabaseAcquire(handlerCtx, lux.DatabasePoolTraceMeta{Backend: "postgresql", Database: "taskflow"})
	databaseTracer.FinishDatabaseAcquire(acquireCtx, nil)
	queryCtx := databaseTracer.StartDatabaseQuery(handlerCtx, lux.DatabaseTraceMeta{
		Backend: "postgresql", Database: "taskflow", Operation: "SELECT", Resource: "tasks",
		Statement: "SELECT id, title FROM tasks WHERE project_id = $1", Fingerprint: "f00d", ArgumentCount: 1,
	})
	databaseTracer.FinishDatabaseQuery(queryCtx, lux.DatabaseTraceResult{Command: "SELECT", RowsAffected: 3, RowsKnown: true})
	trace.FinishSpan(handler)

	decoded, err := DecodeDebugTrace(EncodeDebugTrace(trace.Snapshot()))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Spans) != 3 {
		t.Fatalf("database spans = %+v", decoded.Spans)
	}
	var query, acquire *DebugSpan
	for index := range decoded.Spans {
		span := &decoded.Spans[index]
		switch span.Name {
		case "database.query":
			query = span
		case "database.acquire":
			acquire = span
		}
	}
	if query == nil || query.ParentID != handler.ID() || query.DatabaseBackend != "postgresql" || query.DatabaseName != "taskflow" || query.DatabaseOperation != "SELECT" || query.DatabaseResource != "tasks" {
		t.Fatalf("query attribution = %+v", query)
	}
	if query.Statement == "" || query.Fingerprint != "f00d" || query.ArgumentCount != 1 || !query.RowsKnown || query.RowsAffected != 3 {
		t.Fatalf("query details = %+v", query)
	}
	if acquire == nil || acquire.ParentID != handler.ID() || acquire.DatabaseName != "taskflow" {
		t.Fatalf("pool attribution = %+v", acquire)
	}
}

func TestDebugTraceDatabaseMissingHandlesAndErrors(t *testing.T) {
	ctx, trace := WithDebugTrace(context.Background(), "trace-database-errors")
	trace.FinishDatabaseQuery(ctx, lux.DatabaseTraceResult{Command: "UPDATE"})
	trace.FinishDatabaseAcquire(ctx, errors.New("missing handle"))

	queryCtx := trace.StartDatabaseQuery(ctx, lux.DatabaseTraceMeta{Backend: "postgresql", Operation: "SELECT"})
	trace.FinishDatabaseQuery(queryCtx, lux.DatabaseTraceResult{Command: "UPDATE", ErrorCode: "23505", RowsKnown: true, RowsAffected: 1})
	acquireCtx := trace.StartDatabaseAcquire(ctx, lux.DatabasePoolTraceMeta{Backend: "postgresql"})
	trace.FinishDatabaseAcquire(acquireCtx, errors.New("pool closed"))
	spans := trace.Snapshot().Spans
	if len(spans) != 2 || spans[0].DatabaseOperation != "UPDATE" || spans[0].ErrorCode != "23505" || spans[1].ErrorCode != "acquire_failed" {
		t.Fatalf("database error spans = %+v", spans)
	}
}

func TestSampledTraceSuppressesSQLStatement(t *testing.T) {
	ctx, trace := WithSampledTrace(context.Background(), "trace-sampled-database")
	if trace.DatabaseDetails() {
		t.Fatal("ordinary production sampling must not retain SQL bodies")
	}
	databaseTracer := lux.DatabaseTraceSinkFromContext(ctx)
	queryCtx := databaseTracer.StartDatabaseQuery(ctx, lux.DatabaseTraceMeta{
		Backend: "postgresql", Statement: "SELECT secret FROM credentials", Fingerprint: "safe-fingerprint",
	})
	databaseTracer.FinishDatabaseQuery(queryCtx, lux.DatabaseTraceResult{})
	span := trace.Snapshot().Spans[0]
	if span.Statement != "" || span.Fingerprint != "safe-fingerprint" {
		t.Fatalf("sampled database span = %+v", span)
	}
}

func TestDebugResponseEnvelopeRoundTrip(t *testing.T) {
	snapshot := DebugTraceSnapshot{TraceID: "trace-envelope", Duration: time.Millisecond, Spans: []DebugSpan{{
		DebugSpanMeta: DebugSpanMeta{Name: "database.query", Category: "database", Statement: "SELECT id FROM tasks"},
		ID:            1, Duration: time.Millisecond,
	}}}
	body := []byte(`{"data":{"id":1}}`)
	decodedBody, decodedTrace, err := DecodeDebugResponseEnvelope(EncodeDebugResponseEnvelope(body, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if string(decodedBody) != string(body) || decodedTrace.TraceID != snapshot.TraceID || decodedTrace.Spans[0].Statement != "SELECT id FROM tasks" {
		t.Fatalf("decoded envelope = %s %+v", decodedBody, decodedTrace)
	}
	legacy := []byte{legacyDebugResponseEnvelopeVersion, byte(len(body))}
	legacy = append(legacy, body...)
	legacy = append(legacy, EncodeDebugTrace(snapshot)...)
	legacyBody, legacyTrace, err := DecodeDebugResponseEnvelope(legacy)
	if err != nil || string(legacyBody) != string(body) || legacyTrace.TraceID != snapshot.TraceID {
		t.Fatalf("decoded legacy envelope = %s %+v, %v", legacyBody, legacyTrace, err)
	}
	for _, malformed := range [][]byte{nil, {99}, {1, 4, 1}} {
		if _, _, err := DecodeDebugResponseEnvelope(malformed); err == nil {
			t.Fatalf("malformed envelope %v was accepted", malformed)
		}
	}
	oversized := make([]byte, 1+MaxDebugResponseTraceBytes+1+debugResponseEnvelopeFooterSize)
	oversized[0] = debugResponseEnvelopeVersion
	binary.BigEndian.PutUint64(oversized[len(oversized)-debugResponseEnvelopeFooterSize:], MaxDebugResponseTraceBytes+1)
	copy(oversized[len(oversized)-len(debugResponseEnvelopeMagic):], debugResponseEnvelopeMagic)
	if _, _, err := DecodeDebugResponseEnvelope(oversized); err == nil || !strings.Contains(err.Error(), "trace exceeds") {
		t.Fatalf("oversized trace error = %v", err)
	}
}

func TestDebugTraceDecodeCompatibilityAndValidation(t *testing.T) {
	snapshot := DebugTraceSnapshot{TraceID: "legacy", Duration: time.Millisecond, Spans: []DebugSpan{{
		DebugSpanMeta: DebugSpanMeta{Name: "service.call", Category: "service", Field: "project", ErrorCode: "remote_error", StatementTruncated: true},
		ID:            17, Start: time.Microsecond, Duration: time.Millisecond,
	}}}
	legacy := EncodeDebugTrace(snapshot)
	legacy[0] = 1
	decoded, err := DecodeDebugTrace(legacy)
	if err != nil || decoded.Spans[0].ID != 1 || decoded.Spans[0].FieldPath != "project" || decoded.Spans[0].ErrorCode != "remote_error" || !decoded.Spans[0].StatementTruncated {
		t.Fatalf("legacy trace = %+v, %v", decoded, err)
	}

	for name, data := range map[string][]byte{
		"outer unknown field": encodeTraceForTest("trace", int64(time.Millisecond), nil, func(enc *codec.Encoder) { enc.WriteFieldString(99, "x") }),
		"missing trace id":    encodeTraceForTest("", int64(time.Millisecond), nil, nil),
		"negative duration":   encodeTraceForTest("trace", -1, nil, nil),
		"invalid span":        encodeTraceForTest("trace", int64(time.Millisecond), [][]byte{encodeSpanForTest("", "service", 0, 0, nil)}, nil),
		"span unknown field":  encodeTraceForTest("trace", int64(time.Millisecond), [][]byte{encodeSpanForTest("span", "service", 0, 0, func(enc *codec.Encoder) { enc.WriteFieldString(99, "x") })}, nil),
		"negative span start": encodeTraceForTest("trace", int64(time.Millisecond), [][]byte{encodeSpanForTest("span", "service", -1, 0, nil)}, nil),
	} {
		if _, err := DecodeDebugTrace(data); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	if _, err := DecodeDebugTraceHeader("not base64!"); err == nil {
		t.Fatal("invalid trace header was accepted")
	}
}

func TestDebugTraceDecodeRejectsBrokenTerminators(t *testing.T) {
	validSpan := encodeSpanForTest("span", "service", 0, 0, nil)
	for name, span := range map[string][]byte{
		"truncated span": validSpan[:len(validSpan)-1],
		"trailing span":  append(append([]byte(nil), validSpan...), 1),
	} {
		data := encodeTraceForTest("trace", int64(time.Millisecond), [][]byte{span}, nil)
		if _, err := DecodeDebugTrace(data); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	valid := encodeTraceForTest("trace", int64(time.Millisecond), nil, nil)
	for name, data := range map[string][]byte{
		"truncated trace": valid[:len(valid)-1],
		"trailing trace":  append(append([]byte(nil), valid...), 1),
	} {
		if _, err := DecodeDebugTrace(data); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func encodeTraceForTest(traceID string, duration int64, spans [][]byte, extra func(*codec.Encoder)) []byte {
	var enc codec.Encoder
	enc.WriteFieldString(1, traceID)
	enc.WriteFieldInt(2, duration)
	enc.WriteFieldBytesArray(3, spans)
	if extra != nil {
		extra(&enc)
	}
	enc.WriteEnd()
	return append([]byte{3}, enc.Bytes()...)
}

func encodeSpanForTest(name, category string, start, duration int64, extra func(*codec.Encoder)) []byte {
	var enc codec.Encoder
	enc.WriteFieldString(1, name)
	enc.WriteFieldString(2, category)
	enc.WriteFieldInt(6, start)
	enc.WriteFieldInt(7, duration)
	if extra != nil {
		extra(&enc)
	}
	enc.WriteEnd()
	return append([]byte(nil), enc.Bytes()...)
}

func TestDebugResponseEnvelopeRejectsEveryMalformedBoundary(t *testing.T) {
	validMagic := func(size int) []byte {
		data := make([]byte, size)
		data[0] = debugResponseEnvelopeVersion
		copy(data[len(data)-len(debugResponseEnvelopeMagic):], debugResponseEnvelopeMagic)
		return data
	}
	badFooter := make([]byte, 1+debugResponseEnvelopeFooterSize)
	badFooter[0] = debugResponseEnvelopeVersion
	zeroTrace := validMagic(1 + debugResponseEnvelopeFooterSize)
	longTrace := validMagic(1 + debugResponseEnvelopeFooterSize)
	binary.BigEndian.PutUint64(longTrace[1:], 1)
	badTrace := validMagic(2 + debugResponseEnvelopeFooterSize)
	badTrace[1] = 99
	binary.BigEndian.PutUint64(badTrace[2:], 1)
	legacyTooLarge := append([]byte{legacyDebugResponseEnvelopeVersion, 0}, make([]byte, MaxDebugResponseTraceBytes+1)...)

	for name, data := range map[string][]byte{
		"short v2":           {debugResponseEnvelopeVersion, 0, 0},
		"bad footer":         badFooter,
		"zero trace":         zeroTrace,
		"too long trace":     longTrace,
		"invalid trace":      badTrace,
		"legacy varint":      {legacyDebugResponseEnvelopeVersion, 0x80, 0x80},
		"legacy body length": {legacyDebugResponseEnvelopeVersion, 2, 'x'},
		"legacy no trace":    {legacyDebugResponseEnvelopeVersion, 1, 'x'},
		"legacy oversized":   legacyTooLarge,
		"legacy bad trace":   {legacyDebugResponseEnvelopeVersion, 0, 99},
	} {
		if _, _, err := DecodeDebugResponseEnvelope(data); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

type chunkResponseWriter struct {
	header http.Header
	status int
	chunks [][]byte
}

type failingResponseWriter struct {
	header http.Header
	writes int
	failAt int
}

func (w *failingResponseWriter) Header() http.Header { return w.header }

func (*failingResponseWriter) WriteHeader(int) {}

func (w *failingResponseWriter) Write(data []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, errors.New("write failed")
	}
	return len(data), nil
}

func (w *chunkResponseWriter) Header() http.Header { return w.header }

func (w *chunkResponseWriter) WriteHeader(status int) { w.status = status }

func (w *chunkResponseWriter) Write(data []byte) (int, error) {
	w.chunks = append(w.chunks, append([]byte(nil), data...))
	return len(data), nil
}

func TestDebugResponseWriterStreamsBusinessBodyBeforeTrace(t *testing.T) {
	_, trace := WithDebugTrace(context.Background(), "trace-streaming-envelope")
	target := &chunkResponseWriter{header: make(http.Header)}
	writer := newDebugResponseWriter(target)
	writer.WriteHeader(http.StatusAccepted)
	_, _ = writer.Write([]byte(`{"data":`))
	_, _ = writer.Write([]byte(`{"id":1}}`))
	writer.flush(trace)

	if target.status != http.StatusAccepted || len(target.chunks) < 4 {
		t.Fatalf("response was buffered instead of streamed: status=%d chunks=%d", target.status, len(target.chunks))
	}
	body, snapshot, err := DecodeDebugResponseEnvelope(bytes.Join(target.chunks, nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"data":{"id":1}}` || snapshot.TraceID != "trace-streaming-envelope" {
		t.Fatalf("decoded streaming envelope = %s %+v", body, snapshot)
	}
}

func TestDebugResponseWriterImplicitHeadersAndWriteFailures(t *testing.T) {
	_, trace := WithDebugTrace(context.Background(), "trace-writer-errors")
	implicitTarget := &chunkResponseWriter{header: make(http.Header)}
	implicit := newDebugResponseWriter(implicitTarget)
	implicit.flush(trace)
	implicit.WriteHeader(http.StatusCreated)
	if implicitTarget.status != http.StatusOK {
		t.Fatalf("implicit status = %d", implicitTarget.status)
	}

	prefixFailure := newDebugResponseWriter(&failingResponseWriter{header: make(http.Header), failAt: 1})
	prefixFailure.WriteHeader(http.StatusOK)
	if _, err := prefixFailure.Write([]byte("body")); err == nil {
		t.Fatal("prefix write failure was not retained")
	}
	prefixFailure.flush(trace)

	traceFailure := newDebugResponseWriter(&failingResponseWriter{header: make(http.Header), failAt: 3})
	if _, err := traceFailure.Write([]byte("body")); err != nil {
		t.Fatal(err)
	}
	traceFailure.flush(trace)
}

func TestRouterReturnsDetailedTraceInDebugResponseEnvelope(t *testing.T) {
	router := NewRouter()
	router.SetDevMode(true)
	router.Handle("ping", func(ctx context.Context, req *Request) error {
		databaseTracer := lux.DatabaseTraceSinkFromContext(ctx)
		queryCtx := databaseTracer.StartDatabaseQuery(ctx, lux.DatabaseTraceMeta{Backend: "postgresql", Statement: "SELECT 1", Fingerprint: "abc"})
		databaseTracer.FinishDatabaseQuery(queryCtx, lux.DatabaseTraceResult{})
		req.Buf.AppendInt(7)
		return nil
	})
	req := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(`{"$api":"ping"}`))
	req.Header.Set(DebugTraceRequestHeader, "true")
	req.Header.Set(DebugTraceEnvelopeHeader, "true")
	response := httptest.NewRecorder()
	TraceMiddleware(router).ServeHTTP(response, req)

	if response.Header().Get(DebugTraceResponseHeader) != "" || response.Header().Get(DebugTraceEnvelopeHeader) != "true" {
		t.Fatalf("debug envelope headers = %v", response.Header())
	}
	body, snapshot, err := DecodeDebugResponseEnvelope(response.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"data":7}` {
		t.Fatalf("response body = %s", body)
	}
	var statement string
	for _, span := range snapshot.Spans {
		if span.Name == "database.query" {
			statement = span.Statement
		}
	}
	if statement != "SELECT 1" {
		t.Fatalf("trace snapshot = %+v", snapshot)
	}
}
