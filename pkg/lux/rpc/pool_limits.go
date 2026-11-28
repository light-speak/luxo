package rpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

var (
	// ErrPoolExhausted rejects work beyond the waiting or streaming budget.
	ErrPoolExhausted error = luxerrors.New("LRPCPoolBusy", 503, "error.lrpc_pool_busy")
	// ErrPoolWaitTimeout reports expiration before a connection slot is acquired.
	ErrPoolWaitTimeout error = luxerrors.New("LRPCPoolWaitTimeout", 503, "error.lrpc_pool_wait_timeout")
)

// PoolOptions applies per destination and client, not globally across replicas.
// MaxOpen includes idle, borrowed, dialing, and handshaking connections.
// Streams can use at most MaxOpen minus UnaryReserve connections and never
// enter the wait queue. Unary calls can use any available connection.
// Zero UnaryReserve reserves one quarter of MaxOpen, rounded up. A reserve
// equal to MaxOpen disables streams. Zero MaxWaiters rejects unary saturation
// immediately; zero MaxIdle disables idle retention.
type PoolOptions struct {
	MaxIdle      int
	MaxOpen      int
	MaxWaiters   int
	WaitTimeout  time.Duration
	DialTimeout  time.Duration
	UnaryReserve int
}

func DefaultPoolOptions() PoolOptions {
	return PoolOptions{MaxIdle: 32, MaxOpen: 32, MaxWaiters: 32, WaitTimeout: 5 * time.Second, DialTimeout: 5 * time.Second}
}

func poolOptionsFromEnv() (PoolOptions, error) {
	options := DefaultPoolOptions()
	var err error
	if options.MaxOpen, err = readPoolLimit("LRPC_MAX_OPEN", options.MaxOpen); err != nil {
		return PoolOptions{}, err
	}
	if options.UnaryReserve, err = readPoolLimit("LRPC_UNARY_RESERVE", options.UnaryReserve); err != nil {
		return PoolOptions{}, err
	}
	if options.MaxOpen == 0 {
		return PoolOptions{}, errors.New("lrpc: LRPC_MAX_OPEN must be positive")
	}
	if options.UnaryReserve > options.MaxOpen {
		return PoolOptions{}, errors.New("lrpc: LRPC_UNARY_RESERVE must be at most LRPC_MAX_OPEN")
	}
	options.MaxIdle = min(options.MaxIdle, options.MaxOpen)
	return options, nil
}

func readPoolLimit(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("lrpc: %s must be a non-negative integer", key)
	}
	return value, nil
}

func NewPoolWithOptions(addr string, options PoolOptions) (*Pool, error) {
	if options.MaxOpen <= 0 || options.MaxIdle < 0 || options.MaxIdle > options.MaxOpen ||
		options.MaxWaiters < 0 || options.WaitTimeout < 0 || (options.MaxWaiters > 0 && options.WaitTimeout == 0) ||
		options.DialTimeout <= 0 || options.UnaryReserve < 0 || options.UnaryReserve > options.MaxOpen {
		return nil, errors.New("lrpc: invalid connection pool limits")
	}
	return newPool(addr, options), nil
}

