package api

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/light-speak/luxo/pkg/lux"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

const (
	// DebugTraceRequestHeader opts a request into detailed development tracing.
	DebugTraceRequestHeader = "X-Luxo-Debug-Trace"
	// DebugTraceResponseHeader carries a base64url-encoded Luxo trace envelope.
	DebugTraceResponseHeader = "X-Luxo-Trace"
	// DebugTraceKeyHeader authorizes detailed tracing outside development mode.
	DebugTraceKeyHeader = "X-Luxo-Debug-Key"
	// DebugTraceEnvelopeHeader requests an out-of-band debug response body so
	// SQL details are never constrained by HTTP response-header limits.
	DebugTraceEnvelopeHeader           = "X-Luxo-Debug-Envelope"
	maxDebugTraceSpans                 = 128
	legacyDebugResponseEnvelopeVersion = 1
	debugResponseEnvelopeVersion       = 2
	debugResponseEnvelopeFooterSize    = 12
	debugResponseEnvelopeMagic         = "LXTR"
	// MaxDebugResponseTraceBytes bounds untrusted detailed trace metadata.
	MaxDebugResponseTraceBytes = 1 << 20
	// MaxDebugResponseEnvelopeOverhead is the maximum non-business response
	// capacity callers need when reading a detailed trace envelope.
	MaxDebugResponseEnvelopeOverhead = MaxDebugResponseTraceBytes + 1 + debugResponseEnvelopeFooterSize
)

type traceKey struct{}
type debugTraceKey struct{}
type debugSpanKey struct{}
type traceFieldPathKey struct{}
type databaseQuerySpanKey struct{}
type databaseAcquireSpanKey struct{}

// TraceDependency identifies how a plan node depends on its siblings.
type TraceDependency string

const (
	TraceDependencySequential TraceDependency = "sequential"
	TraceDependencyParallel   TraceDependency = "parallel"
)

// DebugSpanMeta identifies one measured stage in a request execution plan.
type DebugSpanMeta struct {
	Name               string
	Category           string
	Service            string
	Operation          string
	Field              string
	FieldPath          string
	Selection          string
	Dependency         TraceDependency
	DatabaseBackend    string
	DatabaseName       string
	DatabaseOperation  string
	DatabaseResource   string
	Statement          string
	Fingerprint        string
	ErrorCode          string
	ArgumentCount      int
	RowsAffected       int64
	RowsKnown          bool
	StatementTruncated bool
}

// DebugSpan is one relative timing interval in a debug trace.
type DebugSpan struct {
	DebugSpanMeta
	ID       uint32
	ParentID uint32
	Start    time.Duration
	Duration time.Duration
}

// DebugSpanHandle is an allocation-free token for completing one measured span.
type DebugSpanHandle struct {
	span DebugSpan
}

// ID returns the stable span identifier used by child nodes.
func (h DebugSpanHandle) ID() uint32 {
	return h.span.ID
}

// DebugTraceSnapshot is an immutable copy of one request trace.
type DebugTraceSnapshot struct {
	TraceID   string
	Duration  time.Duration
	Spans     []DebugSpan
	Truncated bool
}

// DebugTraceSession collects opt-in request timings. Normal requests do not
// create a session and therefore do not pay for clocks, locks, or allocations.
type DebugTraceSession struct {
	traceID          string
	started          time.Time
	mu               sync.Mutex
	spans            []DebugSpan
	truncated        bool
	nextSpanID       uint32
	headSampled      bool
	exposeHeaders    bool
	databaseDetails  bool
	responseEnvelope bool
}

