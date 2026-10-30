package rpc

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"time"

	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

var (
	// ErrPoolExhausted rejects work beyond the bounded waiting budget.
	ErrPoolExhausted error = luxerrors.New("LRPCPoolBusy", 503, "error.lrpc_pool_busy")
	// ErrPoolWaitTimeout reports expiration before a connection slot is acquired.
	ErrPoolWaitTimeout error = luxerrors.New("LRPCPoolWaitTimeout", 503, "error.lrpc_pool_wait_timeout")
)

// PoolOptions applies per destination and client, not globally across replicas.
// MaxOpen includes idle, borrowed, dialing, and handshaking connections.
// Streams occupy a borrowed connection until termination. Zero MaxWaiters
// rejects saturation immediately; zero MaxIdle disables idle retention.
type PoolOptions struct {
	MaxIdle     int
	MaxOpen     int
	MaxWaiters  int
	WaitTimeout time.Duration
	DialTimeout time.Duration
}

func DefaultPoolOptions() PoolOptions {
	return PoolOptions{MaxIdle: 32, MaxOpen: 32, MaxWaiters: 32, WaitTimeout: 5 * time.Second, DialTimeout: 5 * time.Second}
}

func NewPoolWithOptions(addr string, options PoolOptions) (*Pool, error) {
	if options.MaxOpen <= 0 || options.MaxIdle < 0 || options.MaxIdle > options.MaxOpen || options.MaxWaiters < 0 || options.WaitTimeout < 0 || (options.MaxWaiters > 0 && options.WaitTimeout == 0) || options.DialTimeout <= 0 {
		return nil, errors.New("lrpc: invalid connection pool limits")
	}
	return newPool(addr, options), nil
}

func newPool(addr string, options PoolOptions) *Pool {
	ctx, cancel := context.WithCancel(context.Background())
	return &Pool{addr: addr, maxIdle: options.MaxIdle, timeout: options.DialTimeout,
		conns: make(chan net.Conn, options.MaxIdle), options: options,
		wake: make(chan struct{}, 1), lifetime: ctx, cancel: cancel}
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

// The reservation precedes dialing and is held through handshake. Closing the
// pool cancels in-flight setup without interrupting already borrowed calls.
func (p *Pool) dialReserved(ctx context.Context) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, p.timeout)
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
