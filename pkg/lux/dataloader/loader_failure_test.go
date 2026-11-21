package dataloader

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/selection"
)

func TestLoaderCallbackPanicBoundary(t *testing.T) {
	if scenario := os.Getenv("LUXO_DATALOADER_FAILURE_PROBE"); scenario != "" {
		runCallbackPanicProbe(t, scenario)
		return
	}
	for _, mode := range []string{"immediate", "all", "timer", "threshold"} {
		for _, payload := range []string{"string", "value", "nil"} {
			t.Run(mode+"/"+payload, func(t *testing.T) {
				var requestScope context.Context = testScope(t, t.Context())

				ctx, cancel := context.WithTimeout(requestScope, 5*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLoaderCallbackPanicBoundary$", "-test.v")
				command.Env = append(os.Environ(), "LUXO_DATALOADER_FAILURE_PROBE="+mode+"/"+payload)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("callback escaped %s boundary: %v\n%s", mode, err, output)
				}
			})
		}
	}
}

type unsafeCallbackPanic struct{ formatted *atomic.Bool }

func (value unsafeCallbackPanic) Error() string {
	value.formatted.Store(true)
	runtime.Goexit()
	return "unreachable"
}

func runCallbackPanicProbe(t *testing.T, scenario string) {
	var requestScope context.Context = testScope(t, t.Context())

	t.Helper()
	parts := strings.Split(scenario, "/")
	if len(parts) != 2 {
		t.Fatal("invalid callback panic probe")
	}
	var formatted atomic.Bool
	var calls atomic.Int32
	var fetch BatchFn[int, string] = func(context.Context, []int, []*selection.Field) (map[int]string, error) {
		calls.Add(1)
		if parts[1] == "value" {
			panic(unsafeCallbackPanic{formatted: &formatted})
		}
		panic("callback failure")
	}
	if parts[1] == "nil" {
		fetch = nil
	}
	loader := New(fetch, DefaultConfig())
	load := func() error { _, err := loader.Load(requestScope, 42, nil); return err }
	switch parts[0] {
	case "immediate":
		loader.maxBatch = 1
	case "all":
		load = func() error { _, err := loader.LoadAll(requestScope, []int{42}, nil); return err }
	case "threshold":
		loader.wait, loader.maxBatch = time.Hour, 2
		load = func() error {
			results := make(chan error, 2)
			for range 2 {
				go func() { _, err := loader.Load(requestScope, 42, nil); results <- err }()
			}
			first, second := <-results, <-results
			if first == nil || second == nil {
				t.Fatal("failed callback returned a successful waiter")
			}
			return errors.Join(first, second)
		}
	case "timer":
	default:
		t.Fatal("unknown callback panic probe")
	}
	err := load()
	wantCalls := int32(1)
	if parts[1] == "nil" {
		wantCalls = 0
		if err != nil && !strings.Contains(err.Error(), "batch function is nil") {
			t.Fatalf("missing callback error was not explicit: %v", err)
		}
	}
	if err == nil || formatted.Load() || calls.Load() != wantCalls {
		t.Fatalf("callback boundary: err=%v formatted=%v calls=%d want=%d", err, formatted.Load(), calls.Load(), wantCalls)
	}
}

func TestTimerBatchCompletesAfterCallbackGoexit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var requestScope context.Context = testScope(t, t.Context())

		var calls atomic.Int32
		loader := New(func(context.Context, []int, []*selection.Field) (map[int]string, error) {
			calls.Add(1)
			runtime.Goexit()
			return nil, nil
		}, DefaultConfig())
		ctx, cancel := context.WithTimeout(requestScope, time.Second)
		defer cancel()
		results := make(chan error, 2)
		for range 2 {
			go func() { _, err := loader.Load(ctx, 42, nil); results <- err }()
		}
		for range 2 {
			if err := <-results; err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("terminated callback did not complete its batch: %v", err)
			}
		}
		if calls.Load() != 1 {
			t.Fatalf("callback was not executed once: %d", calls.Load())
		}
	})
}

func TestDispatchedBatchStopsItsWindowTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var requestScope context.Context = testScope(t, t.Context())

		loader := New(func(_ context.Context, keys []int, _ []*selection.Field) (map[int]int, error) {
			result := make(map[int]int, len(keys))
			for _, key := range keys {
				result[key] = key
			}
			return result, nil
		}, Config{Wait: time.Hour, MaxBatch: 2})
		first := make(chan error, 1)
		go func() { _, err := loader.Load(requestScope, 1, nil); first <- err }()
		synctest.Wait()
		loader.mu.Lock()
		pending := loader.batches[testBatchKey(t, requestScope)]
		loader.mu.Unlock()
		if pending == nil {
			t.Fatal("first load was not queued")
		}
		if _, err := loader.Load(requestScope, 2, nil); err != nil {
			t.Fatal(err)
		}
		if err := <-first; err != nil {
			t.Fatal(err)
		}
		if pending.timer.Stop() {
			t.Fatal("completed batch retained an active hour-long timer")
		}
	})
}

func TestThresholdBatchCompletesPeersAfterCallbackGoexit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var requestScope context.Context = testScope(t, t.Context())

		var calls atomic.Int32
		loader := New(func(context.Context, []int, []*selection.Field) (map[int]string, error) {
			calls.Add(1)
			runtime.Goexit()
			return nil, nil
		}, Config{Wait: time.Hour, MaxBatch: 2})
		ctx, cancel := context.WithTimeout(requestScope, time.Second)
		defer cancel()
		exited, results := make(chan struct{}, 2), make(chan error, 2)
		for range 2 {
			go func() {
				defer func() { exited <- struct{}{} }()
				_, err := loader.Load(ctx, 42, nil)
				results <- err
			}()
		}
		for range 2 {
			<-exited
		}
		if calls.Load() != 1 || len(results) != 2 {
			t.Fatalf("callback exit changed: calls=%d returned=%d", calls.Load(), len(results))
		}
		for range 2 {
			if err := <-results; err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("callback exit left a caller waiting: %v", err)
			}
		}
	})
}

func TestLoaderContinuesAfterCallbackPanic(t *testing.T) {
	var requestScope context.Context = testScope(t, t.Context())

	var calls atomic.Int32
	loader := New(func(context.Context, []int, []*selection.Field) (map[int]string, error) {
		if calls.Add(1) == 1 {
			panic("first callback failed")
		}
		return map[int]string{42: "ok"}, nil
	}, DefaultConfig())
	if value, err := loader.Load(requestScope, 42, nil); err == nil || value != "" {
		t.Fatalf("callback panic became successful data: %q/%v", value, err)
	}
	if value, err := loader.Load(requestScope, 42, nil); err != nil || value != "ok" {
		t.Fatalf("callback panic poisoned the next batch: %q/%v", value, err)
	}
}

func TestNilCallbackReturnsExplicitError(t *testing.T) {
	var requestScope context.Context = testScope(t, t.Context())

	loader := New[int, string](nil, Config{MaxBatch: 1})
	if value, err := loader.Load(requestScope, 42, nil); value != "" || !errors.Is(err, errNilBatchFunction) {
		t.Fatalf("missing immediate callback: %q/%v", value, err)
	}
	if values, err := loader.LoadAll(requestScope, []int{42}, nil); values != nil || !errors.Is(err, errNilBatchFunction) {
		t.Fatalf("missing list callback: %v/%v", values, err)
	}
}

func TestCanceledBatchCallsSkipOrDiscardCallback(t *testing.T) {
	for _, operation := range []string{"immediate", "all"} {
		for _, when := range []string{"before", "after"} {
			t.Run(operation+"/"+when, func(t *testing.T) {
				var requestScope context.Context = testScope(t, t.Context())

				ctx, cancel := context.WithCancel(requestScope)
				defer cancel()
				if when == "before" {
					cancel()
				}
				calls := 0
				loader := New(func(context.Context, []int, []*selection.Field) (map[int]string, error) {
					calls++
					cancel()
					return map[int]string{42: "not successful"}, nil
				}, Config{MaxBatch: 1})
				var err error
				if operation == "all" {
					var values map[int]string
					values, err = loader.LoadAll(ctx, []int{42}, nil)
					if values != nil {
						t.Fatal("canceled list returned successful values")
					}
				} else if value, loadErr := loader.Load(ctx, 42, nil); value != "" {
					t.Fatal("canceled immediate load returned a value")
				} else {
					err = loadErr
				}
				wantCalls := 0
				if when == "after" {
					wantCalls = 1
				}
				if !errors.Is(err, context.Canceled) || calls != wantCalls {
					t.Fatalf("canceled callback: err=%v calls=%d want=%d", err, calls, wantCalls)
				}
			})
		}
	}
}

func TestCanceledQueuedBatchReleasesWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var requestScope context.Context = testScope(t, t.Context())

		var calls atomic.Int32
		loader := New(func(context.Context, []int, []*selection.Field) (map[int]string, error) {
			calls.Add(1)
			return nil, nil
		}, Config{Wait: time.Hour, MaxBatch: 100})
		ctx, cancel := context.WithCancel(requestScope)
		defer cancel()
		result := make(chan error, 1)
		go func() { _, err := loader.Load(ctx, 42, nil); result <- err }()
		synctest.Wait()
		loader.mu.Lock()
		pending := loader.batches[testBatchKey(t, ctx)]
		loader.mu.Unlock()
		if pending == nil {
			t.Fatal("request was not queued")
		}
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		loader.mu.Lock()
		remaining := len(loader.batches)
		loader.mu.Unlock()
		wasActive := pending.timer.Stop()
		if remaining != 0 || wasActive || calls.Load() != 0 {
			t.Fatalf("canceled batch retained state: queued=%d timer=%v calls=%d", remaining, wasActive, calls.Load())
		}
	})
}

func TestThresholdCancellationDoesNotWaitForCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var requestScope context.Context = testScope(t, t.Context())

		started, release := make(chan struct{}), make(chan struct{})
		loader := New(func(context.Context, []int, []*selection.Field) (map[int]string, error) {
			close(started)
			<-release
			return map[int]string{42: "late value"}, nil
		}, Config{Wait: time.Hour, MaxBatch: 2})
		ctx, cancel := context.WithCancel(requestScope)
		defer cancel()
		results := make(chan error, 2)
		for range 2 {
			go func() { _, err := loader.Load(ctx, 42, nil); results <- err }()
		}
		<-started
		canceledAt := time.Now()
		cancel()
		unblock := sync.OnceFunc(func() { close(release) })
		fallback := time.AfterFunc(time.Second, unblock)
		defer fallback.Stop()
		for range 2 {
			if err := <-results; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
		elapsed := time.Since(canceledAt)
		unblock()
		synctest.Wait()
		if elapsed != 0 {
			t.Fatalf("threshold caller waited for the callback after cancellation: %s", elapsed)
		}
	})
}

func TestConcurrentBatchDispatchCompletesOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var requestScope context.Context = testScope(t, t.Context())

		var calls atomic.Int32
		loader := New(func(context.Context, []int, []*selection.Field) (map[int]string, error) {
			calls.Add(1)
			return map[int]string{42: "ok"}, nil
		}, Config{Wait: time.Hour})
		results := make(chan error, 2)
		for range 2 {
			go func() {
				value, err := loader.Load(requestScope, 42, nil)
				if value != "ok" && err == nil {
					err = errors.New("completed batch lost its value")
				}
				results <- err
			}()
		}
		synctest.Wait()
		loader.mu.Lock()
		pending := loader.batches[testBatchKey(t, requestScope)]
		loader.mu.Unlock()
		if pending == nil {
			t.Fatal("loads were not queued")
		}
		for range 4 {
			go loader.flushBatch(testBatchKey(t, requestScope), pending)
		}
		for range 2 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("callback ran %d times", calls.Load())
		}
	})
}

func TestBatchedLoadsPreserveMissingAndDuplicateKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var requestScope context.Context = testScope(t, t.Context())

		type row struct{ id int }
		type result struct {
			key   int
			value *row
			err   error
		}
		rows := map[int]*row{0: {id: 0}, 42: {id: 42}}
		var calls atomic.Int32
		loader := New(func(_ context.Context, keys []int, _ []*selection.Field) (map[int]*row, error) {
			calls.Add(1)
			if len(keys) != 3 {
				return nil, errors.New("batch did not deduplicate its keys")
			}
			// Backends can reuse their key slice without changing caller ordering.
			clear(keys)
			return rows, nil
		}, Config{Wait: time.Hour, MaxBatch: 4})
		results := make(chan result, 4)
		for _, key := range []int{0, 42, 42, 99} {
			go func() {
				value, err := loader.Load(requestScope, key, nil)
				results <- result{key, value, err}
			}()
		}
		for range 4 {
			got := <-results
			if got.err != nil || got.value != rows[got.key] {
				t.Fatalf("caller key %d received %v/%v", got.key, got.value, got.err)
			}
		}
		if calls.Load() != 1 {
			t.Fatalf("callback ran %d times", calls.Load())
		}
	})
}
