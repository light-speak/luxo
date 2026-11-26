package migrate

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/light-speak/luxo/pkg/lux"
)

func migrationSessionFixture(t *testing.T, standardStrings string) (*Runner, string, string) {
	t.Helper()
	ctx, admin := databaseTestAdmin(t)
	name := "luxo_migration_session_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	cleanupTestDatabase(t, admin, name)
	config := lux.DBConfigFromEnv()
	config.DBName = name
	dsn, err := url.Parse(config.ConnectionString())
	if err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{`tenant "app"`}.Sanitize()
	params := dsn.Query()
	params.Set("pool_max_conns", "1")
	params.Set("search_path", schema+", public")
	params.Set("standard_conforming_strings", standardStrings)
	dsn.RawQuery = params.Encode()
	db, err := NewPGDB(ctx, dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	runner := NewFromDB(db, dir)
	t.Cleanup(runner.Close)
	if err := runner.db.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	return runner, dir, schema
}

func TestIntegrationMigrationHistoryUsesCreationSchema(t *testing.T) {
	runner, dir, schema := migrationSessionFixture(t, "on")
	ctx := t.Context()
	const migration = "001_scope.sql"
	writeMigration(t, dir, migration, "SET LOCAL search_path TO public; SELECT 1;", "SET LOCAL search_path TO public; SELECT 1;")
	if err := runner.db.Exec(ctx, `CREATE TABLE public._migrations (
		id bigint PRIMARY KEY, name text NOT NULL, checksum text,
		applied_at timestamptz NOT NULL DEFAULT now()
	); INSERT INTO public._migrations (id, name) VALUES (7, '001_scope.sql');`); err != nil {
		t.Fatal(err)
	}
	names, _, err := runner.DryRun(ctx)
	if err != nil || !slices.Equal(names, []string{migration}) {
		t.Errorf("pending tenant migration: %v, %v", names, err)
	}
	assertSessionHistory(t, runner, schema, nil, 1)
	if done, err := runner.Down(ctx, 1); err != nil || len(done) != 0 {
		t.Errorf("empty tenant rollback: %v, %v", done, err)
	}
	assertSessionHistory(t, runner, schema, nil, 1)
	if done, err := runner.Up(ctx); err != nil || !slices.Equal(done, []string{migration}) {
		t.Fatalf("tenant apply: %v, %v", done, err)
	}
	assertSessionHistory(t, runner, schema, []string{migration}, 1)
	if done, err := runner.Down(ctx, 1); err != nil || !slices.Equal(done, []string{migration}) {
		t.Fatalf("tenant rollback: %v, %v", done, err)
	}
	assertSessionHistory(t, runner, schema, []string{}, 1)
}

func assertSessionHistory(t *testing.T, runner *Runner, schema string, tenantMigrations []string, fallbackCount int) {
	t.Helper()
	ctx := t.Context()
	var exists bool
	wantHistory := tenantMigrations != nil
	if err := runner.db.QueryRow(ctx, "SELECT pg_catalog.to_regclass($1) IS NOT NULL", schema+"._migrations").Scan(&exists); err != nil || exists != wantHistory {
		t.Errorf("tenant history exists: %t, want %t, err %v", exists, wantHistory, err)
	}
	if exists {
		var names []string
		query := "SELECT COALESCE(array_agg(name ORDER BY id), ARRAY[]::text[]) FROM " + schema + "._migrations"
		if err := runner.db.QueryRow(ctx, query).Scan(&names); err != nil || !slices.Equal(names, tenantMigrations) {
			t.Errorf("tenant history migrations: %v, want %v, err %v", names, tenantMigrations, err)
		}
	}
	var count int
	if err := runner.db.QueryRow(ctx, "SELECT count(*) FROM public._migrations").Scan(&count); err != nil || count != fallbackCount {
		t.Errorf("fallback history count: %d, want %d, err %v", count, fallbackCount, err)
	}
}

func TestIntegrationMigrationRejectsLegacyStringMode(t *testing.T) {
	runner, dir, schema := migrationSessionFixture(t, "off")
	ctx := t.Context()
	writeMigration(t, dir, "001_standard.sql", "CREATE TABLE standard_mode (id bigint);", "DROP TABLE standard_mode;")
	if _, _, err := runner.DryRun(ctx); err == nil || !strings.Contains(err.Error(), "standard_conforming_strings") {
		t.Errorf("preview did not reject unsupported string mode: %v", err)
	}
	if _, err := runner.Up(ctx); err == nil || !strings.Contains(err.Error(), "standard_conforming_strings") {
		t.Errorf("apply did not reject unsupported string mode: %v", err)
	}
	if _, err := runner.Down(ctx, 1); err == nil || !strings.Contains(err.Error(), "standard_conforming_strings") {
		t.Errorf("rollback did not reject unsupported string mode: %v", err)
	}
	var history, changed bool
	if err := runner.db.QueryRow(ctx, "SELECT pg_catalog.to_regclass($1) IS NOT NULL, pg_catalog.to_regclass($2) IS NOT NULL", schema+"._migrations", schema+".standard_mode").Scan(&history, &changed); err != nil || history || changed {
		t.Errorf("invalid string mode made changes: history=%t DDL=%t err=%v", history, changed, err)
	}
}

func TestIntegrationMigrationChecksStringModeOnExecutionConnection(t *testing.T) {
	for _, sample := range []struct {
		name, statement string
	}{
		{"transactional", "CREATE TABLE second_object (id bigint);"},
		{"standalone", "CREATE INDEX CONCURRENTLY second_object ON first_object (id);"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			runner, dir, schema := migrationSessionFixture(t, "on")
			ctx := t.Context()
			const first = "001_first.sql"
			writeMigration(t, dir, first,
				"CREATE TABLE first_object (id bigint); SET standard_conforming_strings = off;", "DROP TABLE first_object;")
			writeMigration(t, dir, "002_second.sql", sample.statement, "SELECT 1;")
			done, err := runner.Up(ctx)
			if !slices.Equal(done, []string{first}) || !errors.Is(err, errMigrationStringMode) || strings.Contains(err.Error(), "unconfirmed") {
				t.Fatalf("second migration reached a different string mode: %v %v", done, err)
			}
			var changed bool
			if err := runner.db.QueryRow(ctx, "SELECT pg_catalog.to_regclass($1) IS NOT NULL", schema+".second_object").Scan(&changed); err != nil || changed {
				t.Fatalf("second migration changed the database: %t %v", changed, err)
			}
			assertMigrationMetadataRelation(t, runner, pgx.Identifier{`tenant "app"`, migrationAttemptTable}, false)
		})
	}
}

