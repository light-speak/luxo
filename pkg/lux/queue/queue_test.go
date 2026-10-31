package queue

import (
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
	}
	if cfg.RetryDelay != 5e9 {
		t.Errorf("RetryDelay = %v, want 5s", cfg.RetryDelay)
	}
	if cfg.Concurrency != 4 {
		t.Errorf("Concurrency = %d, want 4", cfg.Concurrency)
	}
}

func TestConfigValidateDefaults(t *testing.T) {
	cfg := Config{}
	cfg.SetDefaults()
	if cfg.MaxRetries != 3 || cfg.Concurrency != 4 || cfg.RetryDelay != 5e9 {
		t.Errorf("validate did not fill defaults: %+v", cfg)
	}
}

func TestConfigValidateKeepsExplicit(t *testing.T) {
	cfg := Config{MaxRetries: 5, RetryDelay: 1e9, Concurrency: 8}
	cfg.SetDefaults()
	if cfg.MaxRetries != 5 || cfg.Concurrency != 8 || cfg.RetryDelay != 1e9 {
		t.Errorf("validate overwrote explicit values: %+v", cfg)
	}
}

func TestExplicitMemoryQueueIgnoresNATSAddress(t *testing.T) {
	t.Setenv("NATS_URL", "nats://127.0.0.1:1")
	q := NewMemoryQueue(DefaultConfig())
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
}
