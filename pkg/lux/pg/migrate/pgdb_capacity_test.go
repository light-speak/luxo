package migrate

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/light-speak/luxo/pkg/lux"
)

func limitedRoleTestURL(t *testing.T, limit int) (context.Context, string) {
	t.Helper()
	ctx, admin := databaseTestAdmin(t)
	role := "luxo_migration_slot_" + strings.ToLower(rand.Text())
	database := role + "_db"
	id := pgx.Identifier{role}.Sanitize()
	password := rand.Text()
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE ROLE %s LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE CONNECTION LIMIT %d PASSWORD '%s'", id, limit, password)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanup, "DROP ROLE "+id); err != nil {
			t.Error("cleanup test role", err)
		}
	})
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{database}.Sanitize()+" OWNER "+id); err != nil {
		t.Fatal(err)
	}
	cleanupLimitedRoleDatabase(t, admin, id, database)
	cfg := lux.DBConfigFromEnv()
	cfg.DBName, cfg.User, cfg.Password = database, role, password
	u, err := url.Parse(cfg.ConnectionString())
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("pool_max_conns", "1")
	q.Set("pool_min_conns", "0")
	q.Set("pool_min_idle_conns", "0")
	u.RawQuery = q.Encode()
	return ctx, u.String()
}

func cleanupLimitedRoleDatabase(t *testing.T, admin *pgx.Conn, role, database string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// The fixture creator may create a database for the child role without
		// inheriting its ownership privileges on the administrative connection.
		if _, err := admin.Exec(ctx, "SET ROLE "+role); err != nil {
			t.Error("assume fixture database owner", err)
			return
		}
		if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{database}.Sanitize()); err != nil {
			t.Error("cleanup database", err)
		}
		if _, err := admin.Exec(ctx, "RESET ROLE"); err != nil {
			t.Error("reset administrative session role", err)
		}
	})
}

