package luvia

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"
)

// GatewayRegistrar handles auto-registration and heartbeat with Luxo Studio.
type GatewayRegistrar struct {
	studioURL  string
	apiKey     string
	projectID  int
	instanceID string
	endpoint   string
	done       chan struct{}
	closed     bool
	mu         sync.Mutex
	client     *http.Client
}

// NewGatewayRegistrar creates a registrar from environment variables.
// Returns nil if LUXO_STUDIO_URL or LUXO_API_KEY is not set.
func NewGatewayRegistrar(port string) *GatewayRegistrar {
	studioURL := os.Getenv("LUXO_STUDIO_URL")
	apiKey := os.Getenv("LUXO_API_KEY")
	if studioURL == "" || apiKey == "" {
		return nil
	}
	projectID := 0
	if v := os.Getenv("LUXO_PROJECT_ID"); v != "" {
		fmt.Sscanf(v, "%d", &projectID)
	}
	instanceID, _ := os.Hostname()

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
		endpoint:   endpoint,
		done:       make(chan struct{}),
		client:     &http.Client{Timeout: 10 * time.Second},
	}

	// Register async — don't block gateway boot
	go func() {
		gr.register()
		gr.heartbeatLoop()
	}()

	return gr
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
	body, _ := json.Marshal(map[string]any{
		"$api":       "registerGateway",
		"projectId":  gr.projectID,
		"name":       gr.instanceID,
		"endpoint":   gr.endpoint,
		"instanceId": gr.instanceID,
	})

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

	body, _ := json.Marshal(map[string]any{
		"$api":       "heartbeat",
		"instanceId": gr.instanceID,
		"memoryMB":   memMB,
		"cpuPercent": 0.0, // CPU percent requires sampling over time — placeholder
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
