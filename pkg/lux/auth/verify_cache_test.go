package auth

import (
	"testing"
	"time"
)

func cacheTestConfig() *Config {
	return &Config{Secret: "cache-test-secret", Expires: time.Hour}
}

func TestVerifyCachedHit(t *testing.T) {
	ResetVerifyCache()
	cfg := cacheTestConfig()
	token, err := Sign(cfg, map[string]any{"id": int64(42), "role": "ADMIN"})
	if err != nil {
		t.Fatal(err)
	}

	// First call — miss, full verify
	if _, err := VerifyCached(cfg, token); err != nil {
		t.Fatal(err)
	}
	if n := verifyCacheLen(); n != 1 {
		t.Errorf("cache should hold 1 entry after first verify, has %d", n)
	}
	// Second call — hit, must return the same claims
	data, err := VerifyCached(cfg, token)
	if err != nil {
		t.Fatal(err)
	}
	if data["role"] != "ADMIN" {
		t.Errorf("role = %v, want ADMIN", data["role"])
	}
}

func TestVerifyCachedInvalidNotCached(t *testing.T) {
	ResetVerifyCache()
	cfg := cacheTestConfig()
	if _, err := VerifyCached(cfg, "not.a.token"); err == nil {
		t.Fatal("invalid token should fail")
	}
	// Failure must not fill the cache
	if n := verifyCacheLen(); n != 0 {
		t.Errorf("cache should stay empty after failures, has %d entries", n)
	}
}

func TestVerifyCachedExpired(t *testing.T) {
	ResetVerifyCache()
	cfg := cacheTestConfig()
	token, err := SignWithExpiry(cfg, map[string]any{"id": int64(1)}, -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCached(cfg, token); err == nil {
		t.Fatal("expired token should fail")
	}
}

func TestVerifyCachedExpiresWhileCached(t *testing.T) {
	ResetVerifyCache()
	cfg := cacheTestConfig()
	token, err := Sign(cfg, map[string]any{"id": int64(1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCached(cfg, token); err != nil {
		t.Fatal(err)
	}
	// Force-expire the cached entry (white-box) — deterministic, no sleeps
	sh := &verifyCache[verifyShardIndex(token)]
	sh.mu.Lock()
	e := sh.m[token]
	e.exp = time.Now().Unix() - 1
	sh.m[token] = e
	sh.mu.Unlock()
	// Cached entry must not outlive its exp, and must be evicted on lookup
	if _, err := VerifyCached(cfg, token); err == nil {
		t.Fatal("cached entry must expire with the token")
	}
	if n := verifyCacheLen(); n != 0 {
		t.Errorf("expired entry should be evicted, cache has %d entries", n)
	}
}

func TestVerifyCachedCapEviction(t *testing.T) {
	ResetVerifyCache()
	cfg := cacheTestConfig()
	// Overfill far beyond the cap — the cache must stay bounded
	for i := range verifyCacheMaxEntries * 2 {
		token, err := Sign(cfg, map[string]any{"id": int64(i)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyCached(cfg, token); err != nil {
			t.Fatal(err)
		}
	}
	if n := verifyCacheLen(); n > verifyCacheMaxEntries {
		t.Errorf("cache size %d exceeds cap %d", n, verifyCacheMaxEntries)
	}
}

func BenchmarkVerifyCached(b *testing.B) {
	ResetVerifyCache()
	cfg := cacheTestConfig()
	token, _ := Sign(cfg, map[string]any{"id": int64(42), "role": "ADMIN"})
	// warm
	if _, err := VerifyCached(cfg, token); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := VerifyCached(cfg, token); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVerifyUncached(b *testing.B) {
	cfg := cacheTestConfig()
	token, _ := Sign(cfg, map[string]any{"id": int64(42), "role": "ADMIN"})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Verify(cfg, token); err != nil {
			b.Fatal(err)
		}
	}
}
