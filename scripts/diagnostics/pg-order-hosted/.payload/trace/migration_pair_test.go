package migrate

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
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	writer = "luxo_migperf_writer"
	first  = "001_audit.sql"

	ordinaryUp   = "UPDATE audit_target SET value=17, writer_pid=pg_backend_pid() WHERE id=1;"
	ordinaryDown = "UPDATE audit_target SET value=0, writer_pid=pg_backend_pid() WHERE id=1;"
	indexUp      = "CREATE INDEX CONCURRENTLY IF NOT EXISTS audit_target_idx ON audit_target(value);"
	indexDown    = "DROP INDEX CONCURRENTLY IF EXISTS audit_target_idx;"
)

type historyReadScenario struct {
	name     string
	cold     bool
	up, down string
}

var historyReadScenarios = []historyReadScenario{
	{name: "concurrent/up-existing-index", up: indexUp, down: indexDown},
	{name: "transaction/up-missing-history", cold: true, up: ordinaryUp, down: ordinaryDown},
}

type historyReadFixture struct {
	t       testing.TB
	ctx     context.Context
	observe *pgx.Conn
	config  string
	dir     string
	sha     string
}

func BenchmarkMigrationPair(b *testing.B) {
	for _, sample := range historyReadScenarios {
		b.Run(sample.name, func(b *testing.B) {
			f := prepareHistoryReadFixture(b, sample)
			f.reset(sample)
			runner := f.open(nil)
			defer func() { runner.Close() }()
			before := f.inspect()
			names, err := runner.Up(f.ctx)
			f.verify(sample, before, names, err)
			f.reset(sample)
			if sample.cold {
				runner.Close()
				runner = f.open(nil)
			}
			before = f.inspect()
			b.ReportAllocs()
			for b.Loop() {
				names, err := runner.Up(f.ctx)
				b.StopTimer()
				f.verify(sample, before, names, err)
				f.reset(sample)
				if sample.cold {
					runner.Close()
					runner = f.open(nil)
				}
				before = f.inspect()
				b.StartTimer()
			}
		})
	}
}

func prepareHistoryReadFixture(t testing.TB, sample historyReadScenario) historyReadFixture {
	t.Helper()
	address, err := url.Parse(os.Getenv("LUXO_MIGPAIR_DATABASE_URL"))
	if err != nil || address.Scheme != "postgres" || address.Host != "127.0.0.1:5432" || address.User == nil {
		t.Fatal("migration pair requires private loopback PostgreSQL")
	}
	role := address.User.Username()
	if !regexp.MustCompile("^luxo_migperf_[a-f0-9]{16}_[ab]$").MatchString(role) || address.Path != "/"+role {
		t.Fatal("migration pair requires its task-owned database and role")
	}
	ctx := t.Context()
	observe, err := pgx.Connect(ctx, address.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observe.Close(context.Background()) })
	var owned bool
	var flush, commit string
	err = observe.QueryRow(ctx, `SELECT pg_get_userbyid(datdba)=current_user AND datname=current_user
		AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolbypassrls,
		current_setting('fsync'), current_setting('synchronous_commit') FROM pg_database, pg_roles
		WHERE datname=current_database() AND rolname=current_user`).Scan(&owned, &flush, &commit)
	if err != nil || !owned || flush != "on" || commit != "on" {
		t.Fatalf("database owner or durability mismatch: %t %s %s: %v", owned, flush, commit, err)
	}
	query := address.Query()
	query.Set("application_name", writer)
	query.Set("default_query_exec_mode", "cache_statement")
	address.RawQuery = query.Encode()
	f := historyReadFixture{t: t, ctx: ctx, observe: observe, config: address.String(), dir: t.TempDir()}
	data := []byte("-- ====== UP ======\n" + sample.up + "\n-- ====== DOWN ======\n" + sample.down + "\n")
	if err := os.WriteFile(filepath.Join(f.dir, first), data, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	f.sha = hex.EncodeToString(sum[:16])
	f.exec("DROP TABLE IF EXISTS public._migrations, public.audit_target")
	f.exec("CREATE TABLE public.audit_target (id integer PRIMARY KEY, value integer NOT NULL, writer_pid integer NOT NULL)")
	f.exec("INSERT INTO public.audit_target VALUES (1, -1, 0)")
	if !sample.cold {
		runner := f.open(nil)
		defer runner.Close()
		if err := runner.Init(ctx); err != nil {
			t.Fatal(err)
		}
		f.exec("CREATE INDEX audit_target_idx ON public.audit_target(value)")
	}
	return f
}

