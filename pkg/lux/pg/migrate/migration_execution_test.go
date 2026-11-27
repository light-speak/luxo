package migrate

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const executionIndex = "003_execution_index.sql"

func interruptStandaloneBeforeDispatch(t *testing.T, fixture *migrationReconcileFixture, sessionSQL, indexSQL string) *MigrationAttempt {
	t.Helper()
	writeMigration(t, fixture.dir, auditChange, sessionSQL, "SELECT 1;")
	writeMigration(t, fixture.dir, executionIndex, indexSQL, "DROP INDEX CONCURRENTLY audit_idx;")
	session := fixture.openBoundSession(t)
	defer session.Close()
	fault := errors.New("migration fixture lost the persisted intent reply")
	interrupted := NewFromDB(migrationLostIntentReplyDB{MigrationSessionDB: session, fault: fault}, fixture.dir)
	done, err := interrupted.Up(t.Context())
	if !slices.Equal(done, []string{auditChange}) || !errors.Is(err, fault) {
		t.Fatalf("interrupted migration: done=%v, error=%v", done, err)
	}
	attempt, err := fixture.PendingAttempt(t.Context())
	if err != nil || attempt == nil || attempt.Name != executionIndex || attempt.HistoryCommitted {
		t.Fatalf("pending index: %+v, error=%v", attempt, err)
	}
	return attempt
}

func TestIntegrationRetryRejectsChangedRelationBinding(t *testing.T) {
	fixture := migrationAuditRunner(t)
	sessionSQL := "SET search_path TO " + fixture.schema + "_alt, " + fixture.schema
	attempt := interruptStandaloneBeforeDispatch(t, fixture, sessionSQL,
		"CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);")
	auditExec(t, fixture.db, "CREATE TABLE "+fixture.schema+"_alt.audit_users (label text)")
	restarted := fixture.open(t, fixture.dir)
	if err := restarted.Reconcile(t.Context(), attempt.ID, RetryMigration); err == nil || !strings.Contains(err.Error(), "resolves to a different identity") {
		t.Errorf("retry did not reject the shadowing table identity: %v", err)
	}
	var shadowIndex bool
	err := restarted.db.QueryRow(t.Context(), "SELECT pg_catalog.to_regclass($1) IS NOT NULL", fixture.schema+"_alt.audit_idx").Scan(&shadowIndex)
	if err != nil || shadowIndex {
		t.Errorf("shadow index exists=%t, error=%v", shadowIndex, err)
	}
	auditIndexAndHistory(t, restarted.db, false, 2)
	pending, err := restarted.PendingAttempt(t.Context())
	if err != nil || pending == nil || pending.ID != attempt.ID {
		t.Fatalf("retried attempt was lost: %+v, error=%v", pending, err)
	}
}

func TestIntegrationRetryWithoutUnrelatedTemporarySchema(t *testing.T) {
	fixture := migrationAuditRunner(t)
	attempt := interruptStandaloneBeforeDispatch(t, fixture, "CREATE TEMP TABLE irrelevant_temp (id int);",
		"CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);")
	restarted := fixture.open(t, fixture.dir)
	if err := restarted.Reconcile(t.Context(), attempt.ID, RetryMigration); err != nil {
		t.Fatal(err)
	}
	auditIndexAndHistory(t, restarted.db, true, 3)
}

func TestIntegrationRetrySettingsPrepare(t *testing.T) {
	for _, sample := range []struct {
		name, mode string
		prepares   int
	}{
		{name: "default"},
		{name: "cache-statement", mode: "cache_statement"},
		{name: "cache-describe", mode: "cache_describe", prepares: 1},
		{name: "describe-exec", mode: "describe_exec", prepares: 1},
		{name: "exec", mode: "exec"},
		{name: "simple-protocol", mode: "simple_protocol"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			fixture, attempt := retrySettingsFixture(t)
			dsn, err := url.Parse(fixture.url)
			if err != nil {
				t.Fatal(err)
			}
			if sample.mode != "" {
				options := dsn.Query()
				options.Set("default_query_exec_mode", sample.mode)
				dsn.RawQuery = options.Encode()
			}
			fixture.url = dsn.String()
			restarted := fixture.open(t, fixture.dir)
			trace := traceRetrySettings(t, restarted)
			if err := restarted.Reconcile(t.Context(), attempt.ID, RetryMigration); err != nil {
				t.Fatal(err)
			}
			auditIndexAndHistory(t, restarted.db, false, 3)
			assertRetrySettingsIndex(t, restarted, fixture.schema+"_alt", true)
			trace.check(t, sample.prepares, "settings", "settings-done", "snapshot", "external-ddl", "history", "receipt", "acknowledge")
			if trace.snapshotPrepares != sample.prepares {
				t.Errorf("snapshot prepares = %d, want %d", trace.snapshotPrepares, sample.prepares)
			}
			if trace.receiptPrepares != sample.prepares || trace.receiptWritesInTx != 1 {
				t.Errorf("receipt prepares = %d, want %d; writes in transaction = %d, want 1",
					trace.receiptPrepares, sample.prepares, trace.receiptWritesInTx)
			}
			if trace.historyPrepares != sample.prepares || trace.acknowledgePrepares != sample.prepares {
				t.Errorf("history and acknowledgement prepares = %d/%d, want %d/%d",
					trace.historyPrepares, trace.acknowledgePrepares, sample.prepares, sample.prepares)
			}
			trace.checkLookupPrepares(t, sample.prepares)
		})
	}
}

func TestIntegrationRetryReceiptUpdateRollsBackHistory(t *testing.T) {
	fixture, attempt := retrySettingsFixture(t)
	const receiptRemovedOnInsert = `
		CREATE FUNCTION audit_remove_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			DELETE FROM _luxo_migration_attempt WHERE singleton;
			RETURN NEW;
		END
		$$;
		CREATE TRIGGER audit_remove_receipt BEFORE INSERT ON _migrations
		FOR EACH ROW EXECUTE FUNCTION audit_remove_receipt();`
	auditExec(t, fixture.db, receiptRemovedOnInsert)
	restarted := fixture.open(t, fixture.dir)
	err := restarted.Reconcile(t.Context(), attempt.ID, RetryMigration)
	if !migrationHasSQLState(err, "22012") || !strings.Contains(err.Error(), "history remains uncommitted") {
		t.Fatalf("receipt update should fail with 22012 and leave history uncommitted: %v", err)
	}
	auditIndexAndHistory(t, restarted.db, false, 2)
	assertRetrySettingsIndex(t, restarted, fixture.schema+"_alt", true)
	pending, err := restarted.PendingAttempt(t.Context())
	if err != nil || pending == nil || pending.ID != attempt.ID || pending.HistoryCommitted {
		t.Fatalf("rollback did not retain the original uncommitted receipt: %+v, %v", pending, err)
	}
}

