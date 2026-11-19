package queue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func reportRenewalFailure(err error) {
	fmt.Fprintf(os.Stderr, "queue: progress renewal failed; delivery may repeat: %v\n", err)
}

type progressMessage struct {
	*mockMsg
	progress    atomic.Int32
	progressErr error
}

func (m *progressMessage) InProgress() error { m.progress.Add(1); return m.progressErr }

func TestDeliveryRenewalLifetime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newDeliveryRenewal(30*time.Millisecond, reportRenewalFailure)
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() { r.run(stop); close(done) }()
		message := &progressMessage{mockMsg: &mockMsg{}}
		r.handle(message, func(ctx context.Context, msg jetstream.Msg) {
			time.Sleep(100 * time.Millisecond)
			if ctx.Err() != nil || message.progress.Load() < 2 {
				t.Fatal("task was not renewed", ctx.Err(), message.progress.Load())
			}
			if err := msg.Ack(); err != nil {
				t.Fatal(err)
			}
			count := message.progress.Load()
			time.Sleep(100 * time.Millisecond)
			if message.progress.Load() != count {
				t.Fatal("renewed acknowledged message")
			}
		})
		close(stop)
		<-done
		if len(r.active) != 0 {
			t.Fatal("completed task retained")
		}
	})
}

func TestDeliveryRenewalFailureCancelsWithoutAcknowledging(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("progress unavailable")
		r := newDeliveryRenewal(30*time.Millisecond, reportRenewalFailure)
		stop, done := make(chan struct{}), make(chan struct{})
		go func() { r.run(stop); close(done) }()
		message := &progressMessage{mockMsg: &mockMsg{}, progressErr: failure}
		r.handle(message, func(ctx context.Context, msg jetstream.Msg) {
			<-ctx.Done()
			if !errors.Is(context.Cause(ctx), failure) {
				t.Fatal(context.Cause(ctx))
			}
			if msg.Ack() == nil || msg.NakWithDelay(time.Second) == nil {
				t.Fatal("lost renewal allowed final acknowledgement")
			}
		})
		close(stop)
		<-done
		if message.acked.Load() || message.naked.Load() {
			t.Fatal("failed lease acknowledged")
		}
	})
}

func TestLostRenewalDoesNotTriggerDeadLetter(t *testing.T) {
	for _, mode := range []string{"success", "error", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client := newMockJSClient()
				cfg := testConfig()
				cfg.MaxRetries = 1
				q := newTestNATSQueue(client, cfg)
				r := newDeliveryRenewal(30*time.Millisecond, reportRenewalFailure)
				stop, stopped := make(chan struct{}), make(chan struct{})
				go func() { r.run(stop); close(stopped) }()
				message := &progressMessage{mockMsg: &mockMsg{meta: &jetstream.MsgMetadata{NumDelivered: 1}}, progressErr: errors.New("renewal failed")}
				done := make(chan struct{})
				go func() {
					defer close(done)
					r.handle(message, func(ctx context.Context, msg jetstream.Msg) {
						q.handleDelivery(ctx, "tasks", func(ctx context.Context, _ []byte) error {
							<-ctx.Done()
							if mode == "goexit" {
								runtime.Goexit()
							}
							if mode == "error" {
								return errors.New("cancelled work")
							}
							return nil
						}, msg)
					})
				}()
				<-done
				close(stop)
				<-stopped
				if len(client.published) != 0 || message.acked.Load() || message.naked.Load() {
					t.Fatal("lost renewal must leave delivery for broker recovery")
				}
				if len(r.active) != 0 {
					t.Fatal("abnormal handler retained")
				}
			})
		})
	}
}

