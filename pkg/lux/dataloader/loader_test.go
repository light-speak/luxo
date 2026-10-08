package dataloader

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/selection"
)

func TestLoadSingle(t *testing.T) {
	var calls int32
	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		atomic.AddInt32(&calls, 1)
		result := make(map[int]string)
		for _, k := range keys {
			result[k] = fmt.Sprintf("user_%d", k)
		}
		return result, nil
	}, DefaultConfig())

	val, err := loader.Load(context.Background(), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if val != "user_1" {
		t.Errorf("got %q", val)
	}
}

func TestLoadPreservesCallerContext(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		return map[int]string{keys[0]: ctx.Value(key).(string)}, nil
	}, DefaultConfig())

	value, err := loader.Load(context.WithValue(context.Background(), key, "trace-1"), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if value != "trace-1" {
		t.Fatalf("batch context value = %q", value)
	}
}

func TestLoadSeparatesConcurrentRequestScopes(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	var calls atomic.Int32
	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		calls.Add(1)
		result := make(map[int]string, len(keys))
		for _, item := range keys {
			result[item] = ctx.Value(key).(string)
		}
		return result, nil
	}, Config{Wait: 10 * time.Millisecond, MaxBatch: 100})

	firstBase, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	secondBase, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	contexts := []context.Context{
		context.WithValue(firstBase, key, "first"),
		context.WithValue(secondBase, key, "second"),
	}
	want := []string{"first", "second"}
	got := make([]string, len(contexts))
	var wg sync.WaitGroup
	for i := range contexts {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			got[index], _ = loader.Load(contexts[index], index+1, nil)
		}(i)
	}
	wg.Wait()

	if calls.Load() != 2 {
		t.Fatalf("batch calls = %d, want 2 isolated request scopes", calls.Load())
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("result %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLoadBatching(t *testing.T) {
	var calls int32
	var batchSize int32

	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		atomic.AddInt32(&calls, 1)
		atomic.StoreInt32(&batchSize, int32(len(keys)))
		result := make(map[int]string)
		for _, k := range keys {
			result[k] = fmt.Sprintf("user_%d", k)
		}
		return result, nil
	}, Config{Wait: 10 * time.Millisecond, MaxBatch: 100})

	var wg sync.WaitGroup
	results := make([]string, 3)

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			v, _ := loader.Load(context.Background(), idx+1, nil)
			results[idx] = v
		}(i)
	}
	wg.Wait()

	if results[0] != "user_1" || results[1] != "user_2" || results[2] != "user_3" {
		t.Errorf("results = %v", results)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("expected 1 batch call, got %d", calls)
	}
	if atomic.LoadInt32(&batchSize) != 3 {
		t.Errorf("expected batch size 3, got %d", batchSize)
	}
}

func TestLoadFieldsMerge(t *testing.T) {
	var gotFields []*selection.Field

	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		gotFields = fields
		result := make(map[int]string)
		for _, k := range keys {
			result[k] = "ok"
		}
		return result, nil
	}, Config{Wait: 10 * time.Millisecond, MaxBatch: 100})

	var wg sync.WaitGroup
	// Two goroutines request different fields
	wg.Add(2)
	go func() {
		defer wg.Done()
		loader.Load(context.Background(), 1, []*selection.Field{{Name: "project", Children: []*selection.Field{{Name: "id"}}}})
	}()
	go func() {
		defer wg.Done()
		loader.Load(context.Background(), 2, []*selection.Field{{Name: "project", Children: []*selection.Field{{Name: "name"}}}, {Name: "title"}})
	}()
	wg.Wait()

	// Nested selections must be merged rather than flattened or overwritten.
	if len(gotFields) != 2 {
		t.Fatalf("expected 2 merged fields, got %d: %v", len(gotFields), gotFields)
	}
	if got := selection.Format(gotFields); got != "project{id,name},title" && got != "project{name,id},title" {
		t.Errorf("merged fields = %q", got)
	}
}

