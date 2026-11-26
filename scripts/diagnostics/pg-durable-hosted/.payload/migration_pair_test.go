package migratepair

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/light-speak/luxo/pkg/lux/pg/migrate"
)

const (
	writer = "luxo_migperf_writer"
	first  = "001_audit.sql"
	second = "002_audit.sql"
	third  = "010_audit.sql"

	ordinaryUp   = "UPDATE audit_target SET value=17, writer_pid=pg_backend_pid() WHERE id=1;"
	ordinaryDown = "UPDATE audit_target SET value=0, writer_pid=pg_backend_pid() WHERE id=1;"
	secondUp     = "UPDATE audit_target SET value=18 WHERE id=1;"
	thirdUp      = "UPDATE audit_target SET value=19 WHERE id=1;"
	indexUp      = "CREATE INDEX CONCURRENTLY IF NOT EXISTS audit_target_idx ON audit_target(value);"
	indexDown    = "DROP INDEX CONCURRENTLY IF EXISTS audit_target_idx;"
)

type scenario struct {
	name       string
	operation  string
	index      bool
	existing   bool
	allApplied bool
}

var cases = []scenario{
	{name: "ordinary/up", operation: "up"},
	{name: "ordinary/down", operation: "down"},
	{name: "ordinary/up-existing", operation: "noop"},
	{name: "preview/pending", operation: "preview"},
	{name: "preview/no-pending", operation: "preview", allApplied: true},
	{name: "concurrent/up", operation: "up", index: true},
	{name: "concurrent/down", operation: "down", index: true},
	{name: "concurrent/up-existing-index", operation: "up", index: true, existing: true},
}

type fixture struct {
	t       testing.TB
	ctx     context.Context
	observe *pgx.Conn
	runner  *migrate.Runner
	sha     map[string]string
}

func BenchmarkMigrationPair(b *testing.B) {
	for _, sample := range cases {
		b.Run(sample.name, func(b *testing.B) {
			runPairCase(b, sample)
		})
	}
}

func runPairCase(b *testing.B, sample scenario) {
	f := setup(b, sample)
	run, wantNames, wantSQL := operationFor(f, sample)
	f.reset(sample)
	before := f.inspect()
	names, sql, err := run(f.ctx)
	f.verify(sample, before, wantNames, wantSQL, names, sql, err)
	f.reset(sample)
	before = f.inspect()
	b.ReportAllocs()
	for b.Loop() {
		names, sql, err := run(f.ctx)
		b.StopTimer()
		f.verify(sample, before, wantNames, wantSQL, names, sql, err)
		if sample.operation == "up" || sample.operation == "down" {
			f.reset(sample)
			before = f.inspect()
		}
		b.StartTimer()
	}
}

func operationFor(f fixture, sample scenario) (func(context.Context) ([]string, []string, error), []string, []string) {
	switch sample.operation {
	case "up", "noop":
		want := []string{first}
		if sample.operation == "noop" {
			want = nil
		}
		return func(ctx context.Context) ([]string, []string, error) {
			names, err := f.runner.Up(ctx)
			return names, nil, err
		}, want, nil
	case "down":
		return func(ctx context.Context) ([]string, []string, error) {
			names, err := f.runner.Down(ctx, 1)
			return names, nil, err
		}, []string{first}, nil
	default:
		if sample.allApplied {
			return f.runner.DryRun, nil, nil
		}
		return f.runner.DryRun, []string{second, third}, []string{secondUp, thirdUp}
	}
}

