package api

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/light-speak/luxo/pkg/lux/cache"
)

// CacheNamespace separates deployments even when they share a physical backend.
// Revision must change when business or schema behavior changes.
type CacheNamespace struct {
	Project     string
	Environment string
	Service     string
	Revision    string
}

// ResponseCache is an immutable application binding. Its owner closes the
// backend only after all routers using it have drained their requests.
type ResponseCache struct {
	backend cache.Cache
	prefix  string
	origin  *responseOrigin
}

// cacheRouteBinding is captured during registration and published only after
// successful preparation. Request dispatch never looks up a plan by name.
type cacheRouteBinding struct {
	cache *ResponseCache
	api   string
	plan  atomic.Pointer[cacheDependencyPlan]
}

// NewResponseCache binds an explicit backend without a process-global fallback.
func NewResponseCache(backend cache.Cache, namespace CacheNamespace, options ResponseOriginOptions) (*ResponseCache, error) {
	if backend == nil {
		return nil, errors.New("api: response cache requires an explicit backend")
	}
	prefix, err := namespace.keyPrefix()
	if err != nil {
		return nil, err
	}
	origin, err := newResponseOrigin(options)
	if err != nil {
		return nil, err
	}
	return &ResponseCache{backend: backend, prefix: prefix, origin: origin}, nil
}

func (namespace CacheNamespace) keyPrefix() (string, error) {
	var material []byte
	for _, value := range []string{namespace.Project, namespace.Environment, namespace.Service, namespace.Revision} {
		if strings.TrimSpace(value) == "" || !utf8.ValidString(value) {
			return "", errors.New("api: cache namespace requires valid project, environment, service and revision")
		}
		material = binary.LittleEndian.AppendUint64(material, uint64(len(value)))
		material = append(material, value...)
	}
	sum := sha256.Sum256(material)
	return "luxo:response:v3:" + hex.EncodeToString(sum[:]) + ":", nil
}

// Close releases the explicitly owned backend after its routers have drained.
func (c *ResponseCache) Close() error { return c.backend.Close() }

// RequireCache records generated startup requirements without reading env at runtime.
func (rt *Router) RequireCache(apiName string) {
	if rt.requiredCache == nil {
		rt.requiredCache = make(map[string]struct{})
	}
	rt.requiredCache[apiName] = struct{}{}
}

func bindResponseCache(binding *cacheRouteBinding, next HandlerFunc) HandlerFunc {
	return func(ctx context.Context, req *Request) error {
		previous := req.responseCache
		req.responseCache = binding
		defer func() { req.responseCache = previous }()
		return next(ctx, req)
	}
}
