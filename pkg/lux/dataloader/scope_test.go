package dataloader

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/selection"
)

type scopeValueKey struct{}

func testScope(t testing.TB, ctx context.Context) *Scope {
	t.Helper()
	scope, err := NewScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func testBatchKey(t testing.TB, ctx context.Context) batchContextKey {
	t.Helper()
	scope, err := scopeForContext(ctx)
	if err != nil || scope == nil {
		t.Fatal("missing test scope", err)
	}
	return batchContextKey{scope: scope, done: ctx.Done()}
}

func TestBatchedLoadRequiresScope(t *testing.T) {
	var calls atomic.Int32
	loader := New(func(context.Context, []int, []*selection.Field) (map[int]int, error) {
		calls.Add(1)
		return map[int]int{42: 42}, nil
	}, DefaultConfig())
	if value, err := loader.Load(t.Context(), 42, nil); value != 0 || !errors.Is(err, ErrMissingScope) {
		t.Fatal("unscoped automatic batching was accepted", value, err)
	}
	if calls.Load() != 0 || len(loader.batches) != 0 {
		t.Fatal("unscoped load allocated a batch or reached the backend")
	}
}

func TestBatchedScopesIsolateSharedCancellation(t *testing.T) {
	for _, shared := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			var parent context.Context = context.Background()
			if shared {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				parent = ctx
			}
			first := testScope(t, context.WithValue(parent, scopeValueKey{}, "tenant-a"))
			second := testScope(t, context.WithValue(parent, scopeValueKey{}, "tenant-b"))
			if first.Done() != second.Done() {
				t.Fatal("fixture did not share its cancellation channel")
			}
			var calls atomic.Int32
			loader := New(func(ctx context.Context, _ []int, _ []*selection.Field) (map[int]string, error) {
				calls.Add(1)
				return map[int]string{42: ctx.Value(scopeValueKey{}).(string)}, nil
			}, Config{Wait: time.Second, MaxBatch: 2})
			results := make(chan string, 2)
			for _, scope := range []*Scope{first, second} {
				go func() {
					value, err := loader.Load(scope, 42, nil)
					if err != nil {
						t.Error(err)
					}
					results <- value
				}()
			}
			one, two := <-results, <-results
			if one == two || (one != "tenant-a" && one != "tenant-b") || (two != "tenant-a" && two != "tenant-b") || calls.Load() != 2 {
				t.Fatal("request values crossed scope boundaries", one, two, calls.Load())
			}
		})
	}
}

func TestScopeHasDistinctIdentityAndFixedValues(t *testing.T) {
	parent := context.WithValue(t.Context(), scopeValueKey{}, "tenant-a")
	first, second := testScope(t, parent), testScope(t, parent)
	if first == second {
		t.Fatal("independent requests shared a scope")
	}
	derived := context.WithValue(first, scopeValueKey{}, "tenant-b")
	found, err := scopeForContext(derived)
	if err != nil || found != first {
		t.Fatal("derived context lost its request identity", err)
	}
	fixed := contextForScope(derived, found)
	if fixed.Value(scopeValueKey{}) != "tenant-a" || fixed.Done() != derived.Done() {
		t.Fatal("derived values replaced the fixed request values or cancellation")
	}
	renewed := testScope(t, derived)
	if renewed == first || renewed.Value(scopeValueKey{}) != "tenant-b" {
		t.Fatal("an explicit new scope did not adopt changed values")
	}
	if direct := contextForScope(first, first); direct != first {
		t.Fatal("ordinary scope required an extra context wrapper")
	}
	if got, err := scopeForContext(first); err != nil || got != first {
		t.Fatal("direct scope lookup failed", err)
	}
}

func TestScopeCancellationCauseAndChildren(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scope := testScope(t, context.WithValue(context.Background(), scopeValueKey{}, "tenant-a"))
		derived, cancel := context.WithCancelCause(context.WithValue(scope, scopeValueKey{}, "tenant-b"))
		defer cancel(nil)
		fixed := contextForScope(derived, scope)
		child, stop := context.WithCancel(fixed)
		defer stop()
		if fixed.Value(scopeValueKey{}) != "tenant-a" || fixed.Value(scopeContextKey{}) != scope || context.Cause(fixed) != nil {
			t.Fatal("fixed values or active cancellation cause changed")
		}
		cause := errors.New("request branch failed")
		cancel(cause)
		<-child.Done()
		if fixed.Done() != derived.Done() || fixed.Err() != context.Canceled || context.Cause(fixed) != cause || context.Cause(child) != cause {
			t.Fatal("derived cancellation or cause was lost")
		}
		if scope.Err() != nil || context.Cause(scope) != nil {
			t.Fatal("canceling one branch canceled the whole request")
		}
	})
}

