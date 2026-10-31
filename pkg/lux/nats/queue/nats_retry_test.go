package queue

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

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
