package lux

import (
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Condition represents a WHERE clause condition.
type Condition interface {
	// ToSQL returns the SQL fragment and bound args.
	// argOffset is the starting $N placeholder index.
	ToSQL(argOffset int) (string, []any)
}

// SetField represents a column=value pair for UPDATE statements.
type SetField struct {
	Col string
	Val any
}

// --- Field condition types ---

// IntField provides typed conditions for integer columns.
type IntField struct{ col string }

// NewIntField creates an IntField for the given column.
func NewIntField(col string) IntField { return IntField{col: col} }

func (f IntField) Eq(v int64) Condition  { return &cmpCond{col: f.col, op: "=", val: v} }
func (f IntField) Neq(v int64) Condition { return &cmpCond{col: f.col, op: "!=", val: v} }
func (f IntField) Gt(v int64) Condition  { return &cmpCond{col: f.col, op: ">", val: v} }
func (f IntField) Gte(v int64) Condition { return &cmpCond{col: f.col, op: ">=", val: v} }
func (f IntField) Lt(v int64) Condition  { return &cmpCond{col: f.col, op: "<", val: v} }
func (f IntField) Lte(v int64) Condition { return &cmpCond{col: f.col, op: "<=", val: v} }

// Between returns a condition matching values in [from, to].
func (f IntField) Between(from, to int64) Condition {
	return &betweenCond{col: f.col, from: from, to: to}
}

func (f IntField) IsNull() Condition    { return &nullCond{col: f.col, isNull: true} }
func (f IntField) IsNotNull() Condition { return &nullCond{col: f.col, isNull: false} }

// In returns a condition matching any of the given values.
func (f IntField) In(vs ...int64) Condition {
	vals := make([]any, len(vs))
	for i, v := range vs {
		vals[i] = v
	}
	return &inCond{col: f.col, vals: vals}
}

// NotIn returns a condition excluding the given values.
func (f IntField) NotIn(vs ...int64) Condition {
	vals := make([]any, len(vs))
	for i, v := range vs {
		vals[i] = v
	}
	return &notInCond{col: f.col, vals: vals}
}

// FloatField provides typed conditions for float columns.
type FloatField struct{ col string }

// NewFloatField creates a FloatField for the given column.
func NewFloatField(col string) FloatField { return FloatField{col: col} }

func (f FloatField) Eq(v float64) Condition  { return &cmpCond{col: f.col, op: "=", val: v} }
func (f FloatField) Neq(v float64) Condition { return &cmpCond{col: f.col, op: "!=", val: v} }
func (f FloatField) Gt(v float64) Condition  { return &cmpCond{col: f.col, op: ">", val: v} }
func (f FloatField) Gte(v float64) Condition { return &cmpCond{col: f.col, op: ">=", val: v} }
func (f FloatField) Lt(v float64) Condition  { return &cmpCond{col: f.col, op: "<", val: v} }
func (f FloatField) Lte(v float64) Condition { return &cmpCond{col: f.col, op: "<=", val: v} }

// Between returns a condition matching values in [from, to].
func (f FloatField) Between(from, to float64) Condition {
	return &betweenCond{col: f.col, from: from, to: to}
}

func (f FloatField) IsNull() Condition    { return &nullCond{col: f.col, isNull: true} }
func (f FloatField) IsNotNull() Condition { return &nullCond{col: f.col, isNull: false} }

// In returns a condition matching any of the given values.
func (f FloatField) In(vs ...float64) Condition {
	vals := make([]any, len(vs))
	for i, v := range vs {
		vals[i] = v
	}
	return &inCond{col: f.col, vals: vals}
}

// NotIn returns a condition excluding the given values.
func (f FloatField) NotIn(vs ...float64) Condition {
	vals := make([]any, len(vs))
	for i, v := range vs {
		vals[i] = v
	}
	return &notInCond{col: f.col, vals: vals}
}

// StringField provides typed conditions for string columns.
type StringField struct{ col string }

// NewStringField creates a StringField for the given column.
func NewStringField(col string) StringField { return StringField{col: col} }

func (f StringField) Eq(v string) Condition      { return &cmpCond{col: f.col, op: "=", val: v} }
func (f StringField) Neq(v string) Condition     { return &cmpCond{col: f.col, op: "!=", val: v} }
func (f StringField) Like(v string) Condition    { return &cmpCond{col: f.col, op: "LIKE", val: v} }
func (f StringField) NotLike(v string) Condition { return &cmpCond{col: f.col, op: "NOT LIKE", val: v} }
func (f StringField) ILike(v string) Condition   { return &cmpCond{col: f.col, op: "ILIKE", val: v} }
func (f StringField) IsNull() Condition          { return &nullCond{col: f.col, isNull: true} }
func (f StringField) IsNotNull() Condition       { return &nullCond{col: f.col, isNull: false} }

// In returns a condition matching any of the given values.
func (f StringField) In(vs ...string) Condition {
	vals := make([]any, len(vs))
	for i, v := range vs {
		vals[i] = v
	}
	return &inCond{col: f.col, vals: vals}
}

// NotIn returns a condition excluding the given values.
func (f StringField) NotIn(vs ...string) Condition {
	vals := make([]any, len(vs))
	for i, v := range vs {
		vals[i] = v
	}
	return &notInCond{col: f.col, vals: vals}
}

// BoolField provides typed conditions for boolean columns.
type BoolField struct{ col string }

// NewBoolField creates a BoolField for the given column.
func NewBoolField(col string) BoolField { return BoolField{col: col} }

func (f BoolField) Eq(v bool) Condition { return &cmpCond{col: f.col, op: "=", val: v} }
func (f BoolField) IsTrue() Condition   { return &cmpCond{col: f.col, op: "=", val: true} }
func (f BoolField) IsFalse() Condition  { return &cmpCond{col: f.col, op: "=", val: false} }

// TimeField provides typed conditions for timestamp columns.
type TimeField struct{ col string }

// NewTimeField creates a TimeField for the given column.
func NewTimeField(col string) TimeField { return TimeField{col: col} }

func (f TimeField) Eq(v time.Time) Condition     { return &cmpCond{col: f.col, op: "=", val: v} }
func (f TimeField) Neq(v time.Time) Condition    { return &cmpCond{col: f.col, op: "!=", val: v} }
func (f TimeField) Before(v time.Time) Condition { return &cmpCond{col: f.col, op: "<", val: v} }
func (f TimeField) After(v time.Time) Condition  { return &cmpCond{col: f.col, op: ">", val: v} }
func (f TimeField) Gte(v time.Time) Condition    { return &cmpCond{col: f.col, op: ">=", val: v} }
func (f TimeField) Lte(v time.Time) Condition    { return &cmpCond{col: f.col, op: "<=", val: v} }
func (f TimeField) IsNull() Condition            { return &nullCond{col: f.col, isNull: true} }
func (f TimeField) IsNotNull() Condition         { return &nullCond{col: f.col, isNull: false} }

// Between returns a condition matching times in [from, to].
func (f TimeField) Between(from, to time.Time) Condition {
	return &betweenCond{col: f.col, from: from, to: to}
}

// FilterOp creates a condition from string operator and value (RFC3339).
// Returns FALSE condition if the value cannot be parsed.
func (f TimeField) FilterOp(op, val string) Condition {
	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return falseCond{}
	}
	switch strings.ToLower(op) {
	case "eq":
		return f.Eq(t)
	case "ne":
		return f.Neq(t)
	case "gt", "after":
		return f.After(t)
	case "gte":
		return f.Gte(t)
	case "lt", "before":
		return f.Before(t)
	case "lte":
		return f.Lte(t)
	default:
		return f.Eq(t)
	}
}

