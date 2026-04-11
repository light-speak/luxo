package migrate

import "context"

// DB is the minimal database interface for migration execution.
// PG and MySQL each provide their own implementation.
type DB interface {
	// Exec executes a SQL statement.
	Exec(ctx context.Context, sql string, args ...any) error

	// Query executes a query and returns rows.
	Query(ctx context.Context, sql string, args ...any) (Rows, error)

	// QueryRow executes a query that returns a single row.
	QueryRow(ctx context.Context, sql string, args ...any) Row

	// Begin starts a transaction.
	Begin(ctx context.Context) (Tx, error)

	// AcquireLock acquires a database-level advisory lock (prevents concurrent migration).
	AcquireLock(ctx context.Context) error

	// ReleaseLock releases the advisory lock.
	ReleaseLock(ctx context.Context)

	// Close closes the database connection.
	Close()
}

// Tx is a database transaction.
type Tx interface {
	Exec(ctx context.Context, sql string, args ...any) error
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Rows is an iterator over query results.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close()
}

// Row is a single query result row.
type Row interface {
	Scan(dest ...any) error
}
