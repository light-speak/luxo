package migrate

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestIntegrationMigrationMetadataUsesCurrentSchema(t *testing.T) {
	skipIfNoDB(t)
	for _, checksumColumn := range []bool{false, true} {
		name := "no checksum column"
		if checksumColumn {
			name = "null checksum"
		}
		t.Run(name, func(t *testing.T) {
			assertMigrationMetadataHistorySchema(t, checksumColumn)
		})
	}
}

func assertMigrationMetadataHistorySchema(t *testing.T, checksumColumn bool) {
	t.Helper()
	owner := migrationAuditRunner(t)
	app := owner.schema + " apps"
	history := owner.schema + " history"
	appSchema := pgx.Identifier{app}.Sanitize()
	historySchema := pgx.Identifier{history}.Sanitize()
	legacyHistory := pgx.Identifier{history, migrationHistoryTable}.Sanitize()
	columns := "id bigint PRIMARY KEY, name text NOT NULL"
	if checksumColumn {
		columns += ", checksum text"
	}
	auditExec(t, owner.db, "CREATE SCHEMA "+appSchema+"; CREATE SCHEMA "+historySchema+";")
	defer auditExec(t, owner.db, "DROP SCHEMA "+appSchema+", "+historySchema+" CASCADE")
	auditExec(t, owner.db, "CREATE TABLE "+legacyHistory+" ("+columns+");")
	const migration = "001_legacy_idx.sql"
	if err := owner.db.Exec(t.Context(), "INSERT INTO "+legacyHistory+" (id, name) VALUES (7, $1)", migration); err != nil {
		t.Fatal(err)
	}
	auditExec(t, owner.db, "CREATE TABLE "+appSchema+".accounts (id int); CREATE INDEX legacy_idx ON "+appSchema+".accounts (id);")
	dir := t.TempDir()
	writeMigration(t, dir, migration, "CREATE INDEX CONCURRENTLY legacy_idx ON accounts (id);", "DROP INDEX CONCURRENTLY legacy_idx;")
	appRunner := metadataFixtureRunner(t, appSchema+","+historySchema+",public", dir, false)
	if done, err := appRunner.Down(t.Context(), 1); err != nil || len(done) != 0 {
		t.Fatalf("app schema selected another schema's history: %v %v", done, err)
	}
	assertMigrationMetadataRelation(t, owner.Runner, pgx.Identifier{history, migrationAttemptTable}, false)
	assertMigrationMetadataRelation(t, owner.Runner, pgx.Identifier{app, "legacy_idx"}, true)
	historyRunner := metadataFixtureRunner(t, historySchema+","+appSchema+",public", dir, false)
	done, err := historyRunner.Down(t.Context(), 1)
	if err != nil || len(done) != 1 || done[0] != migration {
		t.Fatalf("qualified legacy rollback: %v %v", done, err)
	}
	var historyEntries int
	if err := owner.db.QueryRow(t.Context(), "SELECT count(*) FROM "+legacyHistory).Scan(&historyEntries); err != nil || historyEntries != 0 {
		t.Fatalf("legacy history was not updated: %d %v", historyEntries, err)
	}
	assertMigrationMetadataRelation(t, owner.Runner, pgx.Identifier{history, migrationAttemptTable}, true)
	assertMigrationMetadataRelation(t, owner.Runner, pgx.Identifier{app, migrationAttemptTable}, false)
	assertMigrationMetadataRelation(t, owner.Runner, pgx.Identifier{app, "legacy_idx"}, false)
	assertMigrationMetadataRelation(t, owner.Runner, pgx.Identifier{owner.schema, "audit_users"}, true)
}

