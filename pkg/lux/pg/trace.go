package pg

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/light-speak/luxo/pkg/lux"
)

const maxTraceStatementBytes = 4 << 10

type pgxTracer struct {
	database string
	debugSQL bool
}

type debugSQLTraceKey struct{}

type debugSQLTrace struct {
	started       time.Time
	statement     string
	argumentCount int
}

func (t *pgxTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	sink := lux.DatabaseTraceSinkFromContext(ctx)
	if sink == nil && !t.debugSQL {
		return ctx
	}
	statement := ""
	truncated := false
	metadataStatement := data.SQL
	if t.debugSQL || sink != nil && sink.DatabaseTraceDetails() {
		statement, truncated = sanitizeTraceSQL(data.SQL)
		metadataStatement = statement
	}
	if sink != nil {
		operation, resource := sqlStatementMetadata(metadataStatement)
		traceStatement := ""
		traceTruncated := false
		if sink.DatabaseTraceDetails() {
			traceStatement = statement
			traceTruncated = truncated
		}
		ctx = sink.StartDatabaseQuery(ctx, lux.DatabaseTraceMeta{
			Backend: "postgresql", Database: t.database, Operation: operation,
			Resource: resource, Statement: traceStatement, Fingerprint: sqlFingerprint(metadataStatement),
			ArgumentCount: len(data.Args), Truncated: traceTruncated,
		})
	}
	if !t.debugSQL {
		return ctx
	}
	return context.WithValue(ctx, debugSQLTraceKey{}, debugSQLTrace{
		started: time.Now(), statement: statement, argumentCount: len(data.Args),
	})
}

func (t *pgxTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if sink := lux.DatabaseTraceSinkFromContext(ctx); sink != nil {
		command := sqlOperation(data.CommandTag.String())
		sink.FinishDatabaseQuery(ctx, lux.DatabaseTraceResult{
			Command: command, ErrorCode: postgresErrorCode(data.Err), RowsAffected: data.CommandTag.RowsAffected(),
			RowsKnown: data.Err == nil && command != "",
		})
	}
	if !t.debugSQL {
		return
	}
	trace, ok := ctx.Value(debugSQLTraceKey{}).(debugSQLTrace)
	if !ok {
		return
	}
	logDebugSQL(trace, data)
}

func (t *pgxTracer) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	sink := lux.DatabaseTraceSinkFromContext(ctx)
	if sink == nil {
		return ctx
	}
	return sink.StartDatabaseAcquire(ctx, lux.DatabasePoolTraceMeta{Backend: "postgresql", Database: t.database})
}

func (t *pgxTracer) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	if sink := lux.DatabaseTraceSinkFromContext(ctx); sink != nil {
		sink.FinishDatabaseAcquire(ctx, data.Err)
	}
}

func logDebugSQL(trace debugSQLTrace, data pgx.TraceQueryEndData) {
	duration := time.Since(trace.started)
	if data.Err != nil {
		log.Printf("[SQL] %s | ERROR %s | %s | args=%d redacted", duration, postgresErrorCode(data.Err), trace.statement, trace.argumentCount)
		return
	}
	log.Printf("[SQL] %s | %s | %s | args=%d redacted", duration, data.CommandTag.String(), trace.statement, trace.argumentCount)
}

func postgresErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return "database_error"
}

func sqlOperation(statement string) string {
	word, _ := nextSQLToken(statement, 0)
	if word == "" {
		return ""
	}
	return canonicalSQLOperation(word)
}

func sqlStatementMetadata(statement string) (string, string) {
	index := 0
	tokenIndex := 0
	operation := ""
	for {
		word, next := nextSQLToken(statement, index)
		if word == "" {
			return operation, ""
		}
		index = next
		if tokenIndex == 0 {
			operation = canonicalSQLOperation(word)
		}
		resourceKeyword := isSQLResourceKeyword(word)
		if !resourceKeyword || sqlWordEqual(word, "UPDATE") && tokenIndex != 0 {
			tokenIndex++
			continue
		}
		resource, _ := nextSQLToken(statement, index)
		return operation, strings.Trim(resource, `"[]`)
	}
}

