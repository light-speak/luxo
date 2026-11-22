package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/bits"
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
const encodedJWTSignatureSize = (sha256.Size*8 + 5) / 6

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

	expires, err := jwtExpiryFromEnv("JWT_EXPIRES", 7*24*time.Hour)
	if err != nil {
		return nil, err
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

	refreshExpires, err := jwtExpiryFromEnv("JWT_REFRESH_EXPIRES", 30*24*time.Hour)
	if err != nil {
		return nil, err
	}

	return &Config{
		Secret:         secret,
		Expires:        expires,
		RefreshEnabled: refreshEnabled,
		RefreshExpires: refreshExpires,
	}, nil
}

func jwtExpiryFromEnv(key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := env.Get(key)
	if !ok {
		return fallback, nil
	}
	duration, err := parseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	if duration <= 0 || strings.HasPrefix(strings.TrimSpace(raw), "-") {
		return 0, fmt.Errorf("invalid %s: duration must be positive", key)
	}
	return duration, nil
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
	issuedAt := now.Unix()
	expiresAt := now.Add(expires).Unix()
	if expires > 0 && expiresAt <= issuedAt {
		return "", fmt.Errorf("JWT lifetime must reach a future whole-second deadline")
	}
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
	envelope := strconv.AppendInt(append(payload, `,"exp":`...), expiresAt, 10)
	envelope = strconv.AppendInt(append(envelope, `,"iat":`...), issuedAt, 10)
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
	header, rest, _ := strings.Cut(token, ".")
	encodedPayload, signature, ok := strings.Cut(rest, ".")
	if !ok {
		return Claims{}, 0, fmt.Errorf("invalid token format")
	}

	// Verify signature
	signed := token[:len(header)+1+len(encodedPayload)]
	var encoded [encodedJWTSignatureSize]byte
	if !hmac.Equal([]byte(signature), appendJWTSignature(encoded[:0], signed, cfg.Secret)) {
		return Claims{}, 0, fmt.Errorf("invalid signature")
	}
	if err := validateJWTHeader(header); err != nil {
		return Claims{}, 0, err
	}

	// Decode payload
	payload, err := base64Decode(encodedPayload)
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
	var encoded [encodedJWTSignatureSize]byte
	return string(appendJWTSignature(encoded[:0], message, secret))
}

func appendJWTSignature(dst []byte, message, secret string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	return base64.RawURLEncoding.AppendEncode(dst, mac.Sum(nil))
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
	s = strings.TrimSuffix(s, "+")
	hasMultiplier := false
	for i := range len(s) {
		switch s[i] {
		case 'd', 'w':
			return parseDayWeekDuration(s, i)
		case '*':
			hasMultiplier = true
		}
	}
	if hasMultiplier {
		return parseDurationWithMultiplier(s)
	}
	return time.ParseDuration(s)
}

func parseDayWeekDuration(s string, unit int) (time.Duration, error) {
	if unit == len(s)-1 {
		scale := 24 * time.Hour
		if s[unit] == 'w' {
			scale *= 7
		}
		if duration, err := scaleDuration(s[:unit], scale, s); err != errInvalidDurationMultiplier {
			return duration, err
		}
	}
	return parseExtendedDuration(s)
}

func parseExtendedDuration(s string) (time.Duration, error) {
	var normalized strings.Builder
	start, numberStart := 0, 0
	for i := 0; i < len(s); i++ {
		unit := s[i]
		if unit != 'd' && unit != 'w' {
			if (unit < '0' || unit > '9') && unit != '.' {
				numberStart = i + 1
			}
			continue
		}
		// Ordinary Go duration units end in h, m, or s. Separate that
		// preceding duration from the number being scaled.
		if numberStart > start && strings.IndexByte("hms", s[numberStart-1]) >= 0 {
			normalized.WriteString(s[start:numberStart])
			normalized.WriteByte('+')
			start = numberStart
		}
		normalized.WriteString(s[start:i])
		if unit == 'd' {
			normalized.WriteString("*24h+")
		} else {
			normalized.WriteString("*168h+")
		}
		start, numberStart = i+1, i+1
	}
	normalized.WriteString(s[start:])
	return parseDurationWithMultiplier(strings.TrimSuffix(normalized.String(), "+"))
}

