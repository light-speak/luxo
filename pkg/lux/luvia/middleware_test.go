package luvia

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/auth"
)

func testCfg() *auth.Config {
	return &auth.Config{
		Secret:  "test-secret",
		Expires: time.Hour,
	}
}

func testClaimsJSON(raw string) auth.ClaimsWriter {
	return func(dst []byte) ([]byte, error) { return append(dst, raw...), nil }
}

func testIdentity(t testing.TB, raw string) *Claims {
	t.Helper()
	cfg := testCfg()
	token, err := auth.Sign(cfg, testClaimsJSON(raw))
	if err != nil {
		t.Fatal(err)
	}
	claims, err := auth.Verify(cfg, token)
	if err != nil {
		t.Fatal(err)
	}
	return &claims
}

func TestAuthMiddlewareValidToken(t *testing.T) {
	cfg := testCfg()
	token, _ := auth.Sign(cfg, testClaimsJSON(`{"id":42,"role":"admin"}`))

	var gotIdentity *Claims
	var gotToken string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIdentity = Identity(r.Context())
		gotToken = BearerToken(r.Context())
		w.WriteHeader(200)
	})

	handler := AuthMiddleware(cfg, inner)

	r := httptest.NewRequest("POST", "/luvia", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if gotIdentity == nil {
		t.Fatal("identity should be set")
	}
	if gotIdentity.ID() != 42 {
		t.Errorf("id = %v, want 42", gotIdentity.ID())
	}
	if gotIdentity.String("role") != "admin" {
		t.Errorf("role = %v", gotIdentity.String("role"))
	}
	if gotToken != token {
		t.Errorf("bearer token = %q, want signed token", gotToken)
	}
}

