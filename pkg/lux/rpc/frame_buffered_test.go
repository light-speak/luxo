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
	"testing/iotest"
	"testing/synctest"
	"time"
)

func TestFrameInputKeepsAdjacentFramesOwned(t *testing.T) {
	var wire bytes.Buffer
	payloads := [][]byte{{42}, {}, bytes.Repeat([]byte{21}, 28), bytes.Repeat([]byte{9}, 256), {71, 72}}
	for _, payload := range payloads {
		if err := WriteFrame(&wire, payload); err != nil {
			t.Fatal(err)
		}
	}
	input := frameInput{source: iotest.DataErrReader(bytes.NewReader(wire.Bytes()))}
	if n, err := input.Read(nil); n != 0 || err != nil {
		t.Fatalf("zero-length read = %d, %v", n, err)
	}
	var header [4]byte
	var responses [][]byte
	for range payloads {
		payload, err := readFrame(&input, nil, &header)
		if err != nil {
			t.Fatal(err)
		}
		responses = append(responses, payload)
	}
	for index, payload := range payloads {
		if !bytes.Equal(responses[index], payload) {
			t.Fatalf("payload %d = %x, want %x", index, responses[index], payload)
		}
	}
	if _, err := readFrame(&input, nil, &header); !errors.Is(err, io.EOF) {
		t.Fatalf("read after final frame = %v", err)
	}
}

func TestClientResponseWindowMatchesDirectRead(t *testing.T) {
	var wire bytes.Buffer
	payloads := [][]byte{{statusOK, 42}, nil, bytes.Repeat([]byte{21}, 14), bytes.Repeat([]byte{9}, 256)}
	for _, payload := range payloads {
		if err := WriteFrame(&wire, payload); err != nil {
			t.Fatal(err)
		}
	}
	for _, read := range []func(io.Reader) io.Reader{iotest.DataErrReader, iotest.HalfReader, iotest.OneByteReader} {
		for end := 0; end <= wire.Len(); end++ {
			data := wire.Bytes()[:end]
			plain := read(bytes.NewReader(data))
			conn := &poolConn{Conn: &prefaceTestConn{reader: read(bytes.NewReader(data))}}
			scratch := make([]byte, 20)
			var retained [][]byte
			for range len(payloads) + 1 {
				want, wantErr := ReadFrame(plain, nil)
				got, err := readClientFrame(conn, scratch)
				if err != wantErr || !bytes.Equal(got, want) || (got == nil) != (want == nil) {
					t.Fatalf("cut %d: got %x, %v, want %x, %v", end, got, err, want, wantErr)
				}
				if wantErr != nil {
					break
				}
				retained = append(retained, got)
				clear(scratch)
			}
			for i, want := range payloads[:len(retained)] {
				if !bytes.Equal(retained[i], want) {
					t.Fatalf("cut %d: retained response %d changed: %x", end, i, retained[i])
				}
			}
		}
	}
}

func TestClientResponseWindowCarriesLookaheadToStream(t *testing.T) {
	wire := []byte{0, 0, 0, 2, statusOK, 42, 0, 0, 0, 1, statusOK}
	conn := &poolConn{Conn: &prefaceTestConn{reader: bytes.NewReader(wire)}}
	if got, err := readClientFrame(conn, make([]byte, len(wire))); err != nil || !bytes.Equal(got, []byte{statusOK, 42}) {
		t.Fatalf("unary response %x: %v", got, err)
	}
	if got, err := ReadFrame(streamResponseConn(conn), nil); err != nil || !bytes.Equal(got, []byte{statusOK}) {
		t.Fatalf("stream acknowledgement %x: %v", got, err)
	}
}

func TestClientResponseWindowHandlesHeaderOnlyScratchAndLimit(t *testing.T) {
	for _, n := range []int{4, 12} {
		header := make([]byte, 4)
		binary.BigEndian.PutUint32(header, maxFrameSize+1)
		conn := &poolConn{Conn: &prefaceTestConn{reader: bytes.NewReader(header)}}
		got, err := readClientFrame(conn, make([]byte, n))
		if got != nil || err == nil {
			t.Fatalf("window %d accepted oversized frame: %x, %v", n, got, err)
		}
	}
}

func TestClientResponseWindowIgnoresErrorWithCompleteHeader(t *testing.T) {
	conn := &poolConn{Conn: &prefaceTestConn{reader: &headerErrorReader{
		header: bytes.NewReader([]byte{0, 0, 0, 2}), body: bytes.NewReader([]byte{statusOK, 42}),
	}}}
	got, err := readClientFrame(conn, make([]byte, 12))
	if err != nil || !bytes.Equal(got, []byte{statusOK, 42}) {
		t.Fatalf("got response %x: %v", got, err)
	}
}

type headerErrorReader struct {
	header *bytes.Reader
	body   *bytes.Reader
}

func (r *headerErrorReader) Read(dst []byte) (int, error) {
	if r.header.Len() == 0 {
		return r.body.Read(dst)
	}
	n, err := r.header.Read(dst)
	if r.header.Len() == 0 {
		return n, io.ErrClosedPipe
	}
	return n, err
}

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
