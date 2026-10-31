package h2transport

import (
	"context"
	"errors"
	"net"
	"time"

	"golang.org/x/net/http2"
)

var errWriterDeadline = errors.New("HTTP/2 writer requires a positive write deadline")

// run is one long-lived writer, not one goroutine per frame. A connection owner
// must also run the reader and handshake deadline. Context cancellation closes
// the socket to unblock a stalled write. The experimental batch sizes below are
// not production configuration defaults or performance-approved limits.
func (w *controlWriter) run(ctx context.Context, conn net.Conn, inbox *controlMailbox, timeout time.Duration) (err error) {
	defer conn.Close()
	defer inbox.close()
	defer w.data.queue.close()
	if timeout <= 0 {
		return errWriterDeadline
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer func() {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
	}()
	if err = conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	if err = w.writeInitialSettings(); err != nil {
		return err
	}
	if err = w.data.buffer.Flush(); err != nil {
		return err
	}
	for {
		if w.draining && len(w.data.queue.streams) == 0 {
			return w.data.buffer.Flush()
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		var work bool
		work, err = w.writeReadyDeadline(inbox, conn, timeout)
		if err != nil {
			return w.finishError(err)
		}
		if work {
			continue
		}
		var command controlCommand
		command, err = inbox.wait(ctx)
		if err != nil {
			return err
		}
		if err = conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
			return err
		}
		if _, err = w.writeCommands(inbox, command, true); err != nil {
			return w.finishError(err)
		}
	}
}

// Flush already-ready DATA with the command's headers, without waiting to fill
// a batch. Even when flow control blocks DATA, control frames must be flushed.
func (w *controlWriter) flushReadyData() error {
	if _, err := w.data.writeBatch(&w.connection, 8, 65536, w.frameSize); err != nil {
		return err
	}
	return w.data.buffer.Flush()
}

func (w *controlWriter) writeReady(inbox *controlMailbox) (bool, error) {
	return w.writeReadyDeadline(inbox, nil, 0)
}

func (w *controlWriter) writeReadyDeadline(inbox *controlMailbox, conn net.Conn, timeout time.Duration) (bool, error) {
	command, ok, err := inbox.poll()
	if err != nil {
		return false, err
	}
	if !ok && w.data.queue.ready == 0 && !w.data.queue.closed && w.data.queue.busy == 0 && w.data.queue.sequence != ^uint64(0) {
		return false, nil
	}
	if conn != nil {
		if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
			return false, err
		}
	}
	return w.writeCommands(inbox, command, ok)
}

// Drain only already-ready commands, sharing the same bounded batch after an
// idle wake and during continuous traffic. Never wait to fill a batch.
func (w *controlWriter) writeCommands(inbox *controlMailbox, command controlCommand, ok bool) (bool, error) {
	controls := 0
	var err error
	for ok {
		if err = w.apply(command); err != nil {
			return false, err
		}
		controls++
		if controls == 8 {
			break
		}
		command, ok, err = inbox.poll()
		if err != nil {
			return false, err
		}
	}
	result, err := w.data.writeBatch(&w.connection, 8, 65536, w.frameSize)
	if err != nil {
		return false, err
	}
	if controls > 0 {
		if err = w.data.buffer.Flush(); err != nil {
			return false, err
		}
	}
	return controls > 0 || result.frames > 0, nil
}

func (w *controlWriter) finishError(err error) error {
	if code, ok := err.(http2.ConnectionError); ok {
		if writeErr := w.data.framer.WriteGoAway(w.lastStream, http2.ErrCode(code), nil); writeErr != nil {
			return errors.Join(err, writeErr)
		}
		if writeErr := w.data.buffer.Flush(); writeErr != nil {
			return errors.Join(err, writeErr)
		}
	}
	return err
}

// Credit is independent of storage: received bytes still require shared budget.
func (w *controlWriter) writeInitialSettings() error {
	settings := [3]http2.Setting{
		{ID: http2.SettingMaxConcurrentStreams, Val: uint32(len(w.data.queue.slots) - 1)},
		{ID: http2.SettingMaxHeaderListSize, Val: 16384},
	}
	count := 2
	if w.receiveStream != 65535 {
		settings[2] = http2.Setting{ID: http2.SettingInitialWindowSize, Val: w.receiveStream}
		count = 3
	}
	if err := w.data.framer.WriteSettings(settings[:count]...); err != nil {
		return err
	}
	if w.receiveConnection > 65535 {
		return w.data.framer.WriteWindowUpdate(0, w.receiveConnection-65535)
	}
	return nil
}
