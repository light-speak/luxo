package queue

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Fail real JetStream operations, not a fake Publish: a conflicting subject
// rejects stream creation; a size limit rejects a stored-message publication.
func TestNATSDeadLetterRecovery(t *testing.T) {
	for _, failure := range []string{"create", "publish"} {
		t.Run(failure, func(t *testing.T) { testNATSDeadLetterRecovery(t, failure) })
	}
}

func testNATSDeadLetterRecovery(t *testing.T, failure string) {
	connection, err := nats.Connect(natsTestURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	name := fmt.Sprintf("dlq_%s_%d", failure, time.Now().UnixNano())
	defer func() {
		_ = js.DeleteStream(context.Background(), streamName(name))
		_ = js.DeleteStream(context.Background(), streamName(name+dlqSuffix))
	}()
	cfg := Config{MaxRetries: 1, RetryDelay: 10 * time.Millisecond}
	q, err := NewNATSQueueFromConn(connection, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	recoverTransfer := blockNATSTransfer(t, ctx, js, q, name, failure)
	var calls atomic.Int32
	handler := func(context.Context, []byte) error { calls.Add(1); return errAlwaysFail }
	if err := q.Subscribe(name, handler); err != nil {
		t.Fatal(err)
	}
	if err := q.Publish(ctx, name, []byte("keep-original-until-durable")); err != nil {
		t.Fatal(err)
	}
	consumer, err := js.Consumer(ctx, streamName(name), streamName(name)+"_worker")
	if err != nil {
		t.Fatal(err)
	}
	waitNATSState(t, ctx, func() bool { info, err := consumer.Info(ctx); return err == nil && info.Delivered.Consumer >= 3 })
	stream, err := js.Stream(ctx, streamName(name))
	if err != nil {
		t.Fatal(err)
	}
	info, err := stream.Info(ctx)
	if err != nil || info.State.Msgs != 1 {
		t.Fatalf("failed transfer lost original: %+v %v", info, err)
	}
	if failure == "create" {
		if err := q.Close(); err != nil {
			t.Fatal(err)
		}
		// Reconstruct all local state; only the durable delivery count survives.
		q, err = NewNATSQueueFromConn(connection, cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer q.Close()
		if err := q.Subscribe(name, handler); err != nil {
			t.Fatal(err)
		}
		waitNATSState(t, ctx, func() bool { info, err := consumer.Info(ctx); return err == nil && info.Delivered.Consumer >= 5 })
	}
	recoverTransfer()
	waitNATSState(t, ctx, func() bool { info, err := stream.Info(ctx); return err == nil && info.State.Msgs == 0 })
	dlq, err := js.Stream(ctx, streamName(name+dlqSuffix))
	if err != nil {
		t.Fatal(err)
	}
	message, err := dlq.GetMsg(ctx, 1)
	if err != nil || string(message.Data) != "keep-original-until-durable" {
		t.Fatalf("missing durable dead letter: %+v %v", message, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("exhausted business handler ran %d times", calls.Load())
	}
}

func blockNATSTransfer(t *testing.T, ctx context.Context, js jetstream.JetStream, q *NATSQueue, name, failure string) func() {
	t.Helper()
	if failure == "create" {
		blocker := streamName(name) + "_blocker"
		if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: blocker, Subjects: []string{name + dlqSuffix}}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = js.DeleteStream(context.Background(), blocker) })
		return func() {
			if err := js.DeleteStream(ctx, blocker); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := q.ensureStream(ctx, name+dlqSuffix); err != nil {
		t.Fatal(err)
	}
	dlq, err := js.Stream(ctx, streamName(name+dlqSuffix))
	if err != nil {
		t.Fatal(err)
	}
	info, err := dlq.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config := info.Config
	config.MaxMsgSize = 1
	if _, err := js.UpdateStream(ctx, config); err != nil {
		t.Fatal(err)
	}
	return func() {
		config.MaxMsgSize = -1
		if _, err := js.UpdateStream(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
}

func waitNATSState(t *testing.T, ctx context.Context, condition func() bool) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for JetStream state", ctx.Err())
		case <-ticker.C:
		}
	}
}