// UUIDField provides typed conditions for UUID columns.
type UUIDField struct{ col string }

// NewUUIDField creates a UUIDField for the given column.
func NewUUIDField(col string) UUIDField { return UUIDField{col: col} }

func (f UUIDField) Eq(v uuid.UUID) Condition  { return &cmpCond{col: f.col, op: "=", val: v} }
func (f UUIDField) Neq(v uuid.UUID) Condition { return &cmpCond{col: f.col, op: "!=", val: v} }
func (f UUIDField) IsNull() Condition         { return &nullCond{col: f.col, isNull: true} }
func (f UUIDField) IsNotNull() Condition      { return &nullCond{col: f.col, isNull: false} }

// In returns a condition matching any of the given UUIDs.
func (f UUIDField) In(vs ...uuid.UUID) Condition {
	vals := make([]any, len(vs))
	for i, v := range vs {
		vals[i] = v
	}
	return &inCond{col: f.col, vals: vals}
}

// NotIn returns a condition excluding the given UUIDs.
func (f UUIDField) NotIn(vs ...uuid.UUID) Condition {
	vals := make([]any, len(vs))
	for i, v := range vs {
		vals[i] = v
	}
	return &notInCond{col: f.col, vals: vals}
}

// DecimalField provides typed conditions for decimal columns.
type DecimalField struct{ col string }