func openSingleSessionTestPool(t *testing.T, ctx context.Context, dsn string) *PGDB {
	t.Helper()
	db, err := NewPGDB(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

func assertSingleSessionCapacityError(t *testing.T, err error) {
	t.Helper()
	failure, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || failure.SQLState() != "53300" {
		t.Fatalf("expected PostgreSQL connection limit, got %v", err)
	}
}

func TestIntegrationPGDBRunnerRecoversSingleRoleSession(t *testing.T) {
	ctx, dsn := limitedRoleTestURL(t, 1)
	db := openSingleSessionTestPool(t, ctx, dsn)
	dir := t.TempDir()
	const file = "001_role_session.sql"
	writeMigration(t, dir, file, "CREATE TABLE role_session (id bigint PRIMARY KEY);", "DROP TABLE role_session;")
	r := NewFromDB(db, dir)
	if planned, statements, err := r.DryRun(ctx); err != nil || !slices.Equal(planned, []string{file}) || len(statements) != 1 {
		t.Fatalf("preview: names=%v statements=%d err=%v", planned, len(statements), err)
	}
	if err := db.Exec(ctx, "SET search_path TO pg_catalog"); err != nil {
		t.Fatal(err)
	}
	var priorPID uint32
	if err := db.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&priorPID); err != nil {
		t.Fatal(err)
	}
	if applied, err := r.Up(ctx); err != nil || !slices.Equal(applied, []string{file}) {
		t.Fatalf("apply after preview and public session state: %v, %v", applied, err)
	}
	var pid uint32
	var schema string
	var table bool
	err := db.QueryRow(ctx, "SELECT pg_backend_pid(), current_schema(), to_regclass('public.role_session') IS NOT NULL").Scan(&pid, &schema, &table)
	if err != nil || pid == priorPID || schema != "public" || !table {
		t.Fatalf("public connection was not restored with clean session: PID=%d schema=%q table=%t err=%v", pid, schema, table, err)
	}
	if applied, err := r.Up(ctx); err != nil || len(applied) != 0 {
		t.Fatalf("subsequent no-op Up: %v, %v", applied, err)
	}
}

func openNoopRoleTestRunner(t *testing.T, ctx context.Context, dsn string) *Runner {
	t.Helper()
	initial, err := NewPGDB(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	err = NewFromDB(initial, dir).Init(ctx)
	initial.Close()
	if err != nil {
		t.Fatal(err)
	}
	return NewFromDB(openSingleSessionTestPool(t, ctx, dsn), dir)
}

func TestIntegrationPGDBSingleRoleNoopKeepsQueriesAvailable(t *testing.T) {
	ctx, dsn := limitedRoleTestURL(t, 1)
	r := openNoopRoleTestRunner(t, ctx, dsn)
	db := r.db.(*PGDB)
	if applied, err := r.Up(ctx); err != nil || len(applied) != 0 {
		t.Fatalf("first no-op Up: %v, %v", applied, err)
	}
	first := db.lockIdle
	if first == nil {
		t.Fatal("the clean holder should remain available to a subsequent Up")
	}
	if applied, err := r.Up(ctx); err != nil || len(applied) != 0 || db.lockIdle != first {
		t.Fatalf("second no-op Up did not reuse the holder: %v, %v", applied, err)
	}
	if names, sql, err := r.DryRun(ctx); err != nil || len(names) != 0 || len(sql) != 0 {
		t.Fatalf("preview after no-op Up: %v, %v", names, err)
	}
	if !first.IsClosed() {
		t.Fatal("the public pool reused the private lock holder")
	}
	if applied, err := r.Up(ctx); err != nil || len(applied) != 0 {
		t.Fatalf("Up after public preview: %v, %v", applied, err)
	}
	var value int
	if err := db.QueryRow(ctx, "SELECT 1").Scan(&value); err != nil || value != 1 {
		t.Fatalf("public query after capacity handoff: %d, %v", value, err)
	}
}

func TestIntegrationPGDBSingleRoleDoesNotRetireBusyOrManualSessions(t *testing.T) {
	ctx, dsn := limitedRoleTestURL(t, 1)
	db := openSingleSessionTestPool(t, ctx, dsn)
	r := NewFromDB(db, t.TempDir())
	var pid uint32
	if err := db.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	assertSingleSessionCapacityError(t, db.AcquireLock(ctx))
	rows, err := db.Query(ctx, "SELECT pg_backend_pid()")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	_, err = r.Up(ctx)
	assertSingleSessionCapacityError(t, err)
	var actual uint32
	if !rows.Next() {
		t.Fatal("outstanding public rows were lost")
	}
	if err := rows.Scan(&actual); err != nil || actual != pid {
		t.Fatalf("active public session changed: %d, %v", actual, err)
	}
	rows.Close()
	if applied, err := r.Up(ctx); err != nil || len(applied) != 0 {
		t.Fatalf("Up after public rows release: %v, %v", applied, err)
	}
}

func TestIntegrationPGDBPublicQueryPreservesHolderWithSpareCapacity(t *testing.T) {
	ctx, dsn := limitedRoleTestURL(t, 2)
	r := openNoopRoleTestRunner(t, ctx, dsn)
	db := r.db.(*PGDB)
	if applied, err := r.Up(ctx); err != nil || len(applied) != 0 {
		t.Fatalf("starting no-op Up: %v, %v", applied, err)
	}
	standby := db.lockIdle
	if standby == nil {
		t.Fatal("missing standby migration holder")
	}
	var publicPID uint32
	if err := db.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&publicPID); err != nil {
		t.Fatal(err)
	}
	if standby.IsClosed() || standby != db.lockIdle || publicPID == standby.PgConn().PID() {
		t.Fatal("public query must preserve the separate standby holder when PostgreSQL has spare capacity")
	}
	if _, err := r.Up(ctx); err != nil || db.lockIdle != standby {
		t.Fatalf("no-op Up failed to reuse the separate standby: %v", err)
	}
}

func TestIntegrationPGDBPublicPoolAlreadyHasCapacity(t *testing.T) {
	ctx, dsn := limitedRoleTestURL(t, 2)
	r := openNoopRoleTestRunner(t, ctx, dsn)
	db := r.db.(*PGDB)
	if _, err := r.Up(ctx); err != nil || db.lockIdle == nil || !db.poolSlotHeld.Load() {
		t.Fatalf("expected holder to leave the borrowed-slot marker: %v", err)
	}
	standby := db.lockIdle
	// A connection may arrive independently of the next public operation.
	pooled, err := db.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	publicPID := pooled.Conn().PgConn().PID()
	pooled.Release()
	var usedPID uint32
	if err := db.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&usedPID); err != nil ||
		usedPID != publicPID || db.poolSlotHeld.Load() || db.lockIdle != standby {
		t.Fatalf("public request did not use already restored capacity: %d, %v", usedPID, err)
	}
}

