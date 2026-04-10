package migrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Integration tests require a running PostgreSQL instance.
// Set DATABASE_* env vars or skip with: go test -short

func skipIfNoDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test (use -short=false)")
	}
	// Check if PG is reachable
	host := os.Getenv("DATABASE_HOST")
	if host == "" {
		host = "localhost"
	}
	user := os.Getenv("DATABASE_USER")
	if user == "" {
		t.Skip("DATABASE_USER not set, skipping integration test")
	}
}

func setupTestRunner(t *testing.T) (*Runner, string) {
	t.Helper()
	skipIfNoDB(t)

	dir := t.TempDir()
	ctx := context.Background()

	runner, err := New(ctx, dir)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Clean up test tables
	t.Cleanup(func() {
		runner.pool.Exec(context.Background(), "DROP TABLE IF EXISTS _migrations")
		runner.pool.Exec(context.Background(), "DROP TABLE IF EXISTS test_users")
		runner.pool.Exec(context.Background(), "DROP TABLE IF EXISTS test_posts")
		runner.Close()
	})

	return runner, dir
}

func writeMigration(t *testing.T, dir, name, up, down string) {
	t.Helper()
	content := "-- ====== UP ======\n\n" + up + "\n\n-- ====== DOWN ======\n\n" + down
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationInitCreatesTable(t *testing.T) {
	runner, _ := setupTestRunner(t)
	ctx := context.Background()

	err := runner.Init(ctx)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Verify _migrations table exists
	var count int64
	err = runner.pool.QueryRow(ctx, "SELECT count(*) FROM _migrations").Scan(&count)
	if err != nil {
		t.Fatalf("_migrations table should exist: %v", err)
	}
}

func TestIntegrationUpAndStatus(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := context.Background()

	// Create migration files
	writeMigration(t, dir, "001_create_users.sql",
		"CREATE TABLE test_users (id SERIAL PRIMARY KEY, name TEXT NOT NULL);",
		"DROP TABLE IF EXISTS test_users;")
	writeMigration(t, dir, "002_create_posts.sql",
		"CREATE TABLE test_posts (id SERIAL PRIMARY KEY, title TEXT NOT NULL, user_id INT NOT NULL);",
		"DROP TABLE IF EXISTS test_posts;")

	// Up
	executed, err := runner.Up(ctx)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if len(executed) != 2 {
		t.Errorf("expected 2 executed, got %d", len(executed))
	}

	// Status — all applied
	statuses, err := runner.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for _, s := range statuses {
		if !s.Applied {
			t.Errorf("%s should be applied", s.Name)
		}
	}

	// Up again — nothing to do
	executed2, err := runner.Up(ctx)
	if err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if len(executed2) != 0 {
		t.Errorf("second Up should execute 0, got %d", len(executed2))
	}

	// Verify tables exist
	var count int64
	runner.pool.QueryRow(ctx, "SELECT count(*) FROM test_users").Scan(&count)
	runner.pool.QueryRow(ctx, "SELECT count(*) FROM test_posts").Scan(&count)
}

func TestIntegrationDown(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := context.Background()

	writeMigration(t, dir, "001_create_users.sql",
		"CREATE TABLE test_users (id SERIAL PRIMARY KEY, name TEXT NOT NULL);",
		"DROP TABLE IF EXISTS test_users;")
	writeMigration(t, dir, "002_create_posts.sql",
		"CREATE TABLE test_posts (id SERIAL PRIMARY KEY, title TEXT NOT NULL);",
		"DROP TABLE IF EXISTS test_posts;")

	// Up all
	runner.Up(ctx)

	// Down 1
	rolledBack, err := runner.Down(ctx, 1)
	if err != nil {
		t.Fatalf("Down: %v", err)
	}
	if len(rolledBack) != 1 {
		t.Errorf("expected 1 rolled back, got %d", len(rolledBack))
	}
	if rolledBack[0] != "002_create_posts.sql" {
		t.Errorf("rolled back = %q", rolledBack[0])
	}

	// test_posts should be gone
	var exists bool
	runner.pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name='test_posts')").Scan(&exists)
	if exists {
		t.Error("test_posts should be dropped after Down")
	}

	// test_users should still exist
	runner.pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name='test_users')").Scan(&exists)
	if !exists {
		t.Error("test_users should still exist")
	}

	// Status — 001 applied, 002 pending
	statuses, _ := runner.Status(ctx)
	for _, s := range statuses {
		if s.Name == "001_create_users.sql" && !s.Applied {
			t.Error("001 should be applied")
		}
		if s.Name == "002_create_posts.sql" && s.Applied {
			t.Error("002 should be pending after rollback")
		}
	}
}

