package migrate

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestIntegrationMigrationExecutionNamespace(t *testing.T) {
	t.Run("retry targets original schema", func(t *testing.T) {
		fixture, attempt := interruptNamespaceMigration(t, false)
		restarted := fixture.open(t, fixture.dir)
		if err := restarted.Reconcile(t.Context(), attempt.ID, RetryMigration); err != nil {
			t.Fatal(err)
		}
		assertNamespaceIndex(t, restarted, fixture.schema+"_alt", true)
		assertNamespaceIndex(t, restarted, fixture.schema, false)
	})
	t.Run("record checks original schema", func(t *testing.T) {
		fixture, attempt := interruptNamespaceMigration(t, false)
		restarted := fixture.open(t, fixture.dir)
		auditExec(t, restarted.db, "CREATE INDEX audit_idx ON "+fixture.schema+"_alt.audit_users (label);")
		if err := restarted.Reconcile(t.Context(), attempt.ID, RecordApplied); err != nil {
			t.Fatal(err)
		}
		assertNamespaceIndex(t, restarted, fixture.schema, false)
	})
	t.Run("newly visible schema must not change the target", func(t *testing.T) {
		fixture, attempt := interruptNamespaceMigration(t, true)
		restarted := fixture.open(t, fixture.dir)
		// A missing schema becomes part of search_path after the first attempt.
		// DROP SCHEMA is limited to the extra schema of this owned fixture.
		third := fixture.schema + "_absent"
		auditExec(t, restarted.db, "CREATE SCHEMA "+third+"; CREATE TABLE "+third+".audit_users (id int PRIMARY KEY, label text);")
		defer auditExec(t, restarted.db, "DROP SCHEMA "+third+" CASCADE")
		err := restarted.Reconcile(t.Context(), attempt.ID, RetryMigration)
		if err == nil || !strings.Contains(err.Error(), "migration execution schemas changed") {
			t.Fatalf("replay accepted changed effective search path: %v", err)
		}
		assertNamespaceIndex(t, restarted, third, false)
		assertNamespaceIndex(t, restarted, fixture.schema+"_alt", false)
		assertNamespaceIndex(t, restarted, fixture.schema, false)
	})
}

func interruptNamespaceMigration(t *testing.T, includeMissing bool) (*migrationReconcileFixture, *MigrationAttempt) {
	t.Helper()
	fixture := migrationAuditRunner(t)
	ctx := t.Context()
	alternative := fixture.schema + "_alt"
	auditExec(t, fixture.db, "CREATE TABLE "+alternative+".audit_users (id int PRIMARY KEY, label text);")
	var missing string
	if includeMissing {
		missing = fixture.schema + "_absent, "
	}
	writeMigration(t, fixture.dir, auditChange,
		"SET search_path TO "+missing+alternative+", "+fixture.schema+";", "SELECT 1;")
	const standalone = "003_standalone.sql"
	writeMigration(t, fixture.dir, standalone,
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS audit_idx ON audit_users (label);",
		"DROP INDEX CONCURRENTLY IF EXISTS audit_idx;")
	cut := cutAfterNamespaceIntent(t, fixture,
		"\n            INSERT INTO "+pgx.Identifier{fixture.schema, migrationAttemptTable}.Sanitize())
	applied, err := fixture.Up(ctx)
	if !cut.disconnected || len(applied) != 1 || applied[0] != auditChange || err == nil {
		t.Fatalf("holder cut after persisting intent: cut=%t applied=%v err=%v", cut.disconnected, applied, err)
	}
	attempt, err := fixture.PendingAttempt(ctx)
	if err != nil || attempt == nil || attempt.Name != standalone || attempt.HistoryCommitted {
		t.Fatalf("persisted attempt: %+v %v", attempt, err)
	}
	assertNamespaceIndex(t, fixture.Runner, alternative, false)
	assertNamespaceIndex(t, fixture.Runner, fixture.schema, false)
	fixture.Close()
	return fixture, attempt
}

func assertNamespaceIndex(t *testing.T, runner *Runner, schema string, want bool) {
	t.Helper()
	var indexed bool
	err := runner.db.QueryRow(t.Context(), `SELECT EXISTS (
	    SELECT 1 FROM pg_catalog.pg_index
	    WHERE indexrelid = pg_catalog.to_regclass($1)
	      AND indrelid = pg_catalog.to_regclass($2)
	      AND indisvalid
	)`, pgx.Identifier{schema, "audit_idx"}.Sanitize(), pgx.Identifier{schema, "audit_users"}.Sanitize()).Scan(&indexed)
	if err != nil || indexed != want {
		t.Fatalf("schema %s: target has index=%t, expected %t, error=%v", schema, indexed, want, err)
	}
}

