// Package h2proto contains isolated experiments, not a production HTTP/2 server.
// Its state machines are independently implemented against RFC 9113.
package h2transport

import "errors"

const maxWindow int64 = 1<<31 - 1

var (
	errInvalidWindow = errors.New("invalid HTTP/2 send window update")
	errWindowBlocked = errors.New("HTTP/2 send window exhausted")
)

// sendWindow belongs to one connection writer. It is not shared with handlers.
// A stream window may become negative after SETTINGS_INITIAL_WINDOW_SIZE shrinks.
// See RFC 9113 sections 6.9.1 and 6.9.2. This is wire credit, not a memory budget.
type sendWindow int64

func (w *sendWindow) increase(increment uint32) error {
	if increment == 0 || int64(increment) > maxWindow || int64(*w)+int64(increment) > maxWindow {
		return errInvalidWindow
	}
	*w += sendWindow(increment)
	return nil
}

// changeInitial applies a previously validated setting to a stream, never to
// the connection window. The caller must validate all streams before committing
// a connection-wide SETTINGS change; this primitive only commits one window.
func (w *sendWindow) changeInitial(previous, next uint32) error {
	if int64(previous) > maxWindow || int64(next) > maxWindow {
		return errInvalidWindow
	}
	updated := int64(*w) + int64(next) - int64(previous)
	if updated > maxWindow {
		return errInvalidWindow
	}
	*w = sendWindow(updated)
	return nil
}

// reserveData debits both windows or neither. Size includes DATA padding, but
// excludes the nine-byte frame header. An empty DATA frame needs no credit.
// The connection owner must separately enforce frame size and stream state.
func reserveData(connection, stream *sendWindow, size int64) error {
	if size < 0 || size > maxWindow {
		return errInvalidWindow
	}
	if size == 0 {
		return nil
	}
	if int64(*connection) < size || int64(*stream) < size {
		return errWindowBlocked
	}
	*connection -= sendWindow(size)
	*stream -= sendWindow(size)
	return nil
}
