package migrate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestIntegrationPendingMigrationSurvivesRestart(t *testing.T) {
	for _, direction := range []string{"UP", "DOWN"} {
		t.Run(direction, func(t *testing.T) { assertPendingMigrationSurvivesRestart(t, direction) })
	}
}

func assertPendingMigrationSurvivesRestart(t *testing.T, direction string) {
	t.Helper()
	ctx := t.Context()
	first := migrationAuditRunner(t)
	writeMigration(t, first.dir, auditChange,
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS audit_idx ON audit_users (label);",
		"DROP INDEX CONCURRENTLY IF EXISTS audit_idx;")
	if direction == "DOWN" {
		auditUp(t, first.Runner, 1)
	}
	auditRejectHistory(t, first.db)
	apply := func(r *Runner) ([]string, error) {
		if direction == "UP" {
			return r.Up(ctx)
		}
		return r.Down(ctx, 1)
	}
	if ran, err := apply(first.Runner); len(ran) != 0 {
		t.Fatalf("reported unsuccessful history as applied: %v, %v", ran, err)
	} else {
		auditSQLState(t, err, "P0001")
	}
	auditExec(t, first.db, "DROP TRIGGER audit_reject_record ON _migrations;")
	first.Close()

	restarted := first.open(t, first.dir)
	// Reading the unresolved state remains possible before reading any migration
	// files, using a new Runner after the owning connection has closed.
	preview, _, previewErr := restarted.DryRun(ctx)
	if !isPendingMigrationError(previewErr, direction) {
		t.Errorf("dry-run did not identify the unresolved %s attempt: %v %v", direction, preview, previewErr)
	}
	ran, retryErr := apply(restarted)
	if len(ran) != 0 || !isPendingMigrationError(retryErr, direction) {
		t.Errorf("restart advanced history without resolving %s: %v %v", direction, ran, retryErr)
	}
	if direction == "UP" {
		auditIndexAndHistory(t, restarted.db, true, 1)
	} else {
		auditIndexAndHistory(t, restarted.db, false, 2)
	}
	assertNamedReconciliation(t, restarted, retryErr, direction)
}

func isPendingMigrationError(err error, direction string) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "reconciliation required") && strings.Contains(message, direction) && strings.Contains(message, auditChange)
}

type migrationLostReplyDB struct {
	MigrationSessionDB
	fault error
}

func (d migrationLostReplyDB) Begin(ctx context.Context) (Tx, error) {
	tx, err := d.MigrationSessionDB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return migrationLostReplyTx{Tx: tx, fault: d.fault}, nil
}

type migrationLostReplyTx struct {
	Tx
	fault error
}

func (tx migrationLostReplyTx) Commit(ctx context.Context) error {
	if err := tx.Tx.Commit(ctx); err != nil {
		return err
	}
	return tx.fault
}

func TestIntegrationPendingMigrationAfterHistoryCommitReplyIsLost(t *testing.T) {
	ctx := t.Context()
	first := migrationAuditRunner(t)
	writeMigration(t, first.dir, auditChange,
		"CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);",
		"DROP INDEX CONCURRENTLY audit_idx;")
	auditUp(t, first.Runner, 1)
	fault := errors.New("migration fixture lost the commit reply")
	lost := NewFromDB(migrationLostReplyDB{MigrationSessionDB: first.openBoundSession(t), fault: fault}, first.dir)
	if done, err := lost.Down(ctx, 1); len(done) != 0 || !errors.Is(err, fault) {
		t.Fatalf("lost commit reply: done=%v err=%v", done, err)
	}
	auditIndexAndHistory(t, first.db, false, 1)
	first.Close()
	restarted := first.open(t, first.dir)
	done, err := restarted.Down(ctx, 1)
	var pending *MigrationReconciliationError
	if len(done) != 0 || !errors.As(err, &pending) || pending.Attempt.Name != auditChange ||
		pending.Attempt.Direction != "DOWN" || !pending.Attempt.HistoryCommitted {
		t.Fatalf("restart unexpectedly selected another rollback: done=%v err=%v", done, err)
	}
	auditIndexAndHistory(t, restarted.db, false, 1)
	if err := restarted.Reconcile(ctx, pending.Attempt.ID, RetryMigration); err == nil {
		t.Fatal("allowed a known committed attempt to run SQL again")
	}
	if err := restarted.Reconcile(ctx, pending.Attempt.ID, AcknowledgeMigration); err != nil {
		t.Fatal(err)
	}
	auditIndexAndHistory(t, restarted.db, false, 1)
}

