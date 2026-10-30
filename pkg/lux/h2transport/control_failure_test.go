package h2transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/net/http2"
)

type faultConn struct {
	bytes.Buffer
	writes, deadlines       int
	failWrite, failDeadline int
	closed                  atomic.Bool
}

func TestReadyDataWriteFailureReleasesQueue(t *testing.T) {
	q, released := testQueue(t, 1, 32)
	queueBody(t, q, 1, 100, []byte("payload"))
	conn := &faultConn{failWrite: 1}
	w := newControlWriter(newDataWriter(q, conn, 4096))
	if err := w.flushReadyData(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	if !q.closed || len(q.streams) != 0 || *released != 1 {
		t.Fatal("failed DATA output retained its queue or body")
	}
	q.close()
	if *released != 1 {
		t.Fatal("body released twice")
	}
}

func TestCommandBatchStopsWhenMailboxClosed(t *testing.T) {
	q, _ := testQueue(t, 1, 32)
	var wire bytes.Buffer
	w := newControlWriter(newDataWriter(q, &wire, 4096))
	m, _ := newControlMailbox(1)
	m.close()
	work, err := w.writeCommands(m, controlCommand{kind: pingCommand}, true)
	if work || !errors.Is(err, errQueueClosed) || wire.Len() != 0 {
		t.Fatal(work, err, wire.Len())
	}
}

func (c *faultConn) Write(p []byte) (int, error) {
	c.writes++
	if c.writes == c.failWrite {
		return 0, io.ErrClosedPipe
	}
	return c.Buffer.Write(p)
}
func (c *faultConn) Read([]byte) (int, error)        { return 0, io.EOF }
func (c *faultConn) Close() error                    { c.closed.Store(true); return nil }
func (c *faultConn) LocalAddr() net.Addr             { return nil }
func (c *faultConn) RemoteAddr() net.Addr            { return nil }
func (c *faultConn) SetReadDeadline(time.Time) error { return nil }
func (c *faultConn) SetDeadline(t time.Time) error   { return c.SetWriteDeadline(t) }
func (c *faultConn) SetWriteDeadline(time.Time) error {
	c.deadlines++
	if c.deadlines == c.failDeadline {
		return io.ErrClosedPipe
	}
	return nil
}

func TestControlWriterRunFailures(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		write, deadline, buffer int
		timeout                 time.Duration
	}{
		{"timeout-config", 0, 0, 64, 0}, {"initial-deadline", 0, 1, 64, time.Second},
		{"loop-deadline", 0, 2, 64, time.Second}, {"after-wait-deadline", 0, 2, 64, time.Second},
		{"initial-frame", 1, 0, 16, time.Second}, {"after-wait-flush", 2, 0, 64, time.Second},
		{"initial-flush", 1, 0, 4096, time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				conn := &faultConn{failWrite: tc.write, failDeadline: tc.deadline}
				q, _ := testQueue(t, 1, 32)
				m, _ := newControlMailbox(8)
				w := newControlWriter(newDataWriter(q, conn, tc.buffer))
				if tc.name == "loop-deadline" {
					_ = m.put(controlCommand{kind: pingCommand})
				}
				done := make(chan error, 1)
				go func() { done <- w.run(context.Background(), conn, m, tc.timeout) }()
				synctest.Wait()
				_ = m.put(controlCommand{kind: pingCommand})
				err := <-done
				want := error(io.ErrClosedPipe)
				if tc.timeout == 0 {
					want = errWriterDeadline
				}
				if !errors.Is(err, want) || !q.closed || !conn.closed.Load() {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestControlWriterWaitingClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn := &faultConn{}
		q, _ := testQueue(t, 1, 32)
		m, _ := newControlMailbox(1)
		w := newControlWriter(newDataWriter(q, conn, 4096))
		done := make(chan error, 1)
		go func() { done <- w.run(context.Background(), conn, m, time.Second) }()
		synctest.Wait()
		m.close()
		if err := <-done; !errors.Is(err, errQueueClosed) {
			t.Fatal(err)
		}
	})
}

func TestControlWriterBlockedSocket(t *testing.T) {
	for _, cancelWrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "cancel"}[cancelWrite], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server, client := net.Pipe()
				defer client.Close()
				q, _ := testQueue(t, 1, 32)
				m, _ := newControlMailbox(1)
				w := newControlWriter(newDataWriter(q, server, 4096))
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- w.run(ctx, server, m, time.Second) }()
				if cancelWrite {
					synctest.Wait()
					cancel()
				}
				err := <-done
				if cancelWrite {
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				} else if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
					t.Fatal(err)
				}
				if !q.closed {
					t.Fatal("stalled connection leaked queue")
				}
			})
		})
	}
}

