package pg

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/light-speak/luxo/pkg/lux"
	"github.com/light-speak/luxo/pkg/lux/api"
)

type capturingDatabaseSink struct {
	details bool
	meta    lux.DatabaseTraceMeta
}

func (s *capturingDatabaseSink) DatabaseTraceDetails() bool {
	return s.details
}

func (s *capturingDatabaseSink) StartDatabaseQuery(ctx context.Context, meta lux.DatabaseTraceMeta) context.Context {
	s.meta = meta
	return ctx
}

func (*capturingDatabaseSink) FinishDatabaseQuery(context.Context, lux.DatabaseTraceResult) {}

func (*capturingDatabaseSink) StartDatabaseAcquire(ctx context.Context, _ lux.DatabasePoolTraceMeta) context.Context {
	return ctx
}

func (*capturingDatabaseSink) FinishDatabaseAcquire(context.Context, error) {}

func TestSQLTraceSanitizesLiteralsAndRecordsMetadata(t *testing.T) {
	ctx, trace := api.WithDebugTrace(context.Background(), "trace-pg")
	tracer := &pgxTracer{database: "taskflow"}
	queryCtx := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{
		SQL:  "SELECT id, title FROM tasks WHERE project_id = $1 AND token = 'secret-value' -- hidden\n LIMIT 10",
		Args: []any{int64(7)},
	})
	tracer.TraceQueryEnd(queryCtx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 2")})

	spans := trace.Snapshot().Spans
	if len(spans) != 1 {
		t.Fatalf("spans = %+v", spans)
	}
	span := spans[0]
	if span.Name != "database.query" || span.DatabaseBackend != "postgresql" || span.DatabaseName != "taskflow" || span.DatabaseOperation != "SELECT" || span.DatabaseResource != "tasks" {
		t.Fatalf("database metadata = %+v", span)
	}
	if span.Statement != "SELECT id, title FROM tasks WHERE project_id = $1 AND token = '?' LIMIT ?" {
		t.Fatalf("sanitized SQL = %q", span.Statement)
	}
	if span.Fingerprint == "" || span.ArgumentCount != 1 || !span.RowsKnown || span.RowsAffected != 2 {
		t.Fatalf("database result = %+v", span)
	}
}

func TestSQLTraceRecordsPoolWaitAndPostgresErrorCode(t *testing.T) {
	ctx, trace := api.WithDebugTrace(context.Background(), "trace-pool")
	tracer := &pgxTracer{database: "taskflow"}
	acquireCtx := tracer.TraceAcquireStart(ctx, nil, pgxpool.TraceAcquireStartData{})
	tracer.TraceAcquireEnd(acquireCtx, nil, pgxpool.TraceAcquireEndData{Err: errors.New("closed")})
	queryCtx := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: "UPDATE tasks SET title = $1 WHERE id = $2", Args: []any{"new", 1}})
	tracer.TraceQueryEnd(queryCtx, nil, pgx.TraceQueryEndData{Err: &pgconn.PgError{Code: "23505"}})

	spans := trace.Snapshot().Spans
	if len(spans) != 2 || spans[0].Name != "database.acquire" || spans[0].ErrorCode != "acquire_failed" {
		t.Fatalf("pool span = %+v", spans)
	}
	if spans[1].Name != "database.query" || spans[1].ErrorCode != "23505" || spans[1].DatabaseOperation != "UPDATE" {
		t.Fatalf("query error span = %+v", spans[1])
	}
}

func TestSQLTraceNoopAndGenericErrorPaths(t *testing.T) {
	tracer := &pgxTracer{database: "taskflow", debugSQL: true}
	tracer.TraceQueryEnd(context.Background(), nil, pgx.TraceQueryEndData{})
	if got := postgresErrorCode(errors.New("connection closed")); got != "database_error" {
		t.Fatalf("generic error code = %q", got)
	}
	if got := postgresErrorCode(nil); got != "" {
		t.Fatalf("nil error code = %q", got)
	}
}

func TestDebugSQLLoggingIsTimedAndRedacted(t *testing.T) {
	var output bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&output)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	}()

	tracer := &pgxTracer{database: "taskflow", debugSQL: true}
	queryCtx := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{
		SQL: "SELECT id FROM tasks WHERE token = 'secret-value' AND id = $1", Args: []any{int64(7)},
	})
	tracer.TraceQueryEnd(queryCtx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 1")})
	if text := output.String(); strings.Contains(text, "secret-value") || !strings.Contains(text, "token = '?'") || !strings.Contains(text, "args=1 redacted") {
		t.Fatalf("success SQL log = %q", text)
	}

	output.Reset()
	queryCtx = tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "DELETE FROM tasks WHERE id = 42"})
	tracer.TraceQueryEnd(queryCtx, nil, pgx.TraceQueryEndData{Err: &pgconn.PgError{Code: "23503"}})
	if text := output.String(); !strings.Contains(text, "ERROR 23503") || !strings.Contains(text, "id = ?") || strings.Contains(text, "id = 42") {
		t.Fatalf("error SQL log = %q", text)
	}
}

