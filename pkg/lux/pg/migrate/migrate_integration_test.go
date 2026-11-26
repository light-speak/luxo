package migrate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/light-speak/luxo/pkg/lux"
)

func TestEnsureDatabasePreservesName(t *testing.T) {
	ctx, admin := databaseTestAdmin(t)
	for _, suffix := range []string{"plain", `quoted"name`, `back\slash`, "東京", "semi;colon"} {
		t.Run(suffix, func(t *testing.T) {
			name := "luxo_ensure_" + strconv.FormatInt(time.Now().UnixNano(), 10) + "_" + suffix
			t.Setenv("DATABASE_PREFIX", name)
			cleanupTestDatabase(t, admin, name)
			for range 2 {
				if err := EnsureDatabase(ctx); err != nil {
					t.Fatal("create or reuse database", err)
				}
				var exists bool
				if err := admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil || !exists {
					t.Fatalf("database name changed: exists=%v err=%v", exists, err)
				}
			}
		})
	}
}

func TestEnsureDatabaseRejectsTruncatedName(t *testing.T) {
	ctx, admin := databaseTestAdmin(t)
	var limit int
	if err := admin.QueryRow(ctx, "SELECT current_setting('max_identifier_length')::int").Scan(&limit); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, suffix string }{
		{"ascii", strings.Repeat("x", limit)},
		{"unicode", strings.Repeat("東", limit)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix := "luxo_ensure_" + strconv.FormatInt(time.Now().UnixNano(), 10)
			t.Setenv("DATABASE_PREFIX", prefix+tc.suffix)
			if err := EnsureDatabase(ctx); err == nil {
				t.Fatal("oversized name was silently truncated into another database")
			}
			var count int
			if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_database WHERE starts_with(datname::text, $1)", prefix).Scan(&count); err != nil || count != 0 {
				t.Fatalf("invalid name created a database: count=%d err=%v", count, err)
			}
		})
	}
}

func TestEnsureDatabaseAcceptsIdentifierLimit(t *testing.T) {
	ctx, admin := databaseTestAdmin(t)
	var limit int
	if err := admin.QueryRow(ctx, "SELECT current_setting('max_identifier_length')::int").Scan(&limit); err != nil {
		t.Fatal(err)
	}
	name := "luxo_ensure_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	name += strings.Repeat("x", limit-len(name))
	t.Setenv("DATABASE_PREFIX", name)
	cleanupTestDatabase(t, admin, name)
	if err := EnsureDatabase(ctx); err != nil {
		t.Fatal(err)
	}
	if exists, err := databaseExists(ctx, admin, name); err != nil || !exists {
		t.Fatalf("maximum-length database missing: exists=%v err=%v", exists, err)
	}
}

func databaseTestAdmin(t *testing.T) (context.Context, *pgx.Conn) {
	t.Helper()
	skipIfNoDB(t)
	cfg := lux.DBConfigFromEnv()
	cfg.DBName = "postgres"
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	conn, err := pgx.Connect(ctx, cfg.ConnectionString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := conn.Close(closeCtx); err != nil {
			t.Error(err)
		}
	})
	return ctx, conn
}

func cleanupTestDatabase(t *testing.T, admin *pgx.Conn, name string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Error("cleanup database", err)
		}
	})
}

func TestEnsureDatabaseConcurrentCreation(t *testing.T) {
	ctx, admin := databaseTestAdmin(t)
	name := "luxo_ensure_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	t.Setenv("DATABASE_PREFIX", name)
	cleanupTestDatabase(t, admin, name)
	start := make(chan struct{})
	results := make(chan error, 8)
	for range cap(results) {
		go func() {
			<-start
			results <- EnsureDatabase(ctx)
		}()
	}
	close(start)
	for range cap(results) {
		if err := <-results; err != nil {
			t.Error("concurrent startup", err)
		}
	}
	if exists, err := databaseExists(ctx, admin, name); err != nil || !exists {
		t.Fatalf("exact database missing: exists=%v err=%v", exists, err)
	}
}