func (f historyReadFixture) open(trace pgx.QueryTracer) *Runner {
	f.t.Helper()
	if trace == nil {
		db, err := NewPGDB(f.ctx, f.config)
		if err != nil {
			f.t.Fatal(err)
		}
		return NewFromDB(db, f.dir)
	}
	config, err := pgxpool.ParseConfig(f.config)
	if err != nil {
		f.t.Fatal(err)
	}
	config.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(f.ctx, config)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := pool.Ping(f.ctx); err != nil {
		pool.Close()
		f.t.Fatal(err)
	}
	return NewFromDB(&PGDB{pool: pool}, f.dir)
}

func (f historyReadFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.observe.Exec(f.ctx, query, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f historyReadFixture) reset(sample historyReadScenario) {
	if !sample.cold {
		f.exec("DELETE FROM public._migrations")
		return
	}
	f.exec("DROP TABLE IF EXISTS public._migrations")
	f.exec("UPDATE public.audit_target SET value=-1, writer_pid=0 WHERE id=1")
}

type historyReadState struct {
	value, pid int
	xmin       string
	history    string
	historyOID int64
	indexOID   int64
	indexValid bool
	lockHeld   bool
}

func (f historyReadFixture) inspect() historyReadState {
	f.t.Helper()
	var s historyReadState
	err := f.observe.QueryRow(f.ctx, `SELECT value, writer_pid, xmin::text,
		COALESCE(to_regclass('public._migrations')::oid::bigint, 0),
		COALESCE(to_regclass('public.audit_target_idx')::oid::bigint, 0),
		EXISTS (SELECT 1 FROM pg_index WHERE indexrelid=to_regclass('public.audit_target_idx') AND indisvalid AND indisready),
		EXISTS (SELECT 1 FROM pg_locks JOIN pg_stat_activity USING (pid)
			WHERE locktype='advisory' AND datname=current_database() AND application_name=$1)
		FROM public.audit_target WHERE id=1`, writer).
		Scan(&s.value, &s.pid, &s.xmin, &s.historyOID, &s.indexOID, &s.indexValid, &s.lockHeld)
	if err != nil {
		f.t.Fatal(err)
	}
	if s.historyOID != 0 {
		if err := f.observe.QueryRow(f.ctx, `SELECT COALESCE(string_agg(name || ':' || COALESCE(checksum, ''), ',' ORDER BY id), '') FROM public._migrations`).Scan(&s.history); err != nil {
			f.t.Fatal(err)
		}
	}
	return s
}

func (f historyReadFixture) verify(sample historyReadScenario, before historyReadState, names []string, err error) {
	f.t.Helper()
	if err != nil || !slices.Equal(names, []string{first}) {
		f.t.Fatalf("migration names=%v: %v", names, err)
	}
	after := f.inspect()
	if before.history != "" || before.lockHeld || after.lockHeld || after.history != first+":"+f.sha || after.historyOID == 0 {
		f.t.Fatalf("history or locks differ: before=%+v after=%+v", before, after)
	}
	if sample.cold {
		if before.historyOID != 0 || before.indexOID != 0 || after.indexOID != 0 || after.indexValid || before.value != -1 || after.value != 17 || after.xmin == before.xmin || after.pid <= 0 {
			f.t.Fatalf("first migration did not create history and commit the business write: before=%+v after=%+v", before, after)
		}
		return
	}
	if before.historyOID != after.historyOID || !before.indexValid || !after.indexValid || before.indexOID == 0 || before.indexOID != after.indexOID || before.value != after.value || before.xmin != after.xmin || after.pid != 0 {
		f.t.Fatalf("existing index or business row changed: before=%+v after=%+v", before, after)
	}
}

const historySessionMetadata = `SELECT selected.schema,
	       pg_catalog.to_regclass(pg_catalog.quote_ident(selected.schema) || '._migrations') IS NOT NULL,
	       pg_catalog.current_setting('standard_conforming_strings') = 'on'
	FROM (SELECT pg_catalog.current_schema() AS schema) AS selected`

func historyReadTraceSQL(sql string) string {
	return strings.Replace(strings.TrimSpace(sql), "public._migrations", "_migrations", 1)
}

type historyReadTraceKey struct{}
type historyReadTrace struct{ queries []string }

func (tr *historyReadTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	sql := historyReadTraceSQL(data.SQL)
	var kind string
	switch {
	case strings.HasPrefix(sql, "CREATE TABLE IF NOT EXISTS _migrations"):
		kind = "init"
	case sql == "SELECT pg_catalog.to_regclass('_migrations') IS NOT NULL":
		kind = "catalog"
	case sql == historySessionMetadata:
		kind = "catalog-session"
	case strings.Contains(sql, "SELECT name, checksum FROM _migrations"):
		kind = "checksum"
	case strings.Contains(sql, "SELECT name, applied_at FROM _migrations"):
		kind = "timestamp"
	case strings.Contains(sql, "JOIN pg_catalog.pg_index"):
		kind = "index-guard"
	}
	if kind == "" {
		return ctx
	}
	return context.WithValue(ctx, historyReadTraceKey{}, kind)
}

func (tr *historyReadTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	kind, ok := ctx.Value(historyReadTraceKey{}).(string)
	if !ok {
		return
	}
	if data.Err != nil {
		var pgErr *pgconn.PgError
		if errors.As(data.Err, &pgErr) {
			kind += ":" + pgErr.Code
		} else {
			kind += ":non-sql-error"
		}
	}
	tr.queries = append(tr.queries, kind)
}

func TestMigrationPairSQLControl(t *testing.T) {
	arm := os.Getenv("LUXO_MIGPAIR_ARM")
	warm := map[string]string{"A": "catalog,checksum,timestamp,index-guard", "B": "catalog-session,checksum,timestamp,index-guard"}
	cold := map[string]string{"A": "catalog,init", "B": "catalog-session,init"}
	if warm[arm] == "" {
		t.Fatal("missing source arm")
	}
	for _, sample := range historyReadScenarios {
		t.Run(sample.name, func(t *testing.T) {
			f := prepareHistoryReadFixture(t, sample)
			f.reset(sample)
			before := f.inspect()
			var trace historyReadTrace
			runner := f.open(&trace)
			defer runner.Close()
			names, err := runner.Up(f.ctx)
			f.verify(sample, before, names, err)
			want := warm[arm]
			if sample.cold {
				want = cold[arm]
			}
			if got := strings.Join(trace.queries, ","); got != want {
				t.Fatalf("workload query order=%s want=%s", got, want)
			}
			t.Logf("pgx-order %s %s", sample.name, want)
		})
	}
}

func TestMigrationPairHistoryErrorControl(t *testing.T) {
	f := prepareHistoryReadFixture(t, historyReadScenarios[0])
	f.exec("ALTER TABLE public._migrations DROP COLUMN checksum")
	var trace historyReadTrace
	runner := f.open(&trace)
	defer runner.Close()
	names, err := runner.Up(f.ctx)
	var pgErr *pgconn.PgError
	if len(names) != 0 || !errors.As(err, &pgErr) || pgErr.Code != "42703" || len(trace.queries) == 0 || trace.queries[len(trace.queries)-1] != "checksum:42703" {
		t.Fatalf("legacy history failure names=%v query=%v error=%v", names, trace.queries, err)
	}
	var count int
	if err := f.observe.QueryRow(f.ctx, "SELECT COUNT(*) FROM public._migrations").Scan(&count); err != nil || count != 0 {
		t.Fatalf("legacy history mutated: count=%d error=%v", count, err)
	}
	t.Logf("pgx-error legacy-history %s", pgErr.Code)
}