func nextSQLToken(statement string, index int) (string, int) {
	for index < len(statement) {
		if statement[index] == '-' || statement[index] == '/' {
			if next, ok := skipSQLComment(statement, index); ok {
				index = next
				continue
			}
		}
		if isSQLTokenSeparator(statement[index]) {
			index++
			continue
		}
		break
	}
	if index >= len(statement) {
		return "", index
	}
	start := index
	if statement[index] == '"' {
		end := skipQuotedSQL(statement, index+1, '"', false)
		return statement[start:end], end
	}
	for index < len(statement) && !isSQLTokenSeparator(statement[index]) {
		if statement[index] == '-' || statement[index] == '/' {
			if _, ok := skipSQLComment(statement, index); ok {
				break
			}
		}
		index++
	}
	return statement[start:index], index
}

func canonicalSQLOperation(word string) string {
	for _, operation := range [...]string{
		"SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "ALTER", "DROP",
		"TRUNCATE", "COPY", "BEGIN", "COMMIT", "ROLLBACK", "WITH",
	} {
		if sqlWordEqual(word, operation) {
			return operation
		}
	}
	return strings.ToUpper(strings.Trim(word, "();"))
}

func isSQLResourceKeyword(word string) bool {
	return sqlWordEqual(word, "FROM") || sqlWordEqual(word, "INTO") ||
		sqlWordEqual(word, "UPDATE") || sqlWordEqual(word, "JOIN") || sqlWordEqual(word, "TABLE")
}

func sqlWordEqual(word, canonical string) bool {
	return word == canonical || strings.EqualFold(word, canonical)
}

func isSQLTokenSeparator(char byte) bool {
	return isSQLSpace(char) || char == '(' || char == ')' || char == ';' || char == ','
}

func sqlFingerprint(statement string) string {
	const offset64 = uint64(14695981039346656037)
	hash := offset64
	spacePending := false
	written := false
	for index := 0; index < len(statement); {
		if isSQLSpace(statement[index]) {
			spacePending = written
			index++
			continue
		}
		if next, ok := skipSQLComment(statement, index); ok {
			spacePending = written
			index = next
			continue
		}
		if next, ok := skipSQLLiteral(statement, index); ok {
			hash = appendFingerprintToken(hash, "'?'", spacePending)
			spacePending = false
			written = true
			index = next
			continue
		}
		if statement[index] == '$' && index+1 < len(statement) && isSQLDigit(statement[index+1]) {
			hash = appendFingerprintToken(hash, "$?", spacePending)
			spacePending = false
			written = true
			index += 2
			for index < len(statement) && isSQLDigit(statement[index]) {
				index++
			}
			continue
		}
		if isSQLNumberStart(statement, index) {
			hash = appendFingerprintToken(hash, "?", spacePending)
			spacePending = false
			written = true
			index = skipSQLNumber(statement, index)
			continue
		}
		if spacePending {
			hash = appendFingerprintByte(hash, ' ')
			spacePending = false
		}
		hash = appendFingerprintByte(hash, statement[index])
		written = true
		index++
	}
	const digits = "0123456789abcdef"
	var encoded [16]byte
	for index := len(encoded) - 1; index >= 0; index-- {
		encoded[index] = digits[hash&15]
		hash >>= 4
	}
	return string(encoded[:])
}

func appendFingerprintToken(hash uint64, token string, space bool) uint64 {
	if space {
		hash = appendFingerprintByte(hash, ' ')
	}
	for index := 0; index < len(token); index++ {
		hash = appendFingerprintByte(hash, token[index])
	}
	return hash
}

func appendFingerprintByte(hash uint64, value byte) uint64 {
	const prime64 = uint64(1099511628211)
	return (hash ^ uint64(value)) * prime64
}

func sanitizeTraceSQL(statement string) (string, bool) {
	var builder strings.Builder
	capacity := len(statement)
	if capacity > maxTraceStatementBytes {
		capacity = maxTraceStatementBytes
	}
	builder.Grow(capacity)
	truncated := false
	spacePending := false
	for index := 0; index < len(statement); {
		if isSQLSpace(statement[index]) {
			spacePending = builder.Len() > 0
			index++
			continue
		}
		if next, ok := skipSQLComment(statement, index); ok {
			spacePending = builder.Len() > 0
			index = next
			continue
		}
		if next, ok := skipSQLLiteral(statement, index); ok {
			appendTraceToken(&builder, "'?'", spacePending, &truncated)
			spacePending = false
			index = next
			continue
		}
		if isSQLNumberStart(statement, index) {
			appendTraceToken(&builder, "?", spacePending, &truncated)
			spacePending = false
			index = skipSQLNumber(statement, index)
			continue
		}
		appendTraceToken(&builder, statement[index:index+1], spacePending, &truncated)
		spacePending = false
		index++
	}
	return strings.TrimSpace(builder.String()), truncated
}

