package api

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/ratelimit"
	"github.com/light-speak/luxo/pkg/lux/selection"
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

func cacheResponse(ctx context.Context, req *Request, identity string, ttl time.Duration, binding *ResponseCache, prefix string, execute func() error) error {
	key, err := cacheKeyInNamespace(req, identity, prefix)
	if err != nil {
		return err
	}
	if data, err := binding.backend.Get(ctx, key); err == nil && data != nil {
		req.Buf.B = append(req.Buf.B, data...)
		return nil
	}
	startLen := len(req.Buf.B)
	if err := binding.origin.run(ctx, execute); err != nil {
		return err
	}
	// Best-effort: ignore cache set errors.
	binding.backend.Set(ctx, key, req.Buf.B[startLen:], ttl)
	return nil
}

var errUnpreparedCacheRequest = errors.New("cache requires canonical binary request parameters")

func cacheKey(req *Request, identity string) (string, error) {
	return cacheKeyInNamespace(req, identity, "")
}

func cacheKeyInNamespace(req *Request, identity, prefix string) (string, error) {
	if req.Params == nil && (req.BinaryMode || req.paramCount > 0) && len(req.binaryParams) == 0 {
		return "", errUnpreparedCacheRequest
	}
	h := sha256.New()
	var word [8]byte
	integer := func(value int) {
		binary.LittleEndian.PutUint64(word[:], uint64(value))
		h.Write(word[:])
	}
	part := func(value []byte) {
		integer(len(value))
		h.Write(value)
	}
	part([]byte("luxo-response-cache-v2"))
	part([]byte(req.API))
	// String-to-byte conversions longer than the compiler's stack buffer can
	// allocate even when they do not escape. Hash through bounded stack scratch.
	integer(len(identity))
	var identityBytes [4 * sha256.BlockSize]byte
	for len(identity) > 0 {
		n := copy(identityBytes[:], identity)
		h.Write(identityBytes[:n])
		identity = identity[n:]
	}
	if req.Internal {
		integer(1)
	} else {
		integer(0)
	}
	part(req.FieldMask)
	if len(req.FieldMask) == 0 {
		part([]byte(selection.Format(req.Select)))
	}
	if req.Params == nil && (req.BinaryMode || req.paramCount > 0) {
		integer(1)
		part(req.binaryParams)
	} else {
		integer(0)
		var small [16]string
		names := sortedCacheParamNames(req, small[:0])
		integer(len(names))
		for _, name := range names {
			part([]byte(name))
			part(req.Params[name])
		}
	}
	integer(req.Page)
	integer(req.PageSize)
	integer(len(req.Filters))
	for _, filter := range req.Filters {
		part([]byte(filter.Field))
		part([]byte(filter.Operator))
		part([]byte(filter.Value))
	}
	integer(len(req.Sorters))
	for _, sorter := range req.Sorters {
		part([]byte(sorter.Field))
		part([]byte(sorter.Order))
	}
	var checksum [sha256.Size]byte
	var encoded [sha256.Size * 2]byte
	hex.Encode(encoded[:], h.Sum(checksum[:0]))
	return prefix + req.API + ":" + string(encoded[:]), nil
}

func sortedCacheParamNames(req *Request, names []string) []string {
	for name := range req.Params {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
