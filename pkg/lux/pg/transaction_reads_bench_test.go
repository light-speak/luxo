package pg

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/light-speak/luxo/pkg/lux"
)

// Frozen pre-fix code is a comparison only for pool-bound successful queries.
// It is not a valid transaction implementation and leaks work on select errors.
func allWithCountBeforeTransactionFix[T any](q *Query[T], ctx context.Context) ([]*T, int64, error) {
	selectSQL, selectArgs := lux.BuildSelectSQL(q.table, q.fields, q.conds, q.orderBy, q.limit, q.offset)
	countSQL, countArgs := lux.BuildCountSQL(q.table, q.conds)
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
	if err != nil {
		return nil, 0, err
	}
	cr := <-ch
	if cr.err != nil {
		return nil, 0, cr.err
	}
	return results, cr.n, nil
}

// Compare the changed result representation with identical SQL and row data.
// The fake driver excludes PostgreSQL/network time; integration tests cover those.
func BenchmarkGroupByProjection(b *testing.B) {
	for _, size := range []struct {
		name string
		rows int
	}{{"one", 1}, {"128", 128}} {
		for _, typed := range []bool{false, true} {
			name := "map"
			if typed {
				name = "typed"
			}
			b.Run(size.name+"/"+name, func(b *testing.B) {
				connection := &groupBenchmarkConn{count: size.rows}
				q := NewQuery(&DB{conn: connection}, "groups", scanMethodValue, nil)
				columns := groupCountColumns()
				keys := []string{"name"}
				b.ReportAllocs()
				for b.Loop() {
					if typed {
						values, err := q.GroupBy(b.Context(), keys, columns, scanGroupCountValue)
						if err != nil || len(values) != size.rows || values[0].Total != 1 {
							b.Fatal(values, err)
						}
					} else {
						values, err := groupByDynamicReference(q, b.Context(), keys, columns)
						if err != nil || len(values) != size.rows || values[0]["total"] != int64(1) {
							b.Fatal(values, err)
						}
					}
				}
			})
		}
	}
}

func scanGroupCountValue(rows Rows) (groupCount, error) {
	var value groupCount
	err := rows.Scan(&value.Name, &value.Total)
	return value, err
}

// Existing model-pointer queries share the row loop with typed projections.
// Keep a control against the exact pre-extraction loop, not the dynamic map case.
func BenchmarkQueryRowsProjectionControl(b *testing.B) {
	for _, size := range []struct {
		name string
		rows int
	}{{"one", 1}, {"128", 128}} {
		for _, current := range []bool{false, true} {
			name := "before"
			if current {
				name = "current"
			}
			b.Run(size.name+"/"+name, func(b *testing.B) {
				db := &DB{conn: &groupBenchmarkConn{count: size.rows}}
				b.ReportAllocs()
				for b.Loop() {
					var rows []*groupCount
					var err error
					if current {
						rows, err = QueryRows(b.Context(), db, scanGroupCount, "SELECT name,total FROM groups")
					} else {
						rows, err = queryRowsBeforeTypedGroups(b.Context(), db, scanGroupCount, "SELECT name,total FROM groups")
					}
					if err != nil || len(rows) != size.rows || rows[0].Total != 1 {
						b.Fatal(rows, err)
					}
				}
			})
		}
	}
}