func TestEnsureDatabasePropagatesPermissionFailure(t *testing.T) {
	ctx, admin := databaseTestAdmin(t)
	role := "luxo_no_createdb_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN PASSWORD 'luxo_test_only' NOCREATEDB"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanupCtx, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Error(err)
		}
	})
	name := "luxo_ensure_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	cleanupTestDatabase(t, admin, name)
	t.Setenv("DATABASE_USER", role)
	t.Setenv("DATABASE_PASSWORD", "luxo_test_only")
	t.Setenv("DATABASE_PREFIX", name)
	err := EnsureDatabase(ctx)
	pgError, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgError.Code != "42501" {
		t.Fatalf("lost create permission failure: %v", err)
	}
	if exists, err := databaseExists(ctx, admin, name); err != nil || exists {
		t.Fatalf("denied creation changed catalog: exists=%v err=%v", exists, err)
	}
}

func TestDatabaseLookupRejectsInvalidNamesAndCancellation(t *testing.T) {
	ctx, admin := databaseTestAdmin(t)
	for _, name := range []string{"", "invalid\x00name", "invalid\xffname"} {
		if exists, err := databaseExists(ctx, admin, name); err == nil || exists {
			t.Errorf("accepted invalid name %q", name)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if exists, err := databaseExists(canceled, admin, "postgres"); err == nil || exists {
		t.Fatal("canceled catalog query succeeded")
	}
}

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
	// Skip if DATABASE_USER not set (CI without PG)
	if os.Getenv("DATABASE_USER") == "" {
		t.Skip("DATABASE_USER not set, skipping integration test")
	}
}

func setupTestRunner(t *testing.T) (*Runner, string) {
	t.Helper()
	skipIfNoDB(t)

	dir := t.TempDir()
	ctx := context.Background()

	db, err := NewPGDB(ctx, BuildDatabaseURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	runner := NewFromDB(db, dir)

	t.Cleanup(func() {
		db.Exec(context.Background(), "DROP TABLE IF EXISTS _migrations")
		db.Exec(context.Background(), "DROP TABLE IF EXISTS test_users")
		db.Exec(context.Background(), "DROP TABLE IF EXISTS test_posts")
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

func TestIntegrationMigrationOrdinalsAfterThreeDigits(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := t.Context()
	names := []string{"20261113_998_create.sql", "20261113_999_insert.sql", "20261113_1000_alter.sql"}
	writeMigration(t, dir, names[0], "CREATE TABLE test_users (id bigint PRIMARY KEY);", "DROP TABLE test_users;")
	writeMigration(t, dir, names[1], "INSERT INTO test_users VALUES (1);", "DELETE FROM test_users WHERE id=1;")
	writeMigration(t, dir, names[2], "ALTER TABLE test_users ADD COLUMN marker bigint DEFAULT 9; INSERT INTO test_users (id) VALUES (2);", "DELETE FROM test_users WHERE id=2; ALTER TABLE test_users DROP COLUMN marker;")
	assertMigrationOrdinalStatus(t, runner, names, false)
	dry, sql, err := runner.DryRun(ctx)
	if err != nil || strings.Join(dry, "|") != strings.Join(names, "|") || len(sql) != len(names) {
		t.Fatalf("dry-run order: %v %v %v", dry, sql, err)
	}
	up, err := runner.Up(ctx)
	if err != nil || strings.Join(up, "|") != strings.Join(names, "|") {
		t.Fatalf("execution order: %v %v", up, err)
	}
	assertMigrationOrdinalStatus(t, runner, names, true)
	writeMigration(t, dir, "20261113_1001_failure.sql", "INSERT INTO test_users (id) VALUES (3); SELECT luxo_missing_function();", "DELETE FROM test_users WHERE id=3;")
	if up, err := runner.Up(ctx); err == nil || len(up) != 0 {
		t.Fatalf("failed migration committed: %v %v", up, err)
	}
	var count, sum int64
	if err := runner.db.(*PGDB).pool.QueryRow(ctx, "SELECT count(*), sum(marker) FROM test_users").Scan(&count, &sum); err != nil || count != 2 || sum != 18 {
		t.Fatalf("failed SQL changed rows: %d %d %v", count, sum, err)
	}
	down, err := runner.Down(ctx, 1)
	if err != nil || len(down) != 1 || down[0] != names[2] {
		t.Fatalf("first rollback order: %v %v", down, err)
	}
	down, err = runner.Down(ctx, 2)
	if err != nil || strings.Join(down, "|") != names[1]+"|"+names[0] {
		t.Fatalf("remaining rollback order: %v %v", down, err)
	}
}

func assertMigrationOrdinalStatus(t *testing.T, runner *Runner, names []string, applied bool) {
	t.Helper()
	status, err := runner.Status(t.Context())
	if err != nil || len(status) != len(names) {
		t.Fatalf("migration status: %v %v", status, err)
	}
	for i, item := range status {
		if item.Name != names[i] || item.Applied != applied {
			t.Fatalf("migration status order: %v, applied=%v", status, applied)
		}
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
	err = runner.db.(*PGDB).pool.QueryRow(ctx, "SELECT count(*) FROM _migrations").Scan(&count)
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
	runner.db.(*PGDB).pool.QueryRow(ctx, "SELECT count(*) FROM test_users").Scan(&count)
	runner.db.(*PGDB).pool.QueryRow(ctx, "SELECT count(*) FROM test_posts").Scan(&count)
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
	runner.db.(*PGDB).pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name='test_posts')").Scan(&exists)
	if exists {
		t.Error("test_posts should be dropped after Down")
	}

	// test_users should still exist
	runner.db.(*PGDB).pool.QueryRow(ctx,
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
	assertMigrationRelations(t, runner, false, false)
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
	runner.db.(*PGDB).pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name='test_users')").Scan(&exists)
	if exists {
		t.Error("DryRun should not create the table")
	}
}

func TestIntegrationMigrationSectionMarkersInsideSQL(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := t.Context()
	const up = `CREATE TABLE test_users (label text);
		INSERT INTO test_users VALUES ($value$
-- ====== DOWN ======
first; second
$value$);
		SELECT 1; -- ====== DOWN ======`
	const down = "DROP TABLE test_users;"
	writeMigration(t, dir, "001_markers.sql", up, down)

	names, preview, err := runner.DryRun(ctx)
	if err != nil || len(names) != 1 || len(preview) != 1 || preview[0] != up {
		t.Errorf("preview changed SQL boundaries: %v %q %v", names, preview, err)
	}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := runner.db.QueryRow(ctx, "SELECT label FROM test_users").Scan(&stored); err != nil ||
		stored != "\n-- ====== DOWN ======\nfirst; second\n" {
		t.Fatalf("stored value: %q %v", stored, err)
	}
	if _, err := runner.Down(ctx, 1); err != nil {
		t.Fatal(err)
	}
	assertMigrationRelations(t, runner, true, false)
}

func TestIntegrationRollbackChecksumPreflightsEntireBatch(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := t.Context()
	const firstFile = "001_users.sql"
	const firstUp = "CREATE TABLE test_users (id int);"
	writeMigration(t, dir, firstFile, firstUp, "DROP TABLE test_users;")
	writeMigration(t, dir, "002_posts.sql", "CREATE TABLE test_posts (id int);", "DROP TABLE test_posts;")
	if applied, err := runner.Up(ctx); err != nil || len(applied) != 2 {
		t.Fatalf("setup: %v %v", applied, err)
	}

	writeMigration(t, dir, firstFile, firstUp, "DELETE FROM test_users;")
	rolled, err := runner.Down(ctx, 2)
	if len(rolled) != 0 || err == nil || !strings.Contains(err.Error(), firstFile) ||
		!strings.Contains(err.Error(), "DOWN") || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("rollback modified a checked batch: %v %v", rolled, err)
	}
	var history int
	var posts bool
	err = runner.db.QueryRow(ctx, "SELECT (SELECT count(*) FROM _migrations), to_regclass('test_posts') IS NOT NULL").Scan(&history, &posts)
	if err != nil || history != 2 || !posts {
		t.Fatalf("earlier rollback changed database: history=%d, posts=%t, err=%v", history, posts, err)
	}
}

func TestIntegrationRollbackLegacyChecksumHistory(t *testing.T) {
	for _, legacy := range []struct{ name, history string }{
		{"null checksum", "ALTER TABLE _migrations ADD COLUMN checksum text"},
		{"no checksum column", "SELECT 1"},
	} {
		t.Run(legacy.name, func(t *testing.T) {
			runner, dir := setupTestRunner(t)
			ctx := t.Context()
			const name = "001_legacy.sql"
			if err := runner.db.Exec(ctx, `CREATE TABLE _migrations (id serial PRIMARY KEY, name text);
				CREATE TABLE test_users (id int);
				INSERT INTO _migrations (name) VALUES ('001_legacy.sql');`+legacy.history); err != nil {
				t.Fatal(err)
			}
			writeMigration(t, dir, name, "CREATE TABLE test_users (id int);", "DROP TABLE test_users;")
			rolled, err := runner.Down(ctx, 1)
			if err != nil || len(rolled) != 1 || rolled[0] != name {
				t.Fatalf("legacy rollback: %v %v", rolled, err)
			}
			assertMigrationRelations(t, runner, true, false)
		})
	}
}

func TestIntegrationAppliedHistoryTimestampMustRemainReadable(t *testing.T) {
	for _, sample := range []struct{ name, column, data string }{
		{"missing", "", ""},
		{"null", ", applied_at TIMESTAMPTZ", ", NULL"},
		{"malformed", ", applied_at TEXT", ", 'invalid-time'"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			runner, dir := setupTestRunner(t)
			ctx := t.Context()
			const schema = "CREATE TABLE _migrations (id SERIAL PRIMARY KEY, name TEXT NOT NULL, checksum TEXT"
			if err := runner.db.Exec(ctx, schema+sample.column+")"); err != nil {
				t.Fatal(err)
			}
			const insert = "INSERT INTO _migrations VALUES (DEFAULT, '001_existing.sql', NULL"
			if err := runner.db.Exec(ctx, insert+sample.data+")"); err != nil {
				t.Fatal(err)
			}
			writeMigration(t, dir, "002_pending.sql", "SELECT 42;", "SELECT 0;")
			names, sqls, err := runner.DryRun(ctx)
			assertUnreadableHistoryTimestamp(t, sample.name, err)
			if len(names) != 0 || len(sqls) != 0 {
				t.Fatalf("preview exposed a plan despite unreadable history: %v %v", names, sqls)
			}
			changed, err := runner.Up(ctx)
			assertUnreadableHistoryTimestamp(t, sample.name, err)
			if len(changed) != 0 {
				t.Fatalf("migration ran despite unreadable history: %v", changed)
			}
			statuses, err := runner.Status(ctx)
			assertUnreadableHistoryTimestamp(t, sample.name, err)
			if len(statuses) != 0 {
				t.Fatalf("status exposed a partial result: %v", statuses)
			}
			var count int
			if err := runner.db.QueryRow(ctx, "SELECT COUNT(*) FROM _migrations").Scan(&count); err != nil || count != 1 {
				t.Fatalf("expected one unchanged historical row: %d: %v", count, err)
			}
		})
	}
}

func assertUnreadableHistoryTimestamp(t *testing.T, sample string, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "applied_at") {
		t.Fatalf("expected a specific historical timestamp failure: %v", err)
	}
	if sample == "missing" && !migrationHasSQLState(err, "42703") {
		t.Fatalf("expected PostgreSQL undefined column: %v", err)
	}
}

func TestIntegrationDryRunDoesNotCreateHistory(t *testing.T) {
	runner, dir := setupTestRunner(t)
	writeMigration(t, dir, "001_init.sql", "CREATE TABLE test_users (id int);", "DROP TABLE test_users;")

	if names, sqls, err := runner.DryRun(t.Context()); err != nil || len(names) != 1 || len(sqls) != 1 {
		t.Fatalf("empty database preview: %v %v %v", names, sqls, err)
	}
	assertMigrationRelations(t, runner, false, false)
}

func TestIntegrationRejectsMixedMigrationBeforeWriting(t *testing.T) {
	runner, dir := setupTestRunner(t)
	writeMigration(t, dir, "001_init.sql", "CREATE TABLE test_users (id int);", "DROP TABLE test_users;")
	writeMigration(t, dir, "002_mixed.sql", `ALTER TABLE test_users ADD COLUMN label text;
		CREATE INDEX CONCURRENTLY test_users_label_idx ON test_users (label);`, "SELECT 1;")

	executed, err := runner.Up(t.Context())
	if len(executed) != 0 || err == nil || !strings.Contains(err.Error(), "002_mixed.sql") ||
		!strings.Contains(err.Error(), "UP") || !strings.Contains(err.Error(), "split") {
		t.Errorf("mixed migration preflight: %v %v", executed, err)
	}
	assertMigrationRelations(t, runner, false, false)
}

func TestIntegrationRejectsMixedRollbackBeforeWriting(t *testing.T) {
	runner, dir := setupTestRunner(t)
	writeMigration(t, dir, "001_init.sql", "CREATE TABLE test_users (id int);", `
		DROP INDEX CONCURRENTLY IF EXISTS test_users_unused_idx;
		DROP TABLE test_users;`)
	writeMigration(t, dir, "002_add.sql", "ALTER TABLE test_users ADD COLUMN label text;", "ALTER TABLE test_users DROP COLUMN label;")
	if applied, err := runner.Up(t.Context()); err != nil || len(applied) != 2 {
		t.Fatalf("setup: %v %v", applied, err)
	}
	rolledBack, err := runner.Down(t.Context(), 2)
	if len(rolledBack) != 0 || err == nil || !strings.Contains(err.Error(), "001_init.sql") ||
		!strings.Contains(err.Error(), "DOWN") || !strings.Contains(err.Error(), "split") {
		t.Errorf("mixed rollback preflight: %v %v", rolledBack, err)
	}
	assertMigrationRelations(t, runner, true, true)
	var count int
	if err := runner.db.QueryRow(t.Context(), "SELECT count(*) FROM _migrations").Scan(&count); err != nil || count != 2 {
		t.Fatalf("migration history: %d %v", count, err)
	}
}

func TestIntegrationConcurrentIndexRetryDoesNotRecordInvalidIndex(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := t.Context()
	writeMigration(t, dir, "001_init.sql", `
		CREATE TABLE test_users (id int PRIMARY KEY, label text);
		INSERT INTO test_users VALUES (1, 'duplicate'), (2, 'duplicate');`, "DROP TABLE test_users;")
	writeMigration(t, dir, "002_idx.sql", `
		CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS "retry;index"
			ON "public"."test_users" (label);`, `DROP INDEX CONCURRENTLY IF EXISTS "retry;index";`)

	var pgErr *pgconn.PgError
	applied, err := runner.Up(ctx)
	if len(applied) != 1 || !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("expected initial unique violation: %v %v", applied, err)
	}
	assertConcurrentIndexState(t, runner, false, 1)

	applied, err = runner.Up(ctx)
	if len(applied) != 0 || err == nil || !strings.Contains(err.Error(), "index") || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("invalid existing index was recorded: %v %v", applied, err)
	}
	assertConcurrentIndexState(t, runner, false, 1)

	if err := runner.db.Exec(ctx, `DROP INDEX "retry;index"; DELETE FROM test_users WHERE id = 2;`); err != nil {
		t.Fatal(err)
	}
	// A deliberately preexisting valid index on the target table remains compatible
	// with PostgreSQL's IF NOT EXISTS contract.
	if err := runner.db.Exec(ctx, `CREATE UNIQUE INDEX "retry;index" ON test_users (label);`); err != nil {
		t.Fatal(err)
	}
	applied, err = runner.Up(ctx)
	if len(applied) != 1 || applied[0] != "002_idx.sql" || err != nil {
		t.Fatalf("valid existing index: %v %v", applied, err)
	}
	assertConcurrentIndexState(t, runner, true, 2)
}