func TestIntegrationMigrationRequiresCreationSchemaForHistory(t *testing.T) {
	runner, dir, _ := migrationSessionFixture(t, "on")
	ctx := t.Context()
	if err := runner.db.Exec(ctx, "SET search_path TO missing_migration_schema"); err != nil {
		t.Fatal(err)
	}
	const migration = "001_schema.sql"
	writeMigration(t, dir, migration, "SELECT 1;", "SELECT 1;")
	names, _, err := runner.DryRun(ctx)
	if err != nil || !slices.Equal(names, []string{migration}) {
		t.Fatalf("preview without a creation schema: %v %v", names, err)
	}
	if err := runner.Init(ctx); err == nil || !strings.Contains(err.Error(), "no schema for migration history") {
		t.Fatalf("creation without a schema: %v", err)
	}
}

func TestIntegrationMigrationStandaloneAcquireHonorsCancellation(t *testing.T) {
	runner, _, _ := migrationSessionFixture(t, "on")
	db := runner.db.(*PGDB)
	conn, err := db.pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := db.execMigrationStatement(ctx, "SELECT 1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("standalone query on a canceled pool acquire: %v", err)
	}
}

func setupPingLockTest(t *testing.T) (*PGDB, uint32) {
	t.Helper()
	ctx, admin := databaseTestAdmin(t)
	name := "luxo_ping_lock_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	cleanupTestDatabase(t, admin, name)
	config := lux.DBConfigFromEnv()
	config.DBName = name
	dsn, err := url.Parse(config.ConnectionString())
	if err != nil {
		t.Fatal(err)
	}
	params := dsn.Query()
	params.Set("pool_max_conns", "1")
	params.Set("application_name", "ping-lock-fresh")
	dsn.RawQuery = params.Encode()
	db, err := NewPGDB(ctx, dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	initial := db.pool.AcquireAllIdle(ctx)
	for _, conn := range initial {
		defer conn.Release()
	}
	if len(initial) != 1 {
		t.Fatalf("idle connections after Ping = %d, want 1", len(initial))
	}
	return db, initial[0].Conn().PgConn().PID()
}

func TestIntegrationPGDBUsesPingSessionForFirstLock(t *testing.T) {
	db, initialPID := setupPingLockTest(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if err := db.AcquireLock(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled first acquire: %v", err)
	}
	if err := db.AcquireLock(ctx); err != nil {
		t.Fatal(err)
	}
	defer db.ReleaseLock(ctx)
	if holderPID := db.lockConn.PgConn().PID(); holderPID != initialPID {
		t.Fatalf("first holder used PID %d, want Ping PID %d", holderPID, initialPID)
	}
	var readerPID uint32
	if err := db.QueryRow(ctx, "SELECT pg_catalog.pg_backend_pid()").Scan(&readerPID); err != nil || readerPID == initialPID {
		t.Fatalf("public query did not get an independent session: PID %d, err %v", readerPID, err)
	}
	closed := make(chan struct{})
	go func() {
		db.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("Close waited for the transferred connection to return to the pool")
	}
}

func TestIntegrationPGDBFirstLockIgnoresUsedPoolSession(t *testing.T) {
	const change = "SET application_name = 'ping-lock-public'"
	const query = "SELECT pg_catalog.set_config('application_name', 'ping-lock-public', false)"
	for _, tc := range []struct {
		name string
		use  func(context.Context, *PGDB) error
	}{
		{"exec", func(ctx context.Context, db *PGDB) error { return db.Exec(ctx, change) }},
		{"query", func(ctx context.Context, db *PGDB) error {
			rows, err := db.Query(ctx, query)
			if err != nil {
				return err
			}
			for rows.Next() {
			}
			rows.Close()
			return migrationRowsError(rows)
		}},
		{"query-row", func(ctx context.Context, db *PGDB) error {
			var name string
			return db.QueryRow(ctx, query).Scan(&name)
		}},
		{"begin", func(ctx context.Context, db *PGDB) error {
			tx, err := db.Begin(ctx)
			if err != nil {
				return err
			}
			defer tx.Rollback(ctx)
			if err := tx.Exec(ctx, change); err != nil {
				return err
			}
			return tx.Commit(ctx)
		}},
		{"migration-statement", func(ctx context.Context, db *PGDB) error { return db.execMigrationStatement(ctx, change) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, initialPID := setupPingLockTest(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if err := tc.use(ctx, db); err != nil {
				t.Fatal(err)
			}
			if err := db.AcquireLock(ctx); err != nil {
				t.Fatal(err)
			}
			defer db.ReleaseLock(ctx)
			var holderName string
			if err := db.lockConn.QueryRow(ctx, "SHOW application_name").Scan(&holderName); err != nil {
				t.Fatal(err)
			}
			if holderPID := db.lockConn.PgConn().PID(); holderPID == initialPID || holderName != "ping-lock-fresh" {
				t.Fatalf("holder inherited public session: PID %d, name %q", holderPID, holderName)
			}
		})
	}
}

func TestIntegrationPGDBRetiresAdoptedSessionAfterWrite(t *testing.T) {
	db, initialPID := setupPingLockTest(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	dir := t.TempDir()
	const name = "001_holder.sql"
	writeMigration(t, dir, name, "CREATE TABLE holder_pid AS SELECT pg_catalog.pg_backend_pid() AS pid;", "DROP TABLE holder_pid;")
	runner := NewFromDB(db, dir)
	if done, err := runner.Up(ctx); err != nil || !slices.Equal(done, []string{name}) {
		t.Fatalf("apply: %v, %v", done, err)
	}
	if db.lockIdle != nil || db.pool.Stat().TotalConns() != 0 {
		t.Fatal("writable Ping holder remained cached")
	}
	var pid uint32
	if err := db.QueryRow(ctx, "SELECT pid FROM holder_pid").Scan(&pid); err != nil || pid != initialPID {
		t.Fatalf("DDL used PID %d, want Ping PID %d: %v", pid, initialPID, err)
	}
}
