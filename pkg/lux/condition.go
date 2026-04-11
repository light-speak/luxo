package lux

import (
	"fmt"
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

// In returns a condition matching any of the given values.
func (f IntField) In(vs ...int64) Condition {
	vals := make([]any, len(vs))
	for i, v := range vs {
		vals[i] = v
	}
	return &inCond{col: f.col, vals: vals}
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

// StringField provides typed conditions for string columns.
type StringField struct{ col string }

// NewStringField creates a StringField for the given column.
func NewStringField(col string) StringField { return StringField{col: col} }

func (f StringField) Eq(v string) Condition   { return &cmpCond{col: f.col, op: "=", val: v} }
func (f StringField) Neq(v string) Condition  { return &cmpCond{col: f.col, op: "!=", val: v} }
func (f StringField) Like(v string) Condition { return &cmpCond{col: f.col, op: "LIKE", val: v} }

// In returns a condition matching any of the given values.
func (f StringField) In(vs ...string) Condition {
	vals := make([]any, len(vs))
	for i, v := range vs {
		vals[i] = v
	}
	return &inCond{col: f.col, vals: vals}
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
func (f TimeField) Before(v time.Time) Condition { return &cmpCond{col: f.col, op: "<", val: v} }
func (f TimeField) After(v time.Time) Condition  { return &cmpCond{col: f.col, op: ">", val: v} }
func (f TimeField) IsNull() Condition            { return &nullCond{col: f.col, isNull: true} }
func (f TimeField) IsNotNull() Condition         { return &nullCond{col: f.col, isNull: false} }

// UUIDField provides typed conditions for UUID columns.
type UUIDField struct{ col string }

// NewUUIDField creates a UUIDField for the given column.
func NewUUIDField(col string) UUIDField { return UUIDField{col: col} }

func (f UUIDField) Eq(v uuid.UUID) Condition  { return &cmpCond{col: f.col, op: "=", val: v} }
func (f UUIDField) Neq(v uuid.UUID) Condition { return &cmpCond{col: f.col, op: "!=", val: v} }

// In returns a condition matching any of the given UUIDs.
func (f UUIDField) In(vs ...uuid.UUID) Condition {
	vals := make([]any, len(vs))
	for i, v := range vs {
		vals[i] = v
	}
	return &inCond{col: f.col, vals: vals}
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

// --- FilterOp: generic operator dispatch from string (used by generated filter parsers) ---

// FilterOp creates a condition from string operator and value.
func (f IntField) FilterOp(op, val string) Condition {
	v, _ := parseInt64(val)
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
		return f.Like("%" + val + "%")
	case "startswith":
		return f.Like(val + "%")
	case "endswith":
		return f.Like("%" + val)
	default:
		return f.Eq(val)
	}
}

// FilterOp creates a condition from string operator and value.
func (f BoolField) FilterOp(op, val string) Condition {
	v := strings.EqualFold(val, "true") || val == "1"
	return f.Eq(v)
}

func parseInt64(s string) (int64, error) {
	var v int64
	for _, c := range s {
		if c < '0' || c > '9' {
			if c == '-' {
				continue
			}
			return 0, fmt.Errorf("invalid int: %s", s)
		}
		v = v*10 + int64(c-'0')
	}
	if len(s) > 0 && s[0] == '-' {
		v = -v
	}
	return v, nil
}

// --- Internal condition implementations ---

// cmpCond is a comparison condition: col op $N.
type cmpCond struct {
	col string
	op  string
	val any
}

func (c *cmpCond) ToSQL(argOffset int) (string, []any) {
	return fmt.Sprintf("%s %s $%d", c.col, c.op, argOffset), []any{c.val}
}

// inCond is an IN condition: col IN ($N, $N+1, ...).
type inCond struct {
	col  string
	vals []any
}

func (c *inCond) ToSQL(argOffset int) (string, []any) {
	placeholders := make([]string, len(c.vals))
	for i := range c.vals {
		placeholders[i] = fmt.Sprintf("$%d", argOffset+i)
	}
	return fmt.Sprintf("%s IN (%s)", c.col, strings.Join(placeholders, ", ")), c.vals
}

// nullCond is a NULL/NOT NULL condition: col IS NULL or col IS NOT NULL.
type nullCond struct {
	col    string
	isNull bool
}

func (c *nullCond) ToSQL(argOffset int) (string, []any) {
	if c.isNull {
		return fmt.Sprintf("%s IS NULL", c.col), nil
	}
	return fmt.Sprintf("%s IS NOT NULL", c.col), nil
}
