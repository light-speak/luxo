package queue

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func TestNATSQueueCloseWaitsAndFlushesRealHandler(t *testing.T) {
	for _, owned := range []bool{false, true} {
		t.Run(strconv.FormatBool(owned), func(t *testing.T) {
			q, observer, js, name := closeQueueFixture(t, owned)
			defer func() {
				if err := q.Close(); err != nil {
					t.Error(err)
				}
			}()
			entered, release := make(chan struct{}), make(chan struct{})
			released := false
			defer func() {
				if !released {
					close(release)
				}
			}()
			if err := q.Subscribe(name, func(context.Context, []byte) error { close(entered); <-release; return nil }); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if err := q.Publish(ctx, name, []byte("task")); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			closed := make(chan error, 1)
			go func() { closed <- q.Close() }()
			select {
			case err := <-closed:
				t.Fatalf("Close returned before handler: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			close(release)
			released = true
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if owned && !q.conn.IsClosed() {
				t.Fatal("owned connection remains open")
			}
			if err := observer.FlushTimeout(time.Second); err != nil {
				t.Fatalf("shared/observer connection unusable: %v", err)
			}
			assertQueueAcknowledged(t, ctx, js, name)
		})
	}
}

func TestNATSQueueCloseReportsSharedConnectionLoss(t *testing.T) {
	conn, err := nats.Connect(natsTestURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	q, err := NewNATSQueueFromConn(conn, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if err := q.Close(); !errors.Is(err, nats.ErrConnectionClosed) {
		t.Fatalf("lost failure: %v", err)
	}
}

func TestNATSQueueCloseReportsFlushTimeout(t *testing.T) {
	address, stopped := silentFlushServer(t)
	q, err := NewNATSQueue(address, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer q.conn.Close()
	err = q.Close()
	if !errors.Is(err, nats.ErrTimeout) {
		t.Fatalf("flush timeout lost: %v", err)
	}
	if q.Close() != err {
		t.Fatal("repeated close lost original timeout")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("failed close left owned connection open")
	}
}

func silentFlushServer(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.WriteString(conn, "INFO {\"server_id\":\"queue-close-audit\",\"version\":\"2.10.0\",\"proto\":1,\"max_payload\":1048576}\r\n")
		reader := bufio.NewReader(conn)
		initial := true
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if line == "PING\r\n" && initial {
				_, _ = io.WriteString(conn, "PONG\r\n")
				initial = false
			}
		}
	}()
	return "nats://" + listener.Addr().String(), stopped
}

func TestNATSQueueConnectionLostWhileClosingHandler(t *testing.T) {
	q, _, _, name := closeQueueFixture(t, true)
	defer q.conn.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		_ = q.Close()
	}()
	if err := q.Subscribe(name, func(context.Context, []byte) error { close(entered); <-release; return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := q.Publish(ctx, name, []byte("task")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	consumer := q.consumers[0].(*renewingConsumer).consumeContext.(*batchConsumer).messageConsumer
	closed := make(chan error, 1)
	go func() { closed <- q.Close() }()
	select {
	case <-consumer.stopped:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	q.conn.Close()
	close(release)
	select {
	case err := <-closed:
		if !errors.Is(err, nats.ErrConnectionClosed) {
			t.Fatalf("lost failure: %v", err)
		}
		if q.Close() != err {
			t.Fatal("repeated close lost failure")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func closeQueueFixture(t *testing.T, owned bool) (*NATSQueue, *nats.Conn, jetstream.JetStream, string) {
	t.Helper()
	observer, err := nats.Connect(natsTestURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(observer.Close)
	js, err := jetstream.New(observer)
	if err != nil {
		t.Fatal(err)
	}
	var q *NATSQueue
	if owned {
		q, err = NewNATSQueue(natsTestURL(t), testConfig())
	} else {
		q, err = NewNATSQueueFromConn(observer, testConfig())
	}
	if err != nil {
		t.Fatal(err)
	}
	name := "closeaudit" + strconv.FormatInt(time.Now().UnixNano(), 10)
	cleanupQueueStream(t, js, streamName(name))
	return q, observer, js, name
}

func assertQueueAcknowledged(t *testing.T, ctx context.Context, js jetstream.JetStream, name string) {
	t.Helper()
	consumer, err := js.Consumer(ctx, streamName(name), streamName(name)+"_worker")
	if err != nil {
		t.Fatal(err)
	}
	for {
		info, err := consumer.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.NumAckPending == 0 && info.AckFloor.Consumer > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("completed task not acknowledged")
		case <-time.After(time.Millisecond):
		}
	}
}
