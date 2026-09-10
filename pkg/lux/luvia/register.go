package luvia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"runtime/metrics"
	"strings"
	"sync"
	"time"

	"github.com/light-speak/luxo/pkg/lux"
)

const defaultGatewayVersion = "dev"

type cpuSampler struct {
	mu              sync.Mutex
	lastBusySeconds float64
	lastSampleAt    time.Time
}

func (s *cpuSampler) percent() float64 {
	return s.percentAt(runtimeBusySeconds(), time.Now(), runtime.GOMAXPROCS(0))
}

func (s *cpuSampler) percentAt(busySeconds float64, sampledAt time.Time, processors int) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	previousBusy := s.lastBusySeconds
	previousAt := s.lastSampleAt
	s.lastBusySeconds = busySeconds
	s.lastSampleAt = sampledAt
	if previousAt.IsZero() || processors <= 0 || busySeconds < previousBusy {
		return 0
	}
	elapsed := sampledAt.Sub(previousAt).Seconds()
	if elapsed <= 0 {
		return 0
	}
	percent := (busySeconds - previousBusy) / elapsed / float64(processors) * 100
	if percent > 100 {
		return 100
	}
	return percent
}

func runtimeBusySeconds() float64 {
	samples := []metrics.Sample{
		{Name: "/cpu/classes/total:cpu-seconds"},
		{Name: "/cpu/classes/idle:cpu-seconds"},
	}
	metrics.Read(samples)
	return samples[0].Value.Float64() - samples[1].Value.Float64()
}

// GatewayRegistrar handles auto-registration and heartbeat with Luxo Studio.
type GatewayRegistrar struct {
	studioURL       string
	apiKey          string
	projectID       int
	instanceID      string
	nodeType        string
	nodeName        string
	endpoint        string
	introKey        string
	version         string
	startedAt       time.Time
	cpu             cpuSampler
	done            chan struct{}
	closed          bool
	mu              sync.Mutex
	worker          sync.WaitGroup
	client          *http.Client
	dependencyStats lux.RuntimeDependencyStatsProvider
}

// NewGatewayRegistrar creates a registrar from environment variables.
// Returns nil if LUXO_STUDIO_URL or LUXO_API_KEY is not set.
func NewGatewayRegistrar(port string) *GatewayRegistrar {
	return newGatewayRegistrar(port, defaultGatewayVersion)
}

func newGatewayRegistrar(port, version string) *GatewayRegistrar {
	return newGatewayRegistrarWithDependencyStats(port, version, nil)
}

func newGatewayRegistrarWithDependencyStats(port, version string, dependencyStats lux.RuntimeDependencyStatsProvider) *GatewayRegistrar {
	studioURL := os.Getenv("LUXO_STUDIO_URL")
	apiKey := os.Getenv("LUXO_API_KEY")
	if studioURL == "" || apiKey == "" {
		return nil
	}
	projectID := 0
	if v := os.Getenv("LUXO_PROJECT_ID"); v != "" {
		fmt.Sscanf(v, "%d", &projectID)
	}
	// instanceID identifies this gateway in the Gateway table (must be unique
	// across instances of the same project). Honor LUXO_INSTANCE_ID first so
	// operators can pin a stable value (e.g. K8s pod name / CI build id); fall
	// back to os.Hostname() and strip macOS's noisy ".local" Bonjour suffix
	// so developer machines don't show up as "MyMac.local".
	instanceID := gatewayInstanceID()
	nodeType := gatewayNodeType()
	nodeName := os.Getenv("LUXO_SERVICE_NAME")
	if nodeName == "" {
		nodeName = instanceID
	}

	// Use LUXO_GATEWAY_ENDPOINT env for explicit endpoint; fall back to hostname:port.
	// Supports both http and https depending on the deployment environment.
	endpoint := os.Getenv("LUXO_GATEWAY_ENDPOINT")
	if endpoint == "" {
		endpoint = fmt.Sprintf("http://%s:%s", instanceID, port)
	}

	gr := &GatewayRegistrar{
		studioURL:  studioURL,
		apiKey:     apiKey,
		projectID:  projectID,
		instanceID: instanceID,
		nodeType:   nodeType,
		nodeName:   nodeName,
		endpoint:   endpoint,
		// Report our own introspection key so Studio can fetch this service's
		// schema (schema browser / playground). Empty means introspection is
		// disabled here and Studio keeps whatever key it already stored.
		introKey:        os.Getenv("INTROSPECTION_KEY"),
		version:         version,
		startedAt:       time.Now(),
		done:            make(chan struct{}),
		client:          &http.Client{Timeout: 10 * time.Second},
		dependencyStats: dependencyStats,
	}
	gr.cpu.percent()

	// Register async — don't block gateway boot
	gr.worker.Add(1)
	go func() {
		defer gr.worker.Done()
		if gr.register() {
			gr.heartbeat()
		}
		gr.heartbeatLoop()
	}()

	return gr
}

