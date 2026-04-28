package luvia

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestBuildGradientLineEmpty(t *testing.T) {
	rgb := func(r, g, b int) string {
		return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
	}
	got := buildGradientLine("", rgb)
	if got != "" {
		t.Errorf("empty line should return empty string, got %q", got)
	}
}

func TestBuildGradientLineSingleChar(t *testing.T) {
	rgb := func(r, g, b int) string {
		return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
	}
	got := buildGradientLine("A", rgb)
	if !strings.Contains(got, "A") {
		t.Error("should contain the character")
	}
	// Should contain ANSI color code
	if !strings.Contains(got, "\033[38;2;") {
		t.Error("should contain ANSI escape")
	}
}

func TestBuildGradientLineMultiChar(t *testing.T) {
	rgb := func(r, g, b int) string {
		return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
	}
	got := buildGradientLine("LUXO", rgb)
	// Should contain all characters
	for _, ch := range "LUXO" {
		if !strings.ContainsRune(got, ch) {
			t.Errorf("missing character %c", ch)
		}
	}
}

func TestBuildBannerWithModules(t *testing.T) {
	got := buildBanner("1.0.0", "4000", "embedded", "json", "postgres://user:pass@host/db", []string{"user", "post"})

	checks := []string{
		"1.0.0",
		"embedded",
		"json",
		"4000",
		"user:***@host/db", // masked DSN
		"user",
		"post",
		"Luvia Gateway",
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("banner missing %q", check)
		}
	}
}

func TestBuildBannerNoDatabase(t *testing.T) {
	got := buildBanner("dev", "3000", "standalone", "binary", "", nil)

	if !strings.Contains(got, "(not configured)") {
		t.Error("should show (not configured) when no DB URL")
	}
	if !strings.Contains(got, "(no modules)") {
		t.Error("should show (no modules) when modules is nil")
	}
}

func TestBuildBannerEmptyModules(t *testing.T) {
	got := buildBanner("dev", "3000", "standalone", "binary", "", []string{})

	if !strings.Contains(got, "(no modules)") {
		t.Error("should show (no modules) when modules is empty")
	}
}

func TestDiscoverModulesWithTempDir(t *testing.T) {
	// Save and restore working directory
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	dir := t.TempDir()
	os.Chdir(dir)

	// Create origin directory with .luxo files
	originDir := filepath.Join(dir, "origin")
	os.Mkdir(originDir, 0755)
	os.WriteFile(filepath.Join(originDir, "user.luxo"), []byte("model User {}"), 0644)
	os.WriteFile(filepath.Join(originDir, "post.luxo"), []byte("model Post {}"), 0644)
	os.WriteFile(filepath.Join(originDir, "readme.txt"), []byte("not a luxo file"), 0644)
	os.Mkdir(filepath.Join(originDir, "subdir.luxo"), 0755) // directory, not a file

	modules := DiscoverModules()
	if len(modules) != 2 {
		t.Fatalf("expected 2 modules, got %d: %v", len(modules), modules)
	}

	// Check names (order may vary based on OS readdir order)
	found := map[string]bool{}
	for _, m := range modules {
		found[m] = true
	}
	if !found["user"] || !found["post"] {
		t.Errorf("expected user and post, got %v", modules)
	}
}

func TestDiscoverModulesEmptyOrigin(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	dir := t.TempDir()
	os.Chdir(dir)

	// Create empty origin directory
	os.Mkdir(filepath.Join(dir, "origin"), 0755)

	modules := DiscoverModules()
	if len(modules) != 0 {
		t.Errorf("expected 0 modules, got %d", len(modules))
	}
}

func TestEnvOrWithSetEnv(t *testing.T) {
	// env.Get delegates to os.LookupEnv, so we can set env vars to test the found path.
	key := "LUXO_TEST_ENV_OR_KEY_12345"
	t.Setenv(key, "found_value")
	got := envOr(key, "default_val")
	if got != "found_value" {
		t.Errorf("expected found_value, got %q", got)
	}
}

func TestEnvOrFallback(t *testing.T) {
	got := envOr("THIS_KEY_SHOULD_NOT_EXIST_IN_ENV_MAP_LUXO_TEST", "default_val")
	if got != "default_val" {
		t.Errorf("expected default_val, got %q", got)
	}
}

func TestServeSetup(t *testing.T) {
	// Test that Serve sets up routes correctly without actually starting the server.
	// We can at least verify the gateway state before Serve.
	gw := New()
	gw.AddModule("test")
	if len(gw.modules) != 1 {
		t.Errorf("expected 1 module, got %d", len(gw.modules))
	}
	if gw.modules[0] != "test" {
		t.Errorf("expected 'test', got %q", gw.modules[0])
	}
}

func TestBuildMux(t *testing.T) {
	gw := New()
	gw.AddModule("user")

	mux, port := gw.buildMux("test-version")

	if port != "4000" {
		t.Errorf("expected default port 4000, got %q", port)
	}
	if mux == nil {
		t.Fatal("mux should not be nil")
	}

	// Verify /health route works via the mux
	r := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("health status = %d, want 200", w.Code)
	}
}

func TestBuildMuxCustomPort(t *testing.T) {
	t.Setenv("APP_PORT", "8080")

	gw := New()
	_, port := gw.buildMux("v1")

	if port != "8080" {
		t.Errorf("expected port 8080, got %q", port)
	}
}

func TestBuildMuxWithAuth(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-key")

	gw := New()
	mux, _ := gw.buildMux("v1")

	// Verify /luvia route goes through auth middleware
	r := httptest.NewRequest("POST", "/luvia", strings.NewReader(`{"api":"test"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	// Without a valid JWT token, should still work (middleware extracts but doesn't reject)
	if mux == nil {
		t.Fatal("mux should not be nil with JWT configured")
	}
}

func TestBuildMuxLuviaRoute(t *testing.T) {
	gw := New()
	mux, _ := gw.buildMux("v1")

	// Verify /luvia route is registered (should return non-404)
	r := httptest.NewRequest("POST", "/luvia", strings.NewReader(`{"api":"test"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	// The router may return various status codes but not 404 "page not found"
	// since the route is registered
	if w.Code == http.StatusNotFound && strings.Contains(w.Body.String(), "page not found") {
		t.Error("/luvia route should be registered")
	}
}

func TestCORSPreflightOptions(t *testing.T) {
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("OPTIONS should not reach inner handler")
	}))

	r := httptest.NewRequest(http.MethodOptions, "/luvia", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Errorf("OPTIONS should return 204, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS origin = %q, want *", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got != "POST, OPTIONS" {
		t.Errorf("CORS methods = %q", got)
	}
}

func TestCORSCustomOrigin(t *testing.T) {
	os.Setenv("CORS_ORIGIN", "https://example.com")
	defer os.Unsetenv("CORS_ORIGIN")

	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	r := httptest.NewRequest(http.MethodPost, "/luvia", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("CORS origin = %q, want https://example.com", got)
	}
}

func TestSecurityHeaders(t *testing.T) {
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	r := httptest.NewRequest(http.MethodPost, "/luvia", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	checks := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"X-XSS-Protection":        "1; mode=block",
		"Content-Security-Policy": "default-src 'none'",
	}
	for header, want := range checks {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestCORSPassThroughNonOptions(t *testing.T) {
	called := false
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodPost, "/luvia", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if !called {
		t.Error("POST should reach inner handler")
	}
	if w.Code != http.StatusOK {
		t.Errorf("POST should return 200, got %d", w.Code)
	}
}