func TestInactiveSQLTracePathAllocatesNothing(t *testing.T) {
	tracer := &pgxTracer{database: "taskflow"}
	ctx := context.Background()
	start := pgx.TraceQueryStartData{SQL: "SELECT id FROM tasks WHERE id = $1", Args: []any{int64(1)}}
	end := pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 1")}
	allocations := testing.AllocsPerRun(1000, func() {
		queryCtx := tracer.TraceQueryStart(ctx, nil, start)
		tracer.TraceQueryEnd(queryCtx, nil, end)
	})
	if allocations != 0 {
		t.Fatalf("inactive SQL tracing allocations = %v, want 0", allocations)
	}
}

func TestSampledSQLTraceDoesNotBuildStatement(t *testing.T) {
	sink := &capturingDatabaseSink{}
	ctx := lux.WithDatabaseTraceSink(context.Background(), sink)
	tracer := &pgxTracer{database: "taskflow"}
	tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{
		SQL:  "SELECT id FROM tasks WHERE token = 'secret-value' AND id = $1",
		Args: []any{int64(1)},
	})

	if sink.meta.Statement != "" || sink.meta.Truncated {
		t.Fatalf("sampled metadata retained statement: %+v", sink.meta)
	}
	if sink.meta.Fingerprint == "" || sink.meta.Operation != "SELECT" || sink.meta.Resource != "tasks" {
		t.Fatalf("sampled metadata = %+v", sink.meta)
	}
}

func TestSampledSQLTraceKeepsAllocationsBounded(t *testing.T) {
	sink := &capturingDatabaseSink{}
	ctx := lux.WithDatabaseTraceSink(context.Background(), sink)
	tracer := &pgxTracer{database: "taskflow"}
	start := pgx.TraceQueryStartData{SQL: "SELECT id FROM tasks WHERE id = $1", Args: []any{int64(1)}}
	allocations := testing.AllocsPerRun(1000, func() {
		tracer.TraceQueryStart(ctx, nil, start)
	})
	if allocations > 2 {
		t.Fatalf("sampled SQL tracing allocations = %v, want <= 2", allocations)
	}
}

func TestTraceSQLSanitizerHandlesPostgresLiteralsAndBoundsOutput(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{name: "escape and scientific", query: `/* secret comment */ INSERT INTO "tasks" (title, score) VALUES (E'secret', 1.5e+3)`, want: `INSERT INTO "tasks" (title, score) VALUES (E'?', ?)`},
		{name: "backslash escaped literal", query: `SELECT E'secret\'value', id FROM tasks`, want: `SELECT E'?', id FROM tasks`},
		{name: "standard conforming backslash", query: `SELECT 'path\', id FROM tasks`, want: `SELECT '?', id FROM tasks`},
		{name: "unicode escaped literal", query: `SELECT U&'d\0061t\+000061', id FROM tasks`, want: `SELECT U&'?', id FROM tasks`},
		{name: "doubled quote", query: `SELECT 'it''s private', id FROM tasks`, want: `SELECT '?', id FROM tasks`},
		{name: "dollar quoted", query: `SELECT $$secret$$, $tag$other-secret$tag$ FROM tasks`, want: `SELECT '?', '?' FROM tasks`},
		{name: "line comment", query: "SELECT id -- secret\n FROM tasks", want: "SELECT id FROM tasks"},
		{name: "nested block comment", query: "SELECT /* outer /* nested-secret */ outer-secret */ id FROM tasks", want: "SELECT id FROM tasks"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, truncated := sanitizeTraceSQL(test.query)
			if truncated || got != test.want {
				t.Fatalf("sanitizeTraceSQL() = %q, %v, want %q, false", got, truncated, test.want)
			}
		})
	}

	got, truncated := sanitizeTraceSQL("SELECT " + strings.Repeat("field_name,", maxTraceStatementBytes))
	if !truncated || len(got) > maxTraceStatementBytes {
		t.Fatalf("bounded SQL length = %d, truncated = %v", len(got), truncated)
	}
}

func TestSQLTokenizerHandlesMalformedAndUncommonInput(t *testing.T) {
	for _, test := range []struct {
		query     string
		operation string
		resource  string
	}{
		{query: "", operation: "", resource: ""},
		{query: "VACUUM tasks", operation: "VACUUM", resource: ""},
		{query: "SELECT 1", operation: "SELECT", resource: ""},
		{query: "SELECT id FROM tasks JOIN users ON true", operation: "SELECT", resource: "tasks"},
	} {
		operation, resource := sqlStatementMetadata(test.query)
		if operation != test.operation || resource != test.resource {
			t.Fatalf("sqlStatementMetadata(%q) = %q, %q", test.query, operation, resource)
		}
	}
	if word, _ := nextSQLToken("SEL/* comment */ECT", 0); word != "SEL" {
		t.Fatalf("token before inline comment = %q", word)
	}
	if word, _ := nextSQLToken("/ not-a-comment", 0); word != "/" {
		t.Fatalf("slash token = %q", word)
	}
}