func gatewayInstanceID() string {
	if instanceID := os.Getenv("LUXO_INSTANCE_ID"); instanceID != "" {
		return instanceID
	}
	instanceID, _ := os.Hostname()
	return strings.TrimSuffix(instanceID, ".local")
}

func gatewayNodeType() string {
	if strings.EqualFold(os.Getenv("LUXO_NODE_TYPE"), "service") {
		return "service"
	}
	return "gateway"
}

// Close stops the heartbeat loop and removes the node from Studio's live registry.
func (gr *GatewayRegistrar) Close() {
	gr.mu.Lock()
	if gr.closed {
		gr.mu.Unlock()
		return
	}
	gr.closed = true
	close(gr.done)
	gr.mu.Unlock()

	// Waiting prevents a delayed startup registration from recreating the node
	// after graceful deregistration has completed.
	gr.worker.Wait()
	gr.deregister()
}

func (gr *GatewayRegistrar) register() bool {
	payload := map[string]any{
		"$api":       "svc:registerGateway",
		"$select":    "id",
		"apiKey":     gr.apiKey,
		"projectId":  gr.projectID,
		"name":       gr.nodeName,
		"endpoint":   gr.endpoint,
		"instanceId": gr.instanceID,
	}
	if gr.nodeType == "service" {
		payload["$api"] = "svc:registerServiceNode"
		payload["addr"] = gr.endpoint
		payload["version"] = gr.version
		delete(payload, "endpoint")
	}
	// A nullable Luxo parameter is still required unless it has a default.
	// Send explicit null when introspection is disabled so registration keeps
	// the stored key without weakening the protocol's missing/null distinction.
	if gr.nodeType != "service" {
		payload["introKey"] = nil
		if gr.introKey != "" {
			payload["introKey"] = gr.introKey
		}
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", gr.studioURL+"/luvia", bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gr.apiKey)

	resp, err := gr.client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[gateway] register failed: %v\n", err)
		return false
	}
	resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		fmt.Fprintf(os.Stderr, "[gateway] register failed: HTTP %d\n", resp.StatusCode)
		return false
	}
	fmt.Fprintf(os.Stderr, "[gateway] registered as %s\n", gr.instanceID)
	return true
}

func (gr *GatewayRegistrar) heartbeatLoop() {
	gr.heartbeatLoopEvery(30 * time.Second)
}

func (gr *GatewayRegistrar) heartbeatLoopEvery(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			gr.heartbeat()
		case <-gr.done:
			return
		}
	}
}

func (gr *GatewayRegistrar) heartbeat() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	memMB := float64(m.Alloc) / 1024 / 1024

	apiName := "svc:heartbeat"
	if gr.nodeType == "service" {
		apiName = "svc:heartbeatServiceNode"
	}
	dependencies := make([]lux.RuntimeDependencyStats, 0)
	if gr.dependencyStats != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		dependencies = gr.dependencyStats.RuntimeDependencies(ctx)
		cancel()
	}
	body, _ := json.Marshal(map[string]any{
		"$api":         apiName,
		"apiKey":       gr.apiKey,
		"projectId":    gr.projectID,
		"instanceId":   gr.instanceID,
		"version":      gr.version,
		"uptime":       time.Since(gr.startedAt),
		"memoryMB":     memMB,
		"cpuPercent":   gr.cpu.percent(),
		"dependencies": dependencies,
	})

	req, err := http.NewRequest("POST", gr.studioURL+"/luvia", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gr.apiKey)

	resp, err := gr.client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		gr.register()
	}
}

func (gr *GatewayRegistrar) deregister() {
	if gr.studioURL == "" || gr.client == nil {
		return
	}
	apiName := "svc:deregisterGateway"
	if gr.nodeType == "service" {
		apiName = "svc:deregisterServiceNode"
	}
	body, _ := json.Marshal(map[string]any{
		"$api":       apiName,
		"apiKey":     gr.apiKey,
		"projectId":  gr.projectID,
		"instanceId": gr.instanceID,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gr.studioURL+"/luvia", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gr.apiKey)
	resp, err := gr.client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}
