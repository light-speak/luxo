package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/light-speak/luxo/pkg/lux/cache"
)

// CacheOpener opens the explicitly compiled adapter after configuration validation.
// ResponseCache takes ownership of a successful result; a failed result is closed.
type CacheOpener func(context.Context) (cache.Cache, error)

// NewResponseCacheFromEnv reads namespace and origin limits, never adapter choice.
// The opener runs once at startup and is not retained on the request path.
func NewResponseCacheFromEnv(ctx context.Context, service string, open CacheOpener) (*ResponseCache, error) {
	if open == nil {
		return nil, errors.New("api: response cache requires a compiled backend opener")
	}
	namespace := CacheNamespace{Project: os.Getenv("LUXO_CACHE_PROJECT"),
		Environment: os.Getenv("LUXO_CACHE_ENVIRONMENT"), Service: service,
		Revision: os.Getenv("LUXO_CACHE_REVISION")}
	prefix, err := namespace.keyPrefix()
	if err != nil {
		return nil, err
	}
	origin, err := responseOriginFromEnv()
	if err != nil {
		return nil, err
	}
	backend, err := open(ctx)
	if err != nil {
		if backend != nil {
			_ = backend.Close()
		}
		return nil, err
	}
	if backend == nil {
		return nil, errors.New("api: cache opener returned no backend")
	}
	return &ResponseCache{backend: backend, prefix: prefix, origin: origin}, nil
}

func responseOriginFromEnv() (*responseOrigin, error) {
	concurrency, err := responseOriginNumber("LUXO_CACHE_ORIGIN_CONCURRENCY")
	if err != nil {
		return nil, err
	}
	waiters, err := responseOriginNumber("LUXO_CACHE_ORIGIN_MAX_WAITERS")
	if err != nil {
		return nil, err
	}
	timeout, err := time.ParseDuration(os.Getenv("LUXO_CACHE_ORIGIN_WAIT_TIMEOUT"))
	if err != nil {
		return nil, errors.New("api: LUXO_CACHE_ORIGIN_WAIT_TIMEOUT must be an explicit duration")
	}
	return newResponseOrigin(ResponseOriginOptions{MaxConcurrent: concurrency, MaxWaiting: waiters, WaitTimeout: timeout})
}

func responseOriginNumber(name string) (int, error) {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return 0, fmt.Errorf("api: %s must be an explicit integer", name)
	}
	return value, nil
}
