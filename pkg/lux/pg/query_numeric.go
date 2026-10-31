package pg

import (
	"context"
	"math"

	"github.com/light-speak/luxo/pkg/lux"
	"github.com/shopspring/decimal"
)

func (q *Query[T]) floatAggregate(ctx context.Context, operation, col string) (float64, error) {
	query, args := lux.BuildAggregateSQL(q.table, operation, col, q.conds)
	value, err := q.db.QueryScalar[float64](ctx, query, args...)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errInvalidScanValue
	}
	return value, nil
}

func (q *Query[T]) decimalAggregate(ctx context.Context, operation, col string) (decimal.Decimal, error) {
	query, args := lux.BuildAggregateSQL(q.table, operation, col, q.conds)
	value, err := q.db.QueryScalar[DecimalValue](ctx, query, args...)
	if err != nil {
		return decimal.Decimal{}, err
	}
	return DecimalFromValue(value)
}

// SumFloat sums a Float column without narrowing to Int.
func (q *Query[T]) SumFloat(ctx context.Context, col string) (float64, error) {
	return q.floatAggregate(ctx, "SUM", col)
}

// MinFloat returns the minimum of a Float column.
func (q *Query[T]) MinFloat(ctx context.Context, col string) (float64, error) {
	return q.floatAggregate(ctx, "MIN", col)
}

// MaxFloat returns the maximum of a Float column.
func (q *Query[T]) MaxFloat(ctx context.Context, col string) (float64, error) {
	return q.floatAggregate(ctx, "MAX", col)
}

// SumDecimal preserves the database's decimal sum without passing through Float.
func (q *Query[T]) SumDecimal(ctx context.Context, col string) (decimal.Decimal, error) {
	return q.decimalAggregate(ctx, "SUM", col)
}

// AvgDecimal preserves the database's decimal average without passing through Float.
func (q *Query[T]) AvgDecimal(ctx context.Context, col string) (decimal.Decimal, error) {
	return q.decimalAggregate(ctx, "AVG", col)
}

// MinDecimal returns the minimum of a Decimal column.
func (q *Query[T]) MinDecimal(ctx context.Context, col string) (decimal.Decimal, error) {
	return q.decimalAggregate(ctx, "MIN", col)
}

// MaxDecimal returns the maximum of a Decimal column.
func (q *Query[T]) MaxDecimal(ctx context.Context, col string) (decimal.Decimal, error) {
	return q.decimalAggregate(ctx, "MAX", col)
}
