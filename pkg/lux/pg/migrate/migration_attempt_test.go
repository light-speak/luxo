package migrate

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
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

type migrationIntentCapture struct {
	mockDB
	query string
	args  []any
	err   error
	calls int
}

func (db *migrationIntentCapture) Exec(_ context.Context, query string, args ...any) error {
	db.query, db.args = query, args
	db.calls++
	return db.err
}

func TestStoreMigrationAttemptPreservesSQLAndArguments(t *testing.T) {
	const checksum = "history checksum"
	historyID, historyChecksum := int64(55), checksum
	plan := migrationExecutionPlan{
		targets:    [2]migrationObjectReference{{kind: migrationRelation, name: "app.users"}, {kind: migrationSchema, name: "app"}},
		tablespace: migrationExplicitTablespace, tablespaceName: "audit_space",
	}
	// Pin the journal's exact SQL, including the compact catalog binding.
	for _, schema := range []struct{ name, schema, insertSHA, replaceSHA string }{
		{"public", "public", "8b32130647cd5c458024a49270c9cc11a7ad2994bb334106272102ea943c8be7", "0f6358e7525dbd5ad9c9c312db5c16b91d0ca99284e926c7c2940301e24d6a9b"},
		{"quoted schema", "team\"blue", "feabb31a7552ca32f4466dd780e63cfc65e619b243e2415d1d1affbbc302ae29", "0cbfa670088ecbe6bda6f7a6218c4f12414f2e7d1c46d02de9ddd8351afc870f"},
		{"empty schema", "", "be2591d20cad6dd904875762438d057432a1bb316f785f922ec9f6bcdb5ae1dc", "c49e9e575b480da3b749840475cb75e38dd645a6379438876cb6d33eed525791"},
	} {
		t.Run(schema.name, func(t *testing.T) {
			for _, write := range []struct {
				name, direction, priorReceipt, slot, digest, state string
				historyID                                          *int64
				historyChecksum                                    *string
			}{
				{"insert", "UP", "", "UP", schema.insertSHA, "42703", nil, nil},
				{"replace", "DOWN", "committed-down", "committed-down", schema.replaceSHA, "22012", &historyID, &historyChecksum},
			} {
				t.Run(write.name, func(t *testing.T) {
					desired := &pgconn.PgError{Code: write.state, Message: "fixture journal write failed"}
					db := &migrationIntentCapture{err: desired}
					operation := migrationOperation{db: db, metadata: migrationMetadata{schema: schema.schema}, rollbackReceipt: write.priorReceipt}
					attempt := storedMigrationAttempt{
						MigrationAttempt: MigrationAttempt{ID: "new-attempt", Name: "001_index.sql", Direction: write.direction},
						digest:           "migration digest", historyID: write.historyID, historyChecksum: write.historyChecksum,
					}
					if err := operation.storeMigrationAttempt(t.Context(), &attempt, plan); err != desired || db.calls != 1 {
						t.Fatalf("journal returned %v through %d writes, want the original error and one write", err, db.calls)
					}
					if got := fmt.Sprintf("%x", sha256.Sum256([]byte(db.query))); got != write.digest {
						t.Fatalf("journal SQL SHA = %s, want %s", got, write.digest)
					}
					want := []any{migrationRelation, "app.users", migrationSchema, "app",
						migrationExplicitTablespace, "audit_space", attempt.ID, attempt.Name, write.slot,
						attempt.digest, write.historyID, write.historyChecksum}
					if !slices.Equal(db.args, want) {
						t.Fatalf("journal arguments = %#v, want %#v", db.args, want)
					}
				})
			}
		})
	}
}

type migrationAttemptReadCapture struct {
	mockDB
	query string
	row   Row
}

func (d *migrationAttemptReadCapture) QueryRow(_ context.Context, query string, _ ...any) Row {
	d.query = query
	return d.row
}

func TestReadMigrationAttemptPreservesJournalQueries(t *testing.T) {
	for _, schema := range []struct{ name, nameInDB, plain, bound string }{
		{"public", `public`, "25534628cb1533d4345f03beced7f5fbbc70ba9bfeab7381d1beccb053b2fc26", "c65d6f623bddbe22385d75554706c6d60dfcd2094e42faba22f7c19d1bf4a106"},
		{"quoted schema", `team"blue`, "c7ab923492af867f4e389d4b82ea19bfe292365b9dcd29313b898422ce323e7e", "649200fa61b3fa4f4a7c44c31f6b4debf1216684975e0fe47f203b5a5bfe5712"},
		{"empty schema", "", "7e21cbced5deccb275b1d9f759b2f6ef53c9cb54c9dc9a2a2a8f1716ae46bd5c", "7d26844bfd5e32bd95a13fdf2aaf365501a1466dca79570dfe028d02f938c5f1"},
	} {
		t.Run(schema.name, func(t *testing.T) {
			for _, want := range []struct {
				includeBinding bool
				sha            string
			}{{false, schema.plain}, {true, schema.bound}} {
				db := &migrationAttemptReadCapture{row: executionErrorRow(func(dest ...any) error {
					if len(dest) != 11 {
						return fmt.Errorf("journal read scans %d fields, want 11", len(dest))
					}
					*dest[0].(*string) = "existing-attempt"
					return nil
				})}
				r := migrationOperation{db: db, metadata: migrationMetadata{schema: schema.nameInDB, attemptExists: true}}
				got, err := r.readMigrationAttempt(t.Context(), want.includeBinding)
				if err != nil || got == nil || got.ID != "existing-attempt" {
					t.Fatalf("journal read binding=%t: attempt=%v err=%v", want.includeBinding, got, err)
				}
				if sum := fmt.Sprintf("%x", sha256.Sum256([]byte(db.query))); sum != want.sha {
					t.Fatalf("journal read binding=%t SQL SHA=%s want=%s", want.includeBinding, sum, want.sha)
				}
			}
		})
	}
}

func TestReadMigrationAttemptPublicQueryAllocation(t *testing.T) {
	db := &migrationAttemptReadCapture{row: &mockRow{}}
	r := migrationOperation{db: db, metadata: migrationMetadata{attemptExists: true}}
	for _, includeBinding := range []bool{false, true} {
		measure := func(schema string) float64 {
			r.metadata.schema = schema
			return testing.AllocsPerRun(100, func() {
				if _, err := r.readMigrationAttempt(t.Context(), includeBinding); err != nil {
					t.Fatal(err)
				}
			})
		}
		empty, public := measure(""), measure("public")
		if public > empty {
			t.Errorf("public journal read binding=%t allocated %.0f times; unqualified equivalent allocated %.0f", includeBinding, public, empty)
		}
	}
}