func appendTraceToken(builder *strings.Builder, token string, space bool, truncated *bool) {
	if *truncated {
		return
	}
	required := len(token)
	if space {
		required++
	}
	if builder.Len()+required > maxTraceStatementBytes {
		*truncated = true
		return
	}
	if space {
		builder.WriteByte(' ')
	}
	builder.WriteString(token)
}

func skipSQLComment(statement string, index int) (int, bool) {
	if index+1 >= len(statement) {
		return index, false
	}
	if statement[index:index+2] == "--" {
		if end := strings.IndexByte(statement[index+2:], '\n'); end >= 0 {
			return index + end + 3, true
		}
		return len(statement), true
	}
	if statement[index:index+2] != "/*" {
		return index, false
	}
	depth := 1
	for cursor := index + 2; cursor+1 < len(statement); cursor += 2 {
		switch statement[cursor : cursor+2] {
		case "/*":
			depth++
		case "*/":
			depth--
			if depth == 0 {
				return cursor + 2, true
			}
		default:
			cursor--
		}
	}
	return len(statement), true
}

func skipSQLLiteral(statement string, index int) (int, bool) {
	if statement[index] == '\'' {
		return skipQuotedSQL(statement, index+1, '\'', sqlLiteralBackslashEscapes(statement, index)), true
	}
	if statement[index] != '$' || index+1 >= len(statement) || statement[index+1] >= '0' && statement[index+1] <= '9' {
		return index, false
	}
	delimiterEnd := strings.IndexByte(statement[index+1:], '$')
	if delimiterEnd < 0 {
		return index, false
	}
	delimiterEnd += index + 1
	for cursor := index + 1; cursor < delimiterEnd; cursor++ {
		if !isSQLIdentifier(statement[cursor]) {
			return index, false
		}
	}
	delimiter := statement[index : delimiterEnd+1]
	end := strings.Index(statement[delimiterEnd+1:], delimiter)
	if end < 0 {
		return len(statement), true
	}
	return delimiterEnd + 1 + end + len(delimiter), true
}

func sqlLiteralBackslashEscapes(statement string, quoteIndex int) bool {
	if quoteIndex > 0 && (statement[quoteIndex-1] == 'e' || statement[quoteIndex-1] == 'E') {
		return quoteIndex == 1 || !isSQLIdentifier(statement[quoteIndex-2])
	}
	if quoteIndex < 2 || statement[quoteIndex-1] != '&' || statement[quoteIndex-2] != 'u' && statement[quoteIndex-2] != 'U' {
		return false
	}
	return quoteIndex == 2 || !isSQLIdentifier(statement[quoteIndex-3])
}

func skipQuotedSQL(statement string, index int, quote byte, backslashEscapes bool) int {
	for index < len(statement) {
		if backslashEscapes && statement[index] == '\\' && index+1 < len(statement) {
			index += 2
			continue
		}
		if statement[index] != quote {
			index++
			continue
		}
		if index+1 < len(statement) && statement[index+1] == quote {
			index += 2
			continue
		}
		return index + 1
	}
	return len(statement)
}

func isSQLNumberStart(statement string, index int) bool {
	if !isSQLDigit(statement[index]) {
		return false
	}
	if index == 0 {
		return true
	}
	previous := statement[index-1]
	return !isSQLIdentifier(previous) && previous != '$'
}

func isSQLDigit(char byte) bool {
	return char >= '0' && char <= '9'
}

func skipSQLNumber(statement string, index int) int {
	for index < len(statement) {
		char := statement[index]
		if char >= '0' && char <= '9' || char == '.' || char == 'e' || char == 'E' || char == '+' || char == '-' {
			index++
			continue
		}
		break
	}
	return index
}

func isSQLSpace(char byte) bool {
	return char == ' ' || char == '\n' || char == '\r' || char == '\t'
}

func isSQLIdentifier(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_'
}
