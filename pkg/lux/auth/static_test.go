package auth

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
)

func rawClaimsWriter(raw string) ClaimsWriter {
	return func(dst []byte) ([]byte, error) { return append(dst, raw...), nil }
}

func TestVerifyRejectsInvalidSignedHeader(t *testing.T) {
	ResetVerifyCache()
	t.Cleanup(ResetVerifyCache)
	cfg := testConfig()
	for _, raw := range []string{
		`{}`, `null`, `[]`, `not json`, `{"alg":"none"}`, `{"alg":"RS256"}`,
		`{"alg":null}`, `{"alg":256}`, `{"alg":"HS256","alg":"HS256"}`,
		`{"alg":"HS256","\u0061lg":"HS256"}`, `{"alg":"HS256"} true`,
		`{"alg":"HS256","crit":["custom"],"custom":true}`,
		`{"alg":"HS256","crit":[]}`, `{"alg":"HS256","b64":false}`,
		`{"alg":"HS256","b64":"true"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			for _, purpose := range []string{accessPurpose, refreshPurpose} {
				payload := `{"data":{"id":42},"exp":4102444800,"token_use":"` + purpose + `"}`
				unsigned := base64Encode([]byte(raw)) + "." + base64Encode([]byte(payload))
				token := unsigned + "." + hmacSign(unsigned, cfg.Secret)
				verify := VerifyRefresh
				if purpose == accessPurpose {
					verify = Verify
				}
				if claims, err := verify(cfg, token); err == nil || claims.Raw() != "" {
					t.Errorf("accepted %s header: %s", purpose, raw)
				}
				if purpose == accessPurpose {
					for range 2 {
						if claims, err := VerifyCached(cfg, token); err == nil || claims.Raw() != "" {
							t.Errorf("cached invalid header: %s", raw)
						}
					}
				}
			}
		})
	}
	if got := verifyCacheLen(); got != 0 {
		t.Errorf("invalid headers populated cache: %d", got)
	}
}

func TestJWTHeaderValidation(t *testing.T) {
	for _, raw := range []string{
		`{"alg":"HS256"}`, ` { "typ": "JWT", "alg": "HS256" } `,
		`{"\u0061lg":"HS\u003256"}`, `{"alg":"HS256","kid":"example"}`,
		`{"alg":"HS256","b64":true}`,
	} {
		if err := validateJWTHeader(base64Encode([]byte(raw))); err != nil {
			t.Errorf("valid header %s: %v", raw, err)
		}
	}
	if err := validateJWTHeader(signedJWTHeader); err != nil {
		t.Fatal(err)
	}
	for _, encoded := range []string{"", "!", "not-base64!"} {
		if err := validateJWTHeader(encoded); err == nil {
			t.Errorf("accepted invalid header encoding %q", encoded)
		}
	}
}

func TestStaticAuthRoundTripAndOwnership(t *testing.T) {
	ResetVerifyCache()
	t.Cleanup(ResetVerifyCache)
	cfg := testConfig()
	for _, id := range []int64{math.MinInt64, 9007199254740993, math.MaxInt64} {
		raw := `{"id":` + strconv.FormatInt(id, 10) + `,"number":123456789012345678901234567890,"decimal":0.123456789012345678901234567890}`
		token, err := Sign(cfg, rawClaimsWriter(raw))
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			claims, err := VerifyCached(cfg, token)
			if err != nil || claims.ID() != id || claims.Raw() != raw {
				t.Fatalf("identity corrupted: %s %v", claims.Raw(), err)
			}
			refreshed, err := SignRefresh(cfg, claims.AppendJSON)
			if err != nil {
				t.Fatal(err)
			}
			read, err := VerifyRefresh(cfg, refreshed)
			if err != nil || read.Raw() != raw {
				t.Fatal("renewal changed claims")
			}
			claims = Claims{}
			if claims.Raw() != "" || claims.ID() != 0 {
				t.Fatal("zero Claims is not empty")
			}
		}
	}
}

func TestStaticSignRejectsMalformedWriter(t *testing.T) {
	for _, invalid := range []ClaimsWriter{
		func(_ []byte) ([]byte, error) { return []byte(`{}`), nil },
		func(dst []byte) ([]byte, error) {
			if len(dst) > 0 {
				dst[0] = '['
			}
			return append(dst, "{}"...), nil
		},
	} {
		if value, err := Sign(testConfig(), invalid); err == nil || value != "" {
			t.Fatal("writer replaced its prefix")
		}
	}
	for _, raw := range []string{"", "null", "[]", `{"id":1} false`, `{"id":1,"id":2}`, `{"id":1,"\u0069d":2}`, `{"role":"\uD800"}`} {
		if token, err := Sign(testConfig(), rawClaimsWriter(raw)); err == nil || token != "" {
			t.Errorf("signed %q", raw)
		}
	}
	want := errors.New("writer failed")
	if token, err := Sign(testConfig(), func(dst []byte) ([]byte, error) { return append(dst, '{'), want }); token != "" || !errors.Is(err, want) {
		t.Fatal("writer error was lost")
	}
}

func TestStaticAuthNilBoundaries(t *testing.T) {
	if _, err := Sign(nil, testClaims(1, "")); err == nil {
		t.Error("nil config")
	}
	if _, err := Sign(testConfig(), nil); err == nil {
		t.Error("nil writer")
	}
	if _, err := SignWithExpiry(nil, testClaims(1, ""), 0); err == nil {
		t.Error("nil config with expiry")
	}
	if _, err := Sign(&Config{Secret: " "}, testClaims(1, "")); err == nil {
		t.Error("empty secret")
	}
	if _, err := SignRefresh(nil, testClaims(1, "")); err == nil {
		t.Error("nil refresh config")
	}
	if _, err := Verify(nil, "x"); err == nil {
		t.Error("nil verification config")
	}
	if _, err := VerifyCached(nil, "x"); err == nil {
		t.Error("nil cached config")
	}
	var claims Claims
	if out, err := claims.AppendJSON([]byte("prefix")); err == nil || string(out) != "prefix" {
		t.Error("empty claims did not preserve prefix")
	}
	if claims.Has("x") || claims.Get("x").Raw() != "" {
		t.Error("empty claims have fields")
	}
}

func TestStaticJWTEnvelope(t *testing.T) {
	for _, raw := range []string{"[]", `{"data":{},`, `{"data":{},"exp":`, `{"data":{},"exp":123} false`} {
		if claims, _, err := readTokenClaims([]byte(raw), accessPurpose); err == nil || claims.Raw() != "" {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, raw := range []string{`{}`, `{"data":{},"exp":null}`, `{"data":{},"exp":1.5}`, `{"data":null,"exp":1}`, `{"data":{},"exp":1,"exp":2}`, `{"data":{},"exp":1,"iat":false}`, `{"data":{},"exp":1,"iat":1,"iat":2}`} {
		if claims, _, err := readTokenClaims([]byte(raw), accessPurpose); err == nil || claims.Raw() != "" {
			t.Errorf("accepted %s", raw)
		}
	}
	if claims, exp, err := readTokenClaims([]byte(`{"data":{},"exp":123,"iat":1,"extra":true,"token_use":"access"}`), accessPurpose); err != nil || exp != 123 || claims.Raw() != "{}" {
		t.Fatal("valid envelope rejected")
	}
	claims, err := parseClaims([]byte(`{"negativeZero":-0.0,"huge":1e999,"false":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := claims.Get("negativeZero").Float64(); !ok || !math.Signbit(value) {
		t.Fatal("negative zero lost")
	}
	if _, ok := claims.Get("huge").Float64(); ok {
		t.Fatal("infinite conversion accepted")
	}
	if value, ok := claims.Get("false").Bool(); !ok || value {
		t.Fatal("false lost")
	}
}

func FuzzStaticClaims(f *testing.F) {
	for _, raw := range []string{`{}`, `{"id":9223372036854775807}`, `{"role":"\uD800"}`, `{"a":1,"a":2}`} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		claims, err := parseClaims([]byte(raw))
		if err != nil {
			return
		}
		if claims.Raw() != raw || !strings.HasPrefix(strings.TrimSpace(raw), "{") {
			t.Fatal("claims changed")
		}
		out, err := claims.AppendJSON(nil)
		if err != nil || string(out) != raw {
			t.Fatal("re-emission changed claims")
		}
	})
}
