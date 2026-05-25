package cache

import (
	"context"
	"testing"
	"time"
)

// ─── Interface compliance ────────────────────────────────────────────────────

var (
	_ Cache = (*MemoryCache)(nil)
	_ Cache = (*RedisCache)(nil)
	_ Cache = Nop{}
)

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

// ─── writeValue coverage ────────────────────────────────────────────────────

func TestBuildKey_UnknownType(t *testing.T) {
	// Unknown value type should still produce a deterministic key.
	type custom struct{}
	k1 := BuildKey("api", map[string]any{"x": custom{}})
	k2 := BuildKey("api", map[string]any{"x": custom{}})
	if k1 != k2 {
		t.Errorf("unknown type should be deterministic: %q vs %q", k1, k2)
	}
}

func TestBuildKey_BoolFalse(t *testing.T) {
	k1 := BuildKey("api", map[string]any{"flag": true})
	k2 := BuildKey("api", map[string]any{"flag": false})
	if k1 == k2 {
		t.Error("true and false should produce different keys")
	}
}

func TestBuildKey_NilValue(t *testing.T) {
	k1 := BuildKey("api", map[string]any{"x": nil})
	k2 := BuildKey("api", map[string]any{"x": "hello"})
	if k1 == k2 {
		t.Error("nil and string should produce different keys")
	}
}

func TestBuildKey_BytesValue(t *testing.T) {
	k1 := BuildKey("api", map[string]any{"data": []byte{1, 2, 3}})
	k2 := BuildKey("api", map[string]any{"data": []byte{4, 5, 6}})
	if k1 == k2 {
		t.Error("different bytes should produce different keys")
	}
}

func TestBuildKey_FloatValue(t *testing.T) {
	k1 := BuildKey("api", map[string]any{"val": float64(1.5)})
	k2 := BuildKey("api", map[string]any{"val": float64(2.5)})
	if k1 == k2 {
		t.Error("different floats should produce different keys")
	}
}
