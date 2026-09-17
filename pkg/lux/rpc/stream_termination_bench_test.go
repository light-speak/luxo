package rpc

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
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
					stop := context.AfterFunc(ctx, func() { conn.Close() })
					if checked {
						sub := &Subscription{messages: make(chan []byte, 64), errors: make(chan error, 1), conn: conn, cancel: cancel, stop: stop}
						sub.finish(result)
						terminalSubscriptionSink = sub
					} else {
						sub := &subscriptionBeforeTerminalResult{messages: make(chan []byte, 64), errors: make(chan error, 1), conn: conn, cancel: cancel, stop: stop}
						sub.finish(result)
						legacySubscriptionSink = sub
					}
				}
			})
		}
	}
}
