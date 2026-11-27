package migrate

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pgLockID = 707813578
const pgLockCleanupTimeout = 5 * time.Second

// PGDB implements DB for PostgreSQL using pgxpool.
type PGDB struct {
	pgMigrationQueries[*pgxpool.Pool]
	pool *pgxpool.Pool

	pingConn       atomic.Pointer[pgx.Conn]
	pingFreshUntil time.Time

	lockConfig    *pgx.ConnConfig
	lockSlot      chan struct{}
	lockLifetime  context.Context
	stopLocks     context.CancelFunc
	lockMu        sync.Mutex
	lockWork      sync.WaitGroup
	lockConn      *pgx.Conn
	lockIdle      *pgx.Conn
	poolSlotHeld  atomic.Bool
	publicHandoff *pgPublicHandoff
}

var _ DB = (*PGDB)(nil)

type pgPublicHandoff struct {
	wait   chan struct{}
	cancel context.CancelFunc
}

// NewPGDB creates a PGDB from a connection URL.
func NewPGDB(ctx context.Context, url string) (*PGDB, error) {
	config, err := pgxpool.ParseConfig(url)
	opened := time.Now()
	var pool *pgxpool.Pool
	if err == nil {
		pool, err = pgxpool.NewWithConfig(ctx, config)
	}
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	pingStarted := time.Now()
	ping, err := pingMigrationPool(ctx, pool)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	lifetime, stopLocks := context.WithCancel(context.Background())
	db := &PGDB{
		pgMigrationQueries: pgMigrationQueries[*pgxpool.Pool]{conn: pool},
		pool:               pool, lockConfig: config.ConnConfig, lockSlot: make(chan struct{}, 1),
		lockLifetime: lifetime, stopLocks: stopLocks,
	}
	// Preserve explicitly reserved pool capacity for public queries.
	if config.MinConns == 0 && config.MinIdleConns == 0 {
		// AcquireAllIdle skips the pool's normal idle-ping and lifetime checks.
		freshUntil := pingStarted.Add(min(config.MaxConnIdleTime, time.Second))
		earliestExpiry := opened.Add(config.MaxConnLifetime).Add(min(config.MaxConnLifetimeJitter, 0))
		if earliestExpiry.Before(freshUntil) {
			freshUntil = earliestExpiry
		}
		db.pingFreshUntil = freshUntil
		db.pingConn.Store(ping)
	}
	return db, nil
}

func pingMigrationPool(ctx context.Context, pool *pgxpool.Pool) (*pgx.Conn, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	if err := conn.Ping(ctx); err != nil {
		return nil, err
	}
	return conn.Conn(), nil
}

// Any public SQL can change session state, including a query returning rows.
func (d *PGDB) invalidatePingSession() {
	if d.pingConn.Load() != nil {
		d.pingConn.Store(nil)
	}
}

// preparePublicSession gives back capacity borrowed by a completed migration.
func (d *PGDB) preparePublicSession(ctx context.Context) error {
	d.invalidatePingSession()
	for d.poolSlotHeld.Load() {
		handoff, wait, err := d.claimPublicHandoff(ctx)
		if handoff != nil {
			return d.completePublicHandoff(ctx, handoff)
		}
		if err != nil || wait == nil {
			return err
		}
		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		case <-d.lockLifetime.Done():
			return d.lockLifetime.Err()
		}
	}
	return nil
}

func (d *PGDB) claimPublicHandoff(ctx context.Context) (*pgPublicHandoff, <-chan struct{}, error) {
	d.lockMu.Lock()
	defer d.lockMu.Unlock()
	if !d.poolSlotHeld.Load() {
		return nil, nil, nil
	}
	if err := d.lockLifetime.Err(); err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if handoff := d.publicHandoff; handoff != nil {
		if handoff.wait == nil {
			handoff.wait = make(chan struct{})
		}
		return nil, handoff.wait, nil
	}
	if stat := d.pool.Stat(); stat.IdleConns() != 0 || stat.AcquiredConns() != 0 {
		d.poolSlotHeld.Store(false)
		return nil, nil, nil
	}
	select {
	case d.lockSlot <- struct{}{}:
		// Reserve ownership of the standby while reconnecting. Public callers
		// can wait with their own context without holding the lifecycle mutex.
		d.lockWork.Add(1)
		d.publicHandoff = &pgPublicHandoff{}
		return d.publicHandoff, nil, nil
	default:
		// Another migration owns the slot. Leave public operations on the pool.
		return nil, nil, nil
	}
}

