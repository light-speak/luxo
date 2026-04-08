package luvia

import (
	"net/http"
	"net/http/httptest"
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

	var gotIdentity map[string]any
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIdentity = Identity(r.Context())
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
	if gotIdentity["id"] != float64(42) {
		t.Errorf("id = %v, want 42", gotIdentity["id"])
	}
	if gotIdentity["role"] != "admin" {
		t.Errorf("role = %v", gotIdentity["role"])
	}
}

func TestAuthMiddlewareNoToken(t *testing.T) {
	cfg := testCfg()

	var gotIdentity map[string]any
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

	var gotIdentity map[string]any
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

	var gotIdentity map[string]any
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