func newPool(addr string, options PoolOptions) *Pool {
	if options.UnaryReserve == 0 {
		options.UnaryReserve = 1 + (options.MaxOpen-1)/4
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Pool{addr: addr,
		conns: make(chan net.Conn, options.MaxIdle), options: options,
		wake: make(chan struct{}, 1), lifetime: ctx, cancel: cancel}
}

// Hold the streaming budget through connection setup and physical close.
// Stream attempts bypass the ordinary wait queue, including when unary calls
// currently occupy all physical connections.
func (p *Pool) getStreamContext(ctx context.Context) (net.Conn, error) {
	if p.closed.Load() {
		return nil, errPoolClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !reserve(&p.streams, p.options.MaxOpen-p.options.UnaryReserve) {
		return nil, ErrPoolExhausted
	}
	conn, err, ready := p.tryAcquire(ctx)
	if !ready {
		err = ErrPoolExhausted
	} else if conn == nil && err == nil {
		conn, err = p.dialReserved(ctx)
	}
	if err != nil {
		p.streams.Add(-1)
		return nil, err
	}
	conn.(*poolConn).state.Or(connStream)
	return conn, nil
}

func reserve(counter *atomic.Int64, limit int) bool {
	for n := counter.Load(); n < int64(limit); n = counter.Load() {
		if counter.CompareAndSwap(n, n+1) {
			return true
		}
	}
	return false
}

func (p *Pool) signal() {
	// Idle returns do not need notification when nobody is waiting. A waiter
	// registers before rechecking availability, avoiding missed publications.
	if p.waiters.Load() == 0 {
		return
	}
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// A ready result with nil connection and nil error owns a dialing reservation.
func (p *Pool) tryAcquire(ctx context.Context) (net.Conn, error, bool) {
	if p.closed.Load() {
		return nil, errPoolClosed, true
	}
	if err := ctx.Err(); err != nil {
		return nil, err, true
	}
	for {
		select {
		case conn := <-p.conns:
			tracked := conn.(*poolConn)
			if !tracked.state.CompareAndSwap(connIdle, connBorrowed) {
				continue
			}
			if p.closed.Load() {
				tracked.Close()
				return nil, errPoolClosed, true
			}
			return tracked, nil, true
		default:
			if !reserve(&p.open, p.options.MaxOpen) {
				return nil, nil, false
			}
			return nil, nil, true
		}
	}
}

// wait bounds both occupancy and time. Acquisition is not FIFO; callers must
// not rely on ordering. Only the saturated path allocates a waiting timer.
func (p *Pool) wait(ctx context.Context) (net.Conn, error) {
	if !reserve(&p.waiters, p.options.MaxWaiters) {
		return nil, ErrPoolExhausted
	}
	defer p.waiters.Add(-1)
	defer p.signal()
	waitCtx, cancel := context.WithTimeout(ctx, p.options.WaitTimeout)
	defer cancel()
	for {
		if err := waitCtx.Err(); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, ErrPoolWaitTimeout
		}
		// The queue deadline ends when a slot is acquired; connection setup has
		// its own timeout, still bounded by the original request context.
		conn, err, ready := p.tryAcquire(ctx)
		if ready {
			return conn, err
		}
		select {
		case <-p.wake:
		case <-p.lifetime.Done():
			return nil, errPoolClosed
		case <-waitCtx.Done():
		}
	}
}

func (p *Pool) checkReady(ctx context.Context) error {
	if err := p.readinessContextErr(ctx); err != nil {
		return err
	}
	replace := false
	if !reserve(&p.open, p.options.MaxOpen) {
		idle, err, ready := p.tryAcquire(ctx)
		if !ready {
			idle, err = p.wait(ctx)
		}
		if idle != nil {
			err = idle.(*poolConn).closeForReplacement()
			replace = true
		}
		if err != nil {
			return err
		}
	}
	conn, err := p.dialReserved(ctx)
	if err != nil {
		return err
	}
	if replace {
		p.Put(conn)
	} else {
		conn.Close()
	}
	return p.readinessContextErr(ctx)
}

func (p *Pool) readinessContextErr(ctx context.Context) error {
	if p.closed.Load() {
		return errPoolClosed
	}
	return ctx.Err()
}

// The reservation precedes dialing and is held through handshake. Closing the
// pool cancels in-flight setup without interrupting already borrowed calls.
func (p *Pool) dialReserved(ctx context.Context) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, p.options.DialTimeout)
	stop := context.AfterFunc(p.lifetime, cancel)
	defer cancel()
	defer stop()
	conn, err := p.dialContext(dialCtx)
	if err != nil {
		p.open.Add(-1)
		p.signal()
		if p.closed.Load() {
			return nil, errPoolClosed
		}
		return nil, err
	}
	return p.acceptDial(ctx, conn)
}

// acceptDial transfers a completed setup reservation to its caller, unless
// cancellation or shutdown won the race with the final handshake response.
func (p *Pool) acceptDial(ctx context.Context, conn net.Conn) (net.Conn, error) {
	tracked := &poolConn{Conn: conn, pool: p}
	if p.closed.Load() {
		tracked.Close()
		return nil, errPoolClosed
	}
	if err := ctx.Err(); err != nil {
		tracked.Close()
		return nil, err
	}
	return tracked, nil
}
