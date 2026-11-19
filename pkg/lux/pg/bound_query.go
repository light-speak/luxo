package pg

import (
	"context"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (db *DB) queryBound(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	bound, err := bindArguments(args)
	if err != nil {
		return nil, err
	}
	return db.conn.Query(ctx, query, bound...)
}

func (db *DB) queryRowBound(ctx context.Context, query string, args ...any) pgx.Row {
	bound, err := bindArguments(args)
	if err != nil {
		return rejectedRow{err: err}
	}
	return db.conn.QueryRow(ctx, query, bound...)
}

func (db *DB) execBound(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	bound, err := bindArguments(args)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	return db.conn.Exec(ctx, query, bound...)
}

type rejectedRow struct{ err error }

func (r rejectedRow) Scan(...any) error { return r.err }

// Keep the empty array non-nil while reusing its interface box across bindings.
var emptyDecimalListArgument any = []pgtype.Numeric{}

func bindArgument(value any) (any, bool, error) {
	switch value := value.(type) {
	case decimal.Decimal:
		bound, err := bindDecimal(value)
		return bound, true, err
	case *decimal.Decimal:
		bound, err := bindDecimalPointer(value)
		return bound, true, err
	case []decimal.Decimal:
		if len(value) == 0 && value != nil {
			return emptyDecimalListArgument, true, nil
		}
		bound, err := bindDecimalList(value, bindDecimal)
		return bound, true, err
	case []*decimal.Decimal:
		bound, err := bindDecimalList(value, bindDecimalPointer)
		return bound, true, err
	case *[]decimal.Decimal:
		if value == nil {
			return []pgtype.Numeric(nil), true, nil
		}
		return bindArgument(*value)
	case *[]*decimal.Decimal:
		if value == nil {
			return []pgtype.Numeric(nil), true, nil
		}
		return bindArgument(*value)
	case time.Duration, *time.Duration, []time.Duration, []*time.Duration, *[]time.Duration, *[]*time.Duration:
		return value, false, validateDurationArgument(value)
	case int64, string, bool, float64:
		return value, false, nil
	case pgx.QueryRewriter:
		return checkedQueryRewriter{source: value}, true, nil
	default:
		return value, false, nil
	}
}

func bindArguments(args []any) ([]any, error) {
	result := args
	copied := false
	for i, value := range args {
		bound, changed, err := bindArgument(value)
		if err != nil {
			return nil, err
		}
		if !changed {
			continue
		}
		if !copied {
			result = append([]any(nil), args...)
			copied = true
		}
		result[i] = bound
	}
	return result, nil
}

// checkedQueryRewriter validates parameters produced after SQL rewriting.
type checkedQueryRewriter struct{ source pgx.QueryRewriter }

func (r checkedQueryRewriter) RewriteQuery(ctx context.Context, connection *pgx.Conn, query string, args []any) (string, []any, error) {
	query, values, err := r.source.RewriteQuery(ctx, connection, query, args)
	if err != nil {
		return "", nil, err
	}
	values, err = bindArguments(values)
	if err != nil {
		return "", nil, err
	}
	return query, values, nil
}
