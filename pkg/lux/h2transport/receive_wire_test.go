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

func TestReceiveCreditOverPipe(t *testing.T) {
	server, client := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() { cancel(); _ = server.Close(); _ = client.Close() })
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	q, _ := newDataQueue(2, 1<<20, func([]byte) {})
	d := newDataWriter(q, server, 4096)
	mailbox, _ := newControlMailbox(8)
	writerDone := make(chan error, 1)
	go func() { writerDone <- newControlWriter(d).run(ctx, server, mailbox, time.Second) }()
	result := make(chan *receivedRequest, 1)
	readerDone := make(chan error, 1)
	go func() { readerDone <- readPipeRequest(server, mailbox, result) }()
	f := http2.NewFramer(client, client)
	if frame, err := f.ReadFrame(); err != nil || frame.Header().Type != http2.FrameSettings {
		t.Fatal(frame, err)
	}
	if _, err := client.Write(openingBytes(t)); err != nil {
		t.Fatal(err)
	}
	if frame, err := f.ReadFrame(); err != nil || !frame.(*http2.SettingsFrame).IsAck() {
		t.Fatal(frame, err)
	}
	if err := f.WriteSettingsAck(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(headerWire(t, requestFields(), false)); err != nil {
		t.Fatal(err)
	}
	for i := range 8 {
		if err := f.WriteData(1, i == 7, bytes.Repeat([]byte{byte(i)}, 16384)); err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			continue
		}
		updates := 2
		if i == 7 {
			updates = 1
		}
		for j := range updates {
			frame, err := f.ReadFrame()
			if err != nil {
				t.Fatal(err)
			}
			update, ok := frame.(*http2.WindowUpdateFrame)
			if !ok || update.StreamID != uint32(j) || update.Increment != 32768 {
				t.Fatal(frame)
			}
		}
	}
	if err := <-readerDone; err != nil {
		t.Fatal(err)
	}
	request := <-result
	if len(request.body) != 131072 || request.body[7*16384] != 7 {
		t.Fatal("incomplete body")
	}
	cancel()
	<-writerDone
}

func readPipeRequest(conn net.Conn, mailbox *controlMailbox, result chan<- *receivedRequest) error {
	f, opening, err := readClientOpening(conn)
	if err != nil {
		return err
	}
	if err := mailbox.put(opening); err != nil {
		return err
	}
	r, _ := newRequestReceiver(2, 200000, 400000)
	defer r.close()
	for {
		frame, err := f.ReadFrame()
		if err != nil {
			return err
		}
		if cmd, ok, err := decodeControl(frame); ok {
			if err != nil {
				return err
			}
			if err := mailbox.put(cmd); err != nil {
				return err
			}
			continue
		}
		var req *receivedRequest
		var credit receiveCredit
		switch frame := frame.(type) {
		case *http2.MetaHeadersFrame:
			req, credit, err = r.headers(frame)
		case *http2.DataFrame:
			req, credit, err = r.data(frame)
		}
		if err != nil {
			return err
		}
		if credit.connection != 0 || credit.stream != 0 {
			if err := mailbox.put(controlCommand{kind: receiveWindowCommand, streamID: frame.Header().StreamID, credit: credit}); err != nil {
				return err
			}
		}
		if req != nil {
			result <- req
			return nil
		}
	}
}

func TestReceiveCreditWriterErrors(t *testing.T) {
	q, _ := newDataQueue(1, 10, func([]byte) {})
	var out bytes.Buffer
	w := newControlWriter(newDataWriter(q, &out, 32))
	for _, cmd := range []controlCommand{
		{streamID: 1, credit: receiveCredit{connection: 1 << 31}},
		{streamID: 1, credit: receiveCredit{stream: 1 << 31}},
		{streamID: 0, credit: receiveCredit{stream: 1}},
		{streamID: 2, credit: receiveCredit{stream: 1}},
		{streamID: 1<<31 + 1, credit: receiveCredit{stream: 1}},
	} {
		if !errors.Is(w.returnReceiveCredit(cmd.streamID, cmd.credit), errInvalidWindow) {
			t.Fatal(cmd)
		}
	}
	if err := w.returnReceiveCredit(0, receiveCredit{}); err != nil {
		t.Fatal(err)
	}
	conn := &faultConn{failWrite: 1}
	w = newControlWriter(newDataWriter(q, conn, 1))
	if err := w.returnReceiveCredit(1, receiveCredit{connection: 1}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	conn = &faultConn{failWrite: 1}
	w = newControlWriter(newDataWriter(q, conn, 1))
	if err := w.returnReceiveCredit(1, receiveCredit{stream: 1}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
}
