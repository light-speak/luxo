package lux

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/light-speak/luxo/pkg/lux/datetime"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
	"github.com/shopspring/decimal"
)

var (
	// ErrInvalidFilterOperator indicates an operator unsupported by the field type.
	ErrInvalidFilterOperator = errors.New("unsupported filter operator")
	// ErrInvalidFilterValue indicates a value outside the field's protocol contract.
	ErrInvalidFilterValue = errors.New("invalid filter value")
)

func comparisonOperator(op string) (string, error) {
	switch strings.ToLower(op) {
	case "eq":
		return "=", nil
	case "ne":
		return "!=", nil
	case "gt":
		return ">", nil
	case "gte":
		return ">=", nil
	case "lt":
		return "<", nil
	case "lte":
		return "<=", nil
	default:
		return "", ErrInvalidFilterOperator
	}
}

// FilterOp parses a signed 64-bit integer without silently dropping bad filters.
func (f IntField) FilterOp(op, val string) (Condition, error) {
	operator, err := comparisonOperator(op)
	if err != nil {
		return nil, err
	}
	v, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return nil, ErrInvalidFilterValue
	}
	return &cmpCond{col: f.col, op: operator, val: v}, nil
}

// FilterOp accepts finite decimal numbers with the same precision rules as JSON.
func (f FloatField) FilterOp(op, val string) (Condition, error) {
	operator, err := comparisonOperator(op)
	if err != nil {
		return nil, err
	}
	v, err := jsonutil.ParseFloat64Text(val)
	if err != nil {
		return nil, ErrInvalidFilterValue
	}
	return &cmpCond{col: f.col, op: operator, val: v}, nil
}

// FilterOp accepts RFC3339 values without dropping fractional seconds.
func (f TimeField) FilterOp(op, val string) (Condition, error) {
	// These aliases are Go helpers, not additional wire operator IDs.
	if strings.EqualFold(op, "after") {
		op = "gt"
	}
	if strings.EqualFold(op, "before") {
		op = "lt"
	}
	operator, err := comparisonOperator(op)
	if err != nil {
		return nil, err
	}
	v, err := datetime.ParseRFC3339(val)
	if err != nil {
		return nil, ErrInvalidFilterValue
	}
	return &cmpCond{col: f.col, op: operator, val: v}, nil
}

// FilterOp only accepts true/false and equality operators.
func (f BoolField) FilterOp(op, val string) (Condition, error) {
	if !strings.EqualFold(op, "eq") && !strings.EqualFold(op, "ne") {
		return nil, ErrInvalidFilterOperator
	}
	if val != "true" && val != "false" {
		return nil, ErrInvalidFilterValue
	}
	v := val == "true"
	if strings.EqualFold(op, "ne") {
		v = !v
	}
	return f.Eq(v), nil
}

// FilterOp escapes pattern literals and never treats an unknown operator as eq.
func (f StringField) FilterOp(op, val string) (Condition, error) {
	if !utf8.ValidString(val) {
		return nil, ErrInvalidFilterValue
	}
	switch strings.ToLower(op) {
	case "eq":
		return f.Eq(val), nil
	case "ne":
		return f.Neq(val), nil
	case "contains":
		return f.Like("%" + EscapeLike(val) + "%"), nil
	case "startswith":
		return f.Like(EscapeLike(val) + "%"), nil
	case "endswith":
		return f.Like("%" + EscapeLike(val)), nil
	default:
		return nil, ErrInvalidFilterOperator
	}
}

// FilterOp only enables full-text matching on an explicitly searchable field.
func (f SearchField) FilterOp(op, val string) (Condition, error) {
	if strings.EqualFold(op, "match") || strings.EqualFold(op, "search") {
		if !utf8.ValidString(val) {
			return nil, ErrInvalidFilterValue
		}
		return f.Match(val), nil
	}
	return f.StringField.FilterOp(op, val)
}

// FilterOp keeps UUID values typed and only accepts the canonical wire shape.
func (f UUIDField) FilterOp(op, val string) (Condition, error) {
	if !strings.EqualFold(op, "eq") && !strings.EqualFold(op, "ne") {
		return nil, ErrInvalidFilterOperator
	}
	if len(val) != 36 || val[8] != '-' || val[13] != '-' || val[18] != '-' || val[23] != '-' {
		return nil, ErrInvalidFilterValue
	}
	v, err := uuid.Parse(val)
	if err != nil {
		return nil, ErrInvalidFilterValue
	}
	if strings.EqualFold(op, "ne") {
		return f.Neq(v), nil
	}
	return f.Eq(v), nil
}

// FilterOp preserves decimal precision instead of coercing through float64.
func (f DecimalField) FilterOp(op, val string) (Condition, error) {
	operator, err := comparisonOperator(op)
	if err != nil {
		return nil, err
	}
	v, err := decimal.NewFromString(val)
	if err != nil {
		return nil, ErrInvalidFilterValue
	}
	return &cmpCond{col: f.col, op: operator, val: v}, nil
}
