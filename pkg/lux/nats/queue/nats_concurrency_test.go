package queue

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func TestNATSQueueUsesConfiguredConcurrency(t *testing.T) {
	observer, err := nats.Connect(natsTestURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(observer.Close)
	js, err := jetstream.New(observer)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Concurrency: 4, MaxRetries: 1, RetryDelay: time.Minute}
	q, err := NewNATSQueue(natsTestURL(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	name := "concurrencyaudit" + strconv.FormatInt(time.Now().UnixNano(), 10)
	cleanupQueueStream(t, js, streamName(name))
	defer func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	}()
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	entered := make(chan struct{}, 8)
	var active, maximum atomic.Int32
	handler := func(context.Context, []byte) error {
		current := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); current > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, current) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		return nil
	}
	if err := q.Subscribe(name, handler); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for range 8 {
		if err := q.Publish(ctx, name, []byte("work")); err != nil {
			t.Fatal(err)
		}
	}
	for range cfg.Concurrency {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatalf("configured %d workers, only %d entered: %v", cfg.Concurrency, maximum.Load(), ctx.Err())
		}
	}
	select {
	case <-entered:
		t.Fatal("exceeded configured concurrency")
	case <-time.After(20 * time.Millisecond):
	}
	consumer, err := js.Consumer(ctx, streamName(name), streamName(name)+"_worker")
	if err != nil {
		t.Fatal(err)
	}
	info, err := consumer.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.NumAckPending > cfg.Concurrency+queuePrefetchLimit {
		t.Fatalf("broker deliveries exceed bounded intake: %d", info.NumAckPending)
	}
	renewal := q.consumers[0].(*renewingConsumer).renewal
	renewal.mu.Lock()
	tracked := len(renewal.active)
	renewal.mu.Unlock()
	if tracked != 8 {
		t.Fatalf("running and waiting messages must all be renewed: tracked=%d", tracked)
	}
	releaseOnce.Do(func() { close(release) })
	for range 8 - cfg.Concurrency {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if maximum.Load() != int32(cfg.Concurrency) {
		t.Fatalf("maximum active=%d", maximum.Load())
	}
}

