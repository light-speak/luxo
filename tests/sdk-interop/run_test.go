package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	luxschema "github.com/light-speak/luxo/pkg/lux/schema"
)

func TestEnumUpgradeRetainsOldSchemaAndClosesBothEndpoints(t *testing.T) {
	t.Chdir("../..")
	for _, gateway := range []bool{false, true} {
		var endpoints []string
		failure := errors.New("upgrade consumer failed")
		err := withEnumUpgradeFixture(gateway, func(endpoint, schemaEndpoint, _ string) error {
			endpoints = []string{endpoint, schemaEndpoint}
			var revisions [2]luxschema.Schema
			for index, address := range endpoints {
				body, err := readFixtureSchema(address)
				if err != nil {
					return err
				}
				if err := json.Unmarshal(body, &revisions[index]); err != nil {
					return err
				}
				want := "OPEN,CLOSED,FUTURE"
				if index == 1 {
					want = "OPEN,CLOSED"
				}
				if got := strings.Join(revisions[index].Enums["FixtureState"].Values, ","); got != want {
					t.Errorf("gateway=%t endpoint=%d: %s, want %s", gateway, index, got, want)
				}
			}
			verifyFixtureUpgradeIDs(t, &revisions[1], &revisions[0])
			if err := verifyGatewayEcho(endpoint, "aFutureInteger", "9223372036854775807"); err != nil {
				return err
			}
			if err := verifyGatewayEcho(endpoint, "echoInteger", "-9223372036854775808"); err != nil {
				return err
			}
			if err := verifyFixtureAddedDefault(endpoint); err != nil {
				return err
			}
			return failure
		})
		if !errors.Is(err, failure) || len(endpoints) != 2 {
			t.Fatalf("upgrade failure lost: %v", err)
		}
		for _, address := range endpoints {
			if _, err := readFixtureSchema(address); err == nil {
				t.Fatalf("upgrade endpoint survived failure: %s", address)
			}
		}
	}
}

func verifyFixtureUpgradeIDs(t *testing.T, old, next *luxschema.Schema) {
	t.Helper()
	maxAPI := 0
	for name, before := range old.APIs {
		after := next.APIs[name]
		if after == nil || after.ID != before.ID || after.ReturnType != before.ReturnType {
			t.Fatalf("upgrade changed existing API identity: %s", name)
		}
		maxAPI = max(maxAPI, before.ID)
		for _, param := range before.Params {
			found := false
			for _, current := range after.Params {
				found = found || current == param
			}
			if !found {
				t.Fatalf("upgrade changed parameter identity: %s.%s", name, param.Name)
			}
		}
	}
	if old.APIs["aFutureInteger"] != nil || next.APIs["aFutureInteger"] == nil || next.APIs["aFutureInteger"].ID <= maxAPI {
		t.Fatal("new API must use a new ID, not shift existing IDs")
	}
	if len(old.APIs["echoInteger"].Params) != 1 || len(next.APIs["echoInteger"].Params) != 2 {
		t.Fatal("upgraded service must actually have an additional parameter")
	}
	added := next.APIs["echoInteger"].Params[1]
	if added.Name != "delta" || !added.HasDefault || added.ID <= old.APIs["echoInteger"].Params[0].ID {
		t.Fatal("added default parameter must have its own stable identity")
	}
	for name, model := range old.Models {
		verifyFixtureFieldIDs(t, model.Fields, next.Models[name].Fields)
	}
	for name, typ := range old.Types {
		verifyFixtureFieldIDs(t, typ.Fields, next.Types[name].Fields)
	}
	if len(next.Models["PageRecord"].Fields) != len(old.Models["PageRecord"].Fields)+1 {
		t.Fatal("upgraded model must have a genuinely additional field")
	}
}

func verifyFixtureAddedDefault(endpoint string) error {
	status, body, err := fixtureJSONRequest(endpoint, `{"$api":"echoInteger","value":40,"delta":2}`)
	if err != nil {
		return err
	}
	var response struct{ Data json.RawMessage }
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	if status != 200 || string(response.Data) != "42" {
		return errors.New("new service ignored the explicitly supplied added parameter")
	}
	return nil
}

func verifyFixtureFieldIDs(t *testing.T, old, next []luxschema.Field) {
	t.Helper()
	for _, field := range old {
		found := false
		for _, current := range next {
			found = found || current.Name == field.Name && current.ID == field.ID
		}
		if !found {
			t.Fatalf("upgrade changed field identity: %s", field.Name)
		}
	}
}

func TestFixtureRunnerRequiresCommand(t *testing.T) {
	if err := runFixtureCommand(nil, false); err == nil {
		t.Fatal("accepted missing consumer command")
	}
	if err := runEnumUpgradeCommand(nil, false); err == nil {
		t.Fatal("accepted missing upgrade consumer command")
	}
}

func TestFixtureRunnerReportsActualTopology(t *testing.T) {
	t.Chdir("../..")
	t.Setenv("LUXO_INTEROP_TOPOLOGY", "stale")
	t.Setenv("LUXO_INTEROP_SCHEMA_ENDPOINT", "stale")
	t.Setenv("LUXO_INTEROP_ENUM_UPGRADE", "true")
	for _, topology := range []string{"direct", "gateway"} {
		t.Run(topology, func(t *testing.T) {
			args := []string{"sh", "-c", `test "$LUXO_INTEROP_TOPOLOGY" = "$1" && test "$LUXO_INTEROP_SCHEMA_ENDPOINT" = "$LUXO_INTEROP_ENDPOINT" && test "$LUXO_INTEROP_ENUM_UPGRADE" = false`, "fixture-consumer", topology}
			if err := runFixtureCommand(args, topology == "gateway"); err != nil {
				t.Fatalf("consumer did not receive its actual topology: %v", err)
			}
		})
	}
}

