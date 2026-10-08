package auth

import (
	"fmt"
	"sync"
	"time"
)

// Token verification cache — the auth middleware verifies the same Bearer
// token on every request of a session. Caching verified claims removes the
// per-request HMAC + base64 + JSON decode from the hot path entirely.
//
// Safety: each entry is bound to the secret that actually verified it. A full
// token alone does not identify the verifier: another application can receive
// that same token while requiring a different signing key. Configuration must
// not be mutated concurrently with verification. Entries expire with exp.
//
// Claims own immutable values; no accessor exposes cache-owned mutable storage.
const (
	verifyCacheShards     = 16
	verifyCacheMaxEntries = 16384 // total cap; per-shard cap = total / shards
)

type verifyEntry struct {
	data   Claims
	exp    int64
	secret string
}

type verifyShard struct {
	mu sync.RWMutex
	m  map[string]verifyEntry
}

var verifyCache [verifyCacheShards]verifyShard

func init() {
	for i := range verifyCache {
		verifyCache[i].m = make(map[string]verifyEntry)
	}
}

// verifyShardIndex picks a shard from the token's signature tail — the
// highest-entropy part — via FNV-1a over at most the last 32 bytes.
func verifyShardIndex(token string) int {
	start := max(len(token)-32, 0)
	h := uint32(2166136261)
	for i := start; i < len(token); i++ {
		h ^= uint32(token[i])
		h *= 16777619
	}
	return int(h % verifyCacheShards)
}

// VerifyCached is Verify with a bounded verification cache. Use it on
// per-request paths (auth middleware); one-shot verifications (refresh
// tokens) must use VerifyRefresh. This cache contains access identities only.
func VerifyCached(cfg *Config, token string) (Claims, error) {
	if cfg == nil {
		return Claims{}, fmt.Errorf("JWT config is required")
	}
	sh := &verifyCache[verifyShardIndex(token)]
	now := time.Now().Unix()

	sh.mu.RLock()
	e, ok := sh.m[token]
	sh.mu.RUnlock()
	if ok && e.secret == cfg.Secret {
		if now < e.exp {
			return e.data, nil
		}
		sh.mu.Lock()
		delete(sh.m, token)
		sh.mu.Unlock()
		return Claims{}, fmt.Errorf("token expired")
	}

	data, exp, err := verifyFull(cfg, token)
	if err != nil {
		// Failures are never cached — an attacker spamming garbage tokens
		// must not be able to fill the cache.
		return Claims{}, err
	}

	sh.mu.Lock()
	if len(sh.m) >= verifyCacheMaxEntries/verifyCacheShards {
		// Evict one arbitrary entry — O(1), keeps the shard bounded under
		// sustained overflow without LRU bookkeeping on every hit.
		for k := range sh.m {
			delete(sh.m, k)
			break
		}
	}
	sh.m[token] = verifyEntry{data: data, exp: exp, secret: cfg.Secret}
	sh.mu.Unlock()
	return data, nil
}

// ResetVerifyCache clears all cached verifications (for testing only).
func ResetVerifyCache() {
	for i := range verifyCache {
		sh := &verifyCache[i]
		sh.mu.Lock()
		sh.m = make(map[string]verifyEntry)
		sh.mu.Unlock()
	}
}

// verifyCacheLen returns the total number of cached entries (for testing).
func verifyCacheLen() int {
	n := 0
	for i := range verifyCache {
		sh := &verifyCache[i]
		sh.mu.RLock()
		n += len(sh.m)
		sh.mu.RUnlock()
	}
	return n
}
