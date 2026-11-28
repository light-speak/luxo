package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/pg"
	"github.com/light-speak/luxo/pkg/lux/pg/migrate"
)

func TestMigrationReconcileRejectsInvalidArgumentsBeforeConfiguration(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.Mkdir(".env", 0700); err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		name, want string
		args       []string
	}{
		{"missing_action", "choose exactly one", []string{"attempt"}},
		{"missing_id", "attempt ID", []string{"--record-executed"}},
		{"empty_id", "attempt ID", []string{"", "--retry"}},
		{"blank_id", "attempt ID", []string{" \t", "--acknowledge"}},
		{"extra_id", "at most 1", []string{"attempt", "extra", "--retry"}},
		{"exclusive", "none of the others", []string{"attempt", "--retry", "--acknowledge"}},
		{"false_is_supplied", "none of the others", []string{"attempt", "--record-executed=false", "--retry"}},
		{"false_is_not_consent", "must be true", []string{"attempt", "--retry=false"}},
		{"unknown", "unknown flag", []string{"--unsafe"}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			output, err := executeReconcileCLI(t.Context(), sample.args...)
			if err == nil || !strings.Contains(err.Error(), sample.want) || output != "" {
				t.Fatalf("invalid invocation reached setup or wrote output: output=%q error=%v", output, err)
			}
		})
	}
	if output, err := executeReconcileCLI(t.Context()); err == nil || !strings.Contains(err.Error(), ".env") || output != "" {
		t.Fatalf("valid display did not report configuration error: output=%q error=%v", output, err)
	}
}

func TestMigrationReconcileReportsConnectionSetupFailure(t *testing.T) {
	enterCacheGenerationScratch(t)
	t.Setenv("DATABASE_HOST", "127.0.0.1")
	t.Setenv("DATABASE_PORT", "invalid")
	t.Setenv("DATABASE_USER", "fixture")
	t.Setenv("DATABASE_PASSWORD", "fixture")
	assertReconcileCLIError(t, nil, "connect:")
}

func TestMigrationReconcileCLIWithPostgreSQL(t *testing.T) {
	fixture := newMigrationCLIFixture(t)
	t.Run("read_only", func(t *testing.T) {
		db := fixture.open(t)
		assertReconcileCLI(t, nil, "No migration attempt awaits reconciliation")
		readOnly, err := pg.QueryScalar[bool](t.Context(), db, `SELECT
			pg_catalog.to_regclass('_migrations') IS NULL AND
			pg_catalog.to_regclass('_luxo_migration_attempt') IS NULL`)
		if err != nil || !readOnly {
			t.Fatalf("display initialized migration state: %v", err)
		}
		if _, err := pg.Exec(t.Context(), db, `CREATE TABLE _luxo_migration_attempt (wrong_column text)`); err != nil {
			t.Fatal(err)
		}
		assertReconcileCLIError(t, nil, "read migration reconciliation state")
	})
	t.Run("record_and_acknowledge_down", func(t *testing.T) { testReconcileRecordAndAcknowledge(t, fixture.open(t)) })
	t.Run("retry_up", func(t *testing.T) { testReconcileRetry(t, fixture.open(t)) })
	t.Run("reports_completed_rollback_receipt", func(t *testing.T) {
		db := fixture.open(t)
		runner := prepareReconcileCLIFixture(t, false)
		if _, err := runner.Up(t.Context()); err != nil {
			t.Fatal(err)
		}
		output := assertMigrationCLICommand(t, db, newMigrationCLICommand(t), runMigrateDown, nil, 2, "1 migration(s) rolled back", "Rollback receipt")
		attempt := requireReconcileCLIAttempt(t, runner)
		if !attempt.HistoryCommitted || !strings.Contains(output, "luxo migrate reconcile "+attempt.ID+" --acknowledge") {
			t.Fatalf("wrong rollback receipt: %+v, output=%q", attempt, output)
		}
		assertReconcileCLI(t, []string{attempt.ID, "--acknowledge"}, "Migration receipt acknowledged")
	})
}

func executeReconcileCLI(ctx context.Context, args ...string) (string, error) {
	command := newMigrateReconcileCmd()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SilenceErrors, command.SilenceUsage = true, true
	command.SetArgs(append([]string{}, args...))
	err := command.ExecuteContext(ctx)
	return output.String(), err
}

