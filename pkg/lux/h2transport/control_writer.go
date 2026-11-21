package h2transport

import (
	"bytes"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// controlWriter owns peer settings, HPACK encoder, flow credit and DATA queue.
// The connection reader must classify HEADERS and register streams before DATA
// submission. This component alone is not an HTTP server.
type controlWriter struct {
	data                             *dataWriter
	connection                       sendWindow
	initialWindow                    uint32
	receiveConnection, receiveStream uint32
	frameSize                        int
	headerLimit                      uint32
	lastStream                       uint32
	pendingSettings                  bool
	draining                         bool
	headerBlock                      bytes.Buffer
	encoder                          *hpack.Encoder
}

func newControlWriter(data *dataWriter) *controlWriter {
	w := &controlWriter{data: data, connection: 65535, initialWindow: 65535, receiveConnection: 65535, receiveStream: 65535, frameSize: 16384, headerLimit: ^uint32(0), pendingSettings: true}
	w.encoder = hpack.NewEncoder(&w.headerBlock)
	for id := range data.queue.streams {
		w.lastStream = max(w.lastStream, id)
	}
	return w
}

func (w *controlWriter) apply(command controlCommand) error {
	switch command.kind {
	case settingsCommand:
		return w.applySettings(command.settings)
	case settingsAckCommand:
		if !w.pendingSettings {
			return http2.ConnectionError(http2.ErrCodeProtocol)
		}
		w.pendingSettings = false
	case pingCommand:
		if !command.ack {
			return w.data.framer.WritePing(true, command.ping)
		}
	case windowCommand:
		return w.updateWindow(command.streamID, command.value)
	case resetCommand:
		return w.resetStream(command.streamID)
	case goAwayCommand:
		w.draining = true
	case drainCommand:
		w.draining = true
		return w.data.framer.WriteGoAway(command.streamID, http2.ErrCodeNo, nil)
	case receiveWindowCommand:
		return w.returnReceiveCredit(command.streamID, command.credit)
	case openStreamCommand:
		w.lastStream = max(w.lastStream, command.streamID)
		return w.data.queue.open(command.streamID, sendWindow(w.initialWindow))
	case responseCommand:
		return w.writeResponse(command.streamID, command.response)
	case abortStreamCommand:
		if _, exists := w.data.queue.streams[command.streamID]; exists {
			_ = w.data.queue.cancel(command.streamID)
		}
		w.lastStream = max(w.lastStream, command.streamID)
		return w.data.framer.WriteRSTStream(command.streamID, command.code)
	default:
		return http2.ConnectionError(http2.ErrCodeProtocol)
	}
	return nil
}

func (w *controlWriter) returnReceiveCredit(id uint32, credit receiveCredit) error {
	if int64(credit.connection) > maxWindow || int64(credit.stream) > maxWindow || credit.stream != 0 && (id == 0 || id&1 == 0 || id > uint32(maxWindow)) {
		return errInvalidWindow
	}
	if credit.connection != 0 {
		if err := w.data.framer.WriteWindowUpdate(0, credit.connection); err != nil {
			return err
		}
	}
	if credit.stream != 0 {
		return w.data.framer.WriteWindowUpdate(id, credit.stream)
	}
	return nil
}

func (w *controlWriter) applySettings(update settingsUpdate) error {
	q := w.data.queue
	if update.present&windowSetting != 0 {
		// Intermediate repeated settings can overflow even if the final value
		// does not. Validate the largest advertised window before committing.
		if err := q.validateInitial(w.initialWindow, update.windowMaximum); err != nil {
			return http2.ConnectionError(http2.ErrCodeFlowControl)
		}
		// record guarantees the final window is no greater than the maximum.
		q.commitInitial(w.initialWindow, update.window)
		w.initialWindow = update.window
	}
	if update.present&tableSetting != 0 {
		// Keep our own encoder bounded at 4KiB even if the peer allows more.
		w.encoder.SetMaxDynamicTableSizeLimit(min(4096, update.tableMinimum))
		w.encoder.SetMaxDynamicTableSizeLimit(min(4096, update.table))
		w.encoder.SetMaxDynamicTableSize(min(4096, update.table))
	}
	if update.present&frameSetting != 0 {
		w.frameSize = int(update.frame)
	}
	if update.present&headersSetting != 0 {
		w.headerLimit = update.headers
	}
	// No server push is implemented; the peer's push/concurrency limits do not
	// change admission for client-initiated streams.
	return w.data.framer.WriteSettingsAck()
}

func (w *controlWriter) knownStream(id uint32) bool {
	return id != 0 && id&1 == 1 && id <= w.lastStream
}

func (w *controlWriter) resetStream(id uint32) error {
	if !w.knownStream(id) {
		return http2.ConnectionError(http2.ErrCodeProtocol)
	}
	if _, exists := w.data.queue.streams[id]; exists {
		return w.data.queue.cancel(id)
	}
	return nil
}

func (w *controlWriter) updateWindow(id, increment uint32) error {
	if id == 0 {
		if increment == 0 {
			return http2.ConnectionError(http2.ErrCodeProtocol)
		}
		if err := w.connection.increase(increment); err != nil {
			return http2.ConnectionError(http2.ErrCodeFlowControl)
		}
		return nil
	}
	if !w.knownStream(id) {
		return http2.ConnectionError(http2.ErrCodeProtocol)
	}
	if _, exists := w.data.queue.streams[id]; !exists {
		return nil
	}
	if err := w.data.queue.increase(id, increment); err != nil {
		code := http2.ErrCodeFlowControl
		if increment == 0 {
			code = http2.ErrCodeProtocol
		}
		w.data.queue.cancelKnown(w.data.queue.streams[id])
		return w.data.framer.WriteRSTStream(id, code)
	}
	return nil
}