func (d *PGDB) completePublicHandoff(ctx context.Context, handoff *pgPublicHandoff) error {
	reconnect, cancel := context.WithCancel(ctx)
	defer cancel()
	d.lockMu.Lock()
	idle := d.lockIdle
	handoff.cancel = cancel
	stopped := d.lockLifetime.Err() != nil
	d.lockMu.Unlock()
	// Close either found the registered cancellation or stopped the adapter
	// before registration. Check both so the pool never delays shutdown.
	if stopped {
		cancel()
	}
	conn, retired, err := d.restorePublicSession(reconnect, idle)
	if err == nil {
		conn.Release()
	}
	d.lockMu.Lock()
	if retired {
		d.lockIdle = nil
	}
	if err == nil {
		d.poolSlotHeld.Store(false)
	}
	d.publicHandoff = nil
	d.finishLock()
	if handoff.wait != nil {
		close(handoff.wait)
	}
	d.lockMu.Unlock()
	return err
}

func (d *PGDB) restorePublicSession(ctx context.Context, idle *pgx.Conn) (*pgxpool.Conn, bool, error) {
	if idle != nil {
		conn, err := d.pool.Acquire(ctx)
		if !connectionCapacityError(err) || ctx.Err() != nil {
			return conn, false, err
		}
		retireLockCapacity(ctx, idle)
	}
	conn, err := retryRetiredSlot(ctx, d.pool.Acquire)
	return conn, idle != nil, err
}

func (d *PGDB) Exec(ctx context.Context, sql string, args ...any) error {
	if err := d.preparePublicSession(ctx); err != nil {
		return err
	}
	return d.pgMigrationQueries.Exec(ctx, sql, args...)
}

func (d *PGDB) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	if err := d.preparePublicSession(ctx); err != nil {
		return nil, err
	}
	return d.pgMigrationQueries.Query(ctx, sql, args...)
}

func (d *PGDB) QueryRow(ctx context.Context, sql string, args ...any) Row {
	if err := d.preparePublicSession(ctx); err != nil {
		return pgErrorRow{err}
	}
	return d.pgMigrationQueries.QueryRow(ctx, sql, args...)
}

func (d *PGDB) Begin(ctx context.Context) (Tx, error) {
	if err := d.preparePublicSession(ctx); err != nil {
		return nil, err
	}
	return d.pgMigrationQueries.Begin(ctx)
}

type pgQueryConnection interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Begin(context.Context) (pgx.Tx, error)
}

type pgMigrationQueries[Connection pgQueryConnection] struct {
	conn Connection
}

func (d pgMigrationQueries[Connection]) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := d.conn.Exec(ctx, sql, args...)
	return err
}

func (d *PGDB) execMigrationStatement(ctx context.Context, sql string) error {
	if err := d.preparePublicSession(ctx); err != nil {
		return err
	}
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if err := checkMigrationStringMode(conn.Conn()); err != nil {
		return err
	}
	_, err = conn.Exec(ctx, sql)
	return err
}

func checkMigrationStringMode(conn *pgx.Conn) error {
	// The PostgreSQL wire protocol reports changes to this setting. A prior
	// migration or a different pool connection can have a different mode.
	if conn.PgConn().ParameterStatus("standard_conforming_strings") != "on" {
		return errMigrationStringMode
	}
	return nil
}

func (d pgMigrationQueries[Connection]) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	rows, err := d.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &pgRows{rows}, nil
}

func (d pgMigrationQueries[Connection]) QueryRow(ctx context.Context, sql string, args ...any) Row {
	return d.conn.QueryRow(ctx, sql, args...)
}

func (d pgMigrationQueries[Connection]) Begin(ctx context.Context) (Tx, error) {
	tx, err := d.conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &pgTx{tx}, nil
}

type pgErrorRow struct{ err error }

func (r pgErrorRow) Scan(...any) error { return r.err }

// Migration SQL can alter session state. Reuse only holders that performed read-only preflight.
type pgMigrationHolder struct {
	pgMigrationQueries[*pgx.Conn]
	wrote            bool
	cachedStatements bool
}

func (d *pgMigrationHolder) execMigrationStatement(ctx context.Context, sql string) error {
	if err := checkMigrationStringMode(d.conn); err != nil {
		return err
	}
	return d.Exec(ctx, sql)
}

func (d *pgMigrationHolder) Exec(ctx context.Context, sql string, args ...any) error {
	d.wrote = true
	return d.pgMigrationQueries.Exec(ctx, sql, args...)
}

func (d *pgMigrationHolder) Begin(ctx context.Context) (Tx, error) {
	d.wrote = true
	return d.pgMigrationQueries.Begin(ctx)
}

