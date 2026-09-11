package luvia

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux"
)

type dependencyStatsProviderFunc func(context.Context) []lux.RuntimeDependencyStats

const testStudioProjectID = "531f2d00-20e5-47f2-b874-22b6fc76328b"

func TestStudioProjectUUIDConfiguration(t *testing.T) {
	for _, value := range []string{"", "1", "7junk", "00000000-0000-0000-0000-000000000000", "531f2d0020e547f2b87422b6fc76328b"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("LUXO_STUDIO_URL", "http://127.0.0.1:9100")
			t.Setenv("LUXO_API_KEY", "test-key")
			t.Setenv("LUXO_PROJECT_ID", testStudioProjectID)
			t.Setenv("LUXO_PROJECT_ID", value)
			if _, err := studioProjectIDFromEnv(); err == nil {
				t.Fatal("registration must reject non-canonical or missing project UUIDs")
			}
			if NewGatewayRegistrar("0") != nil || NewMetricsCollector() != nil {
				t.Fatal("invalid project identifiers must not start exporters")
			}
			if err := New().Serve("test"); err == nil || !strings.Contains(err.Error(), "LUXO_PROJECT_ID") {
				t.Fatalf("Serve must report invalid Studio configuration: %v", err)
			}
		})
	}
	t.Setenv("LUXO_PROJECT_ID", testStudioProjectID)
	if got, err := studioProjectIDFromEnv(); err != nil || got != testStudioProjectID {
		t.Fatalf("UUID = %q, err = %v", got, err)
	}
}