func assertConcurrentIndexState(t *testing.T, runner *Runner, expectedValid bool, expectedHistory int) {
	t.Helper()
	var valid bool
	var history int
	err := runner.db.QueryRow(t.Context(), `SELECT
		(SELECT indisvalid FROM pg_index WHERE indexrelid = 'public."retry;index"'::regclass),
		(SELECT count(*) FROM _migrations)`).Scan(&valid, &history)
	if err != nil || valid != expectedValid || history != expectedHistory {
		t.Fatalf("index valid=%t, history=%d, err=%v", valid, history, err)
	}
}

func TestIntegrationConcurrentIndexRejectsSameNameOnDifferentTable(t *testing.T) {
	runner, dir := setupTestRunner(t)
	writeMigration(t, dir, "001_init.sql", `
		CREATE TABLE test_users (id int);
		CREATE TABLE test_posts (id int);
		CREATE INDEX same_name ON test_posts (id);`, "DROP TABLE test_users, test_posts;")
	writeMigration(t, dir, "002_idx.sql", "CREATE INDEX CONCURRENTLY IF NOT EXISTS same_name ON test_users (id);", "DROP INDEX CONCURRENTLY same_name;")
	applied, err := runner.Up(t.Context())
	if len(applied) != 1 || applied[0] != "001_init.sql" || err == nil || !strings.Contains(err.Error(), "belongs to another table") {
		t.Fatalf("another table's index was accepted: %v %v", applied, err)
	}
	var history int
	if err := runner.db.QueryRow(t.Context(), "SELECT count(*) FROM _migrations").Scan(&history); err != nil || history != 1 {
		t.Fatalf("migration history: %d %v", history, err)
	}
}

