package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/jsontext"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/light-speak/luxo/pkg/lux/env"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

// ClaimsWriter appends one JSON object, preserving the supplied buffer prefix.
// Generated writers and Claims.AppendJSON satisfy this contract without maps,
// reflection, or decoding values merely to encode them again.
type ClaimsWriter func([]byte) ([]byte, error)

const signedJWTHeader = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"

const (
	accessPurpose  = "access"
	refreshPurpose = "refresh"
)

// Config holds JWT configuration read from .env.
type Config struct {
	Secret         string
	Expires        time.Duration
	RefreshEnabled bool
	RefreshExpires time.Duration
}

var (
	cachedConfig *Config
	configOnce   sync.Once
	configErr    error
)

// LoadConfig reads JWT config from environment.
// Config is cached after first load — zero allocation on subsequent calls.
func LoadConfig() (*Config, error) {
	configOnce.Do(func() {
		cachedConfig, configErr = loadConfigFromEnv()
	})
	return cachedConfig, configErr
}

// ResetConfig clears the cached config (for testing only).
func ResetConfig() {
	configOnce = sync.Once{}
	cachedConfig = nil
	configErr = nil
}

func loadConfigFromEnv() (*Config, error) {
	secret, ok := env.Get("JWT_SECRET")
	if !ok || strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("JWT_SECRET is not set")
	}

	expires := 7 * 24 * time.Hour // default 7d
	if v, ok := env.Get("JWT_EXPIRES"); ok {
		d, err := parseDuration(v)
		if err != nil || d <= 0 {
			if err == nil {
				err = fmt.Errorf("duration must be positive")
			}
			return nil, fmt.Errorf("invalid JWT_EXPIRES: %w", err)
		}
		expires = d
	}

	refreshEnabled := false
	if v, ok := env.Get("JWT_REFRESH"); ok {
		switch v {
		case "true":
			refreshEnabled = true
		case "false":
		default:
			return nil, fmt.Errorf("invalid JWT_REFRESH %q: expected true or false", v)
		}
	}

	refreshExpires := 30 * 24 * time.Hour // default 30d
	if v, ok := env.Get("JWT_REFRESH_EXPIRES"); ok {
		d, err := parseDuration(v)
		if err != nil || d <= 0 {
			if err == nil {
				err = fmt.Errorf("duration must be positive")
			}
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
func Sign(cfg *Config, data ClaimsWriter) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("JWT config is required")
	}
	return SignWithExpiry(cfg, data, cfg.Expires)
}

// SignWithExpiry creates a JWT with a custom expiry duration.
func SignWithExpiry(cfg *Config, data ClaimsWriter, expires time.Duration) (string, error) {
	return signWithPurpose(cfg, data, expires, accessPurpose)
}

func signWithPurpose(cfg *Config, data ClaimsWriter, expires time.Duration, purpose string) (string, error) {
	return signWithSubject(cfg, data, expires, purpose, "")
}

func signWithSubject(cfg *Config, data ClaimsWriter, expires time.Duration, purpose, subject string) (string, error) {
	if cfg == nil || strings.TrimSpace(cfg.Secret) == "" {
		return "", fmt.Errorf("JWT secret is required")
	}
	if data == nil {
		return "", fmt.Errorf("JWT claims writer is required")
	}
	now := time.Now()
	prefix := `{"token_use":"access","data":`
	if purpose == refreshPurpose {
		prefix = `{"token_use":"refresh","data":`
	}
	payload, err := data(append(make([]byte, 0, 128), prefix...))
	if err != nil {
		return "", fmt.Errorf("write claims: %w", err)
	}
	if len(payload) < len(prefix) || string(payload[:len(prefix)]) != prefix {
		return "", fmt.Errorf("JWT claims writer replaced its prefix")
	}
	// Validate native writers at the boundary without decoding/re-encoding their
	// values. The signed object retains the original numeric text.
	if err := jsonutil.VisitUniqueObject(payload[len(prefix):], func(_, _ []byte) error { return nil }); err != nil {
		return "", fmt.Errorf("invalid claims object: %w", err)
	}
	if subject != "" {
		payload, err = jsontext.AppendQuote(append(payload, `,"sub":`...), subject)
		if err != nil {
			return "", fmt.Errorf("invalid JWT subject: %w", err)
		}
	}
	envelope := strconv.AppendInt(append(payload, `,"exp":`...), now.Add(expires).Unix(), 10)
	envelope = strconv.AppendInt(append(envelope, `,"iat":`...), now.Unix(), 10)
	payloadB64 := base64Encode(append(envelope, '}'))

	sig := hmacSign(signedJWTHeader+"."+payloadB64, cfg.Secret)
	return signedJWTHeader + "." + payloadB64 + "." + sig, nil
}

// Verify accepts access tokens only. Returns the claims data if valid.
// Per-request paths should use VerifyCached instead.
func Verify(cfg *Config, token string) (Claims, error) {
	data, _, err := verifyFull(cfg, token)
	return data, err
}

// verifyFull validates a token and returns the claims data plus its expiry
// unix timestamp — the expiry lets VerifyCached bound cache entry lifetime.
func verifyFull(cfg *Config, token string) (Claims, int64, error) {
	return verifyPurpose(cfg, token, accessPurpose)
}

