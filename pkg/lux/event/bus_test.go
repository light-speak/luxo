package event

import (
	"os"
	"testing"
)

func TestNewFromEnvChanBus(t *testing.T) {
	// No NATS_URL → ChanBus
	t.Setenv("NATS_URL", "")
	os.Unsetenv("NATS_URL")
	bus, err := NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()

	if _, ok := bus.(*ChanBus); !ok {
		t.Errorf("expected ChanBus, got %T", bus)
	}
}

func TestNewFromEnvNATSBus(t *testing.T) {
	// Explicit integration configuration must connect successfully.
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		t.Skip("set NATS_URL to run the NATS factory integration test")
	}
	t.Setenv("NATS_URL", natsURL)

	bus, err := NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	if _, ok := bus.(*NATSBus); !ok {
		t.Errorf("expected NATSBus, got %T", bus)
	}
}

func TestNewFromEnvNATSFailure(t *testing.T) {
	for _, address := range []string{"nats://127.0.0.1:1", "", "   "} {
		t.Run(address, func(t *testing.T) {
			t.Setenv("NATS_URL", address)
			bus, err := NewFromEnv()
			if err == nil || bus != nil {
				t.Fatalf("configured NATS failure must propagate: bus=%T err=%v", bus, err)
			}
		})
	}
}