// TraceMiddleware generates a unique trace ID for each request and injects
// it into the context. Respects an incoming X-Request-Id header.
func TraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get("X-Request-Id")
		if traceID == "" {
			traceID = uuid.Must(uuid.NewV7()).String()
		}
		ctx := context.WithValue(r.Context(), traceKey{}, traceID)
		w.Header().Set("X-Trace-Id", traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// TraceID returns the trace ID from the context.
// Returns empty string if no trace ID is set.
func TraceID(ctx context.Context) string {
	id, _ := ctx.Value(traceKey{}).(string)
	return id
}

func debugTraceRequested(value string) bool {
	return value == "1" || strings.EqualFold(value, "true")
}

// WithDebugTrace attaches a new detailed trace session to ctx.
func WithDebugTrace(ctx context.Context, traceID string) (context.Context, *DebugTraceSession) {
	return withTraceSession(ctx, traceID, false, true, true)
}

// WithSampledTrace attaches an internal detailed trace selected by head sampling.
// Sampled traces are exported to Studio but never exposed in response headers.
func WithSampledTrace(ctx context.Context, traceID string) (context.Context, *DebugTraceSession) {
	return withTraceSession(ctx, traceID, true, false, false)
}

// WithRemoteTrace attaches a downstream RPC trace and its selected field prefix.
func WithRemoteTrace(ctx context.Context, traceID, fieldPath string, databaseDetails bool) (context.Context, *DebugTraceSession) {
	ctx, trace := withTraceSession(ctx, traceID, false, false, databaseDetails)
	if fieldPath != "" {
		ctx = context.WithValue(ctx, traceFieldPathKey{}, fieldPath)
	}
	return ctx, trace
}

func withTraceSession(ctx context.Context, traceID string, sampled, exposeHeaders, databaseDetails bool) (context.Context, *DebugTraceSession) {
	trace := &DebugTraceSession{
		traceID: traceID, started: time.Now(), spans: make([]DebugSpan, 0, 12),
		headSampled: sampled, exposeHeaders: exposeHeaders, databaseDetails: databaseDetails,
	}
	if TraceID(ctx) == "" {
		ctx = context.WithValue(ctx, traceKey{}, traceID)
	}
	ctx = context.WithValue(ctx, debugTraceKey{}, trace)
	return lux.WithDatabaseTraceSink(ctx, trace), trace
}

// DebugTrace returns the detailed trace session attached to ctx, if enabled.
func DebugTrace(ctx context.Context) *DebugTraceSession {
	trace, _ := ctx.Value(debugTraceKey{}).(*DebugTraceSession)
	return trace
}

// Sampled reports whether this session was created by production head sampling.
func (s *DebugTraceSession) Sampled() bool {
	return s != nil && s.headSampled
}

// DatabaseDetails reports whether this request may retain redacted SQL bodies.
// Ordinary production samples retain fingerprints and timings only.
func (s *DebugTraceSession) DatabaseDetails() bool {
	return s.DatabaseTraceDetails()
}

// DatabaseTraceDetails implements lux.DatabaseTraceSink. It is false for
// production samples so backends can avoid constructing SQL text entirely.
func (s *DebugTraceSession) DatabaseTraceDetails() bool {
	return s != nil && s.databaseDetails
}

// WithTraceFieldPath descends into one selected relation only when tracing is active.
func WithTraceFieldPath(ctx context.Context, field string) context.Context {
	if DebugTrace(ctx) == nil || field == "" {
		return ctx
	}
	return context.WithValue(ctx, traceFieldPathKey{}, joinFieldPath(TraceFieldPath(ctx), field))
}

// TraceFieldPath returns the selected relation path attached to ctx.
func TraceFieldPath(ctx context.Context) string {
	path, _ := ctx.Value(traceFieldPathKey{}).(string)
	return path
}

// JoinTraceFieldPath joins a parent relation path with one field name.
func JoinTraceFieldPath(parent, field string) string {
	return joinFieldPath(parent, field)
}

func joinFieldPath(parent, field string) string {
	if parent == "" {
		return field
	}
	if field == "" {
		return parent
	}
	return parent + "." + field
}

// Start captures a span start only when the session exists.
func (s *DebugTraceSession) Start() time.Time {
	if s == nil {
		return time.Time{}
	}
	return time.Now()
}

// Finish records a completed span. It is safe for concurrent federation calls.
func (s *DebugTraceSession) Finish(started time.Time, meta DebugSpanMeta) {
	if s == nil || started.IsZero() {
		return
	}
	s.record(DebugSpan{DebugSpanMeta: normalizeDebugSpanMeta(meta, ""), Start: started.Sub(s.started), Duration: time.Since(started)})
}

// StartSpan starts a DAG-aware span and returns a context that parents nested work.
func (s *DebugTraceSession) StartSpan(ctx context.Context, meta DebugSpanMeta) (context.Context, DebugSpanHandle) {
	if s == nil {
		return ctx, DebugSpanHandle{}
	}
	parentID, _ := ctx.Value(debugSpanKey{}).(uint32)
	meta = normalizeDebugSpanMeta(meta, TraceFieldPath(ctx))
	handle := DebugSpanHandle{span: DebugSpan{
		DebugSpanMeta: meta, ID: s.reserveSpanID(), ParentID: parentID,
		Start: time.Since(s.started),
	}}
	return context.WithValue(ctx, debugSpanKey{}, handle.span.ID), handle
}

// FinishSpan completes a span created by StartSpan.
func (s *DebugTraceSession) FinishSpan(handle DebugSpanHandle) {
	if s == nil || handle.span.ID == 0 {
		return
	}
	handle.span.Duration = time.Since(s.started) - handle.span.Start
	s.record(handle.span)
}

// StartDatabaseQuery implements lux.DatabaseTraceSink.
func (s *DebugTraceSession) StartDatabaseQuery(ctx context.Context, meta lux.DatabaseTraceMeta) context.Context {
	if s == nil {
		return ctx
	}
	statement := meta.Statement
	if !s.databaseDetails {
		statement = ""
	}
	spanCtx, handle := s.StartSpan(ctx, DebugSpanMeta{
		Name: "database.query", Category: "database",
		DatabaseBackend: meta.Backend, DatabaseName: meta.Database, DatabaseOperation: meta.Operation,
		DatabaseResource: meta.Resource, Statement: statement, Fingerprint: meta.Fingerprint,
		ArgumentCount: meta.ArgumentCount, StatementTruncated: meta.Truncated,
	})
	return context.WithValue(spanCtx, databaseQuerySpanKey{}, handle)
}

// FinishDatabaseQuery implements lux.DatabaseTraceSink.
func (s *DebugTraceSession) FinishDatabaseQuery(ctx context.Context, result lux.DatabaseTraceResult) {
	if s == nil {
		return
	}
	handle, _ := ctx.Value(databaseQuerySpanKey{}).(DebugSpanHandle)
	if handle.span.ID == 0 {
		return
	}
	if result.Command != "" {
		handle.span.DatabaseOperation = result.Command
	}
	handle.span.ErrorCode = result.ErrorCode
	handle.span.RowsAffected = result.RowsAffected
	handle.span.RowsKnown = result.RowsKnown
	s.FinishSpan(handle)
}

// StartDatabaseAcquire implements lux.DatabaseTraceSink.
func (s *DebugTraceSession) StartDatabaseAcquire(ctx context.Context, meta lux.DatabasePoolTraceMeta) context.Context {
	if s == nil {
		return ctx
	}
	spanCtx, handle := s.StartSpan(ctx, DebugSpanMeta{
		Name: "database.acquire", Category: "database",
		DatabaseBackend: meta.Backend, DatabaseName: meta.Database,
	})
	return context.WithValue(spanCtx, databaseAcquireSpanKey{}, handle)
}

// FinishDatabaseAcquire implements lux.DatabaseTraceSink.
func (s *DebugTraceSession) FinishDatabaseAcquire(ctx context.Context, err error) {
	if s == nil {
		return
	}
	handle, _ := ctx.Value(databaseAcquireSpanKey{}).(DebugSpanHandle)
	if handle.span.ID == 0 {
		return
	}
	if err != nil {
		handle.span.ErrorCode = "acquire_failed"
	}
	s.FinishSpan(handle)
}

func normalizeDebugSpanMeta(meta DebugSpanMeta, contextPath string) DebugSpanMeta {
	if meta.FieldPath == "" {
		meta.FieldPath = joinFieldPath(contextPath, meta.Field)
	}
	if meta.Field == "" && meta.FieldPath != "" {
		if index := strings.LastIndexByte(meta.FieldPath, '.'); index >= 0 {
			meta.Field = meta.FieldPath[index+1:]
		} else {
			meta.Field = meta.FieldPath
		}
	}
	return meta
}

func (s *DebugTraceSession) reserveSpanID() uint32 {
	s.mu.Lock()
	s.nextSpanID++
	id := s.nextSpanID
	s.mu.Unlock()
	return id
}

func (s *DebugTraceSession) record(span DebugSpan) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if span.ID == 0 {
		s.nextSpanID++
		span.ID = s.nextSpanID
	}
	if len(s.spans) < maxDebugTraceSpans {
		s.spans = append(s.spans, span)
	} else {
		s.truncated = true
	}
	s.mu.Unlock()
}

