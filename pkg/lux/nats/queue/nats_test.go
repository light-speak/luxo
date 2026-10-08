package queue

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
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

func TestNATSQueueFromNilConn(t *testing.T) {
	if queue, err := NewNATSQueueFromConn(nil, DefaultConfig()); err == nil || queue != nil {
		t.Fatal("nil connection must return an error without a queue")
	}
}

func TestNATSQueueCloseReportsClosedOwnedConnection(t *testing.T) {
	q, err := NewNATSQueue(natsTestURL(t), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	q.conn.Close()
	if err := q.Close(); !errors.Is(err, nats.ErrConnectionClosed) {
		t.Fatalf("closed owned connection lost drain error: %v", err)
	}
	if !q.closed || len(q.consumers) != 0 {
		t.Fatal("drain failure left the queue open")
	}
	if err := q.Close(); err != nil {
		t.Fatalf("repeated close must not retry connection cleanup: %v", err)
	}
}

func TestNATSConsumerAdapterPreservesCreationFailure(t *testing.T) {
	conn, err := nats.Connect(natsTestURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	name := "adapter_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{Name: name, Subjects: []string{name}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := js.DeleteStream(ctx, name); err != nil {
			t.Errorf("delete owned stream: %v", err)
		}
	}()
	adapter := &realJSStream{s: stream}
	consumer, err := adapter.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{Durable: "invalid.name"})
	if !errors.Is(err, jetstream.ErrInvalidConsumerName) || consumer != nil {
		t.Fatalf("invalid consumer must preserve upstream error without a wrapper: %v, %v", consumer, err)
	}
}

func TestNATSQueueBorrowedConnectionOwnership(t *testing.T) {
	connection, err := nats.Connect(natsTestURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	queue, err := NewNATSQueueFromConn(connection, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	name := "borrowed_" + time.Now().Format("150405.000000000")
	if err := queue.Publish(ctx, name, []byte("owned-by-caller")); err != nil {
		t.Fatal(err)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	if connection.IsClosed() {
		t.Fatal("queue closed its caller's shared connection")
	}
	if err := connection.FlushTimeout(time.Second); err != nil {
		t.Fatal("borrowed connection is no longer usable", err)
	}
	if err := queue.Publish(ctx, name, nil); err == nil {
		t.Fatal("closed queue still accepted publication")
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
