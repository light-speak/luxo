package pg

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/light-speak/luxo/pkg/lux"
	"github.com/light-speak/luxo/pkg/lux/errors"
)

// Query is a generic chainable query builder for PostgreSQL.
// Holds *pg.DB directly — zero interface, zero any.
type Query[T any] struct {
	db      *DB
	table   string
	fields  []string
	conds   []lux.Condition
	orderBy []string
	limit   int
	offset  int
	scan    ScanFunc[T]
}

// NewQuery creates a Query for the given table.
func NewQuery[T any](db *DB, table string, scan ScanFunc[T], conds []lux.Condition) *Query[T] {
	return &Query[T]{db: db, table: table, scan: scan, conds: conds}
}

// Where appends additional conditions to the query (chainable).
func (q *Query[T]) Where(conds ...lux.Condition) *Query[T] {
	q.conds = append(q.conds, conds...)
	return q
}

// Select sets the fields to retrieve.
func (q *Query[T]) Select(fields ...string) *Query[T] {
	q.fields = fields
	return q
}

// All returns all matching records.
func (q *Query[T]) All(ctx context.Context) ([]*T, error) {
	query, args := lux.BuildSelectSQL(q.table, q.fields, q.conds, q.orderBy, q.limit, q.offset)
	return q.db.QueryRows(ctx, q.scan, query, args...)
}

// AllWithCount returns matching records and total count.
// Pool queries run concurrently. A transaction owns one connection and must
// finish reading its rows before executing the count on that same connection.
func (q *Query[T]) AllWithCount(ctx context.Context) ([]*T, int64, error) {
	selectSQL, selectArgs := lux.BuildSelectSQL(q.table, q.fields, q.conds, q.orderBy, q.limit, q.offset)
	countSQL, countArgs := lux.BuildCountSQL(q.table, q.conds)
	switch q.db.conn.(type) {
	case *pgxpool.Pool:
		// Only explicitly pooled connections may execute independent queries.
	default:
		return q.allWithCountInTransaction(ctx, selectSQL, selectArgs, countSQL, countArgs)
	}
	return q.allWithCountFromPool(ctx, selectSQL, selectArgs, countSQL, countArgs)
}

func (q *Query[T]) allWithCountFromPool(ctx context.Context, selectSQL string, selectArgs []any, countSQL string, countArgs []any) ([]*T, int64, error) {
	type countResult struct {
		n   int64
		err error
	}
	ch := make(chan countResult, 1)
	go func() {
		n, err := q.db.QueryScalar[int64](ctx, countSQL, countArgs...)
		ch <- countResult{n, err}
	}()

	results, err := q.db.QueryRows(ctx, q.scan, selectSQL, selectArgs...)
	cr := <-ch
	if err != nil {
		return nil, 0, err
	}

	if cr.err != nil {
		return nil, 0, cr.err
	}

	return results, cr.n, nil
}

func (q *Query[T]) allWithCountInTransaction(ctx context.Context, selectSQL string, selectArgs []any, countSQL string, countArgs []any) ([]*T, int64, error) {
	results, err := q.db.QueryRows(ctx, q.scan, selectSQL, selectArgs...)
	if err != nil {
		return nil, 0, err
	}
	count, err := q.db.QueryScalar[int64](ctx, countSQL, countArgs...)
	if err != nil {
		return nil, 0, err
	}
	return results, count, nil
}

// First returns the first matching record, or nil.
func (q *Query[T]) First(ctx context.Context) (*T, error) {
	q.limit = 1
	query, args := lux.BuildSelectSQL(q.table, q.fields, q.conds, q.orderBy, q.limit, q.offset)
	return q.db.QueryRow(ctx, q.scan, query, args...)
}

// FirstRequired returns the first record, reporting NotFound only for a
// successful empty query. Database and scanner errors retain their identity.
func (q *Query[T]) FirstRequired(ctx context.Context) (*T, error) {
	value, err := q.First(ctx)
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, errors.NotFound
	}
	return value, nil
}

// Count returns the number of matching records.
func (q *Query[T]) Count(ctx context.Context) (int64, error) {
	query, args := lux.BuildCountSQL(q.table, q.conds)
	return q.db.QueryScalar[int64](ctx, query, args...)
}

