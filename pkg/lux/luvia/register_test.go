package luvia

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
)

func TestNewGatewayRegistrarNilWhenNoEnv(t *testing.T) {
	os.Unsetenv("LUXO_STUDIO_URL")
	os.Unsetenv("LUXO_API_KEY")

	gr := NewGatewayRegistrar("8080")
	if gr != nil {
		gr.Close()
		t.Fatal("should return nil when env vars not set")
	}
}

func TestNewGatewayRegistrarNilPartialEnv(t *testing.T) {
	os.Unsetenv("LUXO_STUDIO_URL")
	os.Setenv("LUXO_API_KEY", "test-key")
	defer os.Unsetenv("LUXO_API_KEY")

	gr := NewGatewayRegistrar("8080")
	if gr != nil {
		gr.Close()
		t.Fatal("should return nil when studio URL not set")
	}
}

func TestGatewayRegistrarRegisterBody(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		received = append(received, body)
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	os.Setenv("LUXO_STUDIO_URL", srv.URL)
	os.Setenv("LUXO_API_KEY", "test-key-123")
	os.Setenv("LUXO_PROJECT_ID", "7")
	defer os.Unsetenv("LUXO_STUDIO_URL")
	defer os.Unsetenv("LUXO_API_KEY")
	defer os.Unsetenv("LUXO_PROJECT_ID")

	gr := NewGatewayRegistrar("9090")
	if gr == nil {
		t.Fatal("should create registrar")
	}
	defer gr.Close()

	if gr.projectID != 7 {
		t.Errorf("projectID = %d, want 7", gr.projectID)
	}

	mu.Lock()
	defer mu.Unlock()

	// register() is called on startup
	if len(received) == 0 {
		t.Fatal("register should be called on startup")
	}

	regBody := received[0]
	if regBody["$api"] != "registerGateway" {
		t.Errorf("$api = %v, want registerGateway", regBody["$api"])
	}
	if regBody["instanceId"] == nil {
		t.Error("instanceId should be set")
	}
	if regBody["endpoint"] == nil {
		t.Error("endpoint should be set")
	}
}

func TestGatewayRegistrarHeartbeatBody(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		received = append(received, body)
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	gr := &GatewayRegistrar{
		studioURL:  srv.URL,
		apiKey:     "test-key",
		projectID:  1,
		instanceID: "test-instance",
		endpoint:   "http://test-instance:8080",
		done:       make(chan struct{}),
	}

	// Call heartbeat directly
	gr.heartbeat()

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("expected 1 heartbeat call, got %d", len(received))
	}

	hb := received[0]
	if hb["$api"] != "heartbeat" {
		t.Errorf("$api = %v, want heartbeat", hb["$api"])
	}
	if hb["apiKey"] != "test-key" {
		t.Errorf("apiKey = %v, want test-key", hb["apiKey"])
	}
	if hb["instanceId"] != "test-instance" {
		t.Errorf("instanceId = %v, want test-instance", hb["instanceId"])
	}
	if _, ok := hb["memoryMB"]; !ok {
		t.Error("memoryMB should be present")
	}
}

func TestGatewayRegistrarRegisterHTTPError(t *testing.T) {
	gr := &GatewayRegistrar{
		studioURL:  "http://127.0.0.1:1", // unreachable
		apiKey:     "test",
		instanceID: "test",
		done:       make(chan struct{}),
	}
	// Should not panic on HTTP error
	gr.register()
}

func TestGatewayRegistrarHeartbeatHTTPError(t *testing.T) {
	gr := &GatewayRegistrar{
		studioURL:  "http://127.0.0.1:1", // unreachable
		apiKey:     "test",
		instanceID: "test",
		done:       make(chan struct{}),
	}
	// Should not panic on HTTP error
	gr.heartbeat()
}

func TestGatewayRegistrarRegisterAuthHeader(t *testing.T) {
	var authHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	gr := &GatewayRegistrar{
		studioURL:  srv.URL,
		apiKey:     "my-secret-key",
		instanceID: "test",
		done:       make(chan struct{}),
	}
	gr.register()

	if authHeader != "Bearer my-secret-key" {
		t.Errorf("Authorization = %q, want Bearer my-secret-key", authHeader)
	}
}

func TestGatewayRegistrarClose(t *testing.T) {
	gr := &GatewayRegistrar{
		done: make(chan struct{}),
	}
	// Close should not panic
	gr.Close()

	// Verify channel is closed
	select {
	case <-gr.done:
		// ok
	default:
		t.Fatal("done channel should be closed")
	}
}
