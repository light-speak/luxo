package auth

import (
	"crypto/hmac"
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
	sig := hmacSign(header+"."+payloadB64, cfg.Secret)
	return header + "." + payloadB64 + "." + sig, nil
}

func legacyVerifyReference(cfg *Config, token string) (map[string]any, error) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}
	expected := hmacSign(parts[0]+"."+parts[1], cfg.Secret)
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

var staticTokenSink string
var staticClaimsSink Claims
var legacyClaimsSink map[string]any
var staticIDSink int64

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