func TestSameSelection(t *testing.T) {
	left := []*selection.Field{{Name: "project", Children: []*selection.Field{{Name: "id"}, {Name: "name"}}}}
	right := []*selection.Field{{Name: "project", Children: []*selection.Field{{Name: "id"}, {Name: "name"}}}}
	if !sameSelection(left, right) {
		t.Fatal("identical nested selections must compare equal")
	}
	right[0].Children[1].Name = "owner"
	if sameSelection(left, right) {
		t.Fatal("different nested selections must not compare equal")
	}
	if sameSelection(left, nil) || !sameSelection(nil, nil) {
		t.Fatal("selection nil handling is incorrect")
	}
	withNil := []*selection.Field{nil}
	if !sameSelection(withNil, []*selection.Field{nil}) || sameSelection(withNil, []*selection.Field{{Name: "id"}}) {
		t.Fatal("nil selection node handling is incorrect")
	}
}

func TestBatchSelectionBecomesAllAfterPartial(t *testing.T) {
	partial := []*selection.Field{{Name: "id"}}
	batch := newBatch[int, string](t.Context())
	batch.add(1, partial)
	batch.add(2, nil)
	batch.add(3, partial)
	if batch.mergedSelection() != nil || !batch.selectsAll || batch.size() != 3 {
		t.Fatal("internal full selection did not dominate earlier and later projections")
	}
	if len(partial) != 1 || partial[0].Name != "id" {
		t.Fatal("merging mutated caller selection metadata")
	}
}

func TestExpiredBatchTimerCannotFlushReplacement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		loader := New(func(_ context.Context, keys []int, _ []*selection.Field) (map[int]int, error) {
			calls.Add(1)
			values := make(map[int]int, len(keys))
			for _, key := range keys {
				values[key] = key
			}
			return values, nil
		}, Config{Wait: time.Second, MaxBatch: 2})
		results := make(chan int, 3)
		load := func(key int) {
			value, err := loader.Load(t.Context(), key, nil)
			if err != nil {
				t.Errorf("load %d: %v", key, err)
			}
			results <- value
		}
		go load(1)
		synctest.Wait()
		go load(2)
		synctest.Wait()
		first, second := <-results, <-results
		if first+second != 3 || first == second || calls.Load() != 1 {
			t.Fatal("full batch was not dispatched exactly once")
		}
		time.Sleep(500 * time.Millisecond)
		go load(3)
		synctest.Wait()
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		loader.mu.Lock()
		pending := len(loader.batches)
		loader.mu.Unlock()
		if calls.Load() != 1 || len(results) != 0 || pending != 1 {
			t.Fatal("old timer dispatched or removed the replacement batch")
		}
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		if <-results != 3 || calls.Load() != 2 || len(loader.batches) != 0 {
			t.Fatal("replacement batch lost its own timer or remained registered")
		}
	})
}

func TestLoadNoFields(t *testing.T) {
	var gotFields []*selection.Field

	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		gotFields = fields
		return map[int]string{1: "ok"}, nil
	}, DefaultConfig())

	loader.Load(context.Background(), 1, nil)

	if gotFields != nil {
		t.Errorf("nil fields should pass through as nil, got %v", gotFields)
	}
}

func TestLoadSelectAllDominatesPartialSelection(t *testing.T) {
	var gotFields []*selection.Field
	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		gotFields = fields
		return map[int]string{1: "one", 2: "two"}, nil
	}, Config{Wait: 10 * time.Millisecond, MaxBatch: 100})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = loader.Load(context.Background(), 1, nil)
	}()
	go func() {
		defer wg.Done()
		_, _ = loader.Load(context.Background(), 2, []*selection.Field{{Name: "name"}})
	}()
	wg.Wait()
	if gotFields != nil {
		t.Fatalf("select-all must dominate partial selection: %s", selection.Format(gotFields))
	}
}

