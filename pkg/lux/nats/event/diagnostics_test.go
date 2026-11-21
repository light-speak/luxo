package event

import (
	"context"
	"errors"
	"github.com/light-speak/luxo/internal/diagnostics"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"
)

type blockedDiagnosticError struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (e *blockedDiagnosticError) Error() string {
	e.once.Do(func() { close(e.entered) })
	<-e.release
	return "blocked diagnostic"
}

func TestNATSBusDiagnosticDoesNotBlockDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := &blockedDiagnosticError{entered: make(chan struct{}), release: make(chan struct{})}
		bus := &NATSBus{}
		callback := bus.messageHandler("audit.diagnostic", func(context.Context, any) error { return failure })
		returned := make(chan struct{})
		go func() { callback(&nats.Msg{}); close(returned) }()
		<-failure.entered
		synctest.Wait()
		select {
		case <-returned:
		default:
			t.Error("diagnostic formatting blocked completed business delivery")
		}
		close(failure.release)
		<-returned
		if err := bus.reports.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestNATSBusClosePreservesDiagnosticDrainTimeout(t *testing.T) {
	conn, err := nats.Connect(natsURL(t))
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	synctest.Test(t, func(t *testing.T) {
		failure := &blockedDiagnosticError{entered: make(chan struct{}), release: make(chan struct{})}
		defer close(failure.release)
		bus := &NATSBus{conn: conn}
		bus.reports.Submit("audit.close", "blocked", failure)
		<-failure.entered
		start := time.Now()
		err := bus.Close()
		if !errors.Is(err, diagnostics.ErrDrain) || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != nats.DefaultTimeout {
			t.Fatalf("diagnostic timeout lost: %v elapsed=%v", err, time.Since(start))
		}
		if bus.Close() != err {
			t.Fatal("repeated close lost its original error")
		}
		bus.reports.Submit("after-close", "rejected", nil)
		if dropped, failed := bus.DiagnosticStats(); dropped != 1 || failed != 0 {
			t.Fatalf("closed diagnostics accepted new work: %d %d", dropped, failed)
		}
	})
}
