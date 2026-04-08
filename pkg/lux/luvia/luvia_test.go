package luvia

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMaskDSN(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"postgres://user:pass@host:5432/db", "postgres://user:***@host:5432/db"},
		{"postgres://admin:secret123@localhost/mydb", "postgres://admin:***@localhost/mydb"},
		{"no-at-sign", "no-at-sign"},
		{"user@host", "user@host"}, // no colon before @
	}
	for _, tt := range tests {
		if got := maskDSN(tt.in); got != tt.want {
			t.Errorf("maskDSN(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEnvOr(t *testing.T) {
	if got := envOr("LUXO_TEST_NONEXISTENT_KEY", "fallback"); got != "fallback" {
		t.Errorf("envOr = %q, want fallback", got)
	}
}

func TestHandleHealth(t *testing.T) {
	r := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	handleHealth(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != `{"status":"ok"}` {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestNewGateway(t *testing.T) {
	gw := New()
	if gw.Router == nil {
		t.Error("Router should not be nil")
	}
	gw.AddModule("user")
	gw.AddModule("post")
	if len(gw.modules) != 2 {
		t.Errorf("modules = %d, want 2", len(gw.modules))
	}
}

func TestDiscoverModulesNoDir(t *testing.T) {
	// When origin/ doesn't exist
	modules := DiscoverModules()
	// May or may not find modules depending on CWD — just ensure no panic
	_ = modules
}