func TestLoadMaxBatch(t *testing.T) {
	var calls int32

	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		atomic.AddInt32(&calls, 1)
		result := make(map[int]string)
		for _, k := range keys {
			result[k] = "ok"
		}
		return result, nil
	}, Config{Wait: 100 * time.Millisecond, MaxBatch: 2})

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			loader.Load(context.Background(), k, nil)
		}(i)
	}
	wg.Wait()

	// With maxBatch=2 and 4 keys, should dispatch at least 2 batches
	c := atomic.LoadInt32(&calls)
	if c < 2 {
		t.Errorf("expected at least 2 batch calls, got %d", c)
	}
}

func TestLoadError(t *testing.T) {
	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		return nil, fmt.Errorf("db error")
	}, DefaultConfig())

	_, err := loader.Load(context.Background(), 1, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "db error" {
		t.Errorf("error = %v", err)
	}
}

func TestLoadMissingKey(t *testing.T) {
	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		return map[int]string{}, nil // return empty — key not found
	}, DefaultConfig())

	val, err := loader.Load(context.Background(), 999, nil)
	if err != nil {
		t.Fatal(err)
	}
	if val != "" { // zero value for string
		t.Errorf("missing key should return zero value, got %q", val)
	}
}

func TestLoadAll(t *testing.T) {
	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		result := make(map[int]string)
		for _, k := range keys {
			result[k] = fmt.Sprintf("v%d", k)
		}
		return result, nil
	}, DefaultConfig())

	results, err := loader.LoadAll(context.Background(), []int{1, 2, 3}, []*selection.Field{{Name: "name"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[1] != "v1" || results[2] != "v2" || results[3] != "v3" {
		t.Errorf("results = %v", results)
	}
}

func TestLoadAllEmpty(t *testing.T) {
	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		t.Fatal("should not be called for empty keys")
		return nil, nil
	}, DefaultConfig())

	results, err := loader.LoadAll(context.Background(), []int{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if results != nil {
		t.Errorf("expected nil for empty keys, got %v", results)
	}
}

func TestLoadAllError(t *testing.T) {
	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		return nil, fmt.Errorf("batch error")
	}, DefaultConfig())

	_, err := loader.LoadAll(context.Background(), []int{1, 2}, nil)
	if err == nil || err.Error() != "batch error" {
		t.Errorf("expected batch error, got %v", err)
	}
}

func TestLoadDedupKeys(t *testing.T) {
	var gotKeys []int

	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		gotKeys = keys
		result := make(map[int]string)
		for _, k := range keys {
			result[k] = "ok"
		}
		return result, nil
	}, Config{Wait: 10 * time.Millisecond, MaxBatch: 100})

	var wg sync.WaitGroup
	// Same key from multiple goroutines
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			loader.Load(context.Background(), 42, nil)
		}()
	}
	wg.Wait()

	// Keys should be deduplicated
	if len(gotKeys) != 1 || gotKeys[0] != 42 {
		t.Errorf("keys should be deduped, got %v", gotKeys)
	}
}

func TestLoadContextCancel(t *testing.T) {
	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		time.Sleep(50 * time.Millisecond) // slow query
		return map[int]string{1: "ok"}, nil
	}, Config{Wait: 5 * time.Millisecond, MaxBatch: 100})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := loader.Load(ctx, 1, nil)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestLoadContextCancelDoesNotKillOthers(t *testing.T) {
	var calls int32

	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		atomic.AddInt32(&calls, 1)
		result := make(map[int]string)
		for _, k := range keys {
			result[k] = fmt.Sprintf("v%d", k)
		}
		return result, nil
	}, Config{Wait: 10 * time.Millisecond, MaxBatch: 100})

	cancelCtx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	var cancelErr, normalErr error
	var normalVal string

	// Request 1: will be cancelled
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, cancelErr = loader.Load(cancelCtx, 1, nil)
	}()

	// Request 2: normal, should succeed
	wg.Add(1)
	go func() {
		defer wg.Done()
		normalVal, normalErr = loader.Load(context.Background(), 2, nil)
	}()

	// Cancel request 1 after a tiny delay
	time.Sleep(1 * time.Millisecond)
	cancel()

	wg.Wait()

	if cancelErr != context.Canceled {
		t.Errorf("cancelled request should get context.Canceled, got %v", cancelErr)
	}
	if normalErr != nil {
		t.Errorf("normal request should succeed, got %v", normalErr)
	}
	if normalVal != "v2" {
		t.Errorf("normal request value = %q, want v2", normalVal)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Wait != 1*time.Millisecond {
		t.Errorf("default wait = %v", cfg.Wait)
	}
	if cfg.MaxBatch != 100 {
		t.Errorf("default maxBatch = %d", cfg.MaxBatch)
	}
}

