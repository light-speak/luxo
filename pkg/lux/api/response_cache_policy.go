package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/light-speak/luxo/pkg/cachepolicy"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

// Only startup-verified operations reach this immutable request-path snapshot.
type compiledCachePolicy struct {
	ttl    time.Duration
	prefix string
}

func (rt *Router) compileCachePolicy(policy *schema.CachePolicy) (*compiledCachePolicy, error) {
	if policy == nil {
		return nil, errors.New("response cache requires an explicit policy")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if policy.Scope == cachepolicy.Public {
		return nil, errors.New("public sharing is not yet supported by the response cache runtime")
	}
	// Policy changes separate entries even when a deployment reuses its revision.
	material := rt.responseCache.prefix + string(policy.Mode) + ":" + string(policy.Scope) + ":" + strconv.FormatInt(int64(policy.TTL), 10)
	digest := sha256.Sum256([]byte(material))
	// Keep the prefix fixed-size; extra contract fields must not grow each key.
	prefix := "luxo:response:p1:" + hex.EncodeToString(digest[:]) + ":"
	return &compiledCachePolicy{ttl: policy.TTL, prefix: prefix}, nil
}

// CacheResponsePolicy uses the prepared contract rather than caller-selected
// TTL/scope values. Generated handlers call it after authorization and input reads.
// identity is the verified bearer token, or empty for an anonymous request;
// callers obtain it from authentication, never request parameters or headers.
// It is hashed into the key and is never stored verbatim.
func CacheResponsePolicy(ctx context.Context, req *Request, identity string, execute func() error) error {
	route, plan, err := preparedCacheRoute(req)
	if err != nil {
		return err
	}
	if plan.policy == nil {
		return errors.New("api: named response cache policy is not bound")
	}
	return cacheResponse(ctx, req, identity, plan.policy.ttl, route.cache, plan.policy.prefix, execute)
}

func preparedCacheRoute(req *Request) (*cacheRouteBinding, *cacheDependencyPlan, error) {
	route := req.responseCache
	if route == nil {
		return nil, nil, errors.New("api: response cache is not bound to this request")
	}
	plan := route.plan.Load()
	if plan == nil {
		return nil, nil, errors.New("api: response cache route has not been declared and prepared")
	}
	if route.api != req.API {
		return nil, nil, errors.New("api: response cache route does not match request API")
	}
	return route, plan, nil
}
