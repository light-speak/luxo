package migrate

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pgLockID = 707813578

// PGDB implements DB for PostgreSQL using pgxpool.
type PGDB struct {
	pool *pgxpool.Pool
}

var _ DB = (*PGDB)(nil)

// NewPGDB creates a PGDB from a connection URL.
func NewPGDB(ctx context.Context, url string) (*PGDB, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &PGDB{pool: pool}, nil
}

func (d *PGDB) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := d.pool.Exec(ctx, sql, args...)
	return err
}

func (d *PGDB) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	rows, err := d.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &pgRows{rows}, nil
}

func (d *PGDB) QueryRow(ctx context.Context, sql string, args ...any) Row {
	return d.pool.QueryRow(ctx, sql, args...)
}

func (d *PGDB) Begin(ctx context.Context) (Tx, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &pgTx{tx}, nil
}

func (d *PGDB) AcquireLock(ctx context.Context) error {
	return d.Exec(ctx, "SELECT pg_advisory_lock($1)", pgLockID)
}

func (d *PGDB) ReleaseLock(ctx context.Context) {
	d.Exec(ctx, "SELECT pg_advisory_unlock($1)", pgLockID)
}

func (d *PGDB) Close() {
	d.pool.Close()
}

// pgRows wraps pgx.Rows to implement Rows.
type pgRows struct {
	rows pgx.Rows
}

func (r *pgRows) Next() bool             { return r.rows.Next() }
func (r *pgRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r *pgRows) Close()                 { r.rows.Close() }

// pgTx wraps pgx.Tx to implement Tx.
type pgTx struct {
	tx pgx.Tx
}

func (t *pgTx) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := t.tx.Exec(ctx, sql, args...)
	return err
}

func (t *pgTx) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t *pgTx) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }
