package advispair

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/pg/migrate"
)

func prepareAppliedFirstQuery(f fixture) (string, migrationState) {
	f.t.Helper()
	f.exec("DROP TABLE IF EXISTS public._luxo_migration_attempt, public._migrations, public.audit_target")
	f.exec("CREATE TABLE public.audit_target (id integer PRIMARY KEY, value integer NOT NULL, writer_pid integer NOT NULL)")
	f.exec("INSERT INTO public.audit_target VALUES (1, -1, 0)")
	dir := f.t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, migrationName), []byte(migrationSQL), 0600); err != nil {
		f.t.Fatal(err)
	}
	seed := f.open()
	names, err := migrate.NewFromDB(seed, dir).Up(f.ctx)
	seed.Close()
	f.assertClosed()
	state := f.business()
	checksum := sha256.Sum256([]byte(migrationSQL))
	if err != nil || !slices.Equal(names, []string{migrationName}) || state.value != 17 || state.pid <= 0 ||
		state.history != migrationName+":"+hex.EncodeToString(checksum[:16]) {
		f.t.Fatalf("seed did not produce the expected applied migration: %v %+v %v", names, state, err)
	}
	assertNoFirstQueryJournal(f)
	return dir, state
}

func assertNoFirstQueryJournal(f fixture) {
	f.t.Helper()
	var noJournal bool
	err := f.observe.QueryRow(f.ctx, "SELECT to_regclass('public._luxo_migration_attempt') IS NULL").Scan(&noJournal)
	if err != nil || !noJournal {
		f.t.Fatalf("first query fixture must preserve a missing reconciliation journal: %v %v", noJournal, err)
	}
}

func verifyAppliedFirstQuery(f fixture, before migrationState, names []string, err error) {
	f.t.Helper()
	if err != nil || len(names) != 0 || f.business() != before {
		f.t.Fatalf("already applied migration changed: %v %v", names, err)
	}
}

func usedPoolPID(f fixture, db *migrate.PGDB) (int32, error) {
	var pid int32
	err := db.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid)
	return pid, err
}

func TestDurableFirstQuerySessions(t *testing.T) {
	arm := os.Getenv("LUXO_DURABLE_ARM")
	if arm != "P" && arm != "Q" {
		t.Fatal("driver must select P or Q")
	}
	for _, tc := range []struct {
		name string
		used bool
	}{
		{name: "clean-ping"},
		{name: "used-pool", used: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			dir, before := prepareAppliedFirstQuery(f)
			db := f.open()
			t.Cleanup(db.Close)
			initial := f.activity()
			if len(initial.sessions) != 1 || initial.held != 0 {
				t.Fatalf("initial Ping did not leave one unlocked session: %+v", initial)
			}
			if tc.used {
				pid, err := usedPoolPID(f, db)
				if err != nil || pid != initial.sessions[0] {
					t.Fatalf("public query did not use the Ping session: PID=%d, %v", pid, err)
				}
			}
			runner := migrate.NewFromDB(db, dir)
			names, err := runner.Up(f.ctx)
			verifyAppliedFirstQuery(f, before, names, err)
			got := f.activity()
			separate := tc.used
			expectedCount := 1
			if separate {
				expectedCount++
			}
			if len(got.sessions) != expectedCount || got.held != 0 || got.unlocked == 0 ||
				(got.unlocked != initial.sessions[0]) != separate {
				t.Fatalf("unexpected first migration session: %+v -> %+v", initial, got)
			}
			names, err = runner.Up(f.ctx)
			verifyAppliedFirstQuery(f, before, names, err)
			warm := f.activity()
			if warm.held != 0 || warm.unlocked != got.unlocked || !slices.Equal(warm.sessions, got.sessions) {
				t.Fatalf("read-only warm migration replaced a session: %+v -> %+v", got, warm)
			}
			t.Logf("first-query arm=%s case=%s ping-PID=%d first-holder-PID=%d warm-holder-PID=%d sessions=%v",
				arm, tc.name, initial.sessions[0], got.unlocked, warm.unlocked, warm.sessions)
			db.Close()
			f.assertClosed()
			assertNoFirstQueryJournal(f)
		})
	}
}

func BenchmarkDurableFirstQuery(b *testing.B) {
	b.Run("cold-open-ping-up", func(b *testing.B) { benchmarkFreshFirstQuery(b, false) })
	b.Run("already-used-pool-up", func(b *testing.B) { benchmarkFreshFirstQuery(b, true) })
	b.Run("warm-read-only-holder-up", benchmarkWarmFirstQuery)
	b.Run("used-holder-first-reuse-up", benchmarkUsedHolderFirstReuse)
}

func benchmarkFreshFirstQuery(b *testing.B, used bool) {
	f := newFixture(b)
	dir, before := prepareAppliedFirstQuery(f)
	b.ReportAllocs()
	for b.Loop() {
		if used {
			b.StopTimer()
		}
		db := f.open()
		if used {
			if pid, err := usedPoolPID(f, db); err != nil || pid <= 0 {
				db.Close()
				b.Fatalf("prepare already-used pool: PID=%d, %v", pid, err)
			}
			b.StartTimer()
		}
		names, err := migrate.NewFromDB(db, dir).Up(f.ctx)
		b.StopTimer()
		db.Close()
		f.assertClosed()
		verifyAppliedFirstQuery(f, before, names, err)
		b.StartTimer()
	}
	assertNoFirstQueryJournal(f)
}

func benchmarkWarmFirstQuery(b *testing.B) {
	f := newFixture(b)
	dir, before := prepareAppliedFirstQuery(f)
	db := f.open()
	b.Cleanup(db.Close)
	runner := migrate.NewFromDB(db, dir)
	names, err := runner.Up(f.ctx)
	verifyAppliedFirstQuery(f, before, names, err)
	initial := f.activity()
	if initial.unlocked == 0 || initial.held != 0 {
		b.Fatalf("warm primer did not release the lock: %+v", initial)
	}
	b.ReportAllocs()
	for b.Loop() {
		names, err = runner.Up(f.ctx)
		b.StopTimer()
		verifyAppliedFirstQuery(f, before, names, err)
		got := f.activity()
		if got.held != 0 || got.unlocked != initial.unlocked || !slices.Equal(got.sessions, initial.sessions) {
			b.Fatalf("read-only warm migration replaced a session: %+v -> %+v", initial, got)
		}
		b.StartTimer()
	}
	db.Close()
	f.assertClosed()
	assertNoFirstQueryJournal(f)
}
