package queue

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// --- Mock implementations ---

// mockJSClient implements jsClient for testing.
type mockJSClient struct {
	mu              sync.Mutex
	streams         map[string]*mockStream
	published       []publishedMsg
	publishErr      error
	createStreamErr error
	createStreamN   atomic.Int32 // number of CreateOrUpdateStream calls
}

type publishedMsg struct {
	subject string
	payload []byte
}

func newMockJSClient() *mockJSClient {
	return &mockJSClient{
		streams: make(map[string]*mockStream),
	}
}

func (m *mockJSClient) CreateOrUpdateStream(_ context.Context, cfg jetstream.StreamConfig) (jsStream, error) {
	m.createStreamN.Add(1)
	if m.createStreamErr != nil {
		return nil, m.createStreamErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.streams[cfg.Name]
	if !ok {
		s = &mockStream{name: cfg.Name}
		m.streams[cfg.Name] = s
	}
	return s, nil
}

func (m *mockJSClient) Publish(_ context.Context, subject string, payload []byte) error {
	if m.publishErr != nil {
		return m.publishErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(payload))
	copy(cp, payload)
	m.published = append(m.published, publishedMsg{subject: subject, payload: cp})
	return nil
}

// mockStream implements jsStream.
type mockStream struct {
	name              string
	createConsumerErr error
	consumer          *mockConsumer
}

func (s *mockStream) CreateOrUpdateConsumer(_ context.Context, _ jetstream.ConsumerConfig) (jsConsumer, error) {
	if s.createConsumerErr != nil {
		return nil, s.createConsumerErr
	}
	if s.consumer == nil {
		s.consumer = &mockConsumer{}
	}
	return s.consumer, nil
}

// mockConsumer implements jsConsumer.
type mockConsumer struct {
	consumeErr error
	handler    jetstream.MessageHandler
	ctx        *mockConsumeContext
}

func (c *mockConsumer) Consume(handler jetstream.MessageHandler, _ ...jetstream.PullConsumeOpt) (consumeContext, error) {
	if c.consumeErr != nil {
		return nil, c.consumeErr
	}
	c.handler = handler
	c.ctx = &mockConsumeContext{}
	return c.ctx, nil
}

// mockConsumeContext implements consumeContext.
type mockConsumeContext struct {
	stopped atomic.Bool
}

func (c *mockConsumeContext) Stop() {
	c.stopped.Store(true)
}

// mockMsg implements jetstream.Msg for testing message handling.
type mockMsg struct {
	data         []byte
	acked        atomic.Bool
	naked        atomic.Bool
	meta         *jetstream.MsgMetadata
	metaErr      error
	ackErr       error
	nakErr       error
	doubleAckErr error
}

func (m *mockMsg) Ack() error {
	m.acked.Store(true)
	return m.ackErr
}

func (m *mockMsg) DoubleAck(_ context.Context) error {
	return m.doubleAckErr
}

func (m *mockMsg) Nak() error {
	m.naked.Store(true)
	return m.nakErr
}

func (m *mockMsg) NakWithDelay(_ time.Duration) error {
	m.naked.Store(true)
	return m.nakErr
}

func (m *mockMsg) InProgress() error { return nil }

func (m *mockMsg) Term() error { return nil }

func (m *mockMsg) TermWithReason(_ string) error { return nil }

func (m *mockMsg) Metadata() (*jetstream.MsgMetadata, error) {
	if m.metaErr != nil {
		return nil, m.metaErr
	}
	return m.meta, nil
}

func (m *mockMsg) Data() []byte { return m.data }

func (m *mockMsg) Subject() string { return "" }

func (m *mockMsg) Reply() string { return "" }

func (m *mockMsg) Headers() nats.Header {
	return nats.Header{}
}

// newTestNATSQueue creates a NATSQueue with a mock jsClient for unit testing.
func newTestNATSQueue(js *mockJSClient, cfg Config) *NATSQueue {
	cfg.validate()
	return &NATSQueue{
		js:      js,
		cfg:     cfg,
		streams: make(map[string]bool),
	}
}

// --- Tests ---

func TestNATSPublishSuccess(t *testing.T) {
	mock := newMockJSClient()
	q := newTestNATSQueue(mock, testConfig())

	err := q.Publish(context.Background(), "tasks", []byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.published) != 1 {
		t.Fatalf("published %d messages, want 1", len(mock.published))
	}
	if string(mock.published[0].payload) != "hello" {
		t.Errorf("payload = %q, want %q", mock.published[0].payload, "hello")
	}
	if mock.published[0].subject != "tasks" {
		t.Errorf("subject = %q, want %q", mock.published[0].subject, "tasks")
	}
}

func TestNATSPublishError(t *testing.T) {
	mock := newMockJSClient()
	mock.publishErr = fmt.Errorf("connection lost")
	q := newTestNATSQueue(mock, testConfig())
	// Pre-cache the stream so ensureStream succeeds.
	q.streams["tasks"] = true

	err := q.Publish(context.Background(), "tasks", []byte("hello"))
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "queue publish: connection lost" {
		t.Errorf("error = %q, want contains 'connection lost'", got)
	}
}

func TestNATSPublishEnsureStreamError(t *testing.T) {
	mock := newMockJSClient()
	mock.createStreamErr = fmt.Errorf("stream limit")
	q := newTestNATSQueue(mock, testConfig())

	err := q.Publish(context.Background(), "tasks", []byte("hello"))
	if err == nil {
		t.Fatal("expected error from ensureStream")
	}
}

func TestNATSPublishStreamCaching(t *testing.T) {
	mock := newMockJSClient()
	q := newTestNATSQueue(mock, testConfig())

	// First publish creates the stream.
	if err := q.Publish(context.Background(), "tasks", []byte("a")); err != nil {
		t.Fatal(err)
	}
	// Second publish should reuse cached stream.
	if err := q.Publish(context.Background(), "tasks", []byte("b")); err != nil {
		t.Fatal(err)
	}

	// CreateOrUpdateStream called once for Publish (ensureStream caches),
	// but the second call skips it.
	if got := mock.createStreamN.Load(); got != 1 {
		t.Errorf("CreateOrUpdateStream called %d times, want 1", got)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.published) != 2 {
		t.Fatalf("published %d messages, want 2", len(mock.published))
	}
}

func TestNATSPublishAfterCloseMock(t *testing.T) {
	mock := newMockJSClient()
	q := newTestNATSQueue(mock, testConfig())
	q.Close()

	err := q.Publish(context.Background(), "tasks", []byte("late"))
	if err == nil {
		t.Error("expected error publishing to closed queue")
	}
}

func TestNATSSubscribeSuccess(t *testing.T) {
	mock := newMockJSClient()
	q := newTestNATSQueue(mock, testConfig())

	var called atomic.Bool
	err := q.Subscribe("tasks", func(_ context.Context, payload []byte) error {
		called.Store(true)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify consumer was created and consume was started.
	mock.mu.Lock()
	stream := mock.streams["luxo_q_tasks"]
	mock.mu.Unlock()
	if stream == nil {
		t.Fatal("stream not created")
	}
	if stream.consumer == nil {
		t.Fatal("consumer not created")
	}
	if stream.consumer.handler == nil {
		t.Fatal("consume handler not set")
	}

	// Simulate a message delivery.
	msg := &mockMsg{
		data: []byte("task-data"),
		meta: &jetstream.MsgMetadata{NumDelivered: 1},
	}
	stream.consumer.handler(msg)

	if !msg.acked.Load() {
		t.Error("message should have been acked")
	}
}

func TestNATSSubscribeMessageHandlerAck(t *testing.T) {
	mock := newMockJSClient()
	q := newTestNATSQueue(mock, testConfig())

	var received string
	err := q.Subscribe("tasks", func(_ context.Context, payload []byte) error {
		received = string(payload)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	mock.mu.Lock()
	stream := mock.streams["luxo_q_tasks"]
	mock.mu.Unlock()

	msg := &mockMsg{
		data: []byte("payload-1"),
		meta: &jetstream.MsgMetadata{NumDelivered: 1},
	}
	stream.consumer.handler(msg)

	if received != "payload-1" {
		t.Errorf("received = %q, want %q", received, "payload-1")
	}
	if !msg.acked.Load() {
		t.Error("expected ack on success")
	}
	if msg.naked.Load() {
		t.Error("unexpected nak on success")
	}
}

func TestNATSSubscribeMessageHandlerNak(t *testing.T) {
	mock := newMockJSClient()
	cfg := testConfig()
	cfg.MaxRetries = 3
	q := newTestNATSQueue(mock, cfg)

	err := q.Subscribe("tasks", func(_ context.Context, _ []byte) error {
		return fmt.Errorf("processing failed")
	})
	if err != nil {
		t.Fatal(err)
	}

	mock.mu.Lock()
	stream := mock.streams["luxo_q_tasks"]
	mock.mu.Unlock()

	// Delivery 1 of 3 — should nak (not last attempt).
	msg := &mockMsg{
		data: []byte("task"),
		meta: &jetstream.MsgMetadata{NumDelivered: 1},
	}
	stream.consumer.handler(msg)

	if msg.acked.Load() {
		t.Error("should not ack on error (not last attempt)")
	}
	if !msg.naked.Load() {
		t.Error("should nak on error")
	}
}

func TestNATSSubscribeDLQOnMaxRetries(t *testing.T) {
	mock := newMockJSClient()
	cfg := testConfig()
	cfg.MaxRetries = 2
	q := newTestNATSQueue(mock, cfg)

	err := q.Subscribe("tasks", func(_ context.Context, _ []byte) error {
		return fmt.Errorf("always fail")
	})
	if err != nil {
		t.Fatal(err)
	}

	mock.mu.Lock()
	stream := mock.streams["luxo_q_tasks"]
	mock.mu.Unlock()

	// Simulate last delivery attempt (NumDelivered == MaxRetries).
	msg := &mockMsg{
		data: []byte("doomed"),
		meta: &jetstream.MsgMetadata{NumDelivered: 2},
	}
	stream.consumer.handler(msg)

	// Should ack (to remove from main stream) and publish to DLQ.
	if !msg.acked.Load() {
		t.Error("should ack on last attempt (DLQ)")
	}
	if msg.naked.Load() {
		t.Error("should not nak on last attempt")
	}

	// Verify DLQ message was published.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	found := false
	for _, p := range mock.published {
		if p.subject == "tasks.dlq" && string(p.payload) == "doomed" {
			found = true
			break
		}
	}
	if !found {
		t.Error("DLQ message not published")
	}
}

func TestNATSSubscribeMetadataError(t *testing.T) {
	// When Metadata() fails, message should be naked (not sent to DLQ).
	mock := newMockJSClient()
	cfg := testConfig()
	cfg.MaxRetries = 2
	q := newTestNATSQueue(mock, cfg)

	err := q.Subscribe("tasks", func(_ context.Context, _ []byte) error {
		return fmt.Errorf("fail")
	})
	if err != nil {
		t.Fatal(err)
	}

	mock.mu.Lock()
	stream := mock.streams["luxo_q_tasks"]
	mock.mu.Unlock()

	msg := &mockMsg{
		data:    []byte("task"),
		metaErr: fmt.Errorf("metadata unavailable"),
	}
	stream.consumer.handler(msg)

	if msg.acked.Load() {
		t.Error("should not ack when metadata fails")
	}
	if !msg.naked.Load() {
		t.Error("should nak when metadata fails")
	}
}

func TestNATSSubscribeAfterCloseMock(t *testing.T) {
	mock := newMockJSClient()
	q := newTestNATSQueue(mock, testConfig())
	q.Close()

	err := q.Subscribe("tasks", func(_ context.Context, _ []byte) error { return nil })
	if err == nil {
		t.Error("expected error subscribing to closed queue")
	}
}

func TestNATSSubscribeCreateConsumerError(t *testing.T) {
	mock := newMockJSClient()
	q := newTestNATSQueue(mock, testConfig())

	// Pre-set stream with error.
	mock.mu.Lock()
	mock.streams["luxo_q_tasks"] = &mockStream{
		name:              "luxo_q_tasks",
		createConsumerErr: fmt.Errorf("consumer limit"),
	}
	mock.mu.Unlock()

	err := q.Subscribe("tasks", func(_ context.Context, _ []byte) error { return nil })
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNATSSubscribeConsumeError(t *testing.T) {
	mock := newMockJSClient()
	q := newTestNATSQueue(mock, testConfig())

	// Pre-set stream with consumer that fails on Consume.
	mock.mu.Lock()
	mock.streams["luxo_q_tasks"] = &mockStream{
		name: "luxo_q_tasks",
		consumer: &mockConsumer{
			consumeErr: fmt.Errorf("consume failed"),
		},
	}
	mock.mu.Unlock()

	err := q.Subscribe("tasks", func(_ context.Context, _ []byte) error { return nil })
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNATSSubscribeEnsureStreamError(t *testing.T) {
	mock := newMockJSClient()
	mock.createStreamErr = fmt.Errorf("stream error")
	q := newTestNATSQueue(mock, testConfig())

	err := q.Subscribe("tasks", func(_ context.Context, _ []byte) error { return nil })
	if err == nil {
		t.Fatal("expected error from ensureStream")
	}
}

func TestNATSCloseStopsConsumers(t *testing.T) {
	mock := newMockJSClient()
	q := newTestNATSQueue(mock, testConfig())

	err := q.Subscribe("tasks", func(_ context.Context, _ []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	mock.mu.Lock()
	stream := mock.streams["luxo_q_tasks"]
	mock.mu.Unlock()
	cc := stream.consumer.ctx

	err = q.Close()
	if err != nil {
		t.Fatal(err)
	}

	if !cc.stopped.Load() {
		t.Error("consumer context should have been stopped on Close")
	}
}

func TestNATSCloseIdempotent(t *testing.T) {
	mock := newMockJSClient()
	q := newTestNATSQueue(mock, testConfig())

	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	// Second close should be no-op.
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNATSCloseWithDrain(t *testing.T) {
	// When conn is set, Close should drain.
	// We can't easily mock nats.Conn, but we can verify behavior
	// when conn is nil (no drain call, no error).
	mock := newMockJSClient()
	q := newTestNATSQueue(mock, testConfig())
	// conn is nil by default in mock — Close should succeed without drain.
	if err := q.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNATSPublishPayloadIsolation(t *testing.T) {
	mock := newMockJSClient()
	q := newTestNATSQueue(mock, testConfig())

	data := []byte("original")
	if err := q.Publish(context.Background(), "tasks", data); err != nil {
		t.Fatal(err)
	}

	// Mutate original after publish.
	data[0] = 'X'

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if string(mock.published[0].payload) != "original" {
		t.Errorf("payload mutated: got %q", mock.published[0].payload)
	}
}

func TestNATSPublishDLQStreamCreateError(t *testing.T) {
	mock := newMockJSClient()
	q := newTestNATSQueue(mock, testConfig())

	// Subscribe first so we can trigger the handler.
	err := q.Subscribe("tasks", func(_ context.Context, _ []byte) error {
		return fmt.Errorf("fail")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Now make CreateOrUpdateStream fail for DLQ.
	mock.createStreamErr = fmt.Errorf("dlq stream fail")

	mock.mu.Lock()
	stream := mock.streams["luxo_q_tasks"]
	mock.mu.Unlock()

	// Simulate last attempt — DLQ publish should fail silently (logs to stderr).
	msg := &mockMsg{
		data: []byte("task"),
		meta: &jetstream.MsgMetadata{NumDelivered: uint64(q.cfg.MaxRetries)},
	}
	stream.consumer.handler(msg)

	// Message should still be acked (removed from main stream).
	if !msg.acked.Load() {
		t.Error("should ack even when DLQ stream creation fails")
	}
}

func TestNATSPublishDLQPublishError(t *testing.T) {
	mock := newMockJSClient()
	q := newTestNATSQueue(mock, testConfig())

	// Subscribe first.
	err := q.Subscribe("tasks", func(_ context.Context, _ []byte) error {
		return fmt.Errorf("fail")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Make Publish fail (affects DLQ publish too).
	mock.publishErr = fmt.Errorf("publish fail")

	mock.mu.Lock()
	stream := mock.streams["luxo_q_tasks"]
	mock.mu.Unlock()

	msg := &mockMsg{
		data: []byte("task"),
		meta: &jetstream.MsgMetadata{NumDelivered: uint64(q.cfg.MaxRetries)},
	}
	stream.consumer.handler(msg)

	// Message should still be acked.
	if !msg.acked.Load() {
		t.Error("should ack even when DLQ publish fails")
	}
}

func TestNATSHandlerPanicRecovery(t *testing.T) {
	mock := newMockJSClient()
	cfg := testConfig()
	cfg.MaxRetries = 3
	q := newTestNATSQueue(mock, cfg)

	err := q.Subscribe("tasks", func(_ context.Context, _ []byte) error {
		panic("boom")
	})
	if err != nil {
		t.Fatal(err)
	}

	mock.mu.Lock()
	stream := mock.streams["luxo_q_tasks"]
	mock.mu.Unlock()

	msg := &mockMsg{
		data: []byte("task"),
		meta: &jetstream.MsgMetadata{NumDelivered: 1},
	}

	// Should not panic — safeCall recovers.
	stream.consumer.handler(msg)

	if msg.acked.Load() {
		t.Error("should not ack on panic (not last attempt)")
	}
	if !msg.naked.Load() {
		t.Error("should nak on panic")
	}
}

func TestNATSStreamNameSanitization(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "luxo_q_simple"},
		{"with.dots", "luxo_q_with_dots"},
		{"a.b.c.d", "luxo_q_a_b_c_d"},
		{"nodots", "luxo_q_nodots"},
		{"", "luxo_q_"},
	}
	for _, tt := range tests {
		got := streamName(tt.input)
		if got != tt.want {
			t.Errorf("streamName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