func assertMigrationRelations(t *testing.T, runner *Runner, wantHistory, wantUsers bool) {
	t.Helper()
	var history, users bool
	err := runner.db.QueryRow(t.Context(), "SELECT to_regclass('_migrations') IS NOT NULL, to_regclass('test_users') IS NOT NULL").Scan(&history, &users)
	if err != nil || history != wantHistory || users != wantUsers {
		t.Fatalf("relations: history=%t, users=%t, err=%v", history, users, err)
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
	runner.db.(*PGDB).pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE indexname='idx_test_users_name')").Scan(&indexExists)
	if !indexExists {
		t.Error("CONCURRENTLY index should exist")
	}
}

func TestIntegrationAdvisoryLock(t *testing.T) {
	runner, _ := setupTestRunner(t)
	ctx := context.Background()

	err := runner.db.AcquireLock(ctx)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	runner.db.ReleaseLock(ctx)
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

func TestIntegrationUpAfterClose(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := context.Background()

	writeMigration(t, dir, "001_a.sql",
		"CREATE TABLE test_users (id SERIAL PRIMARY KEY);",
		"DROP TABLE IF EXISTS test_users;")

	runner.db.(*PGDB).pool.Close()

	_, err := runner.Up(ctx)
	if err == nil {
		t.Fatal("should fail on closed pool")
	}
}

func TestIntegrationDownAfterClose(t *testing.T) {
	runner, _ := setupTestRunner(t)
	ctx := context.Background()

	runner.db.(*PGDB).pool.Close()

	_, err := runner.Down(ctx, 1)
	if err == nil {
		t.Fatal("should fail on closed pool")
	}
}

func TestIntegrationStatusAfterClose(t *testing.T) {
	runner, _ := setupTestRunner(t)
	ctx := context.Background()

	runner.db.(*PGDB).pool.Close()

	_, err := runner.Status(ctx)
	if err == nil {
		t.Fatal("should fail on closed pool")
	}
}

func TestIntegrationVerifyAfterClose(t *testing.T) {
	runner, _ := setupTestRunner(t)
	ctx := context.Background()

	runner.db.(*PGDB).pool.Close()

	_, err := runner.Verify(ctx)
	if err == nil {
		t.Fatal("should fail on closed pool")
	}
}

func TestIntegrationDryRunAfterClose(t *testing.T) {
	runner, _ := setupTestRunner(t)
	ctx := context.Background()

	runner.db.(*PGDB).pool.Close()

	_, _, err := runner.DryRun(ctx)
	if err == nil {
		t.Fatal("should fail on closed pool")
	}
}

func TestIntegrationDownBadDownSQL(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := context.Background()

	writeMigration(t, dir, "001_a.sql",
		"CREATE TABLE test_users (id SERIAL PRIMARY KEY);",
		"THIS IS BAD SQL;")

	runner.Up(ctx)

	_, err := runner.Down(ctx, 1)
	if err == nil {
		t.Fatal("should fail on bad DOWN SQL")
	}
}

func TestIntegrationDownMissingFile(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := context.Background()

	writeMigration(t, dir, "001_a.sql",
		"CREATE TABLE test_users (id SERIAL PRIMARY KEY);",
		"DROP TABLE IF EXISTS test_users;")

	runner.Up(ctx)

	// Delete the migration file
	os.Remove(filepath.Join(dir, "001_a.sql"))

	_, err := runner.Down(ctx, 1)
	if err == nil {
		t.Fatal("should fail when migration file is missing")
	}
}

func TestIntegrationUpMissingSectionInFile(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := context.Background()

	// Write file without proper sections
	os.WriteFile(filepath.Join(dir, "001_bad.sql"), []byte("no sections here"), 0644)

	_, err := runner.Up(ctx)
	if err == nil {
		t.Fatal("should fail when UP section missing")
	}
}

func TestIntegrationExecRollbackConcurrently(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := context.Background()

	writeMigration(t, dir, "001_create.sql",
		"CREATE TABLE test_users (id SERIAL PRIMARY KEY, name TEXT);",
		"DROP TABLE IF EXISTS test_users;")
	writeMigration(t, dir, "002_idx.sql",
		"CREATE INDEX CONCURRENTLY idx_test_name ON test_users (name);",
		"DROP INDEX CONCURRENTLY IF EXISTS idx_test_name;")

	runner.Up(ctx)

	// Down should handle CONCURRENTLY in DOWN section
	rolledBack, err := runner.Down(ctx, 1)
	if err != nil {
		t.Fatalf("Down with CONCURRENTLY: %v", err)
	}
	if len(rolledBack) != 1 {
		t.Errorf("expected 1, got %d", len(rolledBack))
	}
}

func TestIntegrationPGDBQueryRow(t *testing.T) {
	runner, _ := setupTestRunner(t)
	ctx := context.Background()
	runner.Init(ctx)

	db := runner.db.(*PGDB)
	row := db.QueryRow(ctx, "SELECT count(*) FROM _migrations")
	var count int64
	if err := row.Scan(&count); err != nil {
		t.Fatalf("QueryRow: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}
}

func TestIntegrationPGDBQueryError(t *testing.T) {
	runner, _ := setupTestRunner(t)
	ctx := context.Background()

	db := runner.db.(*PGDB)
	_, err := db.Query(ctx, "SELECT * FROM nonexistent_table_xyz")
	if err == nil {
		t.Fatal("should error on nonexistent table")
	}
}

func TestIntegrationPGDBBeginAndRollback(t *testing.T) {
	runner, _ := setupTestRunner(t)
	ctx := context.Background()
	runner.Init(ctx)

	db := runner.db.(*PGDB)
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	err = tx.Exec(ctx, "INSERT INTO _migrations (name) VALUES ($1)", "test_tx")
	if err != nil {
		t.Fatalf("Exec in tx: %v", err)
	}

	// Rollback — should not persist
	tx.Rollback(ctx)

	row := db.QueryRow(ctx, "SELECT count(*) FROM _migrations WHERE name = 'test_tx'")
	var count int64
	row.Scan(&count)
	if count != 0 {
		t.Error("rollback should not persist")
	}
}

func TestIntegrationPGDBBeginAndCommit(t *testing.T) {
	runner, _ := setupTestRunner(t)
	ctx := context.Background()
	runner.Init(ctx)

	db := runner.db.(*PGDB)
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	tx.Exec(ctx, "INSERT INTO _migrations (name) VALUES ($1)", "test_commit")
	tx.Commit(ctx)

	row := db.QueryRow(ctx, "SELECT count(*) FROM _migrations WHERE name = 'test_commit'")
	var count int64
	row.Scan(&count)
	if count != 1 {
		t.Error("commit should persist")
	}
}

func TestIntegrationNewPGDBError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := NewPGDB(ctx, "postgres://bad:bad@192.0.2.1:1/bad?sslmode=disable")
	if err == nil {
		t.Fatal("should fail")
	}
}

func TestIntegrationPGDBBeginCanceled(t *testing.T) {
	runner, _ := setupTestRunner(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if tx, err := runner.db.Begin(ctx); !errors.Is(err, context.Canceled) || tx != nil {
		t.Fatalf("canceled begin: tx=%v err=%v", tx, err)
	}
}

func TestIntegrationExecMigrationTxError(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := context.Background()
	runner.Init(ctx)

	// Migration with valid non-tx but invalid tx SQL
	content := "-- ====== UP ======\n\nINVALID SQL HERE;\n\n-- ====== DOWN ======\n\nSELECT 1;"
	os.WriteFile(filepath.Join(dir, "001_bad_tx.sql"), []byte(content), 0644)

	_, err := runner.Up(ctx)
	if err == nil {
		t.Fatal("should fail on invalid tx SQL")
	}
}

func TestIntegrationDryRunEmpty(t *testing.T) {
	runner, _ := setupTestRunner(t)
	ctx := context.Background()

	names, sqls, err := runner.DryRun(ctx)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if len(names) != 0 || len(sqls) != 0 {
		t.Error("empty DryRun should return nothing")
	}
}

func TestIntegrationUpChecksumStored(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := context.Background()

	writeMigration(t, dir, "001_create_users.sql",
		"CREATE TABLE test_users (id SERIAL PRIMARY KEY);",
		"DROP TABLE IF EXISTS test_users;")

	runner.Up(ctx)

	// Check that checksum was stored in _migrations
	var checksum string
	err := runner.db.(*PGDB).pool.QueryRow(ctx, "SELECT checksum FROM _migrations WHERE name = $1", "001_create_users.sql").Scan(&checksum)
	if err != nil {
		t.Fatalf("query checksum: %v", err)
	}
	if checksum == "" {
		t.Error("checksum should be stored")
	}
	if len(checksum) != 32 {
		t.Errorf("checksum length = %d, want 32", len(checksum))
	}
}

func TestIntegrationVerifyUnexpectedTable(t *testing.T) {
	runner, dir := setupTestRunner(t)
	ctx := context.Background()

	runner.Init(ctx)

	// Create a table directly (not via migration)
	runner.db.(*PGDB).pool.Exec(ctx, "CREATE TABLE test_rogue (id INT)")
	t.Cleanup(func() {
		runner.db.(*PGDB).pool.Exec(context.Background(), "DROP TABLE IF EXISTS test_rogue")
	})

	// Write state that doesn't include test_rogue
	stateJSON := `{"models":{}}`
	os.WriteFile(filepath.Join(dir, ".state.json"), []byte(stateJSON), 0644)

	drifts, _ := runner.Verify(ctx)
	hasUnexpected := false
	for _, d := range drifts {
		if strings.Contains(d, "test_rogue") && strings.Contains(d, "unexpected") {
			hasUnexpected = true
		}
	}
	if !hasUnexpected {
		t.Errorf("should detect unexpected table, drifts: %v", drifts)
	}
}