func TestIntegrationRetryDownRetainsCommittedReceipt(t *testing.T) {
	ctx := t.Context()
	fixture := migrationAuditRunner(t)
	writeMigration(t, fixture.dir, auditChange, "CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);",
		"DROP INDEX CONCURRENTLY audit_idx;")
	auditUp(t, fixture.Runner, 1)
	session := fixture.openBoundSession(t)
	defer session.Close()
	fault := errors.New("migration fixture lost the persisted DOWN intent reply")
	lost := NewFromDB(migrationLostIntentReplyDB{MigrationSessionDB: session, fault: fault}, fixture.dir)
	if done, err := lost.Down(ctx, 1); len(done) != 0 || !errors.Is(err, fault) {
		t.Fatalf("interrupted DOWN: done=%v, error=%v", done, err)
	}
	auditIndexAndHistory(t, fixture.db, true, 2)
	attempt, err := fixture.PendingAttempt(ctx)
	if err != nil || attempt == nil || attempt.Direction != "DOWN" || attempt.HistoryCommitted {
		t.Fatalf("uncommitted DOWN receipt: %+v, %v", attempt, err)
	}
	restarted := fixture.open(t, fixture.dir)
	trace := traceRetrySettings(t, restarted)
	if err := restarted.Reconcile(ctx, attempt.ID, RetryMigration); err != nil {
		t.Fatal(err)
	}
	trace.checkLookupPrepares(t, 0)
	auditIndexAndHistory(t, restarted.db, false, 1)
	committed, err := restarted.PendingAttempt(ctx)
	if err != nil || committed == nil || committed.ID != attempt.ID || !committed.HistoryCommitted {
		t.Fatalf("recovered DOWN receipt: %+v, %v", committed, err)
	}
}

func TestIntegrationRetrySettingsRejectionsRetireHolder(t *testing.T) {
	for _, sample := range []struct {
		name, assignment string
		snapshot         bool
	}{
		{"different-role", "execution_role = (SELECT oid::text FROM pg_catalog.pg_roles WHERE rolname = 'pg_read_all_data')", true},
		{"invalid-search-path", `execution_search_path = '"unterminated'`, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			fixture, attempt := retrySettingsFixture(t)
			restarted := fixture.open(t, fixture.dir)
			auditExec(t, restarted.db, "UPDATE _luxo_migration_attempt SET "+sample.assignment+" WHERE singleton")
			trace := traceRetrySettings(t, restarted)
			err := restarted.Reconcile(t.Context(), attempt.ID, RetryMigration)
			want := []string{"settings", "settings-done"}
			if sample.snapshot {
				if !errors.Is(err, errMigrationExecutionRoleChanged) {
					t.Fatalf("wrong effective role: %v", err)
				}
				want = append(want, "snapshot")
			} else {
				auditSQLState(t, err, "22023")
			}
			pending, err := restarted.PendingAttempt(t.Context())
			if err != nil || pending == nil || pending.ID != attempt.ID || pending.HistoryCommitted {
				t.Fatalf("rejecting the settings changed the pending attempt: %+v %v", pending, err)
			}
			auditIndexAndHistory(t, restarted.db, false, 2)
			assertRetrySettingsIndex(t, restarted, fixture.schema+"_alt", false)
			trace.check(t, 0, want...)
			if trace.snapshotPrepares != 0 {
				t.Errorf("snapshot prepares = %d, want 0", trace.snapshotPrepares)
			}
		})
	}
}

func retrySettingsFixture(t *testing.T) (*migrationReconcileFixture, *MigrationAttempt) {
	t.Helper()
	fixture := migrationAuditRunner(t)
	// Keep the target unqualified in the file and distinct from the default schema.
	// Quoting the identifier requires escaping 62 of its 63 bytes.
	table := pgx.Identifier{retrySettingsTable()}.Sanitize()
	auditExec(t, fixture.db, "CREATE TABLE "+fixture.schema+"_alt."+table+" (label text)")
	attempt := interruptStandaloneBeforeDispatch(t, fixture,
		"SET search_path TO "+fixture.schema+"_alt, "+fixture.schema+";",
		"CREATE INDEX CONCURRENTLY audit_idx ON "+table+" (label);")
	return fixture, attempt
}

func retrySettingsTable() string { return strings.Repeat("\"", 62) + "t" }

func assertRetrySettingsIndex(t *testing.T, runner *Runner, schema string, want bool) {
	t.Helper()
	var valid bool
	err := runner.db.QueryRow(t.Context(), `SELECT EXISTS (
		SELECT 1 FROM pg_catalog.pg_index WHERE indexrelid = pg_catalog.to_regclass($1)
		    AND indrelid = pg_catalog.to_regclass($2) AND indisvalid
	)`, pgx.Identifier{schema, "audit_idx"}.Sanitize(),
		pgx.Identifier{schema, retrySettingsTable()}.Sanitize()).Scan(&valid)
	if err != nil || valid != want {
		t.Fatalf("restored target has valid index=%t, want %t, error=%v", valid, want, err)
	}
}

type retrySettingsTrace struct {
	prepares              int
	snapshotPrepares      int
	receiptPrepares       int
	receiptWritesInTx     int
	historyPrepares       int
	acknowledgePrepares   int
	attemptLookupPrepares int
	historyLookupPrepares int
	events                []string
	setting               bool
	holder                *pgx.Conn
}

func traceRetrySettings(t *testing.T, runner *Runner) *retrySettingsTrace {
	t.Helper()
	trace := &retrySettingsTrace{}
	freshAdvisoryLockConfig(t, t.Context(), runner.db.(*PGDB)).Tracer = trace
	return trace
}

func (s *retrySettingsTrace) check(t *testing.T, wantPrepares int, wantEvents ...string) {
	t.Helper()
	if s.prepares != wantPrepares || !slices.Equal(s.events, wantEvents) {
		t.Errorf("settings prepares = %d, want %d; events = %v, want %v", s.prepares, wantPrepares, s.events, wantEvents)
	}
	if s.holder == nil || !s.holder.IsClosed() {
		t.Error("the settings holder was not retired")
	}
}

func (s *retrySettingsTrace) checkLookupPrepares(t *testing.T, want int) {
	t.Helper()
	if s.attemptLookupPrepares != want || s.historyLookupPrepares != want {
		t.Errorf("receipt and history lookup prepares = %d/%d, want %d/%d",
			s.attemptLookupPrepares, s.historyLookupPrepares, want, want)
	}
}

func (s *retrySettingsTrace) TracePrepareStart(ctx context.Context, _ *pgx.Conn, data pgx.TracePrepareStartData) context.Context {
	switch {
	case strings.HasPrefix(data.SQL, readMigrationAttemptProjection):
		s.attemptLookupPrepares++
	case strings.HasPrefix(data.SQL, "SELECT EXISTS (SELECT 1 FROM ") && strings.HasSuffix(data.SQL, " WHERE name = $1)"),
		strings.HasPrefix(data.SQL, "SELECT id, checksum FROM ") && strings.HasSuffix(data.SQL, " WHERE name = $1"):
		s.historyLookupPrepares++
	case strings.Contains(data.SQL, "pg_catalog.set_config('search_path'"):
		s.prepares++
	case data.SQL == readRestoredExecutionBinding:
		s.snapshotPrepares++
	case strings.Contains(data.SQL, "SET history_committed = true"):
		s.receiptPrepares++
	case strings.HasSuffix(data.SQL, "(name, checksum) VALUES ($1, $2)"):
		s.historyPrepares++
	case strings.Contains(data.SQL, "WHERE singleton AND id = $1 AND history_committed"):
		s.acknowledgePrepares++
	}
	return ctx
}

func (*retrySettingsTrace) TracePrepareEnd(context.Context, *pgx.Conn, pgx.TracePrepareEndData) {
}

