package h2transport

import (
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"
)

// windowCredit bounds flow-control work by actual outbound DATA progress.
// Two updates cover the connection and stream windows. Idle credit is capped;
// these tokens do not replace protocol validation or authorize other controls.
type windowCredit struct{ available atomic.Int64 }

func (c *windowCredit) sentData() {
	for {
		old := c.available.Load()
		if old >= 1024 || c.available.CompareAndSwap(old, min(old+2, 1024)) {
			return
		}
	}
}

func (c *windowCredit) take() bool {
	for {
		old := c.available.Load()
		if old == 0 {
			return false
		}
		if c.available.CompareAndSwap(old, old-1) {
			return true
		}
	}
}

func (c *candidateConnection) allowFrame(frame http2.Frame) bool {
	if payloadFrame(frame) {
		return true
	}
	if _, ok := frame.(*http2.WindowUpdateFrame); ok && c.windowCredit.take() {
		return true
	}
	return c.rate.allow(time.Now())
}

// controlRate bounds abusive control/empty/invalid frames, not payload DATA.
// It is reader-owned. Window boundaries permit at most two adjacent bursts.
type controlRate struct {
	burst, used int
	interval    time.Duration
	start       time.Time
}

func (r *controlRate) allow(now time.Time) bool {
	if r.start.IsZero() || now.Sub(r.start) >= r.interval {
		r.start, r.used = now, 0
	}
	if r.used >= r.burst {
		return false
	}
	r.used++
	return true
}

func (c *candidateConnection) refreshDeadline() {
	c.deadline = time.Time{}
	for _, task := range c.tasks {
		if !task.deadline.IsZero() && (c.deadline.IsZero() || task.deadline.Before(c.deadline)) {
			c.deadline = task.deadline
		}
	}
}

// Called under c.mu for a registered task. Only changing the current minimum
// can invalidate the cached deadline; later tasks do not require a map scan.
func (c *candidateConnection) replaceTaskDeadline(task *requestTask, next time.Time) {
	previous := task.deadline
	task.deadline = next
	if len(c.tasks) == 1 {
		c.deadline = next
		return
	}
	if !next.IsZero() && (c.deadline.IsZero() || next.Before(c.deadline)) {
		c.deadline = next
		return
	}
	if !previous.IsZero() && previous.Equal(c.deadline) {
		c.refreshDeadline()
	}
}

func (c *candidateConnection) readDeadline(now time.Time) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lockedReadDeadline(now)
}

func (c *candidateConnection) setReadDeadline(now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.applyReadDeadlineLocked(c.lockedReadDeadline(now))
}

// The reader and response completion share the same mutex and deadline cache.
// Only identical deadlines are skipped; errors never update the cached state.
func (c *candidateConnection) applyReadDeadlineLocked(deadline time.Time) error {
	if c.readDeadlineSet && c.appliedReadDeadline.Equal(deadline) {
		return nil
	}
	if err := c.conn.SetReadDeadline(deadline); err != nil {
		return err
	}
	c.appliedReadDeadline, c.readDeadlineSet = deadline, true
	return nil
}

func (c *candidateConnection) lockedReadDeadline(now time.Time) time.Time {
	expires := now.Add(c.limits.IdleTimeout)
	if !c.deadline.IsZero() && c.deadline.Before(expires) {
		expires = c.deadline
	}
	if !c.ackDeadline.IsZero() && c.ackDeadline.Before(expires) {
		expires = c.ackDeadline
	}
	return expires
}
