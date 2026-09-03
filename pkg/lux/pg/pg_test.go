package pg

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/light-speak/luxo/pkg/lux"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// testDB starts a PostgreSQL container and returns a connected *DB.
// The container is automatically cleaned up when the test ends.
func testDB(t *testing.T) *DB {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("luxo_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { container.Terminate(ctx) })

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	db, err := NewDB(ctx, connStr)
	if err != nil {
		t.Fatalf("connect to database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Create test table
	_, execErr := db.pool.Exec(ctx, `
		CREATE TABLE users (
			id UUID PRIMARY KEY,
			name TEXT NOT NULL,
			email TEXT NOT NULL UNIQUE,
			age INT NOT NULL DEFAULT 0,
			score NUMERIC,
			active BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if execErr != nil {
		t.Fatalf("create table: %v", execErr)
	}

	return db
}

// scanUser is a test scanner for the users table.
func scanUser(rows Rows) (*testUser, error) {
	var u testUser
	fds := rows.FieldDescriptions()
	dests := make([]any, len(fds))
	for i, fd := range fds {
		switch string(fd.Name) {
		case "id":
			dests[i] = &u.ID
		case "name":
			dests[i] = &u.Name
		case "email":
			dests[i] = &u.Email
		case "age":
			dests[i] = &u.Age
		case "score":
			dests[i] = &u.Score
		case "active":
			dests[i] = &u.Active
		case "created_at":
			dests[i] = &u.CreatedAt
		case "updated_at":
			dests[i] = &u.UpdatedAt
		default:
			dests[i] = new(any)
		}
	}
	return &u, rows.Scan(dests...)
}

type testUser struct {
	ID        uuid.UUID
	Name      string
	Email     string
	Age       int64
	Score     *float64
	Active    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

func TestNewDBAndClose(t *testing.T) {
	db := testDB(t)
	if db.Pool() == nil {
		t.Fatal("pool should not be nil")
	}
}

func TestNewDBFromPool(t *testing.T) {
	db := testDB(t)
	db2 := NewDBFromPool(db.Pool())
	if db2.Pool() == nil {
		t.Fatal("pool should not be nil")
	}
}

func TestNewDBInvalidConnStr(t *testing.T) {
	ctx := context.Background()
	// Completely invalid connection string format triggers parse error.
	_, err := NewDB(ctx, "not-a-valid-url://:::")
	if err == nil {
		t.Fatal("expected error for invalid connection string")
	}
}

func TestQueryRowsError(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	_, err := QueryRows(ctx, db, scanUser, "SELECT * FROM nonexistent_table")
	if err == nil {
		t.Fatal("expected error for nonexistent table")
	}
}

func TestQueryRowError(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	_, err := QueryRow(ctx, db, scanUser, "SELECT * FROM nonexistent_table")
	if err == nil {
		t.Fatal("expected error for nonexistent table")
	}
}

func TestExecError(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	_, err := Exec(ctx, db, "DELETE FROM nonexistent_table")
	if err == nil {
		t.Fatal("expected error for nonexistent table")
	}
}

func TestInsertAndQueryRow(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	id := uuid.Must(uuid.NewV7())
	user, err := InsertReturning(ctx, db, scanUser, "users",
		[]string{"id", "name", "email", "age"},
		[]any{id, "lin", "lin@test.com", int64(25)},
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if user.Name != "lin" {
		t.Errorf("name = %q, want lin", user.Name)
	}
	if user.ID != id {
		t.Errorf("id mismatch")
	}
}

func TestQueryRows(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	// Insert 3 users
	for i, name := range []string{"alice", "bob", "charlie"} {
		_, err := InsertReturning(ctx, db, scanUser, "users",
			[]string{"id", "name", "email"},
			[]any{uuid.Must(uuid.NewV7()), name, name + "@test.com"},
		)
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	users, err := QueryRows(ctx, db, scanUser, "SELECT * FROM users ORDER BY name")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("len = %d, want 3", len(users))
	}
	if users[0].Name != "alice" {
		t.Errorf("first = %q, want alice", users[0].Name)
	}
}

func TestQueryRowNotFound(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	user, err := QueryRow(ctx, db, scanUser,
		"SELECT * FROM users WHERE id = $1", uuid.Must(uuid.NewV7()))
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if user != nil {
		t.Errorf("expected nil for not found")
	}
}

func TestQueryScalar(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	// Insert 2 users
	for _, name := range []string{"a", "b"} {
		_, _ = InsertReturning(ctx, db, scanUser, "users",
			[]string{"id", "name", "email"},
			[]any{uuid.Must(uuid.NewV7()), name, name + "@test.com"},
		)
	}

	count, err := QueryScalar[int64](ctx, db, "SELECT COUNT(*) FROM users")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestQueryRaw(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	rows, err := QueryRaw(ctx, db, "SELECT $1::bigint", int64(42))
	if err != nil {
		t.Fatalf("query raw: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("expected one row")
	}
	var value int64
	if err := rows.Scan(&value); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if value != 42 {
		t.Fatalf("value = %d, want 42", value)
	}
}

func TestExec(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	id := uuid.Must(uuid.NewV7())
	_, _ = InsertReturning(ctx, db, scanUser, "users",
		[]string{"id", "name", "email"},
		[]any{id, "del", "del@test.com"},
	)

	affected, err := Exec(ctx, db, "DELETE FROM users WHERE id = $1", id)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if affected != 1 {
		t.Errorf("affected = %d, want 1", affected)
	}
}

func TestInsertManyReturning(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	users, err := InsertManyReturning(ctx, db, scanUser, "users",
		[]string{"id", "name", "email"},
		[][]any{
			{uuid.Must(uuid.NewV7()), "x", "x@test.com"},
			{uuid.Must(uuid.NewV7()), "y", "y@test.com"},
		},
	)
	if err != nil {
		t.Fatalf("insert many: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("len = %d, want 2", len(users))
	}
}

func TestInsertManyEmpty(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	users, err := InsertManyReturning(ctx, db, scanUser, "users",
		[]string{"id", "name", "email"},
		nil,
	)
	if err != nil {
		t.Fatalf("insert many empty: %v", err)
	}
	if users != nil {
		t.Errorf("expected nil for empty insert")
	}
}

func TestUpdateReturning(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	id := uuid.Must(uuid.NewV7())
	_, _ = InsertReturning(ctx, db, scanUser, "users",
		[]string{"id", "name", "email"},
		[]any{id, "old", "old@test.com"},
	)

	updated, err := UpdateReturning(ctx, db, scanUser, "users", id,
		[]lux.SetField{
			{Col: "name", Val: "new"},
			{Col: "email", Val: "new@test.com"},
		},
	)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "new" {
		t.Errorf("name = %q, want new", updated.Name)
	}
}

func TestQueryGeneric(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	// Insert a user
	id := uuid.Must(uuid.NewV7())
	_, _ = InsertReturning(ctx, db, scanUser, "users",
		[]string{"id", "name", "email", "age"},
		[]any{id, "test", "test@test.com", int64(30)},
	)

	// Test Query[T] via NewQuery
	q := NewQuery(db, "users", scanUser, []lux.Condition{
		lux.NewUUIDField("id").Eq(id),
	})

	user, err := q.First(ctx)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if user == nil {
		t.Fatal("expected user, got nil")
	}
	if user.Name != "test" {
		t.Errorf("name = %q", user.Name)
	}
}

func TestQueryAll(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	for _, name := range []string{"a", "b", "c"} {
		_, _ = InsertReturning(ctx, db, scanUser, "users",
			[]string{"id", "name", "email"},
			[]any{uuid.Must(uuid.NewV7()), name, name + "@test.com"},
		)
	}

	q := NewQuery[testUser](db, "users", scanUser, nil)
	users, err := q.OrderBy("name ASC").All(ctx)
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(users) != 3 {
		t.Errorf("len = %d, want 3", len(users))
	}
}

func TestQuerySelectFields(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	_, _ = InsertReturning(ctx, db, scanUser, "users",
		[]string{"id", "name", "email"},
		[]any{uuid.Must(uuid.NewV7()), "sel", "sel@test.com"},
	)

	// Select only name — should still work (scanner handles partial fields)
	q := NewQuery[testUser](db, "users", scanUser, nil)
	users, err := q.Select("name").All(ctx)
	if err != nil {
		t.Fatalf("select fields: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("len = %d, want 1", len(users))
	}
	if users[0].Name != "sel" {
		t.Errorf("name = %q, want sel", users[0].Name)
	}
}

func TestQueryCountAndExists(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	q := NewQuery[testUser](db, "users", scanUser, nil)
	count, err := q.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	exists, err := q.Exists(ctx)
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if exists {
		t.Error("should not exist")
	}
}

func TestQueryLimitOffset(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	for _, name := range []string{"a", "b", "c", "d", "e"} {
		_, _ = InsertReturning(ctx, db, scanUser, "users",
			[]string{"id", "name", "email"},
			[]any{uuid.Must(uuid.NewV7()), name, name + "@test.com"},
		)
	}

	q := NewQuery[testUser](db, "users", scanUser, nil)
	users, err := q.OrderBy("name ASC").Limit(2).Offset(1).All(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("len = %d, want 2", len(users))
	}
	if users[0].Name != "b" {
		t.Errorf("first = %q, want b", users[0].Name)
	}
}

func TestQueryDelete(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	_, _ = InsertReturning(ctx, db, scanUser, "users",
		[]string{"id", "name", "email"},
		[]any{uuid.Must(uuid.NewV7()), "del", "del@test.com"},
	)

	q := NewQuery[testUser](db, "users", scanUser, []lux.Condition{
		lux.NewStringField("name").Eq("del"),
	})
	affected, err := q.Delete(ctx)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if affected != 1 {
		t.Errorf("affected = %d", affected)
	}
}

func TestQueryUpdate(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	_, _ = InsertReturning(ctx, db, scanUser, "users",
		[]string{"id", "name", "email"},
		[]any{uuid.Must(uuid.NewV7()), "upd", "upd@test.com"},
	)

	q := NewQuery[testUser](db, "users", scanUser, []lux.Condition{
		lux.NewStringField("name").Eq("upd"),
	})
	affected, err := q.Update(ctx, lux.SetField{Col: "name", Val: "updated"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if affected != 1 {
		t.Errorf("affected = %d", affected)
	}
}

func TestCreateBaseAndUpdateBase(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	// CreateBase
	cb := &CreateBase[testUser]{
		Db:    db,
		Table: "users",
		Scan:  scanUser,
	}
	id := uuid.Must(uuid.NewV7())
	cb.Set("id", id)
	cb.Set("name", "create-base")
	cb.Set("email", "cb@test.com")

	user, err := cb.Exec(ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if user.Name != "create-base" {
		t.Errorf("name = %q", user.Name)
	}

	// UpdateBase
	ub := &UpdateBase[testUser, uuid.UUID]{
		Db:    db,
		Table: "users",
		ID:    id,
		Scan:  scanUser,
	}
	ub.Set("name", "update-base")

	updated, err := ub.Exec(ctx)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "update-base" {
		t.Errorf("name = %q", updated.Name)
	}
}

func TestTxCommit(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	err := db.Tx(ctx, func(tx *DB) error {
		_, err := InsertReturning(ctx, tx, scanUser, "users",
			[]string{"id", "name", "email"},
			[]any{uuid.Must(uuid.NewV7()), "tx-user", "tx@test.com"},
		)
		return err
	})
	if err != nil {
		t.Fatalf("tx commit: %v", err)
	}

	// Verify committed
	count, _ := QueryScalar[int64](ctx, db, "SELECT COUNT(*) FROM users WHERE name = 'tx-user'")
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestTxRollback(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	testErr := errors.New("rollback me")
	err := db.Tx(ctx, func(tx *DB) error {
		_, _ = InsertReturning(ctx, tx, scanUser, "users",
			[]string{"id", "name", "email"},
			[]any{uuid.Must(uuid.NewV7()), "rollback-user", "rb@test.com"},
		)
		return testErr
	})
	if !errors.Is(err, testErr) {
		t.Fatalf("expected testErr, got %v", err)
	}

	// Verify rolled back
	count, _ := QueryScalar[int64](ctx, db, "SELECT COUNT(*) FROM users WHERE name = 'rollback-user'")
	if count != 0 {
		t.Errorf("count = %d, want 0 (should be rolled back)", count)
	}
}

func TestTxPanicRollback(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	defer func() {
		p := recover()
		if p == nil {
			t.Fatal("expected panic")
		}
		if p != "boom" {
			t.Fatalf("panic = %v, want boom", p)
		}
		// Verify rolled back
		count, _ := QueryScalar[int64](ctx, db, "SELECT COUNT(*) FROM users WHERE name = 'panic-user'")
		if count != 0 {
			t.Errorf("count = %d, want 0 (should be rolled back)", count)
		}
	}()

	_ = db.Tx(ctx, func(tx *DB) error {
		_, _ = InsertReturning(ctx, tx, scanUser, "users",
			[]string{"id", "name", "email"},
			[]any{uuid.Must(uuid.NewV7()), "panic-user", "panic@test.com"},
		)
		panic("boom")
	})
}

func TestTxBeginError(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	// Close the pool to force Begin to fail
	db.Close()

	err := db.Tx(ctx, func(tx *DB) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error when pool is closed")
	}
}

func TestTxNestedQuery(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	// Insert outside tx
	id := uuid.Must(uuid.NewV7())
	_, _ = InsertReturning(ctx, db, scanUser, "users",
		[]string{"id", "name", "email"},
		[]any{id, "existing", "exist@test.com"},
	)

	err := db.Tx(ctx, func(tx *DB) error {
		// Query inside tx should see existing data
		q := NewQuery(tx, "users", scanUser, []lux.Condition{
			lux.NewUUIDField("id").Eq(id),
		})
		user, err := q.First(ctx)
		if err != nil {
			return err
		}
		if user == nil {
			t.Error("should see existing user in tx")
		}

		// Insert + query within same tx
		newID := uuid.Must(uuid.NewV7())
		_, err = InsertReturning(ctx, tx, scanUser, "users",
			[]string{"id", "name", "email"},
			[]any{newID, "in-tx", "intx@test.com"},
		)
		if err != nil {
			return err
		}

		count, err := QueryScalar[int64](ctx, tx, "SELECT COUNT(*) FROM users")
		if err != nil {
			return err
		}
		if count != 2 {
			t.Errorf("count in tx = %d, want 2", count)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
}

func TestQueryAllWithCount(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	for _, name := range []string{"x", "y", "z"} {
		_, _ = InsertReturning(ctx, db, scanUser, "users",
			[]string{"id", "name", "email"},
			[]any{uuid.Must(uuid.NewV7()), name, name + "_awc@test.com"},
		)
	}

	q := NewQuery[testUser](db, "users", scanUser, nil)
	results, total, err := q.Limit(2).AllWithCount(ctx)
	if err != nil {
		t.Fatalf("AllWithCount: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("results = %d, want 2", len(results))
	}
	if total < 3 {
		t.Errorf("total = %d, want >= 3", total)
	}
}

// ===== pgxTracer unit tests (pure — no DB required) =====

func TestTraceQueryEndMissingContextKey(t *testing.T) {
	// TraceQueryEnd should return early without panic when the context
	// does not contain the traceStartKey value (type assertion fails, ok=false).
	tr := &pgxTracer{}
	ctx := context.Background() // no traceStartData stored
	// Must not panic.
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
}

func TestTraceQueryStartStoresData(t *testing.T) {
	tr := &pgxTracer{}
	ctx := context.Background()
	startData := pgx.TraceQueryStartData{
		SQL:  "SELECT 1",
		Args: []any{42},
	}
	ctx2 := tr.TraceQueryStart(ctx, nil, startData)
	sd, ok := ctx2.Value(traceStartKey{}).(traceStartData)
	if !ok {
		t.Fatal("expected traceStartData to be stored in context")
	}
	if sd.sql != "SELECT 1" {
		t.Errorf("sql = %q, want SELECT 1", sd.sql)
	}
	if len(sd.args) != 1 || sd.args[0] != 42 {
		t.Errorf("args = %v", sd.args)
	}
}

func TestQueryGroupBy(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	// Insert test data
	for _, d := range []struct {
		name, email string
		age         int64
	}{
		{"Alice", "alice@t.com", 25},
		{"Bob", "bob@t.com", 30},
		{"Alice", "alice2@t.com", 35},
	} {
		_, _ = InsertReturning(ctx, db, scanUser, "users",
			[]string{"id", "name", "email", "age"},
			[]any{uuid.Must(uuid.NewV7()), d.name, d.email, d.age},
		)
	}

	q := NewQuery[testUser](db, "users", scanUser, nil)
	results, err := q.GroupBy(ctx, []string{"name"}, []lux.GroupAgg{
		{Fn: "COUNT", Alias: "cnt"},
		{Fn: "SUM", Col: "age", Alias: "total_age"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 groups, got %d", len(results))
	}
	for _, r := range results {
		if r["name"] == "Alice" {
			cnt, _ := r["cnt"].(int64)
			if cnt != 2 {
				t.Errorf("Alice count = %d, want 2", cnt)
			}
		}
	}
}

func TestQuerySumAvgMinMax(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	for _, age := range []int64{10, 20, 30} {
		_, _ = InsertReturning(ctx, db, scanUser, "users",
			[]string{"id", "name", "email", "age"},
			[]any{uuid.Must(uuid.NewV7()), "u", fmt.Sprintf("u%d@t.com", age), age},
		)
	}

	q := NewQuery[testUser](db, "users", scanUser, nil)

	sum, err := q.Sum(ctx, "age")
	if err != nil {
		t.Fatal(err)
	}
	if sum != 60 {
		t.Errorf("Sum = %d, want 60", sum)
	}

	avg, err := q.Avg(ctx, "age")
	if err != nil {
		t.Fatal(err)
	}
	if avg != 20 {
		t.Errorf("Avg = %d, want 20", avg)
	}

	max, err := q.Max(ctx, "age")
	if err != nil {
		t.Fatal(err)
	}
	if max != 30 {
		t.Errorf("Max = %d, want 30", max)
	}

	min, err := q.Min(ctx, "age")
	if err != nil {
		t.Fatal(err)
	}
	if min != 10 {
		t.Errorf("Min = %d, want 10", min)
	}
}

func TestQueryWhere(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	_, _ = InsertReturning(ctx, db, scanUser, "users",
		[]string{"id", "name", "email", "age"},
		[]any{uuid.Must(uuid.NewV7()), "alice", "a@t.com", int64(25)},
	)
	_, _ = InsertReturning(ctx, db, scanUser, "users",
		[]string{"id", "name", "email", "age"},
		[]any{uuid.Must(uuid.NewV7()), "bob", "b@t.com", int64(30)},
	)

	q := NewQuery[testUser](db, "users", scanUser, nil).Where(lux.NewStringField("name").Eq("alice"))
	results, err := q.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Name != "alice" {
		t.Errorf("Where filter failed: got %d results", len(results))
	}
}
