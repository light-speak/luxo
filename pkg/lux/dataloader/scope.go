package dataloader

import (
	"context"
	"errors"
	"time"
)

// ErrInvalidScope rejects nil contexts and invalid explicit scopes.
var ErrInvalidScope = errors.New("dataloader: request scope requires a non-nil context")

// ErrMissingScope rejects automatic batching without a request identity.
var ErrMissingScope = errors.New("dataloader: batched Load requires an explicit request scope")

type scopeContextKey struct{}

// Scope fixes the value context and identity shared by one request's batches.
// Create a new scope when identity or data routing changes. The zero value is invalid.
type Scope struct{ parent context.Context }

// NewScope fixes the request's value context under a fresh batch identity.
// Values must be immutable; establish another scope after identity/routing changes.
func NewScope(ctx context.Context) (*Scope, error) {
	if _, err := scopeForContext(ctx); err != nil {
		return nil, err
	}
	return &Scope{parent: ctx}, nil
}

func (s *Scope) Deadline() (time.Time, bool) {
	if s == nil || s.parent == nil {
		return time.Time{}, false
	}
	return s.parent.Deadline()
}

func (s *Scope) Done() <-chan struct{} {
	if s == nil || s.parent == nil {
		return nil
	}
	return s.parent.Done()
}

func (s *Scope) Err() error {
	if s == nil || s.parent == nil {
		return ErrInvalidScope
	}
	return s.parent.Err()
}

func (s *Scope) Value(key any) any {
	if _, ok := key.(scopeContextKey); ok {
		// Keep invalid scope identity visible through ordinary context wrappers.
		return s
	}
	if s == nil || s.parent == nil {
		return nil
	}
	return s.parent.Value(key)
}

func scopeForContext(ctx context.Context) (*Scope, error) {
	if ctx == nil {
		return nil, ErrInvalidScope
	}
	var scope *Scope
	var ok bool
	if scope, ok = ctx.(*Scope); !ok {
		if scope, ok = ctx.Value(scopeContextKey{}).(*Scope); !ok {
			return nil, nil
		}
	}
	if scope == nil || scope.parent == nil {
		return nil, ErrInvalidScope
	}
	return scope, nil
}

// Ordinary scopes need no wrapper. Derived cancellation domains keep their own
// deadline/cause while callback values continue to come from the fixed scope.
func contextForScope(ctx context.Context, scope *Scope) context.Context {
	if scope == nil {
		return ctx
	}
	if direct, ok := ctx.(*Scope); ok && direct == scope {
		return scope
	}
	if ctx.Done() == scope.Done() {
		deadline, hasDeadline := ctx.Deadline()
		scopeDeadline, scopeHasDeadline := scope.Deadline()
		if hasDeadline == scopeHasDeadline && (!hasDeadline || deadline.Equal(scopeDeadline)) {
			return scope
		}
	}
	values, cancel := context.WithCancelCause(context.WithoutCancel(scope))
	return &scopedContext{Context: ctx, scope: scope, values: values, cancel: cancel}
}

type scopedContext struct {
	context.Context
	scope  *Scope
	values context.Context
	cancel context.CancelCauseFunc
}

func (ctx *scopedContext) Value(key any) any {
	if _, ok := key.(scopeContextKey); ok {
		return ctx.scope
	}
	// A plain value override would hide context's private cancellation identity
	// and break Cause. Keep a standard cause context, synchronized on observation.
	if ctx.Context.Err() != nil {
		ctx.cancel(context.Cause(ctx.Context))
	}
	return ctx.values.Value(key)
}

// Forward cancellation registration without creating a watcher goroutine.
func (ctx *scopedContext) AfterFunc(f func()) func() bool {
	return context.AfterFunc(ctx.Context, f)
}
