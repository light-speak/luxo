package api

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"nhooyr.io/websocket"
)

func TestWSCancelledWriteAfterReservationReleasesBudget(t *testing.T) {
	var total atomic.Int64
	ws := &wsConn{ctx: t.Context(), totalWriteBytes: &total, limits: WebSocketLimits{WriteTimeout: time.Minute}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ws.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- ws.writeBinary(ctx, []byte("payload")) }()
	deadline := time.Now().Add(time.Second)
	for ws.writeBytes.Load() == 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	reserved := ws.writeBytes.Load()
	cancel()
	ws.mu.Unlock()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled writer remained blocked")
	}
	if reserved != 7 || ws.writeBytes.Load() != 0 || total.Load() != 0 {
		t.Fatal("write did not reserve and release its capacity", reserved)
	}
	// A nil socket makes any accidental write or connection close fail the test.
}

func TestWSExpiredQueuedWriteReleasesBudget(t *testing.T) {
	accepted := make(chan *wsConn, 1)
	release := make(chan struct{})
	var total atomic.Int64
	ctx, peer := newInMemoryWebSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		accepted <- &wsConn{conn: conn, ctx: r.Context(), totalWriteBytes: &total, limits: WebSocketLimits{WriteTimeout: 10 * time.Millisecond}}
		<-release
	}))
	defer close(release)
	ws := <-accepted
	ws.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- ws.writeBinary(ctx, []byte("payload")) }()
	// Wait for the queued writer's timer to close the peer before releasing the lock.
	_, _, readErr := peer.Read(ctx)
	ws.mu.Unlock()
	if readErr == nil || ctx.Err() != nil {
		t.Fatal("writer deadline did not close the connection", readErr, ctx.Err())
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("expired writer remained blocked")
	}
	if ws.writeBytes.Load() != 0 || total.Load() != 0 {
		t.Fatal("expired queued write retained buffer capacity")
	}
}

func TestWSWriteDeadlineStaleCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var d wsWriteDeadline
		defer d.close()
		ctx := d.arm(t.Context(), time.Now().Add(time.Second))
		d.disarm()
		d.expire()
		if ctx.Err() != nil {
			t.Fatal("disarmed callback cancelled writer")
		}
		if d.arm(t.Context(), time.Now().Add(2*time.Second)) != ctx {
			t.Fatal("context not reused")
		}
		d.expire()
		time.Sleep(time.Second)
		if ctx.Err() != nil {
			t.Fatal("stale callback cancelled newer write")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if ctx.Err() != context.Canceled {
			t.Fatal("deadline not enforced")
		}
		d.disarm()
		d.expire()
		if d.arm(t.Context(), time.Now().Add(time.Second)).Err() == nil {
			t.Fatal("expired connection revived")
		}
	})
}

func TestWSWriteDeadlineCloseBeforeArm(t *testing.T) {
	var d wsWriteDeadline
	d.close()
	if d.arm(t.Context(), time.Now().Add(time.Second)).Err() == nil {
		t.Fatal("closed writer reopened")
	}
	d.disarm()
	d.close()
}

func TestWSCancelledQueuedWriteDoesNotCloseSocket(t *testing.T) {
	// Use real time: a deliberately held mutex cannot advance synctest's clock.
	ws := &wsConn{ctx: t.Context()}
	ws.mu.Lock()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(started)
		ws.lockWrite(ctx, time.Millisecond)
		ws.mu.Unlock()
		close(done)
	}()
	<-started
	// conn is deliberately nil: touching it would panic and fail this test.
	time.Sleep(10 * time.Millisecond)
	ws.mu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter stuck")
	}
}
