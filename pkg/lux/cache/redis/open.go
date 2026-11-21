package redis

import (
	"context"
	"errors"

	"github.com/light-speak/luxo/pkg/lux/cache"
)

var _ cache.Cache = (*RedisCache)(nil)

// Open reads connection settings for the explicitly compiled Redis adapter.
// A missing URL or failed readiness check is an error, never a fallback.
func Open(ctx context.Context) (cache.Cache, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	backend, err := NewRedisFromEnv()
	if err != nil {
		return nil, err
	}
	if backend == nil {
		return nil, errors.New("cache: Redis requires REDIS_URL")
	}
	if err := backend.Ping(ctx); err != nil {
		_ = backend.Close()
		return nil, errors.New("cache: Redis readiness check failed")
	}
	return backend, nil
}
