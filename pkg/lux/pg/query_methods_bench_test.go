package pg

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/light-speak/luxo/pkg/lux"
)

// These paired lowerings isolate query-local snapshots with stable input.
// They do not measure generated handlers, side effects, or database/network I/O.
func BenchmarkQuerySnapshotPreparation(b *testing.B) {
	for _, sample := range []struct {
		name    string
		prepare func(int64) *Query[int64]
	}{
		{"deferred", prepareDeferredQuery},
		{"captured", prepareCapturedQuery},
	} {
		b.Run(sample.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				query := sample.prepare(42)
				sql, args := lux.BuildSelectSQL(query.table, query.fields, query.conds, query.orderBy, query.limit, query.offset)
				if sql == "" || len(args) != 3 || args[0] != int64(42) || args[1] != int64(7) || query.limit != 8 {
					b.Fatal(sql, args)
				}
			}
		})
	}
}

//go:noinline
func prepareDeferredQuery(id int64) *Query[int64] {
	limit := snapshotLimit()
	return NewQuery[int64](nil, "records", nil, nil).
		Where(lux.AllOf(lux.NewIntField("id").Eq(id), lux.NewIntField("value").Eq(7))).Limit(limit)
}

//go:noinline
func prepareCapturedQuery(id int64) *Query[int64] {
	left := lux.NewIntField("id").Eq(id)
	right := lux.NewIntField("value").Eq(7)
	query := NewQuery[int64](nil, "records", nil, nil).Where(lux.AllOf(left, right))
	limit := snapshotLimit()
	return query.Limit(limit)
}

//go:noinline
func snapshotLimit() int { return 8 }

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

func BenchmarkBuilderUniqueAssignments(b *testing.B) {
	for _, count := range []int{1, 16, 128} {
		names := make([]string, count)
		for i := range names {
			names[i] = "field" + strconv.Itoa(i)
		}
		for _, sample := range []struct {
			name string
			set  func(*CreateBase[int64], string, any)
		}{
			{"legacy", func(value *CreateBase[int64], col string, v any) {
				value.Cols = append(value.Cols, col)
				value.Vals = append(value.Vals, v)
			}},
			{"rejected-scan", scanCreateAssignment},
		} {
			b.Run("create/"+strconv.Itoa(count)+"/"+sample.name, func(b *testing.B) {
				value := CreateBase[int64]{Cols: make([]string, 0, count), Vals: make([]any, 0, count)}
				b.ReportAllocs()
				for b.Loop() {
					value.Cols = value.Cols[:0]
					value.Vals = value.Vals[:0]
					for _, name := range names {
						sample.set(&value, name, int64(42))
					}
				}
				if len(value.Cols) != count || len(value.Vals) != count {
					b.Fatal("invalid assignment count")
				}
			})
		}
		for _, sample := range []struct {
			name string
			set  func(*UpdateBase[int64, int64], string, any)
		}{
			{"legacy", func(value *UpdateBase[int64, int64], col string, v any) {
				value.Sets = append(value.Sets, lux.SetField{Col: col, Val: v})
			}},
			{"rejected-scan", scanUpdateAssignment},
		} {
			b.Run("update/"+strconv.Itoa(count)+"/"+sample.name, func(b *testing.B) {
				value := UpdateBase[int64, int64]{Sets: make([]lux.SetField, 0, count)}
				b.ReportAllocs()
				for b.Loop() {
					value.Sets = value.Sets[:0]
					for _, name := range names {
						sample.set(&value, name, int64(42))
					}
				}
				if len(value.Sets) != count {
					b.Fatal("invalid assignment count")
				}
			})
		}
	}
}

// Rejected candidates retained only to reproduce their quadratic construction cost.
func scanCreateAssignment(value *CreateBase[int64], col string, v any) {
	for i, existing := range value.Cols {
		if existing == col {
			value.Vals[i] = v
			return
		}
	}
	value.Cols = append(value.Cols, col)
	value.Vals = append(value.Vals, v)
}

