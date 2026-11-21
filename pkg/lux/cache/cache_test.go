package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// ─── Interface compliance ────────────────────────────────────────────────────

var (
	_ Cache = (*MemoryCache)(nil)
	_ Cache = Nop{}
)

func TestCacheRejectsNegativeTTL(t *testing.T) {
	for name, backend := range map[string]Cache{"memory": NewMemory(), "nop": Nop{}} {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(func() { _ = backend.Close() })
			for _, ttl := range []time.Duration{-1, -time.Second, -1 << 63} {
				if err := backend.Set(t.Context(), "invalid", []byte("payload"), ttl); err == nil {
					t.Errorf("negative TTL %v was accepted", ttl)
				}
			}
		})
	}
}

func TestMemoryInvalidTTLLeavesEntriesUntouched(t *testing.T) {
	for _, ttl := range []time.Duration{-1, -time.Second, -1 << 63} {
		t.Run(ttl.String(), func(t *testing.T) {
			backend := NewMemory()
			t.Cleanup(func() { _ = backend.Close() })
			if err := backend.Set(t.Context(), "existing", []byte("original"), time.Minute); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"existing", "missing"} {
				if err := backend.Set(t.Context(), key, []byte("replacement"), ttl); err == nil {
					t.Errorf("negative TTL %v changed %s", ttl, key)
				}
			}
			value, err := backend.Get(t.Context(), "existing")
			if err != nil || string(value) != "original" {
				t.Errorf("existing value changed: %q, %v", value, err)
			}
			value, err = backend.Get(t.Context(), "missing")
			if err != nil || value != nil {
				t.Errorf("invalid value was stored: %q, %v", value, err)
			}
		})
	}
}

// ─── MemoryCache tests ──────────────────────────────────────────────────────

func TestMemoryCache_SetGet(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	ctx := context.Background()

	if err := c.Set(ctx, "key1", []byte("value1"), time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(ctx, "key1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "value1" {
		t.Errorf("got %q, want %q", got, "value1")
	}
}

func TestMemoryCache_Miss(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	ctx := context.Background()

	got, err := c.Get(ctx, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("miss should return nil, got %v", got)
	}
}

func TestMemoryCache_Expired(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	ctx := context.Background()

	c.Set(ctx, "key", []byte("val"), time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	got, _ := c.Get(ctx, "key")
	if got != nil {
		t.Errorf("expired should return nil, got %v", got)
	}
}

func TestMemoryCache_ZeroTTL(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	ctx := context.Background()

	c.Set(ctx, "forever", []byte("data"), 0)
	got, _ := c.Get(ctx, "forever")
	if string(got) != "data" {
		t.Errorf("zero TTL should not expire, got %q", got)
	}
}

func TestMemoryCache_Del(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	ctx := context.Background()

	c.Set(ctx, "a", []byte("1"), time.Minute)
	c.Set(ctx, "b", []byte("2"), time.Minute)
	c.Del(ctx, "a")
	if got, _ := c.Get(ctx, "a"); got != nil {
		t.Error("'a' should be deleted")
	}
	if got, _ := c.Get(ctx, "b"); got == nil {
		t.Error("'b' should still exist")
	}
}

func TestMemoryCache_DelMultiple(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	ctx := context.Background()

	c.Set(ctx, "x", []byte("1"), time.Minute)
	c.Set(ctx, "y", []byte("2"), time.Minute)
	c.Set(ctx, "z", []byte("3"), time.Minute)
	c.Del(ctx, "x", "y")
	if got, _ := c.Get(ctx, "x"); got != nil {
		t.Error("'x' should be deleted")
	}
	if got, _ := c.Get(ctx, "y"); got != nil {
		t.Error("'y' should be deleted")
	}
	if got, _ := c.Get(ctx, "z"); got == nil {
		t.Error("'z' should still exist")
	}
}

func TestMemoryCache_Invalidate(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	ctx := context.Background()

	c.Set(ctx, "User:1", []byte("a"), time.Minute)
	c.Set(ctx, "User:2", []byte("b"), time.Minute)
	c.Set(ctx, "Post:1", []byte("c"), time.Minute)
	c.Invalidate(ctx, "User:")
	if got, _ := c.Get(ctx, "User:1"); got != nil {
		t.Error("User:1 should be invalidated")
	}
	if got, _ := c.Get(ctx, "User:2"); got != nil {
		t.Error("User:2 should be invalidated")
	}
	if got, _ := c.Get(ctx, "Post:1"); got == nil {
		t.Error("Post:1 should still exist")
	}
}

func TestMemoryCache_Close(t *testing.T) {
	c := NewMemory()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	// Double close should not panic.
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryCacheClosedState(t *testing.T) {
	c := NewMemory()
	ctx := t.Context()
	if err := c.Set(ctx, "retained", []byte("private value"), time.Hour); err != nil {
		t.Fatal(err)
	}
	snapshot, err := c.Get(ctx, "retained")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if string(snapshot) != "private value" {
		t.Error("closing the backend invalidated an owned result")
	}
	assertMemoryCacheClosed(t, c)
}

func assertMemoryCacheClosed(t *testing.T, c *MemoryCache) error {
	t.Helper()
	ctx := t.Context()
	if c.Len() != 0 {
		t.Error("closed cache retained its entries")
	}
	var closedError error
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"Get", func() error {
			value, err := c.Get(ctx, "retained")
			if value != nil {
				t.Error("closed cache returned a retained value")
			}
			return err
		}},
		{"Set", func() error { return c.Set(ctx, "new", []byte("value"), time.Minute) }},
		{"Del", func() error { return c.Del(ctx, "retained") }},
		{"Invalidate", func() error { return c.Invalidate(ctx, "") }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			err := operation.run()
			if err == nil {
				t.Fatal("closed cache operation succeeded")
			}
			if closedError == nil {
				closedError = err
			} else if err != closedError {
				t.Fatalf("closed cache returned inconsistent errors: %v / %v", closedError, err)
			}
		})
	}
	if err := c.Close(); err != nil || c.Len() != 0 {
		t.Fatalf("repeated close did not preserve empty state: len=%d error=%v", c.Len(), err)
	}
	return closedError
}

