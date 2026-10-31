package pg

import (
	"context"
	"math"

	"github.com/light-speak/luxo/pkg/lux"
	"github.com/shopspring/decimal"
)

// AggregateDestination selects exactly one typed target at the driver boundary.
type AggregateDestination struct {
	Int     *int64
	Float   *float64
	Decimal *decimal.Decimal
	value   DecimalValue
}

func (d *AggregateDestination) scanTarget() (any, error) {
	count := 0
	var target any
	if d.Int != nil {
		count++
		target = d.Int
	}
	if d.Float != nil {
		count++
		target = d.Float
	}
	if d.Decimal != nil {
		count++
		target = &d.value
	}
	if count != 1 {
		return nil, errInvalidScanValue
	}
	return target, nil
}

func (d *AggregateDestination) finish() error {
	if d.Float != nil && (math.IsInf(*d.Float, 0) || math.IsNaN(*d.Float)) {
		return errInvalidScanValue
	}
	if d.Decimal != nil {
		value, err := DecimalFromValue(d.value)
		if err != nil {
			return err
		}
		*d.Decimal = value
	}
	return nil
}

// AggregateBatchTo reads mixed numeric aggregates in one statement without
// rounding integer/decimal columns through a common floating-point container.
func (q *Query[T]) AggregateBatchTo(ctx context.Context, targets []AggregateDestination, specs ...lux.AggregateSpec) error {
	if len(targets) != len(specs) {
		return errInvalidScanValue
	}
	query, args, err := buildAggregateBatchSQL(q.table, q.conds, specs)
	if err != nil {
		return err
	}
	destinations := make([]any, len(targets))
	for i := range targets {
		if specs[i].Function == lux.AggregateAvg && targets[i].Int != nil {
			return errInvalidScanValue
		}
		destinations[i], err = targets[i].scanTarget()
		if err != nil {
			return err
		}
	}
	if err := q.db.conn.QueryRow(ctx, query, args...).Scan(destinations...); err != nil {
		return err
	}
	for i := range targets {
		if err := targets[i].finish(); err != nil {
			return err
		}
	}
	return nil
}