func TestIntegrationMigrationMetadataReadOnlyPreview(t *testing.T) {
	skipIfNoDB(t)
	owner := migrationAuditRunner(t)
	schema := owner.schema + " preview"
	name := pgx.Identifier{schema}.Sanitize()
	auditExec(t, owner.db, "CREATE SCHEMA "+name)
	defer auditExec(t, owner.db, "DROP SCHEMA "+name+" CASCADE")
	dir := t.TempDir()
	const migration = "001_preview.sql"
	writeMigration(t, dir, migration, "CREATE INDEX CONCURRENTLY account_idx ON accounts (id);", "SELECT 1;")
	reader := metadataFixtureRunner(t, name, dir, true)
	var readOnly bool
	if err := reader.db.QueryRow(t.Context(), "SELECT current_setting('transaction_read_only') = 'on'").Scan(&readOnly); err != nil || !readOnly {
		t.Fatalf("preview requires a read-only database session: %t %v", readOnly, err)
	}
	names, sqls, err := reader.DryRun(t.Context())
	if err != nil || len(names) != 1 || names[0] != migration || len(sqls) != 1 {
		t.Fatalf("read-only preview: %v %v %v", names, sqls, err)
	}
	assertMigrationMetadataRelation(t, owner.Runner, pgx.Identifier{schema, migrationHistoryTable}, false)
	assertMigrationMetadataRelation(t, owner.Runner, pgx.Identifier{schema, migrationAttemptTable}, false)
}

func TestIntegrationMigrationMetadataDoesNotDrift(t *testing.T) {
	dsn := metadataFixtureDatabaseURL(t)
	dir := t.TempDir()
	runner := metadataFixtureRunnerAt(t, dsn, "public", dir, false)
	var role string
	if err := runner.db.QueryRow(t.Context(), "SELECT current_user").Scan(&role); err != nil {
		t.Fatal(err)
	}
	auditExec(t, runner.db, "CREATE SCHEMA "+pgx.Identifier{role}.Sanitize())
	fallback := metadataFixtureRunnerAt(t, dsn, `"$user",public`, dir, false)
	assertMetadataCurrentSchema(t, fallback, role)
	assertMetadataCurrentSchema(t, runner, "public")

	writeMigration(t, dir, auditBase, "CREATE TABLE test_users (label text);", "DROP TABLE test_users;")
	writeMigration(t, dir, auditChange, "CREATE INDEX CONCURRENTLY audit_idx ON test_users (label);",
		"DROP INDEX CONCURRENTLY audit_idx;")
	auditUp(t, runner, 2)
	assertMetadataDriftInputs(t, runner, "public", `{"models":{"User":{"table":"test_users"}}}`)
	drifts, err := runner.Verify(t.Context())
	if err != nil || len(drifts) != 0 {
		t.Fatalf("public schema drift = %v, err=%v", drifts, err)
	}

	t.Run("bound tenant", func(t *testing.T) {
		const schema = `Tenant "drift"`
		quoted := pgx.Identifier{schema}.Sanitize()
		auditExec(t, runner.db, "CREATE SCHEMA "+quoted+"; CREATE TABLE public.expected_missing (id int);")
		tenantDir := t.TempDir()
		tenant := metadataFixtureRunnerAt(t, dsn, quoted+",public", tenantDir, false)
		assertMetadataCurrentSchema(t, tenant, schema)
		writeMigration(t, tenantDir, auditBase, "CREATE TABLE tenant_users (label text); CREATE TABLE unexpected_tenant (id int);", "DROP TABLE tenant_users, unexpected_tenant;")
		writeMigration(t, tenantDir, auditChange, "CREATE INDEX CONCURRENTLY tenant_idx ON tenant_users (label);",
			"DROP INDEX CONCURRENTLY tenant_idx;")
		auditUp(t, tenant, 2)
		assertMetadataDriftInputs(t, tenant, schema, `{"models":{"User":{"table":"tenant_users"},"Missing":{"table":"expected_missing"}}}`)
		drifts, err := tenant.Verify(t.Context())
		slices.Sort(drifts)
		want := []string{"missing table: expected_missing (expected by schema)", "unexpected table: unexpected_tenant (not in schema)"}
		if err != nil || !slices.Equal(drifts, want) {
			t.Fatalf("bound tenant drift = %v, want %v, err=%v", drifts, want, err)
		}
	})
}