func TestControlWriterProtocolShutdown(t *testing.T) {
	for _, preload := range []bool{false, true} {
		t.Run(map[bool]string{false: "wait", true: "ready"}[preload], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				conn := &faultConn{}
				q, _ := testQueue(t, 1, 32)
				m, _ := newControlMailbox(8)
				w := newControlWriter(newDataWriter(q, conn, 4096))
				cmd := controlCommand{kind: commandKind(99)}
				if preload {
					_ = m.put(cmd)
				}
				done := make(chan error, 1)
				go func() { done <- w.run(context.Background(), conn, m, time.Second) }()
				synctest.Wait()
				if !preload {
					_ = m.put(cmd)
				}
				if err := <-done; !errors.Is(err, http2.ConnectionError(http2.ErrCodeProtocol)) {
					t.Fatal(err)
				}
				reader := http2.NewFramer(nil, &conn.Buffer)
				_, _ = reader.ReadFrame()
				frame, err := reader.ReadFrame()
				if err != nil || frame.(*http2.GoAwayFrame).ErrCode != http2.ErrCodeProtocol {
					t.Fatal(frame, err)
				}
			})
		})
	}
}

func TestControlWriterFinishFailure(t *testing.T) {
	for _, bufferSize := range []int{16, 4096} {
		q, _ := testQueue(t, 1, 32)
		w := newControlWriter(newDataWriter(q, failedWriter{}, bufferSize))
		protocol := http2.ConnectionError(http2.ErrCodeProtocol)
		if err := w.finishError(protocol); !errors.Is(err, protocol) || !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal(err)
		}
		if err := w.finishError(errQueueClosed); !errors.Is(err, errQueueClosed) {
			t.Fatal(err)
		}
	}
}

func TestControlWriterBatchPaths(t *testing.T) {
	q, _ := testQueue(t, 1, 32)
	m, _ := newControlMailbox(16)
	var wire bytes.Buffer
	w := newControlWriter(newDataWriter(q, &wire, 4096))
	for range 9 {
		_ = m.put(controlCommand{kind: pingCommand})
	}
	if work, err := w.writeReady(m); !work || err != nil {
		t.Fatal(work, err)
	}
	if m.count != 1 {
		t.Fatal("control batch unbounded")
	}
	m.close()
	if _, err := w.writeReady(m); !errors.Is(err, errQueueClosed) {
		t.Fatal(err)
	}
	m, _ = newControlMailbox(1)
	_ = m.put(controlCommand{kind: pingCommand})
	w = newControlWriter(newDataWriter(q, failedWriter{}, 4096))
	if _, err := w.writeReady(m); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	q.close()
	if _, err := w.writeReady(m); !errors.Is(err, errQueueClosed) {
		t.Fatal(err)
	}
}

func TestControlWriterWindowAndPing(t *testing.T) {
	q, _ := testQueue(t, 1, 32)
	queueBody(t, q, 1, 10, []byte("a"))
	var wire bytes.Buffer
	w := newControlWriter(newDataWriter(q, &wire, 4096))
	if err := w.apply(controlCommand{kind: pingCommand, ack: true}); err != nil || wire.Len() != 0 {
		t.Fatal(err)
	}
	if err := w.updateWindow(0, 0); !errors.Is(err, http2.ConnectionError(http2.ErrCodeProtocol)) {
		t.Fatal(err)
	}
	if err := w.updateWindow(0, 1); err != nil || w.connection != 65536 {
		t.Fatal(err)
	}
	if err := w.updateWindow(1, 1); err != nil {
		t.Fatal(err)
	}
	if err := w.updateWindow(1, 0); err != nil {
		t.Fatal(err)
	}
	_ = w.data.buffer.Flush()
	frame, err := http2.NewFramer(nil, &wire).ReadFrame()
	if err != nil || frame.(*http2.RSTStreamFrame).ErrCode != http2.ErrCodeProtocol {
		t.Fatal(frame, err)
	}
}