func (s *retrySettingsTrace) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	switch {
	case strings.Contains(data.SQL, "pg_catalog.set_config('search_path'"):
		s.events = append(s.events, "settings")
		s.setting, s.holder = true, conn
	case data.SQL == readRestoredExecutionBinding:
		s.events = append(s.events, "snapshot")
	case strings.Contains(data.SQL, "SET history_committed = true") && conn.PgConn().TxStatus() == 'T':
		s.receiptWritesInTx++
		s.events = append(s.events, "receipt")
	case strings.HasSuffix(data.SQL, "(name, checksum) VALUES ($1, $2)") && conn.PgConn().TxStatus() == 'T':
		s.events = append(s.events, "history")
	case strings.Contains(data.SQL, "WHERE singleton AND id = $1 AND history_committed") && conn.PgConn().TxStatus() == 'I':
		s.events = append(s.events, "acknowledge")
	case strings.HasPrefix(strings.TrimSpace(data.SQL), "CREATE INDEX CONCURRENTLY"):
		s.events = append(s.events, "external-ddl")
	}
	return ctx
}

func (s *retrySettingsTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {
	if s.setting {
		s.events = append(s.events, "settings-done")
		s.setting = false
	}
}

func TestIntegrationRetryRejectsChangedTemporaryTypeScope(t *testing.T) {
	fixture := migrationAuditRunner(t)
	attempt := interruptStandaloneBeforeDispatch(t, fixture,
		"CREATE TEMP TABLE irrelevant_temp (id int); CREATE DOMAIN pg_temp.audit_label AS text;",
		"CREATE INDEX CONCURRENTLY audit_idx ON audit_users ((CAST(label AS audit_label)));")
	auditExec(t, fixture.db, "CREATE DOMAIN audit_label AS text")
	restarted := fixture.open(t, fixture.dir)
	if err := restarted.Reconcile(t.Context(), attempt.ID, RetryMigration); err == nil || !strings.Contains(err.Error(), "execution schemas changed") {
		t.Errorf("retry did not reject a different cast type scope: %v", err)
	}
	auditIndexAndHistory(t, restarted.db, false, 2)
}

func TestIntegrationRecordWithoutUnrelatedTemporarySchema(t *testing.T) {
	fixture := migrationAuditRunner(t)
	writeMigration(t, fixture.dir, auditChange, "CREATE TEMP TABLE irrelevant_temp (id int);", "SELECT 1;")
	writeMigration(t, fixture.dir, executionIndex,
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS audit_idx ON audit_users (label);", "DROP INDEX CONCURRENTLY audit_idx;")
	auditExec(t, fixture.db, `
		CREATE FUNCTION audit_reject_record() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN RAISE EXCEPTION 'audit fixture denied history for the index'; END; $$;
		CREATE TRIGGER audit_reject_record BEFORE INSERT ON _migrations
		FOR EACH ROW WHEN (NEW.name = '003_execution_index.sql') EXECUTE FUNCTION audit_reject_record();
	`)
	done, err := fixture.Up(t.Context())
	if !slices.Equal(done, []string{auditChange}) {
		t.Fatalf("unconfirmed history returned as applied: %v", done)
	}
	auditSQLState(t, err, "P0001")
	fixture.Close()
	restarted := fixture.open(t, fixture.dir)
	attempt, err := restarted.PendingAttempt(t.Context())
	if err != nil || attempt == nil || attempt.Name != executionIndex || attempt.HistoryCommitted {
		t.Fatalf("unconfirmed index history: %+v, error=%v", attempt, err)
	}
	auditIndexAndHistory(t, restarted.db, true, 2)
	auditExec(t, restarted.db, "DROP TRIGGER audit_reject_record ON _migrations")
	session := &recordedIndexSnapshotSession{MigrationSessionDB: fixture.openBoundSession(t)}
	if err := NewFromDB(session, fixture.dir).Reconcile(t.Context(), attempt.ID, RecordApplied); err != nil {
		t.Fatal(err)
	}
	if session.reads != 1 {
		t.Fatalf("recorded role and index used %d catalog snapshots", session.reads)
	}
	auditIndexAndHistory(t, restarted.db, true, 3)
}

type recordedIndexSnapshotSession struct {
	MigrationSessionDB
	reads int
}

func (d *recordedIndexSnapshotSession) QueryRow(ctx context.Context, query string, args ...any) Row {
	if strings.Contains(query, "pg_catalog.to_regrole(") || strings.Contains(query, "pg_catalog.pg_index") {
		d.reads++
	}
	return d.MigrationSessionDB.QueryRow(ctx, query, args...)
}

func TestIntegrationRetryPreservesIndexTablespace(t *testing.T) {
	space := os.Getenv("LUXO_TEST_MIGRATION_TABLESPACE")
	if space == "" {
		t.Skip("requires an isolated non-default PostgreSQL tablespace")
	}
	for _, sample := range []struct{ name, clause, want string }{
		{"implicit", "", space},
		{"explicit", " TABLESPACE pg_default", "pg_default"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			fixture := migrationAuditRunner(t)
			attempt := interruptStandaloneBeforeDispatch(t, fixture,
				"SET default_tablespace TO "+pgx.Identifier{space}.Sanitize(),
				"CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label)"+sample.clause+";")
			restarted := fixture.open(t, fixture.dir)
			if err := restarted.Reconcile(t.Context(), attempt.ID, RetryMigration); err != nil {
				t.Fatal(err)
			}
			var actual string
			err := restarted.db.QueryRow(t.Context(), `SELECT space.spcname
				FROM pg_catalog.pg_class idx
				JOIN pg_catalog.pg_database db ON db.datname = current_database()
				JOIN pg_catalog.pg_tablespace space ON space.oid = COALESCE(NULLIF(idx.reltablespace, 0), db.dattablespace)
				WHERE idx.oid = pg_catalog.to_regclass('audit_idx')`).Scan(&actual)
			if err != nil || actual != sample.want {
				t.Fatalf("retried index tablespace=%q, want %q, error=%v", actual, sample.want, err)
			}
			auditIndexAndHistory(t, restarted.db, true, 3)
		})
	}
}

func TestIntegrationRetryRejectsReplacedStandaloneTargets(t *testing.T) {
	for _, sample := range []struct{ name, setup, statement, replace string }{
		{"drop index", "CREATE INDEX audit_idx ON audit_users (label);", "DROP INDEX CONCURRENTLY audit_idx;",
			"DROP INDEX audit_idx; CREATE INDEX audit_idx ON audit_users (id);"},
		{"reindex index", "CREATE INDEX audit_idx ON audit_users (label);", "REINDEX (CONCURRENTLY true) INDEX audit_idx;",
			"DROP INDEX audit_idx; CREATE INDEX audit_idx ON audit_users (id);"},
		{"reindex table", "CREATE TABLE other_users (id bigint);", "REINDEX TABLE CONCURRENTLY other_users;",
			"DROP TABLE other_users; CREATE TABLE other_users (id text);"},
		{"detach partition", "CREATE TABLE audit_parent (id int) PARTITION BY RANGE (id); CREATE TABLE audit_child PARTITION OF audit_parent FOR VALUES FROM (0) TO (5);",
			"ALTER TABLE ONLY (audit_parent) DETACH PARTITION audit_child CONCURRENTLY;",
			"DROP TABLE audit_child; CREATE TABLE audit_child PARTITION OF audit_parent FOR VALUES FROM (0) TO (5);"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			fixture := migrationAuditRunner(t)
			attempt := interruptStandaloneBeforeDispatch(t, fixture, sample.setup, sample.statement)
			auditExec(t, fixture.db, sample.replace)
			restarted := fixture.open(t, fixture.dir)
			err := restarted.Reconcile(t.Context(), attempt.ID, RetryMigration)
			if err == nil || !strings.Contains(err.Error(), "resolves to a different identity") {
				t.Fatalf("replaced target did not stop retry: %v", err)
			}
			pending, err := restarted.PendingAttempt(t.Context())
			if err != nil || pending == nil || pending.ID != attempt.ID || pending.HistoryCommitted {
				t.Fatalf("replaced target changed history: %+v %v", pending, err)
			}
		})
	}
}

