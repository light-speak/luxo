package migrate

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestIntegrationStandaloneBatchRequiresBoundSession(t *testing.T) {
	t.Run("up", func(t *testing.T) { assertStandaloneBatchRejectsUnboundSession(t, false) })
	t.Run("down", func(t *testing.T) { assertStandaloneBatchRejectsUnboundSession(t, true) })
}

func assertStandaloneBatchRejectsUnboundSession(t *testing.T, down bool) {
	t.Helper()
	fixture := migrationAuditRunner(t)
	ctx := t.Context()
	ordinary, standalone := "002_ordinary.sql", "003_standalone.sql"
	if down {
		ordinary, standalone = standalone, ordinary
		writeMigration(t, fixture.dir, standalone, "CREATE INDEX audit_idx ON audit_users (label);",
			"DROP INDEX CONCURRENTLY audit_idx;")
	} else {
		writeMigration(t, fixture.dir, standalone, "CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);",
			"DROP INDEX audit_idx;")
	}
	writeMigration(t, fixture.dir, ordinary, "UPDATE audit_users SET label = 'changed' WHERE id = 1;",
		"UPDATE audit_users SET label = 'one' WHERE id = 1;")

	// Public PGDB methods use a separate query pool and advisory lock
	// connection. This wrapper intentionally supplies no binder capability.
	runner := NewFromDB(struct{ DB }{fixture.db}, fixture.dir)
	var names []string
	var err error
	direction, label, history := "UP", "one", 1
	if down {
		auditUp(t, fixture.Runner, 2)
		direction, label, history = "DOWN", "changed", 3
		names, err = runner.Down(ctx, 2)
	} else {
		preview, _, previewErr := runner.DryRun(ctx)
		if previewErr != nil || !slices.Equal(preview, []string{ordinary, standalone}) {
			t.Fatalf("unbound preview was unexpectedly gated: %v %v", preview, previewErr)
		}
		names, err = runner.Up(ctx)
	}
	if len(names) != 0 || err == nil || !strings.Contains(err.Error(), standalone+": "+direction) ||
		!strings.Contains(err.Error(), "requires a lock-bound database session") {
		t.Errorf("unbound standalone preflight: names=%v err=%v", names, err)
	}
	var gotLabel string
	var gotHistory int
	var hasIndex, hasJournal bool
	err = fixture.db.QueryRow(ctx, `SELECT label, (SELECT count(*) FROM _migrations),
	    pg_catalog.to_regclass('audit_idx') IS NOT NULL,
	    pg_catalog.to_regclass('_luxo_migration_attempt') IS NOT NULL
	    FROM audit_users WHERE id = 1`).Scan(&gotLabel, &gotHistory, &hasIndex, &hasJournal)
	if err != nil || gotLabel != label || gotHistory != history || hasIndex != down || hasJournal {
		t.Fatalf("rejected batch changed state: label=%q history=%d index=%t journal=%t err=%v",
			gotLabel, gotHistory, hasIndex, hasJournal, err)
	}
}

func TestIntegrationStandaloneMigrationsAcceptBoundAdapter(t *testing.T) {
	fixture := migrationAuditRunner(t)
	ctx := t.Context()
	writeMigration(t, fixture.dir, auditChange, "CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);",
		"DROP INDEX CONCURRENTLY audit_idx;")
	runner := NewFromDB(fixture.openBoundSession(t), fixture.dir)
	auditUp(t, runner, 1)
	auditIndexAndHistory(t, fixture.db, true, 2)
	rolledBack, receipt, err := runner.DownWithReceipt(ctx, 1)
	if err != nil || !slices.Equal(rolledBack, []string{auditChange}) || receipt == "" {
		t.Fatalf("trusted adapter rollback: %v %q %v", rolledBack, receipt, err)
	}
	if err := runner.Reconcile(ctx, receipt, AcknowledgeMigration); err != nil {
		t.Fatal(err)
	}
	auditIndexAndHistory(t, fixture.db, false, 1)
}

