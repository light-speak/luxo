package advispair

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/light-speak/luxo/pkg/lux/pg/migrate"
)

const recoverySQL = "-- ====== UP ======\nCREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS audit_recover_idx ON public.audit_target(value);\n" +
	"-- ====== DOWN ======\nDROP INDEX CONCURRENTLY IF EXISTS public.audit_recover_idx;\n"

func BenchmarkDurableRecovery(b *testing.B) {
	b.Run("first-journal-and-create-index", benchmarkFirstJournal)
	b.Run("acknowledge-committed-down", func(b *testing.B) { benchmarkRecovery(b, migrate.AcknowledgeMigration) })
	b.Run("record-applied-after-restart", func(b *testing.B) { benchmarkRecovery(b, migrate.RecordApplied) })
	b.Run("retry-after-invalid-index-repair", func(b *testing.B) { benchmarkRecovery(b, migrate.RetryMigration) })
}

func benchmarkFirstJournal(b *testing.B) {
	f := newFixture(b)
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		db := f.open()
		runner := f.prepareMigration(db, recoverySQL)
		f.exec("DROP TABLE IF EXISTS public._luxo_migration_attempt")
		b.StartTimer()

		names, err := runner.Up(f.ctx)

		b.StopTimer()
		f.requireMigration(names, err)
		after := f.recoveryState()
		if after.history != 1 || after.indexOID == 0 || !after.valid {
			b.Fatalf("first journal operation did not create a valid index and history: %+v", after)
		}
		var exists bool
		err = f.observe.QueryRow(f.ctx, "SELECT to_regclass('public._luxo_migration_attempt') IS NOT NULL").Scan(&exists)
		if err != nil || !exists {
			b.Fatalf("first journal publication: exists=%t error=%v", exists, err)
		}
		if pending, err := runner.PendingAttempt(f.ctx); err != nil || pending != nil {
			b.Fatalf("first journal Up left an attempt: %+v %v", pending, err)
		}
		db.Close()
		f.assertClosed()
		b.StartTimer()
	}
}

func benchmarkRecovery(b *testing.B, action migrate.ReconciliationAction) {
	f := newFixture(b)
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		db, runner, attempt := f.prepareRecovery(action)
		before := f.recoveryState()
		b.StartTimer()

		err := runner.Reconcile(f.ctx, attempt.ID, action)

		b.StopTimer()
		if err != nil {
			b.Fatal(err)
		}
		if pending, err := runner.PendingAttempt(f.ctx); err != nil || pending != nil {
			b.Fatalf("reconciliation left a pending attempt: %+v %v", pending, err)
		}
		after := f.recoveryState()
		if after.historyOID != before.historyOID || after.rowVersion != before.rowVersion {
			b.Fatalf("reconciliation changed unrelated state: before=%+v after=%+v", before, after)
		}
		sum := sha256.Sum256([]byte(recoverySQL))
		checksum := hex.EncodeToString(sum[:16])
		switch action {
		case migrate.AcknowledgeMigration:
			if before != after || after.indexOID != 0 || after.history != 0 {
				b.Fatalf("acknowledgement repeated work: before=%+v after=%+v", before, after)
			}
		case migrate.RecordApplied:
			if before.indexOID == 0 || before.indexOID != after.indexOID || !after.valid || after.history != 1 || after.checksum != checksum {
				b.Fatalf("record did not retain the applied index: before=%+v after=%+v", before, after)
			}
		case migrate.RetryMigration:
			if before.indexOID != 0 || after.indexOID == 0 || !after.valid || after.history != 1 || after.checksum != checksum {
				b.Fatalf("retry did not create the intended index: before=%+v after=%+v", before, after)
			}
		}
		db.Close()
		f.assertClosed()
		b.StartTimer()
	}
}

