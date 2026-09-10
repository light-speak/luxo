package lux

import (
	"strconv"
	"strings"
)

// PlaceholderFunc renders a backend-specific parameter placeholder.
// It is passed explicitly so different backends can safely run concurrently.
type PlaceholderFunc func(index int) string

// PostgreSQLPlaceholder renders PostgreSQL's numbered placeholder syntax.
func PostgreSQLPlaceholder(index int) string {
	return "$" + strconv.Itoa(index)
}

// AggregateSQL generates a SQL aggregate query for computed fields.
// PostgreSQL placeholders remain the compatibility default.
func AggregateSQL(fn, table, fkCol, targetCol string) string {
	return AggregateSQLWithPlaceholder(fn, table, fkCol, targetCol, PostgreSQLPlaceholder)
}

// AggregateSQLWithPlaceholder generates an aggregate query using an explicit
// placeholder renderer. SQL backends own the renderer; no process-global
// dialect state is mutated.
func AggregateSQLWithPlaceholder(fn, table, fkCol, targetCol string, placeholder PlaceholderFunc) string {
	if placeholder == nil {
		placeholder = PostgreSQLPlaceholder
	}
	col := "*"
	if targetCol != "" {
		col = targetCol
	}
	var query strings.Builder
	query.Grow(len(fn) + len(table) + len(fkCol) + len(col) + 48)
	if fn == "COUNT" {
		query.WriteString("SELECT COUNT(*) FROM ")
	} else {
		query.WriteString("SELECT COALESCE(")
		query.WriteString(fn)
		query.WriteByte('(')
		query.WriteString(col)
		query.WriteString("), 0) FROM ")
	}
	query.WriteString(table)
	query.WriteString(" WHERE ")
	query.WriteString(fkCol)
	query.WriteString(" = ")
	query.WriteString(placeholder(1))
	return query.String()
}