func (d *PGDB) AcquireLock(ctx context.Context) error {
	lockCtx, stop := d.lockContext(ctx)
	defer stop()
	session, err := d.acquireLockSession(lockCtx, false)
	if err != nil {
		return err
	}

	d.lockMu.Lock()
	err = d.lockLifetime.Err()
	if err == nil {
		d.lockConn = session.conn
	}
	d.lockMu.Unlock()
	if err != nil {
		d.releaseLockSession(ctx, session, false)
	}
	return err
}

func (d *PGDB) lockContext(ctx context.Context) (context.Context, func()) {
	lockCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(d.lockLifetime, cancel)
	return lockCtx, func() {
		stop()
		cancel()
	}
}

type pgLockSession struct {
	conn     *pgx.Conn
	poolSlot bool
}

func (d *PGDB) acquireLockSession(ctx context.Context, managed bool) (pgLockSession, error) {
	idle, err := d.reserveLock(ctx, managed)
	if err != nil {
		return pgLockSession{}, err
	}
	session, err := d.lockSession(ctx, idle, managed)
	if err != nil {
		if session.poolSlot {
			d.poolSlotHeld.Store(true)
		}
		d.finishLock()
	}
	return session, err
}

func (d *PGDB) reserveLock(ctx context.Context, managed bool) (pgLockSession, error) {
	select {
	case <-ctx.Done():
		return pgLockSession{}, ctx.Err()
	case <-d.lockLifetime.Done():
		return pgLockSession{}, d.lockLifetime.Err()
	case d.lockSlot <- struct{}{}:
	}
	d.lockMu.Lock()
	defer d.lockMu.Unlock()
	if err := d.lockLifetime.Err(); err != nil {
		<-d.lockSlot
		return pgLockSession{}, err
	}
	d.lockWork.Add(1)
	pending := d.poolSlotHeld.Swap(false)
	session := pgLockSession{conn: d.lockIdle, poolSlot: managed && pending}
	d.lockIdle = nil
	return session, nil
}

func (d *PGDB) takePingSession(ctx context.Context) *pgx.Conn {
	ping := d.pingConn.Load()
	if ping == nil || ctx.Err() != nil {
		return nil
	}
	if !time.Now().Before(d.pingFreshUntil) {
		d.invalidatePingSession()
		return nil
	}
	var holder *pgx.Conn
	for _, conn := range d.pool.AcquireAllIdle(ctx) {
		// The pool has given us exclusive access. If a public operation started
		// before this claim, it has already cleared the candidate.
		if conn.Conn() == ping && ctx.Err() == nil && time.Now().Before(d.pingFreshUntil) &&
			d.pingConn.CompareAndSwap(ping, nil) {
			holder = conn.Hijack()
		} else {
			conn.Release()
		}
	}
	// A pool that retired or checked out the Ping connection has no candidate.
	d.invalidatePingSession()
	return holder
}

func (d *PGDB) lockSession(ctx context.Context, session pgLockSession, managed bool) (pgLockSession, error) {
	const lock = "SELECT pg_advisory_lock($1)"
	if session.conn == nil {
		if ping := d.takePingSession(ctx); ping != nil {
			session = pgLockSession{conn: ping, poolSlot: managed}
		}
	}
	if idle := session.conn; idle != nil {
		if _, err := idle.Exec(ctx, lock, pgLockID); err == nil {
			return session, nil
		} else if !idle.IsClosed() || ctx.Err() != nil {
			discardLockSession(ctx, idle)
			return session, err
		}
		discardLockSession(ctx, idle)
	}
	conn, err := d.connectLockSession(ctx, session.poolSlot)
	if err != nil && managed && connectionCapacityError(err) && d.retireIdlePoolSession(ctx) {
		session.poolSlot = true
		conn, err = d.connectLockSession(ctx, true)
	}
	if err != nil {
		return session, err
	}
	if _, err := conn.Exec(ctx, lock, pgLockID); err != nil {
		discardLockSession(ctx, conn)
		return session, err
	}
	session.conn = conn
	return session, nil
}

func (d *PGDB) connectLockSession(ctx context.Context, retiredSlot bool) (*pgx.Conn, error) {
	if retiredSlot {
		return retryRetiredSlot(ctx, func(ctx context.Context) (*pgx.Conn, error) {
			return pgx.ConnectConfig(ctx, d.lockConfig)
		})
	}
	return pgx.ConnectConfig(ctx, d.lockConfig)
}

func connectionCapacityError(err error) bool {
	failure, ok := errors.AsType[*pgconn.PgError](err)
	return ok && failure.SQLState() == "53300"
}