// MergeSpan adds downstream spans beneath the local RPC call that produced them.
func (s *DebugTraceSession) MergeSpan(call DebugSpanHandle, remote DebugTraceSnapshot) {
	if s == nil || call.span.ID == 0 || len(remote.Spans) == 0 {
		return
	}
	base := call.span.Start
	s.mu.Lock()
	remapped := make(map[uint32]uint32, len(remote.Spans))
	for _, span := range remote.Spans {
		if len(s.spans) >= maxDebugTraceSpans {
			s.truncated = true
			break
		}
		remoteID := span.ID
		s.nextSpanID++
		span.ID = s.nextSpanID
		remapped[remoteID] = span.ID
		if span.ParentID == 0 {
			span.ParentID = call.span.ID
		} else if parentID, ok := remapped[span.ParentID]; ok {
			span.ParentID = parentID
		} else {
			span.ParentID = call.span.ID
		}
		span.Start += base
		s.spans = append(s.spans, span)
	}
	if remote.Truncated {
		s.truncated = true
	}
	s.mu.Unlock()
}

// Snapshot returns spans ordered by their relative start time.
func (s *DebugTraceSession) Snapshot() DebugTraceSnapshot {
	if s == nil {
		return DebugTraceSnapshot{}
	}
	s.mu.Lock()
	spans := append([]DebugSpan(nil), s.spans...)
	truncated := s.truncated
	s.mu.Unlock()
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].Start == spans[j].Start {
			return spans[i].Duration > spans[j].Duration
		}
		return spans[i].Start < spans[j].Start
	})
	return DebugTraceSnapshot{TraceID: s.traceID, Duration: time.Since(s.started), Spans: spans, Truncated: truncated}
}

