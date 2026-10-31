package h2transport

import (
	"bytes"
	"testing"

	"golang.org/x/net/http2"
)

type flushCounter struct {
	bytes.Buffer
	writes int
}

func (c *flushCounter) Write(p []byte) (int, error) {
	c.writes++
	return c.Buffer.Write(p)
}

func TestResponseHeadAndReadyDataShareFlush(t *testing.T) {
	for _, credit := range []sendWindow{0, 65535} {
		q, _ := testQueue(t, 2, 1024)
		if err := q.open(1, 65535); err != nil {
			t.Fatal(err)
		}
		var wire flushCounter
		w := newControlWriter(newDataWriter(q, &wire, 8192))
		w.connection = credit
		r := &response{status: 200, body: []byte("payload")}
		if err := w.writeResponse(1, r); err != nil {
			t.Fatal(err)
		}
		if err := w.flushReadyData(); err != nil {
			t.Fatal(err)
		}
		if wire.writes != 1 {
			t.Fatalf("credit=%d writes=%d", credit, wire.writes)
		}
		reader := http2.NewFramer(nil, &wire)
		frame, err := reader.ReadFrame()
		if err != nil || frame.Header().Type != http2.FrameHeaders {
			t.Fatal(frame, err)
		}
		if credit == 0 {
			if wire.Len() != 0 {
				t.Fatal("DATA escaped flow control")
			}
			continue
		}
		frame, err = reader.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		data, ok := frame.(*http2.DataFrame)
		if !ok || !data.StreamEnded() || string(data.Data()) != "payload" {
			t.Fatal(frame)
		}
	}
}

func BenchmarkResponseReadyFlush(b *testing.B) {
	for _, combined := range []bool{false, true} {
		name := "separate"
		if combined {
			name = "combined"
		}
		b.Run(name, func(b *testing.B) {
			q, err := newDataQueue(2, 8192, func([]byte) {})
			if err != nil {
				b.Fatal(err)
			}
			var wire flushCounter
			wire.Grow(8192)
			w := newControlWriter(newDataWriter(q, &wire, 8192))
			r := &response{status: 200, body: make([]byte, 4096)}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				wire.Reset()
				w.connection = 65535
				if err := q.open(1, 65535); err != nil {
					b.Fatal(err)
				}
				if err := w.writeResponse(1, r); err != nil {
					b.Fatal(err)
				}
				if combined {
					if err := w.flushReadyData(); err != nil {
						b.Fatal(err)
					}
				} else {
					if err := w.data.buffer.Flush(); err != nil {
						b.Fatal(err)
					}
					if _, err := w.data.writeBatch(&w.connection, 8, 65536, w.frameSize); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.ReportMetric(float64(wire.writes)/float64(b.N), "writes/op")
		})
	}
}
