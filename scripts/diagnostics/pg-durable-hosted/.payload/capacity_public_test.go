package advispair

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/light-speak/luxo/pkg/lux/pg/migrate"
)

func BenchmarkPGCapacityPublic(b *testing.B) {
	for _, sample := range []struct {
		name string
		sql  func(fixture, *migrate.PGDB)
	}{
		{"warm-pool-query-row", capacityPoolQueryRow},
		{"warm-pool-query", capacityPoolQuery},
		{"warm-pool-exec", capacityPoolExec},
		{"warm-pool-begin-rollback", capacityPoolTransaction},
	} {
		b.Run(sample.name, func(b *testing.B) {
			f := newFixture(b)
			db := f.open()
			b.Cleanup(db.Close)
			capacityPoolQueryRow(f, db)
			b.ReportAllocs()
			for b.Loop() {
				sample.sql(f, db)
			}
		})
	}
	b.Run("first-public-after-clean-noop", func(b *testing.B) { benchmarkCapacityBootstrap(b, false) })
	b.Run("first-public-after-clean-write", func(b *testing.B) { benchmarkCapacityBootstrap(b, true) })
}

func capacityPoolQueryRow(f fixture, db *migrate.PGDB) {
	var n int
	if err := db.QueryRow(f.ctx, "SELECT 1").Scan(&n); err != nil || n != 1 {
		f.t.Fatalf("public QueryRow: %d, %v", n, err)
	}
}

func capacityPoolQuery(f fixture, db *migrate.PGDB) {
	rows, err := db.Query(f.ctx, "SELECT 1")
	if err != nil {
		f.t.Fatal(err)
	}
	var n int
	found := rows.Next()
	if found {
		err = rows.Scan(&n)
	}
	rows.Close()
	if state, ok := rows.(interface{ Err() error }); err == nil && ok {
		err = state.Err()
	}
	if !found || n != 1 || err != nil {
		f.t.Fatalf("public Query: %v, %d, %v", found, n, err)
	}
}

func capacityPoolExec(f fixture, db *migrate.PGDB) {
	if err := db.Exec(f.ctx, "SELECT 1"); err != nil {
		f.t.Fatal(err)
	}
}

