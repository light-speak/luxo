package queue

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// natsTestURL returns the NATS URL for integration tests.
// Skips the test if NATS is not available.
func natsTestURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("NATS_URL")
	if url == "" {
		t.Skip("NATS_URL not set, skipping NATS integration test")
	}
	return url
}

func TestNATSQueueConnectFail(t *testing.T) {
	_, err := NewNATSQueue("nats://192.0.2.1:1", DefaultConfig())
	if err == nil {
		t.Error("expected error connecting to bad NATS URL")
	}
}

func TestNATSQueuePublishSubscribe(t *testing.T) {
	url := natsTestURL(t)

	cfg := Config{
		MaxRetries:  3,
		RetryDelay:  100 * time.Millisecond,
		Concurrency: 2,
	}

	q, err := NewNATSQueue(url, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	var received atomic.Value
	done := make(chan struct{})

	// Use unique queue name to avoid stream conflicts between test runs.
	queueName := "nats_test_" + time.Now().Format("150405")

	err = q.Subscribe(queueName, func(ctx context.Context, payload []byte) error {
		received.Store(string(payload))
		close(done)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = q.Publish(context.Background(), queueName, []byte("nats-hello"))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for NATS handler")
	}

	if got := received.Load().(string); got != "nats-hello" {
		t.Errorf("got %q, want %q", got, "nats-hello")
	}
}

func TestNATSQueueRetryAndDLQ(t *testing.T) {
	url := natsTestURL(t)

	cfg := Config{
		MaxRetries:  2,
		RetryDelay:  100 * time.Millisecond,
		Concurrency: 1,
	}

	q, err := NewNATSQueue(url, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	queueName := "nats_retry_" + time.Now().Format("150405")
	dlqName := queueName + ".dlq"

	var dlqReceived atomic.Value
	dlqDone := make(chan struct{})

	// Subscribe to DLQ.
	err = q.Subscribe(dlqName, func(ctx context.Context, payload []byte) error {
		dlqReceived.Store(string(payload))
		close(dlqDone)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Handler that always fails.
	var attempts atomic.Int32
	err = q.Subscribe(queueName, func(ctx context.Context, payload []byte) error {
		attempts.Add(1)
		return errAlwaysFail
	})
	if err != nil {
		t.Fatal(err)
	}

	err = q.Publish(context.Background(), queueName, []byte("doomed"))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-dlqDone:
	case <-time.After(30 * time.Second):
		t.Fatalf("timeout waiting for DLQ, attempts=%d", attempts.Load())
	}

	if got := dlqReceived.Load().(string); got != "doomed" {
		t.Errorf("DLQ got %q, want %q", got, "doomed")
	}
}

func TestNATSQueuePublishAfterClose(t *testing.T) {
	url := natsTestURL(t)

	q, err := NewNATSQueue(url, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	q.Close()

	err = q.Publish(context.Background(), "closed_test", []byte("late"))
	if err == nil {
		t.Error("expected error publishing to closed queue")
	}
}

func TestNATSQueueSubscribeAfterClose(t *testing.T) {
	url := natsTestURL(t)

	q, err := NewNATSQueue(url, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	q.Close()

	err = q.Subscribe("closed_test", func(ctx context.Context, payload []byte) error { return nil })
	if err == nil {
		t.Error("expected error subscribing to closed queue")
	}
}

var errAlwaysFail = &alwaysFailError{}

type alwaysFailError struct{}

func (e *alwaysFailError) Error() string { return "always fail" }