func TestRefreshTokenCannotEstablishRequestIdentity(t *testing.T) {
	auth.ResetConfig()
	auth.ResetVerifyCache()
	t.Cleanup(auth.ResetConfig)
	t.Cleanup(auth.ResetVerifyCache)
	t.Setenv("JWT_SECRET", "refresh-boundary-test")
	t.Setenv("JWT_REFRESH", "true")
	cfg, err := auth.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.SignRefresh(cfg, testClaimsJSON(`{"id":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.VerifyRefresh(cfg, token); err != nil {
		t.Fatal(err)
	}
	handler := AuthMiddleware(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Identity(r.Context()) != nil || BearerToken(r.Context()) != "" {
			t.Error("refresh token established an access identity")
		}
	}))
	for _, websocket := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/luvia", nil)
		if websocket {
			r.Header.Set("Upgrade", "websocket")
			r.URL.RawQuery = "token=" + url.QueryEscape(token)
		} else {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		for range 2 {
			handler.ServeHTTP(httptest.NewRecorder(), r)
		}
	}
	if ctx, err := internalRequestContext(t.Context(), token); err == nil || ctx != nil {
		t.Fatal("internal RPC accepted refresh as access")
	}
}

func TestInternalRequestContextPreservesExactIdentity(t *testing.T) {
	auth.ResetConfig()
	auth.ResetVerifyCache()
	t.Cleanup(auth.ResetConfig)
	t.Cleanup(auth.ResetVerifyCache)
	t.Setenv("JWT_SECRET", "internal-context-test")
	cfg, err := auth.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.Sign(cfg, testClaimsJSON(`{"id":9007199254740993,"role":"ADMIN"}`))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		ctx, err := internalRequestContext(t.Context(), token)
		if err != nil {
			t.Fatal(err)
		}
		claims := Identity(ctx)
		if claims == nil || claims.ID() != 9007199254740993 || claims.String("role") != "ADMIN" || BearerToken(ctx) != token {
			t.Fatal("internal request lost exact identity or bearer token")
		}
	}
	auth.ResetConfig()
	t.Setenv("JWT_SECRET", " ")
	if ctx, err := internalRequestContext(t.Context(), token); ctx != nil || err == nil {
		t.Fatal("invalid signing configuration accepted an internal request")
	}
}
func TestNewGatewayConfiguresStreamIdentityExtraction(t *testing.T) {
	gateway := New()
	claims := testIdentity(t, `{"id":42}`)
	ctx := context.WithValue(context.Background(), identityKey, claims)

	got := gateway.Router.IdentityExtractor(ctx)
	if got != claims {
		t.Fatalf("stream identity = %#v, want authenticated claims", got)
	}
	for _, anonymous := range []context.Context{
		context.Background(),
		context.WithValue(context.Background(), identityKey, (*Claims)(nil)),
	} {
		if got := gateway.Router.IdentityExtractor(anonymous); got != nil {
			t.Fatalf("anonymous stream identity must be a nil interface, got %T", got)
		}
	}
}

// The historical extractor returns a typed nil for anonymous requests. It is
// retained only as a local performance reference, not an equivalent safe API.
func BenchmarkGatewayIdentity(b *testing.B) {
	identity := testIdentity(b, `{"id":42}`)
	for _, sample := range []struct {
		name string
		ctx  context.Context
		want *Claims
	}{
		{"anonymous", context.Background(), nil},
		{"authenticated", context.WithValue(context.Background(), identityKey, identity), identity},
		{"typed-nil", context.WithValue(context.Background(), identityKey, (*Claims)(nil)), nil},
	} {
		b.Run(sample.name, func(b *testing.B) {
			for _, candidate := range []struct {
				name string
				read func(context.Context) any
			}{
				{"before", func(ctx context.Context) any { return Identity(ctx) }},
				{"after", New().Router.IdentityExtractor},
			} {
				b.Run(candidate.name, func(b *testing.B) {
					var want any = sample.want
					if candidate.name == "after" && sample.want == nil {
						want = nil
					}
					var got any
					b.ReportAllocs()
					for b.Loop() {
						got = candidate.read(sample.ctx)
					}
					if got != want {
						b.Fatal("unexpected extracted identity")
					}
				})
			}
		})
	}
}

func TestAuthMiddlewareAcceptsWebSocketQueryToken(t *testing.T) {
	cfg := testCfg()
	token, _ := auth.Sign(cfg, testClaimsJSON(`{"id":42}`))
	var identity *Claims
	handler := AuthMiddleware(cfg, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		identity = Identity(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/luvia?token="+url.QueryEscape(token), nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if identity == nil || identity.ID() != 42 {
		t.Fatalf("websocket query token identity = %#v", identity)
	}
}

func TestAuthMiddlewareRejectsHTTPQueryToken(t *testing.T) {
	cfg := testCfg()
	token, _ := auth.Sign(cfg, testClaimsJSON(`{"id":42}`))
	var identity *Claims
	handler := AuthMiddleware(cfg, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		identity = Identity(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/luvia?token="+url.QueryEscape(token), nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if identity != nil {
		t.Fatal("ordinary HTTP request must not accept query token")
	}
}

func TestAuthMiddlewareNoToken(t *testing.T) {
	cfg := testCfg()

	var gotIdentity *Claims
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIdentity = Identity(r.Context())
		w.WriteHeader(200)
	})

	handler := AuthMiddleware(cfg, inner)

	r := httptest.NewRequest("POST", "/luvia", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if gotIdentity != nil {
		t.Error("identity should be nil without token")
	}
}

func TestAuthMiddlewareInvalidToken(t *testing.T) {
	cfg := testCfg()

	var gotIdentity *Claims
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIdentity = Identity(r.Context())
		w.WriteHeader(200)
	})

	handler := AuthMiddleware(cfg, inner)

	r := httptest.NewRequest("POST", "/luvia", nil)
	r.Header.Set("Authorization", "Bearer invalid-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if gotIdentity != nil {
		t.Error("identity should be nil for invalid token")
	}
}

func TestAuthMiddlewareBearerCaseInsensitive(t *testing.T) {
	cfg := testCfg()
	token, _ := auth.Sign(cfg, testClaimsJSON(`{"id":1}`))

	var gotIdentity *Claims
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIdentity = Identity(r.Context())
		w.WriteHeader(200)
	})

	handler := AuthMiddleware(cfg, inner)

	r := httptest.NewRequest("POST", "/luvia", nil)
	r.Header.Set("Authorization", "BEARER "+token) // uppercase
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if gotIdentity == nil {
		t.Fatal("should accept case-insensitive Bearer")
	}
}

func TestIdentityNoContext(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	id := Identity(r.Context())
	if id != nil {
		t.Error("should return nil for no identity")
	}
}

func TestBearerTokenEmptyContext(t *testing.T) {
	if got := BearerToken(context.Background()); got != "" {
		t.Fatalf("BearerToken() = %q, want empty", got)
	}
}

func TestExtractBearerTokenEmpty(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if token := extractBearerToken(r); token != "" {
		t.Errorf("expected empty, got %q", token)
	}
}

func TestExtractBearerTokenShort(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bear")
	if token := extractBearerToken(r); token != "" {
		t.Errorf("expected empty, got %q", token)
	}
}

func TestClaimsAccessors(t *testing.T) {
	c := testIdentity(t, `{"id":42,"role":"admin","active":true,"balance":99.5,"count":7}`)

	if c.ID() != 42 {
		t.Errorf("ID() = %d, want 42", c.ID())
	}
	if c.String("role") != "admin" {
		t.Errorf("String(role) = %q", c.String("role"))
	}
	if c.Bool("active") != true {
		t.Error("Bool(active) should be true")
	}
	if c.Float("balance") != 99.5 {
		t.Errorf("Float(balance) = %f", c.Float("balance"))
	}
	if c.Int("count") != 7 {
		t.Errorf("Int(count) = %d, want 7", c.Int("count"))
	}
	if !c.Has("role") {
		t.Error("Has(role) should be true")
	}
	if c.Has("missing") {
		t.Error("Has(missing) should be false")
	}
	if value, ok := c.Get("role").String(); !ok || value != "admin" {
		t.Errorf("Get(role) = %v", c.Get("role"))
	}
	if c.Get("missing").Raw() != "" {
		t.Error("Get(missing) should be absent")
	}
	raw := c.Raw()
	if raw != `{"id":42,"role":"admin","active":true,"balance":99.5,"count":7}` {
		t.Error("Raw() should preserve immutable JSON text")
	}
}

func TestClaimsZeroValues(t *testing.T) {
	c := &Claims{}

	if c.ID() != 0 {
		t.Errorf("ID() on empty = %d, want 0", c.ID())
	}
	if c.String("x") != "" {
		t.Errorf("String on missing = %q", c.String("x"))
	}
	if c.Int("x") != 0 {
		t.Errorf("Int on missing = %d", c.Int("x"))
	}
	if c.Float("x") != 0 {
		t.Errorf("Float on missing = %f", c.Float("x"))
	}
	if c.Bool("x") != false {
		t.Error("Bool on missing should be false")
	}
}

func TestClaimsIntRejectsFloat(t *testing.T) {
	c := testIdentity(t, `{"id":123.5}`)
	if c.Int("id") != 0 {
		t.Errorf("fraction was truncated to %d", c.Int("id"))
	}
}

func TestClaimsFloatFromInt(t *testing.T) {
	c := testIdentity(t, `{"count":10}`)
	if c.Float("count") != 10.0 {
		t.Errorf("Float from int64 = %f, want 10.0", c.Float("count"))
	}
}