// NewDecimalField creates a DecimalField for the given column.
func NewDecimalField(col string) DecimalField { return DecimalField{col: col} }

func (f DecimalField) Eq(v decimal.Decimal) Condition  { return &cmpCond{col: f.col, op: "=", val: v} }
func (f DecimalField) Neq(v decimal.Decimal) Condition { return &cmpCond{col: f.col, op: "!=", val: v} }
func (f DecimalField) Gt(v decimal.Decimal) Condition  { return &cmpCond{col: f.col, op: ">", val: v} }
func (f DecimalField) Gte(v decimal.Decimal) Condition { return &cmpCond{col: f.col, op: ">=", val: v} }
func (f DecimalField) Lt(v decimal.Decimal) Condition  { return &cmpCond{col: f.col, op: "<", val: v} }
func (f DecimalField) Lte(v decimal.Decimal) Condition { return &cmpCond{col: f.col, op: "<=", val: v} }
func (f DecimalField) IsNull() Condition               { return &nullCond{col: f.col, isNull: true} }
func (f DecimalField) IsNotNull() Condition            { return &nullCond{col: f.col, isNull: false} }

// Between returns a condition matching values in [from, to].
func (f DecimalField) Between(from, to decimal.Decimal) Condition {
	return &betweenCond{col: f.col, from: from, to: to}
}

// In returns a condition matching any of the given values.
func (f DecimalField) In(vs ...decimal.Decimal) Condition {
	vals := make([]any, len(vs))
	for i, v := range vs {
		vals[i] = v
	}
	return &inCond{col: f.col, vals: vals}
}

// NotIn returns a condition excluding the given values.
func (f DecimalField) NotIn(vs ...decimal.Decimal) Condition {
	vals := make([]any, len(vs))
	for i, v := range vs {
		vals[i] = v
	}
	return &notInCond{col: f.col, vals: vals}
}

// --- FilterOp: generic operator dispatch from string (used by generated filter parsers) ---

// FilterOp creates a condition from string operator and value.
func (f IntField) FilterOp(op, val string) Condition {
	v, err := parseInt64(val)
	if err != nil {
		return falseCond{}
	}
	switch strings.ToLower(op) {
	case "eq":
		return f.Eq(v)
	case "ne":
		return f.Neq(v)
	case "gt":
		return f.Gt(v)
	case "gte":
		return f.Gte(v)
	case "lt":
		return f.Lt(v)
	case "lte":
		return f.Lte(v)
	default:
		return f.Eq(v)
	}
}

// FilterOp creates a condition from string operator and value.
func (f StringField) FilterOp(op, val string) Condition {
	switch strings.ToLower(op) {
	case "eq":
		return f.Eq(val)
	case "ne":
		return f.Neq(val)
	case "contains":
		return f.Like("%" + EscapeLike(val) + "%")
	case "startswith":
		return f.Like(EscapeLike(val) + "%")
	case "endswith":
		return f.Like("%" + EscapeLike(val))
	default:
		return f.Eq(val)
	}
}

// FilterOp creates a condition from string operator and value.
func (f FloatField) FilterOp(op, val string) Condition {
	v, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return falseCond{}
	}
	switch strings.ToLower(op) {
	case "eq":
		return f.Eq(v)
	case "ne":
		return f.Neq(v)
	case "gt":
		return f.Gt(v)
	case "gte":
		return f.Gte(v)
	case "lt":
		return f.Lt(v)
	case "lte":
		return f.Lte(v)
	default:
		return f.Eq(v)
	}
}

// FilterOp creates a condition from string operator and value.
func (f BoolField) FilterOp(op, val string) Condition {
	v := strings.EqualFold(val, "true") || val == "1"
	if strings.EqualFold(op, "ne") {
		return f.Eq(!v)
	}
	return f.Eq(v)
}