func TestScopeDeadlineAndAfterFunc(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scope := testScope(t, context.Background())
		deadline, cause := time.Now().Add(time.Second), errors.New("branch deadline")
		derived, cancel := context.WithDeadlineCause(scope, deadline, cause)
		defer cancel()
		fixed := contextForScope(derived, scope)
		if got, ok := fixed.Deadline(); !ok || !got.Equal(deadline) {
			t.Fatal("derived deadline changed")
		}
		notified := make(chan struct{})
		stop := context.AfterFunc(fixed, func() { close(notified) })
		defer stop()
		<-notified
		if fixed.Err() != context.DeadlineExceeded || context.Cause(fixed) != cause {
			t.Fatal("deadline or its cause changed")
		}
		if scope.Err() != nil {
			t.Fatal("derived deadline ended the request scope")
		}
	})
}

func TestScopeWithoutCancelPreservesDetachment(t *testing.T) {
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), scopeValueKey{}, "tenant-a"))
	defer cancel()
	scope := testScope(t, parent)
	derived := context.WithoutCancel(context.WithValue(scope, scopeValueKey{}, "tenant-b"))
	fixed := contextForScope(derived, scope)
	cancel()
	if fixed.Done() != nil || fixed.Err() != nil || context.Cause(fixed) != nil || fixed.Value(scopeValueKey{}) != "tenant-a" {
		t.Fatal("detached context inherited cancellation or changed values")
	}
	if _, ok := fixed.Deadline(); ok {
		t.Fatal("detached context inherited a deadline")
	}
}

func TestScopeRejectsInvalidContexts(t *testing.T) {
	if scope, err := NewScope(nil); scope != nil || !errors.Is(err, ErrInvalidScope) {
		t.Fatal("nil context was accepted", scope, err)
	}
	for _, ctx := range []context.Context{nil, (*Scope)(nil), &Scope{}} {
		if _, err := scopeForContext(ctx); !errors.Is(err, ErrInvalidScope) {
			t.Fatal("invalid scope was accepted", err)
		}
	}
	for _, invalid := range []*Scope{nil, {}} {
		wrapped, cancel := context.WithCancel(context.WithValue(invalid, scopeValueKey{}, "invalid"))
		if _, err := scopeForContext(wrapped); !errors.Is(err, ErrInvalidScope) {
			t.Fatal("wrapped invalid scope was accepted", err)
		}
		if _, err := NewScope(wrapped); !errors.Is(err, ErrInvalidScope) {
			t.Fatal("invalid scope was promoted by a new scope", err)
		}
		cancel()
	}
	if scope, err := scopeForContext(context.Background()); scope != nil || err != nil {
		t.Fatal("ordinary context became an invalid explicit scope", scope, err)
	}
	ctx := context.Background()
	if contextForScope(ctx, nil) != ctx {
		t.Fatal("unbatched ordinary context changed")
	}
	var invalid *Scope
	if invalid.Done() != nil || invalid.Err() != ErrInvalidScope || invalid.Value(scopeValueKey{}) != nil {
		t.Fatal("invalid scope methods did not fail safely")
	}
	if _, ok := invalid.Deadline(); ok {
		t.Fatal("invalid scope had a deadline")
	}
}

func TestScopeValuesAcrossDispatchModes(t *testing.T) {
	for _, mode := range []string{"immediate", "all", "batched"} {
		t.Run(mode, func(t *testing.T) {
			scope := testScope(t, context.WithValue(t.Context(), scopeValueKey{}, "tenant-a"))
			derived, cancel := context.WithCancel(context.WithValue(scope, scopeValueKey{}, "tenant-b"))
			defer cancel()
			cfg := Config{Wait: time.Millisecond, MaxBatch: 1}
			if mode == "batched" {
				cfg.MaxBatch = 2
			}
			loader := New(func(ctx context.Context, _ []int, _ []*selection.Field) (map[int]string, error) {
				if ctx.Value(scopeValueKey{}) != "tenant-a" || context.Cause(ctx) != nil {
					t.Error("dispatch changed fixed request values or cancellation")
				}
				return map[int]string{42: "ok"}, nil
			}, cfg)
			if mode == "all" {
				rows, err := loader.LoadAll(derived, []int{42}, nil)
				if err != nil || rows[42] != "ok" {
					t.Fatal(rows, err)
				}
			} else if row, err := loader.Load(derived, 42, nil); err != nil || row != "ok" {
				t.Fatal(row, err)
			}
			if context.Cause(derived) != nil || scope.Err() != nil {
				t.Fatal("completed dispatch canceled its caller")
			}
		})
	}
}

