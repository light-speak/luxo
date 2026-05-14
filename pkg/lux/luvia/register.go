package luvia

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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
	endpoint := fmt.Sprintf("http://%s:%s", instanceID, port)

	gr := &GatewayRegistrar{
		studioURL:  studioURL,
		apiKey:     apiKey,
		projectID:  projectID,
		instanceID: instanceID,
		endpoint:   endpoint,
		done:       make(chan struct{}),
	}

	// Register on startup
	gr.register()

	// Start heartbeat loop
	go gr.heartbeatLoop()

	return gr
}

// Close stops the heartbeat loop.
func (gr *GatewayRegistrar) Close() {
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

	resp, err := http.DefaultClient.Do(req)
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
	body, _ := json.Marshal(map[string]any{
		"$api":       "heartbeat",
		"apiKey":     gr.apiKey,
		"instanceId": gr.instanceID,
	})

	req, err := http.NewRequest("POST", gr.studioURL+"/luvia", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}