func testReconcileRecordAndAcknowledge(t *testing.T, db *pg.DB) {
	runner := prepareReconcileCLIFixture(t, false)
	if _, err := runner.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	installMigrationDiagnosticTrigger(t, db, "BEFORE DELETE")
	if _, err := runner.Down(t.Context(), 1); err == nil || !strings.Contains(err.Error(), "history remains uncommitted") {
		t.Fatalf("expected a standalone rollback with an unconfirmed history change: %v", err)
	}
	attempt := requireReconcileCLIAttempt(t, runner)
	assertReconcileCLI(t, nil, attempt.ID, "DOWN", attempt.Name, "false", "--record-executed", "--retry")
	assertMigrationDiagnosticState(t, db, 3, false, false)
	assertReconcileCLIError(t, []string{"other-attempt", "--record-executed"}, "no suspended migration matches")
	assertReconcileCLIError(t, []string{attempt.ID, "--acknowledge"}, "history is not yet committed")
	if _, err := pg.Exec(t.Context(), db, "DROP TRIGGER cli_reject_history ON _migrations"); err != nil {
		t.Fatal(err)
	}
	// The rollback's unconditional DROP cannot run again once the index is gone.
	assertReconcileCLI(t, []string{attempt.ID, "--record-executed"}, "Recovery action completed")
	assertMigrationDiagnosticState(t, db, 2, false, false)
	assertReconcileCLI(t, nil, attempt.ID, "DOWN", "true", "--acknowledge")
	assertReconcileCLIError(t, []string{attempt.ID, "--retry"}, "history already committed")
	assertReconcileCLI(t, []string{attempt.ID, "--acknowledge"}, "Migration receipt acknowledged")
	assertReconcileCLI(t, nil, "No migration attempt awaits reconciliation")
	assertMigrationDiagnosticState(t, db, 2, false, false)
}

func testReconcileRetry(t *testing.T, db *pg.DB) {
	runner := prepareReconcileCLIFixture(t, true)
	if _, err := pg.Exec(t.Context(), db, "INSERT INTO cli_rollback_probe VALUES (2, 'keep')"); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(t.Context()); err == nil || !strings.Contains(err.Error(), "success is unconfirmed") {
		t.Fatalf("expected a standalone index statement failure: %v", err)
	}
	attempt := requireReconcileCLIAttempt(t, runner)
	assertReconcileCLI(t, nil, attempt.ID, "UP", attempt.Name, "false")
	assertMigrationDiagnosticState(t, db, 2, true, false)
	if _, err := pg.Exec(t.Context(), db, "DROP INDEX cli_migration_diag; DELETE FROM cli_rollback_probe WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	assertReconcileCLI(t, []string{attempt.ID, "--retry=true"}, "Recovery action completed")
	assertReconcileCLI(t, nil, "No migration attempt awaits reconciliation")
	assertMigrationDiagnosticState(t, db, 3, true, true)
}

func prepareReconcileCLIFixture(t *testing.T, unique bool) *migrate.Runner {
	t.Helper()
	writeMigrationCountFixture(t)
	runner, err := migrate.New(t.Context(), "migrations")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runner.Close)
	if _, err := runner.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	up := "CREATE INDEX CONCURRENTLY cli_migration_diag ON cli_rollback_probe (payload);"
	if unique {
		up = "CREATE UNIQUE INDEX CONCURRENTLY cli_migration_diag ON cli_rollback_probe (payload);"
	}
	script := "-- ====== UP ======\n" + up + "\n-- ====== DOWN ======\nDROP INDEX CONCURRENTLY cli_migration_diag;\n"
	if err := os.WriteFile(filepath.Join("migrations", "003_diagnostic.sql"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	return runner
}

func requireReconcileCLIAttempt(t *testing.T, runner *migrate.Runner) *migrate.MigrationAttempt {
	t.Helper()
	attempt, err := runner.PendingAttempt(t.Context())
	if err != nil || attempt == nil {
		t.Fatalf("expected a pending migration attempt: %v %v", attempt, err)
	}
	return attempt
}

func assertReconcileCLI(t *testing.T, args []string, want ...string) {
	t.Helper()
	output, err := executeReconcileCLI(t.Context(), args...)
	if err != nil {
		t.Fatalf("reconcile %v: %v", args, err)
	}
	for _, value := range want {
		if !strings.Contains(output, value) {
			t.Fatalf("reconcile %v wrote %q, missing %q", args, output, value)
		}
	}
}

func assertReconcileCLIError(t *testing.T, args []string, want string) {
	t.Helper()
	output, err := executeReconcileCLI(t.Context(), args...)
	if err == nil || !strings.Contains(err.Error(), want) || output != "" {
		t.Fatalf("reconcile %v: output=%q error=%v, want %q", args, output, err, want)
	}
}
