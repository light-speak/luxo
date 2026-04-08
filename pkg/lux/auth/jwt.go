package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/light-speak/luxo/pkg/lux/env"
)

// Claims represents JWT payload claims.
type Claims struct {
	Data      map[string]any `json:"data"`
	ExpiresAt int64          `json:"exp"`
	IssuedAt  int64          `json:"iat"`
}

// Config holds JWT configuration read from .env.
type Config struct {
	Secret         string
	Expires        time.Duration
	RefreshEnabled bool
	RefreshExpires time.Duration
}

// LoadConfig reads JWT config from environment.
func LoadConfig() (*Config, error) {
	secret, ok := env.Get("JWT_SECRET")
	if !ok {
		return nil, fmt.Errorf("JWT_SECRET is not set")
	}

	expires := 7 * 24 * time.Hour // default 7d
	if v, ok := env.Get("JWT_EXPIRES"); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid JWT_EXPIRES: %w", err)
		}
		expires = d
	}

	refreshEnabled := false
	if v, ok := env.Get("JWT_REFRESH"); ok {
		refreshEnabled = v == "true"
	}

	refreshExpires := 30 * 24 * time.Hour // default 30d
	if v, ok := env.Get("JWT_REFRESH_EXPIRES"); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid JWT_REFRESH_EXPIRES: %w", err)
		}
		refreshExpires = d
	}

	return &Config{
		Secret:         secret,
		Expires:        expires,
		RefreshEnabled: refreshEnabled,
		RefreshExpires: refreshExpires,
	}, nil
}

// Sign creates a JWT token with the given claims data.
func Sign(cfg *Config, data map[string]any) (string, error) {
	return SignWithExpiry(cfg, data, cfg.Expires)
}

// SignWithExpiry creates a JWT with a custom expiry duration.
func SignWithExpiry(cfg *Config, data map[string]any, expires time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		Data:      data,
		ExpiresAt: now.Add(expires).Unix(),
		IssuedAt:  now.Unix(),
	}

	header := base64Encode([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal claims: %w", err)
	}
	payloadB64 := base64Encode(payload)

	sig := hmacSign(header+"."+payloadB64, cfg.Secret)
	return header + "." + payloadB64 + "." + sig, nil
}

// Verify parses and validates a JWT token. Returns the claims data if valid.
func Verify(cfg *Config, token string) (map[string]any, error) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	// Verify signature
	expectedSig := hmacSign(parts[0]+"."+parts[1], cfg.Secret)
	if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
		return nil, fmt.Errorf("invalid signature")
	}

	// Decode payload
	payload, err := base64Decode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}

	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal claims: %w", err)
	}

	// Check expiry
	if time.Now().Unix() > claims.ExpiresAt {
		return nil, fmt.Errorf("token expired")
	}

	return claims.Data, nil
}

// SignRefresh creates a refresh token with longer expiry.
func SignRefresh(cfg *Config, data map[string]any) (string, error) {
	if !cfg.RefreshEnabled {
		return "", fmt.Errorf("refresh tokens are not enabled")
	}
	return SignWithExpiry(cfg, data, cfg.RefreshExpires)
}

func hmacSign(message, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	return base64Encode(mac.Sum(nil))
}

func base64Encode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func base64Decode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
