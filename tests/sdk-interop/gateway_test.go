package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
	"time"
)

func TestFixtureGatewayPreservesJSONNumbers(t *testing.T) {
	t.Chdir("../..")
	data, err := os.ReadFile("pkg/lux/codec/testdata/protocol-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		JSONNumberTokens []string
		Int64Cases       []struct{ Value string }
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.JSONNumberTokens) == 0 || len(corpus.Int64Cases) == 0 {
		t.Fatal("missing numeric corpus")
	}
	err = withFixtureMode(true, func(endpoint, _ string) error {
		for _, item := range corpus.Int64Cases {
			for _, apiName := range []string{"echoInteger", "echoDuration"} {
				if err := verifyGatewayEcho(endpoint, apiName, item.Value); err != nil {
					return err
				}
			}
		}
		for _, token := range corpus.JSONNumberTokens {
			value := `{"n":` + token + `,"items":[` + token + `,null],"text":"精度 café"}`
			if err := verifyGatewayEcho(endpoint, "echoJSON", value); err != nil {
				return err
			}
		}
		return verifyFixturePage(endpoint)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func verifyGatewayEcho(endpoint, apiName, value string) error {
	status, body, err := fixtureJSONRequest(endpoint, `{"$api":"`+apiName+`","value":`+value+`}`)
	if err != nil {
		return err
	}
	var response struct{ Data json.RawMessage }
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	if status != 200 || string(response.Data) != value {
		return fmt.Errorf("gateway %s changed numeric value: %d %s, want %s", apiName, status, body, value)
	}
	return nil
}

func TestFixtureGatewayOwnsTwoProcessesAndFailsWithoutBackend(t *testing.T) {
	t.Chdir("../..")
	var endpoints []string
	err := withBuiltFixture(func(binary, _ string) (result error) {
		deployment, err := startFixtureDeployment(binary, true)
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, deployment.close()) }()
		if len(deployment.processes) != 2 {
			return fmt.Errorf("gateway requires two processes")
		}
		backend, gateway := deployment.processes[0], deployment.processes[1]
		if backend.command.Process.Pid == gateway.command.Process.Pid {
			return fmt.Errorf("shared backend and gateway PID")
		}
		endpoints = deployment.addresses
		if err := verifyGatewayEcho(deployment.endpoint, "echoInteger", "9223372036854775807"); err != nil {
			return err
		}
		if err := backend.close(); err != nil {
			return err
		}
		status, body, err := fixtureJSONRequest(deployment.endpoint, `{"$api":"echoInteger","value":1}`)
		if err != nil {
			return err
		}
		if status == 200 {
			return fmt.Errorf("gateway silently bypassed stopped RPC service: %s", body)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range endpoints {
		connection, err := net.DialTimeout("tcp", address, time.Second)
		if err == nil {
			connection.Close()
			t.Errorf("fixture listener survived cleanup: %s", address)
		}
	}
}
