package queue

import (
	corequeue "github.com/light-speak/luxo/pkg/lux/queue"
	"os"
	"testing"
	"time"
)

func DefaultConfig() Config { return corequeue.DefaultConfig() }

func testConfig() Config {
	return Config{MaxRetries: 3, RetryDelay: 10 * time.Millisecond, Concurrency: 2}
}
func TestNewFromEnvNATS(t *testing.T) {
	if os.Getenv("NATS_URL") == "" {
		t.Skip("set NATS_URL to run the NATS factory integration test")
	}
	q, err := NewFromEnv(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if _, ok := q.(*NATSQueue); !ok {
		t.Fatalf("expected *NATSQueue, got %T", q)
	}
}

func TestNewFromEnvNATSFailure(t *testing.T) {
	for _, address := range []string{"nats://127.0.0.1:1", "", "   "} {
		t.Run(address, func(t *testing.T) {
			t.Setenv("NATS_URL", address)
			q, err := NewFromEnv(DefaultConfig())
			if err == nil || q != nil {
				t.Fatalf("configured NATS failure must propagate: queue=%T err=%v", q, err)
			}
		})
	}
}

func TestStreamName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"tasks", "luxo_q_tasks"},
		{"email.send", "luxo_q_email_dsend"},
		{"a.b.c", "luxo_q_a_db_dc"},
	}
	for _, tt := range tests {
		got := streamName(tt.input)
		if got != tt.want {
			t.Errorf("streamName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestStreamNamesDoNotAlias(t *testing.T) {
	names := []string{"email.send", "email_send", "email__send", "email_dsend"}
	seen := make(map[string]string)
	for _, name := range names {
		encoded := streamName(name)
		if previous, ok := seen[encoded]; ok {
			t.Fatalf("queues %q and %q share stream %q", previous, name, encoded)
		}
		seen[encoded] = name
	}
}
