package migrate

import (
	"database/sql"
	"errors"
	"net/url"
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
	runner, dir := setupTestRunner(t)
	writeMigration(t, dir, auditBase, "CREATE TABLE test_users (label text);", "DROP TABLE test_users;")
	writeMigration(t, dir, auditChange, "CREATE INDEX CONCURRENTLY audit_idx ON test_users (label);",
		"DROP INDEX CONCURRENTLY audit_idx;")
	auditUp(t, runner, 2)
	assertMigrationMetadataRelation(t, runner, pgx.Identifier{"public", migrationAttemptTable}, true)
	drifts, err := runner.Verify(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, drift := range drifts {
		if strings.Contains(drift, migrationAttemptTable) {
			t.Fatalf("internal journal reported as an application table: %v", drifts)
		}
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
			if attempt, err := runner.readMigrationAttempt(t.Context()); attempt != nil || !errors.Is(err, sample.want) {
				t.Fatalf("journal read: attempt=%v err=%v expected error=%v", attempt, err, sample.want)
			}
		})
	}
}

func metadataFixtureRunner(t *testing.T, searchPath, dir string, readOnly bool) *Runner {
	t.Helper()
	dsn, err := url.Parse(BuildDatabaseURL())
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
