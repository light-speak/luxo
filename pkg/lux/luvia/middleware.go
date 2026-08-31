package luvia

import (
	"context"
	"net/http"
	"strings"

	"github.com/light-speak/luxo/pkg/lux/auth"
)

type identityCtxKey struct{}
type bearerTokenCtxKey struct{}

var identityKey identityCtxKey

// Claims wraps JWT claims data with type-safe accessors.
// This is the `my` keyword in Luxo — access the current user's data.
type Claims struct {
	data map[string]any
}

// ID returns the "id" claim as int64. Returns 0 if missing or wrong type.
func (c *Claims) ID() int64 {
	return c.Int("id")
}

// String returns a string claim by key. Returns "" if missing or wrong type.
func (c *Claims) String(key string) string {
	if v, ok := c.data[key].(string); ok {
		return v
	}
	return ""
}

// Int returns an integer claim by key. Returns 0 if missing or wrong type.
// Handles both int64 and float64 (JSON numbers decode as float64).
func (c *Claims) Int(key string) int64 {
	switch v := c.data[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

// Float returns a float64 claim by key. Returns 0 if missing or wrong type.
func (c *Claims) Float(key string) float64 {
	switch v := c.data[key].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	}
	return 0
}

// Bool returns a boolean claim by key. Returns false if missing or wrong type.
func (c *Claims) Bool(key string) bool {
	v, _ := c.data[key].(bool)
	return v
}

// Has reports whether a claim key exists.
func (c *Claims) Has(key string) bool {
	_, ok := c.data[key]
	return ok
}

// Get returns a claim value by key. Returns nil if missing.
func (c *Claims) Get(key string) any {
	return c.data[key]
}

// Raw returns the underlying map for cases requiring direct access.
func (c *Claims) Raw() map[string]any {
	return c.data
}

// Identity returns the authenticated user's claims from context.
// Returns nil if no valid JWT was present on the request.
func Identity(ctx context.Context) *Claims {
	v, _ := ctx.Value(identityKey).(*Claims)
	return v
}

// BearerToken returns the verified bearer token associated with the request.
// It is intended for trusted proxy handlers that must preserve end-user auth.
func BearerToken(ctx context.Context) string {
	token, _ := ctx.Value(bearerTokenCtxKey{}).(string)
	return token
}

// AuthMiddleware wraps an http.Handler to extract and verify JWT from
// the Authorization header. On success, injects claims into context.
// On failure (invalid/expired token), the request continues without identity.
// Handlers with @auth will check Identity() and reject if nil.
func AuthMiddleware(cfg *auth.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerToken(r)
		if token == "" && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			token = r.URL.Query().Get("token")
		}
		if token != "" {
			// Cached verify — same Bearer token repeats across a session's
			// requests; the cache removes HMAC + JSON decode from the hot path.
			data, err := auth.VerifyCached(cfg, token)
			if err == nil {
				ctx := context.WithValue(r.Context(), identityKey, &Claims{data: data})
				ctx = context.WithValue(ctx, bearerTokenCtxKey{}, token)
				r = r.WithContext(ctx)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// extractBearerToken extracts the JWT from "Authorization: Bearer <token>".
func extractBearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return h[7:]
	}
	return ""
}
