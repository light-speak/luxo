package event

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// Serial publish -> real broker -> handler notification. Subscription and Close
// are outside the timed loop; this is not a throughput or shutdown benchmark.
func BenchmarkNATSEventRoundTrip(b *testing.B) {
	address := os.Getenv("NATS_URL")
	if address == "" {
		b.Skip("NATS_URL required for real event benchmark")
	}
	for _, size := range []int{8, 4096} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			bus, err := NewNATSBus(address)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() {
				if err := bus.Close(); err != nil {
					b.Error(err)
				}
			})
			subject := fmt.Sprintf("audit.event.bench.%d", time.Now().UnixNano())
			completed := make(chan struct{}, 1)
			if err := bus.On(subject, func(_ context.Context, value any) error {
				if len(value.([]byte)) != size {
					b.Error("wrong event size")
				}
				completed <- struct{}{}
				return nil
			}); err != nil {
				b.Fatal(err)
			}
			if err := bus.conn.FlushTimeout(time.Second); err != nil {
				b.Fatal(err)
			}
			payload := make([]byte, size)
			b.ReportAllocs()
			for b.Loop() {
				if err := bus.Emit(b.Context(), subject, payload); err != nil {
					b.Fatal(err)
				}
				<-completed
			}
		})
	}
}
