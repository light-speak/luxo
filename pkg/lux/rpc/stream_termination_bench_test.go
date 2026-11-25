package rpc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

// Frozen pre-terminal-result layout and finish path. This reference is only for
// local setup/teardown cost, not a substitute for explicit wire completion.
type subscriptionBeforeTerminalResult struct {
	messages chan []byte
	errors   chan error
	conn     net.Conn
	cancel   context.CancelFunc
	stop     func() bool
	once     sync.Once
}

func (s *subscriptionBeforeTerminalResult) finish(err error) {
	s.once.Do(func() {
		if s.stop != nil {
			s.stop()
		}
		s.cancel()
		s.conn.Close()
		if err != nil {
			s.errors <- err
		}
		close(s.messages)
		close(s.errors)
	})
}

type terminalBenchmarkConn struct{ net.Conn }

func (*terminalBenchmarkConn) Close() error { return nil }

var terminalSubscriptionSink *Subscription
var legacySubscriptionSink *subscriptionBeforeTerminalResult

func BenchmarkRPCStreamTerminalLifecycle(b *testing.B) {
	for _, failure := range []bool{false, true} {
		for _, checked := range []bool{false, true} {
			b.Run(fmt.Sprintf("failure=%t/checked=%t", failure, checked), func(b *testing.B) {
				conn := &terminalBenchmarkConn{}
				var result error
				if failure {
					result = context.Canceled
				}
				b.ReportAllocs()
				for b.Loop() {
					ctx, cancel := context.WithCancel(context.Background())
					if checked {
						sub := &Subscription{messages: make(chan []byte, 64), errors: make(chan error, 1), conn: conn, cancel: cancel}
						sub.stop = context.AfterFunc(ctx, sub.closeConnection)
						sub.finish(result)
						terminalSubscriptionSink = sub
					} else {
						stop := context.AfterFunc(ctx, func() { conn.Close() })
						sub := &subscriptionBeforeTerminalResult{messages: make(chan []byte, 64), errors: make(chan error, 1), conn: conn, cancel: cancel, stop: stop}
						sub.finish(result)
						legacySubscriptionSink = sub
					}
				}
			})
		}
	}
}

type streamDataBenchmarkConn struct {
	net.Conn
	reader *bytes.Reader
}

func (c *streamDataBenchmarkConn) Read(data []byte) (int, error) { return c.reader.Read(data) }
func (*streamDataBenchmarkConn) Close() error                    { return nil }

func BenchmarkRPCStreamDataBoundary(b *testing.B) {
	for _, size := range []int{1, 8, 1024, 4096} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			var wire bytes.Buffer
			value := make([]byte, size)
			for range 64 {
				if err := writeStatusFrame(&wire, statusStream, value); err != nil {
					b.Fatal(err)
				}
			}
			if err := writeStatusFrame(&wire, statusStreamComplete, nil); err != nil {
				b.Fatal(err)
			}
			data := wire.Bytes()
			b.SetBytes(int64(64 * size))
			b.ReportAllocs()
			for b.Loop() {
				ctx, cancel := context.WithCancel(context.Background())
				sub := &Subscription{
					messages: make(chan []byte, 64), errors: make(chan error, 1), cancel: cancel,
					conn: &streamDataBenchmarkConn{reader: bytes.NewReader(data)},
				}
				sub.readLoop(ctx)
				count := 0
				for value := range sub.Messages() {
					if len(value) != size {
						b.Fatal("changed stream payload size")
					}
					count++
				}
				if count != 64 || sub.Err() != nil {
					b.Fatal("stream did not complete normally", count, sub.Err())
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*64), "ns/message")
		})
	}
}

var deadlineClassificationSink error

// The frozen implementation only observes ctx.Err and misses a socket timer
// that fires before the context timer publishes its terminal state.
func contextErrorBeforeDeadlineFix(ctx context.Context, _ error) error { return ctx.Err() }

func BenchmarkRPCDeadlineClassification(b *testing.B) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	past := pendingDeadlineContext{context.Background(), time.Now().Add(-time.Hour)}
	future := pendingDeadlineContext{context.Background(), time.Now().Add(time.Hour)}
	for _, sample := range []struct {
		name    string
		ctx     context.Context
		failure error
	}{
		{"NoDeadline", context.Background(), io.EOF},
		{"Cancelled", cancelled, io.EOF},
		{"ExpiredEOF", past, io.EOF},
		{"FutureTimeout", future, os.ErrDeadlineExceeded},
		{"ExpiredTimeout", past, os.ErrDeadlineExceeded},
		{"WrappedTimeout", past, fmt.Errorf("read: %w", os.ErrDeadlineExceeded)},
	} {
		b.Run(sample.name, func(b *testing.B) {
			for _, version := range []struct {
				name     string
				classify func(context.Context, error) error
			}{
				{"Before", contextErrorBeforeDeadlineFix}, {"After", contextErrorAfterIO},
			} {
				b.Run(version.name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						deadlineClassificationSink = version.classify(sample.ctx, sample.failure)
					}
				})
			}
		})
	}
}
