package event

import (
	"os"
	"testing"
)

func TestNewFromEnvChanBus(t *testing.T) {
	// No NATS_URL → ChanBus
	os.Unsetenv("NATS_URL")
	bus := NewFromEnv()
	defer bus.Close()

	if _, ok := bus.(*ChanBus); !ok {
		t.Errorf("expected ChanBus, got %T", bus)
	}
}

func TestNewFromEnvNATSBus(t *testing.T) {
	// With NATS_URL → NATSBus (if NATS is running)
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}
	t.Setenv("NATS_URL", natsURL)

	bus := NewFromEnv()
	defer bus.Close()

	if _, ok := bus.(*NATSBus); !ok {
		// NATS not available, should fallback to ChanBus
		if _, ok := bus.(*ChanBus); !ok {
			t.Errorf("expected NATSBus or ChanBus fallback, got %T", bus)
		}
	}
}

func TestNewFromEnvNATSFallback(t *testing.T) {
	// Bad NATS_URL → should fallback to ChanBus
	t.Setenv("NATS_URL", "nats://192.0.2.1:1")

	bus := NewFromEnv()
	defer bus.Close()

	if _, ok := bus.(*ChanBus); !ok {
		t.Errorf("expected ChanBus fallback on bad NATS, got %T", bus)
	}
}