// EncodeDebugTrace encodes a trace without reflection or JSON serialization.
func EncodeDebugTrace(snapshot DebugTraceSnapshot) []byte {
	spans := make([][]byte, len(snapshot.Spans))
	for i := range snapshot.Spans {
		spans[i] = encodeDebugSpan(snapshot.Spans[i])
	}
	var enc codec.Encoder
	enc.WriteFieldString(1, snapshot.TraceID)
	enc.WriteFieldInt(2, int64(snapshot.Duration))
	enc.WriteFieldBytesArray(3, spans)
	if snapshot.Truncated {
		enc.WriteFieldBool(4, true)
	}
	enc.WriteEnd()
	return append([]byte{3}, enc.Bytes()...)
}

func encodeDebugSpan(span DebugSpan) []byte {
	var enc codec.Encoder
	enc.WriteFieldString(1, span.Name)
	enc.WriteFieldString(2, span.Category)
	if span.Service != "" {
		enc.WriteFieldString(3, span.Service)
	}
	if span.Operation != "" {
		enc.WriteFieldString(4, span.Operation)
	}
	if span.Field != "" {
		enc.WriteFieldString(5, span.Field)
	}
	enc.WriteFieldInt(6, int64(span.Start))
	enc.WriteFieldInt(7, int64(span.Duration))
	enc.WriteFieldInt(8, int64(span.ID))
	if span.ParentID != 0 {
		enc.WriteFieldInt(9, int64(span.ParentID))
	}
	if span.FieldPath != "" {
		enc.WriteFieldString(10, span.FieldPath)
	}
	if span.Dependency != "" {
		enc.WriteFieldString(11, string(span.Dependency))
	}
	if span.Selection != "" {
		enc.WriteFieldString(12, span.Selection)
	}
	if span.DatabaseBackend != "" {
		enc.WriteFieldString(13, span.DatabaseBackend)
	}
	if span.DatabaseName != "" {
		enc.WriteFieldString(14, span.DatabaseName)
	}
	if span.DatabaseOperation != "" {
		enc.WriteFieldString(15, span.DatabaseOperation)
	}
	if span.DatabaseResource != "" {
		enc.WriteFieldString(16, span.DatabaseResource)
	}
	if span.Statement != "" {
		enc.WriteFieldString(17, span.Statement)
	}
	if span.Fingerprint != "" {
		enc.WriteFieldString(18, span.Fingerprint)
	}
	if span.ArgumentCount != 0 {
		enc.WriteFieldInt(19, int64(span.ArgumentCount))
	}
	if span.RowsKnown {
		enc.WriteFieldInt(20, span.RowsAffected)
	}
	if span.ErrorCode != "" {
		enc.WriteFieldString(21, span.ErrorCode)
	}
	if span.StatementTruncated {
		enc.WriteFieldBool(22, true)
	}
	enc.WriteEnd()
	return append([]byte(nil), enc.Bytes()...)
}

