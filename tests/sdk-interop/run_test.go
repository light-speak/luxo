package main

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestFixtureRunnerRequiresCommand(t *testing.T) {
	if err := runFixtureCommand(nil, false); err == nil {
		t.Fatal("accepted missing consumer command")
	}
}

func TestFixtureRunnerReportsActualTopology(t *testing.T) {
	t.Chdir("../..")
	t.Setenv("LUXO_INTEROP_TOPOLOGY", "stale")
	for _, topology := range []string{"direct", "gateway"} {
		t.Run(topology, func(t *testing.T) {
			args := []string{"sh", "-c", `test "$LUXO_INTEROP_TOPOLOGY" = "$1"`, "fixture-consumer", topology}
			if err := runFixtureCommand(args, topology == "gateway"); err != nil {
				t.Fatalf("consumer did not receive its actual topology: %v", err)
			}
		})
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
