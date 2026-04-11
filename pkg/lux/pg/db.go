// Package pg provides the PostgreSQL runtime for Luxo-generated code.
// It wraps pgx/v5 for zero-abstraction database access.
package pg

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/light-speak/luxo/pkg/lux"
)

// Rows is a type alias for pgx.Rows, so generated code does not import pgx directly.
type Rows = pgx.Rows

// ScanFunc scans the current row into a T. Generated per-model with zero reflection.
type ScanFunc[T any] func(rows Rows) (*T, error)

// conn is the internal interface satisfied by both *pgxpool.Pool and pgx.Tx.
// Not exported — generated code never sees this.
type conn interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// DB wraps a pgx connection pool or transaction.
type DB struct {
	pool *pgxpool.Pool
	conn conn // pool or tx — all queries go through this
}

// NewDB creates a new DB from a connection string.
//
//	db, err := pg.NewDB(ctx, "postgres://user:pass@localhost:5432/mydb")
func NewDB(ctx context.Context, connString string) (*DB, error) {
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("pg: parse config: %w", err)
	}
	config.MaxConns = 50
	config.MinConns = 10
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("pg: connect to database: %w", err)
	}
	return &DB{pool: pool, conn: pool}, nil
}

// NewDBFromPool creates a DB from an existing pgxpool.Pool.
func NewDBFromPool(pool *pgxpool.Pool) *DB {
	return &DB{pool: pool, conn: pool}
}

// Close closes the underlying connection pool.
func (db *DB) Close() {
	db.pool.Close()
}

// Pool returns the underlying pgxpool.Pool for advanced usage.
func (db *DB) Pool() *pgxpool.Pool {
	return db.pool
}

// Tx runs fn inside a database transaction. The *DB passed to fn uses the
// transaction for all queries. If fn returns nil, the transaction commits;
// otherwise it rolls back. Panics inside fn are recovered and cause a rollback.
func (db *DB) Tx(ctx context.Context, fn func(tx *DB) error) (err error) {
	pgxTx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pg: begin tx: %w", err)
	}

	txDB := &DB{pool: db.pool, conn: pgxTx}

	defer func() {
		if p := recover(); p != nil {
			_ = pgxTx.Rollback(ctx)
			panic(p) // re-panic after rollback
		}
		if err != nil {
			_ = pgxTx.Rollback(ctx)
			return
		}
		err = pgxTx.Commit(ctx)
	}()

	err = fn(txDB)
	return
}

// QueryRows executes a SELECT and scans all rows using the provided scan function.
func QueryRows[T any](ctx context.Context, db *DB, scan ScanFunc[T], query string, args ...any) ([]*T, error) {
	rows, err := db.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*T
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, item)
	}
	return results, rows.Err()
}

// QueryRow executes a SELECT and scans the first row, or returns nil.
func QueryRow[T any](ctx context.Context, db *DB, scan ScanFunc[T], query string, args ...any) (*T, error) {
	rows, err := db.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return scan(rows)
}

// QueryScalar executes a SELECT and scans a single scalar value.
func QueryScalar[T any](ctx context.Context, db *DB, query string, args ...any) (T, error) {
	var result T
	err := db.conn.QueryRow(ctx, query, args...).Scan(&result)
	return result, err
}

// Exec executes a statement and returns rows affected.
func Exec(ctx context.Context, db *DB, query string, args ...any) (int64, error) {
	tag, err := db.conn.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// InsertReturning inserts a row and scans the RETURNING * result.
func InsertReturning[T any](ctx context.Context, db *DB, scan ScanFunc[T], table string, cols []string, vals []any) (*T, error) {
	placeholders := make([]string, len(cols))
	for i := range cols {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	query := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) RETURNING *",
		table,
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "),
	)
	return QueryRow(ctx, db, scan, query, vals...)
}

// InsertManyReturning inserts multiple rows and scans all RETURNING * results.
func InsertManyReturning[T any](ctx context.Context, db *DB, scan ScanFunc[T], table string, cols []string, rows [][]any) ([]*T, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "INSERT INTO %s (%s) VALUES ", table, strings.Join(cols, ", "))

	args := make([]any, 0, len(cols)*len(rows))
	argIdx := 1
	for i, row := range rows {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('(')
		for j := range cols {
			if j > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "$%d", argIdx)
			args = append(args, row[j])
			argIdx++
		}
		b.WriteByte(')')
	}
	b.WriteString(" RETURNING *")

	return QueryRows(ctx, db, scan, b.String(), args...)
}

// UpdateReturning updates a single record by ID and scans the RETURNING * result.
func UpdateReturning[T any, ID comparable](ctx context.Context, db *DB, scan ScanFunc[T], table string, id ID, sets []lux.SetField) (*T, error) {
	var b strings.Builder
	args := make([]any, 0, len(sets)+1)
	argIdx := 1

	fmt.Fprintf(&b, "UPDATE %s SET ", table)
	for i, s := range sets {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s = $%d", s.Col, argIdx)
		args = append(args, s.Val)
		argIdx++
	}
	fmt.Fprintf(&b, " WHERE id = $%d RETURNING *", argIdx)
	args = append(args, id)

	return QueryRow(ctx, db, scan, b.String(), args...)
}
