package migrate

import (
	"errors"
	"strings"
	"testing"
)

func TestMigrationReconcileRejectsInvalidRequestsAndMetadata(t *testing.T) {
	cause := errors.New("fixture could not read metadata")
	db := &mockDB{queryRowVal: &mockRow{scanErr: cause}}
	runner := NewFromDB(db, t.TempDir())
	ctx := t.Context()
	if err := runner.Init(ctx); !errors.Is(err, cause) {
		t.Fatalf("Init lost metadata error: %v", err)
	}
	if _, err := runner.PendingAttempt(ctx); !errors.Is(err, cause) {
		t.Fatalf("PendingAttempt lost metadata error: %v", err)
	}
	for _, sample := range []struct {
		id      string
		action  ReconciliationAction
		message string
	}{
		{"", RetryMigration, "attempt ID is required"},
		{"named", 0, "invalid migration reconciliation action"},
	} {
		err := runner.Reconcile(ctx, sample.id, sample.action)
		if err == nil || !strings.Contains(err.Error(), sample.message) {
			t.Errorf("invalid reconciliation request: %v", err)
		}
	}
	runner.metadata.attemptExists = true
	if err := runner.migrationOperation.reconcile(ctx, "named", RetryMigration); !errors.Is(err, cause) {
		t.Fatalf("Reconcile lost persisted attempt read error: %v", err)
	}
}

func TestMigrationReconcileRejectsChangedFilesAndHistory(t *testing.T) {
	historyErr := errors.New("fixture could not read history")
	runner := NewFromDB(&mockDB{queryRowVal: &mockRow{scanErr: historyErr}}, t.TempDir())
	ctx := t.Context()
	for _, name := range []string{"../outside.sql", "missing.sql"} {
		attempt := &storedMigrationAttempt{MigrationAttempt: MigrationAttempt{Name: name, Direction: "UP"}}
		if _, err := runner.validateReconciliation(ctx, attempt); err == nil {
			t.Errorf("accepted invalid or unavailable migration file %q", name)
		}
	}
	for _, direction := range []string{"UP", "DOWN"} {
		attempt := &storedMigrationAttempt{MigrationAttempt: MigrationAttempt{Name: auditChange, Direction: direction}}
		if err := runner.checkReconciliationHistory(ctx, attempt); !errors.Is(err, historyErr) {
			t.Errorf("history %s read error was lost: %v", direction, err)
		}
	}
	if _, err := runner.startMigrationAttempt(ctx, plannedMigration{name: auditChange}, "DOWN"); !errors.Is(err, historyErr) {
		t.Fatalf("missing original rollback history did not stop journaling: %v", err)
	}

	normal := NewFromDB(&mockDB{}, t.TempDir())
	changed := &storedMigrationAttempt{MigrationAttempt: MigrationAttempt{Name: auditChange, Direction: "UP"}}
	if err := normal.checkReconciliationHistory(ctx, changed); err == nil || !strings.Contains(err.Error(), "already contains") {
		t.Fatalf("Reconcile ignored an intervening history row: %v", err)
	}
	changed.Direction = "DOWN"
	id := int64(7)
	changed.historyID = &id
	if err := normal.checkReconciliationHistory(ctx, changed); err == nil || !strings.Contains(err.Error(), "row changed") {
		t.Fatalf("Reconcile ignored a different rollback identity: %v", err)
	}
	checksum := "same"
	if !sameMigrationChecksum(nil, nil) || sameMigrationChecksum(nil, &checksum) || sameMigrationChecksum(&checksum, nil) {
		t.Fatal("reconciliation confused missing legacy checksums with recorded checksums")
	}
}

func TestMigrationAttemptPropagatesCommitReceiptFailures(t *testing.T) {
	cause := errors.New("fixture could not write the attempt receipt")
	for _, sample := range []struct {
		name    string
		db      *mockDB
		message string
	}{
		{
			name: "mark committed",
			db: &mockDB{beginTx: &mockTx{execSQL: func(query string) error {
				if strings.Contains(query, "UPDATE") && strings.Contains(query, "_luxo_migration_attempt") {
					return cause
				}
				return nil
			}}},
			message: "history remains uncommitted",
		},
		{
			name: "acknowledgement",
			db: &mockDB{beginTx: &mockTx{}, execSQL: func(query string) error {
				if strings.Contains(query, "DELETE FROM") && strings.Contains(query, "_luxo_migration_attempt") {
					return cause
				}
				return nil
			}},
			message: "acknowledgement unconfirmed",
		},
	} {
		t.Run(sample.name, func(t *testing.T) {
			runner := NewFromDB(sample.db, t.TempDir())
			migration := plannedMigration{name: auditChange, mode: migrationMode{autocommit: true}}
			attempt := &storedMigrationAttempt{MigrationAttempt: MigrationAttempt{ID: "named", Direction: "UP"}}
			err := runner.finishMigrationAttempt(t.Context(), migration, attempt)
			if !errors.Is(err, cause) || !strings.Contains(err.Error(), sample.message) {
				t.Fatalf("lost attempt failure: %v", err)
			}
		})
	}
}

type migrationSessionMock struct {
	*mockDB
	bound bool
}

func (d migrationSessionMock) MigrationSessionBound() bool { return d.bound }

func TestMigrationReconcileRequiresBoundCustomSession(t *testing.T) {
	for _, sample := range []struct {
		db       DB
		expected string
	}{
		{&mockDB{}, "requires a lock-bound database session"},
		{migrationSessionMock{mockDB: &mockDB{}}, "requires a lock-bound database session"},
		{migrationSessionMock{mockDB: &mockDB{}, bound: true}, "no suspended migration matches"},
	} {
		runner := NewFromDB(sample.db, t.TempDir())
		if err := runner.Reconcile(t.Context(), "named", RetryMigration); err == nil || !strings.Contains(err.Error(), sample.expected) {
			t.Errorf("custom recovery session contract: %v, expected %q", err, sample.expected)
		}
	}
}
