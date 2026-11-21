package api

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

type hostileTaskPanic struct{}

func (hostileTaskPanic) Error() string  { panic("must not format panic payload") }
func (hostileTaskPanic) String() string { panic("must not format panic payload") }

func TestTaskGroupSuccessAndContext(t *testing.T) {
	type key struct{}
	parent := context.WithValue(t.Context(), key{}, int64(9007199254740993))
	group, ctx := NewTaskGroup(parent)
	if ctx.Value(key{}) != int64(9007199254740993) || ctx.Err() != nil {
		t.Fatal("parent values changed")
	}
	var count atomic.Int64
	for range 64 {
		group.Go(func() error { count.Add(1); return nil })
	}
	if err := group.Wait(); err != nil || count.Load() != 64 {
		t.Fatal(count.Load(), err)
	}
	if ctx.Err() != context.Canceled || parent.Err() != nil {
		t.Fatal("wrong cancellation boundary", ctx.Err(), parent.Err())
	}
	if err := group.Wait(); err != nil {
		t.Fatal("repeat wait changed result", err)
	}
}

func TestTaskGroupAbnormalExit(t *testing.T) {
	for _, sample := range []struct {
		name string
		task func() error
	}{
		{"string-panic", func() error { panic("private native panic") }},
		{"opaque-panic", func() error { panic(hostileTaskPanic{}) }},
		{"nil-panic", func() error { panic(nil) }},
		{"goexit", func() error { runtime.Goexit(); return nil }},
		{"nil-handler", nil},
	} {
		t.Run(sample.name, func(t *testing.T) {
			group, ctx := NewTaskGroup(t.Context())
			group.Go(sample.task)
			err := group.Wait()
			failure, ok := errors.AsType[*luxerrors.AppError](err)
			if !ok || failure.Code != 500 || !failure.Internal {
				t.Fatal("abnormal exit was not private failure", err)
			}
			if context.Cause(ctx) != err {
				t.Fatal("lost cancellation cause")
			}
		})
	}
}

func TestTaskGroupFirstErrorCancelsSibling(t *testing.T) {
	parent, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	group, ctx := NewTaskGroup(parent)
	ready, stopped := make(chan struct{}), make(chan struct{})
	group.Go(func() error { close(ready); <-ctx.Done(); close(stopped); return ctx.Err() })
	failure := errors.New("business failure")
	group.Go(func() error { <-ready; return failure })
	if err := group.Wait(); err != failure {
		t.Fatal("business error identity changed", err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("returned before sibling stopped")
	}
	if parent.Err() != nil || context.Cause(ctx) != failure {
		t.Fatal("lost cancellation isolation")
	}
}

func TestTaskGroupZeroAndEmpty(t *testing.T) {
	var zero TaskGroup
	zero.Go(func() error { return nil })
	if err := zero.Wait(); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("without context")
	var failed TaskGroup
	failed.Go(func() error { return failure })
	if err := failed.Wait(); err != failure {
		t.Fatal(err)
	}
	group, ctx := NewTaskGroup(t.Context())
	if err := group.Wait(); err != nil || ctx.Err() != context.Canceled {
		t.Fatal(err, ctx.Err())
	}
}

func TestTaskGroupAbnormalExitCancelsSibling(t *testing.T) {
	for _, sample := range []struct {
		name string
		fail func()
	}{
		{"panic", func() { panic("private task") }},
		{"goexit", runtime.Goexit},
	} {
		t.Run(sample.name, func(t *testing.T) {
			parent, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			group, ctx := NewTaskGroup(parent)
			ready, stopped := make(chan struct{}), make(chan struct{})
			group.Go(func() error { close(ready); <-ctx.Done(); close(stopped); return ctx.Err() })
			group.Go(func() error { <-ready; sample.fail(); return nil })
			err := group.Wait()
			failure, ok := errors.AsType[*luxerrors.AppError](err)
			if !ok || failure.Code != 500 || !failure.Internal || parent.Err() != nil {
				t.Fatal("abnormal task did not cancel sibling", err, parent.Err())
			}
			select {
			case <-stopped:
			default:
				t.Fatal("wait returned before sibling cleanup")
			}
		})
	}
}
