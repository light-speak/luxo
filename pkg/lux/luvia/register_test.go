package luvia

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
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
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("failed to decode body: %v", err)
			w.WriteHeader(400)
			return
		}
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

	// register is async now — wait briefly for it to complete
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

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
	var authHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("failed to decode body: %v", err)
			w.WriteHeader(400)
			return
		}
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
		client:     &http.Client{Timeout: 5 * time.Second},
	}

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
	if hb["instanceId"] != "test-instance" {
		t.Errorf("instanceId = %v, want test-instance", hb["instanceId"])
	}
	if _, ok := hb["memoryMB"]; !ok {
		t.Error("memoryMB should be present")
	}
	// Auth should be in header, not body
	if authHeader != "Bearer test-key" {
		t.Errorf("Authorization header = %q, want Bearer test-key", authHeader)
	}
}

func TestGatewayRegistrarRegisterHTTPError(t *testing.T) {
	gr := &GatewayRegistrar{
		studioURL:  "http://127.0.0.1:1", // unreachable
		apiKey:     "test",
		instanceID: "test",
		done:       make(chan struct{}),
		client:     &http.Client{Timeout: 1 * time.Second},
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
		client:     &http.Client{Timeout: 1 * time.Second},
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
		client:     &http.Client{Timeout: 5 * time.Second},
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
	gr.Close()

	// Verify channel is closed
	select {
	case <-gr.done:
		// ok
	default:
		t.Fatal("done channel should be closed")
	}
}

func TestGatewayRegistrarDoubleClose(t *testing.T) {
	gr := &GatewayRegistrar{
		done: make(chan struct{}),
	}
	gr.Close()
	gr.Close() // should not panic
}

func TestGatewayRegistrarRegisterInvalidURL(t *testing.T) {
	// Test register with an invalid URL that causes http.NewRequest to fail
	gr := &GatewayRegistrar{
		studioURL:  "://invalid", // invalid scheme
		apiKey:     "test",
		instanceID: "test",
		done:       make(chan struct{}),
		client:     &http.Client{Timeout: 1 * time.Second},
	}
	// Should not panic — register exits early on NewRequest error
	gr.register()
}

func TestGatewayRegistrarHeartbeatInvalidURL(t *testing.T) {
	// Test heartbeat with an invalid URL that causes http.NewRequest to fail
	gr := &GatewayRegistrar{
		studioURL:  "://invalid", // invalid scheme
		apiKey:     "test",
		instanceID: "test",
		done:       make(chan struct{}),
		client:     &http.Client{Timeout: 1 * time.Second},
	}
	// Should not panic — heartbeat exits early on NewRequest error
	gr.heartbeat()
}

func TestGatewayRegistrarHeartbeatLoopDone(t *testing.T) {
	gr := &GatewayRegistrar{
		studioURL:  "http://127.0.0.1:1",
		apiKey:     "test",
		instanceID: "test",
		done:       make(chan struct{}),
		client:     &http.Client{Timeout: 100 * time.Millisecond},
	}
	// Close done channel immediately — heartbeatLoop should exit
	close(gr.done)
	// heartbeatLoop should return without blocking
	gr.heartbeatLoop()
}

func TestGatewayRegistrarCustomEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	os.Setenv("LUXO_STUDIO_URL", srv.URL)
	os.Setenv("LUXO_API_KEY", "test-key")
	os.Setenv("LUXO_GATEWAY_ENDPOINT", "https://my-gateway.example.com")
	defer os.Unsetenv("LUXO_STUDIO_URL")
	defer os.Unsetenv("LUXO_API_KEY")
	defer os.Unsetenv("LUXO_GATEWAY_ENDPOINT")

	gr := NewGatewayRegistrar("8080")
	if gr == nil {
		t.Fatal("should create registrar")
	}
	defer gr.Close()

	if gr.endpoint != "https://my-gateway.example.com" {
		t.Errorf("endpoint = %q, want https://my-gateway.example.com", gr.endpoint)
	}
}