func TestIntegrationLegacyMigrationReceiptCanBeRecorded(t *testing.T) {
	fixture := migrationAuditRunner(t)
	attempt := interruptStandaloneBeforeDispatch(t, fixture, "SELECT 1;",
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS audit_idx ON audit_users (label);")
	auditExec(t, fixture.db, "ALTER TABLE _luxo_migration_attempt DROP COLUMN execution_binding")
	restarted := fixture.open(t, fixture.dir)
	trace := traceRetrySettings(t, restarted)
	if pending, err := restarted.PendingAttempt(t.Context()); err != nil || pending == nil || pending.ID != attempt.ID {
		t.Fatalf("legacy journal not readable: %+v %v", pending, err)
	}
	if err := restarted.Reconcile(t.Context(), attempt.ID, RetryMigration); err == nil || !strings.Contains(err.Error(), "no recorded migration object binding") {
		t.Fatalf("replayed a legacy journal without a bound target: %v", err)
	}
	auditExec(t, restarted.db, "CREATE INDEX audit_idx ON audit_users (label)")
	if err := restarted.Reconcile(t.Context(), attempt.ID, RecordApplied); err != nil {
		t.Fatal(err)
	}
	trace.check(t, 1, "settings", "settings-done", "history", "receipt", "acknowledge")
	if trace.historyPrepares != 1 || trace.receiptPrepares != 1 || trace.acknowledgePrepares != 1 {
		t.Errorf("recording a legacy receipt changed the configured query mode: history=%d, receipt=%d, acknowledgement=%d",
			trace.historyPrepares, trace.receiptPrepares, trace.acknowledgePrepares)
	}
	auditIndexAndHistory(t, restarted.db, true, 3)
	writeMigration(t, fixture.dir, "004_second.sql", "CREATE INDEX CONCURRENTLY audit_second_idx ON audit_users (id);", "SELECT 1;")
	auditUp(t, restarted, 1)
	var upgraded bool
	if err := restarted.db.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_attribute WHERE attrelid = '_luxo_migration_attempt'::regclass AND attname = 'execution_binding' AND NOT attisdropped)").Scan(&upgraded); err != nil || !upgraded {
		t.Fatalf("legacy journal not upgraded: upgraded=%t error=%v", upgraded, err)
	}
}

func TestPlanMigrationExecutionBindings(t *testing.T) {
	for _, sample := range []struct {
		name, sql, first, second, space string
		kind                            migrationObjectKind
		mode                            migrationTablespaceMode
		independent                     bool
	}{
		{"unnamed create", `; /* ready */ CREATE INDEX CONCURRENTLY ON app.users (first, second);`,
			"app.users", "", "", migrationRelation, migrationDefaultTablespace, true},
		{"qualified create", `CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS "Named" ON ONLY "schema name"."Users" USING btree (label) INCLUDE (id) NULLS NOT DISTINCT WITH (fillfactor = 80) TABLESPACE "home space";`,
			`"schema name"."Users"`, "", `"home space"`, migrationRelation, migrationExplicitTablespace, true},
		{"expression and predicate", `CREATE INDEX CONCURRENTLY idx ON app.users ((lower(label))) WHERE id > 3;`,
			"app.users", "", "", migrationRelation, migrationDefaultTablespace, false},
		{"operator class", `CREATE INDEX CONCURRENTLY idx ON app.users (label varchar_pattern_ops);`,
			"app.users", "", "", migrationRelation, migrationDefaultTablespace, false},
		{"unicode", `CREATE INDEX CONCURRENTLY idx ON U&"caf\00E9".U&"face#D83D#DE01" UESCAPE '#' (label);`,
			`"café"."face😁"`, "", "", migrationRelation, migrationDefaultTablespace, true},
		{"drop", `DROP INDEX CONCURRENTLY IF EXISTS app.idx;`, "app.idx", "", "", migrationRelation, migrationNoTablespace, true},
		{"detach", `ALTER TABLE IF EXISTS ONLY (app.parent) DETACH PARTITION other.child CONCURRENTLY;`,
			"app.parent", "other.child", "", migrationRelation, migrationNoTablespace, true},
		{"reindex index", `REINDEX (TABLESPACE "archive", "concurrently" true) INDEX app.idx;`,
			"app.idx", "", `"archive"`, migrationRelation, migrationExplicitTablespace, true},
		{"reindex concurrently qualified schema", `REINDEX (CONCURRENTLY true) INDEX concurrently.idx;`,
			"concurrently.idx", "", "", migrationRelation, migrationNoTablespace, true},
		{"reindex schema", `REINDEX SCHEMA CONCURRENTLY "App";`, `"App"`, "", "", migrationSchema, migrationNoTablespace, true},
		{"reindex database", `REINDEX DATABASE "Luxo";`, `"Luxo"`, "", "", migrationDatabase, migrationNoTablespace, true},
		{"reindex current database", `REINDEX SYSTEM;`, "", "", "", migrationDatabase, migrationNoTablespace, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			plan := planMigrationExecution(sample.sql)
			if plan.targets[0].kind != sample.kind || plan.targets[0].name != sample.first || plan.targets[1].name != sample.second ||
				plan.tablespace != sample.mode || plan.tablespaceName != sample.space || plan.tempIndependent != sample.independent {
				t.Fatalf("execution plan for %q: %+v", sample.sql, plan)
			}
		})
	}
}

func TestQualifyMigrationPreservesUnboundParts(t *testing.T) {
	const statement = `ALTER TABLE ONLY ("Original" /* dot */ .parent) DETACH PARTITION child CONCURRENTLY; -- tail`
	plan := planMigrationExecution(statement)
	binding := migrationExecutionBinding{Targets: [2]migrationBoundObject{
		{OID: 1, Name: `"Original".parent`}, {OID: 2, Name: `hidden.child`},
	}}
	const want = `ALTER TABLE ONLY ("Original".parent) DETACH PARTITION hidden.child CONCURRENTLY; -- tail`
	if qualified := qualifyMigrationObjects(statement, plan, binding); qualified != want {
		t.Fatalf("qualified migration = %q, want %q", qualified, want)
	}
	if unchanged := qualifyMigrationObjects(statement, plan, migrationExecutionBinding{}); unchanged != statement {
		t.Fatalf("unbound migration changed: %q", unchanged)
	}
}

