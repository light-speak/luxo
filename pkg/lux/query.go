package lux

import (
	"fmt"
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
	fmt.Fprintf(&b, " FROM %s", table)
	argIdx, args = appendWhere(&b, argIdx, args, conds)
	appendOrderBy(&b, orderBy)
	appendLimitOffset(&b, &argIdx, &args, limit, offset)

	return b.String(), args
}

// BuildCountSQL builds a SELECT COUNT(*) query.
func BuildCountSQL(table string, conds []Condition) (string, []any) {
	var b strings.Builder
	var args []any
	argIdx := 1
	fmt.Fprintf(&b, "SELECT COUNT(*) FROM %s", table)
	argIdx, args = appendWhere(&b, argIdx, args, conds)
	_ = argIdx
	return b.String(), args
}

// BuildDeleteSQL builds a DELETE query.
func BuildDeleteSQL(table string, conds []Condition) (string, []any) {
	var b strings.Builder
	var args []any
	argIdx := 1
	fmt.Fprintf(&b, "DELETE FROM %s", table)
	argIdx, args = appendWhere(&b, argIdx, args, conds)
	_ = argIdx
	return b.String(), args
}

// BuildUpdateSQL builds an UPDATE query.
func BuildUpdateSQL(table string, sets []SetField, conds []Condition) (string, []any) {
	var b strings.Builder
	var args []any
	argIdx := 1
	fmt.Fprintf(&b, "UPDATE %s SET ", table)
	for i, s := range sets {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s = $%d", s.Col, argIdx)
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

func appendLimitOffset(b *strings.Builder, argIdx *int, args *[]any, limit, offset int) {
	if limit > 0 {
		fmt.Fprintf(b, " LIMIT $%d", *argIdx)
		*args = append(*args, limit)
		*argIdx++
	}
	if offset > 0 {
		fmt.Fprintf(b, " OFFSET $%d", *argIdx)
		*args = append(*args, offset)
		*argIdx++
	}
}