func TestIntegrationDownMultiple(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := context.Background()

	writeMigration(t, dir, "001_a.sql",
		"CREATE TABLE test_users (id SERIAL PRIMARY KEY);",
		"DROP TABLE IF EXISTS test_users;")
	writeMigration(t, dir, "002_b.sql",
		"CREATE TABLE test_posts (id SERIAL PRIMARY KEY);",
		"DROP TABLE IF EXISTS test_posts;")

	runner.Up(ctx)

	// Down 2
	rolledBack, err := runner.Down(ctx, 2)
	if err != nil {
		t.Fatalf("Down 2: %v", err)
	}
	if len(rolledBack) != 2 {
		t.Errorf("expected 2 rolled back, got %d", len(rolledBack))
	}

	// Both tables should be gone
	statuses, _ := runner.Status(ctx)
	for _, s := range statuses {
		if s.Applied {
			t.Errorf("%s should be pending after full rollback", s.Name)
		}
	}
}

func TestIntegrationDownZero(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := context.Background()

	writeMigration(t, dir, "001_a.sql",
		"CREATE TABLE test_users (id SERIAL PRIMARY KEY);",
		"DROP TABLE IF EXISTS test_users;")
	runner.Up(ctx)

	// Down 0 → defaults to 1
	rolledBack, err := runner.Down(ctx, 0)
	if err != nil {
		t.Fatalf("Down 0: %v", err)
	}
	if len(rolledBack) != 1 {
		t.Errorf("Down(0) should default to 1, got %d", len(rolledBack))
	}
}

func TestIntegrationUpBadSQL(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := context.Background()

	writeMigration(t, dir, "001_bad.sql",
		"THIS IS NOT VALID SQL;",
		"SELECT 1;")

	_, err := runner.Up(ctx)
	if err == nil {
		t.Fatal("expected error for bad SQL")
	}

	// _migrations should not have the bad migration
	statuses, _ := runner.Status(ctx)
	for _, s := range statuses {
		if s.Applied {
			t.Errorf("%s should NOT be applied after failure", s.Name)
		}
	}
}

func TestIntegrationDownNoPending(t *testing.T) {
	runner, _ := setupTestRunner(t)
	ctx := context.Background()

	// No migrations at all
	rolledBack, err := runner.Down(ctx, 1)
	if err != nil {
		t.Fatalf("Down with no migrations: %v", err)
	}
	if len(rolledBack) != 0 {
		t.Errorf("expected 0, got %d", len(rolledBack))
	}
}

func TestIntegrationStatusEmpty(t *testing.T) {
	runner, _ := setupTestRunner(t)
	ctx := context.Background()

	statuses, err := runner.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != 0 {
		t.Errorf("expected 0, got %d", len(statuses))
	}
}

func TestIntegrationVerifyWithState(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := context.Background()

	// Write .state.json to test loadExpectedTables
	stateJSON := `{"models":{"User":{"table":"test_users"}}}`
	os.WriteFile(filepath.Join(dir, ".state.json"), []byte(stateJSON), 0644)

	runner.Init(ctx)

	// test_users doesn't exist → missing table drift
	drifts, err := runner.Verify(ctx)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	hasMissing := false
	for _, d := range drifts {
		if d == "missing table: test_users (expected by schema)" {
			hasMissing = true
		}
	}
	if !hasMissing {
		t.Errorf("should detect missing table, drifts: %v", drifts)
	}
}

