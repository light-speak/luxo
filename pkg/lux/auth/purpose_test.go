package auth

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestSignRequiresFutureDeadlineForPositiveLifetime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		time.Sleep(100 * time.Millisecond)
		cfg := testConfig()
		cfg.Expires, cfg.RefreshExpires = 500*time.Millisecond, 500*time.Millisecond
		for _, sign := range []func(*Config, ClaimsWriter) (string, error){Sign, SignRefresh} {
			if token, err := sign(cfg, testClaims(42, "ADMIN")); err == nil || token != "" {
				t.Fatal("signer returned a token whose deadline is the current second")
			}
		}

		cfg.Expires = 950 * time.Millisecond
		token, err := Sign(cfg, testClaims(42, "ADMIN"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(cfg, token); err != nil {
			t.Fatalf("fresh token rejected: %v", err)
		}
		time.Sleep(900 * time.Millisecond)
		if _, err := Verify(cfg, token); err == nil {
			t.Fatal("token remained valid at its signed deadline")
		}
	})
}

func TestTokensExpireAtExactBoundary(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ResetVerifyCache()
		t.Cleanup(ResetVerifyCache)
		cfg := testConfig()
		cfg.Expires, cfg.RefreshExpires = time.Second, time.Second
		access, err := Sign(cfg, testClaims(42, "ADMIN"))
		if err != nil {
			t.Fatal(err)
		}
		refresh, err := SignRefresh(cfg, testClaims(42, "ADMIN"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyCached(cfg, access); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second - time.Nanosecond)
		if _, err := VerifyCached(cfg, access); err != nil {
			t.Fatal("access expired before its deadline", err)
		}
		if _, err := VerifyRefresh(cfg, refresh); err != nil {
			t.Fatal("refresh expired before its deadline", err)
		}
		time.Sleep(time.Nanosecond)
		for name, verify := range map[string]func(*Config, string) (Claims, error){
			"access": Verify, "cached": VerifyCached, "refresh": VerifyRefresh,
		} {
			token := access
			if name == "refresh" {
				token = refresh
			}
			if claims, err := verify(cfg, token); err == nil || claims.Raw() != "" {
				t.Errorf("%s accepted token at exp", name)
			}
		}
		if count := verifyCacheLen(); count != 0 {
			t.Errorf("expired access identity retained: %d", count)
		}
	})
}

func TestAccessVerificationRejectsRefresh(t *testing.T) {
	ResetVerifyCache()
	t.Cleanup(ResetVerifyCache)
	cfg := testConfig()
	token, err := SignRefresh(cfg, testClaims(9007199254740993, "ADMIN"))
	if err != nil {
		t.Fatal(err)
	}
	for name, verify := range map[string]func(*Config, string) (Claims, error){
		"direct": Verify, "cached": VerifyCached,
	} {
		t.Run(name, func(t *testing.T) {
			for range 2 {
				if claims, err := verify(cfg, token); err == nil || claims.Raw() != "" {
					t.Fatal("refresh token established an access identity")
				}
			}
		})
	}
	if verifyCacheLen() != 0 {
		t.Fatal("refresh token populated the access cache")
	}
}

func TestSignedTokenPurposeIsExplicit(t *testing.T) {
	cfg := testConfig()
	for purpose, sign := range map[string]func(*Config, ClaimsWriter) (string, error){
		"access": Sign, "refresh": SignRefresh,
	} {
		t.Run(purpose, func(t *testing.T) {
			token, err := sign(cfg, testClaims(42, ""))
			if err != nil {
				t.Fatal(err)
			}
			payload, err := base64Decode(strings.Split(token, ".")[1])
			if err != nil || !strings.Contains(string(payload), `"token_use":"`+purpose+`"`) {
				t.Fatalf("missing signed purpose: payload=%s err=%v", payload, err)
			}
		})
	}
}