func (f fixture) prepareRecovery(action migrate.ReconciliationAction) (*migrate.PGDB, *migrate.Runner, *migrate.MigrationAttempt) {
	f.t.Helper()
	db := f.open()
	f.exec("DROP TABLE IF EXISTS public._luxo_migration_attempt, public._migrations, public.audit_target")
	f.exec("CREATE TABLE public.audit_target (id integer PRIMARY KEY, value integer NOT NULL, writer_pid integer NOT NULL)")
	f.exec("INSERT INTO public.audit_target VALUES (1, -1, 0)")
	dir := f.t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, migrationName), []byte(recoverySQL), 0600); err != nil {
		f.t.Fatal(err)
	}
	runner := migrate.NewFromDB(db, dir)
	if err := runner.Init(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	switch action {
	case migrate.AcknowledgeMigration:
		f.requireMigration(runner.Up(f.ctx))
		f.requireMigration(runner.Down(f.ctx, 1))
	case migrate.RecordApplied:
		f.exec(`CREATE FUNCTION public.audit_reject_record() RETURNS trigger LANGUAGE plpgsql AS
			$$ BEGIN RAISE EXCEPTION 'audit fixture denied history'; END; $$;
			CREATE TRIGGER audit_reject_record BEFORE INSERT ON public._migrations
			FOR EACH ROW EXECUTE FUNCTION public.audit_reject_record();`)
		f.requireFailedMigration("P0001", runner)
		f.exec("DROP TRIGGER audit_reject_record ON public._migrations; DROP FUNCTION public.audit_reject_record()")
	case migrate.RetryMigration:
		f.exec("INSERT INTO public.audit_target VALUES (2, -1, 0)")
		f.requireFailedMigration("23505", runner)
		broken := f.recoveryState()
		if broken.indexOID == 0 || broken.valid || broken.history != 0 {
			f.t.Fatalf("initial build did not leave the expected invalid index: %+v", broken)
		}
		f.exec("DROP INDEX public.audit_recover_idx; DELETE FROM public.audit_target WHERE id=2")
	}
	runner.Close()
	f.assertClosed()
	db = f.open()
	runner = migrate.NewFromDB(db, dir)
	attempt, err := runner.PendingAttempt(f.ctx)
	committed := action == migrate.AcknowledgeMigration
	direction := "UP"
	if committed {
		direction = "DOWN"
	}
	if err != nil || attempt == nil || attempt.ID == "" || attempt.Name != migrationName ||
		attempt.Direction != direction || attempt.HistoryCommitted != committed {
		f.t.Fatalf("unexpected recovery input: %+v %v", attempt, err)
	}
	return db, runner, attempt
}

func (f fixture) requireMigration(names []string, err error) {
	f.t.Helper()
	if err != nil || !slices.Equal(names, []string{migrationName}) {
		f.t.Fatalf("unexpected migration names=%v error=%v", names, err)
	}
}

func (f fixture) requireFailedMigration(code string, runner *migrate.Runner) {
	f.t.Helper()
	names, err := runner.Up(f.ctx)
	var pgErr *pgconn.PgError
	if len(names) != 0 || !errors.As(err, &pgErr) || pgErr.Code != code {
		f.t.Fatalf("expected real PostgreSQL failure %s, got %v: %v", code, names, err)
	}
}

type recoveryState struct {
	historyOID int64
	indexOID   int64
	history    int
	rowVersion string
	checksum   string
	valid      bool
}

func (f fixture) recoveryState() recoveryState {
	f.t.Helper()
	var state recoveryState
	err := f.observe.QueryRow(f.ctx, `SELECT pg_catalog.to_regclass('public._migrations')::oid::bigint,
		COALESCE(pg_catalog.to_regclass('public.audit_recover_idx')::oid::bigint, 0),
		(SELECT count(*) FROM public._migrations), xmin::text,
		COALESCE((SELECT max(checksum) FROM public._migrations), ''),
		EXISTS (SELECT 1 FROM pg_catalog.pg_index
		        WHERE indexrelid=pg_catalog.to_regclass('public.audit_recover_idx') AND indisvalid AND indisready AND indislive)
		FROM public.audit_target WHERE id=1`).Scan(&state.historyOID, &state.indexOID, &state.history, &state.rowVersion, &state.checksum, &state.valid)
	if err != nil || state.historyOID == 0 {
		f.t.Fatalf("cannot observe recovery state: %+v %v", state, err)
	}
	return state
}
