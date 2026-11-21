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

// The exact pre-fix body is retained only as a scoped benchmark reference.
func authMiddlewareBeforeUpgradeHeaders(cfg *auth.Config, next http.Handler) http.Handler {
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

func BenchmarkAuthUpgradeHeaders(b *testing.B) {
	cfg := testCfg()
	token, err := auth.Sign(cfg, testClaimsJSON(`{"id":42}`))
	if err != nil {
		b.Fatal(err)
	}
	if _, err := auth.VerifyCached(cfg, token); err != nil {
		b.Fatal(err)
	}
	for _, kind := range []string{"anonymous-http", "bearer-http", "anonymous-get", "bearer-get", "bearer-ws", "query-ws"} {
		for _, before := range []bool{true, false} {
			name := "current"
			if before {
				name = "before"
			}
			b.Run(kind+"/"+name, func(b *testing.B) {
				benchmarkAuthUpgrade(b, cfg, token, kind, before)
			})
		}
	}
}

func benchmarkAuthUpgrade(b *testing.B, cfg *auth.Config, token, kind string, before bool) {
	request := httptest.NewRequest(http.MethodPost, "/luvia", nil)
	if strings.HasSuffix(kind, "-get") {
		request.Method = http.MethodGet
	}
	if strings.HasSuffix(kind, "-ws") {
		request.Method = http.MethodGet
		request.Header.Set("Upgrade", "websocket")
		request.Header.Set("Connection", "Upgrade")
	}
	if strings.HasPrefix(kind, "bearer-") {
		request.Header.Set("Authorization", "Bearer "+token)
	} else if kind == "query-ws" {
		request.URL.RawQuery = "token=" + token
	}
	var observed *Claims
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { observed = Identity(r.Context()) })
	handler := AuthMiddleware(cfg, next)
	if before {
		handler = authMiddlewareBeforeUpgradeHeaders(cfg, next)
	}
	response := httptest.NewRecorder()
	b.ReportAllocs()
	for b.Loop() {
		handler.ServeHTTP(response, request)
	}
	if (observed != nil) != !strings.HasPrefix(kind, "anonymous-") {
		b.Fatal("benchmark identity changed")
	}
}
