package auth

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func cacheTestConfig() *Config {
	return &Config{Secret: "cache-test-secret", Expires: time.Hour}
}

func TestVerifyCachedHit(t *testing.T) {
	ResetVerifyCache()
	cfg := cacheTestConfig()
	token, err := Sign(cfg, testClaims(42, "ADMIN"))
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
	if data.String("role") != "ADMIN" {
		t.Errorf("role = %v, want ADMIN", data.String("role"))
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

func TestVerifyCachedSeparatesVerificationSecrets(t *testing.T) {
	ResetVerifyCache()
	t.Cleanup(ResetVerifyCache)
	cfg := cacheTestConfig()
	token, err := Sign(cfg, testClaims(int64(42), ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCached(cfg, token); err != nil {
		t.Fatal(err)
	}
	originalSecret := cfg.Secret
	cfg.Secret = "another-application-secret"
	if _, err := Verify(cfg, token); err == nil {
		t.Fatal("uncached verification accepted a different secret")
	}
	if _, err := VerifyCached(cfg, token); err == nil {
		t.Error("cached verification accepted a different secret")
	}
	other := &Config{Secret: "third-application-secret", Expires: time.Hour}
	if _, err := VerifyCached(other, token); err == nil {
		t.Error("another application reused the first application's verification")
	}
	if n := verifyCacheLen(); n != 1 {
		t.Errorf("failed verification changed cache size: %d", n)
	}
	cfg.Secret = originalSecret
	if _, err := VerifyCached(cfg, token); err != nil {
		t.Fatalf("valid original configuration stopped working: %v", err)
	}
}

func TestVerifyCachedConcurrentVerifierIsolation(t *testing.T) {
	ResetVerifyCache()
	t.Cleanup(ResetVerifyCache)
	const workers = 16
	configs := make([]Config, workers)
	tokens, refresh := make([]string, workers), make([]string, workers)
	for i := range workers {
		configs[i] = Config{Secret: "isolated-verifier-" + strconv.Itoa(i), Expires: time.Hour,
			RefreshEnabled: true, RefreshExpires: time.Hour}
		var err error
		tokens[i], err = Sign(&configs[i], testClaims(9007199254740993+int64(i), "ADMIN"))
		if err != nil {
			t.Fatal(err)
		}
		refresh[i], err = SignRefresh(&configs[i], testClaims(9007199254740993+int64(i), "ADMIN"))
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Run("concurrent", func(t *testing.T) {
		for i := range workers {
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				t.Parallel()
				cfg := configs[i]
				cfg.Secret = strings.Clone(cfg.Secret)
				for range 64 {
					claims, err := VerifyCached(&cfg, tokens[i])
					if err != nil || claims.ID() != 9007199254740993+int64(i) {
						t.Fatalf("own identity changed: %d %v", claims.ID(), err)
					}
					for _, invalid := range []string{tokens[(i+1)%workers], refresh[i]} {
						if _, err := VerifyCached(&cfg, invalid); err == nil {
							t.Fatal("foreign verifier or refresh token established an access identity")
						}
					}
				}
			})
		}
	})
	if size := verifyCacheLen(); size != workers {
		t.Fatalf("unexpected cached identities: %d want %d", size, workers)
	}
}

func TestVerifyCachedExpired(t *testing.T) {
	ResetVerifyCache()
	cfg := cacheTestConfig()
	token, err := SignWithExpiry(cfg, testClaims(int64(1), ""), -time.Minute)
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
	token, err := Sign(cfg, testClaims(int64(1), ""))
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
		token, err := Sign(cfg, testClaims(int64(i), ""))
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
	token, _ := Sign(cfg, testClaims(42, "ADMIN"))
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
	token, _ := Sign(cfg, testClaims(42, "ADMIN"))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Verify(cfg, token); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVerifyCachedSecretBinding(b *testing.B) {
	for _, size := range []int{32, 512} {
		for _, copied := range []bool{false, true} {
			name := strconv.Itoa(size) + "/copied=" + strconv.FormatBool(copied)
			b.Run(name, func(b *testing.B) {
				ResetVerifyCache()
				cfg := &Config{Secret: strings.Repeat("x", size), Expires: time.Hour}
				token, err := Sign(cfg, testClaims(int64(42), ""))
				if err != nil {
					b.Fatal(err)
				}
				if _, err := VerifyCached(cfg, token); err != nil {
					b.Fatal(err)
				}
				if copied {
					cfg = &Config{Secret: strings.Clone(cfg.Secret), Expires: time.Hour}
				}
				b.ReportAllocs()
				for b.Loop() {
					if _, err := VerifyCached(cfg, token); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

var verificationStorageSink map[string]verifyEntry

// This isolates cold map storage; signature checks and payload parsing are not
// timed. The entry comes from actual verification in both compared revisions.
func BenchmarkVerificationEntryStorage(b *testing.B) {
	ResetVerifyCache()
	cfg := cacheTestConfig()
	token, err := Sign(cfg, testClaims(int64(42), ""))
	if err != nil {
		b.Fatal(err)
	}
	if _, err := VerifyCached(cfg, token); err != nil {
		b.Fatal(err)
	}
	entry := verifyCache[verifyShardIndex(token)].m[token]
	for _, count := range []int{1, 128} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			keys := make([]string, count)
			for i := range keys {
				keys[i] = strconv.Itoa(i)
			}
			b.Cleanup(func() { verificationStorageSink = nil })
			b.ReportAllocs()
			for b.Loop() {
				entries := make(map[string]verifyEntry, count)
				for _, key := range keys {
					entries[key] = entry
				}
				verificationStorageSink = entries
			}
		})
	}
}