// DecodeDebugTrace decodes the versioned Luxo trace envelope.
func DecodeDebugTrace(data []byte) (DebugTraceSnapshot, error) {
	if len(data) < 2 || (data[0] != 1 && data[0] != 2 && data[0] != 3) {
		return DebugTraceSnapshot{}, fmt.Errorf("invalid debug trace envelope")
	}
	version := data[0]
	dec := codec.NewDecoder(data[1:])
	var snapshot DebugTraceSnapshot
	var spanData [][]byte
	for dec.NextField() {
		switch dec.FieldID() {
		case 1:
			snapshot.TraceID = dec.ReadString()
		case 2:
			snapshot.Duration = time.Duration(dec.ReadInt())
		case 3:
			spanData = dec.ReadBytesArray()
		case 4:
			snapshot.Truncated = dec.ReadBool()
		default:
			return DebugTraceSnapshot{}, fmt.Errorf("unknown debug trace field %d", dec.FieldID())
		}
	}
	if err := finishDebugDecode(dec, len(data)-1); err != nil {
		return DebugTraceSnapshot{}, err
	}
	if snapshot.TraceID == "" || snapshot.Duration < 0 {
		return DebugTraceSnapshot{}, fmt.Errorf("invalid debug trace metadata")
	}
	snapshot.Spans = make([]DebugSpan, len(spanData))
	for i := range spanData {
		span, err := decodeDebugSpan(spanData[i])
		if err != nil {
			return DebugTraceSnapshot{}, err
		}
		snapshot.Spans[i] = span
		if version == 1 {
			snapshot.Spans[i].ID = uint32(i + 1)
			snapshot.Spans[i].FieldPath = snapshot.Spans[i].Field
		}
	}
	return snapshot, nil
}