func capacityPoolTransaction(f fixture, db *migrate.PGDB) {
	tx, err := db.Begin(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := tx.Rollback(f.ctx); err != nil {
		f.t.Fatal(err)
	}
}

const capacityWriteName = "002_public_holder.sql"

func benchmarkCapacityBootstrap(b *testing.B, write bool) {
	f := newFixture(b)
	dir, before := prepareAppliedFirstQuery(f)
	if write {
		sql := "-- ====== UP ======\nUPDATE audit_target SET value=18, writer_pid=pg_backend_pid() WHERE id=1;\n" +
			"-- ====== DOWN ======\nUPDATE audit_target SET value=17, writer_pid=pg_backend_pid() WHERE id=1;\n"
		if err := os.WriteFile(filepath.Join(dir, capacityWriteName), []byte(sql), 0600); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		db := f.open()
		b.Cleanup(db.Close)
		prepareCapacityBootstrap(f, db, dir, before, write)
		b.StartTimer()
		capacityPoolQueryRow(f, db)
		b.StopTimer()
		db.Close()
		f.assertClosed()
		if write {
			f.exec("DELETE FROM public._migrations WHERE name='002_public_holder.sql'; UPDATE public.audit_target SET value=17 WHERE id=1")
		}
		b.StartTimer()
	}
}

func prepareCapacityBootstrap(f fixture, db *migrate.PGDB, dir string, before migrationState, write bool) {
	names, err := migrate.NewFromDB(db, dir).Up(f.ctx)
	if !write {
		verifyAppliedFirstQuery(f, before, names, err)
		return
	}
	if err != nil || !slices.Equal(names, []string{capacityWriteName}) {
		f.t.Fatalf("write primer: %v, %v", names, err)
	}
	var value, pid int32
	err = f.observe.QueryRow(f.ctx, "SELECT value, writer_pid FROM public.audit_target WHERE id=1").Scan(&value, &pid)
	if err != nil || value != 18 || pid <= 0 {
		f.t.Fatalf("write primer lost stored result: %d %d %v", value, pid, err)
	}
	state, ok := f.waitActivity(func(state activity) bool { return !slices.Contains(state.sessions, pid) && state.held == 0 })
	if !ok {
		f.t.Fatalf("write primer still has a private connection: %+v", state)
	}
}

func TestPGCapacityPublicKeepsSpareSession(t *testing.T) {
	f := newFixture(t)
	dir, before := prepareAppliedFirstQuery(f)
	db := f.open()
	t.Cleanup(db.Close)
	prepareCapacityBootstrap(f, db, dir, before, false)
	standby := f.activity()
	pid, err := usedPoolPID(f, db)
	got := f.activity()
	if err != nil || standby.unlocked <= 0 || pid <= 0 || pid == standby.unlocked || got.unlocked != standby.unlocked || got.held != 0 || len(got.sessions) != 2 {
		t.Fatalf("public pool and warm lock roles changed: %d %+v -> %+v, %v", pid, standby, got, err)
	}
}

// The ordinary two-session observer would itself exhaust a role limited to one.
// This smoke uses only the adapter's connection by construction.
func TestPGCapacityLimitedRole(t *testing.T) {
	address, err := url.Parse(os.Getenv("LUXO_MIGPAIR_DATABASE_URL"))
	if err != nil || address.User == nil || address.Scheme != "postgres" || address.Host != "127.0.0.1:5432" {
		t.Fatal("capacity smoke requires the task-owned loopback database")
	}
	role := address.User.Username()
	if !regexp.MustCompile(`^luxo_migperf_[a-f0-9]{16}_[ab]$`).MatchString(role) || address.Path != "/"+role {
		t.Fatal("invalid capacity smoke role/database ownership")
	}
	params := address.Query()
	params.Set("pool_max_conns", "1")
	address.RawQuery = params.Encode()
	db, err := migrate.NewPGDB(t.Context(), address.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	dir := t.TempDir()
	path := filepath.Join(dir, migrationName)
	sql := []byte("-- ====== UP ======\nCREATE TABLE single_role_probe (id integer);\n-- ====== DOWN ======\nDROP TABLE single_role_probe;\n")
	if err := os.WriteFile(path, sql, 0600); err != nil {
		t.Fatal(err)
	}
	r := migrate.NewFromDB(db, dir)
	names, statements, err := r.DryRun(t.Context())
	if err != nil || !slices.Equal(names, []string{migrationName}) || len(statements) != 1 {
		t.Fatalf("capacity preview: %v, %v", names, err)
	}
	var limit int
	var super bool
	if err := db.QueryRow(t.Context(), "SELECT rolconnlimit, rolsuper FROM pg_roles WHERE rolname=current_user").Scan(&limit, &super); err != nil || super || limit != 1 {
		t.Fatalf("smoke role is not constrained: %d %v %v", limit, super, err)
	}
	names, err = r.Up(t.Context())
	switch os.Getenv("LUXO_CAPACITY_EXPECT") {
	case "server-limit":
		pgErr, ok := errors.AsType[*pgconn.PgError](err)
		if !ok || pgErr.SQLState() != "53300" || len(names) != 0 {
			t.Fatalf("old capacity case must preserve SQLSTATE 53300: %v, %v", names, err)
		}
		t.Log("role-limit historical handshake=53300")
	case "available":
		assertCapacityRecovery(t, db, r, names, err)
	default:
		t.Fatal("driver must select the role-limit outcome")
	}
}

func assertCapacityRecovery(t *testing.T, db *migrate.PGDB, r *migrate.Runner, names []string, err error) {
	t.Helper()
	if err != nil || !slices.Equal(names, []string{migrationName}) {
		t.Fatalf("migration did not use available capacity: %v %v", names, err)
	}
	var table bool
	if err := db.QueryRow(t.Context(), "SELECT to_regclass('public.single_role_probe') IS NOT NULL").Scan(&table); err != nil || !table {
		t.Fatalf("public pool could not query the new table: %v %v", table, err)
	}
	if names, err := r.Up(t.Context()); err != nil || len(names) != 0 {
		t.Fatalf("repeated Up changed history: %v %v", names, err)
	}
	if names, sql, err := r.DryRun(t.Context()); err != nil || len(names) != 0 || len(sql) != 0 {
		t.Fatalf("public preview after no-op: %v %v", names, err)
	}
	t.Log("role-limit corrected preview/up/public/noop=ok")
}
