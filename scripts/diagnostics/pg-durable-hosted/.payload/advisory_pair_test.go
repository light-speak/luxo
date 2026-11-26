package advispair

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/light-speak/luxo/pkg/lux/pg/migrate"
)

const (
	writer        = "luxo_advispair_writer"
	lockID        = 707813578
	migrationName = "001_audit.sql"
	migrationSQL  = "-- ====== UP ======\nUPDATE audit_target SET value=17, writer_pid=pg_backend_pid() WHERE id=1;\n" +
		"-- ====== DOWN ======\nUPDATE audit_target SET value=0, writer_pid=pg_backend_pid() WHERE id=1;\n"
)

type fixture struct {
	t        testing.TB
	ctx      context.Context
	observe  *pgx.Conn
	url      string
	sessions int
}

func newFixture(t testing.TB) fixture {
	t.Helper()
	address, err := url.Parse(os.Getenv("LUXO_MIGPAIR_DATABASE_URL"))
	if err != nil || address.Scheme != "postgres" || address.Host != "127.0.0.1:5432" || address.User == nil {
		t.Fatal("advisory comparison requires the private loopback PostgreSQL driver")
	}
	role := address.User.Username()
	if !regexp.MustCompile("^luxo_migperf_[a-f0-9]{16}_[ab]$").MatchString(role) || address.Path != "/"+role {
		t.Fatal("advisory comparison requires a task-owned database and role")
	}
	ctx := t.Context()
	observe, err := pgx.Connect(ctx, address.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observe.Close(context.Background()) })

	var owned bool
	var flush, commit string
	err = observe.QueryRow(ctx, "SELECT pg_get_userbyid(datdba)=current_user, current_setting('fsync'), current_setting('synchronous_commit') FROM pg_database WHERE datname=current_database()").Scan(&owned, &flush, &commit)
	if err != nil || !owned || flush != "on" || commit != "on" {
		t.Fatalf("database owner or durability mismatch: %t %s %s: %v", owned, flush, commit, err)
	}
	q := address.Query()
	q.Set("application_name", writer)
	q.Set("pool_max_conns", "1")
	address.RawQuery = q.Encode()

	sessions := 1
	switch os.Getenv("LUXO_ADVISORY_MODEL") {
	case "pooled":
	case "holder":
		sessions = 2
	default:
		t.Fatal("driver must set the expected advisory session model")
	}
	return fixture{t: t, ctx: ctx, observe: observe, url: address.String(), sessions: sessions}
}

func (f fixture) open() *migrate.PGDB {
	f.t.Helper()
	db, err := migrate.NewPGDB(f.ctx, f.url)
	if err != nil {
		f.t.Fatal(err)
	}
	return db
}

func (f fixture) exec(query string) {
	f.t.Helper()
	if _, err := f.observe.Exec(f.ctx, query); err != nil {
		f.t.Fatal(err)
	}
}

type activity struct {
	sessions []int32
	unlocked int32
	held     int32
}

func (f fixture) activity() activity {
	f.t.Helper()
	var seen activity
	err := f.observe.QueryRow(f.ctx, `
		SELECT COALESCE(array_agg(a.pid ORDER BY a.pid), ARRAY[]::integer[]),
		       COALESCE(max(a.pid) FILTER (WHERE a.query LIKE 'SELECT pg_advisory_unlock(%'), 0),
		       COALESCE(max(a.pid) FILTER (WHERE EXISTS (
		         SELECT 1 FROM pg_locks AS l WHERE l.pid=a.pid AND l.locktype='advisory'
		           AND l.granted AND l.classid=0 AND l.objid=$2::oid AND l.objsubid=1)), 0)
		FROM pg_stat_activity AS a WHERE a.datname=current_database() AND a.usename=current_user
		  AND a.application_name=$1`, writer, lockID).Scan(&seen.sessions, &seen.unlocked, &seen.held)
	if err != nil {
		f.t.Fatal(err)
	}
	return seen
}

func (f fixture) assertReleased() activity {
	f.t.Helper()
	seen := f.activity()
	if len(seen.sessions) != f.sessions || seen.unlocked == 0 || seen.held != 0 {
		f.t.Fatalf("unexpected release: %+v, %d expected sessions", seen, f.sessions)
	}
	return seen
}

