package h2transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"unsafe"

	"golang.org/x/net/http2"
)

var receiverInitSink *requestReceiver

func BenchmarkReceiverInit(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var err error
		receiverInitSink, err = newRequestReceiver(8, 1<<20, 8<<20)
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(unsafe.Sizeof(requestReceiver{})), "struct-B")
}

func BenchmarkReceiveBurst(b *testing.B) {
	for _, count := range []int{1, 8, 16} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			r, _ := newRequestReceiver(16, 65536, 1<<20)
			r.budget = &byteBudget{limit: 1 << 20}
			var requests [16]receivedRequest
			body := make([]byte, 4096)
			b.ReportAllocs()
			for b.Loop() {
				for i := range count {
					r.streams[uint32(i+1)] = &requests[i]
					if !r.appendBody(&requests[i], body) {
						b.Fatal("append")
					}
				}
				for i := range count {
					r.release(uint32(i + 1))
				}
			}
		})
	}
}

func BenchmarkRequestHead(b *testing.B) {
	frame := &http2.MetaHeadersFrame{HeadersFrame: &http2.HeadersFrame{FrameHeader: http2.FrameHeader{StreamID: 1}}, Fields: requestFields()}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := parseRequestHead(frame); err != nil {
			b.Fatal(err)
		}
	}
}

// Includes admission, fresh body ownership, copying and release, but not HPACK,
// mailbox handoff, business execution or socket I/O. No allocation is hidden.
func BenchmarkReceiveUnary4KiB(b *testing.B) {
	var wire bytes.Buffer
	_ = http2.NewFramer(&wire, nil).WriteData(1, true, make([]byte, 4096))
	f, err := http2.NewFramer(nil, &wire).ReadFrame()
	if err != nil {
		b.Fatal(err)
	}
	data := f.(*http2.DataFrame)
	head := &http2.MetaHeadersFrame{HeadersFrame: &http2.HeadersFrame{}, Fields: requestFields()}
	r, _ := newRequestReceiver(8, 1<<20, 8<<20)
	id := uint32(1)
	b.ReportAllocs()
	for b.Loop() {
		head.StreamID, data.StreamID = id, id
		if _, err := r.open(head); err != nil {
			b.Fatal(err)
		}
		if _, _, err := r.data(data); err != nil {
			b.Fatal(err)
		}
		r.release(id)
		id += 2
	}
}

var receiveConnectionInitSink *candidateConnection

// Both variants use the current layout. This isolates budget configuration,
// not historical object-size changes, goroutine startup or a TLS handshake.
func BenchmarkReceiveWindowConnectionInit(b *testing.B) {
	for _, configured := range []bool{false, true} {
		b.Run(fmt.Sprint(configured), func(b *testing.B) {
			ctx, handler := context.Background(), http.NotFoundHandler()
			b.ReportAllocs()
			for b.Loop() {
				c := newCandidateConnection(ctx, nil, handler)
				if configured {
					c.configureReceiveWindows()
				}
				receiveConnectionInitSink = c
			}
		})
	}
}

// Reuses the writer to isolate initial control-frame encoding and flushing to
// a discard sink. It does not measure socket writes or connection allocation.
func BenchmarkReceiveWindowAnnouncement(b *testing.B) {
	for _, bounded := range []bool{false, true} {
		b.Run(fmt.Sprint(bounded), func(b *testing.B) {
			q, _ := newDataQueue(2, 1024, func([]byte) {})
			w := newControlWriter(newDataWriter(q, io.Discard, 1024))
			if bounded {
				w.receiveConnection, w.receiveStream = 8<<20, 2<<20
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := w.writeInitialSettings(); err != nil {
					b.Fatal(err)
				}
				if err := w.data.buffer.Flush(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