func decodeDebugSpan(data []byte) (DebugSpan, error) {
	dec := codec.NewDecoder(data)
	var span DebugSpan
	for dec.NextField() {
		switch dec.FieldID() {
		case 1:
			span.Name = dec.ReadString()
		case 2:
			span.Category = dec.ReadString()
		case 3:
			span.Service = dec.ReadString()
		case 4:
			span.Operation = dec.ReadString()
		case 5:
			span.Field = dec.ReadString()
		case 6:
			span.Start = time.Duration(dec.ReadInt())
		case 7:
			span.Duration = time.Duration(dec.ReadInt())
		case 8:
			span.ID = uint32(dec.ReadInt())
		case 9:
			span.ParentID = uint32(dec.ReadInt())
		case 10:
			span.FieldPath = dec.ReadString()
		case 11:
			span.Dependency = TraceDependency(dec.ReadString())
		case 12:
			span.Selection = dec.ReadString()
		case 13, 14, 15, 16, 17, 18, 19, 20, 21, 22:
			decodeDatabaseDebugSpanField(&span, dec)
		default:
			return DebugSpan{}, fmt.Errorf("unknown debug span field %d", dec.FieldID())
		}
	}
	if err := finishDebugDecode(dec, len(data)); err != nil {
		return DebugSpan{}, err
	}
	if span.Name == "" || span.Category == "" || span.Start < 0 || span.Duration < 0 {
		return DebugSpan{}, fmt.Errorf("invalid debug span metadata")
	}
	if span.FieldPath == "" {
		span.FieldPath = span.Field
	}
	return span, nil
}

func decodeDatabaseDebugSpanField(span *DebugSpan, dec *codec.Decoder) {
	switch dec.FieldID() {
	case 13:
		span.DatabaseBackend = dec.ReadString()
	case 14:
		span.DatabaseName = dec.ReadString()
	case 15:
		span.DatabaseOperation = dec.ReadString()
	case 16:
		span.DatabaseResource = dec.ReadString()
	case 17:
		span.Statement = dec.ReadString()
	case 18:
		span.Fingerprint = dec.ReadString()
	case 19:
		span.ArgumentCount = int(dec.ReadInt())
	case 20:
		span.RowsAffected = dec.ReadInt()
		span.RowsKnown = true
	case 21:
		span.ErrorCode = dec.ReadString()
	case 22:
		span.StatementTruncated = dec.ReadBool()
	}
}

func finishDebugDecode(dec *codec.Decoder, size int) error {
	if err := dec.Err(); err != nil {
		return fmt.Errorf("decode debug trace: %w", err)
	}
	if dec.FieldID() != 0 || dec.Offset() != size {
		return fmt.Errorf("invalid debug trace terminator")
	}
	return nil
}

// EncodeDebugTraceHeader returns a base64url value safe for HTTP headers.
func EncodeDebugTraceHeader(snapshot DebugTraceSnapshot) string {
	return base64.RawURLEncoding.EncodeToString(EncodeDebugTrace(snapshot))
}

// DecodeDebugTraceHeader decodes a trace response header.
func DecodeDebugTraceHeader(value string) (DebugTraceSnapshot, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return DebugTraceSnapshot{}, fmt.Errorf("decode debug trace header: %w", err)
	}
	return DecodeDebugTrace(data)
}

// EncodeDebugResponseEnvelope carries an unmodified response body and its
// binary trace in the response body for Studio's explicitly traced requests.
// Version 2 puts a fixed-size footer after the trace so servers can stream the
// business body without buffering or copying it first.
func EncodeDebugResponseEnvelope(body []byte, snapshot DebugTraceSnapshot) []byte {
	trace := EncodeDebugTrace(snapshot)
	result := make([]byte, 1, 1+len(body)+len(trace)+debugResponseEnvelopeFooterSize)
	result[0] = debugResponseEnvelopeVersion
	result = append(result, body...)
	result = append(result, trace...)
	return appendDebugResponseFooter(result, len(trace))
}