func parseDurationWithMultiplier(s string) (time.Duration, error) {
	var total time.Duration
	for part := range strings.SplitSeq(s, "+") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var duration time.Duration
		if idx := strings.Index(part, "*"); idx >= 0 {
			numStr := part[:idx]
			durStr := part[idx+1:]
			d, err := time.ParseDuration(durStr)
			if err != nil {
				return 0, fmt.Errorf("invalid duration %q: %w", s, err)
			}
			duration, err = scaleDuration(numStr, d, s)
			if err == errInvalidDurationMultiplier {
				return 0, fmt.Errorf("invalid duration %q", s)
			}
			if err != nil {
				return 0, err
			}
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

var errInvalidDurationMultiplier = errors.New("invalid duration multiplier")

func scaleDuration(raw string, unit time.Duration, input string) (time.Duration, error) {
	value, divisor, negative := decimalDurationMultiplier(raw)
	if divisor == 0 {
		value, divisor, negative = scientificDurationMultiplier(raw)
	}
	if divisor == 0 {
		value, divisor, negative = hexDurationMultiplier(raw)
	}
	if divisor == 0 {
		return scaleOtherDurationMultiplier(raw, unit, input)
	}
	return scaleDurationRatio(value, divisor, negative, unit, input)
}

// Up to 19 decimal digits fit in uint64 without rounding. Exponents, digit
// separators, hex floats, and longer decimals use separate paths.
func decimalDurationMultiplier(raw string) (value, divisor uint64, negative bool) {
	if raw == "" {
		return 0, 0, false
	}
	if raw[0] == '-' || raw[0] == '+' {
		negative, raw = raw[0] == '-', raw[1:]
	}
	if len(raw) > 20 {
		return 0, 0, false
	}
	divisor = 1
	digits, fraction := 0, false
	for i := range len(raw) {
		digit := raw[i] - '0'
		if raw[i] == '.' && !fraction {
			fraction = true
			continue
		}
		if digit > 9 || digits == 19 {
			return 0, 0, false
		}
		value = value*10 + uint64(digit)
		digits++
		if fraction {
			divisor *= 10
		}
	}
	if digits == 0 {
		return 0, 0, false
	}
	return value, divisor, negative
}

func scientificDurationMultiplier(raw string) (value, divisor uint64, negative bool) {
	marker := strings.IndexAny(raw, "eE")
	if marker < 1 {
		return 0, 0, false
	}
	exp, err := strconv.Atoi(raw[marker+1:])
	if err != nil || exp < -19 || exp > 19 {
		return 0, 0, false
	}
	value, divisor, negative = decimalDurationMultiplier(raw[:marker])
	if divisor == 0 {
		return 0, 0, false
	}
	for ; exp > 0 && divisor > 1; exp-- {
		divisor /= 10
	}
	for ; exp > 0; exp-- {
		if value > math.MaxUint64/10 {
			return 0, 0, false
		}
		value *= 10
	}
	for ; exp < 0; exp++ {
		if divisor > math.MaxUint64/10 {
			return 0, 0, false
		}
		divisor *= 10
	}
	return value, divisor, negative
}

func hexDurationMultiplier(raw string) (value, divisor uint64, negative bool) {
	if raw == "" {
		return 0, 0, false
	}
	if raw[0] == '+' || raw[0] == '-' {
		negative, raw = raw[0] == '-', raw[1:]
	}
	if len(raw) < 4 || raw[0] != '0' || raw[1]|0x20 != 'x' {
		return 0, 0, false
	}
	marker := strings.IndexAny(raw, "pP")
	if marker < 3 {
		return 0, 0, false
	}
	exp, err := strconv.Atoi(raw[marker+1:])
	if err != nil || exp < -1024 || exp > 1024 {
		return 0, 0, false
	}
	value, fraction, ok := hexDurationSignificand(raw[2:marker])
	if !ok {
		return 0, 0, false
	}
	if value == 0 {
		return 0, 1, false
	}
	// Drop powers of two shared by the numerator and divisor before checking
	// the compact range. Wider valid ratios still use the exact fallback.
	trailing := bits.TrailingZeros64(value)
	value >>= trailing
	shift := exp - 4*fraction + trailing
	if shift < -63 || shift > bits.LeadingZeros64(value) {
		return 0, 0, false
	}
	if shift >= 0 {
		return value << shift, 1, negative
	}
	return value, uint64(1) << -shift, negative
}

func hexDurationSignificand(raw string) (value uint64, fraction int, ok bool) {
	digits, point := 0, false
	for i := range len(raw) {
		if raw[i] == '.' && !point {
			point = true
			continue
		}
		digit := raw[i] - '0'
		if digit > 9 {
			digit = (raw[i] | 0x20) - 'a'
			if digit > 5 {
				return 0, 0, false
			}
			digit += 10
		}
		if digits == 16 {
			return 0, 0, false
		}
		value = value<<4 | uint64(digit)
		digits++
		if point {
			fraction++
		}
	}
	return value, fraction, digits != 0
}

func scaleDurationRatio(value, divisor uint64, negative bool, unit time.Duration, input string) (time.Duration, error) {
	factor := uint64(unit)
	if unit < 0 {
		factor, negative = -factor, !negative
	}
	high, low := bits.Mul64(value, factor)
	if high >= divisor {
		return 0, fmt.Errorf("duration %q overflows", input)
	}
	var magnitude uint64
	if high == 0 {
		magnitude = low / divisor
	} else {
		magnitude, _ = bits.Div64(high, low, divisor)
	}
	if negative && magnitude <= 1<<63 {
		return time.Duration(-magnitude), nil
	}
	if !negative && magnitude <= math.MaxInt64 {
		return time.Duration(magnitude), nil
	}
	return 0, fmt.Errorf("duration %q overflows", input)
}

func scaleOtherDurationMultiplier(raw string, unit time.Duration, input string) (time.Duration, error) {
	number, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, errInvalidDurationMultiplier
	}
	magnitude := math.Abs(number * float64(unit))
	if math.IsNaN(magnitude) || magnitude >= 0x1p64 {
		return 0, fmt.Errorf("duration %q overflows", input)
	}
	if magnitude == 0 {
		return 0, nil
	}
	// Padding a decimal with trailing zeros must not enlarge its exact scale.
	if strings.Contains(raw, ".") && !strings.ContainsAny(raw, "_eEpP") {
		raw = strings.TrimRight(raw, "0")
	}
	var numberExact big.Rat
	if _, ok := numberExact.SetString(raw); !ok {
		return 0, fmt.Errorf("invalid duration %q", input)
	}
	var scaled, factor big.Int
	scaled.Mul(numberExact.Num(), factor.SetInt64(int64(unit)))
	scaled.Quo(&scaled, numberExact.Denom())
	if !scaled.IsInt64() {
		return 0, fmt.Errorf("duration %q overflows", input)
	}
	return time.Duration(scaled.Int64()), nil
}
