package main

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"

	"github.com/light-speak/luxo/internal/benchlayout"
)

const rpcHandlerPanicFixture = "handler_panic_bench_test.go"
const rpcHandlerPanicSHA = "d3f4a6e1b9e267997b4ab7d33a432e3345f32cc7fe4c248fa5fb47e5d94e9338"

func writeRPCHandlerPanicOverlay(pkg, workspace string) (string, error) {
	data, err := readRegularTestFixture(filepath.Join(pkg, rpcHandlerPanicFixture))
	if err != nil {
		return "", err
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != rpcHandlerPanicSHA {
		return "", fmt.Errorf("RPC handler panic fixture does not match the verified source")
	}
	// Keep the native witness's entire file, including the frozen handler and error sink.
	return retainBenchmarkTestFiles(pkg, workspace, "RPC handler panic", rpcHandlerPanicFixture)
}

func rpcBenchmarkOverlay(root, pkg, profile string) (string, func() error, error) {
	switch profile {
	case benchlayout.RPCOwnedRequestProfile:
		return packageTestOverlay(root, pkg, "bench-rpc-owned-request-*", writeRPCOwnedRequestOverlay)
	case benchlayout.RPCDeadlineProfile:
		return packageTestOverlay(root, pkg, "bench-rpc-deadline-*", writeRPCDeadlineOverlay)
	case benchlayout.RPCNilContextGuardProfile:
		return packageTestOverlay(root, pkg, "bench-rpc-nil-context-guard-*", writeRPCNilContextGuardOverlay)
	case benchlayout.RPCPreparedFrameProfile:
		return pairedBenchmarkOverlay(root, pkg, profile, rpcPreparedFrameFixture)
	case benchlayout.RPCHandlerPanicProfile:
		return packageTestOverlay(root, pkg, "bench-rpc-handler-panic-*", writeRPCHandlerPanicOverlay)
	default:
		return "", nil, fmt.Errorf("unsupported RPC benchmark profile %q", profile)
	}
}
