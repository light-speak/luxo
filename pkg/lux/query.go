package lux

import (
	"fmt"
	"strconv"
	"strings"
)

// --- SQL builder functions (shared by PG, MySQL, SQLite) ---

// BuildSelectSQL builds a SELECT query string with args.
func BuildSelectSQL(table string, fields []string, conds []Condition, orderBy []string, limit, offset int) (string, []any) {
	var b strings.Builder
	var args []any
	argIdx := 1

	b.WriteString("SELECT ")
	if len(fields) > 0 {
		b.WriteString(strings.Join(fields, ", "))
	} else {
		b.WriteByte('*')
	}
	b.WriteString(" FROM ")
	b.WriteString(table)
	argIdx, args = appendWhere(&b, argIdx, args, conds)
	appendOrderBy(&b, orderBy)
	appendLimitOffset(&b, &argIdx, &args, limit, offset)

	return b.String(), args
}

// BuildExistsSQL builds a SELECT EXISTS(SELECT 1 FROM ... WHERE ... LIMIT 1) query.
func BuildExistsSQL(table string, conds []Condition) (string, []any) {
	var b strings.Builder
	var args []any
	argIdx := 1
	b.WriteString("SELECT EXISTS(SELECT 1 FROM ")
	b.WriteString(table)
	argIdx, args = appendWhere(&b, argIdx, args, conds)
	_ = argIdx
	b.WriteString(" LIMIT 1)")
	return b.String(), args
}

// BuildCountSQL builds a SELECT COUNT(*) query.
func BuildCountSQL(table string, conds []Condition) (string, []any) {
	var b strings.Builder
	var args []any
	argIdx := 1
	b.WriteString("SELECT COUNT(*) FROM ")
	b.WriteString(table)
	argIdx, args = appendWhere(&b, argIdx, args, conds)
	_ = argIdx
	return b.String(), args
}

// BuildAggregateSQL builds a SELECT AGG(col) query (e.g., SUM, AVG, MIN, MAX).
func BuildAggregateSQL(table, fn, col string, conds []Condition) (string, []any) {
	var b strings.Builder
	var args []any
	argIdx := 1
	fmt.Fprintf(&b, "SELECT COALESCE(%s(%s), 0) FROM %s", fn, col, table)
	argIdx, args = appendWhere(&b, argIdx, args, conds)
	_ = argIdx
	return b.String(), args
}

// BuildDeleteSQL builds a DELETE query.
func BuildDeleteSQL(table string, conds []Condition) (string, []any) {
	var b strings.Builder
	var args []any
	argIdx := 1
	b.WriteString("DELETE FROM ")
	b.WriteString(table)
	argIdx, args = appendWhere(&b, argIdx, args, conds)
	_ = argIdx
	return b.String(), args
}

// BuildUpdateSQL builds an UPDATE query.
func BuildUpdateSQL(table string, sets []SetField, conds []Condition) (string, []any) {
	var b strings.Builder
	var args []any
	argIdx := 1
	b.WriteString("UPDATE ")
	b.WriteString(table)
	b.WriteString(" SET ")
	var tmp [20]byte
	for i, s := range sets {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(s.Col)
		if s.Atomic != "" {
			// Atomic: col = col + $N
			b.WriteString(" = ")
			b.WriteString(s.Col)
			b.WriteByte(' ')
			b.WriteString(s.Atomic)
			b.WriteString(" $")
		} else {
			b.WriteString(" = $")
		}
		b.Write(strconv.AppendInt(tmp[:0], int64(argIdx), 10))
		args = append(args, s.Val)
		argIdx++
	}
	argIdx, args = appendWhere(&b, argIdx, args, conds)
	_ = argIdx
	return b.String(), args
}

func appendWhere(b *strings.Builder, argIdx int, args []any, conds []Condition) (int, []any) {
	if len(conds) == 0 {
		return argIdx, args
	}
	b.WriteString(" WHERE ")
	for i, c := range conds {
		if i > 0 {
			b.WriteString(" AND ")
		}
		frag, cArgs := c.ToSQL(argIdx)
		b.WriteString(frag)
		args = append(args, cArgs...)
		argIdx += len(cArgs)
	}
	return argIdx, args
}

func appendOrderBy(b *strings.Builder, orderBy []string) {
	if len(orderBy) == 0 {
		return
	}
	b.WriteString(" ORDER BY ")
	b.WriteString(strings.Join(orderBy, ", "))
}

// GroupAgg represents a single aggregation in a GROUP BY query.
type GroupAgg struct {
	Fn    string // "SUM", "AVG", "COUNT", "MAX", "MIN"
	Col   string // column to aggregate, empty for COUNT(*)
	Alias string // result alias in output
}

// BuildGroupBySQL builds a GROUP BY query with multiple aggregations.
// SELECT groupCols..., AGG(col) AS alias, ... FROM table WHERE ... GROUP BY groupCols... ORDER BY ...
func BuildGroupBySQL(table string, groupCols []string, aggs []GroupAgg, conds []Condition, orderBy []string, limit int) (string, []any) {
	var b strings.Builder
	var args []any
	argIdx := 1

	b.WriteString("SELECT ")
	b.WriteString(strings.Join(groupCols, ", "))
	for _, agg := range aggs {
		b.WriteString(", ")
		if agg.Col == "" {
			fmt.Fprintf(&b, "%s(*)", agg.Fn)
		} else {
			fmt.Fprintf(&b, "COALESCE(%s(%s), 0)", agg.Fn, agg.Col)
		}
		if agg.Alias != "" {
			b.WriteString(" AS ")
			b.WriteString(agg.Alias)
		}
	}
	b.WriteString(" FROM ")
	b.WriteString(table)
	argIdx, args = appendWhere(&b, argIdx, args, conds)
	b.WriteString(" GROUP BY ")
	b.WriteString(strings.Join(groupCols, ", "))
	appendOrderBy(&b, orderBy)
	appendLimitOffset(&b, &argIdx, &args, limit, 0)

	return b.String(), args
}

func appendLimitOffset(b *strings.Builder, argIdx *int, args *[]any, limit, offset int) {
	var tmp [20]byte
	if limit > 0 {
		b.WriteString(" LIMIT $")
		b.Write(strconv.AppendInt(tmp[:0], int64(*argIdx), 10))
		*args = append(*args, limit)
		*argIdx++
	}
	if offset > 0 {
		b.WriteString(" OFFSET $")
		b.Write(strconv.AppendInt(tmp[:0], int64(*argIdx), 10))
		*args = append(*args, offset)
		*argIdx++
	}
}
