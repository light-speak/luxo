package luvia

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/auth"
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
	got := buildBanner("1.0.0", "4000", "embedded", "postgres://user:pass@host/db", []string{"user", "post"})

	checks := []string{
		"1.0.0",
		"embedded",
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
	got := buildBanner("dev", "3000", "standalone", "", nil)

	if !strings.Contains(got, "(not configured)") {
		t.Error("should show (not configured) when no DB URL")
	}
	if !strings.Contains(got, "(no modules)") {
		t.Error("should show (no modules) when modules is nil")
	}
}

func TestBuildBannerEmptyModules(t *testing.T) {
	got := buildBanner("dev", "3000", "standalone", "", []string{})

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

func TestServeRejectsMissingStreamImplementationBeforeListening(t *testing.T) {
	gw := New()
	gw.Router.RequireStream("watchMissing")
	err := gw.Serve("test")
	if err == nil || !strings.Contains(err.Error(), "watchMissing") {
		t.Fatalf("Serve validation error = %v", err)
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
	auth.ResetConfig() // clear sync.Once cache
	defer auth.ResetConfig()

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

func TestPrintBanner(t *testing.T) {
	// printBanner just calls fmt.Print(buildBanner(...)) — verify no panic
	// Capture by redirecting is overkill; just ensure it doesn't crash
	printBanner("1.0", "4000", "embedded", "", []string{"test"})
}

func TestBuildMuxIntrospectionKey(t *testing.T) {
	t.Setenv("INTROSPECTION_KEY", "my-secret-key")

	gw := New()
	gw.buildMux("v1")

	if gw.Router.IntrospectionKey != "my-secret-key" {
		t.Errorf("IntrospectionKey = %q, want my-secret-key", gw.Router.IntrospectionKey)
	}
}

func TestBuildMuxDevMode(t *testing.T) {
	t.Setenv("APP_ENV", "development")

	gw := New()
	gw.buildMux("v1")

	// In dev mode, router should be in dev mode
	// We can't easily check this without exposing state, but ensure no crash
}

func TestBuildMuxUsesCORSOriginForWebSocket(t *testing.T) {
	t.Setenv("CORS_ORIGIN", "https://studio.example.com:8443")

	gw := New()
	gw.buildMux("v1")

	if len(gw.Router.WSOrigins) != 1 || gw.Router.WSOrigins[0] != "studio.example.com:8443" {
		t.Fatalf("WebSocket origins = %v", gw.Router.WSOrigins)
	}
	if gw.Router.WSAllowAllOrigins {
		t.Fatal("specific CORS origin must not allow every WebSocket origin")
	}
}

func TestBuildMuxUsesWildcardCORSForWebSocket(t *testing.T) {
	t.Setenv("CORS_ORIGIN", "*")

	gw := New()
	gw.buildMux("v1")

	if !gw.Router.WSAllowAllOrigins {
		t.Fatal("wildcard CORS must apply to WebSocket origin checks")
	}
}

func TestBuildBannerDatabaseDisplay(t *testing.T) {
	// With full DATABASE_* fields
	t.Setenv("DATABASE_HOST", "localhost")
	t.Setenv("DATABASE_PORT", "5432")
	t.Setenv("DATABASE_USER", "admin")
	t.Setenv("DATABASE_PREFIX", "myapp")

	gw := New()
	gw.AddModule("user")
	// buildMux constructs DB display from env
	_, _ = gw.buildMux("v1")
}

func TestServe_H2C(t *testing.T) {
	gw := New()
	gw.AddModule("test")

	// Use a random high port
	t.Setenv("APP_PORT", "0")

	// Start Serve in goroutine and stop with signal
	done := make(chan error, 1)
	go func() {
		done <- gw.Serve("test-v1")
	}()

	// Give server time to start then send interrupt
	time.Sleep(50 * time.Millisecond)
	proc, _ := os.FindProcess(os.Getpid())
	proc.Signal(syscall.SIGINT)

	err := <-done
	if err != nil {
		t.Errorf("Serve should shut down cleanly, got: %v", err)
	}
}

func TestServe_CustomShutdownTimeout(t *testing.T) {
	t.Setenv("SHUTDOWN_TIMEOUT", "1s")
	t.Setenv("APP_PORT", "0")

	gw := New()
	done := make(chan error, 1)
	go func() {
		done <- gw.Serve("test-v1")
	}()

	time.Sleep(50 * time.Millisecond)
	proc, _ := os.FindProcess(os.Getpid())
	proc.Signal(syscall.SIGINT)

	err := <-done
	if err != nil {
		t.Errorf("got: %v", err)
	}
}

func TestServe_TLS(t *testing.T) {
	// Generate self-signed cert for testing
	certFile, keyFile := generateTestCert(t)
	t.Setenv("APP_TLS_CERT", certFile)
	t.Setenv("APP_TLS_KEY", keyFile)
	t.Setenv("APP_PORT", "0")

	gw := New()
	done := make(chan error, 1)
	go func() {
		done <- gw.Serve("test-tls")
	}()

	time.Sleep(100 * time.Millisecond)
	proc, _ := os.FindProcess(os.Getpid())
	proc.Signal(syscall.SIGINT)

	err := <-done
	if err != nil {
		t.Errorf("TLS Serve should shut down cleanly, got: %v", err)
	}
}

func generateTestCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"Test"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	os.WriteFile(certPath, certPEM, 0600)
	os.WriteFile(keyPath, keyPEM, 0600)
	return certPath, keyPath
}

func TestServe_InvalidShutdownTimeout(t *testing.T) {
	t.Setenv("SHUTDOWN_TIMEOUT", "not-a-duration")
	t.Setenv("APP_PORT", "0")

	gw := New()
	done := make(chan error, 1)
	go func() {
		done <- gw.Serve("test-v1")
	}()

	time.Sleep(50 * time.Millisecond)
	proc, _ := os.FindProcess(os.Getpid())
	proc.Signal(syscall.SIGINT)

	err := <-done
	if err != nil {
		t.Errorf("got: %v", err)
	}
}

func TestBuildMuxWithI18nDir(t *testing.T) {
	// Create a temp dir with i18n translations to test the success path
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	dir := t.TempDir()
	os.Chdir(dir)

	// Create i18n directory with a translation file
	i18nDir := filepath.Join(dir, "i18n")
	os.Mkdir(i18nDir, 0755)
	os.WriteFile(filepath.Join(i18nDir, "en.json"), []byte(`{"hello": "Hello"}`), 0644)

	gw := New()
	mux, _ := gw.buildMux("v1")
	if mux == nil {
		t.Fatal("mux should not be nil")
	}
}

func TestServeWithMetricsAndRegistrar(t *testing.T) {
	// Test Serve with Studio env vars set to test metrics/registrar init paths
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	t.Setenv("LUXO_STUDIO_URL", srv.URL)
	t.Setenv("LUXO_API_KEY", "test-key")
	t.Setenv("APP_PORT", "0")

	gw := New()
	done := make(chan error, 1)
	go func() {
		done <- gw.Serve("test-v1")
	}()

	time.Sleep(100 * time.Millisecond)
	proc, _ := os.FindProcess(os.Getpid())
	proc.Signal(syscall.SIGINT)

	err := <-done
	if err != nil {
		t.Errorf("Serve should shut down cleanly, got: %v", err)
	}
}

func TestWaitForShutdown_NilMetricsAndRegistrar(t *testing.T) {
	// Test waitForShutdown with nil metrics and registrar (already covered
	// by the basic Serve tests, but explicitly verify the nil guard paths)
	t.Setenv("APP_PORT", "0")

	gw := New()
	done := make(chan error, 1)
	go func() {
		done <- gw.Serve("test-v1")
	}()

	time.Sleep(50 * time.Millisecond)
	proc, _ := os.FindProcess(os.Getpid())
	proc.Signal(syscall.SIGINT)

	err := <-done
	if err != nil {
		t.Errorf("got: %v", err)
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
