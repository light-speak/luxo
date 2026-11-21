package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The reference deliberately retains the old, lossy reflection implementation.
// It is a historical baseline, not an equivalent correctness implementation.
type legacyTokenClaims struct {
	Data      map[string]any `json:"data"`
	ExpiresAt int64          `json:"exp"`
	IssuedAt  int64          `json:"iat"`
}

func legacySignReference(cfg *Config, data map[string]any) (string, error) {
	now := time.Now()
	claims := legacyTokenClaims{Data: data, ExpiresAt: now.Add(cfg.Expires).Unix(), IssuedAt: now.Unix()}
	header := base64Encode([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payloadB64 := base64Encode(payload)
	sig := legacyHMACSign(header+"."+payloadB64, cfg.Secret)
	return header + "." + payloadB64 + "." + sig, nil
}

func legacyVerifyReference(cfg *Config, token string) (map[string]any, error) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}
	expected := legacyHMACSign(parts[0]+"."+parts[1], cfg.Secret)
	if !hmac.Equal([]byte(parts[2]), []byte(expected)) {
		return nil, fmt.Errorf("invalid signature")
	}
	payload, err := base64Decode(parts[1])
	if err != nil {
		return nil, err
	}
	var claims legacyTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	if time.Now().Unix() > claims.ExpiresAt {
		return nil, fmt.Errorf("token expired")
	}
	return claims.Data, nil
}

func legacyHMACSign(message, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	return base64Encode(mac.Sum(nil))
}

var staticTokenSink string
var staticClaimsSink Claims
var legacyClaimsSink map[string]any
var staticIDSink int64

func BenchmarkVerifyAlternateHeader(b *testing.B) {
	cfg := testConfig()
	payload := base64Encode([]byte(`{"data":{"id":42,"role":"ADMIN"},"exp":4102444800,"token_use":"access"}`))
	unsigned := base64Encode([]byte(`{"typ":"JWT","alg":"HS256"}`)) + "." + payload
	token := unsigned + "." + hmacSign(unsigned, cfg.Secret)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Verify(cfg, token); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStaticJWT(b *testing.B) {
	cfg := testConfig()
	data := map[string]any{"id": int64(42), "role": "ADMIN"}
	writer := testClaims(42, "ADMIN")
	token, err := Sign(cfg, writer)
	if err != nil {
		b.Fatal(err)
	}
	for _, phase := range []string{"Sign", "Verify", "Identity"} {
		for _, revision := range []string{"Before", "After"} {
			b.Run(phase+"/"+revision, func(b *testing.B) {
				benchmarkStaticJWTPhase(b, cfg, data, writer, token, phase, revision == "Before")
			})
		}
	}
}

func benchmarkStaticJWTPhase(b *testing.B, cfg *Config, data map[string]any, writer ClaimsWriter, token, phase string, before bool) {
	b.ReportAllocs()
	switch phase {
	case "Sign":
		if before {
			for b.Loop() {
				value, err := legacySignReference(cfg, data)
				if err != nil {
					b.Fatal(err)
				}
				staticTokenSink = value
			}
			return
		}
		for b.Loop() {
			value, err := Sign(cfg, writer)
			if err != nil {
				b.Fatal(err)
			}
			staticTokenSink = value
		}
	case "Verify":
		if before {
			for b.Loop() {
				value, err := legacyVerifyReference(cfg, token)
				if err != nil {
					b.Fatal(err)
				}
				legacyClaimsSink = value
			}
			return
		}
		for b.Loop() {
			value, err := Verify(cfg, token)
			if err != nil {
				b.Fatal(err)
			}
			staticClaimsSink = value
		}
	case "Identity":
		if before {
			value, err := legacyVerifyReference(cfg, token)
			if err != nil {
				b.Fatal(err)
			}
			for b.Loop() {
				staticIDSink = int64(value["id"].(float64))
			}
			return
		}
		value, err := Verify(cfg, token)
		if err != nil {
			b.Fatal(err)
		}
		for b.Loop() {
			staticIDSink = value.ID()
		}
	}
}

func TestStaticJWTBenchmarkReference(t *testing.T) {
	cfg := testConfig()
	token, err := legacySignReference(cfg, map[string]any{"id": int64(42), "role": "ADMIN"})
	if err != nil {
		t.Fatal(err)
	}
	value, err := Verify(cfg, token)
	if err == nil || value.Raw() != "" {
		t.Fatal("candidate accepted historical token without a purpose")
	}
	token, err = Sign(cfg, testClaims(42, "ADMIN"))
	if err != nil {
		t.Fatal(err)
	}
	old, err := legacyVerifyReference(cfg, token)
	if err != nil || old["id"] != float64(42) || old["role"] != "ADMIN" {
		t.Fatal("reference rejected candidate fixture", err)
	}
}

var permissionPredicateResult bool

// The old predicate incorrectly accepts absent or invalid text; this compares
// local predicate costs, not equivalent authorization or full request latency.
func BenchmarkPermissionTextValidity(b *testing.B) {
	for name, raw := range map[string]string{
		"member": `{"role":"member"}`, "empty": `{"role":""}`,
		"blocked": `{"role":"blocked"}`, "missing": `{}`,
	} {
		cfg := &Config{Secret: "predicate-benchmark", Expires: time.Hour}
		token, err := Sign(cfg, func(dst []byte) ([]byte, error) { return append(dst, raw...), nil })
		if err != nil {
			b.Fatal(err)
		}
		claims, err := Verify(cfg, token)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(name+"/before", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				permissionPredicateResult = claims.String("role") != "blocked"
			}
		})
		b.Run(name+"/strict", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				permissionPredicateResult = func() bool {
					value, valid := claims.Get("role").String()
					return valid && value != "blocked"
				}()
			}
		})
	}
}

// Compare subject-bearing credentials with otherwise identical unbound tokens.
// Unbound credentials are not an equivalent authentication contract.
func BenchmarkJWTSubjectBoundary(b *testing.B) {
	cfg := testConfig()
	writer := testClaims(9007199254740993, "MEMBER")
	for _, bound := range []bool{false, true} {
		name := "unbound"
		sign := func() (string, error) { return Sign(cfg, writer) }
		if bound {
			name = "bound"
			sign = func() (string, error) { return SignForSubject(cfg, "User", writer) }
		}
		token, err := sign()
		if err != nil {
			b.Fatal(err)
		}
		verified, err := Verify(cfg, token)
		if err != nil || verified.ID() != 9007199254740993 {
			b.Fatal("invalid benchmark identity", err)
		}
		if bound && verified.Subject() != "User" {
			b.Fatal("subject missing")
		}
		b.Run("sign/"+name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				value, err := sign()
				if err != nil {
					b.Fatal(err)
				}
				staticTokenSink = value
			}
		})
		b.Run("verify/"+name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				value, err := Verify(cfg, token)
				if err != nil {
					b.Fatal(err)
				}
				staticClaimsSink = value
			}
		})
		b.Run("cached/"+name, func(b *testing.B) {
			ResetVerifyCache()
			b.Cleanup(ResetVerifyCache)
			if _, err := VerifyCached(cfg, token); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				value, err := VerifyCached(cfg, token)
				if err != nil {
					b.Fatal(err)
				}
				staticClaimsSink = value
			}
		})
	}
}
