package h2transport

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"golang.org/x/net/http2"
)

func openingBytes(t *testing.T, settings ...http2.Setting) []byte {
	t.Helper()
	var b bytes.Buffer
	b.WriteString(http2.ClientPreface)
	if err := http2.NewFramer(&b, nil).WriteSettings(settings...); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestClientOpening(t *testing.T) {
	data := openingBytes(t, http2.Setting{ID: http2.SettingInitialWindowSize, Val: 0}, http2.Setting{ID: http2.SettingMaxFrameSize, Val: 32768})
	_, event, err := readClientOpening(bytes.NewReader(data))
	if err != nil || event.kind != settingsCommand || event.settings.window != 0 || event.settings.frame != 32768 {
		t.Fatal(event, err)
	}
	for _, data := range [][]byte{nil, []byte("PRI *"), bytes.Repeat([]byte{'x'}, len(http2.ClientPreface))} {
		if _, _, err := readClientOpening(bytes.NewReader(data)); err == nil {
			t.Fatal("invalid preface accepted")
		}
	}
	var b bytes.Buffer
	b.WriteString(http2.ClientPreface)
	_ = http2.NewFramer(&b, nil).WriteSettingsAck()
	if _, _, err := readClientOpening(&b); !errors.Is(err, http2.ConnectionError(http2.ErrCodeProtocol)) {
		t.Fatal(err)
	}
	b.WriteString(http2.ClientPreface)
	_ = http2.NewFramer(&b, nil).WritePing(false, [8]byte{})
	if _, _, err := readClientOpening(&b); !errors.Is(err, http2.ConnectionError(http2.ErrCodeProtocol)) {
		t.Fatal(err)
	}
	if _, _, err := readClientOpening(bytes.NewBufferString(http2.ClientPreface)); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func TestSettingsCopyAndValidation(t *testing.T) {
	data := openingBytes(t, http2.Setting{ID: http2.SettingInitialWindowSize, Val: 1}, http2.Setting{ID: http2.SettingInitialWindowSize, Val: 2}, http2.Setting{ID: 99, Val: 123})
	_, event, err := readClientOpening(bytes.NewReader(data))
	if err != nil || event.settings.window != 2 {
		t.Fatal("duplicate settings must apply in order", err)
	}
	for _, setting := range []http2.Setting{{ID: http2.SettingInitialWindowSize, Val: 1 << 31}, {ID: http2.SettingMaxFrameSize, Val: 1}, {ID: http2.SettingEnablePush, Val: 2}} {
		if _, _, err := readClientOpening(bytes.NewReader(openingBytes(t, setting))); err == nil {
			t.Fatal("invalid setting accepted")
		}
	}
}

func TestControlFrameCopies(t *testing.T) {
	var b bytes.Buffer
	f := http2.NewFramer(&b, nil)
	ping := [8]byte{1, 2, 3}
	_ = f.WritePing(false, ping)
	_ = f.WriteWindowUpdate(1, 7)
	_ = f.WriteRSTStream(1, http2.ErrCodeCancel)
	_ = f.WriteGoAway(0, http2.ErrCodeNo, []byte("debug"))
	_ = f.WriteSettingsAck()
	_ = f.WriteData(1, false, []byte("x"))
	r := http2.NewFramer(nil, &b)
	var saved []controlCommand
	for range 5 {
		frame, err := r.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		cmd, recognized, err := decodeControl(frame)
		if err != nil || !recognized {
			t.Fatal(err)
		}
		saved = append(saved, cmd)
	}
	frame, _ := r.ReadFrame()
	if _, ok, err := decodeControl(frame); ok || err != nil {
		t.Fatal("DATA mistaken for control")
	}
	if saved[0].ping != ping || saved[1].value != 7 || saved[2].code != http2.ErrCodeCancel || saved[3].kind != goAwayCommand || saved[4].kind != settingsAckCommand {
		t.Fatal(saved)
	}
}