func TestDeliveryRenewalContinuesWhileConsumerDrains(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		messages := newTestMessages()
		message := &progressMessage{mockMsg: &mockMsg{}}
		messages.results <- iteratorResult{message: message}
		r := newDeliveryRenewal(30*time.Millisecond, reportRenewalFailure)
		entered, release := make(chan struct{}), make(chan struct{})
		inner := newMessageConsumer(messages, func(msg jetstream.Msg) {
			r.handle(msg, func(ctx context.Context, held jetstream.Msg) {
				close(entered)
				<-release
				if err := held.Ack(); err != nil {
					t.Error(err)
				}
			})
		}, reportRenewalFailure)
		c := &renewingConsumer{consumeContext: inner, renewal: r, done: make(chan struct{})}
		c.Start(1)
		<-entered
		c.Stop()
		time.Sleep(100 * time.Millisecond)
		if message.progress.Load() < 2 {
			t.Fatal("shutdown stopped renewal before handler finished")
		}
		select {
		case <-c.Done():
			t.Fatal("consumer reported completion early")
		default:
		}
		close(release)
		<-c.Done()
		if !message.acked.Load() || len(r.active) != 0 {
			t.Fatal("drain did not finalize accepted task")
		}
	})
}

func TestRenewalErrorReportingDoesNotBlockOtherAcknowledgements(t *testing.T) {
	r := newDeliveryRenewal(time.Second, reportRenewalFailure)
	failed := r.hold(&progressMessage{mockMsg: &mockMsg{}, progressErr: errors.New("offline")})
	healthy := r.hold(&progressMessage{mockMsg: &mockMsg{}})
	defer failed.release()
	defer healthy.release()
	reporting, releaseReport, reported := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() { r.renewAndReport(func(error) { close(reporting); <-releaseReport }); close(reported) }()
	<-reporting
	acknowledged := make(chan error, 1)
	go func() { acknowledged <- healthy.Ack() }()
	var blocked bool
	select {
	case err := <-acknowledged:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		blocked = true
	}
	close(releaseReport)
	<-reported
	if blocked {
		<-acknowledged
		t.Fatal("blocked error reporting held the delivery lock")
	}
}

func TestRenewalFailureDuringDrain(t *testing.T) {
	for _, workers := range []int{1, 4} {
		for _, mode := range []string{"success", "error", "goexit"} {
			t.Run(fmt.Sprintf("workers%d/%s", workers, mode), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) { testRenewalFailureDuringDrain(t, workers, mode) })
			})
		}
	}
}

func testRenewalFailureDuringDrain(t *testing.T, workers int, mode string) {
	t.Helper()
	client := newMockJSClient()
	cfg := testConfig()
	cfg.MaxRetries = 1
	q := newTestNATSQueue(client, cfg)
	failure := errors.New("renewal failed during drain")
	message := &progressMessage{mockMsg: &mockMsg{meta: &jetstream.MsgMetadata{NumDelivered: 1}}, progressErr: failure}
	messages := newTestMessages()
	messages.results <- iteratorResult{message: message}
	renewal := newDeliveryRenewal(30*time.Millisecond, func(error) {})
	entered := make(chan struct{})
	inner := newMessageConsumer(messages, func(msg jetstream.Msg) {
		renewal.handle(msg, func(ctx context.Context, held jetstream.Msg) {
			q.handleDelivery(ctx, "tasks", func(ctx context.Context, _ []byte) error {
				close(entered)
				<-ctx.Done()
				if !errors.Is(context.Cause(ctx), failure) {
					t.Error("lost cancellation cause", context.Cause(ctx))
				}
				if mode == "goexit" {
					runtime.Goexit()
				}
				if mode == "error" {
					return errors.New("handler noticed cancellation")
				}
				return nil
			}, held)
		})
	}, func(error) {})
	consumer := &renewingConsumer{consumeContext: inner, renewal: renewal, done: make(chan struct{})}
	consumer.Start(workers)
	<-entered
	consumer.Stop()
	<-consumer.Done()
	if message.progress.Load() != 1 {
		t.Fatal("renewal did not stop after failure", message.progress.Load())
	}
	if message.acked.Load() || message.naked.Load() || len(client.published) != 0 {
		t.Fatal("lost delivery was acknowledged or transferred")
	}
	if len(renewal.active) != 0 {
		t.Fatal("completed drain retained delivery")
	}
	if inner.parallel != nil && len(inner.parallel.slots) != 0 {
		t.Fatal("worker slot leaked")
	}
	time.Sleep(100 * time.Millisecond)
	if message.progress.Load() != 1 {
		t.Fatal("renewal continued after completion")
	}
}
