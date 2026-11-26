package migrate

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	poolConfig, err := pgxpool.ParseConfig(config.ConnectionString())
	if err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{`tenant "app"`}.Sanitize()
	poolConfig.MaxConns = 1
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema + ", public"
	poolConfig.ConnConfig.RuntimeParams["standard_conforming_strings"] = standardStrings
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	runner := NewFromDB(&PGDB{pool: pool}, dir)
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
