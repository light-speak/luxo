package queue

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func TestNATSGoexitDeliveryUsesFailurePath(t *testing.T) {
	client := newMockJSClient()
	q := newTestNATSQueue(client, testConfig())
	defer q.Close()
	if err := q.Subscribe("tasks", func(context.Context, []byte) error {
		runtime.Goexit()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	message := &mockMsg{data: []byte("accepted"), meta: &jetstream.MsgMetadata{NumDelivered: 1}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		client.streams["luxo_q_tasks"].consumer.handler(message)
	}()
	<-done
	if message.acked.Load() || !message.naked.Load() || message.nakDelay <= 0 {
		t.Fatalf("abnormal exit skipped retry: ack=%v nak=%v delay=%v", message.acked.Load(), message.naked.Load(), message.nakDelay)
	}
}

func TestNATSExhaustedTaskRetriesOnlyTransfer(t *testing.T) {
	for _, failure := range []string{"create", "publish"} {
		t.Run(failure, func(t *testing.T) {
			client := newMockJSClient()
			q := newTestNATSQueue(client, testConfig())
			defer q.Close()
			calls := 0
			if err := q.Subscribe("tasks", func(context.Context, []byte) error { calls++; return errAlwaysFail }); err != nil {
				t.Fatal(err)
			}
			consumer := client.streams["luxo_q_tasks"].consumer
			if failure == "create" {
				client.createStreamErr = errAlwaysFail
			} else {
				client.publishErr = errAlwaysFail
			}
			for delivery := uint64(q.cfg.MaxRetries); delivery < uint64(q.cfg.MaxRetries)+3; delivery++ {
				msg := &mockMsg{data: []byte("retained"), meta: &jetstream.MsgMetadata{NumDelivered: delivery}}
				consumer.handler(msg)
				if msg.acked.Load() || !msg.naked.Load() || msg.nakDelay <= 0 || msg.nakDelay > time.Minute {
					t.Fatalf("transfer failure was not delayed: %+v", msg)
				}
			}
			client.createStreamErr = nil
			client.publishErr = nil
			msg := &mockMsg{data: []byte("retained"), meta: &jetstream.MsgMetadata{NumDelivered: uint64(q.cfg.MaxRetries) + 3}}
			consumer.handler(msg)
			if !msg.acked.Load() || msg.naked.Load() {
				t.Fatal("successful transfer not acknowledged")
			}
			if calls != 1 {
				t.Fatalf("exhausted business handler ran %d times", calls)
			}
			if len(client.published) != 1 || client.published[0].subject != "tasks.dlq" || string(client.published[0].payload) != "retained" {
				t.Fatal(client.published)
			}
		})
	}
}

func TestNATSInvalidDeliveryMetadataDoesNotRunHandler(t *testing.T) {
	for _, meta := range []*jetstream.MsgMetadata{nil, {}, {NumDelivered: 0}} {
		client := newMockJSClient()
		q := newTestNATSQueue(client, testConfig())
		t.Cleanup(func() { q.Close() })
		if err := q.Subscribe("tasks", func(context.Context, []byte) error { t.Error("untrusted delivery ran business handler"); return nil }); err != nil {
			t.Fatal(err)
		}
		msg := &mockMsg{meta: meta}
		client.streams["luxo_q_tasks"].consumer.handler(msg)
		if msg.acked.Load() || !msg.naked.Load() || msg.nakDelay <= 0 {
			t.Fatal("invalid metadata must retain original")
		}
	}
}

func TestNATSDeliveryConfigurationAndRetryBound(t *testing.T) {
	for _, attempts := range []int{1, 3, 1000} {
		t.Run(fmt.Sprint(attempts), func(t *testing.T) {
			client := newMockJSClient()
			q := newTestNATSQueue(client, Config{MaxRetries: attempts, RetryDelay: time.Second})
			defer q.Close()
			if err := q.Subscribe("tasks", func(context.Context, []byte) error { return errAlwaysFail }); err != nil {
				t.Fatal(err)
			}
			stream := client.streams["luxo_q_tasks"]
			if stream.config.MaxDeliver != -1 {
				t.Fatal("transfer retries must not exhaust broker delivery", stream.config.MaxDeliver)
			}
			if len(stream.config.BackOff) > 64 {
				t.Fatal("unbounded backoff allocation", len(stream.config.BackOff))
			}
			for _, delay := range stream.config.BackOff {
				if delay <= 0 || delay > time.Minute {
					t.Fatal("invalid backoff", delay)
				}
			}
			client.publishErr = errAlwaysFail
			msg := &mockMsg{meta: &jetstream.MsgMetadata{NumDelivered: ^uint64(0)}}
			stream.consumer.handler(msg)
			if msg.acked.Load() || msg.nakDelay != time.Minute {
				t.Fatal("overflowing retry was not capped", msg.nakDelay)
			}
		})
	}
}

func TestNATSBusinessFailureUsesDelayedNak(t *testing.T) {
	client := newMockJSClient()
	q := newTestNATSQueue(client, Config{MaxRetries: 4, RetryDelay: time.Second})
	defer q.Close()
	if err := q.Subscribe("tasks", func(context.Context, []byte) error { return errAlwaysFail }); err != nil {
		t.Fatal(err)
	}
	for delivery, want := range map[uint64]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second} {
		msg := &mockMsg{meta: &jetstream.MsgMetadata{NumDelivered: delivery}}
		client.streams["luxo_q_tasks"].consumer.handler(msg)
		if msg.acked.Load() || msg.nakDelay != want {
			t.Fatal(delivery, msg.nakDelay, want)
		}
	}
}

func TestNATSAcknowledgementFailureDoesNotDropTask(t *testing.T) {
	for _, success := range []bool{false, true} {
		client := newMockJSClient()
		q := newTestNATSQueue(client, testConfig())
		t.Cleanup(func() { q.Close() })
		if err := q.Subscribe("tasks", func(context.Context, []byte) error {
			if success {
				return nil
			}
			return errAlwaysFail
		}); err != nil {
			t.Fatal(err)
		}
		msg := &mockMsg{meta: &jetstream.MsgMetadata{NumDelivered: 1}, ackErr: errAlwaysFail, nakErr: errAlwaysFail}
		client.streams["luxo_q_tasks"].consumer.handler(msg)
		if msg.acked.Load() != success || msg.naked.Load() == success {
			t.Fatal("failed broker acknowledgement used the wrong termination path")
		}
	}
}

func TestNATSGoexitRestoresRealConsumer(t *testing.T) {
	for _, exhaust := range []bool{false, true} {
		t.Run(fmt.Sprint(exhaust), func(t *testing.T) {
			q, js, parent, name := queueNamingFixture(t)
			q.cfg = testConfig()
			cleanupQueueStream(t, js, streamName(name))
			cleanupQueueStream(t, js, streamName(name+dlqSuffix))
			ctx, cancel := context.WithTimeout(parent, 3*time.Second)
			defer cancel()
			var attempts, following atomic.Int32
			if err := q.Subscribe(name, func(_ context.Context, payload []byte) error {
				if string(payload) == "next" {
					following.Add(1)
					return nil
				}
				attempt := attempts.Add(1)
				if exhaust || attempt == 1 {
					runtime.Goexit()
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, payload := range []string{"exit", "next"} {
				if err := q.Publish(ctx, name, []byte(payload)); err != nil {
					t.Fatal(err)
				}
			}
			wantAttempts := int32(2)
			if exhaust {
				wantAttempts = int32(q.cfg.MaxRetries)
			}
			waitNATSState(t, ctx, func() bool { return attempts.Load() == wantAttempts && following.Load() == 1 })
			stream, err := js.Stream(ctx, streamName(name))
			if err != nil {
				t.Fatal(err)
			}
			waitNATSState(t, ctx, func() bool { info, err := stream.Info(ctx); return err == nil && info.State.Msgs == 0 })
			if exhaust {
				dead, err := js.Stream(ctx, streamName(name+dlqSuffix))
				if err != nil {
					t.Fatal(err)
				}
				msg, err := dead.GetMsg(ctx, 1)
				if err != nil || string(msg.Data) != "exit" {
					t.Fatalf("missing exhausted task: %v %v", msg, err)
				}
			}
		})
	}
}

func TestNATSGoexitExhaustionRetainsFailedDeadLetter(t *testing.T) {
	client := newMockJSClient()
	q := newTestNATSQueue(client, Config{MaxRetries: 1, RetryDelay: time.Millisecond})
	defer q.Close()
	var calls atomic.Int32
	if err := q.Subscribe("tasks", func(context.Context, []byte) error { calls.Add(1); runtime.Goexit(); return nil }); err != nil {
		t.Fatal(err)
	}
	client.publishErr = errAlwaysFail
	message := &mockMsg{data: []byte("keep"), meta: &jetstream.MsgMetadata{NumDelivered: 1}}
	done := make(chan struct{})
	go func() { defer close(done); client.streams["luxo_q_tasks"].consumer.handler(message) }()
	<-done
	if message.acked.Load() || !message.naked.Load() {
		t.Fatal("failed Goexit dead-letter transfer lost original")
	}
	client.publishErr = nil
	redelivery := &mockMsg{data: []byte("keep"), meta: &jetstream.MsgMetadata{NumDelivered: 2}}
	client.streams["luxo_q_tasks"].consumer.handler(redelivery)
	if calls.Load() != 1 || !redelivery.acked.Load() || redelivery.naked.Load() {
		t.Fatal("exhausted task replayed business or failed to acknowledge transfer")
	}
	if len(client.published) != 1 || client.published[0].subject != "tasks.dlq" || string(client.published[0].payload) != "keep" {
		t.Fatal("wrong retained dead letter")
	}
}

func TestNATSCanceledDeliveryLeavesMessageForRedelivery(t *testing.T) {
	q := newTestNATSQueue(newMockJSClient(), testConfig())
	defer q.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	msg := &mockMsg{}
	q.handleDelivery(ctx, "tasks", func(context.Context, []byte) error { t.Error("cancelled delivery invoked the handler"); return nil }, msg)
	if msg.acked.Load() || msg.naked.Load() {
		t.Fatal("cancelled delivery was acknowledged or retried locally")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("fixture did not cancel")
	}
}
