package queue

import (
	"os"
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
	cfg.validate()
	if cfg.MaxRetries != 3 || cfg.Concurrency != 4 || cfg.RetryDelay != 5e9 {
		t.Errorf("validate did not fill defaults: %+v", cfg)
	}
}

func TestConfigValidateKeepsExplicit(t *testing.T) {
	cfg := Config{MaxRetries: 5, RetryDelay: 1e9, Concurrency: 8}
	cfg.validate()
	if cfg.MaxRetries != 5 || cfg.Concurrency != 8 || cfg.RetryDelay != 1e9 {
		t.Errorf("validate overwrote explicit values: %+v", cfg)
	}
}

func TestNewFromEnvMemory(t *testing.T) {
	os.Unsetenv("NATS_URL")
	q := NewFromEnv(DefaultConfig())
	defer q.Close()

	if _, ok := q.(*MemoryQueue); !ok {
		t.Errorf("expected *MemoryQueue, got %T", q)
	}
}

func TestNewFromEnvNATSFallback(t *testing.T) {
	// Bad NATS URL should fall back to MemoryQueue.
	t.Setenv("NATS_URL", "nats://192.0.2.1:1")
	q := NewFromEnv(DefaultConfig())
	defer q.Close()

	if _, ok := q.(*MemoryQueue); !ok {
		t.Errorf("expected *MemoryQueue fallback, got %T", q)
	}
}

func TestStreamName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"tasks", "luxo_q_tasks"},
		{"email.send", "luxo_q_email_send"},
		{"a.b.c", "luxo_q_a_b_c"},
	}
	for _, tt := range tests {
		got := streamName(tt.input)
		if got != tt.want {
			t.Errorf("streamName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
