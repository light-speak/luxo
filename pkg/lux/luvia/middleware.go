package luvia

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/auth"
)

type identityCtxKey struct{}
type bearerTokenCtxKey struct{}

var identityKey identityCtxKey

// Claims exposes the same immutable, exact identity used by JWT verification.
// This is the `my` keyword in Luxo — access the current user's data.
type Claims = auth.Claims

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
		if token == "" && r.Method == http.MethodGet && api.IsWebSocketUpgrade(r) {
			token = r.URL.Query().Get("token")
		}
		if token != "" {
			// Cached verify — same Bearer token repeats across a session's
			// requests; the cache removes HMAC + JSON decode from the hot path.
			data, expiry, err := auth.VerifyCachedWithExpiry(cfg, token)
			if err == nil {
				ctx := context.WithValue(r.Context(), identityKey, &data)
				ctx = context.WithValue(ctx, bearerTokenCtxKey{}, token)
				if r.Method == http.MethodGet && api.IsWebSocketUpgrade(r) {
					ctx = api.WithWebSocketTokenExpiry(ctx, time.Unix(expiry, 0))
				}
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}
		if Identity(r.Context()) != nil || BearerToken(r.Context()) != "" {
			ctx := context.WithValue(r.Context(), identityKey, (*Claims)(nil))
			ctx = context.WithValue(ctx, bearerTokenCtxKey{}, "")
			ctx = api.WithoutWebSocketTokenExpiry(ctx)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

// extractBearerToken extracts the JWT from "Authorization: Bearer <token>".
func extractBearerToken(r *http.Request) string {
	// Authorization is already canonical. Keep Header.Get's first-value rule.
	values := r.Header["Authorization"]
	if len(values) == 0 {
		return ""
	}
	h := values[0]
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return h[7:]
	}
	return ""
}
