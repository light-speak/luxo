package pg

import (
	"context"

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

// Exists returns true if any matching record exists.
func (q *Query[T]) Exists(ctx context.Context) (bool, error) {
	count, err := q.Count(ctx)
	return count > 0, err
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

// Delete deletes all matching records.
func (q *Query[T]) Delete(ctx context.Context) (int64, error) {
	query, args := lux.BuildDeleteSQL(q.table, q.conds)
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