func TestFixtureSchemaReaderRejectsInvalidEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	for _, endpoint := range []string{"http://[", server.URL} {
		if _, err := readFixtureSchema(endpoint); err == nil {
			t.Fatalf("accepted invalid introspection endpoint %q", endpoint)
		}
	}
}

func TestFixtureRunnerClosesServerAfterConsumerFailure(t *testing.T) {
	t.Chdir("../..")
	var endpoint, directory string
	failure := errors.New("consumer failure")
	err := withFixture(func(address, scratch string) error {
		endpoint = address
		directory = scratch
		if !strings.Contains(scratch, "/.tmp/sdk-interop-run-") {
			t.Fatalf("scratch escaped project: %s", scratch)
		}
		client := &http.Client{Timeout: time.Second}
		request, err := http.NewRequest(http.MethodGet, address+"?$schema", nil)
		if err != nil {
			return err
		}
		request.Header.Set("X-Introspection-Key", "sdk-interop")
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), "echoInteger") {
			t.Fatalf("unexpected introspection: status %d, error %v", response.StatusCode, err)
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("lost consumer failure: %v", err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture scratch survived consumer failure: %v", err)
	}
	client := &http.Client{Timeout: time.Second}
	if response, err := client.Get(endpoint); err == nil {
		response.Body.Close()
		t.Fatal("fixture server survived consumer failure")
	}
}

func TestFixtureRunnerRetainsScratchAfterUnverifiedProcessCleanup(t *testing.T) {
	t.Chdir("../..")
	root, err := filepath.Abs(".tmp")
	if err != nil {
		t.Fatal(err)
	}
	var directory string
	err = withBuiltFixture(func(_, scratch string) error {
		directory = scratch
		return errFixtureProcessCleanup
	})
	if directory == "" {
		t.Fatalf("fixture did not start: %v", err)
	}
	if filepath.Dir(directory) != root {
		t.Fatalf("unexpected scratch %s", directory)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	if !errors.Is(err, errFixtureProcessCleanup) || !strings.Contains(err.Error(), "retaining fixture scratch") {
		t.Fatalf("lost cleanup failure: %v", err)
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatalf("removed unverified scratch: %v", err)
	}
}

func TestFixtureEndpointRejectsInvalidAddresses(t *testing.T) {
	for _, address := range []string{"", "garbage", "https://127.0.0.1:1234", "http://example.com:1234", "http://127.0.0.1", "http://127.0.0.1:bad"} {
		t.Run(address, func(t *testing.T) {
			process := fixtureProcess{output: io.NopCloser(strings.NewReader(address + "\n"))}
			if _, err := process.endpoint(); err == nil {
				t.Fatalf("accepted unsafe fixture endpoint %q", address)
			}
		})
	}
}

func TestFixtureEndpointTimeoutUsesFakeTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reader, writer := io.Pipe()
		defer reader.Close()
		defer writer.Close()
		process := fixtureProcess{output: reader}
		started := time.Now()
		if _, err := process.endpoint(); err == nil || !strings.Contains(err.Error(), "did not become ready") {
			t.Fatalf("missing readiness timeout: %v", err)
		}
		if elapsed := time.Since(started); elapsed != 10*time.Second {
			t.Fatalf("unexpected fake readiness deadline: %s", elapsed)
		}
	})
}

func TestFixtureStartRejectsMissingExecutable(t *testing.T) {
	if process, err := startFixture("./no-such-sdk-interop-executable"); err == nil || process != nil {
		t.Fatalf("accepted missing executable: %v, %v", process, err)
	}
	for _, gateway := range []bool{false, true} {
		if deployment, err := startFixtureDeployment("./no-such-sdk-interop-executable", gateway); err == nil || deployment != nil {
			t.Fatalf("accepted missing deployment executable: %v, %v", deployment, err)
		}
	}
}

func TestFixtureEndpointRequiresExpectedProtocol(t *testing.T) {
	for _, scheme := range []string{"http", "tcp"} {
		process := fixtureProcess{output: io.NopCloser(strings.NewReader(scheme + "://127.0.0.1:12345\n"))}
		if endpoint, err := process.endpointScheme(scheme); err != nil || !strings.HasPrefix(endpoint, scheme+"://") {
			t.Fatalf("rejected owned %s endpoint: %q, %v", scheme, endpoint, err)
		}
	}
	process := fixtureProcess{output: io.NopCloser(strings.NewReader("tcp://127.0.0.1:12345\n"))}
	if _, err := process.endpoint(); err == nil {
		t.Fatal("accepted backend TCP endpoint as a public HTTP endpoint")
	}
}

func TestFixtureDeploymentClosesChildAfterInvalidReadiness(t *testing.T) {
	t.Chdir("../..")
	err := withBuiltFixture(func(binary, _ string) error {
		deployment := &fixtureDeployment{}
		defer deployment.close()
		if _, err := deployment.start(binary, "http", "-rpc"); err == nil {
			t.Fatal("accepted RPC backend as an HTTP gateway")
		}
		if len(deployment.processes) != 1 {
			t.Fatal("readiness failure lost ownership of started child")
		}
		if err := deployment.close(); err != nil {
			return err
		}
		if !deployment.processes[0].command.ProcessState.Exited() {
			t.Fatal("child survived failed readiness")
		}
		return deployment.close()
	})
	if err != nil {
		t.Fatal(err)
	}
}
