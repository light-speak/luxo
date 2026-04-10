package dataloader

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadSingle(t *testing.T) {
	var calls int32
	loader := New(func(ctx context.Context, keys []int, fields []string) (map[int]string, error) {
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

func TestLoadBatching(t *testing.T) {
	var calls int32
	var batchSize int32

	loader := New(func(ctx context.Context, keys []int, fields []string) (map[int]string, error) {
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
	var gotFields []string

	loader := New(func(ctx context.Context, keys []int, fields []string) (map[int]string, error) {
		sort.Strings(fields)
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
		loader.Load(context.Background(), 1, []string{"title"})
	}()
	go func() {
		defer wg.Done()
		loader.Load(context.Background(), 2, []string{"title", "content"})
	}()
	wg.Wait()

	// Fields should be union: [content, title]
	if len(gotFields) != 2 {
		t.Fatalf("expected 2 merged fields, got %d: %v", len(gotFields), gotFields)
	}
	sort.Strings(gotFields)
	if gotFields[0] != "content" || gotFields[1] != "title" {
		t.Errorf("merged fields = %v, want [content, title]", gotFields)
	}
}

func TestLoadNoFields(t *testing.T) {
	var gotFields []string

	loader := New(func(ctx context.Context, keys []int, fields []string) (map[int]string, error) {
		gotFields = fields
		return map[int]string{1: "ok"}, nil
	}, DefaultConfig())

	loader.Load(context.Background(), 1, nil)

	if gotFields != nil {
		t.Errorf("nil fields should pass through as nil, got %v", gotFields)
	}
}

func TestLoadMaxBatch(t *testing.T) {
	var calls int32

	loader := New(func(ctx context.Context, keys []int, fields []string) (map[int]string, error) {
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
	loader := New(func(ctx context.Context, keys []int, fields []string) (map[int]string, error) {
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
	loader := New(func(ctx context.Context, keys []int, fields []string) (map[int]string, error) {
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

func TestLoadMany(t *testing.T) {
	var calls int32

	loader := New(func(ctx context.Context, keys []int, fields []string) (map[int]string, error) {
		atomic.AddInt32(&calls, 1)
		result := make(map[int]string)
		for _, k := range keys {
			result[k] = fmt.Sprintf("v%d", k)
		}
		return result, nil
	}, Config{Wait: 10 * time.Millisecond, MaxBatch: 100})

	results, err := loader.LoadMany(context.Background(), []int{1, 2, 3}, []string{"name"})
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

func TestLoadDedupKeys(t *testing.T) {
	var gotKeys []int

	loader := New(func(ctx context.Context, keys []int, fields []string) (map[int]string, error) {
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

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Wait != 2*time.Millisecond {
		t.Errorf("default wait = %v", cfg.Wait)
	}
	if cfg.MaxBatch != 100 {
		t.Errorf("default maxBatch = %d", cfg.MaxBatch)
	}
}
