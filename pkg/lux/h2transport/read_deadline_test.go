package h2transport

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/net/http2"
)

type deadlineCountConn struct {
	faultConn
	sets int
	fail bool
}

func BenchmarkReadDeadline(b *testing.B) {
	for _, changing := range []bool{false, true} {
		name := "same"
		if changing {
			name = "changed"
		}
		b.Run(name, func(b *testing.B) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				b.Fatal(err)
			}
			defer listener.Close()
			conn, err := net.Dial("tcp", listener.Addr().String())
			if err != nil {
				b.Fatal(err)
			}
			defer conn.Close()
			peer, err := listener.Accept()
			if err != nil {
				b.Fatal(err)
			}
			defer peer.Close()
			now := time.Now()
			limits := DefaultLimits()
			limits.IdleTimeout = 2 * time.Hour
			c := &candidateConnection{conn: conn, limits: limits, deadline: now.Add(time.Hour)}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if changing {
					c.deadline = c.deadline.Add(time.Nanosecond)
				}
				if err := c.setReadDeadline(now); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(unsafe.Sizeof(candidateConnection{})), "connection-struct-B")
		})
	}
}

func (c *deadlineCountConn) SetReadDeadline(time.Time) error {
	c.sets++
	if c.fail {
		return errWriterDeadline
	}
	return nil
}

func TestUnchangedReadDeadline(t *testing.T) {
	now := time.Now()
	conn := new(deadlineCountConn)
	c := &candidateConnection{conn: conn, limits: DefaultLimits(), deadline: now.Add(time.Second)}
	for range 8 {
		if err := c.setReadDeadline(now); err != nil {
			t.Fatal(err)
		}
	}
	if conn.sets != 1 {
		t.Fatalf("unchanged deadline set %d times", conn.sets)
	}
	c.deadline = now.Add(2 * time.Second)
	conn.fail = true
	if err := c.setReadDeadline(now); !errors.Is(err, errWriterDeadline) {
		t.Fatal(err)
	}
	conn.fail = false
	if err := c.setReadDeadline(now); err != nil {
		t.Fatal(err)
	}
	if conn.sets != 3 {
		t.Fatal("failed update was cached", conn.sets)
	}
	c.deadline = now.Add(time.Millisecond)
	if err := c.setReadDeadline(now); err != nil {
		t.Fatal(err)
	}
	if conn.sets != 4 {
		t.Fatal("earlier deadline was skipped", conn.sets)
	}
}

type openingReadConn struct {
	faultConn
	input                 *bytes.Reader
	readDeadlines, failAt int
}

func (c *openingReadConn) Read(p []byte) (int, error) { return c.input.Read(p) }
func (c *openingReadConn) SetReadDeadline(time.Time) error {
	c.readDeadlines++
	if c.readDeadlines == c.failAt {
		return io.ErrClosedPipe
	}
	return nil
}

func TestConnectionReadOpeningFailures(t *testing.T) {
	var opening bytes.Buffer
	opening.WriteString(http2.ClientPreface)
	if err := http2.NewFramer(&opening, nil).WriteSettings(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		failAt        int
		closedMailbox bool
		want          error
	}{
		{"handshake-deadline", 1, false, io.ErrClosedPipe},
		{"request-deadline", 2, false, io.ErrClosedPipe},
		{"closed-mailbox", 0, true, errQueueClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &openingReadConn{input: bytes.NewReader(opening.Bytes()), failAt: tc.failAt}
			c := newCandidateConnection(t.Context(), conn, http.NotFoundHandler())
			if tc.closedMailbox {
				c.inbox.close()
			}
			if err := c.read(); !errors.Is(err, tc.want) {
				t.Fatal("opening failure was not preserved", err)
			}
			if tc.failAt == 1 && conn.input.Len() != opening.Len() {
				t.Fatal("read before handshake deadline was installed")
			}
			if c.openingComplete != (tc.failAt == 2) || len(c.tasks) != 0 {
				t.Fatal("failed opening advanced connection state")
			}
		})
	}
}