// DecodeDebugResponseEnvelope restores a response body and detailed trace.
func DecodeDebugResponseEnvelope(data []byte) ([]byte, DebugTraceSnapshot, error) {
	if len(data) < 3 {
		return nil, DebugTraceSnapshot{}, fmt.Errorf("invalid debug response envelope")
	}
	if data[0] == legacyDebugResponseEnvelopeVersion {
		return decodeLegacyDebugResponseEnvelope(data)
	}
	if data[0] != debugResponseEnvelopeVersion || len(data) < 1+debugResponseEnvelopeFooterSize {
		return nil, DebugTraceSnapshot{}, fmt.Errorf("invalid debug response envelope")
	}
	footerStart := len(data) - debugResponseEnvelopeFooterSize
	if string(data[footerStart+8:]) != debugResponseEnvelopeMagic {
		return nil, DebugTraceSnapshot{}, fmt.Errorf("invalid debug response envelope footer")
	}
	traceLength := binary.BigEndian.Uint64(data[footerStart : footerStart+8])
	if traceLength > MaxDebugResponseTraceBytes {
		return nil, DebugTraceSnapshot{}, fmt.Errorf("debug response trace exceeds %d bytes", MaxDebugResponseTraceBytes)
	}
	if traceLength == 0 || traceLength > uint64(footerStart-1) {
		return nil, DebugTraceSnapshot{}, fmt.Errorf("debug response trace length exceeds envelope")
	}
	traceStart := footerStart - int(traceLength)
	snapshot, err := DecodeDebugTrace(data[traceStart:footerStart])
	if err != nil {
		return nil, DebugTraceSnapshot{}, err
	}
	return data[1:traceStart], snapshot, nil
}

func decodeLegacyDebugResponseEnvelope(data []byte) ([]byte, DebugTraceSnapshot, error) {
	bodyLength, consumed := codec.ReadVarint(data, 1)
	if consumed <= 0 {
		return nil, DebugTraceSnapshot{}, fmt.Errorf("invalid debug response body length")
	}
	start := 1 + consumed
	if bodyLength > uint64(len(data)-start) {
		return nil, DebugTraceSnapshot{}, fmt.Errorf("debug response body length exceeds envelope")
	}
	end := start + int(bodyLength)
	if end == len(data) {
		return nil, DebugTraceSnapshot{}, fmt.Errorf("debug response envelope has no trace")
	}
	if len(data)-end > MaxDebugResponseTraceBytes {
		return nil, DebugTraceSnapshot{}, fmt.Errorf("debug response trace exceeds %d bytes", MaxDebugResponseTraceBytes)
	}
	snapshot, err := DecodeDebugTrace(data[end:])
	if err != nil {
		return nil, DebugTraceSnapshot{}, err
	}
	return data[start:end], snapshot, nil
}

func appendDebugResponseFooter(data []byte, traceLength int) []byte {
	var footer [debugResponseEnvelopeFooterSize]byte
	binary.BigEndian.PutUint64(footer[:8], uint64(traceLength))
	copy(footer[8:], debugResponseEnvelopeMagic)
	return append(data, footer[:]...)
}

func writeDebugTraceHeaders(header http.Header, trace *DebugTraceSession) {
	if trace == nil || !trace.exposeHeaders {
		return
	}
	snapshot := trace.Snapshot()
	if !trace.responseEnvelope {
		header.Set(DebugTraceResponseHeader, EncodeDebugTraceHeader(snapshot))
	}
	header.Set("Server-Timing", serverTimingValue(snapshot.Spans))
}

type debugResponseWriter struct {
	target      http.ResponseWriter
	wroteHeader bool
	writeErr    error
}

func newDebugResponseWriter(target http.ResponseWriter) *debugResponseWriter {
	return &debugResponseWriter{target: target}
}

func (w *debugResponseWriter) Header() http.Header {
	return w.target.Header()
}

func (w *debugResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.prepareHeader()
	w.target.WriteHeader(statusCode)
	w.wroteHeader = true
	_, w.writeErr = w.target.Write([]byte{debugResponseEnvelopeVersion})
}

func (w *debugResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return w.target.Write(data)
}

func (w *debugResponseWriter) flush(trace *DebugTraceSession) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.writeErr != nil {
		return
	}
	encodedTrace := EncodeDebugTrace(trace.Snapshot())
	if _, err := w.target.Write(encodedTrace); err != nil {
		return
	}
	_, _ = w.target.Write(appendDebugResponseFooter(nil, len(encodedTrace)))
}

func (w *debugResponseWriter) prepareHeader() {
	header := w.target.Header()
	header.Del("Content-Length")
	header.Del(DebugTraceResponseHeader)
	header.Set(DebugTraceEnvelopeHeader, "true")
	header.Set("Content-Type", "application/x-luxo-debug")
}