func TestPlanMigrationExecutionDoesNotBindUnrecognizedTargets(t *testing.T) {
	for _, text := range []string{
		`SELECT 1;`,
		`CREATE INDEX idx ON users (id);`,
		`CREATE INDEX CONCURRENTLY IF EXISTS idx ON users (id);`,
		`CREATE INDEX CONCURRENTLY , ON users (id);`,
		`CREATE INDEX CONCURRENTLY idx ON app. (id);`,
		`CREATE INDEX CONCURRENTLY idx ON users;`,
		`CREATE INDEX CONCURRENTLY idx ON users (id) INCLUDE id;`,
		`CREATE INDEX CONCURRENTLY idx ON users (id) WITH (fillfactor = 80;`,
		`CREATE INDEX CONCURRENTLY idx ON users (id) NULLS UNKNOWN;`,
		`CREATE INDEX CONCURRENTLY idx ON users (id) TABLESPACE;`,
		`DROP TABLE idx;`,
		`DROP INDEX CONCURRENTLY idx, second;`,
		`ALTER TABLE ONLY (app.parent DETACH PARTITION child CONCURRENTLY;`,
		`ALTER TABLE . DETACH PARTITION child CONCURRENTLY;`,
		`ALTER VIEW parent DETACH PARTITION child CONCURRENTLY;`,
		`ALTER TABLE parent DETACH PARTITION child;`,
		`REINDEX (`,
		`REINDEX (U&"o" UESCAPE '0' true) INDEX app.idx;`,
		`REINDEX (VERBOSE true`,
		`REINDEX (TABLESPACE) INDEX app.idx;`,
		`REINDEX unknown app.idx;`,
		`CREATE INDEX CONCURRENTLY idx ON U&"x" UESCAPE '0' (id);`,
		`CREATE INDEX CONCURRENTLY idx ON U&"x\0000" (id);`,
	} {
		t.Run(text, func(t *testing.T) {
			if plan := planMigrationExecution(text); plan.targets[0].kind != migrationNoObject {
				t.Fatalf("invalid syntax acquired an execution binding: %+v", plan)
			}
		})
	}
}

func TestMigrationExecutionBindingPreservesErrors(t *testing.T) {
	for _, sample := range []struct{ input, message string }{
		{"not json", "decode recorded migration execution binding"},
		{`{"version":2}`, "unknown version of the migration execution binding"},
	} {
		if _, err := readMigrationBinding(sample.input); err == nil || !strings.Contains(err.Error(), sample.message) {
			t.Errorf("record %q unexpectedly accepted: %v", sample.input, err)
		}
	}
}

