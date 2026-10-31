package h2transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

func TestWriterAbortIsolatesStreamAndReleasesBody(t *testing.T) {
	q, released := testQueue(t, 2, 32)
	queueBody(t, q, 1, 100, []byte("discard"))
	queueBody(t, q, 3, 100, []byte("keep"))
	var wire bytes.Buffer
	w := newControlWriter(newDataWriter(q, &wire, 4096))
	for _, id := range []uint32{1, 5} {
		if err := w.apply(controlCommand{kind: abortStreamCommand, streamID: id, code: http2.ErrCodeProtocol}); err != nil {
			t.Fatal(err)
		}
	}
	if *released != 1 || len(q.streams) != 1 || q.streams[3] == 0 || w.lastStream != 5 {
		t.Fatal("abort changed another stream or retained its body")
	}
	if err := w.flushReadyData(); err != nil {
		t.Fatal(err)
	}
	reader := http2.NewFramer(nil, &wire)
	for _, id := range []uint32{1, 5} {
		frame, err := reader.ReadFrame()
		reset, ok := frame.(*http2.RSTStreamFrame)
		if err != nil || !ok || reset.StreamID != id || reset.ErrCode != http2.ErrCodeProtocol {
			t.Fatal(frame, err)
		}
	}
	frame, err := reader.ReadFrame()
	data, ok := frame.(*http2.DataFrame)
	if err != nil || !ok || data.StreamID != 3 || string(data.Data()) != "keep" || !data.StreamEnded() {
		t.Fatal(frame, err)
	}
	q.close()
	if *released != 2 || wire.Len() != 0 {
		t.Fatal("unexpected remaining output or duplicate body release")
	}
}

func TestWriterCommandFailureStopsBatch(t *testing.T) {
	q, _ := testQueue(t, 1, 32)
	var wire bytes.Buffer
	w := newControlWriter(newDataWriter(q, &wire, 4096))
	m, _ := newControlMailbox(2)
	if err := m.put(controlCommand{kind: pingCommand}); err != nil {
		t.Fatal(err)
	}
	work, err := w.writeCommands(m, controlCommand{kind: commandKind(99)}, true)
	if work || !errors.Is(err, http2.ConnectionError(http2.ErrCodeProtocol)) {
		t.Fatal(work, err)
	}
	command, ok, err := m.poll()
	if err != nil || !ok || command.kind != pingCommand || wire.Len() != 0 {
		t.Fatal("writer continued after failed command", command, err)
	}
}

func TestWriterSettingsCommit(t *testing.T) {
	q, _ := testQueue(t, 2, 32)
	queueBody(t, q, 1, 10, []byte("a"))
	queueBody(t, q, 3, maxWindow, []byte("b"))
	var wire bytes.Buffer
	w := newControlWriter(newDataWriter(q, &wire, 4096))
	_, event, err := readClientOpening(bytes.NewReader(openingBytes(t, http2.Setting{ID: http2.SettingInitialWindowSize, Val: 65536}, http2.Setting{ID: http2.SettingInitialWindowSize, Val: 0})))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.apply(event); !errors.Is(err, http2.ConnectionError(http2.ErrCodeFlowControl)) {
		t.Fatal(err)
	}
	if w.initialWindow != 65535 || q.slots[q.streams[1]].window != 10 || wire.Len() != 0 {
		t.Fatal("partial settings commit")
	}
	_, event, err = readClientOpening(bytes.NewReader(openingBytes(t, http2.Setting{ID: http2.SettingInitialWindowSize, Val: 0}, http2.Setting{ID: http2.SettingHeaderTableSize, Val: 0}, http2.Setting{ID: http2.SettingHeaderTableSize, Val: 128}, http2.Setting{ID: http2.SettingMaxFrameSize, Val: 32768}, http2.Setting{ID: http2.SettingMaxHeaderListSize, Val: 1024}, http2.Setting{ID: http2.SettingMaxConcurrentStreams, Val: 5}, http2.Setting{ID: http2.SettingEnablePush, Val: 0})))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.apply(event); err != nil {
		t.Fatal(err)
	}
	if w.initialWindow != 0 || w.frameSize != 32768 || w.headerLimit != 1024 || w.encoder.MaxDynamicTableSize() != 128 || q.slots[q.streams[1]].window != -65525 {
		t.Fatal("settings not applied")
	}
	if err := w.data.buffer.Flush(); err != nil {
		t.Fatal(err)
	}
	f, err := http2.NewFramer(nil, &wire).ReadFrame()
	if err != nil || !f.(*http2.SettingsFrame).IsAck() {
		t.Fatal(f, err)
	}
}

