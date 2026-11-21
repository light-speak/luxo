package pg

import (
	"context"
	"fmt"
	"testing"
)

// BenchmarkTransactionBoundary includes a real local PostgreSQL round trip.
// Nested measures a savepoint, not an independently committed transaction.
func BenchmarkTransactionBoundary(b *testing.B) {
	db := testDB(b)
	ctx := b.Context()
	for _, test := range []struct {
		name   string
		run    func(context.Context, func(*DB) error) error
		nested bool
	}{
		{"previous-pool-begin", db.previousPoolTransaction, false},
		{"current-connection-begin", db.Tx, false},
		{"nested-savepoint", db.Tx, true},
	} {
		b.Run(test.name, func(b *testing.B) {
			read := func(tx *DB) error { return transactionBenchmarkRead(ctx, tx) }
			operation := read
			if test.nested {
				operation = func(tx *DB) error {
					return tx.Tx(ctx, read)
				}
			}
			if err := test.run(ctx, operation); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := test.run(ctx, operation); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func transactionBenchmarkRead(ctx context.Context, tx *DB) error {
	value, err := tx.QueryScalar[int64](ctx, "SELECT 42::bigint")
	if err != nil {
		return err
	}
	if value != 42 {
		return fmt.Errorf("unexpected transaction response: %d", value)
	}
	return nil
}

// previousPoolTransaction preserves the old implementation for a non-nested
// comparison only. Its independent commits are not a valid nested baseline.
func (db *DB) previousPoolTransaction(ctx context.Context, fn func(*DB) error) (err error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pg: begin tx: %w", err)
	}
	txDB := &DB{pool: db.pool, conn: tx}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return
		}
		err = tx.Commit(ctx)
	}()
	return fn(txDB)
}