func TestStandalonePreflightDoesNotInitializeHistory(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "001_users.sql", "CREATE TABLE audit_users (id int);", "SELECT 1;")
	writeMigration(t, dir, "002_index.sql", "CREATE INDEX CONCURRENTLY audit_idx ON audit_users (id);", "SELECT 1;")
	db := &mockDB{
		queryRowVal: &mockRow{},
		beginTx:     &mockTx{},
		execSQL: func(_ string) error {
			t.Error("unbound preflight attempted database mutation")
			return nil
		},
	}
	names, err := NewFromDB(db, dir).Up(t.Context())
	if len(names) != 0 || err == nil || !strings.Contains(err.Error(), "002_index.sql: UP: standalone migration requires") {
		t.Fatalf("unbound Up before history initialization: names=%v err=%v", names, err)
	}
}

type standaloneJournalTrace struct {
	prepares map[string]int
	calls    map[string]int
	phases   []string
	holder   *pgx.Conn
}

func journalStatement(query string) string {
	switch {
	case strings.Contains(query, "(id, name, direction, digest"):
		return "intent"
	case strings.Contains(query, "WITH next AS ("):
		return "replace"
	case strings.Contains(query, "SET history_committed = true"):
		return "receipt"
	case strings.Contains(query, "WHERE singleton AND id = $1 AND history_committed"):
		return "ack"
	case strings.Contains(query, "(name, checksum) VALUES ($1, $2)"), strings.Contains(query, "WITH removed AS ("):
		return "history"
	default:
		return ""
	}
}

