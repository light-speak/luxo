package queue

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// A preloaded real stream isolates consumption from publishing. Timing starts
// before consumer Start and ends after ACK flush and close, so neither candidate
// can preload deliveries outside timing. Includes client background allocations
// and worker startup; excludes broker resources and subscription registration.
func BenchmarkNATSQueuedConsumption(b *testing.B) {
	for _, workers := range []int{1, 4} {
		b.Run(fmt.Sprint(workers), func(b *testing.B) { benchmarkQueuedConsumption(b, workers) })
	}
}

func benchmarkQueuedConsumption(b *testing.B, workers int) {
	b.StopTimer()
	address := os.Getenv("NATS_URL")
	if address == "" {
		b.Skip("NATS_URL required")
	}
	observer, err := nats.Connect(address)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(observer.Close)
	js, err := jetstream.New(observer)
	if err != nil {
		b.Fatal(err)
	}
	q, err := NewNATSQueue(address, Config{Concurrency: workers, MaxRetries: 3, RetryDelay: time.Minute})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := q.Close(); err != nil {
			b.Error(err)
		}
	})
	name := fmt.Sprintf("queuedbench%d", time.Now().UnixNano())
	b.Cleanup(func() {
		if err := js.DeleteStream(context.Background(), streamName(name)); err != nil {
			b.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(b.Context(), 30*time.Second)
	defer cancel()
	payload := []byte("queued-consumption")
	for range b.N {
		if err := q.Publish(ctx, name, payload); err != nil {
			b.Fatal(err)
		}
	}
	finished := make(chan struct{})
	var completed atomic.Int64
	consumer, err := q.createSubscription(name, func(context.Context, []byte) error {
		if completed.Add(1) == int64(b.N) {
			close(finished)
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	q.consumers = append(q.consumers, consumer)
	b.ReportAllocs()
	b.ResetTimer()
	b.StartTimer()
	consumer.Start(workers)
	select {
	case <-finished:
	case <-ctx.Done():
		b.Fatal(ctx.Err())
	}
	if err := q.Close(); err != nil {
		b.Fatal(err)
	}
	b.StopTimer()
	if completed.Load() != int64(b.N) {
		b.Fatal("unexpected delivery count", completed.Load())
	}
}
