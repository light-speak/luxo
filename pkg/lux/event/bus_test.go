package event

import "testing"

func TestExplicitMemoryBusIgnoresNATSAddress(t *testing.T) {
	t.Setenv("NATS_URL", "nats://127.0.0.1:1")
	bus := NewChanBus(256)
	bus.Close()
}