// VerifyRefresh accepts refresh tokens only when refresh support is enabled.
// It never inserts refresh identities into the access verification cache.
func VerifyRefresh(cfg *Config, token string) (Claims, error) {
	if cfg == nil {
		return Claims{}, fmt.Errorf("JWT config is required")
	}
	if !cfg.RefreshEnabled {
		return Claims{}, fmt.Errorf("refresh tokens are not enabled")
	}
	data, _, err := verifyPurpose(cfg, token, refreshPurpose)
	return data, err
}

func verifyPurpose(cfg *Config, token, purpose string) (Claims, int64, error) {
	if cfg == nil || strings.TrimSpace(cfg.Secret) == "" {
		return Claims{}, 0, fmt.Errorf("JWT secret is required")
	}
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return Claims{}, 0, fmt.Errorf("invalid token format")
	}

	// Verify signature
	expectedSig := hmacSign(parts[0]+"."+parts[1], cfg.Secret)
	if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
		return Claims{}, 0, fmt.Errorf("invalid signature")
	}
	if err := validateJWTHeader(parts[0]); err != nil {
		return Claims{}, 0, err
	}

	// Decode payload
	payload, err := base64Decode(parts[1])
	if err != nil {
		return Claims{}, 0, fmt.Errorf("decode payload: %w", err)
	}

	data, expiresAt, err := readTokenClaims(payload, purpose)
	if err != nil {
		return Claims{}, 0, err
	}
	if time.Now().Unix() >= expiresAt {
		return Claims{}, 0, fmt.Errorf("token expired")
	}

	return data, expiresAt, nil
}

// Our own fixed header needs no JSON parsing; alternate encodings still must
// declare HS256 in one complete, unambiguous object.
func validateJWTHeader(encoded string) error {
	if encoded == signedJWTHeader {
		return nil
	}
	raw, err := base64Decode(encoded)
	if err != nil {
		return fmt.Errorf("decode JWT header: %w", err)
	}
	var algorithm bool
	err = jsonutil.VisitUniqueObject(raw, func(name, value []byte) error {
		switch string(name) {
		case "crit":
			return fmt.Errorf("JWT critical header extensions are not supported")
		case "b64":
			if string(value) != "true" {
				return fmt.Errorf("JWT payload must use base64url encoding")
			}
			return nil
		case "alg":
		default:
			return nil
		}
		if string(value) == `"HS256"` {
			algorithm = true
			return nil
		}
		alg, err := jsonutil.ParseString(value)
		if err != nil || alg != "HS256" {
			return fmt.Errorf("JWT algorithm must be HS256")
		}
		algorithm = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("invalid JWT header: %w", err)
	}
	if !algorithm {
		return fmt.Errorf("JWT algorithm is required")
	}
	return nil
}

// SignRefresh creates a refresh token with longer expiry.
func SignRefresh(cfg *Config, data ClaimsWriter) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("JWT config is required")
	}
	if !cfg.RefreshEnabled {
		return "", fmt.Errorf("refresh tokens are not enabled")
	}
	return signWithPurpose(cfg, data, cfg.RefreshExpires, refreshPurpose)
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

// parseDuration extends time.ParseDuration with support for "d" (days) and "w" (weeks).
// Examples: "7d" → 168h, "2w" → 336h, "1d12h" → 36h, "30m" → 30m.
func parseDuration(s string) (time.Duration, error) {
	// Replace "d" and "w" with hour equivalents before parsing
	var result string
	i := 0
	for i < len(s) {
		j := i
		// Scan digits
		for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.') {
			j++
		}
		if j < len(s) && s[j] == 'd' {
			// Convert days to hours
			result += s[i:j] + "*24h+"
			i = j + 1
		} else if j < len(s) && s[j] == 'w' {
			// Convert weeks to hours
			result += s[i:j] + "*168h+"
			i = j + 1
		} else {
			result += string(s[i])
			i++
		}
	}
	result = strings.TrimSuffix(result, "+")

	// If simple substitution (e.g. "7*24h"), evaluate manually
	if strings.Contains(result, "*") {
		return parseDurationWithMultiplier(result)
	}
	return time.ParseDuration(result)
}

func parseDurationWithMultiplier(s string) (time.Duration, error) {
	var total time.Duration
	for _, part := range strings.Split(s, "+") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var duration time.Duration
		if idx := strings.Index(part, "*"); idx >= 0 {
			numStr := part[:idx]
			durStr := part[idx+1:]
			num, err := strconv.ParseFloat(numStr, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid duration %q", s)
			}
			d, err := time.ParseDuration(durStr)
			if err != nil {
				return 0, fmt.Errorf("invalid duration %q: %w", s, err)
			}
			product := num * float64(d)
			// The positive bound is exclusive: float64(MaxInt64) rounds to 2^63.
			if math.IsNaN(product) || math.IsInf(product, 0) || product >= 0x1p63 || product < -0x1p63 {
				return 0, fmt.Errorf("duration %q overflows", s)
			}
			duration = time.Duration(product)
		} else {
			d, err := time.ParseDuration(part)
			if err != nil {
				return 0, fmt.Errorf("invalid duration %q: %w", s, err)
			}
			duration = d
		}
		if duration > 0 && total > math.MaxInt64-duration || duration < 0 && total < math.MinInt64-duration {
			return 0, fmt.Errorf("duration %q overflows", s)
		}
		total += duration
	}
	return total, nil
}