func queryRowsBeforeTypedGroups[T any](ctx context.Context, db *DB, scan ScanFunc[T], query string, args ...any) ([]*T, error) {
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

type groupBenchmarkConn struct {
	conn
	count int
	rows  groupBenchmarkRows
}

func (c *groupBenchmarkConn) Query(context.Context, string, ...any) (pgx.Rows, error) {
	c.rows.left = c.count
	return &c.rows, nil
}

type groupBenchmarkRows struct{ methodTestRows }

func (r *groupBenchmarkRows) Scan(values ...any) error {
	*values[0].(*string) = "group"
	*values[1].(*int64) = 1
	return nil
}
func (r *groupBenchmarkRows) Values() ([]any, error) { return []any{"group", int64(1)}, nil }
func (r *groupBenchmarkRows) FieldDescriptions() []pgconn.FieldDescription {
	return groupBenchmarkDescriptions[:]
}

var groupBenchmarkDescriptions = [...]pgconn.FieldDescription{{Name: "name"}, {Name: "total"}}

// Frozen dynamic result loop, deliberately kept out of production code.
func groupByDynamicReference[T any](q *Query[T], ctx context.Context, keys []string, columns []lux.GroupAgg) ([]map[string]any, error) {
	query, args := lux.BuildGroupBySQL(q.table, keys, columns, q.conds, q.orderBy, q.limit, q.offset)
	rows, err := q.db.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []map[string]any
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		fields := rows.FieldDescriptions()
		value := make(map[string]any, len(fields))
		for index, field := range fields {
			value[field.Name] = values[index]
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

// Frozen direct-pool implementation; never use as a transaction baseline.
func groupByBeforeTransactionFix[T any](q *Query[T], ctx context.Context, cols []string, aggs []lux.GroupAgg) ([]map[string]any, error) {
	query, args := lux.BuildGroupBySQL(q.table, cols, aggs, q.conds, q.orderBy, q.limit, 0)
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

// This benchmark excludes concrete pool dispatch; use the database benchmark
// for the complete method. Historical Outbox-wrapper results are not equivalent.
func BenchmarkPoolReadExecution(b *testing.B) {
	db := &DB{conn: &queryMethodConn{rows: queryMethodRows{value: 42}}}
	q := NewQuery(db, "values", scanMethodValue, nil)
	for _, current := range transactionReadBenchmarkOrder() {
		name := "before"
		run := func(ctx context.Context) ([]*int64, int64, error) { return allWithCountBeforeTransactionFix(q, ctx) }
		if current {
			name, run = "current", func(ctx context.Context) ([]*int64, int64, error) { return allWithCountPoolFixture(ctx, q) }
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				rows, count, err := run(b.Context())
				if err != nil || len(rows) != 1 || count != 42 || *rows[0] != 42 {
					b.Fatal(rows, count, err)
				}
			}
		})
	}
}

func BenchmarkTransactionReadDatabase(b *testing.B) {
	db := testDB(b)
	if err := insertTransactionUser(b.Context(), db, "read-benchmark"); err != nil {
		b.Fatal(err)
	}
	q := NewQuery(db, "users", scanUser, nil)
	for _, current := range transactionReadBenchmarkOrder() {
		name := "before"
		if current {
			name = "current"
		}
		b.Run("pool/all-with-count/"+name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var rows []*testUser
				var count int64
				var err error
				if current {
					rows, count, err = q.AllWithCount(b.Context())
				} else {
					rows, count, err = allWithCountBeforeTransactionFix(q, b.Context())
				}
				if err != nil || len(rows) != 1 || count != 1 {
					b.Fatal(rows, count, err)
				}
			}
		})
		b.Run("pool/group-by/"+name, func(b *testing.B) { benchmarkPoolGroupBy(b, q, current) })
	}
	for _, group := range []bool{false, true} {
		name := "transaction/all-with-count"
		if group {
			name = "transaction/group-by"
		}
		b.Run(name, func(b *testing.B) { benchmarkTransactionalRead(b, db, group) })
	}
}

func transactionReadBenchmarkOrder() []bool {
	order := []bool{false, true}
	if os.Getenv("LUXO_PG_BENCH_REVERSE") == "1" {
		slices.Reverse(order)
	}
	return order
}

func benchmarkPoolGroupBy(b *testing.B, q *Query[testUser], current bool) {
	cols, aggs := []string{"name"}, groupCountColumns()
	b.ReportAllocs()
	for b.Loop() {
		if current {
			rows, err := q.GroupBy(b.Context(), cols, aggs, scanGroupCount)
			if err != nil || len(rows) != 1 || rows[0].Name != "read-benchmark" || rows[0].Total != 1 {
				b.Fatal(rows, err)
			}
		} else {
			rows, err := groupByBeforeTransactionFix(q, b.Context(), cols, aggs)
			if err != nil || len(rows) != 1 || rows[0]["name"] != "read-benchmark" || rows[0]["total"] != int64(1) {
				b.Fatal(rows, err)
			}
		}
	}
}

func benchmarkTransactionalRead(b *testing.B, db *DB, group bool) {
	operation := func(tx *DB) error {
		q := NewQuery(tx, "users", scanUser, nil)
		if group {
			rows, err := q.GroupBy(b.Context(), []string{"name"}, groupCountColumns(), scanGroupCount)
			if err == nil && (len(rows) != 1 || rows[0].Total != 1) {
				b.Fatal(rows)
			}
			return err
		}
		rows, count, err := q.AllWithCount(b.Context())
		if err == nil && (len(rows) != 1 || count != 1) {
			b.Fatal(rows, count)
		}
		return err
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := db.Tx(b.Context(), operation); err != nil {
			b.Fatal(err)
		}
	}
}