func scanUpdateAssignment(value *UpdateBase[int64, int64], col string, v any) {
	for i := range value.Sets {
		if value.Sets[i].Col == col {
			value.Sets[i] = lux.SetField{Col: col, Val: v}
			return
		}
	}
	value.Sets = append(value.Sets, lux.SetField{Col: col, Val: v})
}

// Frozen valid-column control before default-only SQL support.
func legacyInsertReturning[T any](ctx context.Context, db *DB, scan ScanFunc[T], table string, cols []string, vals []any) (*T, error) {
	var b strings.Builder
	var tmp [20]byte
	b.WriteString("INSERT INTO ")
	b.WriteString(table)
	b.WriteString(" (")
	b.WriteString(strings.Join(cols, ", "))
	b.WriteString(") VALUES (")
	for i := range cols {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('$')
		b.Write(strconv.AppendInt(tmp[:0], int64(i+1), 10))
	}
	b.WriteString(") RETURNING *")
	return QueryRow(ctx, db, scan, b.String(), vals...)
}

// Frozen valid-column control before default-only SQL support.
func legacyInsertManyReturning[T any](ctx context.Context, db *DB, scan ScanFunc[T], table string, cols []string, rows [][]any) ([]*T, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	var b strings.Builder
	var tmp [20]byte
	b.WriteString("INSERT INTO ")
	b.WriteString(table)
	b.WriteString(" (")
	b.WriteString(strings.Join(cols, ", "))
	b.WriteString(") VALUES ")

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
			b.WriteByte('$')
			b.Write(strconv.AppendInt(tmp[:0], int64(argIdx), 10))
			args = append(args, row[j])
			argIdx++
		}
		b.WriteByte(')')
	}
	b.WriteString(" RETURNING *")

	return QueryRows(ctx, db, scan, b.String(), args...)
}

// SQL construction and driver dispatch only; the fake driver returns one row.
// This does not simulate database execution or batch-result decoding.
func BenchmarkInsertDefaultBranchControl(b *testing.B) {
	for _, width := range []int{1, 16} {
		cols := make([]string, width)
		values := make([]any, width)
		for i := range cols {
			cols[i] = "field" + strconv.Itoa(i)
			values[i] = int64(i)
		}
		for _, sample := range []struct {
			name  string
			write func(context.Context, *DB, ScanFunc[int64], string, []string, []any) (*int64, error)
		}{
			{"legacy", legacyInsertReturning[int64]}, {"current", InsertReturning[int64]},
		} {
			b.Run("single/"+strconv.Itoa(width)+"/"+sample.name, func(b *testing.B) {
				db := &DB{conn: &queryMethodConn{rows: queryMethodRows{value: 42}}}
				ctx := b.Context()
				b.ReportAllocs()
				for b.Loop() {
					row, err := sample.write(ctx, db, scanMethodValue, "records", cols, values)
					if err != nil || row == nil || *row != 42 {
						b.Fatal(row, err)
					}
				}
			})
		}
		for _, count := range []int{1, 128} {
			rows := make([][]any, count)
			for i := range rows {
				rows[i] = values
			}
			for _, sample := range []struct {
				name  string
				write func(context.Context, *DB, ScanFunc[int64], string, []string, [][]any) ([]*int64, error)
			}{
				{"legacy", legacyInsertManyReturning[int64]}, {"current", InsertManyReturning[int64]},
			} {
				b.Run("batch/"+strconv.Itoa(width)+"x"+strconv.Itoa(count)+"/"+sample.name, func(b *testing.B) {
					db := &DB{conn: &queryMethodConn{rows: queryMethodRows{value: 42}}}
					ctx := b.Context()
					b.ReportAllocs()
					for b.Loop() {
						result, err := sample.write(ctx, db, scanMethodValue, "records", cols, rows)
						if err != nil || len(result) != 1 || *result[0] != 42 {
							b.Fatal(result, err)
						}
					}
				})
			}
		}
	}
}
