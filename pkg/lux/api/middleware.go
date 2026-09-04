package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/light-speak/luxo/pkg/lux/cache"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/ratelimit"
)

// WithRateLimit wraps a handler with a per-client token bucket.
func WithRateLimit(max int, window time.Duration, handler HandlerFunc) HandlerFunc {
	limiter := ratelimit.New(max, window)
	return func(ctx context.Context, req *Request) error {
		if req.Internal {
			return handler(ctx, req)
		}
		key := req.ClientKey
		if key == "" {
			key = "internal"
		}
		if !limiter.Allow(key) {
			return luxerrors.RateLimited
		}
		return handler(ctx, req)
	}
}

// DefaultCache is the shared cache instance for @cache directive.
// Defaults to in-memory cache. Set to a RedisCache for multi-instance deployments.
var DefaultCache cache.Cache = cache.NewMemory()

// SetCache replaces the default cache backend.
// Call this at startup before registering handlers.
func SetCache(c cache.Cache) {
	DefaultCache = c
}

// WithCache wraps a handler with TTL caching. Cache key = API name + params hash.
func WithCache(ttl time.Duration, handler HandlerFunc) HandlerFunc {
	return func(ctx context.Context, req *Request) error {
		key := cacheKey(req)
		if data, _ := DefaultCache.Get(ctx, key); data != nil {
			req.Buf.B = append(req.Buf.B, data...)
			return nil
		}
		startLen := len(req.Buf.B)
		if err := handler(ctx, req); err != nil {
			return err
		}
		// Best-effort: ignore cache set errors.
		DefaultCache.Set(ctx, key, req.Buf.B[startLen:], ttl)
		return nil
	}
}

// InvalidateCache clears cache entries for a model (called after create/update/delete).
func InvalidateCache(model string) {
	DefaultCache.Invalidate(context.Background(), model+":")
}

func cacheKey(req *Request) string {
	h := sha256.New()
	h.Write([]byte(req.API))
	for k, v := range req.Params {
		h.Write([]byte(k))
		h.Write(v)
	}
	return req.API + ":" + hex.EncodeToString(h.Sum(nil))
}