func TestIntegrationPGDBPublicHandoffWaitersRespectTheirContext(t *testing.T) {
	ctx, dsn := limitedRoleTestURL(t, 1)
	r := openNoopRoleTestRunner(t, ctx, dsn)
	db := r.db.(*PGDB)
	if _, err := r.Up(ctx); err != nil {
		t.Fatal(err)
	}
	handoff, wait, err := db.claimPublicHandoff(ctx)
	if err != nil || handoff == nil || wait != nil || handoff.wait != nil {
		t.Fatalf("test requires a handoff owner without a waiting caller: %v", err)
	}
	complete := sync.OnceValue(func() error { return db.completePublicHandoff(ctx, handoff) })
	defer func() {
		if err := complete(); err != nil {
			t.Error(err)
		}
	}()
	cancelled, cancel := context.WithCancel(ctx)
	defer cancel()
	cancelledQuery := startPublicHandoffWaiter(t, cancelled, db)
	waitingQuery := startPublicHandoffWaiter(t, ctx, db)
	cancel()
	if err := publicHandoffWaitResult(t, ctx, cancelledQuery); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting query lost its cancellation: %v", err)
	}
	select {
	case <-handoff.wait:
		t.Fatal("cancelled public query interrupted its handoff owner")
	default:
	}
	if err := complete(); err != nil {
		t.Fatal(err)
	}
	if err := publicHandoffWaitResult(t, ctx, waitingQuery); err != nil {
		t.Fatalf("waiting query did not resume after the connection returned: %v", err)
	}
}

type publicHandoffWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *publicHandoffWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func startPublicHandoffWaiter(t *testing.T, parent context.Context, db *PGDB) <-chan error {
	t.Helper()
	ctx := &publicHandoffWaitContext{Context: parent, waiting: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- db.QueryRow(ctx, "SELECT 1").Scan(new(int)) }()
	select {
	case <-ctx.waiting:
	case err := <-result:
		t.Fatalf("public query finished before waiting for the handoff: %v", err)
	case <-parent.Done():
		t.Fatal("public query never waited for its handoff")
	}
	return result
}

func publicHandoffWaitResult(t *testing.T, ctx context.Context, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		t.Fatal("public query did not settle after the handoff")
		return nil
	}
}

func TestIntegrationPGDBCloseCancelsPublicHandoffBeforePoolDrain(t *testing.T) {
	ctx, dsn := limitedRoleTestURL(t, 2)
	r := openNoopRoleTestRunner(t, ctx, dsn)
	db := r.db.(*PGDB)
	if _, err := r.Up(ctx); err != nil || db.lockIdle == nil || !db.poolSlotHeld.Load() {
		t.Fatalf("expected unlocked holder with borrowed public capacity: %v", err)
	}
	entered, unblock := blockPublicHandoffDial(t, ctx, db, dsn)
	defer unblock()
	query := make(chan error, 1)
	go func() { query <- db.QueryRow(ctx, "SELECT 1").Scan(new(int)) }()
	select {
	case <-entered:
	case err := <-query:
		t.Fatalf("public query returned before the pool dial began: %v", err)
	case <-ctx.Done():
		t.Fatal("public handoff did not reach the pool dial")
	}
	closed := make(chan struct{})
	go func() {
		db.Close()
		close(closed)
	}()
	select {
	case err := <-query:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("adapter shutdown did not cancel the blocked public acquire: %v", err)
		}
	case <-closed:
		t.Fatal("adapter closed before its pool constructor returned")
	case <-ctx.Done():
		t.Fatal("public acquire waited for the pool to drain")
	}
	unblock()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("adapter did not close after the pool constructor returned")
	}
}