func TestSQLLiteralScannerRejectsMalformedDelimitersAndTerminatesSafely(t *testing.T) {
	for _, query := range []string{"$tag", "$bad-tag$value$bad-tag$"} {
		if next, ok := skipSQLLiteral(query, 0); ok || next != 0 {
			t.Fatalf("invalid dollar literal %q = %d, %v", query, next, ok)
		}
	}
	for _, query := range []string{"$tag$unterminated", "'unterminated", "/* unterminated", "-- trailing comment"} {
		var next int
		var ok bool
		if query[0] == '$' || query[0] == '\'' {
			next, ok = skipSQLLiteral(query, 0)
		} else {
			next, ok = skipSQLComment(query, 0)
		}
		if !ok || next != len(query) {
			t.Fatalf("unterminated token %q = %d, %v", query, next, ok)
		}
	}
	if next, ok := skipSQLComment("/", 0); ok || next != 0 {
		t.Fatalf("single slash comment = %d, %v", next, ok)
	}
}

func TestSQLLiteralPrefixAndNumberBoundaries(t *testing.T) {
	if !sqlLiteralBackslashEscapes("E'x'", 1) || !sqlLiteralBackslashEscapes("U&'x'", 2) {
		t.Fatal("PostgreSQL escape literal prefix was not recognized")
	}
	if sqlLiteralBackslashEscapes("nameE'x'", 5) || sqlLiteralBackslashEscapes("nameU&'x'", 6) || sqlLiteralBackslashEscapes("'x'", 0) {
		t.Fatal("identifier suffix was mistaken for an escape literal prefix")
	}
	if !isSQLNumberStart("42", 0) || isSQLNumberStart("x42", 1) || isSQLNumberStart("$42", 1) {
		t.Fatal("numeric boundary detection is incorrect")
	}
	first := sqlFingerprint("SELECT $123, 42")
	second := sqlFingerprint("SELECT $9, 9001")
	if first != second {
		t.Fatalf("multi-digit placeholders were not normalized: %s != %s", first, second)
	}
}

func TestSQLFingerprintNormalizesSensitiveValuesWithBoundedAllocation(t *testing.T) {
	first := `SELECT id FROM tasks /* first secret */ WHERE token = E'secret\'value' AND score = 42 AND owner_id = $1`
	second := `SELECT id FROM tasks /* second secret */ WHERE token = E'other\'value' AND score = 9001 AND owner_id = $9`
	if sqlFingerprint(first) != sqlFingerprint(second) {
		t.Fatalf("equivalent SQL shapes have different fingerprints: %q != %q", sqlFingerprint(first), sqlFingerprint(second))
	}
	if sqlFingerprint(first) == sqlFingerprint(`SELECT title FROM tasks WHERE owner_id = $1`) {
		t.Fatal("different SQL shapes have the same fingerprint")
	}
	allocations := testing.AllocsPerRun(1000, func() {
		_ = sqlFingerprint(first)
	})
	if allocations > 1 {
		t.Fatalf("SQL fingerprint allocations = %v, want <= 1", allocations)
	}
}

func TestSQLStatementMetadataHandlesCommentsAndQuotedResources(t *testing.T) {
	for _, test := range []struct {
		query     string
		operation string
		resource  string
	}{
		{query: "-- comment\nselect id from \"task_items\"", operation: "SELECT", resource: "task_items"},
		{query: "UPDATE tasks SET title = $1", operation: "UPDATE", resource: "tasks"},
		{query: "INSERT INTO activities (id) VALUES ($1)", operation: "INSERT", resource: "activities"},
	} {
		operation, resource := sqlStatementMetadata(test.query)
		if operation != test.operation || resource != test.resource {
			t.Fatalf("sqlStatementMetadata(%q) = %q, %q", test.query, operation, resource)
		}
	}
}

func BenchmarkInactiveSQLTrace(b *testing.B) {
	tracer := &pgxTracer{database: "taskflow"}
	ctx := context.Background()
	start := pgx.TraceQueryStartData{SQL: "SELECT id FROM tasks WHERE id = $1", Args: []any{int64(1)}}
	end := pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 1")}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		queryCtx := tracer.TraceQueryStart(ctx, nil, start)
		tracer.TraceQueryEnd(queryCtx, nil, end)
	}
}

func BenchmarkSampledSQLTrace(b *testing.B) {
	sink := &capturingDatabaseSink{}
	ctx := lux.WithDatabaseTraceSink(context.Background(), sink)
	tracer := &pgxTracer{database: "taskflow"}
	start := pgx.TraceQueryStartData{SQL: "SELECT id FROM tasks WHERE id = $1", Args: []any{int64(1)}}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		tracer.TraceQueryStart(ctx, nil, start)
	}
}