func (s *standaloneJournalTrace) TracePrepareStart(ctx context.Context, _ *pgx.Conn, data pgx.TracePrepareStartData) context.Context {
	if key := journalStatement(data.SQL); key != "" {
		s.prepares[key]++
	}
	return ctx
}
func (*standaloneJournalTrace) TracePrepareEnd(context.Context, *pgx.Conn, pgx.TracePrepareEndData) {}
func (s *standaloneJournalTrace) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if key := journalStatement(data.SQL); key != "" {
		s.holder = conn
		s.calls[key]++
		s.phases = append(s.phases, fmt.Sprintf("%s:%c", key, conn.PgConn().TxStatus()))
	}
	return ctx
}
func (*standaloneJournalTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func traceStandaloneJournal(t *testing.T, runner *Runner) *standaloneJournalTrace {
	t.Helper()
	trace := &standaloneJournalTrace{prepares: make(map[string]int), calls: make(map[string]int)}
	freshAdvisoryLockConfig(t, t.Context(), runner.db.(*PGDB)).Tracer = trace
	return trace
}

func TestIntegrationSingleStandaloneJournalPrepare(t *testing.T) {
	for _, mode := range []struct {
		name, value string
		prepares    int
	}{
		{"default", "", 0}, {"cache-statement", "cache_statement", 0},
		{"cache-describe", "cache_describe", 1}, {"describe-exec", "describe_exec", 1},
		{"exec", "exec", 0}, {"simple-protocol", "simple_protocol", 0},
	} {
		t.Run(mode.name, func(t *testing.T) {
			for _, direction := range []string{"up", "down"} {
				t.Run(direction, func(t *testing.T) { assertSingleStandaloneJournal(t, mode.value, direction, mode.prepares, false) })
			}
		})
	}
	t.Run("legacy-column", func(t *testing.T) { assertSingleStandaloneJournal(t, "", "up", 0, true) })
}

func assertSingleStandaloneJournal(t *testing.T, mode, direction string, prepares int, legacy bool) {
	t.Helper()
	f := migrationAuditRunner(t)
	writeMigration(t, f.dir, auditChange, "CREATE INDEX CONCURRENTLY IF NOT EXISTS audit_idx ON audit_users (label);", "DROP INDEX CONCURRENTLY audit_idx;")
	if direction == "down" {
		auditUp(t, f.Runner, 1)
	}
	if legacy {
		auditExec(t, f.db, "CREATE TABLE _luxo_migration_attempt"+migrationAttemptTableDefinition)
		auditExec(t, f.db, "ALTER TABLE _luxo_migration_attempt DROP COLUMN execution_binding")
	}
	dsn, err := url.Parse(f.url)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "" {
		q := dsn.Query()
		q.Set("default_query_exec_mode", mode)
		dsn.RawQuery = q.Encode()
	}
	f.url = dsn.String()
	runner := f.open(t, f.dir)
	trace := traceStandaloneJournal(t, runner)
	want := map[string]int{"intent": 1, "history": 1, "receipt": 1, "ack": 1}
	phases := []string{"intent:I", "history:T", "receipt:T", "ack:I"}
	if legacy {
		want["intent"] = 2
		phases = append([]string{"intent:I"}, phases...)
	}
	if direction == "up" {
		auditUp(t, runner, 1)
		auditIndexAndHistory(t, runner.db, true, 2)
	} else {
		names, receipt, err := runner.DownWithReceipt(t.Context(), 1)
		if err != nil || !slices.Equal(names, []string{auditChange}) || receipt == "" {
			t.Fatalf("DownWithReceipt: %v %q %v", names, receipt, err)
		}
		pending, err := runner.PendingAttempt(t.Context())
		if err != nil || pending == nil || pending.ID != receipt || !pending.HistoryCommitted {
			t.Fatalf("committed DOWN receipt: %+v %v", pending, err)
		}
		delete(want, "ack")
		phases = phases[:3]
		auditIndexAndHistory(t, runner.db, false, 1)
	}
	for key, count := range want {
		if trace.calls[key] != count || trace.prepares[key] != prepares {
			t.Errorf("%s calls=%d prepares=%d, want %d/%d", key, trace.calls[key], trace.prepares[key], count, prepares)
		}
	}
	if !slices.Equal(trace.phases, phases) {
		t.Errorf("journal phases=%v want=%v", trace.phases, phases)
	}
	if trace.holder == nil || !trace.holder.IsClosed() {
		t.Error("standalone writer holder was not retired")
	}
}

func TestIntegrationStandaloneJournalBatchKeepsCache(t *testing.T) {
	for _, sample := range []struct {
		name        string
		down, mixed bool
	}{{"up", false, false}, {"down", true, false}, {"mixed-up", false, true}} {
		t.Run(sample.name, func(t *testing.T) { assertStandaloneJournalBatch(t, sample.down, sample.mixed) })
	}
}

func assertStandaloneJournalBatch(t *testing.T, down, mixed bool) {
	t.Helper()
	f := migrationAuditRunner(t)
	firstSQL := "CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);"
	if mixed {
		firstSQL = "UPDATE audit_users SET label='two';"
	}
	writeMigration(t, f.dir, auditChange, firstSQL, "DROP INDEX CONCURRENTLY audit_idx;")
	writeMigration(t, f.dir, executionIndex, "CREATE INDEX CONCURRENTLY audit_second_idx ON audit_users (id);", "DROP INDEX CONCURRENTLY audit_second_idx;")
	if down {
		auditUp(t, f.Runner, 2)
	}
	runner := f.open(t, f.dir)
	trace := traceStandaloneJournal(t, runner)
	if down {
		names, receipt, err := runner.DownWithReceipt(t.Context(), 2)
		if err != nil || len(names) != 2 || receipt == "" {
			t.Fatalf("batch DOWN: %v %q %v", names, receipt, err)
		}
		pending, err := runner.PendingAttempt(t.Context())
		if err != nil || pending == nil || pending.ID != receipt || pending.Name != auditChange || !pending.HistoryCommitted {
			t.Fatalf("last batch receipt: %+v %v", pending, err)
		}
		if trace.calls["replace"] != 1 || trace.prepares["replace"] != 1 || trace.calls["ack"] != 0 {
			t.Errorf("rollback fork: calls=%v prepares=%v", trace.calls, trace.prepares)
		}
	} else {
		auditUp(t, runner, 2)
	}
	for _, key := range []string{"intent", "history", "receipt"} {
		if trace.prepares[key] != 1 {
			t.Errorf("batch %s prepares=%d want one cached prepare", key, trace.prepares[key])
		}
	}
	if trace.calls["history"] != 2 {
		t.Errorf("history did not execute twice: %v", trace.calls)
	}
}

func TestIntegrationSingleStandaloneJournalFailures(t *testing.T) {
	for _, sample := range []struct {
		name, stage string
		down        bool
	}{
		{"intent", "intent", false}, {"history", "history", false}, {"receipt", "receipt", false}, {"ack", "ack", false},
		{"down-history", "history", true}, {"down-receipt", "receipt", true},
	} {
		t.Run(sample.name, func(t *testing.T) { assertSingleJournalFailure(t, sample.stage, sample.down) })
	}
}

func assertSingleJournalFailure(t *testing.T, stage string, down bool) {
	t.Helper()
	f := migrationAuditRunner(t)
	writeMigration(t, f.dir, auditChange, "CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);", "DROP INDEX CONCURRENTLY audit_idx;")
	if down {
		auditUp(t, f.Runner, 1)
	}
	auditExec(t, f.db, "CREATE TABLE IF NOT EXISTS _luxo_migration_attempt"+migrationAttemptTableDefinition)
	auditExec(t, f.db, singleJournalFault(stage))
	runner := f.open(t, f.dir)
	trace := traceStandaloneJournal(t, runner)
	var err error
	if down {
		_, _, err = runner.DownWithReceipt(t.Context(), 1)
	} else {
		_, err = runner.Up(t.Context())
	}
	code := "P0001"
	if stage == "receipt" {
		code = "22012"
	}
	auditSQLState(t, err, code)
	wantIndex, wantHistory := stage != "intent", 1
	if down {
		wantIndex, wantHistory = false, 2
	}
	if stage == "ack" {
		wantHistory = 2
	}
	auditIndexAndHistory(t, runner.db, wantIndex, wantHistory)
	pending, readErr := runner.PendingAttempt(t.Context())
	if readErr != nil {
		t.Fatal(readErr)
	}
	if stage == "intent" {
		if pending != nil {
			t.Fatalf("unconfirmed intent persisted: %+v", pending)
		}
	} else {
		if pending == nil || pending.HistoryCommitted != (stage == "ack") {
			t.Fatalf("wrong failure receipt: %+v", pending)
		}
		operation, bindErr := runner.bindMetadata(t.Context(), false)
		if bindErr != nil {
			t.Fatal(bindErr)
		}
		stored, storedErr := operation.readMigrationAttempt(t.Context(), true)
		if storedErr != nil || stored == nil {
			t.Fatalf("stored receipt: %+v %v", stored, storedErr)
		}
		binding, bindingErr := readMigrationBinding(stored.executionBinding)
		if bindingErr != nil || binding.Version != 1 || binding.Targets[0].OID == 0 {
			t.Fatalf("failure lost execution binding: %+v %v", binding, bindingErr)
		}
	}
	for key, count := range trace.prepares {
		if count != 0 {
			t.Errorf("failed one-shot %s prepared %d times", key, count)
		}
	}
	if trace.holder == nil || !trace.holder.IsClosed() {
		t.Error("failed writer holder was not retired")
	}
}

func singleJournalFault(stage string) string {
	if stage == "receipt" {
		return `CREATE FUNCTION audit_fail_journal() RETURNS trigger LANGUAGE plpgsql AS $$
   BEGIN DELETE FROM _luxo_migration_attempt WHERE singleton;
    IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW; END $$;
   CREATE TRIGGER audit_fail_journal BEFORE INSERT OR DELETE ON _migrations
   FOR EACH ROW EXECUTE FUNCTION audit_fail_journal();`
	}
	relation, event := "_migrations", "INSERT OR DELETE"
	if stage == "intent" {
		relation, event = "_luxo_migration_attempt", "INSERT"
	}
	if stage == "ack" {
		relation, event = "_luxo_migration_attempt", "DELETE"
	}
	return `CREATE FUNCTION audit_fail_journal() RETURNS trigger LANGUAGE plpgsql AS $$
  BEGIN RAISE EXCEPTION 'fixture denied journal stage'; END $$;
  CREATE TRIGGER audit_fail_journal BEFORE ` + event + ` ON ` + relation + `
  FOR EACH ROW EXECUTE FUNCTION audit_fail_journal();`
}

type standaloneJournalBoundGuard struct{ MigrationSessionDB }
type standaloneJournalTxGuard struct{ Tx }

func rejectJournalDriverOptions(args []any) error {
	for _, arg := range args {
		if _, ok := arg.(pgx.QueryExecMode); ok {
			return fmt.Errorf("native driver option reached a generic adapter")
		}
	}
	return nil
}
func (d standaloneJournalBoundGuard) Exec(ctx context.Context, query string, args ...any) error {
	if err := rejectJournalDriverOptions(args); err != nil {
		return err
	}
	return d.MigrationSessionDB.Exec(ctx, query, args...)
}
func (d standaloneJournalBoundGuard) Begin(ctx context.Context) (Tx, error) {
	tx, err := d.MigrationSessionDB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return standaloneJournalTxGuard{tx}, nil
}
func (d standaloneJournalTxGuard) Exec(ctx context.Context, query string, args ...any) error {
	if err := rejectJournalDriverOptions(args); err != nil {
		return err
	}
	return d.Tx.Exec(ctx, query, args...)
}
func TestIntegrationStandaloneJournalGenericAdapter(t *testing.T) {
	f := migrationAuditRunner(t)
	writeMigration(t, f.dir, auditChange, "CREATE INDEX CONCURRENTLY audit_idx ON audit_users (label);", "DROP INDEX CONCURRENTLY audit_idx;")
	runner := NewFromDB(standaloneJournalBoundGuard{f.openBoundSession(t)}, f.dir)
	auditUp(t, runner, 1)
	names, receipt, err := runner.DownWithReceipt(t.Context(), 1)
	if err != nil || len(names) != 1 || receipt == "" {
		t.Fatalf("generic standalone DOWN: %v %q %v", names, receipt, err)
	}
	if err := runner.Reconcile(t.Context(), receipt, AcknowledgeMigration); err != nil {
		t.Fatal(err)
	}
	auditIndexAndHistory(t, f.db, false, 1)
}

func TestSingleStandaloneJournalSelection(t *testing.T) {
	cached := &pgMigrationHolder{cachedStatements: true}
	standalone := plannedMigration{mode: migrationMode{autocommit: true}}
	for _, sample := range []struct {
		name string
		db   migrationQueries
		plan []plannedMigration
		want bool
	}{
		{"single", cached, []plannedMigration{standalone}, true},
		{"empty", cached, nil, false},
		{"ordinary", cached, []plannedMigration{{}}, false},
		{"multiple", cached, []plannedMigration{standalone, standalone}, false},
		{"different-mode", &pgMigrationHolder{}, []plannedMigration{standalone}, false},
		{"custom", &mockDB{}, []plannedMigration{standalone}, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			operation := migrationOperation{db: sample.db, uncachedStandaloneJournal: true}
			operation.configureStandaloneJournal(sample.plan)
			if operation.uncachedStandaloneJournal != sample.want {
				t.Fatalf("one-shot journal=%t want %t", operation.uncachedStandaloneJournal, sample.want)
			}
		})
	}
}