func (f fixture) waitActivity(match func(activity) bool) (activity, bool) {
	limit := time.Now().Add(500 * time.Millisecond)
	for {
		seen := f.activity()
		if match(seen) {
			return seen, true
		}
		if time.Now().After(limit) {
			return seen, false
		}
		time.Sleep(time.Millisecond)
	}
}

func (f fixture) assertClosed() {
	f.t.Helper()
	seen, ok := f.waitActivity(func(state activity) bool {
		return len(state.sessions) == 0 && state.held == 0
	})
	if !ok {
		f.t.Fatalf("closed adapter still has activity: %+v", seen)
	}
}

func (f fixture) assertWriteReleased(pid int32) {
	f.t.Helper()
	if f.sessions == 1 {
		if seen := f.assertReleased(); seen.unlocked != pid {
			f.t.Fatalf("pooled writer PID %d did not release its lock: %+v", pid, seen)
		}
		return
	}

	seen, ok := f.waitActivity(func(state activity) bool {
		return len(state.sessions) == 1 && state.held == 0 && !slices.Contains(state.sessions, pid)
	})
	if !ok {
		f.t.Fatalf("write holder PID %d still active after migration: %+v", pid, seen)
	}
}

func cycle(t testing.TB, ctx context.Context, db *migrate.PGDB) {
	t.Helper()
	if err := db.AcquireLock(ctx); err != nil {
		t.Fatal(err)
	}
	db.ReleaseLock(ctx)
}

func BenchmarkAdvisoryPair(b *testing.B) {
	b.Run("open-close", benchmarkOpen)
	b.Run("first-lock", benchmarkFirst)
	b.Run("warm-lock", benchmarkWarm)
	b.Run("ordinary-up", func(b *testing.B) { benchmarkUp(b, false) })
	b.Run("up-no-pending", func(b *testing.B) { benchmarkUp(b, true) })
}

func benchmarkOpen(b *testing.B) {
	f := newFixture(b)
	f.open().Close()
	f.assertClosed()
	b.ReportAllocs()
	for b.Loop() {
		f.open().Close()
	}
	f.assertClosed()
}

func benchmarkFirst(b *testing.B) {
	f := newFixture(b)
	warm := f.open()
	cycle(b, f.ctx, warm)
	f.assertReleased()
	warm.Close()
	f.assertClosed()
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		db := f.open()
		b.StartTimer()
		cycle(b, f.ctx, db)
		b.StopTimer()
		f.assertReleased()
		db.Close()
		f.assertClosed()
		b.StartTimer()
	}
}

func benchmarkWarm(b *testing.B) {
	f := newFixture(b)
	db := f.open()
	b.Cleanup(db.Close)
	cycle(b, f.ctx, db)
	open := f.assertReleased()
	b.ReportAllocs()
	for b.Loop() {
		cycle(b, f.ctx, db)
	}
	end := f.assertReleased()
	if open.unlocked != end.unlocked || !slices.Equal(open.sessions, end.sessions) {
		b.Fatalf("warm lock replaced a session: %+v → %+v", open, end)
	}
}

type migrationState struct {
	value   int
	pid     int32
	xmin    string
	history string
}

func (f fixture) prepareMigration(db *migrate.PGDB, sql string) *migrate.Runner {
	f.t.Helper()
	f.exec("DROP TABLE IF EXISTS public._migrations, public.audit_target")
	f.exec("CREATE TABLE public.audit_target (id integer PRIMARY KEY, value integer NOT NULL, writer_pid integer NOT NULL)")
	f.exec("INSERT INTO public.audit_target VALUES (1, -1, 0)")
	dir := f.t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, migrationName), []byte(sql), 0600); err != nil {
		f.t.Fatal(err)
	}
	runner := migrate.NewFromDB(db, dir)
	if err := runner.Init(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	return runner
}

func (f fixture) business() migrationState {
	f.t.Helper()
	var seen migrationState
	err := f.observe.QueryRow(f.ctx, `
		SELECT value, writer_pid, xmin::text,
		       (SELECT COALESCE(string_agg(name || ':' || COALESCE(checksum, ''), ',' ORDER BY id), '') FROM public._migrations)
		FROM public.audit_target WHERE id=1`).Scan(&seen.value, &seen.pid, &seen.xmin, &seen.history)
	if err != nil {
		f.t.Fatal(err)
	}
	return seen
}