func TestAccessRequiresValidPurpose(t *testing.T) {
	cfg := testConfig()
	for _, suffix := range []string{
		"", `,"token_use":null`, `,"token_use":1`, `,"token_use":true`,
		`,"token_use":""`, `,"token_use":"refresh"`, `,"token_use":"unknown"`,
		`,"token_use":"access","token_use":"refresh"`,
	} {
		payload := `{"data":{"id":42},"exp":4102444800` + suffix + `}`
		token := signedPurposePayload(cfg, payload)
		if claims, err := Verify(cfg, token); err == nil || claims.Raw() != "" {
			t.Errorf("accepted invalid purpose: %s", suffix)
		}
	}
}

func signedPurposePayload(cfg *Config, payload string) string {
	header := base64Encode([]byte(`{"alg":"HS256","typ":"JWT"}`))
	unsigned := header + "." + base64Encode([]byte(payload))
	return unsigned + "." + hmacSign(unsigned, cfg.Secret)
}

func TestRefreshVerificationBoundary(t *testing.T) {
	ResetVerifyCache()
	t.Cleanup(ResetVerifyCache)
	cfg := testConfig()
	access, err := Sign(cfg, testClaims(42, "ADMIN"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCached(cfg, access); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyRefresh(cfg, access); err == nil {
		t.Fatal("cached access token used to refresh")
	}
	refresh, err := SignRefresh(cfg, testClaims(9007199254740993, "ADMIN"))
	if err != nil {
		t.Fatal(err)
	}
	claims, err := VerifyRefresh(cfg, refresh)
	if err != nil || claims.ID() != 9007199254740993 {
		t.Fatalf("refresh identity changed: %s err=%v", claims.Raw(), err)
	}
	if verifyCacheLen() != 1 {
		t.Fatal("refresh verification changed the access cache")
	}
	cfg.RefreshEnabled = false
	if claims, err := VerifyRefresh(cfg, refresh); err == nil || claims.Raw() != "" {
		t.Fatal("disabled refresh was accepted")
	}
	if _, err := VerifyCached(cfg, access); err != nil {
		t.Fatal("disabling refresh invalidated access", err)
	}
	if _, err := VerifyRefresh(nil, refresh); err == nil {
		t.Fatal("nil refresh config accepted")
	}
}

func TestPurposeEnvelopeRejectsMalformedValues(t *testing.T) {
	for _, payload := range []string{
		`{"data":{},"exp":4102444800,"token_use":`,
		`{"data":{},"exp":4102444800,"token_use":"\uD800"}`,
		`{"data":{},"exp":4102444800,"token_use":[]}`,
		`{"data":{},"exp":4102444800,"token_use":"access","token_use":"access"}`,
	} {
		if _, _, err := readTokenClaims([]byte(payload), accessPurpose); err == nil {
			t.Errorf("malformed purpose accepted: %s", payload)
		}
	}
}

func TestVerifyCachedWithExpiryPreservesSignedDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ResetVerifyCache()
		t.Cleanup(ResetVerifyCache)
		cfg := testConfig()
		cfg.Expires = time.Second
		token, err := Sign(cfg, testClaims(42, "ADMIN"))
		if err != nil {
			t.Fatal(err)
		}
		expected := time.Now().Add(time.Second).Unix()
		for range 2 {
			claims, expiry, err := VerifyCachedWithExpiry(cfg, token)
			if err != nil || claims.ID() != 42 || expiry != expected {
				t.Fatalf("claims=%s expiry=%d err=%v", claims.Raw(), expiry, err)
			}
		}
		time.Sleep(time.Second)
		claims, expiry, err := VerifyCachedWithExpiry(cfg, token)
		if err == nil || claims.Raw() != "" || expiry != 0 {
			t.Fatalf("expired identity retained: %s %d %v", claims.Raw(), expiry, err)
		}
		for _, config := range []*Config{nil, {Secret: "different"}} {
			claims, expiry, err := VerifyCachedWithExpiry(config, token)
			if err == nil || claims.Raw() != "" || expiry != 0 {
				t.Fatal("invalid verifier retained deadline")
			}
		}
	})
}
