package rpc

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/api"
)

func TestReadFrameReusesCapacityAcrossLengths(t *testing.T) {
	storage := make([]byte, 0, 128)
	address := &storage[:cap(storage)][0]
	for _, size := range []int{128, 3, 0, 100, 1} {
		payload := bytes.Repeat([]byte{byte(size)}, size)
		var wire bytes.Buffer
		if err := WriteFrame(&wire, payload); err != nil {
			t.Fatal(err)
		}
		got, err := ReadFrame(&wire, storage[:0])
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("size %d: %x, %v", size, got, err)
		}
		if &got[:cap(got)][0] != address {
			t.Fatalf("size %d discarded available capacity", size)
		}
		storage = got
	}
}

func TestReadFrameCapacityStillRejectsBadFrames(t *testing.T) {
	storage := make([]byte, 0, 128)
	for _, size := range []uint32{10, maxFrameSize + 1} {
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], size)
		if got, err := ReadFrame(bytes.NewReader(header[:]), storage); err == nil || got != nil {
			t.Fatalf("accepted incomplete/oversized frame: %x, %v", got, err)
		}
	}
}

func TestRetainedReadFrameIsBounded(t *testing.T) {
	for _, size := range []int{0, 128, 64 << 10, (64 << 10) + 1, maxFrameSize} {
		buf := make([]byte, size)
		got := retainReadFrame(buf)
		if len(got) != 0 {
			t.Fatal("retained buffer must be reset")
		}
		if size > 64<<10 {
			if got != nil {
				t.Fatal("oversized backing storage retained")
			}
		} else if cap(got) != size {
			t.Fatal("small buffer discarded")
		}
	}
}

func TestServerReusesRequestOnlyAfterSynchronousDispatch(t *testing.T) {
	router := api.NewRouter()
	if err := router.Registry.Register("echo", 1); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterParams("echo", nil); err != nil {
		t.Fatal(err)
	}
	var first *byte
	calls := 0
	router.Handle("echo", func(_ context.Context, req *api.Request) error {
		body := req.BinaryRequest()
		if calls == 0 {
			first = &body[0]
		} else if first != &body[0] {
			t.Error("server did not reuse request storage")
		}
		req.Buf.B = append(req.Buf.B, body...)
		calls++
		return nil
	})
	server := NewServer(router)
	defer server.Close()
	var input, output bytes.Buffer
	preface := protocolPreface()
	input.Write(preface[:])
	body := encodeCallRequest(1, nil, nil)
	for range 2 {
		if err := WriteFrame(&input, encodeRequestEnvelope(requestKindCall, "", body)); err != nil {
			t.Fatal(err)
		}
	}
	conn := &prefaceTestConn{Conn: &poolLifecycleConn{}, reader: &input, writer: &output}
	if !server.registerConnection(conn) {
		t.Fatal("registration failed")
	}
	server.handleConn(conn)
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
	output.Next(len(preface))
	for range 2 {
		frame, err := ReadFrame(&output, nil)
		if err != nil || !bytes.Equal(frame, append([]byte{statusOK}, body...)) {
			t.Fatalf("response changed: %x, %v", frame, err)
		}
	}
	if _, err := output.ReadByte(); err != io.EOF {
		t.Fatal("unexpected trailing response")
	}
}