func setup(t testing.TB, sample scenario) fixture {
	t.Helper()
	address, err := url.Parse(os.Getenv("LUXO_MIGPAIR_DATABASE_URL"))
	if err != nil || address.Scheme != "postgres" || address.Host != "127.0.0.1:5432" || address.User == nil {
		t.Fatal("migration pair requires the private loopback PostgreSQL driver")
	}
	role := address.User.Username()
	if !regexp.MustCompile("^luxo_migperf_[a-f0-9]{16}_[ab]$").MatchString(role) || address.Path != "/"+role {
		t.Fatal("migration pair requires a task-owned database and role")
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
	query := address.Query()
	query.Set("application_name", writer)
	address.RawQuery = query.Encode()
	db, err := migrate.NewPGDB(ctx, address.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	dir := t.TempDir()
	f := fixture{t: t, ctx: ctx, observe: observe, runner: migrate.NewFromDB(db, dir), sha: map[string]string{}}
	up, down := ordinaryUp, ordinaryDown
	if sample.index {
		up, down = indexUp, indexDown
	}
	f.write(dir, first, up, down)
	if sample.operation == "preview" {
		f.write(dir, third, thirdUp, ordinaryDown)
		f.write(dir, second, secondUp, ordinaryDown)
	}

	f.exec("DROP TABLE IF EXISTS public._migrations, public.audit_target")
	f.exec("CREATE TABLE public.audit_target (id integer PRIMARY KEY, value integer NOT NULL, writer_pid integer NOT NULL)")
	f.exec("INSERT INTO public.audit_target VALUES (1, -1, 0)")
	if err := f.runner.Init(ctx); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f fixture) write(dir, name, up, down string) {
	f.t.Helper()
	data := []byte("-- ====== UP ======\n" + up + "\n-- ====== DOWN ======\n" + down + "\n")
	if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
		f.t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	f.sha[name] = hex.EncodeToString(sum[:16])
}

func (f fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.observe.Exec(f.ctx, query, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f fixture) reset(sample scenario) {
	value := -1
	switch sample.operation {
	case "down", "noop", "preview":
		value = 17
	}
	f.exec("UPDATE public.audit_target SET value=$1, writer_pid=0 WHERE id=1", value)
	f.exec("DELETE FROM public._migrations")
	if sample.operation == "down" || sample.operation == "noop" || sample.operation == "preview" {
		f.insert(first)
	}
	if sample.allApplied {
		f.insert(second)
		f.insert(third)
	}
	if sample.index {
		f.exec("DROP INDEX IF EXISTS public.audit_target_idx")
		if sample.operation == "down" || sample.existing {
			f.exec("CREATE INDEX audit_target_idx ON public.audit_target(value)")
		}
	}
}

func (f fixture) insert(name string) {
	f.exec("INSERT INTO public._migrations (name, checksum) VALUES ($1, $2)", name, f.sha[name])
}

type state struct {
	value, pid int
	xmin       string
	history    string
	unchanged  string
	historyOID int64
	indexOID   int64
	indexValid bool
	lockHeld   bool
}

func (f fixture) inspect() state {
	f.t.Helper()
	var s state
	err := f.observe.QueryRow(f.ctx, `
		SELECT value, writer_pid, xmin::text,
			(SELECT COALESCE(string_agg(name || ':' || COALESCE(checksum, ''), ',' ORDER BY id), '') FROM public._migrations),
			(SELECT COALESCE(string_agg(id::text || ':' || applied_at::text || ':' || xmin::text, ',' ORDER BY id), '') FROM public._migrations),
			to_regclass('public._migrations')::oid::bigint,
			COALESCE(to_regclass('public.audit_target_idx')::oid::bigint, 0),
			EXISTS (SELECT 1 FROM pg_index WHERE indexrelid=to_regclass('public.audit_target_idx') AND indisvalid AND indisready),
			EXISTS (SELECT 1 FROM pg_locks JOIN pg_stat_activity USING (pid)
				WHERE locktype='advisory' AND datname=current_database() AND application_name=$1)
		FROM public.audit_target WHERE id=1`, writer).
		Scan(&s.value, &s.pid, &s.xmin, &s.history, &s.unchanged, &s.historyOID, &s.indexOID, &s.indexValid, &s.lockHeld)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

func (f fixture) verify(sample scenario, before state, wantNames, wantSQL, names, sql []string, err error) {
	f.t.Helper()
	if err != nil || !slices.Equal(names, wantNames) || !slices.Equal(sql, wantSQL) {
		f.t.Fatalf("result names=%v sql=%q, want names=%v sql=%q: %v", names, sql, wantNames, wantSQL, err)
	}
	got := f.inspect()
	if got.lockHeld || got.historyOID == 0 || got.historyOID != before.historyOID {
		f.t.Fatalf("history identity or advisory lock changed: before=%+v after=%+v", before, got)
	}
	if sample.operation == "noop" || sample.operation == "preview" {
		if got != before {
			f.t.Fatalf("read or no-op changed PostgreSQL data: before=%+v after=%+v", before, got)
		}
		return
	}
	f.verifyWrite(sample, before, got)
}

func (f fixture) verifyWrite(sample scenario, before, got state) {
	f.t.Helper()
	wantValue, wantHistory, wantIndex := 17, first+":"+f.sha[first], sample.index
	if sample.operation == "down" {
		wantValue, wantHistory, wantIndex = 0, "", false
	}
	if sample.index {
		wantValue = before.value
	}
	if got.history != wantHistory || got.value != wantValue || got.indexValid != wantIndex || (got.indexOID != 0) != wantIndex {
		f.t.Fatalf("unexpected terminal state: before=%+v after=%+v", before, got)
	}
	if sample.existing && got.indexOID != before.indexOID {
		f.t.Fatalf("existing index changed identity: before=%+v after=%+v", before, got)
	}
	if sample.index {
		if got.xmin != before.xmin || got.pid != 0 {
			f.t.Fatalf("index migration unexpectedly rewrote a business row: before=%+v after=%+v", before, got)
		}
	} else if got.xmin == before.xmin || got.pid <= 0 {
		f.t.Fatalf("transaction did not write a business row: before=%+v after=%+v", before, got)
	}
}

func TestMigrationPairEmptyPreview(t *testing.T) {
	f := setup(t, scenario{})
	f.exec("DROP TABLE public._migrations")
	names, sql, err := f.runner.DryRun(f.ctx)
	if err != nil || !slices.Equal(names, []string{first}) || !slices.Equal(sql, []string{ordinaryUp}) {
		t.Fatalf("empty preview names=%v sql=%q: %v", names, sql, err)
	}
	var exists bool
	var value int
	err = f.observe.QueryRow(f.ctx, "SELECT to_regclass('public._migrations') IS NOT NULL, value FROM public.audit_target WHERE id=1").Scan(&exists, &value)
	if err != nil || value != -1 {
		t.Fatalf("empty preview changed business value=%d: %v", value, err)
	}
	want := os.Getenv("LUXO_MIGPAIR_EMPTY_HISTORY_CREATE")
	if (want != "yes" && want != "no") || (want == "yes") != exists {
		t.Fatalf("empty preview history exists=%t expected=%q", exists, want)
	}
	t.Logf("empty preview %s, history created=%t", first, exists)
}
