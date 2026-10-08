package redis

import (
	"context"
	"crypto/rand"
	"os"
	"testing"
	"time"
)

func skipNoRedis(t testing.TB) *RedisCache {
	t.Helper()
	c := mustRedisFromEnv(t)
	if c == nil {
		c = mustRedis(t, "localhost:6379")
	}
	c.prefix = "luxo_test:" + rand.Text()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		c.Close()
		t.Skipf("Redis not available: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		defer c.Close()
		if err := c.Invalidate(ctx, ""); err != nil {
			t.Errorf("cleanup owned Redis namespace: %v", err)
		}
	})
	return c
}

func TestRedisCache_SetGet(t *testing.T) {
	c := skipNoRedis(t)
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

func TestRedisCache_InvalidateLiteralPrefix(t *testing.T) {
	c := skipNoRedis(t)
	ctx := context.Background()
	for _, key := range []string{"User[0]*?:1", "User0ab:1"} {
		if err := c.Set(ctx, key, []byte("value"), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Invalidate(ctx, "User[0]*?:"); err != nil {
		t.Fatal(err)
	}
	if got, err := c.Get(ctx, "User[0]*?:1"); err != nil || got != nil {
		t.Fatalf("literal prefix was not removed: (%q, %v)", got, err)
	}
	if got, err := c.Get(ctx, "User0ab:1"); err != nil || string(got) != "value" {
		t.Fatalf("unrelated key changed: (%q, %v)", got, err)
	}
}

func TestRedisCache_Prefix(t *testing.T) {
	c := skipNoRedis(t)
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

	c := mustRedisFromEnv(t)
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
	c := mustRedisFromEnv(t)
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
	c := mustRedis(t, "localhost:6379",
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
	c := mustRedis(t, "invalid:0", WithTimeout(100*time.Millisecond))
	defer c.Close()
	ctx := context.Background()

	// The backend must distinguish an outage from a cache miss.
	got, err := c.Get(ctx, "key")
	if got != nil {
		t.Errorf("should return nil on error, got %v", got)
	}
	if err == nil {
		t.Error("must return the backend error")
	}
}

func TestRedisCache_SetError(t *testing.T) {
	c := mustRedis(t, "invalid:0", WithTimeout(100*time.Millisecond))
	defer c.Close()
	ctx := context.Background()

	err := c.Set(ctx, "key", []byte("val"), time.Minute)
	if err == nil {
		t.Error("should return error on unreachable Redis")
	}
}

func TestRedisCache_DelError(t *testing.T) {
	c := mustRedis(t, "invalid:0", WithTimeout(100*time.Millisecond))
	defer c.Close()
	ctx := context.Background()

	err := c.Del(ctx, "key")
	if err == nil {
		t.Error("should return error on unreachable Redis")
	}
}

func TestRedisCache_InvalidateError(t *testing.T) {
	c := mustRedis(t, "invalid:0", WithTimeout(100*time.Millisecond))
	defer c.Close()
	ctx := context.Background()

	err := c.Invalidate(ctx, "prefix")
	if err == nil {
		t.Error("should return error on unreachable Redis")
	}
}

// ─── Issue 2: NewRedisFromEnv URL parsing ──────────────────────────────────

func TestNewRedisFromEnv_RedisURL(t *testing.T) {
	origURL := os.Getenv("REDIS_URL")
	origPW := os.Getenv("REDIS_PASSWORD")
	origPfx := os.Getenv("REDIS_PREFIX")
	defer func() {
		restoreEnv("REDIS_URL", origURL)
		restoreEnv("REDIS_PASSWORD", origPW)
		restoreEnv("REDIS_PREFIX", origPfx)
	}()

	os.Setenv("REDIS_URL", "redis://localhost:6379/0")
	os.Unsetenv("REDIS_PASSWORD")
	os.Unsetenv("REDIS_PREFIX")
	c := mustRedisFromEnv(t)
	if c == nil {
		t.Fatal("should create cache from redis:// URL")
	}
	defer c.Close()
}

func TestNewRedisFromEnv_RedissURL(t *testing.T) {
	origURL := os.Getenv("REDIS_URL")
	origPW := os.Getenv("REDIS_PASSWORD")
	origPfx := os.Getenv("REDIS_PREFIX")
	defer func() {
		restoreEnv("REDIS_URL", origURL)
		restoreEnv("REDIS_PASSWORD", origPW)
		restoreEnv("REDIS_PREFIX", origPfx)
	}()

	os.Setenv("REDIS_URL", "rediss://localhost:6380/1")
	os.Unsetenv("REDIS_PASSWORD")
	os.Setenv("REDIS_PREFIX", "myprefix")
	c := mustRedisFromEnv(t)
	if c == nil {
		t.Fatal("should create cache from rediss:// URL")
	}
	defer c.Close()
	if c.prefix != "myprefix" {
		t.Errorf("prefix = %q, want %q", c.prefix, "myprefix")
	}
}

func TestNewRedisFromEnv_URLWithPassword(t *testing.T) {
	origURL := os.Getenv("REDIS_URL")
	origPW := os.Getenv("REDIS_PASSWORD")
	origPfx := os.Getenv("REDIS_PREFIX")
	defer func() {
		restoreEnv("REDIS_URL", origURL)
		restoreEnv("REDIS_PASSWORD", origPW)
		restoreEnv("REDIS_PREFIX", origPfx)
	}()

	// Password in URL, but REDIS_PASSWORD env overrides.
	os.Setenv("REDIS_URL", "redis://:urlpass@localhost:6379/0")
	os.Setenv("REDIS_PASSWORD", "envpass")
	os.Unsetenv("REDIS_PREFIX")
	c := mustRedisFromEnv(t)
	if c == nil {
		t.Fatal("should create cache from redis:// URL with password override")
	}
	defer c.Close()
}

// ─── Issue 3: Wildcard invalidate guard ────────────────────────────────────

func TestRedisCache_InvalidateWildcardGuard(t *testing.T) {
	// No prefix + empty pattern = bare "*" should be rejected.
	c := mustRedis(t, "localhost:6379")
	defer c.Close()
	ctx := context.Background()

	err := c.Invalidate(ctx, "")
	if err != ErrWildcardInvalidate {
		t.Errorf("expected ErrWildcardInvalidate, got %v", err)
	}
}

func TestRedisCache_InvalidateWithPrefixAllowed(t *testing.T) {
	// With a prefix configured, empty pattern becomes "prefix:*" which is safe.
	c := mustRedis(t, "invalid:0", WithPrefix("myapp"), WithTimeout(100*time.Millisecond))
	defer c.Close()
	ctx := context.Background()

	err := c.Invalidate(ctx, "")
	// Should NOT return ErrWildcardInvalidate; it will fail with connection error instead.
	if err == ErrWildcardInvalidate {
		t.Error("invalidate with prefix should not be blocked")
	}
}

func TestRedisCache_InvalidateNonEmptyPatternAllowed(t *testing.T) {
	// No prefix but non-empty pattern = "User:*" which is safe.
	c := mustRedis(t, "invalid:0", WithTimeout(100*time.Millisecond))
	defer c.Close()
	ctx := context.Background()

	err := c.Invalidate(ctx, "User:")
	// Should NOT return ErrWildcardInvalidate; it will fail with connection error instead.
	if err == ErrWildcardInvalidate {
		t.Error("invalidate with non-empty pattern should not be blocked")
	}
}

func mustRedisFromEnv(t testing.TB) *RedisCache {
	t.Helper()
	c, err := NewRedisFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewRedisFromEnvRejectsInvalidConfig(t *testing.T) {
	for _, value := range []string{
		"redis://user:DO_NOT_LOG@invalid host",
		"redis://:DO_NOT_LOG@localhost:6379/not-a-db",
		"rediss://localhost:6379?password=DO_NOT_LOG&unknown=secret",
		"http://:DO_NOT_LOG@localhost:6379",
		"unix:///path/to/redis.sock",
		"redis://user:%XX@localhost:6379",
		"localhost", "localhost:0", "localhost:65536", "localhost:redis", ":6379",
	} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("REDIS_URL", value)
			c, err := NewRedisFromEnv()
			if c != nil {
				_ = c.Close()
				t.Fatal("invalid configuration created a client")
			}
			if err == nil || err.Error() != "cache: invalid REDIS_URL" {
				t.Fatalf("expected a configuration error without connection secrets, got %v", err)
			}
		})
	}
}
