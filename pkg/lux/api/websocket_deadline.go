package api

import (
	"context"
	"sync"
	"time"
)

// wsWriteDeadline reuses one context and timer for a connection's serialized
// writes. The mutex protects against a timer callback left over from a reset.
type wsWriteDeadline struct {
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	timer    *time.Timer
	deadline time.Time
	closed   bool
}

// context initializes the reusable cancellation without starting a deadline.
func (d *wsWriteDeadline) context(parent context.Context) context.Context {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ctx == nil {
		d.ctx, d.cancel = context.WithCancel(parent)
	}
	return d.ctx
}

func (d *wsWriteDeadline) arm(parent context.Context, deadline time.Time) context.Context {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ctx == nil {
		d.ctx, d.cancel = context.WithCancel(parent)
	}
	if d.closed {
		d.cancel()
		return d.ctx
	}
	d.deadline = deadline
	remaining := time.Until(deadline)
	if d.timer == nil {
		d.timer = time.AfterFunc(remaining, d.expire)
	} else {
		d.timer.Reset(remaining)
	}
	return d.ctx
}

func (d *wsWriteDeadline) expire() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.deadline.IsZero() {
		return
	}
	if remaining := time.Until(d.deadline); remaining > 0 {
		d.timer.Reset(remaining)
		return
	}
	d.closed = true
	d.cancel()
}

func (d *wsWriteDeadline) disarm() {
	d.mu.Lock()
	d.deadline = time.Time{}
	if d.timer != nil {
		d.timer.Stop()
	}
	d.mu.Unlock()
}

func (d *wsWriteDeadline) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	if d.cancel != nil {
		d.cancel()
	}
	if d.timer != nil {
		d.timer.Stop()
	}
}

// The uncontended path allocates nothing. Only waiting writers need their own
// timer: their deadline must also hold while another frame owns the socket.
func (c *wsConn) lockWrite(ctx context.Context, timeout time.Duration) time.Time {
	deadline := time.Now().Add(timeout)
	if c.mu.TryLock() {
		return deadline
	}
	timer := time.AfterFunc(timeout, func() {
		if ctx.Err() == nil {
			c.conn.CloseNow()
		}
	})
	c.mu.Lock()
	timer.Stop()
	return deadline
}
