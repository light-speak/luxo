package api

import (
	"context"
	stderrors "errors"
	"sync"

	"github.com/light-speak/luxo/internal/panicerror"
	"github.com/light-speak/luxo/pkg/lux/errors"
)

var errTaskDidNotReturn = stderrors.New("api: parallel task exited without returning")
var errTaskHandlerMissing = stderrors.New("api: parallel task handler is nil")

// TaskGroup waits for accepted tasks, preserving the first failure. Panic and
// Goexit become private failures before completion is published to Wait.
// The zero value works without a derived cancellation context. Do not copy or
// reuse a group after Wait; register root tasks before calling Wait.
type TaskGroup struct {
	cancel  context.CancelCauseFunc
	pending sync.WaitGroup
	failed  sync.Once
	err     error
}

// NewTaskGroup derives a context canceled by the first task failure or Wait.
// As with context.WithCancelCause, parent must not be nil.
func NewTaskGroup(parent context.Context) (*TaskGroup, context.Context) {
	ctx, cancel := context.WithCancelCause(parent)
	return &TaskGroup{cancel: cancel}, ctx
}

// Go starts one task goroutine. Tasks must cooperate with context cancellation;
// this group cannot forcibly interrupt arbitrary native code.
func (g *TaskGroup) Go(task func() error) {
	if task == nil {
		g.fail(errors.InternalError.WithCause(errTaskHandlerMissing))
		return
	}
	g.pending.Add(1)
	go func() {
		returned := false
		defer func() {
			if !returned {
				cause := errTaskDidNotReturn
				if value := recover(); value != nil {
					cause = panicerror.New("panic: ", value)
				}
				g.fail(errors.InternalError.WithCause(cause))
			}
			g.pending.Done()
		}()
		err := task()
		returned = true
		if err != nil {
			g.fail(err)
		}
	}()
}

func (g *TaskGroup) fail(err error) {
	g.failed.Do(func() {
		g.err = err
		if g.cancel != nil {
			g.cancel(err)
		}
	})
}

// Wait returns after all accepted tasks, including abnormal exits, have stopped.
func (g *TaskGroup) Wait() error {
	g.pending.Wait()
	if g.cancel != nil {
		g.cancel(g.err)
	}
	return g.err
}