func TestMemoryCacheConcurrentCloseAndAccess(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	start := make(chan struct{})
	failures := make(chan error, 16*64)
	var group sync.WaitGroup
	for worker := range 16 {
		group.Go(func() {
			<-start
			key := string(rune('a' + worker))
			for index := range 64 {
				if err := accessMemoryCache(t, c, key, byte(index)); err != nil {
					failures <- err
				}
			}
		})
	}
	group.Go(func() { <-start; _ = c.Close() })
	close(start)
	group.Wait()
	close(failures)
	want := assertMemoryCacheClosed(t, c)
	for err := range failures {
		if err != want {
			t.Fatalf("concurrent operation returned unexpected error: %v", err)
		}
	}
}

func accessMemoryCache(t *testing.T, c *MemoryCache, key string, index byte) error {
	t.Helper()
	switch index % 4 {
	case 0:
		return c.Set(t.Context(), key, []byte{index}, time.Minute)
	case 1:
		value, err := c.Get(t.Context(), key)
		if value != nil {
			value[0] = 255
		}
		return err
	case 2:
		return c.Del(t.Context(), key)
	default:
		return c.Invalidate(t.Context(), key)
	}
}

func TestMemoryCacheConcurrentClose(t *testing.T) {
	var panics atomic.Int32
	for range 100 {
		c := NewMemory()
		start := make(chan struct{})
		var workers sync.WaitGroup
		for range 64 {
			workers.Go(func() {
				defer func() {
					if recover() != nil {
						panics.Add(1)
					}
				}()
				<-start
				_ = c.Close()
			})
		}
		close(start)
		workers.Wait()
	}
	if got := panics.Load(); got != 0 {
		t.Fatalf("concurrent Close panicked %d times", got)
	}
}

func TestMemoryCacheCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewMemory()
		defer c.Close()
		ctx := context.Background()
		_ = c.Set(ctx, "expired", []byte("a"), time.Second)
		_ = c.Set(ctx, "live", []byte("b"), 2*time.Minute)
		_ = c.Set(ctx, "persistent", []byte("c"), 0)
		synctest.Wait()
		time.Sleep(61 * time.Second)
		synctest.Wait()
		if c.Len() != 2 {
			t.Fatalf("cleanup retained %d entries, want 2", c.Len())
		}
	})
}

func TestMemoryCache_DataIsolation(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	ctx := context.Background()

	original := []byte("original")
	c.Set(ctx, "key", original, time.Minute)
	// Mutate original after set.
	original[0] = 'X'
	got, _ := c.Get(ctx, "key")
	if string(got) != "original" {
		t.Errorf("set should copy data, got %q", got)
	}
	// Mutate returned data.
	got[0] = 'Y'
	got2, _ := c.Get(ctx, "key")
	if string(got2) != "original" {
		t.Errorf("get should return copy, got %q", got2)
	}
}

