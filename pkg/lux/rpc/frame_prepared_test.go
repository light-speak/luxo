package rpc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/api"
)

type preparedWriter struct {
	writes int
	data   []byte
	err    error
	short  bool
}

func (w *preparedWriter) Write(p []byte) (int, error) {
	w.writes++
	w.data = append(w.data, p...)
	if w.short {
		return len(p) - 1, w.err
	}
	return len(p), w.err
}

func TestPreparedFrameSingleWrite(t *testing.T) {
	for _, size := range []int{0, 1, 128, 4096, maxFrameSize} {
		body := bytes.Repeat([]byte{17}, size)
		frame := append(make([]byte, 4), body...)
		var want bytes.Buffer
		if err := WriteFrame(&want, body); err != nil {
			t.Fatal(err)
		}
		var writer preparedWriter
		if err := writePreparedFrame(&writer, frame); err != nil {
			t.Fatal(err)
		}
		if writer.writes != 1 || !bytes.Equal(writer.data, want.Bytes()) {
			t.Fatal("frame changed or split")
		}
	}
}

func TestPreparedFrameFailures(t *testing.T) {
	for _, size := range []int{0, 3, maxFrameSize + 5} {
		var writer preparedWriter
		if err := writePreparedFrame(&writer, make([]byte, size)); err == nil || writer.writes != 0 {
			t.Fatal("invalid frame written")
		}
	}
	w := &preparedWriter{short: true}
	if err := writePreparedFrame(w, make([]byte, 5)); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	w = &preparedWriter{err: io.ErrClosedPipe}
	if err := writePreparedFrame(w, make([]byte, 5)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
}

func TestPreparedUnaryResponseUsesSingleWrite(t *testing.T) {
	router := api.NewRouter()
	if err := router.Registry.Register("echo", 1); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterParams("echo", nil); err != nil {
		t.Fatal(err)
	}
	router.Handle("echo", func(_ context.Context, req *api.Request) error {
		req.Buf.B = append(req.Buf.B, 7, 9)
		return nil
	})
	server := NewServer(router)
	defer server.Close()
	var writer preparedWriter
	request := encodeRequestEnvelope(requestKindCall, "", encodeCallRequest(1, nil, nil))
	if err := server.processRequest(&writer, request); err != nil {
		t.Fatal(err)
	}
	if writer.writes != 1 || !bytes.Equal(writer.data, []byte{0, 0, 0, 3, statusOK, 7, 9}) {
		t.Fatalf("writes=%d data=%x", writer.writes, writer.data)
	}
}

func TestConnectionFrameReuseCandidateMatchesWire(t *testing.T) {
	var storage []byte
	for _, size := range []int{0, 4096, 1, 128, 65536, 0} {
		params := bytes.Repeat([]byte{17}, size)
		frame := prepareReusedFrameCandidate(storage, params)
		want := prepareCallFrame(requestKindCall, "", 1, nil, params)
		if !bytes.Equal(frame, want) {
			t.Fatalf("reused frame mismatch: size=%d", size)
		}
		if cap(frame) > 64<<10 {
			storage = nil
		} else {
			storage = frame[:0]
		}
	}
}
