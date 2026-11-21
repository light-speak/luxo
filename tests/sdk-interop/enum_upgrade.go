package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"time"
)

// Capture the old service's real introspection bytes, close that deployment,
// then expose the immutable snapshot solely for SDK generation. Business calls
// always go to the separately compiled newer service, never this snapshot.
func withEnumUpgradeFixture(gateway bool, use func(endpoint, schemaEndpoint, scratch string) error) error {
	var snapshot []byte
	err := withFixtureMode(gateway, func(endpoint, _ string) error {
		var err error
		snapshot, err = readFixtureSchema(endpoint)
		return err
	})
	if err != nil {
		return err
	}
	schema := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !r.URL.Query().Has("$schema") || r.Header.Get("X-Introspection-Key") != "sdk-interop" {
			http.Error(w, "introspection only", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(snapshot)
	}))
	defer schema.Close()
	return withBuiltFixtureRevision(true, func(binary, scratch string) (result error) {
		deployment, err := startFixtureDeployment(binary, gateway)
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, deployment.close()) }()
		return use(deployment.endpoint, schema.URL+"/luvia", scratch)
	})
}

func readFixtureSchema(endpoint string) ([]byte, error) {
	request, err := http.NewRequest(http.MethodGet, endpoint+"?$schema", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Introspection-Key", "sdk-interop")
	client := &http.Client{Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fixture introspection status %d", response.StatusCode)
	}
	return io.ReadAll(response.Body)
}
