package luvia

import (
	"context"
	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The previous implementation is retained only as a local performance baseline.
func authMiddlewareBeforeIsolation(cfg *auth.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerToken(r)
		if token == "" && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			token = r.URL.Query().Get("token")
		}
		if token != "" {
			// Cached verify — same Bearer token repeats across a session's
			// requests; the cache removes HMAC + JSON decode from the hot path.
			data, expiry, err := auth.VerifyCachedWithExpiry(cfg, token)
			if err == nil {
				ctx := context.WithValue(r.Context(), identityKey, &data)
				ctx = context.WithValue(ctx, bearerTokenCtxKey{}, token)
				if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
					ctx = api.WithWebSocketTokenExpiry(ctx, time.Unix(expiry, 0))
				}
				r = r.WithContext(ctx)
			}
		}
		next.ServeHTTP(w, r)
	})
}

var middlewareIdentitySink *Claims
var middlewareTokenSink string

func BenchmarkAuthMiddlewareIsolation(b *testing.B) {
	auth.ResetVerifyCache()
	b.Cleanup(auth.ResetVerifyCache)
	cfg := testCfg()
	token, err := auth.Sign(cfg, testClaimsJSON(`{"id":42}`))
	if err != nil {
		b.Fatal(err)
	}
	claims, err := auth.VerifyCached(cfg, token)
	if err != nil {
		b.Fatal(err)
	}
	inherited := context.WithValue(context.Background(), identityKey, &claims)
	inherited = context.WithValue(inherited, bearerTokenCtxKey{}, token)
	inherited = api.WithWebSocketTokenExpiry(inherited, time.Now().Add(time.Hour))
	for _, sample := range []string{"valid", "anonymous", "invalid", "inherited-missing", "inherited-other-key"} {
		b.Run(sample, func(b *testing.B) {
			request := httptest.NewRequest("POST", "/luvia", nil)
			config := *cfg
			switch sample {
			case "valid":
				request.Header.Set("Authorization", "Bearer "+token)
			case "invalid":
				request.Header.Set("Authorization", "Bearer invalid")
			case "inherited-missing":
				request = request.WithContext(inherited)
			case "inherited-other-key":
				request = request.WithContext(inherited)
				request.Header.Set("Authorization", "Bearer "+token)
				config.Secret = "different-secret"
			}
			for _, candidate := range []struct {
				name string
				wrap func(*auth.Config, http.Handler) http.Handler
			}{
				{"before", authMiddlewareBeforeIsolation}, {"after", AuthMiddleware},
			} {
				b.Run(candidate.name, func(b *testing.B) {
					next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
						middlewareIdentitySink = Identity(r.Context())
						middlewareTokenSink = BearerToken(r.Context())
					})
					handler := candidate.wrap(&config, next)
					handler.ServeHTTP(nil, request)
					wantIdentity := sample == "valid" || candidate.name == "before" && strings.HasPrefix(sample, "inherited-")
					if (middlewareIdentitySink != nil) != wantIdentity {
						b.Fatal("invalid benchmark identity state")
					}
					b.ReportAllocs()
					for b.Loop() {
						handler.ServeHTTP(nil, request)
					}
				})
			}
		})
	}
}
