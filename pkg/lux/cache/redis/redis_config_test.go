package redis

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func mustRedis(t testing.TB, address string, opts ...RedisOption) *RedisCache {
	t.Helper()
	c, err := NewRedis(address, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRedisConfigurationRejectsInvalidOptions(t *testing.T) {
	for _, factory := range redisTestFactories() {
		t.Run(factory.name, func(t *testing.T) {
			for _, sample := range []struct {
				name   string
				option RedisOption
			}{
				{"nil", nil},
				{"zero-timeout", WithTimeout(0)},
				{"negative-timeout", WithTimeout(-1)},
				{"disabled-timeout", WithTimeout(-2 * time.Second)},
				{"zero-pool", WithPoolSize(0)},
				{"negative-pool", WithPoolSize(-1)},
				{"negative-database", WithDB(-1)},
			} {
				t.Run(sample.name, func(t *testing.T) {
					c, err := factory.new(WithPassword("secret-never-in-errors"), sample.option)
					if c != nil {
						_ = c.Close()
						t.Fatal("invalid configuration created a Redis client")
					}
					if !errors.Is(err, ErrInvalidRedisConfig) || strings.Contains(err.Error(), "secret-never-in-errors") {
						t.Fatalf("configuration error = %v", err)
					}
				})
			}
		})
	}
}

func TestRedisConfigurationDefaultsAndOverrides(t *testing.T) {
	for _, factory := range redisTestFactories() {
		t.Run(factory.name, func(t *testing.T) {
			c, err := factory.new()
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if o := c.client.Options(); !o.ContextTimeoutEnabled || o.ReadTimeout != 3*time.Second || o.WriteTimeout != 3*time.Second || o.DialTimeout != 3*time.Second || o.PoolSize != 10 {
				t.Fatal("default I/O limits not preserved")
			}
			c, err = factory.new(WithTimeout(-1), WithTimeout(time.Second), WithPoolSize(20), WithDB(2), WithPrefix("team"))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			o := c.client.Options()
			if o.ReadTimeout != time.Second || o.WriteTimeout != time.Second || o.DialTimeout != time.Second || o.PoolSize != 20 || o.DB != 2 || c.prefix != "team" {
				t.Fatal("explicit options were ignored")
			}
		})
	}
}

type redisTestFactory struct {
	name string
	new  func(...RedisOption) (*RedisCache, error)
}

func redisTestFactories() []redisTestFactory {
	return []redisTestFactory{
		{"address", func(opts ...RedisOption) (*RedisCache, error) { return NewRedis("localhost:1", opts...) }},
		{"url", func(opts ...RedisOption) (*RedisCache, error) { return newRedisFromURL("redis://localhost:1", opts...) }},
	}
}

func TestRedisURLConfigurationPreservesExplicitOptions(t *testing.T) {
	url := "rediss://name:url-password@localhost:6380/5?read_timeout=7s&write_timeout=8s&dial_timeout=9s&pool_size=30"
	c, err := newRedisFromURL(url)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	o := c.client.Options()
	if o.ReadTimeout != 7*time.Second || o.WriteTimeout != 8*time.Second || o.DialTimeout != 9*time.Second || o.PoolSize != 30 || o.DB != 5 || o.TLSConfig == nil || o.Username != "name" || o.Password != "url-password" {
		t.Fatal("URL connection settings were silently overwritten")
	}
	c, err = newRedisFromURL(url, WithTimeout(time.Second), WithPassword(""), WithDB(0), WithPoolSize(1))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	o = c.client.Options()
	if o.ReadTimeout != time.Second || o.WriteTimeout != time.Second || o.DialTimeout != time.Second || o.PoolSize != 1 || o.DB != 0 || o.Password != "" || o.TLSConfig == nil {
		t.Fatal("explicit options did not override URL settings")
	}
}

func TestRedisURLConfigurationRejectsDisabledDeadlines(t *testing.T) {
	for _, query := range []string{"read_timeout=0", "read_timeout=-1", "write_timeout=0s", "write_timeout=-1", "dial_timeout=-1", "pool_size=-1", "db=-1"} {
		t.Run(query, func(t *testing.T) {
			t.Setenv("REDIS_URL", "redis://name:secret-never-in-errors@localhost:6379/?"+query)
			c, err := NewRedisFromEnv()
			if c != nil {
				_ = c.Close()
				t.Fatal("unsafe URL configuration created a client")
			}
			if !errors.Is(err, ErrInvalidRedisConfig) || strings.Contains(err.Error(), "secret-never-in-errors") {
				t.Fatalf("configuration error = %v", err)
			}
		})
	}
}