// AggregateBatch executes compatible scalar aggregates in one database
// statement. Query conditions are shared; each spec may add its own filter.
func (q *Query[T]) AggregateBatch(ctx context.Context, specs ...lux.AggregateSpec) ([]int64, error) {
	query, args, err := buildAggregateBatchSQL(q.table, q.conds, specs)
	if err != nil {
		return nil, err
	}
	values := make([]int64, len(specs))
	destinations := make([]any, len(values))
	for index := range values {
		if specs[index].Function == lux.AggregateAvg {
			return nil, errInvalidScanValue
		}
		destinations[index] = &values[index]
	}
	if err := q.db.conn.QueryRow(ctx, query, args...).Scan(destinations...); err != nil {
		return nil, err
	}
	return values, nil
}

func buildAggregateBatchSQL(table string, common []lux.Condition, specs []lux.AggregateSpec) (string, []any, error) {
	if len(specs) == 0 {
		return "", nil, fmt.Errorf("pg: aggregate batch requires at least one result")
	}
	commonSQL, args := aggregateConditionsSQL(common, 1)
	var query strings.Builder
	query.WriteString("SELECT ")
	for index, spec := range specs {
		if index > 0 {
			query.WriteString(", ")
		}
		filterSQL, filterArgs := aggregateConditionsSQL(spec.Conditions, len(args)+1)
		expression, err := postgresAggregateExpression(spec, filterSQL)
		if err != nil {
			return "", nil, err
		}
		query.WriteString(expression)
		args = append(args, filterArgs...)
	}
	query.WriteString(" FROM ")
	query.WriteString(table)
	if commonSQL != "" {
		query.WriteString(" WHERE ")
		query.WriteString(commonSQL)
	}
	return query.String(), args, nil
}

func postgresAggregateExpression(spec lux.AggregateSpec, filter string) (string, error) {
	var expression string
	switch spec.Function {
	case lux.AggregateCount:
		if spec.Column != "" {
			return "", fmt.Errorf("pg: COUNT aggregate must not specify a column")
		}
		expression = "COUNT(*)"
	case lux.AggregateSum, lux.AggregateAvg, lux.AggregateMin, lux.AggregateMax:
		if spec.Column == "" {
			return "", fmt.Errorf("pg: %s aggregate requires a column", spec.Function)
		}
		expression = string(spec.Function) + "(" + spec.Column + ")"
	default:
		return "", fmt.Errorf("pg: unsupported aggregate function %q", spec.Function)
	}
	if filter != "" {
		expression += " FILTER (WHERE " + filter + ")"
	}
	if spec.Function != lux.AggregateCount {
		expression = "COALESCE(" + expression + ", 0)"
	}
	return expression, nil
}

func aggregateConditionsSQL(conditions []lux.Condition, argOffset int) (string, []any) {
	var query strings.Builder
	var args []any
	written := 0
	for _, condition := range conditions {
		if condition == nil {
			continue
		}
		fragment, values := condition.ToSQL(argOffset + len(args))
		if written > 0 {
			query.WriteString(" AND ")
		}
		query.WriteString(fragment)
		args = append(args, values...)
		written++
	}
	return query.String(), args
}

// Sum returns an Int sum and reports overflow or a non-integral result.
func (q *Query[T]) Sum(ctx context.Context, col string) (int64, error) {
	query, args := lux.BuildAggregateSQL(q.table, "SUM", col, q.conds)
	return q.db.QueryScalar[int64](ctx, query, args...)
}

// Avg returns a finite floating-point average without integer truncation.
func (q *Query[T]) Avg(ctx context.Context, col string) (float64, error) {
	return q.floatAggregate(ctx, "AVG", col)
}

// Max returns the maximum of an Int column without floating-point conversion.
func (q *Query[T]) Max(ctx context.Context, col string) (int64, error) {
	query, args := lux.BuildAggregateSQL(q.table, "MAX", col, q.conds)
	return q.db.QueryScalar[int64](ctx, query, args...)
}

// Min returns the minimum of an Int column without floating-point conversion.
func (q *Query[T]) Min(ctx context.Context, col string) (int64, error) {
	query, args := lux.BuildAggregateSQL(q.table, "MIN", col, q.conds)
	return q.db.QueryScalar[int64](ctx, query, args...)
}

