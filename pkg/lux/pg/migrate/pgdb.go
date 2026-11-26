package migrate

import (
	"context"
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

	lockConfig   *pgx.ConnConfig
	lockSlot     chan struct{}
	lockLifetime context.Context
	stopLocks    context.CancelFunc
	lockMu       sync.Mutex
	lockWork     sync.WaitGroup
	lockConn     *pgx.Conn
	lockIdle     *pgx.Conn
}

var _ DB = (*PGDB)(nil)

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

func (d *PGDB) Exec(ctx context.Context, sql string, args ...any) error {
	d.invalidatePingSession()
	return d.pgMigrationQueries.Exec(ctx, sql, args...)
}

func (d *PGDB) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	d.invalidatePingSession()
	return d.pgMigrationQueries.Query(ctx, sql, args...)
}

func (d *PGDB) QueryRow(ctx context.Context, sql string, args ...any) Row {
	d.invalidatePingSession()
	return d.pgMigrationQueries.QueryRow(ctx, sql, args...)
}

func (d *PGDB) Begin(ctx context.Context) (Tx, error) {
	d.invalidatePingSession()
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
	d.invalidatePingSession()
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

// Migration SQL can alter session state. Reuse only holders that performed read-only preflight.
type pgMigrationHolder struct {
	pgMigrationQueries[*pgx.Conn]
	wrote bool
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
	conn, err := d.acquireLockSession(lockCtx)
	if err != nil {
		return err
	}

	d.lockMu.Lock()
	err = d.lockLifetime.Err()
	if err == nil {
		d.lockConn = conn
	}
	d.lockMu.Unlock()
	if err != nil {
		d.releaseLockSession(ctx, conn, false)
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

func (d *PGDB) acquireLockSession(ctx context.Context) (*pgx.Conn, error) {
	idle, err := d.reserveLock(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := d.lockSession(ctx, idle)
	if err != nil {
		d.finishLock()
	}
	return conn, err
}

func (d *PGDB) reserveLock(ctx context.Context) (*pgx.Conn, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-d.lockLifetime.Done():
		return nil, d.lockLifetime.Err()
	case d.lockSlot <- struct{}{}:
	}
	d.lockMu.Lock()
	defer d.lockMu.Unlock()
	if err := d.lockLifetime.Err(); err != nil {
		<-d.lockSlot
		return nil, err
	}
	d.lockWork.Add(1)
	idle := d.lockIdle
	d.lockIdle = nil
	return idle, nil
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

func (d *PGDB) lockSession(ctx context.Context, idle *pgx.Conn) (*pgx.Conn, error) {
	const lock = "SELECT pg_advisory_lock($1)"
	if idle == nil {
		idle = d.takePingSession(ctx)
	}
	if idle != nil {
		if _, err := idle.Exec(ctx, lock, pgLockID); err == nil {
			return idle, nil
		} else if !idle.IsClosed() || ctx.Err() != nil {
			discardLockSession(ctx, idle)
			return nil, err
		}
		discardLockSession(ctx, idle)
	}
	// A separate session preserves the migration's access to the query pool,
	// including when that pool is configured with only one connection.
	conn, err := pgx.ConnectConfig(ctx, d.lockConfig)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, lock, pgLockID); err != nil {
		discardLockSession(ctx, conn)
		return nil, err
	}
	return conn, nil
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
		d.releaseLockSession(ctx, conn, true)
	}
}

func (d *PGDB) releaseLockSession(ctx context.Context, conn *pgx.Conn, reusable bool) {
	defer d.finishLock()
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
	d.invalidatePingSession()
	d.pool.Close()
	d.ReleaseLock(context.Background())
	d.lockWork.Wait()
	d.lockMu.Lock()
	defer d.lockMu.Unlock()
	if idle := d.lockIdle; idle != nil {
		d.lockIdle = nil
		discardLockSession(context.Background(), idle)
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
