package h2transport

import (
	"bytes"
	"io"

	"golang.org/x/net/http2"
)

type commandKind uint8

const (
	settingsCommand commandKind = iota + 1
	settingsAckCommand
	pingCommand
	windowCommand
	resetCommand
	goAwayCommand
	receiveWindowCommand
	openStreamCommand
	responseCommand
	abortStreamCommand
	drainCommand
)

const (
	tableSetting uint8 = 1 << iota
	pushSetting
	concurrentSetting
	windowSetting
	frameSetting
	headersSetting
)

// Fixed-size messages never reference the read framer's reused input memory.
// A response command transfers an owned bounded unary response to the writer.
type controlCommand struct {
	kind            commandKind
	streamID, value uint32
	code            http2.ErrCode
	ping            [8]byte
	ack             bool
	settings        settingsUpdate
	credit          receiveCredit
	response        *response
}

type settingsUpdate struct {
	present               uint8
	table, tableMinimum   uint32
	push, concurrent      uint32
	window, windowMaximum uint32
	frame, headers        uint32
}

// readClientOpening handles prior-knowledge h2c or an already-negotiated TLS h2
// connection. The caller owns handshake deadlines and concurrent server SETTINGS.
// This is not an HTTP/1 Upgrade or ALPN implementation.
func readClientOpening(source io.Reader) (*http2.Framer, controlCommand, error) {
	var preface [24]byte
	if _, err := io.ReadFull(source, preface[:]); err != nil {
		return nil, controlCommand{}, err
	}
	if !bytes.Equal(preface[:], []byte(http2.ClientPreface)) {
		return nil, controlCommand{}, http2.ConnectionError(http2.ErrCodeProtocol)
	}
	reader := newRequestFramer(source)
	frame, err := reader.ReadFrame()
	if err != nil {
		return nil, controlCommand{}, err
	}
	settings, ok := frame.(*http2.SettingsFrame)
	if !ok || settings.IsAck() {
		return nil, controlCommand{}, http2.ConnectionError(http2.ErrCodeProtocol)
	}
	cmd, _, err := decodeControl(settings)
	return reader, cmd, err
}

func decodeControl(frame http2.Frame) (controlCommand, bool, error) {
	var cmd controlCommand
	switch f := frame.(type) {
	case *http2.SettingsFrame:
		cmd.kind = settingsCommand
		if f.IsAck() {
			cmd.kind = settingsAckCommand
			break
		}
		if err := f.ForeachSetting(cmd.settings.record); err != nil {
			return controlCommand{}, true, err
		}
	case *http2.PingFrame:
		cmd.kind, cmd.ping, cmd.ack = pingCommand, f.Data, f.IsAck()
	case *http2.WindowUpdateFrame:
		cmd.kind, cmd.streamID, cmd.value = windowCommand, f.StreamID, f.Increment
	case *http2.RSTStreamFrame:
		cmd.kind, cmd.streamID, cmd.code = resetCommand, f.StreamID, f.ErrCode
	case *http2.GoAwayFrame:
		cmd.kind, cmd.streamID, cmd.code = goAwayCommand, f.LastStreamID, f.ErrCode
	default:
		return cmd, false, nil
	}
	return cmd, true, nil
}

func (s *settingsUpdate) record(setting http2.Setting) error {
	if err := setting.Valid(); err != nil {
		return err
	}
	switch setting.ID {
	case http2.SettingHeaderTableSize:
		if s.present&tableSetting == 0 {
			s.tableMinimum = setting.Val
		} else {
			s.tableMinimum = min(s.tableMinimum, setting.Val)
		}
		s.table, s.present = setting.Val, s.present|tableSetting
	case http2.SettingEnablePush:
		s.push, s.present = setting.Val, s.present|pushSetting
	case http2.SettingMaxConcurrentStreams:
		s.concurrent, s.present = setting.Val, s.present|concurrentSetting
	case http2.SettingInitialWindowSize:
		s.windowMaximum = max(s.windowMaximum, setting.Val)
		s.window, s.present = setting.Val, s.present|windowSetting
	case http2.SettingMaxFrameSize:
		s.frame, s.present = setting.Val, s.present|frameSetting
	case http2.SettingMaxHeaderListSize:
		s.headers, s.present = setting.Val, s.present|headersSetting
	}
	return nil
}
