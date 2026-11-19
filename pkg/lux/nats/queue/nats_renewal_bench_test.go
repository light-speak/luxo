package queue

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// These local lifecycles exclude timers, broker I/O and contention. The control
// lacks renewal/cancellation protection and is not functionally equivalent.
func BenchmarkDeliveryRenewalLifecycle(b *testing.B) {
	for _, tracked := range []bool{false, true} {
		name := "unprotected"
		if tracked {
			name = "tracked"
		}
		b.Run(name, func(b *testing.B) {
			renewal := newDeliveryRenewal(time.Second, reportRenewalFailure)
			message := &mockMsg{}
			calls := 0
			handle := func(_ context.Context, msg jetstream.Msg) {
				calls++
				if err := msg.Ack(); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				if tracked {
					renewal.handle(message, handle)
				} else {
					handle(context.Background(), message)
				}
			}
			if calls != b.N || len(renewal.active) != 0 {
				b.Fatal("incomplete lifecycle")
			}
		})
	}
}