func TestNATSQueueRenewsHealthyLongRunningTask(t *testing.T) {
	q, _, _, name := closeQueueFixture(t, true)
	q.cfg.RetryDelay = 40 * time.Millisecond
	q.cfg.MaxRetries = 3
	q.cfg.Concurrency = 2
	defer func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	}()
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	entered := make(chan struct{}, 8)
	if err := q.Subscribe(name, func(context.Context, []byte) error {
		entered <- struct{}{}
		<-release
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := q.Publish(ctx, name, []byte("long-task")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-entered:
		t.Fatal("healthy task was started again while its first handler was still running")
	case <-time.After(250 * time.Millisecond):
	}
	once.Do(func() { close(release) })
}

func TestNATSQueueDoesNotDeadLetterRunningHealthyTask(t *testing.T) {
	q, observer, js, name := closeQueueFixture(t, true)
	q.cfg.RetryDelay = 40 * time.Millisecond
	q.cfg.MaxRetries = 1
	q.cfg.Concurrency = 2
	cleanupQueueStream(t, js, streamName(name+dlqSuffix))
	defer func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	}()
	release := make(chan struct{})
	defer close(release)
	entered := make(chan struct{}, 1)
	if err := q.Subscribe(name, func(context.Context, []byte) error {
		entered <- struct{}{}
		<-release
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	dlq, err := observer.SubscribeSync(name + dlqSuffix)
	if err != nil {
		t.Fatal(err)
	}
	defer dlq.Unsubscribe()
	if err := observer.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := q.Publish(ctx, name, []byte("still-healthy")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	message, err := dlq.NextMsg(250 * time.Millisecond)
	if err != nats.ErrTimeout {
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("running healthy task transferred to DLQ before completion: %q", message.Data)
	}
}

func TestNATSQueueRenewalAcrossConsumersAndConnectionLoss(t *testing.T) {
	first, _, js, name := closeQueueFixture(t, true)
	first.cfg = Config{Concurrency: 1, MaxRetries: 10, RetryDelay: 60 * time.Millisecond}
	defer first.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entered, cancelled := make(chan struct{}), make(chan error, 1)
	if err := first.Subscribe(name, func(work context.Context, _ []byte) error {
		close(entered)
		select {
		case <-work.Done():
			cancelled <- context.Cause(work)
			return work.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := first.Publish(ctx, name, []byte("recover-me")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	second, err := NewNATSQueue(natsTestURL(t), first.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := second.Close(); err != nil {
			t.Error(err)
		}
	}()
	recovered := make(chan string, 4)
	if err := second.Subscribe(name, func(_ context.Context, payload []byte) error {
		recovered <- string(payload)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case payload := <-recovered:
		t.Fatalf("healthy first consumer lost ownership: %q", payload)
	case <-time.After(300 * time.Millisecond):
	}
	first.conn.Close()
	select {
	case cause := <-cancelled:
		if !errors.Is(cause, nats.ErrConnectionClosed) {
			t.Fatalf("cancellation cause: %v", cause)
		}
	case <-ctx.Done():
		t.Fatal("lost connection did not cancel handler", ctx.Err())
	}
	select {
	case payload := <-recovered:
		if payload != "recover-me" {
			t.Fatal(payload)
		}
	case <-ctx.Done():
		t.Fatal("second consumer did not recover task", ctx.Err())
	}
	assertQueueAcknowledged(t, ctx, js, name)
	if err := first.Close(); !errors.Is(err, nats.ErrConnectionClosed) {
		t.Fatalf("close lost connection error: %v", err)
	}
}

func TestNATSIdleQueueDoesNotContinuouslyRecreatePulls(t *testing.T) {
	q, _, js, name := closeQueueFixture(t, true)
	defer q.Close()
	if err := q.Subscribe(name, func(context.Context, []byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	consumer, err := js.Consumer(ctx, streamName(name), streamName(name)+"_worker")
	if err != nil {
		t.Fatal(err)
	}
	for {
		info, err := consumer.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.NumWaiting > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	before := q.conn.Stats().OutMsgs
	time.Sleep(600 * time.Millisecond)
	after := q.conn.Stats().OutMsgs
	closed := make(chan error, 1)
	go func() { closed <- q.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("long pull did not honor shutdown cancellation")
	}
	if after != before {
		t.Fatalf("idle queue recreated pulls: %d extra outbound messages", after-before)
	}
}

func TestNATSQueuedTasksRenewBeforeWorkerIsAvailable(t *testing.T) {
	q, observer, js, name := closeQueueFixture(t, true)
	q.cfg = Config{Concurrency: 1, MaxRetries: 1, RetryDelay: 40 * time.Millisecond}
	cleanupQueueStream(t, js, streamName(name+dlqSuffix))
	defer q.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	entered := make(chan string, 16)
	if err := q.Subscribe(name, func(_ context.Context, payload []byte) error {
		entered <- string(payload)
		if string(payload) == "0" {
			<-release
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	dlq, err := observer.SubscribeSync(name + dlqSuffix)
	if err != nil {
		t.Fatal(err)
	}
	defer dlq.Unsubscribe()
	if err := observer.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	for i := range 8 {
		if err := q.Publish(ctx, name, []byte(strconv.Itoa(i))); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case id := <-entered:
		if id != "0" {
			t.Fatal("unexpected first task", id)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	renewal := q.consumers[0].(*renewingConsumer).renewal
	for {
		renewal.mu.Lock()
		held := len(renewal.active)
		renewal.mu.Unlock()
		if held == 8 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("did not receive queued tasks")
		case <-time.After(time.Millisecond):
		}
	}
	if msg, err := dlq.NextMsg(250 * time.Millisecond); err != nats.ErrTimeout {
		t.Fatalf("queued task expired before a worker was available: message=%v error=%v", msg, err)
	}
	once.Do(func() { close(release) })
	seen := map[string]bool{"0": true}
	for range 7 {
		select {
		case id := <-entered:
			if len(id) != 1 || id[0] < '0' || id[0] > '7' {
				t.Fatal("unexpected task identity", id)
			}
			if seen[id] {
				t.Fatal("duplicate task", id)
			}
			seen[id] = true
		case <-ctx.Done():
			t.Fatal("queued task did not run", ctx.Err())
		}
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	assertQueueAcknowledged(t, ctx, js, name)
	if len(entered) != 0 {
		t.Fatal("unexpected repeated execution")
	}
}