func TestIntegrationPGDBCloseCancelsUnregisteredPublicHandoff(t *testing.T) {
	ctx, dsn := limitedRoleTestURL(t, 2)
	r := openNoopRoleTestRunner(t, ctx, dsn)
	db := r.db.(*PGDB)
	if _, err := r.Up(ctx); err != nil {
		t.Fatal(err)
	}
	handoff, wait, err := db.claimPublicHandoff(ctx)
	if err != nil || handoff == nil || wait != nil {
		t.Fatalf("public request did not reserve the borrowed slot: %v", err)
	}
	complete := sync.OnceValue(func() error { return db.completePublicHandoff(ctx, handoff) })
	defer func() { _ = complete() }()
	waiting := startPublicHandoffWaiter(t, ctx, db)
	closed := make(chan struct{})
	go func() {
		db.Close()
		close(closed)
	}()
	<-db.lockLifetime.Done()
	if err := publicHandoffWaitResult(t, ctx, waiting); !errors.Is(err, context.Canceled) {
		t.Errorf("waiting public request did not stop while its owner was reserved: %v", err)
	}
	if err := complete(); !errors.Is(err, context.Canceled) {
		t.Errorf("public request started reconnecting after shutdown: %v", err)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("public handoff did not release its slot")
	}
}

func blockPublicHandoffDial(t *testing.T, ctx context.Context, db *PGDB, dsn string) (<-chan struct{}, func()) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	cfg.BeforeConnect = func(context.Context, *pgx.ConnConfig) error {
		close(entered)
		<-release
		return context.Canceled
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	db.pool.Close()
	db.pool, db.pgMigrationQueries.conn = pool, pool
	return entered, sync.OnceFunc(func() { close(release) })
}

func TestRetryRetiredSlotOnlyRetriesConnectionLimit(t *testing.T) {
	rejected := &pgconn.PgError{Code: "53300"}
	other := errors.New("other connect failure")
	calls := 0
	conn, err := retryRetiredSlot(t.Context(), func(context.Context) (int, error) {
		calls++
		if calls == 1 {
			return 0, rejected
		}
		return 99, other
	})
	if conn != 99 || !errors.Is(err, other) || calls != 2 {
		t.Fatalf("retry must stop on unrelated errors: %#v, %v, calls=%d", conn, err, calls)
	}
	calls = 0
	conn, err = retryRetiredSlot(t.Context(), func(context.Context) (int, error) {
		calls++
		if calls == 1 {
			return 0, rejected
		}
		return 7, nil
	})
	if conn != 7 || err != nil || calls != 2 {
		t.Fatalf("retry must return the connection once capacity is restored: %#v, %v, calls=%d", conn, err, calls)
	}
	ctx, stop := context.WithCancel(t.Context())
	stop()
	_, err = retryRetiredSlot(ctx, func(context.Context) (int, error) { return 0, rejected })
	if !errors.Is(err, context.Canceled) || !errors.Is(err, rejected) {
		t.Fatalf("expected both context and PostgreSQL diagnostics: %v", err)
	}
}

func TestPGDBPublicHandoffRejectsCancelledClaims(t *testing.T) {
	ctx, stop := context.WithCancel(t.Context())
	stop()
	db := &PGDB{lockLifetime: t.Context()}
	if owner, wait, err := db.claimPublicHandoff(t.Context()); err != nil || owner != nil || wait != nil {
		t.Fatalf("cleared slot acquired another handoff: %v %p %v", wait, owner, err)
	}
	db.poolSlotHeld.Store(true)
	if owner, wait, err := db.claimPublicHandoff(ctx); !errors.Is(err, context.Canceled) || owner != nil || wait != nil {
		t.Fatalf("cancelled request acquired a handoff: %v %p %v", wait, owner, err)
	}
	db.lockLifetime = ctx
	db.publicHandoff = &pgPublicHandoff{wait: make(chan struct{})}
	if err := db.preparePublicSession(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting public request ignored adapter shutdown: %v", err)
	}
}