func serverTimingValue(spans []DebugSpan) string {
	var b strings.Builder
	for i := range spans {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("luxo_")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(";dur=")
		b.WriteString(strconv.FormatFloat(float64(spans[i].Duration)/float64(time.Millisecond), 'f', 3, 64))
		b.WriteString(";desc=\"")
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(spans[i].Name, "\\", "\\\\"), "\"", "\\\""))
		b.WriteByte('"')
	}
	return b.String()
}

// DebugTraceJSON formats spans for Studio persistence without reflection.
func DebugTraceJSON(snapshot DebugTraceSnapshot) string {
	buf := make([]byte, 0, len(snapshot.Spans)*128)
	buf = append(buf, '[')
	for i := range snapshot.Spans {
		if i > 0 {
			buf = append(buf, ',')
		}
		span := &snapshot.Spans[i]
		buf = append(buf, '{')
		buf = append(buf, `"id":`...)
		buf = strconv.AppendUint(buf, uint64(span.ID), 10)
		buf = append(buf, `,"parentId":`...)
		buf = strconv.AppendUint(buf, uint64(span.ParentID), 10)
		buf = appendDebugJSONString(buf, "name", span.Name, true)
		buf = appendDebugJSONString(buf, "category", span.Category, true)
		buf = appendDebugJSONString(buf, "service", span.Service, true)
		buf = appendDebugJSONString(buf, "operation", span.Operation, true)
		buf = appendDebugJSONString(buf, "field", span.Field, true)
		buf = appendDebugJSONString(buf, "fieldPath", span.FieldPath, true)
		buf = appendDebugJSONString(buf, "selection", span.Selection, true)
		buf = appendDebugJSONString(buf, "dependency", string(span.Dependency), true)
		if span.DatabaseBackend != "" {
			buf = appendDebugJSONString(buf, "databaseBackend", span.DatabaseBackend, true)
		}
		if span.DatabaseName != "" {
			buf = appendDebugJSONString(buf, "databaseName", span.DatabaseName, true)
		}
		if span.DatabaseOperation != "" {
			buf = appendDebugJSONString(buf, "databaseOperation", span.DatabaseOperation, true)
		}
		if span.DatabaseResource != "" {
			buf = appendDebugJSONString(buf, "databaseResource", span.DatabaseResource, true)
		}
		if span.Statement != "" {
			buf = appendDebugJSONString(buf, "statement", span.Statement, true)
		}
		if span.Fingerprint != "" {
			buf = appendDebugJSONString(buf, "fingerprint", span.Fingerprint, true)
		}
		if span.ArgumentCount != 0 {
			buf = append(buf, `,"argumentCount":`...)
			buf = strconv.AppendInt(buf, int64(span.ArgumentCount), 10)
		}
		if span.RowsKnown {
			buf = append(buf, `,"rowsAffected":`...)
			buf = strconv.AppendInt(buf, span.RowsAffected, 10)
		}
		if span.ErrorCode != "" {
			buf = appendDebugJSONString(buf, "errorCode", span.ErrorCode, true)
		}
		if span.StatementTruncated {
			buf = append(buf, `,"statementTruncated":true`...)
		}
		buf = append(buf, `,"start":`...)
		buf = strconv.AppendFloat(buf, float64(span.Start)/float64(time.Millisecond), 'f', -1, 64)
		buf = append(buf, `,"duration":`...)
		buf = strconv.AppendFloat(buf, float64(span.Duration)/float64(time.Millisecond), 'f', -1, 64)
		buf = append(buf, '}')
	}
	buf = append(buf, ']')
	return string(buf)
}

func appendDebugJSONString(buf []byte, name, value string, comma bool) []byte {
	if comma {
		buf = append(buf, ',')
	}
	buf = strconv.AppendQuote(buf, name)
	buf = append(buf, ':')
	return strconv.AppendQuote(buf, value)
}
