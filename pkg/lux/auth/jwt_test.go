package auth

import (
	"os"
	"testing"
	"time"
)

func testConfig() *Config {
	return &Config{
		Secret:         "test-secret-key",
		Expires:        time.Hour,
		RefreshEnabled: true,
		RefreshExpires: 24 * time.Hour,
	}
}

func TestSignAndVerify(t *testing.T) {
	cfg := testConfig()
	data := map[string]any{"id": float64(1), "role": "admin"}

	token, err := Sign(cfg, data)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	if token == "" {
		t.Fatal("token should not be empty")
	}

	result, err := Verify(cfg, token)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}

	if result["id"] != float64(1) {
		t.Errorf("id = %v, want 1", result["id"])
	}
	if result["role"] != "admin" {
		t.Errorf("role = %v, want admin", result["role"])
	}
}

func TestVerifyExpired(t *testing.T) {
	cfg := testConfig()
	data := map[string]any{"id": float64(1)}

	token, _ := SignWithExpiry(cfg, data, -time.Hour)

	_, err := Verify(cfg, token)
	if err == nil {
		t.Fatal("should reject expired token")
	}
}

func TestVerifyBadSignature(t *testing.T) {
	cfg := testConfig()
	data := map[string]any{"id": float64(1)}

	token, _ := Sign(cfg, data)

	// Tamper with token
	_, err := Verify(cfg, token+"x")
	if err == nil {
		t.Fatal("should reject tampered token")
	}
}

func TestVerifyBadFormat(t *testing.T) {
	cfg := testConfig()

	_, err := Verify(cfg, "not.a.valid.token")
	if err == nil {
		t.Fatal("should reject bad format")
	}

	_, err = Verify(cfg, "onlyonepart")
	if err == nil {
		t.Fatal("should reject single part")
	}
}

func TestVerifyBadPayload(t *testing.T) {
	cfg := testConfig()
	// Valid header + bad payload + valid sig format
	header := base64Encode([]byte(`{"alg":"HS256"}`))
	payload := base64Encode([]byte(`not json`))
	sig := hmacSign(header+"."+payload, cfg.Secret)
	token := header + "." + payload + "." + sig

	_, err := Verify(cfg, token)
	if err == nil {
		t.Fatal("should reject bad payload")
	}
}

func TestSignRefresh(t *testing.T) {
	cfg := testConfig()
	data := map[string]any{"id": float64(1)}

	token, err := SignRefresh(cfg, data)
	if err != nil {
		t.Fatalf("SignRefresh failed: %v", err)
	}

	result, err := Verify(cfg, token)
	if err != nil {
		t.Fatalf("Verify refresh token failed: %v", err)
	}
	if result["id"] != float64(1) {
		t.Error("refresh token should contain same data")
	}
}

func TestSignRefreshDisabled(t *testing.T) {
	cfg := testConfig()
	cfg.RefreshEnabled = false

	_, err := SignRefresh(cfg, map[string]any{})
	if err == nil {
		t.Fatal("should error when refresh disabled")
	}
}

func TestLoadConfig(t *testing.T) {
	ResetConfig()
	os.Setenv("JWT_SECRET", "my-secret")
	os.Setenv("JWT_EXPIRES", "2h")
	os.Setenv("JWT_REFRESH", "true")
	os.Setenv("JWT_REFRESH_EXPIRES", "720h")
	defer func() {
		os.Unsetenv("JWT_SECRET")
		os.Unsetenv("JWT_EXPIRES")
		os.Unsetenv("JWT_REFRESH")
		os.Unsetenv("JWT_REFRESH_EXPIRES")
	}()

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Secret != "my-secret" {
		t.Error("wrong secret")
	}
	if cfg.Expires != 2*time.Hour {
		t.Errorf("expires = %v, want 2h", cfg.Expires)
	}
	if !cfg.RefreshEnabled {
		t.Error("refresh should be enabled")
	}
}

func TestLoadConfigMissing(t *testing.T) {
	ResetConfig()
	os.Unsetenv("JWT_SECRET")
	_, err := LoadConfig()
	if err == nil {
		t.Fatal("should error when JWT_SECRET missing")
	}
}

func TestLoadConfigBadExpires(t *testing.T) {
	ResetConfig()
	os.Setenv("JWT_SECRET", "s")
	os.Setenv("JWT_EXPIRES", "not-a-duration")
	defer func() {
		os.Unsetenv("JWT_SECRET")
		os.Unsetenv("JWT_EXPIRES")
	}()

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("should error on bad JWT_EXPIRES")
	}
}

func TestVerifyBadBase64Payload(t *testing.T) {
	cfg := testConfig()
	// Valid header, invalid base64 payload, valid-looking sig
	header := base64Encode([]byte(`{"alg":"HS256"}`))
	payload := "!!!invalid-base64!!!"
	sig := hmacSign(header+"."+payload, cfg.Secret)
	token := header + "." + payload + "." + sig

	_, err := Verify(cfg, token)
	if err == nil {
		t.Fatal("should reject bad base64 payload")
	}
}

func TestLoadConfigBadRefreshExpires(t *testing.T) {
	ResetConfig()
	os.Setenv("JWT_SECRET", "s")
	os.Setenv("JWT_REFRESH_EXPIRES", "bad")
	defer func() {
		os.Unsetenv("JWT_SECRET")
		os.Unsetenv("JWT_REFRESH_EXPIRES")
	}()

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("should error on bad JWT_REFRESH_EXPIRES")
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"7d", 7 * 24 * time.Hour, false},
		{"2w", 2 * 7 * 24 * time.Hour, false},
		{"1d12h", 36 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"168h", 168 * time.Hour, false},
		{"500ms", 500 * time.Millisecond, false},
		{"0.5d", 12 * time.Hour, false},
		{"bad", 0, true},
	}
	for _, tt := range tests {
		got, err := parseDuration(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseDuration(%q) should error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDuration(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.expected {
			t.Errorf("parseDuration(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}
