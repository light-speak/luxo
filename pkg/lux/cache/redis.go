package cache

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCache implements Cache using Redis as the backend.
// Suitable for multi-instance deployments where cache must be shared.
type RedisCache struct {
	client *redis.Client
	prefix string
}

// RedisOption configures a RedisCache.
type RedisOption func(*redisConfig)

type redisConfig struct {
	password string
	db       int
	poolSize int
	prefix   string
	timeout  time.Duration
}

// WithPassword sets the Redis password.
func WithPassword(password string) RedisOption {
	return func(c *redisConfig) { c.password = password }
}

// WithDB sets the Redis database number.
func WithDB(db int) RedisOption {
	return func(c *redisConfig) { c.db = db }
}

// WithPoolSize sets the connection pool size.
func WithPoolSize(size int) RedisOption {
	return func(c *redisConfig) { c.poolSize = size }
}

// WithPrefix sets a key prefix for all cache operations.
func WithPrefix(prefix string) RedisOption {
	return func(c *redisConfig) { c.prefix = prefix }
}

// WithTimeout sets the command timeout for Redis operations.
func WithTimeout(d time.Duration) RedisOption {
	return func(c *redisConfig) { c.timeout = d }
}

// NewRedis creates a Redis-backed cache. addr is "host:port" format.
func NewRedis(addr string, opts ...RedisOption) *RedisCache {
	cfg := redisConfig{
		poolSize: 10,
		prefix:   "",
		timeout:  3 * time.Second,
	}
	for _, o := range opts {
		o(&cfg)
	}
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     cfg.password,
		DB:           cfg.db,
		PoolSize:     cfg.poolSize,
		ReadTimeout:  cfg.timeout,
		WriteTimeout: cfg.timeout,
		DialTimeout:  cfg.timeout,
	})
	return &RedisCache{
		client: client,
		prefix: cfg.prefix,
	}
}

// NewRedisFromEnv creates a Redis cache from REDIS_URL environment variable.
// Supports both plain "host:port" and URL format "redis://..." / "rediss://...".
// Returns nil if REDIS_URL is not set.
func NewRedisFromEnv() *RedisCache {
	addr := os.Getenv("REDIS_URL")
	if addr == "" {
		return nil
	}
	var opts []RedisOption
	if pw := os.Getenv("REDIS_PASSWORD"); pw != "" {
		opts = append(opts, WithPassword(pw))
	}
	if prefix := os.Getenv("REDIS_PREFIX"); prefix != "" {
		opts = append(opts, WithPrefix(prefix))
	}
	// If the URL uses redis:// or rediss:// scheme, parse it with go-redis.
	if strings.HasPrefix(addr, "redis://") || strings.HasPrefix(addr, "rediss://") {
		return newRedisFromURL(addr, opts...)
	}
	return NewRedis(addr, opts...)
}

// newRedisFromURL creates a Redis cache by parsing a redis:// or rediss:// URL.
func newRedisFromURL(rawURL string, opts ...RedisOption) *RedisCache {
	cfg := redisConfig{
		poolSize: 10,
		prefix:   "",
		timeout:  3 * time.Second,
	}
	for _, o := range opts {
		o(&cfg)
	}
	parsed, err := redis.ParseURL(rawURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[cache] redis parse URL %q: %v\n", rawURL, err)
		return nil
	}
	// Apply option overrides where set.
	if cfg.password != "" {
		parsed.Password = cfg.password
	}
	parsed.PoolSize = cfg.poolSize
	parsed.ReadTimeout = cfg.timeout
	parsed.WriteTimeout = cfg.timeout
	parsed.DialTimeout = cfg.timeout
	client := redis.NewClient(parsed)
	return &RedisCache{
		client: client,
		prefix: cfg.prefix,
	}
}

func (r *RedisCache) prefixKey(key string) string {
	if r.prefix == "" {
		return key
	}
	return r.prefix + ":" + key
}

// Get returns cached data for the key. Returns nil, nil on cache miss.
func (r *RedisCache) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := r.client.Get(ctx, r.prefixKey(key)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		// Cache failure is best-effort: log and return miss.
		fmt.Fprintf(os.Stderr, "[cache] redis get %q: %v\n", key, err)
		return nil, nil
	}
	return data, nil
}

// Set stores data with a TTL.
func (r *RedisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	err := r.client.Set(ctx, r.prefixKey(key), value, ttl).Err()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[cache] redis set %q: %v\n", key, err)
	}
	return err
}

// Del removes specific keys.
func (r *RedisCache) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	prefixed := make([]string, len(keys))
	for i, k := range keys {
		prefixed[i] = r.prefixKey(k)
	}
	err := r.client.Del(ctx, prefixed...).Err()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[cache] redis del: %v\n", err)
	}
	return err
}

// scanDelScript uses SCAN + DEL to safely remove keys matching a pattern.
// Unlike KEYS, SCAN does not block the server on large keyspaces.
var scanDelScript = redis.NewScript(`
local cursor = "0"
local count = 0
repeat
    local result = redis.call("SCAN", cursor, "MATCH", ARGV[1], "COUNT", 100)
    cursor = result[1]
    local keys = result[2]
    if #keys > 0 then
        redis.call("DEL", unpack(keys))
        count = count + #keys
    end
until cursor == "0"
return count
`)

// ErrWildcardInvalidate is returned when Invalidate is called with an empty prefix
// and no key prefix is configured, which would match all keys in the Redis database.
var ErrWildcardInvalidate = fmt.Errorf("cache: refusing to invalidate with bare wildcard pattern \"*\"; set a prefix or provide a non-empty pattern")

// Invalidate removes all keys matching a prefix using SCAN+DEL (safe for production).
// Returns ErrWildcardInvalidate if the resulting pattern is just "*" (no prefix guard).
func (r *RedisCache) Invalidate(ctx context.Context, prefix string) error {
	pattern := r.prefixKey(prefix) + "*"
	// Guard: bare "*" would wipe the entire Redis DB.
	if pattern == "*" {
		return ErrWildcardInvalidate
	}
	err := scanDelScript.Run(ctx, r.client, nil, pattern).Err()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[cache] redis invalidate %q: %v\n", prefix, err)
	}
	return err
}

// Close closes the Redis connection pool.
func (r *RedisCache) Close() error {
	return r.client.Close()
}

// Ping checks the Redis connection.
func (r *RedisCache) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}