func TestMigrationExecutionDecodesPostgreSQLUnicodeIdentifiers(t *testing.T) {
	for _, raw := range []string{`\`, `\000x`, `\D83D\0000`, `\D83D`, `\DE00`, `\+110000`} {
		if decoded, ok := decodeExecutionIdentifier(raw, '\\'); ok {
			t.Errorf("invalid identifier %q decoded to %q", raw, decoded)
		}
	}
	if decoded, ok := decodeExecutionIdentifier(`\+01F601\\end`, '\\'); !ok || decoded != `😁\end` {
		t.Fatalf("valid encoded identifier: %q, accepted=%t", decoded, ok)
	}
}

type executionErrorRow func(...any) error

func (scan executionErrorRow) Scan(dest ...any) error { return scan(dest...) }

type executionErrorDB struct {
	mockDB
	read func(string) Row
}

func (d *executionErrorDB) QueryRow(_ context.Context, query string, _ ...any) Row {
	return d.read(query)
}

func TestMigrationRetryStopsWhenRestoringSessionSettingsFails(t *testing.T) {
	cause := errors.New("fixture could not restore session settings")
	db := &executionErrorDB{
		mockDB: mockDB{execErr: cause},
		read: func(query string) Row {
			t.Fatalf("queried catalog after restoring session settings failed: %s", query)
			return nil
		},
	}
	runner := migrationOperation{db: db}
	plan := plannedMigration{sql: "CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label)"}
	got, err := runner.prepareMigrationRetry(t.Context(), plan, &storedMigrationAttempt{}, migrationExecutionBinding{Version: 1})
	if got != plan || !errors.Is(err, cause) || !strings.Contains(err.Error(), "restore migration execution settings") {
		t.Fatalf("retry after failed session restoration: plan=%+v, error=%v", got, err)
	}
}

func TestMigrationRetryRejectsFailedOrChangedCatalogBinding(t *testing.T) {
	cause := errors.New("fixture lost catalog connection")
	for _, sample := range []struct {
		name, row string
		err       error
		message   string
	}{
		{"lookup error", "", cause, "bind standalone migration execution objects"},
		{"invalid row", "not json", nil, "decode recorded migration execution binding"},
		{"changed tablespace", `{"version":1,"targets":[{"oid":13},{}],"permanent_schemas":"{11}","tablespace":27}`, nil, "migration execution tablespace changed"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			db := &executionErrorDB{read: func(query string) Row {
				return executionErrorRow(func(dest ...any) error {
					if query != readRestoredExecutionBinding {
						return errors.New("unexpected additional catalog query")
					}
					*dest[0].(*bool), *dest[1].(*bool) = true, true
					*dest[2].(*string) = sample.row
					return sample.err
				})
			}}
			runner := NewFromDB(db, t.TempDir())
			plan := plannedMigration{sql: "CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label)"}
			binding := migrationExecutionBinding{Version: 1, PermanentSchemas: "{11}", Tablespace: 26,
				Targets: [2]migrationBoundObject{{OID: 13}}}
			got, err := runner.prepareMigrationRetry(t.Context(), plan, &storedMigrationAttempt{}, binding)
			if err == nil || !strings.Contains(err.Error(), sample.message) || sample.err != nil && !errors.Is(err, sample.err) || got != plan {
				t.Fatalf("retry binding: plan=%+v, error=%v", got, err)
			}
		})
	}
}

func TestStartMigrationAttemptPreservesBindingAndUpgradeErrors(t *testing.T) {
	cause := errors.New("fixture could not establish execution binding")
	plan := plannedMigration{name: "001_idx.sql", sql: "CREATE INDEX CONCURRENTLY idx ON users (id)"}
	operation := &migrationOperation{db: &mockDB{execErr: cause}, sessionBound: true, metadata: migrationMetadata{attemptExists: true}}
	if attempt, err := operation.startMigrationAttempt(t.Context(), plan, "UP"); attempt != nil || !errors.Is(err, cause) {
		t.Fatalf("failed catalog binding started an attempt: %+v %v", attempt, err)
	}
	operation = &migrationOperation{db: &mockDB{execSQL: func(query string) error {
		if strings.Contains(query, "ALTER TABLE") {
			return cause
		}
		return &pgconn.PgError{Code: "42703"}
	}}, metadata: migrationMetadata{attemptExists: true}}
	if attempt, err := operation.startMigrationAttempt(t.Context(), plan, "UP"); attempt != nil || !errors.Is(err, cause) || !strings.Contains(err.Error(), "upgrade migration reconciliation table") {
		t.Fatalf("failed journal upgrade started an attempt: %+v %v", attempt, err)
	}
}

func TestReconcilePropagatesInvalidPersistedExecutionBinding(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, auditChange, "CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);", "SELECT 1;")
	plan, err := planMigration(filepath.Join(dir, auditChange), "UP", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	db := &executionErrorDB{read: func(query string) Row {
		if strings.Contains(query, "execution_binding") {
			return executionErrorRow(func(dest ...any) error {
				*dest[0].(*string), *dest[1].(*string), *dest[2].(*string) = "pending", auditChange, "UP"
				*dest[3].(*string), *dest[10].(*string) = plan.digest, "invalid binding"
				return nil
			})
		}
		return &mockRow{}
	}}
	runner := NewFromDB(db, dir)
	runner.metadata.attemptExists = true
	if err := runner.migrationOperation.reconcile(t.Context(), "pending", RetryMigration); err == nil || !strings.Contains(err.Error(), "decode recorded migration execution binding") {
		t.Fatalf("invalid binding was not rejected before dispatch: %v", err)
	}
}

func TestRecordMigrationRejectsUnconfirmedIdentity(t *testing.T) {
	cause := errors.New("fixture authorization lookup failed")
	attempt := &storedMigrationAttempt{}
	plan := plannedMigration{mode: migrationMode{index: "idx", table: "users"}}
	for _, sample := range []struct {
		name    string
		db      *mockDB
		binding migrationExecutionBinding
		message string
	}{
		{"legacy restore", &mockDB{execErr: cause}, migrationExecutionBinding{}, "restore migration execution settings"},
		{"verify role", &mockDB{queryRowVal: &mockRow{scanErr: cause}}, migrationExecutionBinding{Version: 1}, "verify migration execution role"},
		{"role before missing target", &mockDB{queryRowVal: &mockRow{}}, migrationExecutionBinding{Version: 1}, "migration execution role changed"},
		{"unresolved table", &mockDB{queryRowVal: &mockRow{history: true}}, migrationExecutionBinding{Version: 1}, "no recorded relation for the index"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			runner := NewFromDB(sample.db, t.TempDir())
			if err := runner.verifyRecordedMigration(t.Context(), plan, attempt, sample.binding); err == nil || !strings.Contains(err.Error(), sample.message) {
				t.Fatalf("recorded execution: %v", err)
			}
		})
	}
}

func TestRecordMigrationChecksBoundRoleAndIndexTogether(t *testing.T) {
	fault := &pgconn.PgError{Code: "42501", Message: "fixture catalog read denied"}
	attempt := &storedMigrationAttempt{executionRole: "1983"}
	plan := plannedMigration{mode: migrationMode{index: "idx", table: "users"}}
	binding := migrationExecutionBinding{Version: 1, Targets: [2]migrationBoundObject{{OID: 4123}}}
	for _, sample := range []struct {
		name, want      string
		sameRole, valid bool
		err             error
	}{
		{name: "valid", sameRole: true, valid: true},
		{name: "different role", want: "migration execution role changed; use the recorded effective database role", valid: true},
		{name: "bad index", want: "index idx on users is invalid, missing, or belongs to another table; inspect the existing object and resolve the failed build before retrying", sameRole: true},
		{name: "catalog error", err: fault},
	} {
		t.Run(sample.name, func(t *testing.T) {
			queries := 0
			db := &executionErrorDB{read: func(query string) Row {
				queries++
				hasRole := strings.Contains(query, "pg_catalog.to_regrole(")
				hasIndex := strings.Contains(query, "pg_catalog.pg_index")
				if !hasRole || !hasIndex || !strings.Contains(query, "CASE WHEN") {
					t.Errorf("role and index require one guarded catalog query, got %q", query)
				}
				return executionErrorRow(func(dest ...any) error {
					if sample.err != nil {
						return sample.err
					}
					if len(dest) != 2 {
						return errors.New("expected role and index results from the same row")
					}
					*dest[0].(*bool) = sample.sameRole
					*dest[1].(*bool) = sample.valid
					return nil
				})
			}}
			runner := NewFromDB(db, t.TempDir())
			err := runner.verifyRecordedMigration(t.Context(), plan, attempt, binding)
			if queries != 1 {
				t.Errorf("recorded checks issued %d catalog queries", queries)
			}
			switch {
			case sample.err != nil:
				var pgErr *pgconn.PgError
				if !errors.Is(err, sample.err) || !errors.As(err, &pgErr) || pgErr.Code != "42501" {
					t.Fatalf("catalog failure did not preserve SQLSTATE: %v", err)
				}
			case sample.want != "":
				if err == nil || err.Error() != sample.want {
					t.Fatalf("recorded validation error = %v, want %q", err, sample.want)
				}
			case err != nil:
				t.Fatal(err)
			}
		})
	}
}

type atomicIntentSession struct{ MigrationSessionDB }

func (d atomicIntentSession) QueryRow(ctx context.Context, query string, args ...any) Row {
	if strings.Contains(query, "pg_catalog.jsonb_build_object") {
		return executionErrorRow(func(...any) error { return errors.New("standalone binding read separately from its intent") })
	}
	return d.MigrationSessionDB.QueryRow(ctx, query, args...)
}

func TestIntegrationStandaloneRecordsBindingInIntentInsert(t *testing.T) {
	fixture := migrationAuditRunner(t)
	writeMigration(t, fixture.dir, auditChange, "CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);", "DROP INDEX CONCURRENTLY audit_idx;")
	lost := errors.New("fixture lost execution intent acknowledgement")
	session := atomicIntentSession{migrationLostIntentReplyDB{MigrationSessionDB: fixture.openBoundSession(t), fault: lost}}
	if done, err := NewFromDB(session, fixture.dir).Up(t.Context()); len(done) != 0 || !errors.Is(err, lost) {
		t.Fatalf("recorded intent acknowledgement: %v %v", done, err)
	}
	op, err := fixture.bindMetadata(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := op.readMigrationAttempt(t.Context(), true)
	if err != nil || attempt == nil || attempt.HistoryCommitted {
		t.Fatalf("missing durable execution intent: %+v %v", attempt, err)
	}
	binding, err := readMigrationBinding(attempt.executionBinding)
	if err != nil || binding.Targets[0].OID == 0 || binding.Tablespace == 0 || binding.Version != 1 {
		t.Fatalf("journal did not capture recovered database identity: %+v %v", binding, err)
	}
	auditIndexAndHistory(t, fixture.db, false, 1)
	if err := fixture.Reconcile(t.Context(), attempt.ID, RetryMigration); err != nil {
		t.Fatal(err)
	}
	auditIndexAndHistory(t, fixture.db, true, 2)
}

type retrySnapshotSession struct {
	MigrationSessionDB
	checking bool
	reads    int
}

func (d *retrySnapshotSession) Exec(ctx context.Context, query string, args ...any) error {
	if strings.HasPrefix(strings.TrimSpace(query), "CREATE INDEX CONCURRENTLY") {
		d.checking = false
	}
	err := d.MigrationSessionDB.Exec(ctx, query, args...)
	if err == nil && strings.Contains(query, "pg_catalog.set_config('search_path'") {
		d.checking = true
	}
	return err
}

func (d *retrySnapshotSession) QueryRow(ctx context.Context, query string, args ...any) Row {
	if d.checking {
		d.reads++
	}
	return d.MigrationSessionDB.QueryRow(ctx, query, args...)
}

func TestIntegrationRetryChecksRestoredIdentityInOneSnapshot(t *testing.T) {
	fixture := migrationAuditRunner(t)
	attempt := interruptStandaloneBeforeDispatch(t, fixture, "SELECT 1;", "CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);")
	session := &retrySnapshotSession{MigrationSessionDB: fixture.openBoundSession(t)}
	if err := NewFromDB(session, fixture.dir).Reconcile(t.Context(), attempt.ID, RetryMigration); err != nil {
		t.Fatal(err)
	}
	if session.reads != 1 {
		t.Fatalf("restored identity used %d catalog snapshots before executing the index", session.reads)
	}
	auditIndexAndHistory(t, fixture.db, true, 3)
}

type receiptOnlySession struct{ MigrationSessionDB }

func (d receiptOnlySession) QueryRow(ctx context.Context, query string, args ...any) Row {
	if strings.Contains(query, "execution_binding::text") {
		return executionErrorRow(func(...any) error { return errors.New("receipt-only operation fetched the execution binding") })
	}
	return d.MigrationSessionDB.QueryRow(ctx, query, args...)
}

func TestIntegrationStatusAndAcknowledgementSkipStoredBinding(t *testing.T) {
	fixture := migrationAuditRunner(t)
	writeMigration(t, fixture.dir, auditChange, "CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);", "DROP INDEX CONCURRENTLY audit_idx;")
	auditUp(t, fixture.Runner, 1)
	ctx := t.Context()
	if done, err := fixture.Down(ctx, 1); err != nil || !slices.Equal(done, []string{auditChange}) {
		t.Fatalf("committed rollback receipt: %v %v", done, err)
	}
	reader := NewFromDB(receiptOnlySession{fixture.openBoundSession(t)}, fixture.dir)
	attempt, err := reader.PendingAttempt(ctx)
	if err != nil || attempt == nil || !attempt.HistoryCommitted {
		t.Fatalf("read committed receipt: %+v %v", attempt, err)
	}
	if err := reader.Reconcile(ctx, attempt.ID, AcknowledgeMigration); err != nil {
		t.Fatal(err)
	}
	if pending, err := reader.PendingAttempt(ctx); err != nil || pending != nil {
		t.Fatalf("acknowledged receipt remains visible: %+v %v", pending, err)
	}
}

func TestQualifyMigrationBuildsOneBuffer(t *testing.T) {
	const sql = `ALTER TABLE ONLY (parent) DETACH PARTITION café CONCURRENTLY; -- untouched`
	const want = `ALTER TABLE ONLY ("tenant"."parent") DETACH PARTITION "tenant"."café" CONCURRENTLY; -- untouched`
	plan := planMigrationExecution(sql)
	binding := migrationExecutionBinding{Targets: [2]migrationBoundObject{
		{OID: 1, Name: `"tenant"."parent"`}, {OID: 2, Name: `"tenant"."café"`},
	}}
	var result string
	if allocations := testing.AllocsPerRun(100, func() {
		result = qualifyMigrationObjects(sql, plan, binding)
	}); allocations != 1 || result != want {
		t.Fatalf("qualified statement = %q, allocations = %g, want %q with one allocation", result, allocations, want)
	}
	binding.Targets[1] = migrationBoundObject{}
	const partial = `ALTER TABLE ONLY ("tenant"."parent") DETACH PARTITION café CONCURRENTLY; -- untouched`
	if result := qualifyMigrationObjects(sql, plan, binding); result != partial {
		t.Fatalf("partly qualified statement = %q, want %q", result, partial)
	}
}

// Freeze the pre-formatting catalog query as an independent semantic oracle.
const originalPermanentSchemasSQL = `ARRAY(
  SELECT namespace.oid
  FROM pg_catalog.unnest(pg_catalog.current_schemas(true)) WITH ORDINALITY AS resolved(name, position)
  JOIN pg_catalog.pg_namespace namespace ON namespace.nspname = resolved.name
  WHERE namespace.oid <> pg_catalog.pg_my_temp_schema()
  ORDER BY resolved.position
)::text`

const originalQueryExecutionBindingSQL = `
  WITH requested(slot, kind, name) AS (VALUES (1, $1::int, $2::text), (2, $3::int, $4::text)),
  targets AS (
    SELECT slot, kind, CASE kind
      WHEN 1 THEN pg_catalog.to_regclass(name)::oid
      WHEN 2 THEN pg_catalog.to_regnamespace(name)::oid
      WHEN 3 THEN (SELECT oid FROM pg_catalog.pg_database WHERE datname =
        CASE WHEN name = '' THEN current_database() ELSE (pg_catalog.parse_ident(name))[1]::pg_catalog.name END)
    END AS oid FROM requested
  ),
  bindings AS (
    SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_object('oid', COALESCE(t.oid, 0)::bigint,
      'name', CASE WHEN t.oid IS NOT NULL THEN CASE t.kind
        WHEN 1 THEN pg_catalog.format('%I.%I', rel_schema.nspname, relation.relname)
        WHEN 2 THEN pg_catalog.quote_ident(namespace.nspname)
        WHEN 3 THEN pg_catalog.quote_ident(db.datname)
      END END) ORDER BY t.slot) AS value
    FROM targets t
    LEFT JOIN pg_catalog.pg_class relation ON t.kind = 1 AND relation.oid = t.oid
    LEFT JOIN pg_catalog.pg_namespace rel_schema ON rel_schema.oid = relation.relnamespace
    LEFT JOIN pg_catalog.pg_namespace namespace ON t.kind = 2 AND namespace.oid = t.oid
    LEFT JOIN pg_catalog.pg_database db ON t.kind = 3 AND db.oid = t.oid
  ),
  tablespace AS (
    SELECT pg_catalog.current_setting('default_tablespace') AS setting
  )
  SELECT pg_catalog.jsonb_build_object(
    'version', 1, 'targets', bindings.value, 'permanent_schemas', ` + originalPermanentSchemasSQL + `,
    'tablespace', (CASE $5::int
      WHEN 1 THEN COALESCE(space.oid, db.dattablespace)
      WHEN 2 THEN COALESCE(space.oid, 0)
      ELSE 0
    END)::bigint, 'tablespace_value', CASE WHEN $5::int = 1 THEN tablespace.setting ELSE '' END
  )
  FROM bindings CROSS JOIN tablespace
  JOIN pg_catalog.pg_database db ON db.datname = current_database()
  LEFT JOIN pg_catalog.pg_tablespace space ON space.spcname = CASE $5::int
    WHEN 1 THEN NULLIF(tablespace.setting, '')::pg_catalog.name
    WHEN 2 THEN (pg_catalog.parse_ident($6::text))[1]::pg_catalog.name
  END`

const originalStoreBindingSQL = `CASE WHEN $1::int <> 0 THEN (` + originalQueryExecutionBindingSQL + `) END`

func compareExecutionBindingSQL(t *testing.T, session MigrationSessionDB, plan migrationExecutionPlan, candidate string) (*string, *string, bool) {
	t.Helper()
	query := `WITH captured AS MATERIALIZED (SELECT (` + originalStoreBindingSQL + `) AS original, (` + candidate + `) AS candidate)
 SELECT original::text, candidate::text, original IS NOT DISTINCT FROM candidate FROM captured`
	var before, after *string
	var same bool
	if err := session.QueryRow(t.Context(), query, plan.targets[0].kind, plan.targets[0].name, plan.targets[1].kind, plan.targets[1].name, plan.tablespace, plan.tablespaceName).Scan(&before, &after, &same); err != nil {
		t.Fatal(err)
	}
	return before, after, same
}

func TestIntegrationExecutionBindingSQLIdentity(t *testing.T) {
	f := migrationAuditRunner(t)
	session := f.openBoundSession(t)
	longName, unicodeName := retrySettingsTable(), "模型字段"
	for _, table := range []string{longName, unicodeName} {
		if err := session.Exec(t.Context(), "CREATE TABLE "+pgx.Identifier{table}.Sanitize()+" (id int)"); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.Exec(t.Context(), "CREATE TEMP TABLE temp_identity (id int)"); err != nil {
		t.Fatal(err)
	}
	var database string
	var tempOID uint32
	if err := session.QueryRow(t.Context(), "SELECT current_database(), pg_my_temp_schema()").Scan(&database, &tempOID); err != nil {
		t.Fatal(err)
	}
	space := os.Getenv("LUXO_TEST_MIGRATION_TABLESPACE")
	if space == "" {
		space = "pg_default"
	}
	if err := session.Exec(t.Context(), "SET default_tablespace TO "+pgx.Identifier{space}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		name          string
		first, second migrationObjectReference
		space         migrationTablespaceMode
		spaceName     string
		exists        bool
	}{
		{"relation-default", migrationObjectReference{kind: migrationRelation, name: "audit_users"}, migrationObjectReference{}, migrationDefaultTablespace, "", true},
		{"relation-missing", migrationObjectReference{kind: migrationRelation, name: "missing_identity"}, migrationObjectReference{}, migrationDefaultTablespace, "", false},
		{"two-targets", migrationObjectReference{kind: migrationRelation, name: "audit_users"}, migrationObjectReference{kind: migrationSchema, name: f.schema}, migrationNoTablespace, "", true},
		{"schema", migrationObjectReference{kind: migrationSchema, name: pgx.Identifier{f.schema}.Sanitize()}, migrationObjectReference{}, migrationNoTablespace, "", true},
		{"schema-missing", migrationObjectReference{kind: migrationSchema, name: "missing_identity"}, migrationObjectReference{}, migrationNoTablespace, "", false},
		{"database-current", migrationObjectReference{kind: migrationDatabase}, migrationObjectReference{}, migrationNoTablespace, "", true},
		{"database-quoted", migrationObjectReference{kind: migrationDatabase, name: pgx.Identifier{database}.Sanitize()}, migrationObjectReference{}, migrationNoTablespace, "", true},
		{"database-missing", migrationObjectReference{kind: migrationDatabase, name: "missing_identity"}, migrationObjectReference{}, migrationNoTablespace, "", false},
		{"no-primary-target", migrationObjectReference{}, migrationObjectReference{kind: migrationRelation, name: "audit_users"}, migrationNoTablespace, "", false},
		{"quoted-long", migrationObjectReference{kind: migrationRelation, name: pgx.Identifier{longName}.Sanitize()}, migrationObjectReference{}, migrationDefaultTablespace, "", true},
		{"unicode", migrationObjectReference{kind: migrationRelation, name: pgx.Identifier{unicodeName}.Sanitize()}, migrationObjectReference{}, migrationNoTablespace, "", true},
		{"temporary", migrationObjectReference{kind: migrationRelation, name: "pg_temp.temp_identity"}, migrationObjectReference{}, migrationNoTablespace, "", true},
		{"explicit-tablespace", migrationObjectReference{kind: migrationRelation, name: "audit_users"}, migrationObjectReference{}, migrationExplicitTablespace, "pg_default", true},
		{"explicit-tablespace-missing", migrationObjectReference{kind: migrationRelation, name: "audit_users"}, migrationObjectReference{}, migrationExplicitTablespace, "missing_identity", true},
		{"two-relations", migrationObjectReference{kind: migrationRelation, name: "audit_users"}, migrationObjectReference{kind: migrationRelation, name: pgx.Identifier{longName}.Sanitize()}, migrationNoTablespace, "", true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			plan := migrationExecutionPlan{targets: [2]migrationObjectReference{sample.first, sample.second}, tablespace: sample.space, tablespaceName: sample.spaceName}
			before, after, same := compareExecutionBindingSQL(t, session, plan, storeExecutionBinding)
			if !same || (before == nil) != (after == nil) {
				t.Fatalf("catalog identity changed: %v %v", before, after)
			}
			if sample.first.kind == migrationNoObject {
				if after != nil {
					t.Fatal("unbound statement captured a binding")
				}
				return
			}
			if after == nil {
				t.Fatal("bound statement lost its binding")
			}
			binding, err := readMigrationBinding(*after)
			if err != nil || binding.Version != 1 || (binding.Targets[0].OID != 0) != sample.exists {
				t.Fatalf("wrong binding: %+v %v", binding, err)
			}
			if (binding.Targets[1].OID != 0) != (sample.second.kind != migrationNoObject) {
				t.Fatalf("second target identity changed: %+v", binding)
			}
			for _, oid := range strings.Split(strings.Trim(binding.PermanentSchemas, "{}"), ",") {
				if oid == strconv.FormatUint(uint64(tempOID), 10) {
					t.Fatal("temporary namespace became permanent")
				}
			}
			if sample.space == migrationDefaultTablespace && binding.TablespaceValue != space {
				t.Fatalf("lost default tablespace: %+v", binding)
			}
		})
	}
	t.Run("oracle-rejects-semantic-change", func(t *testing.T) {
		changed := strings.Replace(storeExecutionBinding, "'version', 1", "'version', 2", 1)
		if changed == storeExecutionBinding {
			t.Fatal("semantic control did not change SQL")
		}
		plan := migrationExecutionPlan{targets: [2]migrationObjectReference{{kind: migrationRelation, name: "audit_users"}}}
		_, _, same := compareExecutionBindingSQL(t, session, plan, changed)
		if same {
			t.Fatal("oracle accepted a changed binding version")
		}
	})
}

func executionBindingOutcome(t *testing.T, session MigrationSessionDB, sql string, plan migrationExecutionPlan) (*string, string) {
	t.Helper()
	var encoded *string
	err := session.QueryRow(t.Context(), "SELECT ("+sql+")::text", plan.targets[0].kind, plan.targets[0].name, plan.targets[1].kind, plan.targets[1].name, plan.tablespace, plan.tablespaceName).Scan(&encoded)
	if err == nil {
		return encoded, ""
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected a PostgreSQL outcome, got %v", err)
	}
	return nil, pgErr.Code
}

func TestIntegrationExecutionBindingSQLErrors(t *testing.T) {
	f := migrationAuditRunner(t)
	session := f.openBoundSession(t)
	for _, sample := range []struct {
		name      string
		plan      migrationExecutionPlan
		wantError bool
	}{
		{"relation-null", migrationExecutionPlan{targets: [2]migrationObjectReference{{kind: migrationRelation, name: `"unclosed`}}}, false},
		{"schema-null", migrationExecutionPlan{targets: [2]migrationObjectReference{{kind: migrationSchema, name: `"unclosed`}}}, false},
		{"database", migrationExecutionPlan{targets: [2]migrationObjectReference{{kind: migrationDatabase, name: `"unclosed`}}}, true},
		{"tablespace", migrationExecutionPlan{targets: [2]migrationObjectReference{{kind: migrationRelation, name: "audit_users"}}, tablespace: migrationExplicitTablespace, tablespaceName: `"unclosed`}, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			before, oldCode := executionBindingOutcome(t, session, originalStoreBindingSQL, sample.plan)
			after, newCode := executionBindingOutcome(t, session, storeExecutionBinding, sample.plan)
			if oldCode != newCode || (oldCode != "") != sample.wantError {
				t.Fatalf("SQLSTATE changed: %q/%q", oldCode, newCode)
			}
			if sample.wantError {
				return
			}
			if before == nil || after == nil || *before != *after {
				t.Fatal("driver NULL-identity outcome changed")
			}
			binding, err := readMigrationBinding(*after)
			if err != nil || binding.Targets[0].OID != 0 || binding.Targets[0].Name != "" {
				t.Fatalf("malformed relation/schema no longer resolves to an empty identity: %+v %v", binding, err)
			}
		})
	}
}