func parseInt64(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

// likeReplacer escapes SQL LIKE wildcard characters.
var likeReplacer = strings.NewReplacer("%", "\\%", "_", "\\_")

// EscapeLike escapes SQL LIKE wildcard characters (%, _) in user input.
func EscapeLike(s string) string {
	return likeReplacer.Replace(s)
}

// --- Internal condition implementations ---

// cmpCond is a comparison condition: col op $N.
type cmpCond struct {
	col string
	op  string
	val any
}

func (c *cmpCond) ToSQL(argOffset int) (string, []any) {
	// c.col + " " + c.op + " $" + itoa — no fmt.Sprintf
	var buf [32]byte
	b := append(buf[:0], c.col...)
	b = append(b, ' ')
	b = append(b, c.op...)
	b = append(b, " $"...)
	b = strconv.AppendInt(b, int64(argOffset), 10)
	return string(b), []any{c.val}
}

// inCond is an IN condition: col IN ($N, $N+1, ...).
type inCond struct {
	col  string
	vals []any
}

func (c *inCond) ToSQL(argOffset int) (string, []any) {
	if len(c.vals) == 0 {
		return "FALSE", nil
	}
	var b strings.Builder
	b.WriteString(c.col)
	b.WriteString(" IN (")
	var tmp [20]byte
	for i := range c.vals {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('$')
		b.Write(strconv.AppendInt(tmp[:0], int64(argOffset+i), 10))
	}
	b.WriteByte(')')
	return b.String(), c.vals
}

// notInCond is a NOT IN condition: col NOT IN ($N, $N+1, ...).
type notInCond struct {
	col  string
	vals []any
}

func (c *notInCond) ToSQL(argOffset int) (string, []any) {
	if len(c.vals) == 0 {
		return "TRUE", nil
	}
	var b strings.Builder
	b.WriteString(c.col)
	b.WriteString(" NOT IN (")
	var tmp [20]byte
	for i := range c.vals {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('$')
		b.Write(strconv.AppendInt(tmp[:0], int64(argOffset+i), 10))
	}
	b.WriteByte(')')
	return b.String(), c.vals
}

// nullCond is a NULL/NOT NULL condition: col IS NULL or col IS NOT NULL.
type nullCond struct {
	col    string
	isNull bool
}

func (c *nullCond) ToSQL(argOffset int) (string, []any) {
	if c.isNull {
		return c.col + " IS NULL", nil
	}
	return c.col + " IS NOT NULL", nil
}

// betweenCond is a BETWEEN condition: col BETWEEN $N AND $N+1.
type betweenCond struct {
	col  string
	from any
	to   any
}

func (c *betweenCond) ToSQL(argOffset int) (string, []any) {
	var b strings.Builder
	var tmp [20]byte
	b.WriteString(c.col)
	b.WriteString(" BETWEEN $")
	b.Write(strconv.AppendInt(tmp[:0], int64(argOffset), 10))
	b.WriteString(" AND $")
	b.Write(strconv.AppendInt(tmp[:0], int64(argOffset+1), 10))
	return b.String(), []any{c.from, c.to}
}

// falseCond always evaluates to FALSE — used when filter input is invalid.
type falseCond struct{}

func (c falseCond) ToSQL(argOffset int) (string, []any) { return "FALSE", nil }

// rawCond is a pre-built SQL condition with embedded args.
type rawCond struct {
	sql  string
	args []any
}

// RawCondition creates a condition from raw SQL with args.
// Used for complex conditions like OR groups.
func RawCondition(sql string, args ...any) Condition {
	return &rawCond{sql: sql, args: args}
}

func (c *rawCond) ToSQL(argOffset int) (string, []any) {
	// Rewrite $1, $2, ... to $argOffset, $argOffset+1, ...
	// Use a builder to avoid $1 matching inside $10
	var b strings.Builder
	i := 0
	for i < len(c.sql) {
		if c.sql[i] == '$' && i+1 < len(c.sql) && c.sql[i+1] >= '1' && c.sql[i+1] <= '9' {
			// Parse the full number after $
			j := i + 1
			for j < len(c.sql) && c.sql[j] >= '0' && c.sql[j] <= '9' {
				j++
			}
			n := 0
			for _, ch := range c.sql[i+1 : j] {
				n = n*10 + int(ch-'0')
			}
			b.WriteByte('$')
			var tmp [20]byte
			b.Write(strconv.AppendInt(tmp[:0], int64(argOffset+n-1), 10))
			i = j
		} else {
			b.WriteByte(c.sql[i])
			i++
		}
	}
	return "(" + b.String() + ")", c.args
}
