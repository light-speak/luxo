// Package search provides pluggable full-text search backends for Luxo.
// The core interface (SearchBackend) allows swapping between PG tsvector,
// MySQL FULLTEXT, Meilisearch, etc. without changing generated code.
//
// pkg/lux/search/ has zero database driver dependencies — backends return
// raw SQL strings; callers handle execution.
package search

import (
	"fmt"
	"strconv"
	"strings"
)

// SearchBackend defines the interface for full-text search implementations.
// Each backend generates SQL/conditions and migration DDL for its engine.
type SearchBackend interface {
	// BuildSearchCondition returns a SQL fragment and args for a full-text search.
	// column is the DB column name, query is the user search text,
	// paramIdx is the starting $N placeholder index.
	BuildSearchCondition(column, query string, paramIdx int) (sql string, args []any)

	// MigrationSQL returns idempotent DDL statements to create search
	// infrastructure (indexes, generated columns, etc.) for the given table/column.
	MigrationSQL(table, column string) []string

	// Name returns the backend identifier (e.g., "pg_tsvector", "mysql_fulltext").
	Name() string
}

// PGTsvector implements SearchBackend using PostgreSQL tsvector/tsquery.
type PGTsvector struct {
	// Config is the PG text search configuration name (e.g., "simple", "english").
	// Defaults to "simple" if empty.
	Config string
}

// BuildSearchCondition returns a PG tsvector search condition.
// Example: to_tsvector('simple', title) @@ plainto_tsquery('simple', $1)
func (p *PGTsvector) BuildSearchCondition(column, query string, paramIdx int) (string, []any) {
	cfg := p.config()
	qcol := quoteIdent(column)
	var b strings.Builder
	b.WriteString("to_tsvector('")
	b.WriteString(cfg)
	b.WriteString("', ")
	b.WriteString(qcol)
	b.WriteString(") @@ plainto_tsquery('")
	b.WriteString(cfg)
	b.WriteString("', $")
	b.WriteString(strconv.Itoa(paramIdx))
	b.WriteByte(')')
	return b.String(), []any{query}
}

// MigrationSQL returns idempotent DDL for PG tsvector search infrastructure:
// 1. A GIN index on the expression to_tsvector(config, column).
func (p *PGTsvector) MigrationSQL(table, column string) []string {
	cfg := p.config()
	idxName := fmt.Sprintf("idx_%s_%s_search", table, column)
	qtable := quoteIdent(table)
	qcol := quoteIdent(column)
	return []string{
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s USING gin(to_tsvector('%s', %s));",
			idxName, qtable, cfg, qcol),
	}
}

// Name returns the backend identifier.
func (p *PGTsvector) Name() string {
	return "pg_tsvector"
}

func (p *PGTsvector) config() string {
	if p.Config == "" {
		return "simple"
	}
	return p.Config
}

// quoteIdent quotes a SQL identifier with double quotes,
// escaping any embedded double quotes per SQL standard.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
