package redis

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
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
	options *redis.Options
	prefix  string
}

// WithPassword sets the Redis password.
func WithPassword(password string) RedisOption {
	return func(c *redisConfig) { c.options.Password = password }
}

// WithDB sets the Redis database number.
func WithDB(db int) RedisOption {
	return func(c *redisConfig) { c.options.DB = db }
}

// WithPoolSize sets the connection pool size.
func WithPoolSize(size int) RedisOption {
	return func(c *redisConfig) { c.options.PoolSize = size }
}

// WithPrefix sets a key prefix for all cache operations.
func WithPrefix(prefix string) RedisOption {
	return func(c *redisConfig) { c.prefix = prefix }
}

// WithTimeout sets a strictly positive socket timeout for Redis operations.
func WithTimeout(d time.Duration) RedisOption {
	return func(c *redisConfig) {
		c.options.ReadTimeout, c.options.WriteTimeout, c.options.DialTimeout = d, d, d
	}
}

// NewRedis creates a Redis-backed cache. addr is "host:port" format.
// Clients respect operation deadlines as well as positive socket timeouts.
// Invalid configuration is rejected before constructing the client; no fallback
// cache, connection or background worker is created on failure.
func NewRedis(addr string, opts ...RedisOption) (*RedisCache, error) {
	return newConfiguredRedis(&redis.Options{Addr: addr}, opts)
}

// ErrInvalidRedisConfig excludes option values, which may contain credentials.
var ErrInvalidRedisConfig = errors.New("cache: invalid Redis configuration")

func newConfiguredRedis(options *redis.Options, opts []RedisOption) (*RedisCache, error) {
	if options.PoolSize == 0 {
		options.PoolSize = 10
	}
	for _, duration := range []*time.Duration{&options.ReadTimeout, &options.WriteTimeout, &options.DialTimeout} {
		if *duration == 0 {
			*duration = 3 * time.Second
		}
	}
	cfg := redisConfig{options: options}
	for _, option := range opts {
		if option == nil {
			return nil, ErrInvalidRedisConfig
		}
		option(&cfg)
	}
	if options.ReadTimeout <= 0 || options.WriteTimeout <= 0 || options.DialTimeout <= 0 || options.PoolSize < 1 || options.DB < 0 {
		return nil, ErrInvalidRedisConfig
	}
	options.ContextTimeoutEnabled = true
	return &RedisCache{client: redis.NewClient(options), prefix: cfg.prefix}, nil
}

// NewRedisFromEnv creates a Redis cache from REDIS_URL environment variable.
// Supports both plain "host:port" and URL format "redis://..." / "rediss://...".
// Returns (nil, nil) only when REDIS_URL is not set. Invalid configuration is
// an error, never an instruction to fall back to a process-local backend.
// Construction does not connect; the owner must check readiness with Ping.
func NewRedisFromEnv() (*RedisCache, error) {
	addr := os.Getenv("REDIS_URL")
	if addr == "" {
		return nil, nil
	}
	var opts []RedisOption
	if pw := os.Getenv("REDIS_PASSWORD"); pw != "" {
		opts = append(opts, WithPassword(pw))
	}
	if prefix := os.Getenv("REDIS_PREFIX"); prefix != "" {
		opts = append(opts, WithPrefix(prefix))
	}
	// If the URL uses redis:// or rediss:// scheme, parse it with go-redis.
	if strings.Contains(addr, "://") {
		return newRedisFromURL(addr, opts...)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return nil, ErrInvalidRedisURL
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return nil, ErrInvalidRedisURL
	}
	return NewRedis(addr, opts...)
}

// ErrInvalidRedisURL never includes the URL or upstream parsing error because
// either can contain credentials, including malformed credential escapes.
var ErrInvalidRedisURL = errors.New("cache: invalid REDIS_URL")

// newRedisFromURL creates a Redis cache by parsing a redis:// or rediss:// URL.
func newRedisFromURL(rawURL string, opts ...RedisOption) (*RedisCache, error) {
	if !strings.HasPrefix(rawURL, "redis://") && !strings.HasPrefix(rawURL, "rediss://") {
		return nil, ErrInvalidRedisURL
	}
	parsed, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, ErrInvalidRedisURL
	}
	return newConfiguredRedis(parsed, opts)
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
		return nil, err
	}
	return data, nil
}

// Set stores data with a TTL.
func (r *RedisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return r.client.Set(ctx, r.prefixKey(key), value, ttl).Err()
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
	return r.client.Del(ctx, prefixed...).Err()
}

const invalidateBatchSize = 128

var redisGlobEscaper = strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`, `]`, `\]`)

// ErrWildcardInvalidate is returned when Invalidate is called with an empty prefix
// and no key prefix is configured, which would match all keys in the Redis database.
var ErrWildcardInvalidate = errors.New("cache: refusing to invalidate with bare wildcard pattern \"*\"; set a prefix or provide a non-empty pattern")

// Invalidate removes keys under a literal prefix, yielding between SCAN pages.
// This maintenance operation is not atomic with concurrent cache fills. SCAN's
// COUNT is a hint, so UNLINK commands are independently bounded. Request-path
// dependency invalidation must not rely on a whole-keyspace maintenance scan.
func (r *RedisCache) Invalidate(ctx context.Context, prefix string) error {
	literal := r.prefixKey(prefix)
	if literal == "" {
		return ErrWildcardInvalidate
	}
	pattern := redisGlobEscaper.Replace(literal) + "*"
	var cursor uint64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		keys, next, err := r.client.Scan(ctx, cursor, pattern, invalidateBatchSize).Result()
		if err != nil {
			return err
		}
		if err := r.unlinkMatching(ctx, literal, keys); err != nil {
			return err
		}
		if next == 0 {
			return nil
		}
		cursor = next
	}
}

func (r *RedisCache) unlinkMatching(ctx context.Context, prefix string, keys []string) error {
	matched := keys[:0]
	for _, key := range keys {
		if strings.HasPrefix(key, prefix) {
			matched = append(matched, key)
		}
	}
	for len(matched) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(len(matched), invalidateBatchSize)
		if err := r.client.Unlink(ctx, matched[:n]...).Err(); err != nil {
			return err
		}
		matched = matched[n:]
	}
	return nil
}

// Close closes the Redis connection pool.
func (r *RedisCache) Close() error {
	return r.client.Close()
}

// Ping checks the Redis connection.
func (r *RedisCache) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}