func TestIntegrationChecksumTamper(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := context.Background()

	writeMigration(t, dir, "001_create_users.sql",
		"CREATE TABLE test_users (id SERIAL PRIMARY KEY);",
		"DROP TABLE IF EXISTS test_users;")

	// Up — stores checksum
	runner.Up(ctx)

	// Tamper the migration file
	path := filepath.Join(dir, "001_create_users.sql")
	os.WriteFile(path, []byte("-- TAMPERED\n-- ====== UP ======\nSELECT 1;\n-- ====== DOWN ======\nSELECT 1;"), 0644)

	// Add a second migration
	writeMigration(t, dir, "002_add_col.sql",
		"ALTER TABLE test_users ADD COLUMN name TEXT;",
		"ALTER TABLE test_users DROP COLUMN name;")

	// Up should detect checksum mismatch
	_, err := runner.Up(ctx)
	if err == nil {
		t.Fatal("should detect tampered migration")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error should mention checksum, got: %v", err)
	}
}

func TestIntegrationDryRun(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := context.Background()

	writeMigration(t, dir, "001_create_users.sql",
		"CREATE TABLE test_users (id SERIAL PRIMARY KEY);",
		"DROP TABLE IF EXISTS test_users;")

	// DryRun should return SQL without executing
	names, sqls, err := runner.DryRun(ctx)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if len(names) != 1 || names[0] != "001_create_users.sql" {
		t.Errorf("names = %v", names)
	}
	if !strings.Contains(sqls[0], "CREATE TABLE test_users") {
		t.Errorf("sql should contain CREATE TABLE, got: %s", sqls[0])
	}

	// Table should NOT exist (dry run doesn't execute)
	var exists bool
	runner.pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name='test_users')").Scan(&exists)
	if exists {
		t.Error("DryRun should not create the table")
	}
}

func TestIntegrationConcurrently(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := context.Background()

	// First create the table
	writeMigration(t, dir, "001_create_users.sql",
		"CREATE TABLE test_users (id SERIAL PRIMARY KEY, name TEXT);",
		"DROP TABLE IF EXISTS test_users;")
	runner.Up(ctx)

	// Then add a CONCURRENTLY index
	writeMigration(t, dir, "002_add_index.sql",
		"CREATE INDEX CONCURRENTLY idx_test_users_name ON test_users (name);",
		"DROP INDEX IF EXISTS idx_test_users_name;")

	executed, err := runner.Up(ctx)
	if err != nil {
		t.Fatalf("Up with CONCURRENTLY: %v", err)
	}
	if len(executed) != 1 {
		t.Errorf("expected 1 executed, got %d", len(executed))
	}

	// Verify index exists
	var indexExists bool
	runner.pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE indexname='idx_test_users_name')").Scan(&indexExists)
	if !indexExists {
		t.Error("CONCURRENTLY index should exist")
	}
}

func TestIntegrationAdvisoryLock(t *testing.T) {
	runner, _ := setupTestRunner(t)
	ctx := context.Background()

	// Acquire lock
	err := runner.acquireLock(ctx)
	if err != nil {
		t.Fatalf("acquireLock: %v", err)
	}

	// Release lock
	runner.releaseLock(ctx)
	// Should not panic or error
}

func TestIntegrationVerify(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := context.Background()

	writeMigration(t, dir, "001_create_users.sql",
		"CREATE TABLE test_users (id SERIAL PRIMARY KEY);",
		"DROP TABLE IF EXISTS test_users;")

	// Before up — pending migration drift
	drifts, err := runner.Verify(ctx)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	hasPending := false
	for _, d := range drifts {
		if d == "pending migration: 001_create_users.sql" {
			hasPending = true
		}
	}
	if !hasPending {
		t.Error("should detect pending migration")
	}

	// After up — no pending
	runner.Up(ctx)
	drifts2, _ := runner.Verify(ctx)
	for _, d := range drifts2 {
		if d == "pending migration: 001_create_users.sql" {
			t.Error("should not have pending after Up")
		}
	}
}
