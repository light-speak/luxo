package queue

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// The Msg is received from JetStream once before timing. Retain its real
// Metadata parser, but exclude network acknowledgements from this local control.
func BenchmarkNATSDeliveryMetadata(b *testing.B) {
	msg := benchmarkNATSMessage(b)
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "business-retry"
		}
		b.Run(name, func(b *testing.B) {
			client := newMockJSClient()
			q := newTestNATSQueue(client, DefaultConfig())
			b.Cleanup(func() { q.Close() })
			calls := 0
			if err := q.Subscribe("tasks", func(context.Context, []byte) error {
				calls++
				if fail {
					return errAlwaysFail
				}
				return nil
			}); err != nil {
				b.Fatal(err)
			}
			handler := client.streams["luxo_q_tasks"].consumer.handler
			b.ReportAllocs()
			for b.Loop() {
				handler(msg)
			}
			if calls != b.N {
				b.Fatalf("business calls=%d iterations=%d", calls, b.N)
			}
		})
	}
}

func benchmarkNATSMessage(b *testing.B) jetstream.Msg {
	b.Helper()
	address := os.Getenv("NATS_URL")
	if address == "" {
		b.Skip("NATS_URL required for real metadata benchmark")
	}
	connection, err := nats.Connect(address)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		b.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(b.Context(), 5*time.Second)
	defer cancel()
	name := fmt.Sprintf("metadata_%d", time.Now().UnixNano())
	stream, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: name, Subjects: []string{name}})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := js.DeleteStream(context.Background(), name); err != nil {
			b.Error(err)
		}
	})
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{Durable: "sample", AckPolicy: jetstream.AckExplicitPolicy, AckWait: time.Minute})
	if err != nil {
		b.Fatal(err)
	}
	if _, err := js.Publish(ctx, name, []byte("sample")); err != nil {
		b.Fatal(err)
	}
	msg, err := consumer.Next(jetstream.FetchMaxWait(time.Second))
	if err != nil {
		b.Fatal(err)
	}
	return &benchmarkAcklessMessage{Msg: msg}
}

type benchmarkAcklessMessage struct{ jetstream.Msg }

func (*benchmarkAcklessMessage) Ack() error                       { return nil }
func (*benchmarkAcklessMessage) Nak() error                       { return nil }
func (*benchmarkAcklessMessage) NakWithDelay(time.Duration) error { return nil }