func (f fixture) verifyUp(names []string, err error) migrationState {
	f.t.Helper()
	seen := f.business()
	sum := sha256.Sum256([]byte(migrationSQL))
	checksum := hex.EncodeToString(sum[:16])
	if err != nil || !slices.Equal(names, []string{migrationName}) || seen.value != 17 ||
		seen.pid <= 0 || seen.history != migrationName+":"+checksum {
		f.t.Fatalf("unexpected migration: names=%v state=%+v: %v", names, seen, err)
	}
	f.assertWriteReleased(seen.pid)
	return seen
}

func benchmarkUp(b *testing.B, applied bool) {
	f := newFixture(b)
	db := f.open()
	b.Cleanup(db.Close)
	runner := f.prepareMigration(db, migrationSQL)
	before := f.verifyUp(runner.Up(f.ctx))
	reset := func() {
		f.exec("UPDATE public.audit_target SET value=-1,writer_pid=0 WHERE id=1")
		f.exec("DELETE FROM public._migrations")
	}
	var steady activity
	if applied {
		// Both arms start the steady-state leaf after a real untimed no-op.
		// The holder intentionally discards the connection used by the prior write.
		names, err := runner.Up(f.ctx)
		steady = f.verifyNoPending(names, err, before)
	} else {
		reset()
	}
	b.ReportAllocs()
	for b.Loop() {
		names, err := runner.Up(f.ctx)
		b.StopTimer()
		if applied {
			seen := f.verifyNoPending(names, err, before)
			if seen.unlocked != steady.unlocked || !slices.Equal(seen.sessions, steady.sessions) {
				b.Fatalf("no-op migration replaced a session: %+v → %+v", steady, seen)
			}
		} else {
			f.verifyUp(names, err)
			reset()
		}
		b.StartTimer()
	}
}

func (f fixture) verifyNoPending(names []string, err error, before migrationState) activity {
	f.t.Helper()
	if err != nil || len(names) != 0 || f.business() != before {
		f.t.Fatalf("already applied migration was changed: %v: %v", names, err)
	}
	return f.assertReleased()
}

func TestAdvisoryPairSessionRounds(t *testing.T) {
	f := newFixture(t)
	db := f.open()
	t.Cleanup(db.Close)
	initial := f.activity()
	if len(initial.sessions) != 1 || initial.held != 0 {
		t.Fatalf("unexpected session before acquisition: %+v", initial)
	}
	var previous int32
	for round := range 3 {
		if err := db.AcquireLock(f.ctx); err != nil {
			t.Fatal(err)
		}
		locked := f.activity()
		db.ReleaseLock(f.ctx)
		released := f.assertReleased()
		if locked.held == 0 || locked.held != released.unlocked || !slices.Equal(locked.sessions, released.sessions) ||
			(previous != 0 && locked.held != previous) || (locked.held == initial.sessions[0]) != (f.sessions == 1) {
			t.Fatalf("unexpected round %d: initial=%+v locked=%+v released=%+v", round+1, initial, locked, released)
		}
		previous = locked.held
		t.Logf("session-round=%d active=%v holding-PID=%d released-PID=%d", round+1, locked.sessions, locked.held, released.unlocked)
	}
	runner := f.prepareMigration(db, migrationSQL)
	business := f.verifyUp(runner.Up(f.ctx))
	t.Logf("ordinary-writer-PID=%d", business.pid)
}