func TestScopeCancellationDomainsStayIndependent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scope := testScope(t, context.Background())
		branch, cancel := context.WithCancel(scope)
		defer cancel()
		calls := 0
		loader := New(func(context.Context, []int, []*selection.Field) (map[int]int, error) {
			calls++
			return map[int]int{42: 42}, nil
		}, Config{Wait: time.Second, MaxBatch: 2})
		results := make(chan error, 1)
		go func() { _, err := loader.Load(branch, 42, nil); results <- err }()
		synctest.Wait()
		cancel()
		if err := <-results; !errors.Is(err, context.Canceled) {
			t.Fatal("branch cancellation was lost", err)
		}
		if row, err := loader.Load(scope, 42, nil); err != nil || row != 42 || calls != 1 {
			t.Fatal("canceled branch affected its sibling", row, err, calls)
		}
	})
}

func TestScopeLegacyAndInvalidDispatch(t *testing.T) {
	for _, max := range []int{1, 2} {
		loader := New(func(context.Context, []int, []*selection.Field) (map[int]int, error) {
			return map[int]int{42: 42}, nil
		}, Config{MaxBatch: max})
		if max == 1 {
			if row, err := loader.Load(t.Context(), 42, nil); err != nil || row != 42 {
				t.Fatal("unbatched legacy context rejected", row, err)
			}
		}
		if rows, err := loader.LoadAll(t.Context(), []int{42}, nil); err != nil || rows[42] != 42 {
			t.Fatal("explicit batch rejected legacy context", rows, err)
		}
		for _, ctx := range []context.Context{nil, (*Scope)(nil), &Scope{}} {
			if row, err := loader.Load(ctx, 42, nil); row != 0 || !errors.Is(err, ErrInvalidScope) {
				t.Fatal("invalid load context accepted", row, err)
			}
			if rows, err := loader.LoadAll(ctx, []int{42}, nil); rows != nil || !errors.Is(err, ErrInvalidScope) {
				t.Fatal("invalid batch context accepted", rows, err)
			}
		}
	}
}

func TestLoadAllPreservesDirectReadBoundary(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		loader := New(func(ctx context.Context, keys []int, _ []*selection.Field) (map[int]int, error) {
			deadline, ok := ctx.Deadline()
			if !ok || !deadline.Equal(started.Add(30*time.Second)) {
				t.Fatal("direct read lost the existing backend deadline", deadline)
			}
			return map[int]int{keys[0]: 42}, nil
		}, Config{Wait: time.Hour, MaxBatch: 100})
		if values, err := loader.LoadAll(context.Background(), []int{1}, nil); err != nil || values[1] != 42 || !time.Now().Equal(started) {
			t.Fatal("direct read required a scope or entered the batch window", values, err)
		}
		if len(loader.batches) != 0 {
			t.Fatal("direct read registered automatic batch state")
		}
	})
}

func TestLoadAllBackendTimeout(t *testing.T) {
	for _, earlier := range []time.Duration{0, 5 * time.Millisecond} {
		synctest.Test(t, func(t *testing.T) {
			loader := New(func(ctx context.Context, _ []int, _ []*selection.Field) (map[int]int, error) {
				<-ctx.Done()
				return map[int]int{1: 42}, nil
			}, DefaultConfig())
			started := time.Now()
			var ctx context.Context = context.Background()
			expected := 30 * time.Second
			if earlier > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, earlier)
				defer cancel()
				expected = earlier
			}
			if values, err := loader.LoadAll(ctx, []int{1}, nil); values != nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(started) != expected {
				t.Fatal("backend timeout became success", values, err, time.Since(started))
			}
			if earlier == 0 && ctx.Err() != nil {
				t.Fatal("backend cap canceled its caller")
			}
		})
	}
}

func TestBatchContextOutlivesResultSnapshot(t *testing.T) {
	var backend context.Context
	loader := New(func(ctx context.Context, _ []int, _ []*selection.Field) (map[int]int, error) {
		backend = ctx
		return map[int]int{1: 42}, nil
	}, DefaultConfig())
	ctx, cancel := batchReadContext(t.Context())
	defer cancel()
	values, err := loader.invokeBatchForCaller(t.Context(), ctx, []int{1}, nil)
	if err != nil || backend == nil || backend.Err() != nil {
		t.Fatal("backend context ended before its caller could consume the result", err)
	}
	if values[1] != 42 {
		t.Fatal("snapshot changed")
	}
	cancel()
	if backend.Err() != context.Canceled {
		t.Fatal("result owner did not release the backend context")
	}
}
