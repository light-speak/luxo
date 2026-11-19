package event

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/light-speak/luxo/internal/diagnostics"
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

func TestChanBusDiagnosticDoesNotBlockListeners(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := &blockedDiagnosticError{entered: make(chan struct{}), release: make(chan struct{})}
		bus := NewChanBus(1)
		if err := bus.On("audit.diagnostic", func(context.Context, any) error { return failure }); err != nil {
			t.Fatal(err)
		}
		delivered := make(chan struct{})
		if err := bus.On("audit.diagnostic", func(context.Context, any) error { close(delivered); return nil }); err != nil {
			t.Fatal(err)
		}
		if err := bus.Emit(t.Context(), "audit.diagnostic", testEvent{UserID: 1}); err != nil {
			t.Fatal(err)
		}
		<-failure.entered
		synctest.Wait()
		select {
		case <-delivered:
		default:
			t.Error("diagnostic formatting blocked the next business listener")
		}
		close(failure.release)
		if err := bus.Close(); err != nil {
			t.Fatal(err)
		}
		if dropped, failed := bus.DiagnosticStats(); dropped != 0 || failed != 0 {
			t.Fatal(dropped, failed)
		}
	})
}

type panickingDiagnosticError struct{}

func (panickingDiagnosticError) Error() string { panic("invalid diagnostic") }

func TestChanBusDiagnosticFailureIsIndependent(t *testing.T) {
	bus := NewChanBus(1)
	if err := bus.On("audit", func(context.Context, any) error { return panickingDiagnosticError{} }); err != nil {
		t.Fatal(err)
	}
	delivered := make(chan struct{})
	if err := bus.On("audit", func(context.Context, any) error { close(delivered); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := bus.Emit(t.Context(), "audit", testEvent{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := bus.Close(); !errors.Is(err, diagnostics.ErrAborted) || !errors.Is(err, ErrDiagnosticShutdown) {
		t.Fatal("diagnostic failure was not marked separately", err)
	}
	select {
	case <-delivered:
	default:
		t.Fatal("diagnostic failure interrupted business delivery")
	}
	if dropped, failed := bus.DiagnosticStats(); dropped != 0 || failed != 1 {
		t.Fatal(dropped, failed)
	}
}

func TestChanBusDiagnosticCapacityAndShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := &blockedDiagnosticError{entered: make(chan struct{}), release: make(chan struct{})}
		bus := NewChanBus(64)
		var delivered atomic.Int64
		if err := bus.On("audit", func(context.Context, any) error { delivered.Add(1); return failure }); err != nil {
			t.Fatal(err)
		}
		if err := bus.Emit(t.Context(), "audit", testEvent{UserID: 1}); err != nil {
			t.Fatal(err)
		}
		<-failure.entered
		const messages = 33
		for range messages - 1 {
			if err := bus.Emit(t.Context(), "audit", testEvent{UserID: 1}); err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		if dropped, failed := bus.DiagnosticStats(); delivered.Load() != messages || dropped != messages-17 || failed != 0 {
			t.Fatal("diagnostic pressure changed business delivery", delivered.Load(), dropped, failed)
		}
		first := bus.Close()
		if !errors.Is(first, context.DeadlineExceeded) || !errors.Is(first, ErrDiagnosticShutdown) {
			t.Fatal("blocked diagnostics hid shutdown failure", first)
		}
		close(failure.release)
		if err := bus.reports.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if next := bus.Close(); next != first {
			t.Fatal("output recovery changed the first shutdown result", next)
		}
	})
}