func TestNewZeroWaitUsesDefault(t *testing.T) {
	// cfg.Wait <= 0 should fall back to default 2ms
	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		return map[int]string{1: "ok"}, nil
	}, Config{Wait: 0, MaxBatch: 10})

	if loader.wait != 1*time.Millisecond {
		t.Errorf("zero wait should default to 2ms, got %v", loader.wait)
	}
}

func TestNewNegativeWaitUsesDefault(t *testing.T) {
	loader := New(func(ctx context.Context, keys []int, fields []*selection.Field) (map[int]string, error) {
		return map[int]string{1: "ok"}, nil
	}, Config{Wait: -5 * time.Millisecond, MaxBatch: 10})

	if loader.wait != 1*time.Millisecond {
		t.Errorf("negative wait should default to 2ms, got %v", loader.wait)
	}
}

func TestBatchStateIsAllocatedOnlyForLoad(t *testing.T) {
	loader := New(func(_ context.Context, keys []int, _ []*selection.Field) (map[int]string, error) {
		return map[int]string{keys[0]: "ok"}, nil
	}, Config{Wait: time.Microsecond, MaxBatch: 2})
	if loader.batches != nil {
		t.Fatal("unused loader allocated batch state")
	}
	values, err := loader.LoadAll(t.Context(), []int{1}, nil)
	if err != nil || values[1] != "ok" || loader.batches != nil {
		t.Fatalf("direct load initialized batching: values=%v err=%v", values, err)
	}
	for range 2 {
		value, err := loader.Load(t.Context(), 1, nil)
		if err != nil || value != "ok" {
			t.Fatalf("first/reused batch: value=%s err=%v", value, err)
		}
	}
	loader.mu.Lock()
	defer loader.mu.Unlock()
	if loader.batches == nil || len(loader.batches) != 0 {
		t.Fatal("batch state was not initialized or retained a completed batch")
	}
}

func TestImmediateLoadDoesNotCreateBatch(t *testing.T) {
	fields := []*selection.Field{{Name: "user", Children: []*selection.Field{{Name: "id"}}}}
	loader := New(func(ctx context.Context, keys []int, selected []*selection.Field) (map[int]string, error) {
		if len(keys) != 1 || keys[0] != 7 || &selected[0] != &fields[0] {
			t.Fatal("immediate dispatch changed keys or field selection")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
			t.Fatal("immediate dispatch lost its timeout")
		}
		return map[int]string{7: "value"}, nil
	}, Config{Wait: time.Hour, MaxBatch: 1})
	value, err := loader.Load(t.Context(), 7, fields)
	if err != nil || value != "value" || loader.batches != nil {
		t.Fatalf("immediate load used batch state: value=%s err=%v", value, err)
	}
}

func TestImmediateLoadErrors(t *testing.T) {
	failure := fmt.Errorf("batch failed")
	for _, scenario := range []string{"missing", "error", "cancel", "deadline"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "deadline" {
				var release context.CancelFunc
				ctx, release = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer release()
			}
			loader := New(func(context.Context, []int, []*selection.Field) (map[int]string, error) {
				switch scenario {
				case "error":
					return map[int]string{7: "not a success"}, failure
				case "cancel":
					cancel()
				}
				return nil, nil
			}, Config{MaxBatch: 1})
			want := ctx.Err()
			if scenario == "error" {
				want = failure
			}
			if scenario == "cancel" {
				want = context.Canceled
			}
			value, err := loader.Load(ctx, 7, nil)
			if err != want || value != "" {
				t.Fatalf("got %q/%v, want zero/%v", value, err, want)
			}
		})
	}
}