func invalidStreamWire(t *testing.T, count int, ping bool) []byte {
	t.Helper()
	var wire bytes.Buffer
	wire.Write(openingBytes(t))
	framer := http2.NewFramer(&wire, nil)
	for i := range count {
		if err := framer.WriteHeaders(http2.HeadersFrameParam{StreamID: uint32(2*i + 1), EndHeaders: true, EndStream: true}); err != nil {
			t.Fatal(err)
		}
	}
	if ping {
		if err := framer.WritePing(false, [8]byte{1}); err != nil {
			t.Fatal(err)
		}
	}
	return wire.Bytes()
}

func TestReadRejectsInvalidSettingsAfterOpening(t *testing.T) {
	for _, setting := range []http2.Setting{{ID: http2.SettingEnablePush, Val: 2}, {ID: http2.SettingMaxFrameSize, Val: 1}} {
		var wire bytes.Buffer
		wire.Write(openingBytes(t))
		if err := http2.NewFramer(&wire, nil).WriteSettings(setting); err != nil {
			t.Fatal(err)
		}
		conn := &openingReadConn{input: bytes.NewReader(wire.Bytes())}
		c := newCandidateConnection(t.Context(), conn, http.NotFoundHandler())
		if err := c.read(); !errors.Is(err, http2.ConnectionError(http2.ErrCodeProtocol)) {
			t.Fatal(setting, err)
		}
		command, ok, err := c.inbox.poll()
		if err != nil || !ok || command.kind != settingsCommand {
			t.Fatal("valid initial settings lost", command, err)
		}
		if _, ok, err := c.inbox.poll(); err != nil || ok || len(c.tasks) != 0 {
			t.Fatal("invalid settings reached writer or admitted work", err)
		}
	}
}

func TestReadStreamErrorContinuesToNextFrame(t *testing.T) {
	conn := &openingReadConn{input: bytes.NewReader(invalidStreamWire(t, 1, true))}
	c := newCandidateConnection(t.Context(), conn, http.NotFoundHandler())
	if err := c.read(); !errors.Is(err, io.EOF) {
		t.Fatal("single malformed stream stopped connection", err)
	}
	for _, kind := range []commandKind{settingsCommand, abortStreamCommand, pingCommand} {
		cmd, ok, err := c.inbox.poll()
		if err != nil || !ok || cmd.kind != kind {
			t.Fatal("missing subsequent frame or termination", cmd, err)
		}
		if kind == abortStreamCommand && (cmd.streamID != 1 || cmd.code != http2.ErrCodeProtocol) {
			t.Fatal("wrong malformed stream termination", cmd)
		}
	}
	if len(c.tasks) != 0 || len(c.receiver.streams) != 0 {
		t.Fatal("malformed stream admitted business work")
	}
}

func TestReadStreamErrorFloodAndMailboxFailure(t *testing.T) {
	for _, flood := range []bool{false, true} {
		conn := &openingReadConn{input: bytes.NewReader(invalidStreamWire(t, 2, false))}
		c := newCandidateConnection(t.Context(), conn, http.NotFoundHandler())
		if flood {
			c.rate.burst, c.rate.interval = 1, time.Hour
		} else {
			c.inbox, _ = newControlMailbox(1)
		}
		err := c.read()
		if flood {
			if !errors.Is(err, http2.ConnectionError(http2.ErrCodeEnhanceYourCalm)) {
				t.Fatal("stream-error flood not stopped", err)
			}
		} else {
			var streamErr http2.StreamError
			if !errors.As(err, &streamErr) || streamErr.StreamID != 1 {
				t.Fatal("full abort queue did not stop reading", err)
			}
		}
		if len(c.tasks) != 0 || len(c.receiver.streams) != 0 {
			t.Fatal("error handling retained request resources")
		}
	}
}
