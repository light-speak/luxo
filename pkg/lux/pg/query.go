package pg

import (
	"context"
	"time"

	"github.com/light-speak/luxo/pkg/lux"
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
	return QueryRows(ctx, q.db, q.scan, query, args...)
}

// AllWithCount returns matching records and total count.
// Runs select and count queries concurrently for lower latency.
func (q *Query[T]) AllWithCount(ctx context.Context) ([]*T, int64, error) {
	selectSQL, selectArgs := lux.BuildSelectSQL(q.table, q.fields, q.conds, q.orderBy, q.limit, q.offset)
	countSQL, countArgs := lux.BuildCountSQL(q.table, q.conds)

	type countResult struct {
		n   int64
		err error
	}
	ch := make(chan countResult, 1)
	go func() {
		n, err := QueryScalar[int64](ctx, q.db, countSQL, countArgs...)
		ch <- countResult{n, err}
	}()

	results, err := QueryRows(ctx, q.db, q.scan, selectSQL, selectArgs...)
	if err != nil {
		return nil, 0, err
	}

	cr := <-ch
	if cr.err != nil {
		return nil, 0, cr.err
	}

	return results, cr.n, nil
}

// First returns the first matching record, or nil.
func (q *Query[T]) First(ctx context.Context) (*T, error) {
	q.limit = 1
	query, args := lux.BuildSelectSQL(q.table, q.fields, q.conds, q.orderBy, q.limit, q.offset)
	return QueryRow(ctx, q.db, q.scan, query, args...)
}

// Count returns the number of matching records.
func (q *Query[T]) Count(ctx context.Context) (int64, error) {
	query, args := lux.BuildCountSQL(q.table, q.conds)
	return QueryScalar[int64](ctx, q.db, query, args...)
}

// Sum returns the SUM of the given column for matching records.
func (q *Query[T]) Sum(ctx context.Context, col string) (int64, error) {
	query, args := lux.BuildAggregateSQL(q.table, "SUM", col, q.conds)
	return QueryScalar[int64](ctx, q.db, query, args...)
}

// Avg returns the AVG of the given column for matching records (truncated to int64).
func (q *Query[T]) Avg(ctx context.Context, col string) (int64, error) {
	query, args := lux.BuildAggregateSQL(q.table, "AVG", col, q.conds)
	return QueryScalar[int64](ctx, q.db, query, args...)
}

// Max returns the MAX of the given column for matching records.
func (q *Query[T]) Max(ctx context.Context, col string) (int64, error) {
	query, args := lux.BuildAggregateSQL(q.table, "MAX", col, q.conds)
	return QueryScalar[int64](ctx, q.db, query, args...)
}

// Min returns the MIN of the given column for matching records.
func (q *Query[T]) Min(ctx context.Context, col string) (int64, error) {
	query, args := lux.BuildAggregateSQL(q.table, "MIN", col, q.conds)
	return QueryScalar[int64](ctx, q.db, query, args...)
}

// Exists returns true if any matching record exists.
// Uses SELECT EXISTS(SELECT 1 ... LIMIT 1) — short-circuits on first match.
func (q *Query[T]) Exists(ctx context.Context) (bool, error) {
	query, args := lux.BuildExistsSQL(q.table, q.conds)
	return QueryScalar[bool](ctx, q.db, query, args...)
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

// GroupBy executes a GROUP BY query with aggregations and returns rows as []map[string]any.
// Each map contains group column values + aggregation results.
func (q *Query[T]) GroupBy(ctx context.Context, groupCols []string, aggs []lux.GroupAgg) ([]map[string]any, error) {
	query, args := lux.BuildGroupBySQL(q.table, groupCols, aggs, q.conds, q.orderBy, q.limit)
	rows, err := q.db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []map[string]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		descs := rows.FieldDescriptions()
		m := make(map[string]any, len(descs))
		for i, desc := range descs {
			m[string(desc.Name)] = vals[i]
		}
		results = append(results, m)
	}
	return results, rows.Err()
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
	ID    ID
	Sets  []lux.SetField
	Scan  ScanFunc[T]
}

// Set appends a column=value pair. Called by generated Set methods.
func (b *UpdateBase[T, ID]) Set(col string, val any) {
	b.Sets = append(b.Sets, lux.SetField{Col: col, Val: val})
}

// Exec updates the record and returns it via RETURNING *.
func (b *UpdateBase[T, ID]) Exec(ctx context.Context) (*T, error) {
	return UpdateReturning(ctx, b.Db, b.Scan, b.Table, b.ID, b.Sets)
}
