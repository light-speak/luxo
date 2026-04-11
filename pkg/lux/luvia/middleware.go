package luvia

import (
	"context"
	"net/http"
	"strings"

	"github.com/light-speak/luxo/pkg/lux/auth"
)

type identityCtxKey struct{}

var identityKey identityCtxKey

// Identity returns the authenticated user's claims from context.
// This is the `my` keyword in Luxo — access the current user's data.
func Identity(ctx context.Context) map[string]any {
	v, _ := ctx.Value(identityKey).(map[string]any)
	return v
}

// AuthMiddleware wraps an http.Handler to extract and verify JWT from
// the Authorization header. On success, injects claims into context.
// On failure (invalid/expired token), the request continues without identity.
// Handlers with @auth will check Identity() and reject if nil.
func AuthMiddleware(cfg *auth.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerToken(r)
		if token != "" {
			claims, err := auth.Verify(cfg, token)
			if err == nil {
				ctx := context.WithValue(r.Context(), identityKey, claims)
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
