package lux

import (
	"fmt"
	"strconv"
	"strings"
)

// AggregateFunction is a database-independent scalar aggregate operation.
// Backend packages translate it into their native query language.
type AggregateFunction string

const (
	AggregateCount AggregateFunction = "COUNT"
	AggregateSum   AggregateFunction = "SUM"
	AggregateAvg   AggregateFunction = "AVG"
	AggregateMin   AggregateFunction = "MIN"
	AggregateMax   AggregateFunction = "MAX"
)

// AggregateSpec describes one scalar result in a fused aggregate query.
// Conditions are additional to the common conditions already held by Query.
type AggregateSpec struct {
	Function   AggregateFunction
	Column     string
	Conditions []Condition
}

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

// GroupAgg represents one explicitly selected column in a GROUP BY query.
type GroupAgg struct {
	Fn    string // "SUM", "AVG", "COUNT", "MAX", "MIN", or empty for a grouping key
	Col   string // column to aggregate, empty for COUNT(*)
	Alias string // result alias in output
}

// SQLExpression shares aggregate spelling between SELECT and checked HAVING.
// Column/function identifiers must originate from the trusted schema, not input.
func (a GroupAgg) SQLExpression() string {
	var b strings.Builder
	appendGroupExpression(&b, a)
	return b.String()
}

func appendGroupExpression(b *strings.Builder, agg GroupAgg) {
	if agg.Fn == "" {
		b.WriteString(agg.Col)
		return
	}
	if agg.Col == "" {
		b.WriteString(agg.Fn)
		b.WriteString("(*)")
		return
	}
	coalesce := agg.Fn == "SUM" || agg.Fn == "AVG"
	if coalesce {
		b.WriteString("COALESCE(")
	}
	b.WriteString(agg.Fn)
	b.WriteByte('(')
	b.WriteString(agg.Col)
	b.WriteByte(')')
	if coalesce {
		b.WriteString(", 0)")
	}
}

// BuildGroupBySQL selects only the requested columns, in projection order.
// Grouping keys need not be returned, and may be renamed by the projection.
func BuildGroupBySQL(table string, groupCols []string, aggs []GroupAgg, conds []Condition, orderBy []string, limit, offset int, having ...Condition) (string, []any) {
	var b strings.Builder
	var args []any
	argIdx := 1

	b.WriteString("SELECT ")
	for i, agg := range aggs {
		if i > 0 {
			b.WriteString(", ")
		}
		appendGroupExpression(&b, agg)
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
	if len(having) != 0 {
		group := conditionGroup{operator: " AND ", emptySQL: "TRUE", conditions: having}
		fragment, values := group.ToSQL(argIdx)
		b.WriteString(" HAVING ")
		b.WriteString(fragment)
		args = append(args, values...)
		argIdx += len(values)
	}
	appendOrderBy(&b, orderBy)
	appendLimitOffset(&b, &argIdx, &args, limit, offset)

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
