package main

import (
	_ "embed"
	"errors"
	"net/url"
)

//go:embed deployment.go.txt
var fixtureDeploymentServer []byte

type fixtureDeployment struct {
	endpoint  string
	addresses []string
	processes []*fixtureProcess
}

func startFixtureDeployment(binary string, gateway bool) (_ *fixtureDeployment, result error) {
	deployment := &fixtureDeployment{}
	defer func() {
		if result != nil {
			result = errors.Join(result, deployment.close())
		}
	}()
	var args []string
	if gateway {
		backend, err := deployment.start(binary, "tcp", "-rpc")
		if err != nil {
			return nil, err
		}
		address, _ := url.Parse(backend)
		args = []string{"-gateway", address.Host}
	}
	endpoint, err := deployment.start(binary, "http", args...)
	if err != nil {
		return nil, err
	}
	deployment.endpoint = endpoint
	return deployment, nil
}

func (d *fixtureDeployment) start(binary, scheme string, args ...string) (string, error) {
	process, err := startFixture(binary, args...)
	if err != nil {
		return "", err
	}
	d.processes = append(d.processes, process)
	endpoint, err := process.endpointScheme(scheme)
	if err != nil {
		return "", err
	}
	address, _ := url.Parse(endpoint)
	d.addresses = append(d.addresses, address.Host)
	return endpoint, nil
}

func (d *fixtureDeployment) close() (result error) {
	// The gateway closes its RPC clients before the backend is stopped.
	for index := len(d.processes) - 1; index >= 0; index-- {
		result = errors.Join(result, d.processes[index].close())
	}
	return result
}
