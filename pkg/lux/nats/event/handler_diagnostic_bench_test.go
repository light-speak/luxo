package event

import (
	"context"
	"testing"

	coreevent "github.com/light-speak/luxo/pkg/lux/event"
	"github.com/nats-io/nats.go"
)

// Preserve the former callback so both variants use the same dispatch entry.
func natsMessageHandlerBeforeDiagnostics(handler coreevent.Handler) nats.MsgHandler {
	return func(msg *nats.Msg) { coreevent.InvokeHandler(handler, bgCtx, msg.Data) }
}

func BenchmarkNATSHandlerDiagnostics(b *testing.B) {
	for _, candidate := range []bool{false, true} {
		name := "Before"
		if candidate {
			name = "After"
		}
		b.Run(name, func(b *testing.B) {
			calls := 0
			handler := func(context.Context, any) error { calls++; return nil }
			callback := natsMessageHandlerBeforeDiagnostics(handler)
			if candidate {
				callback = (&NATSBus{}).messageHandler("probe", handler)
			}
			message := &nats.Msg{Data: []byte{42}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				callback(message)
			}
			b.StopTimer()
			if calls != b.N {
				b.Fatalf("handled %d, want %d", calls, b.N)
			}
		})
	}
}

var diagnosticBusSink *NATSBus

func BenchmarkNATSDiagnosticBusState(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		diagnosticBusSink = &NATSBus{}
	}
}

var diagnosticHandlerSink nats.MsgHandler

func BenchmarkNATSDiagnosticHandlerFactory(b *testing.B) {
	bus := &NATSBus{}
	_ = bus
	handler := func(context.Context, any) error { return nil }
	b.ReportAllocs()
	for b.Loop() {
		diagnosticHandlerSink = bus.messageHandler("probe", handler)
	}
}

var diagnosticSubscriptionSink *busSubscription

func BenchmarkNATSDiagnosticSubscriptionState(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		diagnosticSubscriptionSink = &busSubscription{}
	}
}
