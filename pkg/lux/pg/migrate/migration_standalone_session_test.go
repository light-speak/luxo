package migrate

import (
	"slices"
	"strings"
	"testing"
)

func TestIntegrationStandaloneBatchRequiresBoundSession(t *testing.T) {
	t.Run("up", func(t *testing.T) { assertStandaloneBatchRejectsUnboundSession(t, false) })
	t.Run("down", func(t *testing.T) { assertStandaloneBatchRejectsUnboundSession(t, true) })
}

func assertStandaloneBatchRejectsUnboundSession(t *testing.T, down bool) {
	t.Helper()
	fixture := migrationAuditRunner(t)
	ctx := t.Context()
	ordinary, standalone := "002_ordinary.sql", "003_standalone.sql"
	if down {
		ordinary, standalone = standalone, ordinary
		writeMigration(t, fixture.dir, standalone, "CREATE INDEX audit_idx ON audit_users (label);",
			"DROP INDEX CONCURRENTLY audit_idx;")
	} else {
		writeMigration(t, fixture.dir, standalone, "CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);",
			"DROP INDEX audit_idx;")
	}
	writeMigration(t, fixture.dir, ordinary, "UPDATE audit_users SET label = 'changed' WHERE id = 1;",
		"UPDATE audit_users SET label = 'one' WHERE id = 1;")

	// Public PGDB methods use a separate query pool and advisory lock
	// connection. This wrapper intentionally supplies no binder capability.
	runner := NewFromDB(struct{ DB }{fixture.db}, fixture.dir)
	var names []string
	var err error
	direction, label, history := "UP", "one", 1
	if down {
		auditUp(t, fixture.Runner, 2)
		direction, label, history = "DOWN", "changed", 3
		names, err = runner.Down(ctx, 2)
	} else {
		preview, _, previewErr := runner.DryRun(ctx)
		if previewErr != nil || !slices.Equal(preview, []string{ordinary, standalone}) {
			t.Fatalf("unbound preview was unexpectedly gated: %v %v", preview, previewErr)
		}
		names, err = runner.Up(ctx)
	}
	if len(names) != 0 || err == nil || !strings.Contains(err.Error(), standalone+": "+direction) ||
		!strings.Contains(err.Error(), "requires a lock-bound database session") {
		t.Errorf("unbound standalone preflight: names=%v err=%v", names, err)
	}
	var gotLabel string
	var gotHistory int
	var hasIndex, hasJournal bool
	err = fixture.db.QueryRow(ctx, `SELECT label, (SELECT count(*) FROM _migrations),
	    pg_catalog.to_regclass('audit_idx') IS NOT NULL,
	    pg_catalog.to_regclass('_luxo_migration_attempt') IS NOT NULL
	    FROM audit_users WHERE id = 1`).Scan(&gotLabel, &gotHistory, &hasIndex, &hasJournal)
	if err != nil || gotLabel != label || gotHistory != history || hasIndex != down || hasJournal {
		t.Fatalf("rejected batch changed state: label=%q history=%d index=%t journal=%t err=%v",
			gotLabel, gotHistory, hasIndex, hasJournal, err)
	}
}

func TestIntegrationStandaloneMigrationsAcceptBoundAdapter(t *testing.T) {
	fixture := migrationAuditRunner(t)
	ctx := t.Context()
	writeMigration(t, fixture.dir, auditChange, "CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);",
		"DROP INDEX CONCURRENTLY audit_idx;")
	runner := NewFromDB(fixture.openBoundSession(t), fixture.dir)
	auditUp(t, runner, 1)
	auditIndexAndHistory(t, fixture.db, true, 2)
	rolledBack, receipt, err := runner.DownWithReceipt(ctx, 1)
	if err != nil || !slices.Equal(rolledBack, []string{auditChange}) || receipt == "" {
		t.Fatalf("trusted adapter rollback: %v %q %v", rolledBack, receipt, err)
	}
	if err := runner.Reconcile(ctx, receipt, AcknowledgeMigration); err != nil {
		t.Fatal(err)
	}
	auditIndexAndHistory(t, fixture.db, false, 1)
}

func TestStandalonePreflightDoesNotInitializeHistory(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "001_users.sql", "CREATE TABLE audit_users (id int);", "SELECT 1;")
	writeMigration(t, dir, "002_index.sql", "CREATE INDEX CONCURRENTLY audit_idx ON audit_users (id);", "SELECT 1;")
	db := &mockDB{
		queryRowVal: &mockRow{},
		beginTx:     &mockTx{},
		execSQL: func(_ string) error {
			t.Error("unbound preflight attempted database mutation")
			return nil
		},
	}
	names, err := NewFromDB(db, dir).Up(t.Context())
	if len(names) != 0 || err == nil || !strings.Contains(err.Error(), "002_index.sql: UP: standalone migration requires") {
		t.Fatalf("unbound Up before history initialization: names=%v err=%v", names, err)
	}
}