func TestSingleStandaloneJournalWriteOptions(t *testing.T) {
	for _, prior := range []string{"", "previous-down"} {
		t.Run(prior, func(t *testing.T) {
			id, checksum := int64(7), "checksum"
			attempt := storedMigrationAttempt{MigrationAttempt: MigrationAttempt{ID: "attempt", Name: "index.sql", Direction: "DOWN"}, digest: "digest", historyID: &id, historyChecksum: &checksum}
			plan := migrationExecutionPlan{targets: [2]migrationObjectReference{{kind: migrationRelation, name: `"quoted"."table"`}, {kind: migrationSchema, name: `"quoted"`}}, tablespace: migrationExplicitTablespace, tablespaceName: "somewhere"}
			db := &migrationIntentCapture{}
			operation := migrationOperation{db: db, metadata: migrationMetadata{schema: `team"blue`}, rollbackReceipt: prior}
			if err := operation.storeMigrationAttempt(t.Context(), &attempt, plan); err != nil {
				t.Fatal(err)
			}
			query, args := db.query, db.args
			operation.uncachedStandaloneJournal = true
			if err := operation.storeMigrationAttempt(t.Context(), &attempt, plan); err != nil {
				t.Fatal(err)
			}
			if db.query != query || len(db.args) != len(args)+1 || db.args[0] != pgx.QueryExecModeExec || !slices.Equal(db.args[1:], args) {
				t.Fatalf("driver option changed SQL or arguments: %#v / %#v", db.args, args)
			}
			if db.args[10] != attempt.digest || db.args[11] != attempt.historyID || db.args[12] != attempt.historyChecksum {
				t.Fatal("journal parameter identities changed")
			}
		})
	}
}