func (d *PGDB) retireIdlePoolSession(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	idle := d.pool.AcquireAllIdle(ctx)
	var retire *pgxpool.Conn
	if len(idle) != 0 && ctx.Err() == nil {
		retire = idle[0]
		idle = idle[1:]
	}
	for _, conn := range idle {
		conn.Release()
	}
	if retire == nil {
		return false
	}
	// A public backend may retain SQL session state. Reconnect using the
	// original startup configuration before taking the advisory lock.
	retireLockCapacity(ctx, retire.Hijack())
	return true
}

func retireLockCapacity(ctx context.Context, conn *pgx.Conn) {
	closeCtx, cancel := context.WithTimeout(ctx, pgLockCleanupTimeout)
	defer cancel()
	closeLockSession(closeCtx, conn)
}

func retryRetiredSlot[T any](ctx context.Context, connect func(context.Context) (T, error)) (T, error) {
	conn, err := connect(ctx)
	if !connectionCapacityError(err) {
		return conn, err
	}
	// Closing our socket does not acknowledge the backend's role-slot release.
	// Retry only authentication-time capacity errors after retiring our connection.
	retry, cancel := context.WithTimeout(ctx, pgLockCleanupTimeout)
	defer cancel()
	interval := time.NewTicker(10 * time.Millisecond)
	defer interval.Stop()
	for connectionCapacityError(err) {
		select {
		case <-retry.Done():
			return conn, errors.Join(err, retry.Err())
		case <-interval.C:
			conn, err = connect(retry)
		}
	}
	return conn, err
}

func (d *PGDB) finishLock() {
	<-d.lockSlot
	d.lockWork.Done()
}

func (d *PGDB) ReleaseLock(ctx context.Context) {
	d.lockMu.Lock()
	conn := d.lockConn
	d.lockConn = nil
	d.lockMu.Unlock()
	if conn != nil {
		d.releaseLockSession(ctx, pgLockSession{conn: conn}, true)
	}
}

func (d *PGDB) releaseLockSession(ctx context.Context, session pgLockSession, reusable bool) {
	defer d.finishLock()
	conn := session.conn
	if session.poolSlot {
		defer d.poolSlotHeld.Store(true)
	}
	if !reusable {
		discardLockSession(ctx, conn)
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pgLockCleanupTimeout)
	defer cancel()
	var unlocked bool
	err := conn.QueryRow(cleanupCtx, "SELECT pg_advisory_unlock($1)", pgLockID).Scan(&unlocked)
	if err == nil && unlocked {
		d.lockMu.Lock()
		if d.lockLifetime.Err() == nil {
			d.lockIdle = conn
			conn = nil
		}
		d.lockMu.Unlock()
	}
	if conn != nil {
		closeLockSession(cleanupCtx, conn)
	}
}

func discardLockSession(ctx context.Context, conn *pgx.Conn) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pgLockCleanupTimeout)
	defer cancel()
	closeLockSession(cleanupCtx, conn)
}

func closeLockSession(ctx context.Context, conn *pgx.Conn) {
	if conn.IsClosed() {
		// pgx may already have initiated an asynchronous close after cancellation.
		_ = conn.PgConn().Conn().Close()
		return
	}
	_ = conn.Close(ctx)
}

func (d *PGDB) Close() {
	d.stopLocks()
	d.cancelPublicHandoff()
	d.invalidatePingSession()
	d.ReleaseLock(context.Background())
	d.pool.Close()
	d.lockWork.Wait()
	d.lockMu.Lock()
	defer d.lockMu.Unlock()
	if idle := d.lockIdle; idle != nil {
		d.lockIdle = nil
		discardLockSession(context.Background(), idle)
	}
}

func (d *PGDB) cancelPublicHandoff() {
	d.lockMu.Lock()
	var cancel context.CancelFunc
	if handoff := d.publicHandoff; handoff != nil {
		cancel = handoff.cancel
	}
	d.lockMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// pgRows wraps pgx.Rows to implement Rows.
type pgRows struct {
	rows pgx.Rows
}

func (r *pgRows) Next() bool             { return r.rows.Next() }
func (r *pgRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r *pgRows) Err() error             { return r.rows.Err() }
func (r *pgRows) Close()                 { r.rows.Close() }

// pgTx wraps pgx.Tx to implement Tx.
type pgTx struct {
	tx pgx.Tx
}

func (t *pgTx) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := t.tx.Exec(ctx, sql, args...)
	return err
}

func (t *pgTx) execMigrationStatement(ctx context.Context, sql string) error {
	if err := checkMigrationStringMode(t.tx.Conn()); err != nil {
		return err
	}
	return t.Exec(ctx, sql)
}

func (t *pgTx) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t *pgTx) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }
