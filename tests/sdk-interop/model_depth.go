package main

import _ "embed"

//go:embed model_depth.luxo
var fixtureModelDepthSchema string

//go:embed model_depth_server.go.txt
var fixtureModelDepthServer []byte

//go:embed model_depth_peer.go.txt
var fixtureModelDepthPeer []byte

func generateModelDepthFixture(out string) error {
	if err := validateOutput(out); err != nil {
		return err
	}
	files, err := compileFixtureRevision(fixtureModelDepthSchema, "")
	if err != nil {
		return err
	}
	files["main.go"] = fixtureModelDepthServer
	files["depth_peer.go"] = fixtureModelDepthPeer
	return writeFixtureFiles(out, files)
}
