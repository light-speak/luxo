package pg

import (
	"context"
	"math/big"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/light-speak/luxo/pkg/lux"
	"github.com/shopspring/decimal"
)

// numericBenchConn isolates construction and dispatch from pgx/network costs.
type numericBenchConn struct{ conn }

var numericBenchCoefficient = big.NewInt(42)

func (c *numericBenchConn) QueryRow(context.Context, string, ...any) pgx.Row { return c }
func (c *numericBenchConn) Scan(targets ...any) error {
	for _, target := range targets {
		switch target := target.(type) {
		case *int64:
			*target = 42
		case *float64:
			*target = 42
		case *DecimalValue:
			*target = DecimalValue{Int: numericBenchCoefficient, Valid: true}
		default:
			return errInvalidScanValue
		}
	}
	return nil
}

// legacyNumericBatch reproduces the pre-validation integer-only implementation.
func legacyNumericBatch(q *Query[int64], ctx context.Context, specs []lux.AggregateSpec) ([]int64, error) {
	query, args, err := buildAggregateBatchSQL(q.table, q.conds, specs)
	if err != nil {
		return nil, err
	}
	values := make([]int64, len(specs))
	targets := make([]any, len(values))
	for i := range values {
		targets[i] = &values[i]
	}
	if err := q.db.conn.QueryRow(ctx, query, args...).Scan(targets...); err != nil {
		return nil, err
	}
	return values, nil
}

func BenchmarkNumericAggregateContract(b *testing.B) {
	db := &DB{conn: &numericBenchConn{}}
	benchmarkNumericAggregate(b, db)
}

// BenchmarkNumericAggregatePostgres includes the real driver and network.
func BenchmarkNumericAggregatePostgres(b *testing.B) {
	db := testDB(b)
	if _, err := db.conn.Exec(b.Context(), "CREATE TABLE metrics (i bigint, d numeric); INSERT INTO metrics VALUES (42,42)"); err != nil {
		b.Fatal(err)
	}
	benchmarkNumericAggregate(b, db)
}

func benchmarkNumericAggregate(b *testing.B, db *DB) {
	q := NewQuery[int64](db, "metrics", scanMethodValue, nil)
	ctx := context.Background()
	specs := []lux.AggregateSpec{{Function: lux.AggregateSum, Column: "i"}, {Function: lux.AggregateMax, Column: "i"}, {Function: lux.AggregateMin, Column: "i"}}
	for _, current := range []bool{false, true} {
		name := "legacy"
		if current {
			name = "current"
		}
		b.Run("integer_batch/"+name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var values []int64
				var err error
				if current {
					values, err = q.AggregateBatch(ctx, specs...)
				} else {
					values, err = legacyNumericBatch(q, ctx, specs)
				}
				if err != nil || len(values) != 3 || values[2] != 42 {
					b.Fatal(values, err)
				}
			}
		})
		b.Run("average_integral_control/"+name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var value float64
				var err error
				if current {
					value, err = q.Avg(ctx, "i")
				} else {
					query, args := lux.BuildAggregateSQL(q.table, "AVG", "i", q.conds)
					var old int64
					old, err = db.QueryScalar[int64](ctx, query, args...)
					value = float64(old)
				}
				if err != nil || value != 42 {
					b.Fatal(value, err)
				}
			}
		})
	}
	b.Run("mixed_batch_absolute", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var integer int64
			var average float64
			err := q.AggregateBatchTo(ctx, []AggregateDestination{{Int: &integer}, {Float: &average}}, lux.AggregateSpec{Function: lux.AggregateSum, Column: "i"}, lux.AggregateSpec{Function: lux.AggregateAvg, Column: "i"})
			if err != nil || integer != 42 || average != 42 {
				b.Fatal(integer, average, err)
			}
		}
	})
	b.Run("decimal_absolute", func(b *testing.B) {
		want := decimal.NewFromInt(42)
		b.ReportAllocs()
		for b.Loop() {
			value, err := q.SumDecimal(ctx, "d")
			if err != nil || !value.Equal(want) {
				b.Fatal(value, err)
			}
		}
	})
}