func metadataFixtureDatabaseURL(t *testing.T) string {
	t.Helper()
	ctx, admin := databaseTestAdmin(t)
	name := "luxo_metadata_" + strings.ToLower(rand.Text())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0"); err != nil {
		t.Fatal(err)
	}
	cleanupTestDatabase(t, admin, name)
	dsn, err := url.Parse(BuildDatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	dsn.Path, dsn.RawPath = "/"+name, ""
	return dsn.String()
}

func assertMetadataCurrentSchema(t *testing.T, runner *Runner, want string) {
	t.Helper()
	var got string
	if err := runner.db.QueryRow(t.Context(), "SELECT pg_catalog.current_schema()").Scan(&got); err != nil || got != want {
		t.Fatalf("current schema = %q, want %q, err=%v", got, want, err)
	}
}

func assertMetadataDriftInputs(t *testing.T, runner *Runner, schema, state string) {
	t.Helper()
	assertMigrationMetadataRelation(t, runner, pgx.Identifier{schema, migrationHistoryTable}, true)
	assertMigrationMetadataRelation(t, runner, pgx.Identifier{schema, migrationAttemptTable}, true)
	if err := os.WriteFile(filepath.Join(runner.dir, ".state.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationMigrationMetadataRejectsUnloggedJournal(t *testing.T) {
	skipIfNoDB(t)
	runner := migrationAuditRunner(t)
	auditExec(t, runner.db, "CREATE UNLOGGED TABLE "+runner.schema+"._luxo_migration_attempt (singleton boolean)")
	_, _, err := runner.DryRun(t.Context())
	if err == nil || !strings.Contains(err.Error(), "requires persistent tables") {
		t.Fatalf("unlogged journal accepted: %v", err)
	}
}

func TestMigrationMetadataRecognizesOnlyDriverNoRows(t *testing.T) {
	missing := &pgconn.PgError{Code: "42P01"}
	for _, sample := range []struct {
		name string
		scan error
		want error
	}{
		{name: "standard driver empty", scan: sql.ErrNoRows},
		{name: "pgx driver empty", scan: pgx.ErrNoRows},
		{name: "bound journal unavailable", scan: missing, want: missing},
	} {
		t.Run(sample.name, func(t *testing.T) {
			db := &mockDB{queryRowVal: &mockRow{scanErr: sample.scan}}
			runner := NewFromDB(db, t.TempDir())
			runner.metadata.attemptExists = true
			if attempt, err := runner.readMigrationAttempt(t.Context(), true); attempt != nil || !errors.Is(err, sample.want) {
				t.Fatalf("journal read: attempt=%v err=%v expected error=%v", attempt, err, sample.want)
			}
		})
	}
}

func metadataFixtureRunner(t *testing.T, searchPath, dir string, readOnly bool) *Runner {
	t.Helper()
	return metadataFixtureRunnerAt(t, BuildDatabaseURL(), searchPath, dir, readOnly)
}

func metadataFixtureRunnerAt(t *testing.T, databaseURL, searchPath, dir string, readOnly bool) *Runner {
	t.Helper()
	dsn, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	params := dsn.Query()
	params.Set("search_path", searchPath)
	if readOnly {
		params.Set("default_transaction_read_only", "on")
	}
	dsn.RawQuery = params.Encode()
	db, err := NewPGDB(t.Context(), dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return NewFromDB(db, dir)
}

func assertMigrationMetadataRelation(t *testing.T, runner *Runner, name pgx.Identifier, want bool) {
	t.Helper()
	var exists bool
	err := runner.db.QueryRow(t.Context(), "SELECT pg_catalog.to_regclass($1) IS NOT NULL", name.Sanitize()).Scan(&exists)
	if err != nil || exists != want {
		t.Fatalf("relation %s: exists=%t, expected=%t, error=%v", name.Sanitize(), exists, want, err)
	}
}
