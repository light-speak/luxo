package pg

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// queryMethodConn isolates query dispatch from network and database latency.
type queryMethodConn struct {
	conn
	rows queryMethodRows
}

func (c *queryMethodConn) Query(context.Context, string, ...any) (pgx.Rows, error) {
	c.rows.position = 0
	return &c.rows, nil
}

func (c *queryMethodConn) QueryRow(context.Context, string, ...any) pgx.Row {
	return &c.rows
}

type queryMethodRows struct {
	pgx.Rows
	position int
	value    int64
}

func (r *queryMethodRows) Close()     {}
func (r *queryMethodRows) Err() error { return nil }
func (r *queryMethodRows) Next() bool {
	r.position++
	return r.position == 1
}

func (r *queryMethodRows) Scan(dest ...any) error {
	*dest[0].(*int64) = r.value
	return nil
}

func scanMethodValue(rows Rows) (*int64, error) {
	var value int64
	err := rows.Scan(&value)
	return &value, err
}

func BenchmarkQueryFirstContract(b *testing.B) {
	for _, required := range []bool{false, true} {
		name := "optional"
		if required {
			name = "required"
		}
		b.Run(name, func(b *testing.B) {
			db := &DB{conn: &queryMethodConn{rows: queryMethodRows{value: 42}}}
			query := NewQuery(db, "values", scanMethodValue, nil)
			read := query.First
			if required {
				read = query.FirstRequired
			}
			ctx := context.Background()
			b.ReportAllocs()
			for b.Loop() {
				value, err := read(ctx)
				if err != nil || value == nil || *value != 42 {
					b.Fatal(value, err)
				}
			}
		})
	}
}

func BenchmarkDBQueryDispatch(b *testing.B) {
	db := &DB{conn: &queryMethodConn{rows: queryMethodRows{value: 42}}}
	ctx := context.Background()
	b.Run("Rows", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rows, err := db.QueryRows(ctx, scanMethodValue, "SELECT value")
			if err != nil || len(rows) != 1 || *rows[0] != 42 {
				b.Fatal(rows, err)
			}
		}
	})
	b.Run("Row", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			row, err := db.QueryRow(ctx, scanMethodValue, "SELECT value")
			if err != nil || row == nil || *row != 42 {
				b.Fatal(row, err)
			}
		}
	})
	b.Run("Scalar", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			value, err := db.QueryScalar[int64](ctx, "SELECT value")
			if err != nil || value != 42 {
				b.Fatal(value, err)
			}
		}
	})
}

func BenchmarkFunctionQueryDispatch(b *testing.B) {
	db := &DB{conn: &queryMethodConn{rows: queryMethodRows{value: 42}}}
	ctx := context.Background()
	b.Run("Rows", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rows, err := queryRowsBeforeMethods(ctx, db, scanMethodValue, "SELECT value")
			if err != nil || len(rows) != 1 || *rows[0] != 42 {
				b.Fatal(rows, err)
			}
		}
	})
	b.Run("Row", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			row, err := queryRowBeforeMethods(ctx, db, scanMethodValue, "SELECT value")
			if err != nil || row == nil || *row != 42 {
				b.Fatal(row, err)
			}
		}
	})
	b.Run("Scalar", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			value, err := queryScalarBeforeMethods[int64](ctx, db, "SELECT value")
			if err != nil || value != 42 {
				b.Fatal(value, err)
			}
		}
	})
}

// Frozen pre-method implementations keep the same-toolchain comparison independent.
// They intentionally duplicate the old query loops only in benchmark code.
// QueryRows executes a SELECT and scans all rows using the provided scan function.
func queryRowsBeforeMethods[T any](ctx context.Context, db *DB, scan ScanFunc[T], query string, args ...any) ([]*T, error) {
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
func queryRowBeforeMethods[T any](ctx context.Context, db *DB, scan ScanFunc[T], query string, args ...any) (*T, error) {
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
func queryScalarBeforeMethods[T any](ctx context.Context, db *DB, query string, args ...any) (T, error) {
	var result T
	err := db.conn.QueryRow(ctx, query, args...).Scan(&result)
	return result, err
}