func TestPGDBPublicOperationsPropagateCapacityShutdown(t *testing.T) {
	lifetime, stop := context.WithCancel(t.Context())
	stop()
	db := &PGDB{lockLifetime: lifetime}
	db.poolSlotHeld.Store(true)
	ctx := t.Context()
	for _, sample := range []struct {
		name string
		call func() error
	}{
		{"exec", func() error { return db.Exec(ctx, "SELECT 1") }},
		{"query", func() error { _, err := db.Query(ctx, "SELECT 1"); return err }},
		{"query row", func() error { return db.QueryRow(ctx, "SELECT 1").Scan(new(int)) }},
		{"begin", func() error { _, err := db.Begin(ctx); return err }},
		{"standalone", func() error { return db.execMigrationStatement(ctx, "SELECT 1") }},
	} {
		t.Run(sample.name, func(t *testing.T) {
			if err := sample.call(); !errors.Is(err, context.Canceled) {
				t.Fatalf("lost adapter shutdown: %v", err)
			}
		})
	}
}

func TestPGDBPublicHandoffLeavesActiveMigrationSlotAlone(t *testing.T) {
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, "postgres://fixture:fixture@127.0.0.1:1/unused?pool_min_conns=0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	db := &PGDB{pool: pool, lockLifetime: ctx, lockSlot: make(chan struct{}, 1)}
	db.poolSlotHeld.Store(true)
	db.lockSlot <- struct{}{}
	if owner, wait, err := db.claimPublicHandoff(ctx); err != nil || owner != nil || wait != nil || len(db.lockSlot) != 1 {
		t.Fatalf("public code took over active slot: %v %p %v", wait, owner, err)
	}
	<-db.lockSlot
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	if db.retireIdlePoolSession(stopped) {
		t.Fatal("cancelled request retired a pool session")
	}
}

func TestIntegrationPGDBPreservesBorrowedSlotAfterReconnectFailure(t *testing.T) {
	ctx, dsn := limitedRoleTestURL(t, 1)
	runner := openNoopRoleTestRunner(t, ctx, dsn)
	db := runner.db.(*PGDB)
	if _, err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if db.lockIdle == nil || !db.poolSlotHeld.Load() {
		t.Fatal("standby did not retain its borrowed slot")
	}
	if err := db.lockIdle.Close(ctx); err != nil {
		t.Fatal(err)
	}
	failed := errors.New("fixture could not dial a new migration holder")
	db.lockConfig = db.lockConfig.Copy()
	db.lockConfig.DialFunc = func(context.Context, string, string) (net.Conn, error) { return nil, failed }
	if _, err := runner.Up(ctx); !errors.Is(err, failed) || !db.poolSlotHeld.Load() {
		t.Fatalf("failed reconnect lost its borrowed capacity or cause: %v", err)
	}
	var n int
	if err := db.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil || n != 1 || db.poolSlotHeld.Load() {
		t.Fatalf("public query did not restore capacity: value=%d error=%v", n, err)
	}
}

func TestIntegrationPGDBRetiresOnlyOneIdlePoolConnection(t *testing.T) {
	ctx, dsn := limitedRoleTestURL(t, 2)
	configured, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := configured.Query()
	query.Set("pool_max_conns", "2")
	configured.RawQuery = query.Encode()
	db := openSingleSessionTestPool(t, ctx, configured.String())
	checkouts := make([]*pgxpool.Conn, 0, 2)
	for range 2 {
		checkout, err := db.pool.Acquire(ctx)
		if err != nil {
			for _, active := range checkouts {
				active.Release()
			}
			t.Fatal(err)
		}
		checkouts = append(checkouts, checkout)
	}
	for _, checkout := range checkouts {
		checkout.Release()
	}
	if !db.retireIdlePoolSession(ctx) || db.pool.Stat().IdleConns() != 1 {
		t.Fatalf("capacity fallback did not preserve the other idle backend: %+v", db.pool.Stat())
	}
}
