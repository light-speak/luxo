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

func TestAuthMiddlewareValidToken(t *testing.T) {
	cfg := testCfg()
	token, _ := auth.Sign(cfg, map[string]any{"id": float64(42), "role": "admin"})

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

func TestNewGatewayConfiguresStreamIdentityExtraction(t *testing.T) {
	gateway := New()
	claims := &Claims{data: map[string]any{"id": float64(42)}}
	ctx := context.WithValue(context.Background(), identityKey, claims)

	got := gateway.Router.IdentityExtractor(ctx)
	if got != claims {
		t.Fatalf("stream identity = %#v, want authenticated claims", got)
	}
}

func TestAuthMiddlewareAcceptsWebSocketQueryToken(t *testing.T) {
	cfg := testCfg()
	token, _ := auth.Sign(cfg, map[string]any{"id": float64(42)})
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
	token, _ := auth.Sign(cfg, map[string]any{"id": float64(42)})
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
	token, _ := auth.Sign(cfg, map[string]any{"id": float64(1)})

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
	c := &Claims{data: map[string]any{
		"id":      float64(42),
		"role":    "admin",
		"active":  true,
		"balance": float64(99.5),
		"count":   int64(7),
	}}

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
	if c.Get("role") != "admin" {
		t.Errorf("Get(role) = %v", c.Get("role"))
	}
	if c.Get("missing") != nil {
		t.Error("Get(missing) should be nil")
	}
	raw := c.Raw()
	if raw["id"] != float64(42) {
		t.Error("Raw() should return underlying map")
	}
}

func TestClaimsZeroValues(t *testing.T) {
	c := &Claims{data: map[string]any{}}

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

func TestClaimsIntFromFloat(t *testing.T) {
	// JSON numbers decode as float64
	c := &Claims{data: map[string]any{"id": float64(123)}}
	if c.Int("id") != 123 {
		t.Errorf("Int from float64 = %d, want 123", c.Int("id"))
	}
}

func TestClaimsFloatFromInt(t *testing.T) {
	c := &Claims{data: map[string]any{"count": int64(10)}}
	if c.Float("count") != 10.0 {
		t.Errorf("Float from int64 = %f, want 10.0", c.Float("count"))
	}
}
