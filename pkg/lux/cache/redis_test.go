package cache

import (
	"context"
	"os"
	"testing"
	"time"
)

func redisAddr() string {
	if addr := os.Getenv("REDIS_URL"); addr != "" {
		return addr
	}
	return "localhost:6379"
}

func skipNoRedis(t *testing.T) *RedisCache {
	t.Helper()
	c := NewRedis(redisAddr(), WithPrefix("luxo_test"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		c.Close()
		t.Skipf("Redis not available: %v", err)
	}
	// Clean up test keys before each test.
	c.Invalidate(ctx, "")
	return c
}

func TestRedisCache_SetGet(t *testing.T) {
	c := skipNoRedis(t)
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

func TestRedisCache_Miss(t *testing.T) {
	c := skipNoRedis(t)
	defer c.Close()
	ctx := context.Background()

	got, err := c.Get(ctx, "nonexistent_key_xyz")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("miss should return nil, got %v", got)
	}
}

func TestRedisCache_Expired(t *testing.T) {
	c := skipNoRedis(t)
	defer c.Close()
	ctx := context.Background()

	c.Set(ctx, "ttlkey", []byte("val"), 100*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	got, _ := c.Get(ctx, "ttlkey")
	if got != nil {
		t.Errorf("expired should return nil, got %v", got)
	}
}

func TestRedisCache_Del(t *testing.T) {
	c := skipNoRedis(t)
	defer c.Close()
	ctx := context.Background()

	c.Set(ctx, "del_a", []byte("1"), time.Minute)
	c.Set(ctx, "del_b", []byte("2"), time.Minute)
	c.Del(ctx, "del_a")
	if got, _ := c.Get(ctx, "del_a"); got != nil {
		t.Error("'del_a' should be deleted")
	}
	if got, _ := c.Get(ctx, "del_b"); got == nil {
		t.Error("'del_b' should still exist")
	}
}

func TestRedisCache_Invalidate(t *testing.T) {
	c := skipNoRedis(t)
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

func TestRedisCache_Prefix(t *testing.T) {
	c := skipNoRedis(t)
	defer c.Close()
	ctx := context.Background()

	// Key should be prefixed internally.
	c.Set(ctx, "mykey", []byte("val"), time.Minute)
	got, _ := c.Get(ctx, "mykey")
	if string(got) != "val" {
		t.Errorf("prefixed key should work, got %q", got)
	}
}

func TestRedisCache_DelEmpty(t *testing.T) {
	c := skipNoRedis(t)
	defer c.Close()
	ctx := context.Background()

	// Del with no keys should not error.
	if err := c.Del(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestNewRedisFromEnv_NoEnv(t *testing.T) {
	// Ensure REDIS_URL is not set for this test.
	orig := os.Getenv("REDIS_URL")
	os.Unsetenv("REDIS_URL")
	defer func() {
		if orig != "" {
			os.Setenv("REDIS_URL", orig)
		}
	}()

	c := NewRedisFromEnv()
	if c != nil {
		c.Close()
		t.Error("should return nil when REDIS_URL is not set")
	}
}

func TestNewRedisFromEnv_WithEnv(t *testing.T) {
	origURL := os.Getenv("REDIS_URL")
	origPW := os.Getenv("REDIS_PASSWORD")
	origPfx := os.Getenv("REDIS_PREFIX")
	defer func() {
		restoreEnv("REDIS_URL", origURL)
		restoreEnv("REDIS_PASSWORD", origPW)
		restoreEnv("REDIS_PREFIX", origPfx)
	}()

	os.Setenv("REDIS_URL", "localhost:6379")
	os.Setenv("REDIS_PASSWORD", "testpass")
	os.Setenv("REDIS_PREFIX", "myapp")
	c := NewRedisFromEnv()
	if c == nil {
		t.Fatal("should create cache when REDIS_URL is set")
	}
	defer c.Close()
	if c.prefix != "myapp" {
		t.Errorf("prefix = %q, want %q", c.prefix, "myapp")
	}
}

func restoreEnv(key, val string) {
	if val == "" {
		os.Unsetenv(key)
	} else {
		os.Setenv(key, val)
	}
}

func TestRedisOptions(t *testing.T) {
	// Test that all options are applied correctly.
	c := NewRedis("localhost:6379",
		WithPassword("secret"),
		WithDB(2),
		WithPoolSize(20),
		WithPrefix("test"),
		WithTimeout(5*time.Second),
	)
	defer c.Close()
	if c.prefix != "test" {
		t.Errorf("prefix = %q, want %q", c.prefix, "test")
	}
}

func TestRedisCache_PrefixKey_NoPrefix(t *testing.T) {
	c := &RedisCache{prefix: ""}
	if got := c.prefixKey("mykey"); got != "mykey" {
		t.Errorf("no prefix: got %q, want %q", got, "mykey")
	}
}

func TestRedisCache_PrefixKey_WithPrefix(t *testing.T) {
	c := &RedisCache{prefix: "app"}
	if got := c.prefixKey("mykey"); got != "app:mykey" {
		t.Errorf("with prefix: got %q, want %q", got, "app:mykey")
	}
}

func TestRedisCache_GetError(t *testing.T) {
	// Use an invalid address to trigger error path.
	c := NewRedis("invalid:0", WithTimeout(100*time.Millisecond))
	defer c.Close()
	ctx := context.Background()

	// Get on unreachable Redis should return nil, nil (best-effort).
	got, err := c.Get(ctx, "key")
	if got != nil {
		t.Errorf("should return nil on error, got %v", got)
	}
	if err != nil {
		t.Errorf("should swallow error, got %v", err)
	}
}

func TestRedisCache_SetError(t *testing.T) {
	c := NewRedis("invalid:0", WithTimeout(100*time.Millisecond))
	defer c.Close()
	ctx := context.Background()

	err := c.Set(ctx, "key", []byte("val"), time.Minute)
	if err == nil {
		t.Error("should return error on unreachable Redis")
	}
}

func TestRedisCache_DelError(t *testing.T) {
	c := NewRedis("invalid:0", WithTimeout(100*time.Millisecond))
	defer c.Close()
	ctx := context.Background()

	err := c.Del(ctx, "key")
	if err == nil {
		t.Error("should return error on unreachable Redis")
	}
}

func TestRedisCache_InvalidateError(t *testing.T) {
	c := NewRedis("invalid:0", WithTimeout(100*time.Millisecond))
	defer c.Close()
	ctx := context.Background()

	err := c.Invalidate(ctx, "prefix")
	if err == nil {
		t.Error("should return error on unreachable Redis")
	}
}
