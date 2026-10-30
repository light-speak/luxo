package rpc

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"testing/synctest"
	"time"
)

// Exercise buffering independently of whether the production transport enables it.
func TestBufferedReadFrameBoundaries(t *testing.T) {
	var wire bytes.Buffer
	sizes := []int{0, 1, 8188, 8189, 8192, 64 << 10, maxFrameSize, 3}
	for _, size := range sizes {
		if err := WriteFrame(&wire, bytes.Repeat([]byte{17}, size)); err != nil {
			t.Fatal(err)
		}
	}
	reader := bufio.NewReaderSize(&wire, 8192)
	var storage []byte
	for _, size := range sizes {
		got, err := ReadFrame(reader, storage)
		if err != nil || !bytes.Equal(got, bytes.Repeat([]byte{17}, size)) {
			t.Fatalf("size=%d err=%v", size, err)
		}
		storage = retainReadFrame(got)
	}
	if _, err := ReadFrame(reader, storage); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func TestBufferedReadFrameRejectsBrokenFrames(t *testing.T) {
	oversized := make([]byte, 4)
	binary.BigEndian.PutUint32(oversized, maxFrameSize+1)
	for _, data := range [][]byte{{0, 0}, {0, 0, 0, 3, 7}, oversized} {
		got, err := ReadFrame(bufio.NewReaderSize(bytes.NewReader(data), 8192), nil)
		if err == nil || got != nil {
			t.Fatalf("broken frame accepted: %x", data)
		}
	}
}

func TestBufferedReadFrameRetainsDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn, peer := net.Pipe()
		defer conn.Close()
		defer peer.Close()
		done := make(chan error, 1)
		go func() {
			_, err := ReadFrame(bufio.NewReaderSize(conn, 8192), nil)
			done <- err
		}()
		if _, err := peer.Write([]byte{0, 0, 0, 3, 7}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("partial frame completed: %v", err)
		default:
		}
		if err := conn.SetReadDeadline(time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := <-done; !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatal(err)
		}
	})
}