func TestIntegrationMigrationExecutionNamespaceReplacement(t *testing.T) {
	fixture := migrationAuditRunner(t)
	ctx := t.Context()
	alternative := fixture.schema + "_alt"
	auditExec(t, fixture.db, "CREATE TABLE "+alternative+".audit_users (id int PRIMARY KEY, label text);")
	auditExec(t, fixture.db, "CREATE INDEX audit_idx ON "+alternative+".audit_users (label);")
	writeMigration(t, fixture.dir, auditChange, "CREATE INDEX audit_idx ON audit_users (label);",
		"DROP INDEX CONCURRENTLY audit_idx;")
	writeMigration(t, fixture.dir, "003_switch.sql", "SELECT 1;",
		"SET search_path TO "+alternative+", "+fixture.schema+";")
	writeMigration(t, fixture.dir, "004_previous.sql", "CREATE INDEX previous_idx ON audit_users (id);",
		"DROP INDEX CONCURRENTLY previous_idx;")
	auditUp(t, fixture.Runner, 3)

	cut := cutAfterNamespaceIntent(t, fixture, "\n        WITH next AS (")
	rolledBack, err := fixture.Down(ctx, 3)
	if !cut.disconnected || len(rolledBack) != 2 || rolledBack[0] != "004_previous.sql" || rolledBack[1] != "003_switch.sql" || err == nil {
		t.Fatalf("holder cut after replacing committed intent: cut=%t rolled=%v err=%v", cut.disconnected, rolledBack, err)
	}
	attempt, err := fixture.PendingAttempt(ctx)
	if err != nil || attempt == nil || attempt.Name != auditChange || attempt.HistoryCommitted {
		t.Fatalf("replacement intent: %+v %v", attempt, err)
	}
	fixture.Close()

	restarted := fixture.open(t, fixture.dir)
	if err := restarted.Reconcile(ctx, attempt.ID, RetryMigration); err != nil {
		t.Fatal(err)
	}
	assertNamespaceIndex(t, restarted, alternative, false)
	assertNamespaceIndex(t, restarted, fixture.schema, true)
}

func TestIntegrationMigrationExecutionNamespaceRejectsDifferentRole(t *testing.T) {
	fixture, attempt := interruptNamespaceMigration(t, false)
	restarted := fixture.open(t, fixture.dir)
	// Exercise the database-side role comparison with a different existing role
	// in this intent. The fixture need not create or grant that role.
	auditExec(t, restarted.db, "UPDATE _luxo_migration_attempt SET execution_role = "+
		"(SELECT oid::text FROM pg_catalog.pg_roles WHERE rolname = 'pg_read_all_data') WHERE singleton")
	for _, action := range []ReconciliationAction{RetryMigration, RecordApplied} {
		err := restarted.Reconcile(t.Context(), attempt.ID, action)
		if err == nil || !strings.Contains(err.Error(), "migration execution role changed") {
			t.Fatalf("action %d accepted a different recorded effective role: %v", action, err)
		}
	}
	assertNamespaceIndex(t, restarted, fixture.schema+"_alt", false)
	assertNamespaceIndex(t, restarted, fixture.schema, false)
	stillPending, err := restarted.PendingAttempt(t.Context())
	if err != nil || stillPending == nil || stillPending.ID != attempt.ID || stillPending.HistoryCommitted {
		t.Fatalf("role mismatch changed reconciliation state: %+v %v", stillPending, err)
	}
}

func cutAfterNamespaceIntent(t *testing.T, fixture *migrationReconcileFixture, statement string) *migrationHolderCut {
	t.Helper()
	db := fixture.db.(*PGDB)
	cut := &migrationHolderCut{testing: t, statement: statement}
	db.lockConfig = db.lockConfig.Copy()
	db.lockConfig.Tracer = cut
	return cut
}

func TestMigrationExecutionNamespacePreservesDriverErrors(t *testing.T) {
	cause := errors.New("fixture driver failure")
	for _, sample := range []struct {
		name string
		db   *mockDB
	}{
		{name: "restore", db: &mockDB{execErr: cause}},
		{name: "verify", db: &mockDB{queryRowVal: &mockRow{scanErr: cause}}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			runner := NewFromDB(sample.db, t.TempDir())
			err := runner.restoreMigrationExecution(t.Context(), &storedMigrationAttempt{}, migrationExecutionPlan{}, migrationExecutionBinding{})
			if !errors.Is(err, cause) {
				t.Fatalf("lost original database error: %v", err)
			}
		})
	}
}
