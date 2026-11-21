package rpc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

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

func TestReusedFrameHeaderRejectsTruncatedFollowingFrame(t *testing.T) {
	wire := bytes.NewReader([]byte{0, 0, 0, 3, 'o', 'n', 'e', 0, 0, 0})
	var header [4]byte
	first, err := readFrame(wire, nil, &header)
	if err != nil || string(first) != "one" {
		t.Fatalf("first frame = %q, %v", first, err)
	}
	if frame, err := readFrame(wire, nil, &header); frame != nil || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated frame = %q, %v", frame, err)
	}
	if string(first) != "one" {
		t.Fatalf("later read changed the first payload to %q", first)
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
	addresses := make(map[*byte]bool)
	calls := 0
	router.Handle("echo", func(_ context.Context, req *api.Request) error {
		body := req.BinaryRequest()
		addresses[&body[0]] = true
		req.Buf.B = append(req.Buf.B, body...)
		calls++
		return nil
	})
	server := NewServer(router)
	defer server.Close()
	conn, peer := net.Pipe()
	defer peer.Close()
	if !server.registerConnection(conn) {
		t.Fatal("registration failed")
	}
	done := make(chan struct{})
	go func() { server.handleConn(conn); close(done) }()
	deadline, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := initiateHandshake(deadline, peer, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := peer.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	body := encodeCallRequest(1, nil, nil)
	for range 8 {
		if err := WriteFrame(peer, encodeRequestEnvelope(requestKindCall, "", body)); err != nil {
			t.Fatal(err)
		}
		frame, err := ReadFrame(peer, nil)
		if err != nil || !bytes.Equal(frame, append([]byte{statusOK}, body...)) {
			t.Fatalf("response changed: %x, %v", frame, err)
		}
	}
	peer.Close()
	select {
	case <-done:
	case <-deadline.Done():
		t.Fatal("connection did not stop")
	}
	if calls != 8 || len(addresses) > 3 {
		t.Fatalf("calls=%d retained request buffers=%d", calls, len(addresses))
	}
}