func assertNamedReconciliation(t *testing.T, restarted *Runner, pendingErr error, direction string) {
	t.Helper()
	ctx := t.Context()
	var pending *MigrationReconciliationError
	if !errors.As(pendingErr, &pending) || pending.Attempt.ID == "" {
		t.Fatalf("expected typed pending attempt, got %v", pendingErr)
	}
	if err := restarted.Reconcile(ctx, "wrong-attempt-id", RecordApplied); err == nil {
		t.Fatal("accepted reconciliation for the wrong attempt")
	}
	path := filepath.Join(restarted.dir, auditChange)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append([]byte("-- different file\n"), original...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Reconcile(ctx, pending.Attempt.ID, RecordApplied); err == nil || !strings.Contains(err.Error(), "file changed") {
		t.Fatalf("reconciled changed migration file: %v", err)
	}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Reconcile(ctx, pending.Attempt.ID, RecordApplied); err != nil {
		t.Fatal(err)
	}
	if direction == "UP" {
		auditIndexAndHistory(t, restarted.db, true, 2)
	} else {
		auditIndexAndHistory(t, restarted.db, false, 1)
		receipt, err := restarted.PendingAttempt(ctx)
		if err != nil || receipt == nil || receipt.ID != pending.Attempt.ID || !receipt.HistoryCommitted {
			t.Fatalf("standalone rollback receipt: %+v %v", receipt, err)
		}
		if err := restarted.Reconcile(ctx, pending.Attempt.ID, AcknowledgeMigration); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := restarted.DryRun(ctx); err != nil {
		t.Fatalf("reconciled attempt still blocked preview: %v", err)
	}
}

type migrationLostAcknowledgementDB struct {
	MigrationSessionDB
	fault error
}

func (d migrationLostAcknowledgementDB) Exec(ctx context.Context, sql string, args ...any) error {
	if err := d.MigrationSessionDB.Exec(ctx, sql, args...); err != nil {
		return err
	}
	if strings.Contains(sql, "DELETE FROM") && strings.Contains(sql, "_luxo_migration_attempt") {
		return d.fault
	}
	return nil
}

func TestIntegrationPendingMigrationRetainsDownReceipt(t *testing.T) {
	ctx := t.Context()
	first := migrationAuditRunner(t)
	writeMigration(t, first.dir, auditChange,
		"CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);",
		"DROP INDEX CONCURRENTLY audit_idx;")
	auditUp(t, first.Runner, 1)
	if done, err := first.Down(ctx, 1); err != nil || !slices.Equal(done, []string{auditChange}) {
		t.Fatalf("standalone rollback: done=%v err=%v", done, err)
	}
	auditIndexAndHistory(t, first.db, false, 1)
	first.Close()
	restarted := first.open(t, first.dir)
	done, err := restarted.Down(ctx, 1)
	var pending *MigrationReconciliationError
	if len(done) != 0 || !errors.As(err, &pending) || pending.Attempt.Name != auditChange || !pending.Attempt.HistoryCommitted {
		t.Fatalf("the completed standalone rollback lost its receipt: done=%v err=%v", done, err)
	}
	auditIndexAndHistory(t, restarted.db, false, 1)
}

func TestIntegrationPendingMigrationAcknowledgementReplyIsLost(t *testing.T) {
	ctx := t.Context()
	first := migrationAuditRunner(t)
	writeMigration(t, first.dir, auditChange,
		"CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);",
		"DROP INDEX CONCURRENTLY audit_idx;")
	auditUp(t, first.Runner, 1)
	if done, err := first.Down(ctx, 1); err != nil || !slices.Equal(done, []string{auditChange}) {
		t.Fatalf("standalone rollback: done=%v err=%v", done, err)
	}
	receipt, err := first.PendingAttempt(ctx)
	if err != nil || receipt == nil || !receipt.HistoryCommitted {
		t.Fatalf("committed rollback receipt: %+v %v", receipt, err)
	}
	first.Close()

	fault := errors.New("migration fixture lost the acknowledgement reply")
	lost := NewFromDB(migrationLostAcknowledgementDB{MigrationSessionDB: first.openBoundSession(t), fault: fault}, first.dir)
	if err := lost.Reconcile(ctx, receipt.ID, AcknowledgeMigration); !errors.Is(err, fault) {
		t.Fatalf("lost acknowledgement did not propagate: %v", err)
	}
	restarted := first.open(t, first.dir)
	if pending, err := restarted.PendingAttempt(ctx); err != nil || pending != nil {
		t.Fatalf("acknowledged rollback remained pending: %+v %v", pending, err)
	}
	auditIndexAndHistory(t, restarted.db, false, 1)
}

func TestIntegrationPendingMigrationSurvivesTransactionSearchPath(t *testing.T) {
	ctx := t.Context()
	first := migrationAuditRunner(t)
	writeMigration(t, first.dir, auditChange,
		"SET search_path TO "+first.schema+"_alt, "+first.schema+";",
		"SELECT 1;")
	const standalone = "003_audit_index.sql"
	writeMigration(t, first.dir, standalone,
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS audit_idx ON "+first.schema+".audit_users (label);",
		"DROP INDEX CONCURRENTLY IF EXISTS "+first.schema+".audit_idx;")
	auditExec(t, first.db, `
        CREATE FUNCTION audit_reject_record() RETURNS trigger LANGUAGE plpgsql AS
        $$ BEGIN RAISE EXCEPTION 'audit fixture denied selected history'; END; $$;
        CREATE TRIGGER audit_reject_record BEFORE INSERT ON _migrations
        FOR EACH ROW WHEN (NEW.name = '003_audit_index.sql') EXECUTE FUNCTION audit_reject_record();
    `)
	if ran, err := first.Up(ctx); len(ran) != 1 || ran[0] != auditChange {
		t.Fatalf("first transactional migration should succeed: %v %v", ran, err)
	} else {
		auditSQLState(t, err, "P0001")
	}
	auditIndexAndHistory(t, first.db, true, 2)
	first.Close()

	restarted := first.open(t, first.dir)
	_, _, err := restarted.DryRun(ctx)
	var pending *MigrationReconciliationError
	if !errors.As(err, &pending) || pending.Attempt.Name != standalone {
		t.Fatalf("fresh session cannot see the pending marker after a migration changed search_path: %v", err)
	}
}

func TestIntegrationPendingMigrationReplacesOnlyItsOwnCommittedDownReceipt(t *testing.T) {
	ctx := t.Context()
	first := migrationAuditRunner(t)
	writeMigration(t, first.dir, auditChange,
		"CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);",
		"DROP INDEX CONCURRENTLY audit_idx;")
	const later = "003_audit_second_index.sql"
	writeMigration(t, first.dir, later,
		"CREATE INDEX CONCURRENTLY audit_idx2 ON audit_users (id);",
		"DROP INDEX CONCURRENTLY audit_idx2;")
	auditUp(t, first.Runner, 2)
	done, err := first.Down(ctx, 2)
	if err != nil || !slices.Equal(done, []string{later, auditChange}) {
		t.Fatalf("multi-file rollback: %v %v", done, err)
	}
	auditIndexAndHistory(t, first.db, false, 1)
	receipt, err := first.PendingAttempt(ctx)
	if err != nil || receipt == nil || !receipt.HistoryCommitted || receipt.Name != auditChange {
		t.Fatalf("latest receipt: %+v %v", receipt, err)
	}
	if done, err := first.Down(ctx, 1); len(done) != 0 || !isPendingMigrationError(err, "DOWN") {
		t.Fatalf("another batch advanced history before acknowledgement: %v %v", done, err)
	}
	if err := first.Reconcile(ctx, receipt.ID, AcknowledgeMigration); err != nil {
		t.Fatal(err)
	}
	if pending, err := first.PendingAttempt(ctx); pending != nil || err != nil {
		t.Fatalf("acknowledgement left receipt: %+v %v", pending, err)
	}
}

type migrationLostIntentReplyDB struct {
	MigrationSessionDB
	fault error
}

func (d migrationLostIntentReplyDB) Exec(ctx context.Context, sql string, args ...any) error {
	if err := d.MigrationSessionDB.Exec(ctx, sql, args...); err != nil {
		return err
	}
	if strings.Contains(sql, "INSERT INTO") && strings.Contains(sql, "_luxo_migration_attempt") {
		return d.fault
	}
	return nil
}

func TestIntegrationPendingMigrationWaitsForIntentBeforeStandaloneSQL(t *testing.T) {
	ctx := t.Context()
	first := migrationAuditRunner(t)
	writeMigration(t, first.dir, auditChange,
		"CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);",
		"DROP INDEX CONCURRENTLY audit_idx;")
	fault := errors.New("migration fixture lost the persisted intent reply")
	lost := NewFromDB(migrationLostIntentReplyDB{MigrationSessionDB: first.openBoundSession(t), fault: fault}, first.dir)
	done, err := lost.Up(ctx)
	if len(done) != 0 || !errors.Is(err, fault) {
		t.Fatalf("unconfirmed intent: done=%v err=%v", done, err)
	}
	auditIndexAndHistory(t, first.db, false, 1)
	attempt, err := first.PendingAttempt(ctx)
	if err != nil || attempt == nil || attempt.Name != auditChange || attempt.HistoryCommitted {
		t.Fatalf("persisted intent: %+v %v", attempt, err)
	}
	first.Close()

	restarted := first.open(t, first.dir)
	if err := restarted.Reconcile(ctx, attempt.ID, AcknowledgeMigration); err == nil {
		t.Fatal("acknowledged an uncommitted history")
	}
	if ran, err := restarted.Up(ctx); len(ran) != 0 || !isPendingMigrationError(err, "UP") {
		t.Fatalf("ordinary Up executed unconfirmed intent: %v %v", ran, err)
	}
	if err := restarted.Reconcile(ctx, attempt.ID, RetryMigration); err != nil {
		t.Fatal(err)
	}
	auditIndexAndHistory(t, restarted.db, true, 2)
	if err := restarted.Reconcile(ctx, attempt.ID, RetryMigration); err == nil {
		t.Fatal("accepted repeat of a completed attempt")
	}
}

func TestIntegrationPendingMigrationRejectsInvalidIndexUntilRepaired(t *testing.T) {
	ctx := t.Context()
	runner := migrationAuditRunner(t)
	auditExec(t, runner.db, "INSERT INTO audit_users VALUES (2, 'one')")
	writeMigration(t, runner.dir, auditChange,
		"CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS audit_idx ON audit_users (label);",
		"DROP INDEX CONCURRENTLY audit_idx;")
	if ran, err := runner.Up(ctx); len(ran) != 0 {
		t.Fatalf("invalid index recorded as applied: %v %v", ran, err)
	} else {
		auditSQLState(t, err, "23505")
	}
	attempt, err := runner.PendingAttempt(ctx)
	if err != nil || attempt == nil || attempt.Name != auditChange || attempt.HistoryCommitted {
		t.Fatalf("failed index attempt: %+v %v", attempt, err)
	}
	for _, action := range []ReconciliationAction{RecordApplied, RetryMigration} {
		if err := runner.Reconcile(ctx, attempt.ID, action); err == nil || !strings.Contains(err.Error(), "is invalid") {
			t.Fatalf("accepted invalid index for action %d: %v", action, err)
		}
	}
	current, err := runner.PendingAttempt(ctx)
	if err != nil || current == nil || current.ID != attempt.ID {
		t.Fatalf("failed reconciliation changed the attempt: %+v %v", current, err)
	}
	auditExec(t, runner.db, "DROP INDEX audit_idx; DELETE FROM audit_users WHERE id = 2")
	if err := runner.Reconcile(ctx, attempt.ID, RetryMigration); err != nil {
		t.Fatal(err)
	}
	auditIndexAndHistory(t, runner.db, true, 2)
}

func TestIntegrationPendingMigrationRejectsTransactionBoundary(t *testing.T) {
	ctx := t.Context()
	runner := migrationAuditRunner(t)
	const earlier = "002_audit_valid.sql"
	const escapes = "003_audit_early_commit.sql"
	writeMigration(t, runner.dir, earlier, "ALTER TABLE audit_users ADD COLUMN earlier text;", "SELECT 1;")
	writeMigration(t, runner.dir, escapes,
		"ALTER TABLE audit_users ADD COLUMN escaped text; COMMIT; SELECT absent_column FROM audit_users;",
		"SELECT 1;")
	done, err := runner.Up(ctx)
	if len(done) != 0 || err == nil || !strings.Contains(err.Error(), escapes) || !strings.Contains(err.Error(), "transaction boundary") {
		t.Errorf("migration managed its own transaction boundary: done=%v err=%v", done, err)
	}
	var added int
	err = runner.db.QueryRow(ctx, `
        SELECT count(*) FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name = 'audit_users' AND column_name IN ('earlier', 'escaped')
    `).Scan(&added)
	if err != nil || added != 0 {
		t.Fatalf("part of the invalid batch escaped preflight: columns=%d err=%v", added, err)
	}
	auditIndexAndHistory(t, runner.db, false, 1)
}

func TestMigrationTransactionBoundaryTokens(t *testing.T) {
	blocked := []string{
		"BEGIN", "START TRANSACTION", "COMMIT AND CHAIN", "END WORK",
		"ROLLBACK TRANSACTION", "ABORT", "PREPARE TRANSACTION 'g1'",
		"COMMIT PREPARED 'g1'", "ROLLBACK PREPARED 'g1'",
	}
	for _, sql := range blocked {
		if _, err := planMigrationSQL("SELECT 1; /* boundary */ " + sql); err == nil || !strings.Contains(err.Error(), "transaction boundary") {
			t.Errorf("accepted transaction boundary %q: %v", sql, err)
		}
	}
	local := []string{
		"SAVEPOINT local; ROLLBACK TO local; RELEASE local;",
		"SAVEPOINT local; ROLLBACK TRANSACTION TO SAVEPOINT local;",
		"PREPARE local AS SELECT 1; EXECUTE local; DEALLOCATE local;",
		"SELECT 'COMMIT', 'ROLLBACK'; -- COMMIT",
		"DO $$ BEGIN RAISE NOTICE 'COMMIT'; END $$;",
	}
	for _, sql := range local {
		if _, err := planMigrationSQL(sql); err != nil {
			t.Errorf("rejected transaction-local SQL %q: %v", sql, err)
		}
	}
}
