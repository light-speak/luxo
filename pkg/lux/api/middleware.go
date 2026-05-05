package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/light-speak/luxo/pkg/lux/cache"
)

// DefaultCache is the shared cache instance for @cache directive.
var DefaultCache = cache.New()

// WithCache wraps a handler with TTL caching. Cache key = API name + params hash.
func WithCache(ttl time.Duration, handler HandlerFunc) HandlerFunc {
	return func(ctx context.Context, req *Request) error {
		key := cacheKey(req)
		if data := DefaultCache.Get(key); data != nil {
			req.Buf.B = append(req.Buf.B, data...)
			return nil
		}
		startLen := len(req.Buf.B)
		if err := handler(ctx, req); err != nil {
			return err
		}
		DefaultCache.Set(key, req.Buf.B[startLen:], ttl)
		return nil
	}
}

// InvalidateCache clears cache entries for a model (called after create/update/delete).
func InvalidateCache(model string) {
	DefaultCache.Invalidate(model + ":")
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
