package queue

import (
	"testing"
)

func TestConfigDefaultsDoNotHideNegativeValues(t *testing.T) {
	for _, original := range []Config{{MaxRetries: -1}, {RetryDelay: -1}, {Concurrency: -1}} {
		cfg := original
		cfg.SetDefaults()
		if original.MaxRetries < 0 && cfg.MaxRetries >= 0 ||
			original.RetryDelay < 0 && cfg.RetryDelay >= 0 ||
			original.Concurrency < 0 && cfg.Concurrency >= 0 {
			t.Errorf("negative configuration was silently defaulted: %+v -> %+v", original, cfg)
		}
	}
}

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
	q := mustMemoryQueue(t, DefaultConfig())
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
}

func mustMemoryQueue(t testing.TB, cfg Config) *MemoryQueue {
	t.Helper()
	q, err := NewMemoryQueue(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestMemoryQueueRejectsNegativeConfiguration(t *testing.T) {
	for _, cfg := range []Config{{MaxRetries: -1}, {RetryDelay: -1}, {Concurrency: -1}} {
		q, err := NewMemoryQueue(cfg)
		if q != nil {
			_ = q.Close()
		}
		if q != nil || err != errNegativeConfig {
			t.Errorf("negative configuration accepted: %+v, %v", cfg, err)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	for _, cfg := range []Config{{}, DefaultConfig(), {MaxRetries: 1, RetryDelay: 1, Concurrency: 1}} {
		if err := cfg.Validate(); err != nil {
			t.Errorf("valid configuration rejected: %+v, %v", cfg, err)
		}
		q := mustMemoryQueue(t, cfg)
		want := cfg
		want.SetDefaults()
		if q.cfg != want {
			t.Errorf("constructor changed explicit/default configuration: %+v != %+v", q.cfg, want)
		}
		if err := q.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
