package h2transport

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

func TestDataWriterBoundedBatch(t *testing.T) {
	q, released := testQueue(t, 2, 16)
	queueBody(t, q, 1, 100, []byte("abcdef"))
	queueBody(t, q, 3, 100, []byte("1234"))
	var wire bytes.Buffer
	w := newDataWriter(q, &wire, 4096)
	connection := sendWindow(100)
	got, err := w.writeBatch(&connection, 2, 100, 2)
	if err != nil || got.frames != 2 || got.bytes != 4 || *released != 0 {
		t.Fatal(got, err)
	}
	reader := http2.NewFramer(nil, &wire)
	for _, want := range []struct {
		id   uint32
		data string
	}{{1, "ab"}, {3, "12"}} {
		frame, err := reader.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		data := frame.(*http2.DataFrame)
		if data.StreamID != want.id || string(data.Data()) != want.data || data.StreamEnded() {
			t.Fatal("wrong frame")
		}
	}
	got, err = w.writeBatch(&connection, 100, 3, 16384)
	if err != nil || got.bytes != 3 || got.frames != 1 {
		t.Fatal("byte budget exceeded", got, err)
	}
	q.close()
	if *released != 2 {
		t.Fatal("pending bodies leaked")
	}
}

func TestDataWriterPipeFrames(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	_ = server.SetDeadline(time.Now().Add(3 * time.Second))
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	q, released := testQueue(t, 1, 32768)
	body := bytes.Repeat([]byte{7}, 20000)
	queueBody(t, q, 1, 65535, body)
	done := make(chan error, 1)
	go func() {
		connection := sendWindow(65535)
		_, err := newDataWriter(q, server, 4096).writeBatch(&connection, 8, 32768, 16384)
		done <- err
	}()
	reader := http2.NewFramer(nil, client)
	var got []byte
	for {
		f, err := reader.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		d := f.(*http2.DataFrame)
		got = append(got, d.Data()...)
		if d.StreamEnded() {
			break
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) || *released != 1 {
		t.Fatal("body damaged or not released")
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestDataWriterFailureCleanup(t *testing.T) {
	for _, size := range []int{1, 8192} {
		q, released := testQueue(t, 2, 16384)
		queueBody(t, q, 1, 65535, make([]byte, size))
		queueBody(t, q, 3, 65535, []byte("x"))
		connection := sendWindow(65535)
		_, err := newDataWriter(q, failedWriter{}, 32).writeBatch(&connection, 1, 16384, 16384)
		if !errors.Is(err, io.ErrClosedPipe) || *released != 2 || !q.closed || q.retained != 0 {
			t.Fatal("failed write leaked state", err)
		}
	}
}

func TestDataWriterBlockedAndInvalid(t *testing.T) {
	q, _ := testQueue(t, 1, 8)
	queueBody(t, q, 1, 0, []byte("a"))
	var wire bytes.Buffer
	w := newDataWriter(q, &wire, 64)
	connection := sendWindow(100)
	for _, limits := range [][3]int{{0, 1, 1}, {1, 0, 1}, {1, 1, 0}, {1, 1, 1 << 24}} {
		if _, err := w.writeBatch(&connection, limits[0], limits[1], limits[2]); !errors.Is(err, errBatchLimit) {
			t.Fatal(err)
		}
	}
	if got, err := w.writeBatch(&connection, 1, 100, 16384); err != nil || got.frames != 0 || wire.Len() != 0 {
		t.Fatal(got, err)
	}
	q.close()
	if _, err := w.writeBatch(&connection, 1, 100, 16384); !errors.Is(err, errQueueClosed) {
		t.Fatal(err)
	}
}