func TestWriterControlErrors(t *testing.T) {
	q, _ := testQueue(t, 2, 32)
	queueBody(t, q, 1, 10, []byte("a"))
	queueBody(t, q, 3, maxWindow, []byte("b"))
	var wire bytes.Buffer
	w := newControlWriter(newDataWriter(q, &wire, 4096))
	w.lastStream = 3
	if err := w.apply(controlCommand{kind: settingsAckCommand}); err != nil {
		t.Fatal(err)
	}
	for _, event := range []controlCommand{{kind: settingsAckCommand}, {kind: windowCommand, streamID: 5, value: 1}, {kind: resetCommand, streamID: 2}, {kind: commandKind(99)}} {
		if err := w.apply(event); !errors.Is(err, http2.ConnectionError(http2.ErrCodeProtocol)) {
			t.Fatal(event, err)
		}
	}
	w.connection = sendWindow(maxWindow)
	if err := w.apply(controlCommand{kind: windowCommand, value: 1}); !errors.Is(err, http2.ConnectionError(http2.ErrCodeFlowControl)) {
		t.Fatal(err)
	}
	if err := w.apply(controlCommand{kind: windowCommand, streamID: 3, value: 1}); err != nil {
		t.Fatal(err)
	}
	if _, exists := q.streams[3]; exists {
		t.Fatal("overflow stream not reset")
	}
	_ = w.data.buffer.Flush()
	f, err := http2.NewFramer(nil, &wire).ReadFrame()
	if err != nil || f.(*http2.RSTStreamFrame).ErrCode != http2.ErrCodeFlowControl {
		t.Fatal(f, err)
	}
	if err := w.apply(controlCommand{kind: resetCommand, streamID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := w.apply(controlCommand{kind: resetCommand, streamID: 1}); err != nil {
		t.Fatal("closed stream reset must be ignored", err)
	}
	if err := w.apply(controlCommand{kind: windowCommand, streamID: 1, value: 1}); err != nil {
		t.Fatal(err)
	}
	if err := w.apply(controlCommand{kind: goAwayCommand}); err != nil || !w.draining {
		t.Fatal(err)
	}
}

func TestControlWriterHandshakeAndPing(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	q, _ := testQueue(t, 2, 32)
	m, _ := newControlMailbox(8)
	w := newControlWriter(newDataWriter(q, server, 4096))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.run(ctx, server, m, time.Second) }()
	r := http2.NewFramer(nil, client)
	f, err := r.ReadFrame()
	if err != nil || f.Header().Type != http2.FrameSettings {
		t.Fatal(f, err)
	}
	readerDone := make(chan error, 1)
	go func() {
		_, cmd, err := readClientOpening(server)
		if err == nil {
			err = m.put(cmd)
		}
		readerDone <- err
	}()
	if _, err := client.Write(openingBytes(t)); err != nil {
		t.Fatal(err)
	}
	if err := <-readerDone; err != nil {
		t.Fatal(err)
	}
	f, err = r.ReadFrame()
	if err != nil || !f.(*http2.SettingsFrame).IsAck() {
		t.Fatal(f, err)
	}
	ping := [8]byte{8, 7, 6}
	if err := m.put(controlCommand{kind: pingCommand, ping: ping}); err != nil {
		t.Fatal(err)
	}
	f, err = r.ReadFrame()
	if err != nil || !f.(*http2.PingFrame).IsAck() || f.(*http2.PingFrame).Data != ping {
		t.Fatal(f, err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestControlWriterWriteFailure(t *testing.T) {
	server, client := net.Pipe()
	_ = client.Close()
	q, _ := testQueue(t, 1, 1)
	m, _ := newControlMailbox(1)
	w := newControlWriter(newDataWriter(q, server, 32))
	if err := w.run(context.Background(), server, m, time.Second); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	if !q.closed {
		t.Fatal("queue not closed")
	}
}