func TestMemoryCacheEmptyValueIsPresentAndOwned(t *testing.T) {
	for _, value := range [][]byte{nil, {}} {
		c := NewMemory()
		if err := c.Set(t.Context(), "key", value, time.Hour); err != nil {
			t.Fatal(err)
		}
		got, err := c.Get(t.Context(), "key")
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("stored empty value became a miss: %v %v", got, err)
		}
		got = append(got, 42)
		again, err := c.Get(t.Context(), "key")
		if err != nil || again == nil || len(again) != 0 {
			t.Fatalf("appending to a result mutated the cache: %v %v", again, err)
		}
		if missing, err := c.Get(t.Context(), "missing"); err != nil || missing != nil {
			t.Fatalf("missing key returned data: %v %v", missing, err)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMemoryCache_Len(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	ctx := context.Background()

	c.Set(ctx, "a", []byte("1"), time.Minute)
	c.Set(ctx, "b", []byte("2"), time.Minute)
	if c.Len() != 2 {
		t.Errorf("Len() = %d, want 2", c.Len())
	}
}

// ─── Nop tests ──────────────────────────────────────────────────────────────

func TestNop_AlwaysMiss(t *testing.T) {
	var c Nop
	ctx := context.Background()

	c.Set(ctx, "key", []byte("val"), time.Minute)
	got, err := c.Get(ctx, "key")
	if err != nil || got != nil {
		t.Errorf("Nop should always miss, got %v, err %v", got, err)
	}
}

func TestNop_NoError(t *testing.T) {
	var c Nop
	ctx := context.Background()

	if err := c.Del(ctx, "a", "b"); err != nil {
		t.Error(err)
	}
	if err := c.Invalidate(ctx, "prefix"); err != nil {
		t.Error(err)
	}
	if err := c.Close(); err != nil {
		t.Error(err)
	}
}

func TestMemoryCacheExpiresAtDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewMemory()
		defer c.Close()
		ctx := t.Context()
		if err := c.Set(ctx, "key", []byte("value"), time.Second); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second - time.Nanosecond)
		if got, err := c.Get(ctx, "key"); err != nil || string(got) != "value" {
			t.Fatalf("before deadline: got=%q err=%v", got, err)
		}
		time.Sleep(time.Nanosecond)
		if got, err := c.Get(ctx, "key"); err != nil || got != nil {
			t.Fatalf("at deadline: got=%q err=%v", got, err)
		}
	})
}

func TestMemoryCacheCleanupAtDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewMemory()
		defer c.Close()
		synctest.Wait()
		if err := c.Set(t.Context(), "key", []byte("value"), time.Minute); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if c.Len() != 0 {
			t.Fatalf("cleanup retained %d entries at expiration", c.Len())
		}
	})
}

func TestMemoryCacheReplacementSurvivesCleanup(t *testing.T) {
	for _, ttl := range []time.Duration{0, time.Minute} {
		t.Run(ttl.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c := NewMemory()
				defer c.Close()
				synctest.Wait()
				if err := c.Set(t.Context(), "key", []byte("old"), time.Minute); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() {
					time.Sleep(time.Minute)
					done <- c.Set(t.Context(), "key", []byte("replacement"), ttl)
				}()
				synctest.Wait()
				time.Sleep(time.Minute)
				synctest.Wait()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				if got, err := c.Get(t.Context(), "key"); err != nil || string(got) != "replacement" {
					t.Fatalf("replacement after cleanup: value=%q err=%v", got, err)
				}
			})
		})
	}
}

func TestMemoryCacheConcurrentSnapshotOwnership(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	start := make(chan struct{})
	var workers sync.WaitGroup
	for worker := range 16 {
		workers.Go(func() {
			<-start
			for range 128 {
				value := make([]byte, 256)
				for i := range value {
					value[i] = byte(worker + 1)
				}
				if err := c.Set(t.Context(), "shared", value, time.Minute); err != nil {
					t.Error(err)
					return
				}
				clear(value)
				got, err := c.Get(t.Context(), "shared")
				if err != nil || len(got) != 256 {
					t.Errorf("invalid snapshot length=%d err=%v", len(got), err)
					return
				}
				for _, b := range got {
					if b == 0 || b != got[0] {
						t.Error("snapshot aliases mutable caller data or mixes concurrent writes")
						return
					}
				}
				clear(got)
			}
		})
	}
	close(start)
	workers.Wait()
}