// Exists returns true if any matching record exists.
// Uses SELECT EXISTS(SELECT 1 ... LIMIT 1) — short-circuits on first match.
func (q *Query[T]) Exists(ctx context.Context) (bool, error) {
	query, args := lux.BuildExistsSQL(q.table, q.conds)
	return q.db.QueryScalar[bool](ctx, query, args...)
}

// OrderBy adds ORDER BY clauses.
func (q *Query[T]) OrderBy(clauses ...string) *Query[T] {
	q.orderBy = append(q.orderBy, clauses...)
	return q
}

// Limit sets the maximum number of records.
func (q *Query[T]) Limit(n int) *Query[T] {
	q.limit = n
	return q
}

// Offset sets the number of records to skip.
func (q *Query[T]) Offset(n int) *Query[T] {
	q.offset = n
	return q
}

// GroupBy reads an explicit projection with a concrete result scanner.
// The query retains its current pool or transaction connection.
func (q *Query[T]) GroupBy[R any](ctx context.Context, groupCols []string, columns []lux.GroupAgg, scan func(Rows) (R, error), having ...lux.Condition) ([]R, error) {
	query, args := lux.BuildGroupBySQL(q.table, groupCols, columns, q.conds, q.orderBy, q.limit, q.offset, having...)
	return queryValues(ctx, q.db, scan, query, args...)
}

// Delete deletes all matching records.
func (q *Query[T]) Delete(ctx context.Context) (int64, error) {
	query, args := lux.BuildDeleteSQL(q.table, q.conds)
	return Exec(ctx, q.db, query, args...)
}

// SoftDelete marks matching records as deleted by setting deleted_at = NOW().
// Only affects records where deleted_at IS NULL.
func (q *Query[T]) SoftDelete(ctx context.Context) (int64, error) {
	q.conds = append(q.conds, lux.NewTimeField("deleted_at").IsNull())
	sets := []lux.SetField{{Col: "deleted_at", Val: time.Now()}}
	query, args := lux.BuildUpdateSQL(q.table, sets, q.conds)
	return Exec(ctx, q.db, query, args...)
}

// Update updates all matching records with the given fields.
func (q *Query[T]) Update(ctx context.Context, sets ...lux.SetField) (int64, error) {
	query, args := lux.BuildUpdateSQL(q.table, sets, q.conds)
	return Exec(ctx, q.db, query, args...)
}

// --- CreateBase[T] ---

// CreateBase provides the common create builder logic for PostgreSQL.
// Holds *pg.DB directly — zero any.
type CreateBase[T any] struct {
	Db    *DB
	Table string
	Cols  []string
	Vals  []any
	Scan  ScanFunc[T]
}

// Set appends a column=value pair. Called by generated Set methods.
func (b *CreateBase[T]) Set(col string, val any) {
	b.Cols = append(b.Cols, col)
	b.Vals = append(b.Vals, val)
}

// Exec inserts the record and returns it via RETURNING *.
func (b *CreateBase[T]) Exec(ctx context.Context) (*T, error) {
	return InsertReturning(ctx, b.Db, b.Scan, b.Table, b.Cols, b.Vals)
}

// --- UpdateBase[T] ---

// UpdateBase provides the common update builder logic for PostgreSQL.
// Two type params: T is the model, ID is the primary key type (int64 or uuid.UUID).
type UpdateBase[T any, ID comparable] struct {
	Db    *DB
	Table string
	// IDColumn is the schema-declared primary-key column. Empty defaults to "id".
	IDColumn string
	ID       ID
	Sets     []lux.SetField
	Scan     ScanFunc[T]
}

// Set appends a column=value pair. Called by generated Set methods.
func (b *UpdateBase[T, ID]) Set(col string, val any) {
	b.Sets = append(b.Sets, lux.SetField{Col: col, Val: val})
}

// Exec updates the record and returns it via RETURNING *.
func (b *UpdateBase[T, ID]) Exec(ctx context.Context) (*T, error) {
	return UpdateReturningBy(ctx, b.Db, b.Scan, b.Table, b.IDColumn, b.ID, b.Sets)
}