func TestAdvisoryPairStandaloneWriterHoldsLock(t *testing.T) {
	f := newFixture(t)
	db := f.open()
	t.Cleanup(db.Close)
	const sql = "-- ====== UP ======\nCREATE INDEX CONCURRENTLY audit_holder_idx ON public.audit_target(value);\n" +
		"-- ====== DOWN ======\nDROP INDEX CONCURRENTLY public.audit_holder_idx;\n"
	runner := f.prepareMigration(db, sql)
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	barrier, err := f.observe.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer barrier.Rollback(f.ctx)
	if _, err := barrier.Exec(ctx, "LOCK TABLE public.audit_target IN SHARE UPDATE EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}

	type outcome struct {
		names []string
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		names, err := runner.Up(ctx)
		done <- outcome{names, err}
	}()

	writerPID, lockPID := f.waitForStandaloneWriter(ctx, barrier)
	if writerPID != lockPID {
		t.Fatalf("standalone DDL used PID %d while advisory lock belonged to PID %d", writerPID, lockPID)
	}
	if err := barrier.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err != nil || !slices.Equal(result.names, []string{migrationName}) {
			t.Fatalf("standalone DDL: %v %v", result.names, result.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	f.assertWriteReleased(writerPID)

	var valid bool
	err = f.observe.QueryRow(ctx, `SELECT EXISTS (
	    SELECT 1 FROM pg_catalog.pg_index
	    WHERE indexrelid = 'public.audit_holder_idx'::regclass
	      AND indrelid = 'public.audit_target'::regclass AND indisvalid
	)`).Scan(&valid)
	sum := sha256.Sum256([]byte(sql))
	if err != nil || !valid || f.business().history != migrationName+":"+hex.EncodeToString(sum[:16]) {
		t.Fatalf("standalone DDL and history: valid=%t %v", valid, err)
	}
	t.Logf("standalone-writer-PID=%d standalone-lock-PID=%d", writerPID, lockPID)
}

func (f fixture) waitForStandaloneWriter(ctx context.Context, barrier pgx.Tx) (int32, int32) {
	f.t.Helper()
	for {
		if _, err := barrier.Exec(ctx, "SELECT pg_catalog.pg_stat_clear_snapshot()"); err != nil {
			f.t.Fatal(err)
		}
		var writerPID, lockPID int32
		err := barrier.QueryRow(ctx, `
		    SELECT COALESCE(MAX(a.pid), 0),
		           COALESCE((SELECT MAX(held.pid)
		                     FROM pg_catalog.pg_locks held
		                     JOIN pg_catalog.pg_stat_activity owner ON owner.pid = held.pid
		                     WHERE owner.datname = current_database() AND owner.usename = current_user
		                       AND owner.application_name = $1 AND held.locktype = 'advisory'
		                       AND held.granted AND held.classid = 0 AND held.objid = $2::oid
		                       AND held.objsubid = 1), 0)
		    FROM pg_catalog.pg_stat_activity a
		    WHERE a.datname = current_database() AND a.usename = current_user AND a.application_name = $1
		      AND a.query LIKE 'CREATE INDEX CONCURRENTLY audit_holder_idx %' AND a.wait_event_type = 'Lock'
		`, writer, lockID).Scan(&writerPID, &lockPID)
		if err != nil {
			f.t.Fatal(err)
		}
		if writerPID > 0 {
			return writerPID, lockPID
		}
		select {
		case <-ctx.Done():
			f.t.Fatal("standalone DDL never reached the lock barrier", ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
}

func TestAdvisoryPairContention(t *testing.T) {
	f := newFixture(t)
	db := f.open()
	t.Cleanup(db.Close)
	if err := db.AcquireLock(f.ctx); err != nil {
		t.Fatal(err)
	}
	initial := f.activity()
	if initial.held == 0 || f.independentCanTakeLock() {
		t.Fatalf("the first acquisition did not lock against an independent SQL session: %+v", initial)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 100*time.Millisecond)
	defer cancel()
	err := db.AcquireLock(ctx)
	if f.sessions == 1 {
		if err != nil {
			t.Fatalf("old adapter did not reenter the same SQL session: %v", err)
		}
		db.ReleaseLock(f.ctx)
		t.Log("contention=recursive-session-acquired")
	} else {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("new adapter did not cancel competing acquisition: %v", err)
		}
		t.Log("contention=waiter-deadline")
	}
	if after := f.activity(); after.held != initial.held {
		t.Fatalf("second acquire disturbed the holder: before=%+v after=%+v", initial, after)
	}
	db.ReleaseLock(f.ctx)
	f.assertReleased()
	if !f.independentCanTakeLock() {
		t.Fatal("independent SQL session could not acquire after release")
	}
}

func (f fixture) independentCanTakeLock() bool {
	f.t.Helper()
	var obtained bool
	err := f.observe.QueryRow(f.ctx, "SELECT pg_try_advisory_lock($1)", lockID).Scan(&obtained)
	if err != nil {
		f.t.Fatal(err)
	}
	if obtained {
		var released bool
		err := f.observe.QueryRow(f.ctx, "SELECT pg_advisory_unlock($1)", lockID).Scan(&released)
		if err != nil || !released {
			f.t.Fatalf("independent probe did not release lock: %v", err)
		}
	}
	return obtained
}