func TestRegistrarRetriesRegistrationBeforeSendingHeartbeat(t *testing.T) {
	var calls []string
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			API string `json:"$api"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		calls = append(calls, body.API)
		if body.API == "svc:registerGateway" {
			attempts++
			if attempts == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	registrar := &GatewayRegistrar{studioURL: server.URL, client: server.Client()}
	registrar.refreshRegistration()
	registrar.refreshRegistration()
	registrar.refreshRegistration()
	want := []string{"svc:registerGateway", "svc:registerGateway", "svc:heartbeat", "svc:heartbeat"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("requests = %v, want %v", calls, want)
	}
}

func (provider dependencyStatsProviderFunc) RuntimeDependencies(ctx context.Context) []lux.RuntimeDependencyStats {
	return provider(ctx)
}

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
	t.Setenv("LUXO_PROJECT_ID", testStudioProjectID)
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
	t.Setenv("LUXO_PROJECT_ID", testStudioProjectID)
	t.Setenv("LUXO_PROJECT_ID", testStudioProjectID)
	os.Unsetenv("INTROSPECTION_KEY")
	defer os.Unsetenv("LUXO_STUDIO_URL")
	defer os.Unsetenv("LUXO_API_KEY")
	defer os.Unsetenv("LUXO_PROJECT_ID")

	gr := NewGatewayRegistrar("9090")
	if gr == nil {
		t.Fatal("should create registrar")
	}
	defer gr.Close()

	if gr.projectID != testStudioProjectID {
		t.Errorf("projectID = %s, want UUID", gr.projectID)
	}

	// register is async now — wait briefly for it to complete
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(received) == 0 {
		t.Fatal("register should be called on startup")
	}

	regBody := received[0]
	if regBody["$api"] != "svc:registerGateway" {
		t.Errorf("$api = %v, want svc:registerGateway", regBody["$api"])
	}
	if regBody["$select"] != "id" {
		t.Errorf("$select = %v, want id", regBody["$select"])
	}
	if regBody["instanceId"] == nil {
		t.Error("instanceId should be set")
	}
	if regBody["endpoint"] == nil {
		t.Error("endpoint should be set")
	}
	if regBody["apiKey"] != "test-key-123" {
		t.Errorf("apiKey = %v, want test-key-123", regBody["apiKey"])
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
		projectID:  testStudioProjectID,
		instanceID: "test-instance",
		endpoint:   "http://test-instance:8080",
		version:    "v1.2.3",
		startedAt:  time.Now().Add(-2 * time.Second),
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
	if hb["$api"] != "svc:heartbeat" {
		t.Errorf("$api = %v, want svc:heartbeat", hb["$api"])
	}
	if hb["instanceId"] != "test-instance" {
		t.Errorf("instanceId = %v, want test-instance", hb["instanceId"])
	}
	if hb["projectId"] != testStudioProjectID {
		t.Errorf("projectId = %v, want %s", hb["projectId"], testStudioProjectID)
	}
	if _, ok := hb["memoryMB"]; !ok {
		t.Error("memoryMB should be present")
	}
	if hb["apiKey"] != "test-key" {
		t.Errorf("apiKey = %v, want test-key", hb["apiKey"])
	}
	if hb["version"] != "v1.2.3" {
		t.Errorf("version = %v, want v1.2.3", hb["version"])
	}
	if uptime, ok := hb["uptime"].(float64); !ok || uptime < float64(time.Second) {
		t.Errorf("uptime = %v, want at least one second in nanoseconds", hb["uptime"])
	}
	if cpu, ok := hb["cpuPercent"].(float64); !ok || cpu < 0 || cpu > 100 {
		t.Errorf("cpuPercent = %v, want a value in [0, 100]", hb["cpuPercent"])
	}
	if authHeader != "Bearer test-key" {
		t.Errorf("Authorization header = %q, want Bearer test-key", authHeader)
	}
}

func TestGatewayRegistrarHeartbeatReregistersExpiredLease(t *testing.T) {
	var mu sync.Mutex
	var apiNames []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		apiName, _ := body["$api"].(string)
		mu.Lock()
		apiNames = append(apiNames, apiName)
		mu.Unlock()
		if apiName == "svc:heartbeat" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	gr := &GatewayRegistrar{
		studioURL: srv.URL, apiKey: "test", projectID: testStudioProjectID, instanceID: "gateway-1",
		endpoint: "http://gateway-1:8080", done: make(chan struct{}), client: srv.Client(),
	}
	gr.heartbeat()

	mu.Lock()
	defer mu.Unlock()
	if len(apiNames) != 2 || apiNames[0] != "svc:heartbeat" || apiNames[1] != "svc:registerGateway" {
		t.Fatalf("API sequence = %v, want heartbeat then registration", apiNames)
	}
}

func TestGatewayRegistrarDeregisterPayloads(t *testing.T) {
	for _, test := range []struct {
		name     string
		nodeType string
		apiName  string
	}{
		{name: "gateway", apiName: "svc:deregisterGateway"},
		{name: "service", nodeType: "service", apiName: "svc:deregisterServiceNode"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var received map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
					t.Fatal(err)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			gr := &GatewayRegistrar{
				studioURL: srv.URL, apiKey: "key", projectID: testStudioProjectID, instanceID: "node-1",
				nodeType: test.nodeType, done: make(chan struct{}), client: srv.Client(),
			}
			gr.deregister()
			if received["$api"] != test.apiName || received["projectId"] != testStudioProjectID || received["instanceId"] != "node-1" {
				t.Fatalf("deregister payload = %+v", received)
			}
		})
	}
}

func TestCPUSamplerPercent(t *testing.T) {
	s := &cpuSampler{lastBusySeconds: 10, lastSampleAt: time.Unix(100, 0)}
	got := s.percentAt(11, time.Unix(101, 0), 2)
	if got != 50 {
		t.Fatalf("percentAt() = %v, want 50", got)
	}

	if got := s.percentAt(20, time.Unix(102, 0), 2); got != 100 {
		t.Fatalf("percentAt() clamp = %v, want 100", got)
	}
}

func TestCPUSamplerFirstAndInvalidSamples(t *testing.T) {
	s := &cpuSampler{}
	if got := s.percentAt(10, time.Unix(100, 0), 4); got != 0 {
		t.Fatalf("first percentAt() = %v, want 0", got)
	}
	if got := s.percentAt(9, time.Unix(101, 0), 4); got != 0 {
		t.Fatalf("decreasing sample percentAt() = %v, want 0", got)
	}
	if got := s.percentAt(10, time.Unix(101, 0), 0); got != 0 {
		t.Fatalf("invalid processor count percentAt() = %v, want 0", got)
	}
}

func TestCPUSamplerRejectsNonIncreasingTime(t *testing.T) {
	s := &cpuSampler{lastBusySeconds: 10, lastSampleAt: time.Unix(100, 0)}
	if got := s.percentAt(11, time.Unix(100, 0), 2); got != 0 {
		t.Fatalf("non-increasing sample percentAt() = %v, want 0", got)
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

func TestGatewayRegistrarRegisterNonSuccess(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	gr := &GatewayRegistrar{
		studioURL: srv.URL, apiKey: "test", instanceID: "test",
		done: make(chan struct{}), client: srv.Client(),
	}
	gr.register()
	if requests != 1 {
		t.Fatalf("register requests = %d, want 1", requests)
	}
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

func TestGatewayRegistrarHeartbeatLoopTicks(t *testing.T) {
	requestReceived := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestReceived <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	gr := &GatewayRegistrar{
		studioURL: srv.URL, apiKey: "test", instanceID: "gateway-1",
		done: make(chan struct{}), client: srv.Client(), startedAt: time.Now(),
	}
	loopDone := make(chan struct{})
	go func() {
		gr.heartbeatLoopEvery(time.Millisecond)
		close(loopDone)
	}()
	select {
	case <-requestReceived:
	case <-time.After(time.Second):
		t.Fatal("heartbeat loop did not tick")
	}
	close(gr.done)
	select {
	case <-loopDone:
	case <-time.After(time.Second):
		t.Fatal("heartbeat loop did not stop")
	}
}

func TestGatewayRegistrarRegisterIntroKey(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
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
	os.Setenv("LUXO_API_KEY", "test-key")
	t.Setenv("LUXO_PROJECT_ID", testStudioProjectID)
	os.Setenv("INTROSPECTION_KEY", "intro-secret")
	defer os.Unsetenv("LUXO_STUDIO_URL")
	defer os.Unsetenv("LUXO_API_KEY")
	defer os.Unsetenv("INTROSPECTION_KEY")

	gr := NewGatewayRegistrar("9090")
	if gr == nil {
		t.Fatal("should create registrar")
	}
	defer gr.Close()

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("register should be called on startup")
	}
	if received[0]["introKey"] != "intro-secret" {
		t.Errorf("introKey = %v, want intro-secret", received[0]["introKey"])
	}
}

func TestGatewayRegistrarDeregisterInvalidURL(t *testing.T) {
	gr := &GatewayRegistrar{
		studioURL: "://invalid", apiKey: "test", instanceID: "gateway-1",
		client: &http.Client{},
	}
	gr.deregister()
}

func TestGatewayRegistrarRegisterNoIntroKey(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
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
	os.Setenv("LUXO_API_KEY", "test-key")
	t.Setenv("LUXO_PROJECT_ID", testStudioProjectID)
	os.Unsetenv("INTROSPECTION_KEY")
	defer os.Unsetenv("LUXO_STUDIO_URL")
	defer os.Unsetenv("LUXO_API_KEY")

	gr := NewGatewayRegistrar("9090")
	if gr == nil {
		t.Fatal("should create registrar")
	}
	defer gr.Close()

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("register should be called on startup")
	}
	// Nullable and omitted are distinct in Luxo. Explicit null tells Studio to
	// preserve the stored key while satisfying the required nullable parameter.
	if introKey, present := received[0]["introKey"]; !present || introKey != nil {
		t.Errorf("introKey = %v, present=%v, want explicit null", introKey, present)
	}
}

func TestGatewayRegistrarCustomEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	os.Setenv("LUXO_STUDIO_URL", srv.URL)
	os.Setenv("LUXO_API_KEY", "test-key")
	t.Setenv("LUXO_PROJECT_ID", testStudioProjectID)
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

func TestServiceNodeRegistrarPayloads(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		received = append(received, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("LUXO_STUDIO_URL", srv.URL)
	t.Setenv("LUXO_API_KEY", "key")
	t.Setenv("LUXO_PROJECT_ID", testStudioProjectID)
	t.Setenv("LUXO_NODE_TYPE", "service")
	t.Setenv("LUXO_SERVICE_NAME", "billing")
	t.Setenv("LUXO_INSTANCE_ID", "billing-1")
	t.Setenv("LUXO_GATEWAY_ENDPOINT", "http://billing-1:4000")
	t.Setenv("INTROSPECTION_KEY", "gateway-only-key")

	registrar := newGatewayRegistrar("4000", "v2")
	if registrar == nil {
		t.Fatal("registrar is nil")
	}
	defer registrar.Close()
	time.Sleep(100 * time.Millisecond)
	registrar.heartbeat()

	mu.Lock()
	defer mu.Unlock()
	if len(received) < 2 {
		t.Fatalf("received %d payloads, want registration and heartbeat", len(received))
	}
	if received[0]["$api"] != "svc:registerServiceNode" || received[0]["name"] != "billing" || received[0]["addr"] == nil {
		t.Fatalf("registration payload = %+v", received[0])
	}
	if received[0]["$select"] != "id" {
		t.Fatalf("registration $select = %v, want id", received[0]["$select"])
	}
	if _, ok := received[0]["introKey"]; ok {
		t.Fatalf("service registration must not send gateway-only introKey: %+v", received[0])
	}
	if received[len(received)-1]["$api"] != "svc:heartbeatServiceNode" {
		t.Fatalf("heartbeat payload = %+v", received[len(received)-1])
	}
}

func TestGatewayRegistrarHeartbeatIncludesRuntimeDependencies(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		received = append(received, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	t.Setenv("LUXO_STUDIO_URL", server.URL)
	t.Setenv("LUXO_API_KEY", "key")
	t.Setenv("LUXO_PROJECT_ID", testStudioProjectID)

	provider := dependencyStatsProviderFunc(func(context.Context) []lux.RuntimeDependencyStats {
		latency := 1.25
		return []lux.RuntimeDependencyStats{{
			Key: "database:postgresql", Kind: lux.DependencyDatabase, Provider: "postgresql",
			Target: "db.internal:5432", Name: "taskflow", Status: lux.DependencyOnline,
			LatencyMs: &latency, PoolMax: 20, PoolTotal: 4, PoolInUse: 1, PoolIdle: 3,
		}}
	})
	registrar := newGatewayRegistrarWithDependencyStats("8080", "v1", provider)
	if registrar == nil {
		t.Fatal("registrar is nil")
	}
	defer registrar.Close()
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	last := received[len(received)-1]
	dependencies, ok := last["dependencies"].([]any)
	if !ok || len(dependencies) != 1 {
		t.Fatalf("dependencies = %#v", last["dependencies"])
	}
	database, ok := dependencies[0].(map[string]any)
	if !ok || database["target"] != "db.internal:5432" || database["poolInUse"] != float64(1) {
		t.Fatalf("database dependency = %#v", dependencies[0])
	}
}
