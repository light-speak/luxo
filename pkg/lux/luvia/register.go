package luvia

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"runtime/metrics"
	"strings"
	"sync"
	"time"
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
	if percent < 0 {
		return 0
	}
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
	if samples[0].Value.Kind() != metrics.KindFloat64 || samples[1].Value.Kind() != metrics.KindFloat64 {
		return 0
	}
	busy := samples[0].Value.Float64() - samples[1].Value.Float64()
	if busy < 0 {
		return 0
	}
	return busy
}

// GatewayRegistrar handles auto-registration and heartbeat with Luxo Studio.
type GatewayRegistrar struct {
	studioURL  string
	apiKey     string
	projectID  int
	instanceID string
	nodeType   string
	nodeName   string
	endpoint   string
	introKey   string
	version    string
	startedAt  time.Time
	cpu        cpuSampler
	done       chan struct{}
	closed     bool
	mu         sync.Mutex
	client     *http.Client
}

// NewGatewayRegistrar creates a registrar from environment variables.
// Returns nil if LUXO_STUDIO_URL or LUXO_API_KEY is not set.
func NewGatewayRegistrar(port string) *GatewayRegistrar {
	return newGatewayRegistrar(port, defaultGatewayVersion)
}

func newGatewayRegistrar(port, version string) *GatewayRegistrar {
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
		introKey:  os.Getenv("INTROSPECTION_KEY"),
		version:   version,
		startedAt: time.Now(),
		done:      make(chan struct{}),
		client:    &http.Client{Timeout: 10 * time.Second},
	}
	gr.cpu.percent()

	// Register async — don't block gateway boot
	go func() {
		gr.register()
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

// Close stops the heartbeat loop.
func (gr *GatewayRegistrar) Close() {
	gr.mu.Lock()
	defer gr.mu.Unlock()
	if gr.closed {
		return
	}
	gr.closed = true
	close(gr.done)
}

func (gr *GatewayRegistrar) register() {
	payload := map[string]any{
		"$api":       "svc:registerGateway",
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
	// introKey is nullable on the Studio side: omitting it means "keep the
	// stored key", so only send it when introspection is actually enabled.
	if gr.introKey != "" {
		payload["introKey"] = gr.introKey
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", gr.studioURL+"/luvia", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gr.apiKey)

	resp, err := gr.client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[gateway] register failed: %v\n", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		fmt.Fprintf(os.Stderr, "[gateway] register failed: HTTP %d\n", resp.StatusCode)
		return
	}
	fmt.Fprintf(os.Stderr, "[gateway] registered as %s\n", gr.instanceID)
}

func (gr *GatewayRegistrar) heartbeatLoop() {
	ticker := time.NewTicker(30 * time.Second)
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
	body, _ := json.Marshal(map[string]any{
		"$api":       apiName,
		"apiKey":     gr.apiKey,
		"instanceId": gr.instanceID,
		"version":    gr.version,
		"uptime":     time.Since(gr.startedAt),
		"memoryMB":   memMB,
		"cpuPercent": gr.cpu.percent(),
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
}
