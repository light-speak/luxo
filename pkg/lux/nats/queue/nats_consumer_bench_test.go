package queue

import (
	"strconv"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
)

func BenchmarkMessageConsumerRoundTrip(b *testing.B) {
	for _, count := range []int{1, 4} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			messages := newTestMessages()
			handled := make(chan struct{}, 1)
			consumer := newMessageConsumer(messages, func(jetstream.Msg) { handled <- struct{}{} }, reportRenewalFailure)
			consumer.Start(count)
			b.Cleanup(func() { consumer.Stop(); <-consumer.Done() })
			message := &mockMsg{}
			b.ReportAllocs()
			for b.Loop() {
				messages.results <- iteratorResult{message: message}
				<-handled
			}
		})
	}
}

func BenchmarkMessageConsumerLifecycle(b *testing.B) {
	for _, count := range []int{1, 4} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			handler := func(jetstream.Msg) {}
			b.ReportAllocs()
			for b.Loop() {
				consumer := newMessageConsumer(newTestMessages(), handler, reportRenewalFailure)
				consumer.Start(count)
				consumer.Stop()
				<-consumer.Done()
			}
		})
	}
}
