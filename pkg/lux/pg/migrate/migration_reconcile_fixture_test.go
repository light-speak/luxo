package migrate

import (
	"context"
	"crypto/rand"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const auditBase = "001_audit_base.sql"
const auditChange = "002_audit_change.sql"

type migrationReconcileFixture struct {
	*Runner
	url    string
	schema string
}

func migrationAuditRunner(t *testing.T) *migrationReconcileFixture {
	t.Helper()
	skipIfNoDB(t)
	databaseURL, err := url.Parse(BuildDatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(t.Context(), databaseURL.String())
	if err != nil {
		t.Fatal(err)
	}
	schema := "luxo_reconcile_" + strings.ToLower(rand.Text())
	if _, err := conn.Exec(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		conn.Close(t.Context())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+", "+schema+"_alt CASCADE"); err != nil {
			t.Error(err)
		}
		if err := conn.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	if _, err := conn.Exec(t.Context(), "CREATE SCHEMA "+schema+"_alt"); err != nil {
		t.Fatal(err)
	}
	query := databaseURL.Query()
	query.Set("search_path", schema)
	databaseURL.RawQuery = query.Encode()
	fixture := &migrationReconcileFixture{url: databaseURL.String(), schema: schema}
	fixture.Runner = fixture.open(t, t.TempDir())
	writeMigration(t, fixture.dir, auditBase,
		"CREATE TABLE audit_users (id int PRIMARY KEY, label text); INSERT INTO audit_users VALUES (1, 'one');",
		"DROP TABLE audit_users;")
	auditUp(t, fixture.Runner, 1)
	return fixture
}

func (f *migrationReconcileFixture) open(t *testing.T, dir string) *Runner {
	t.Helper()
	db, err := NewPGDB(t.Context(), f.url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return NewFromDB(db, dir)
}

// Keep fault injection and every migration statement on the real lock connection.
type migrationFixtureSession struct {
	pgMigrationQueries[*pgx.Conn]
	t *testing.T
}

func (d *migrationFixtureSession) AcquireLock(ctx context.Context) error {
	return d.Exec(ctx, "SELECT pg_advisory_lock($1)", pgLockID)
}

func (d *migrationFixtureSession) ReleaseLock(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var unlocked bool
	if err := d.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", pgLockID).Scan(&unlocked); err != nil || !unlocked {
		d.t.Errorf("release migration fixture lock: unlocked=%t, error=%v", unlocked, err)
	}
}

func (d *migrationFixtureSession) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.conn.Close(ctx); err != nil {
		d.t.Error(err)
	}
}

func (*migrationFixtureSession) MigrationSessionBound() bool { return true }

func (f *migrationReconcileFixture) openBoundSession(t *testing.T) MigrationSessionDB {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), f.url)
	if err != nil {
		t.Fatal(err)
	}
	session := &migrationFixtureSession{pgMigrationQueries: pgMigrationQueries[*pgx.Conn]{conn: conn}, t: t}
	t.Cleanup(session.Close)
	return session
}

func auditExec(t *testing.T, db DB, sql string) {
	t.Helper()
	if err := db.Exec(t.Context(), sql); err != nil {
		t.Fatal(err)
	}
}

func auditUp(t *testing.T, r *Runner, count int) {
	t.Helper()
	ids, err := r.Up(t.Context())
	if err != nil || len(ids) != count {
		t.Fatalf("up: ids=%v err=%v", ids, err)
	}
}

func auditSQLState(t *testing.T, err error, code string) {
	t.Helper()
	t.Logf("runner returned %q", err)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code {
		t.Fatalf("expected SQLSTATE %s, got %v", code, err)
	}
}

func auditRejectHistory(t *testing.T, db DB) {
	t.Helper()
	auditExec(t, db, `CREATE FUNCTION audit_reject_record() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN RAISE EXCEPTION 'audit fixture denied history'; END; $$;
		CREATE TRIGGER audit_reject_record BEFORE INSERT OR DELETE ON _migrations
		FOR EACH ROW EXECUTE FUNCTION audit_reject_record();`)
}

func auditIndexAndHistory(t *testing.T, db DB, index bool, history int) {
	t.Helper()
	var gotIndex, valid bool
	var gotHistory int
	err := db.QueryRow(t.Context(), `SELECT
		pg_catalog.to_regclass('audit_idx') IS NOT NULL,
		COALESCE((SELECT indisvalid FROM pg_index WHERE indexrelid = pg_catalog.to_regclass('audit_idx')), false),
		(SELECT count(*) FROM _migrations)`).Scan(&gotIndex, &valid, &gotHistory)
	if err != nil || gotIndex != index || valid != index || gotHistory != history {
		t.Fatalf("observed index=%t valid=%t history=%d err=%v", gotIndex, valid, gotHistory, err)
	}
	t.Logf("index=%t valid=%t history=%d", gotIndex, valid, gotHistory)
}
