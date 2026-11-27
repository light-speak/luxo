// benchrun compiles and runs historical benchmark groups across module splits.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/light-speak/luxo/internal/benchlayout"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, input io.Reader, output, errorOutput io.Writer) error {
	if len(args) == 1 && args[0] == "canonical" {
		return canonical(input, output)
	}
	if len(args) == 1 && args[0] == "performance-profile" {
		_, err := fmt.Fprintln(output, performanceProfile)
		return err
	}
	if len(args) == 2 && args[0] == "isolation-profile" {
		return writeIsolationProfiles(output, args[1])
	}
	if len(args) < 4 || (args[0] != "compile" && args[0] != "compile-performance" && args[0] != "compile-performance-baseline" && args[0] != "run" && args[0] != "run-performance") {
		return errors.New("usage: benchrun canonical | performance-profile | isolation-profile package | {compile|compile-performance|compile-performance-baseline|run|run-performance} revision binary-directory logical-package [profile] [test flags]")
	}
	profile, testFlags, err := performanceArguments(args[0], args[3], args[4:])
	if err != nil {
		return err
	}
	root, err := filepath.Abs(args[1])
	if err != nil {
		return err
	}
	bin, err := filepath.Abs(args[2])
	if err != nil {
		return err
	}
	packages, err := performancePackages(root, args[3], profile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(bin, 0755); err != nil {
		return err
	}
	for _, path := range packages {
		name := strings.ReplaceAll(path, "/", "-")
		if profile != benchlayout.DefaultProfile {
			name += "-" + profile
		}
		binary := filepath.Join(bin, name+".test")
		if args[0] != "run" && args[0] != "run-performance" {
			if err := compileBenchmarks(root, binary, path, args[0], profile, output, errorOutput); err != nil {
				return fmt.Errorf("%s %s: %w", args[0], path, err)
			}
			continue
		}
		cmd := exec.Command(binary, testFlags...)
		cmd.Dir = filepath.Join(root, path)
		cmd.Stdout, cmd.Stderr = output, errorOutput
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s %s: %w", args[0], path, err)
		}
	}
	return nil
}

func performancePackages(root, pkg, profile string) ([]string, error) {
	if profile == benchlayout.QueueStreamNameProfile {
		return benchlayout.Packages(root, benchlayout.NATSQueuePackage)
	}
	packages, err := benchlayout.Packages(root, pkg)
	if err != nil || profile != benchlayout.PGDurationWriteRejectProfile {
		return packages, err
	}
	for _, physical := range packages {
		if physical == benchlayout.PGPackage {
			return []string{physical}, nil
		}
	}
	return nil, fmt.Errorf("missing benchmark package %s in %s", benchlayout.PGPackage, root)
}

func writeIsolationProfiles(output io.Writer, pkg string) error {
	for _, spec := range benchlayout.IsolationProfiles(pkg) {
		if _, err := fmt.Fprintf(output, "%s\t%s\n", spec.Name, spec.Filter); err != nil {
			return err
		}
	}
	return nil
}

func performanceArguments(command, pkg string, args []string) (string, []string, error) {
	if command == "run" || command == "compile" {
		return benchlayout.DefaultProfile, args, nil
	}
	if len(args) == 0 && command != "run-performance" {
		return benchlayout.DefaultProfile, nil, nil
	}
	if len(args) == 0 || !benchlayout.ValidPerformanceProfile(pkg, args[0]) {
		return "", nil, fmt.Errorf("invalid performance profile for %s: %v", pkg, args)
	}
	if command != "run-performance" && len(args) != 1 {
		return "", nil, errors.New("unexpected performance compile arguments")
	}
	return args[0], args[1:], nil
}

func compileOverlay(root, packagePath string, baseline bool, profile string) (string, func() error, error) {
	switch profile {
	case benchlayout.LuxShortPathProfile:
		return packageTestOverlay(root, packagePath, "bench-lux-short-path-*", writeLuxShortPathOverlay)
	case benchlayout.CodecReaderProfile:
		return codecReaderOverlay(root)
	case benchlayout.RPCOwnedRequestProfile:
		return packageTestOverlay(root, packagePath, "bench-rpc-owned-request-*", writeRPCOwnedRequestOverlay)
	case benchlayout.RPCDeadlineProfile:
		return packageTestOverlay(root, packagePath, "bench-rpc-deadline-*", writeRPCDeadlineOverlay)
	case benchlayout.RPCNilContextGuardProfile:
		return packageTestOverlay(root, packagePath, "bench-rpc-nil-context-guard-*", writeRPCNilContextGuardOverlay)
	case benchlayout.RPCPreparedFrameProfile:
		return pairedBenchmarkOverlay(root, packagePath, profile, rpcPreparedFrameFixture)
	case benchlayout.QueueStreamNameProfile:
		return pairedBenchmarkOverlay(root, packagePath, profile, queueStreamNameFixture)
	case benchlayout.APINonIdentityProfile:
		return packageTestOverlay(root, packagePath, "bench-api-non-identity-*", writeAPIBenchmarkOverlay)
	case benchlayout.APILocalRouteExportProfile:
		return packageTestOverlay(root, packagePath, "bench-api-local-route-export-*", writeAPILocalRouteExportOverlay)
	case benchlayout.APICanonicalJSONProfile:
		return packageTestOverlay(root, packagePath, "bench-api-canonical-json-*", writeAPICanonicalJSONOverlay)
	case benchlayout.APIBinaryParamLookupProfile:
		return packageTestOverlay(root, packagePath, "bench-api-binary-param-lookup-*", writeAPIBinaryParamLookupOverlay)
	case benchlayout.APIRequestLogProfile:
		return packageTestOverlay(root, packagePath, "bench-api-request-log-*", func(pkg, workspace string) (string, error) {
			return writeAPIRequestLogOverlay(pkg, workspace, apiRequestLogProjectionSHA, apiRequestLogSourceSHA)
		})
	case benchlayout.APIIdentityConditionProfile, benchlayout.APIHandlerPanicProfile, benchlayout.APIWebSocketRegistrationsProfile:
		return packageTestOverlay(root, packagePath, "bench-"+profile+"-*", func(pkg, workspace string) (string, error) {
			return writeAPIBenchmarkFileOverlay(pkg, workspace, profile)
		})
	case benchlayout.LuviaPrimaryTraceProfile:
		return packageTestOverlay(root, packagePath, "bench-luvia-primary-trace-*", writeLuviaBenchmarkOverlay)
	case benchlayout.LuviaGatewayIdentityProfile:
		return packageTestOverlay(root, packagePath, "bench-luvia-gateway-identity-*", writeLuviaGatewayIdentityOverlay)
	case benchlayout.LuviaAuthMiddlewareProfile:
		return packageTestOverlay(root, packagePath, "bench-luvia-auth-middleware-*", writeLuviaAuthMiddlewareOverlay)
	case benchlayout.LuviaMergeDestinationProfile:
		return packageTestOverlay(root, packagePath, "bench-luvia-merge-destination-*", writeLuviaMergeDestinationOverlay)
	case benchlayout.LuviaResolvedFieldsProfile:
		return packageTestOverlay(root, packagePath, "bench-luvia-resolved-fields-*", writeLuviaResolvedFieldsOverlay)
	case benchlayout.LuviaConvenienceKeysProfile:
		return packageTestOverlay(root, packagePath, "bench-luvia-convenience-keys-*", writeLuviaConvenienceKeysOverlay)
	case benchlayout.CodegenLiteralProfile:
		return packageTestOverlay(root, packagePath, "bench-codegen-integer-literals-*", writeCodegenLiteralOverlay)
	case benchlayout.CodegenReceiverProfile:
		return packageTestOverlay(root, packagePath, "bench-codegen-global-receiver-*", writeCodegenReceiverOverlay)
	case benchlayout.CodegenMigrationStateProfile:
		return packageTestOverlay(root, packagePath, "bench-codegen-migration-state-*", writeCodegenMigrationStateOverlay)
	case benchlayout.CodegenRelationLoaderProfile:
		return pairedBenchmarkOverlay(root, packagePath, profile, codegenRelationLoaderFixture)
	case benchlayout.SemanticEventEmitsProfile:
		return pairedBenchmarkOverlay(root, packagePath, profile, semanticEventEmitsFixture)
	default:
		return compileCompactBenchmarkOverlay(root, packagePath, baseline, profile)
	}
}

func compileBenchmarks(root, binary, packagePath, command, profile string, output, errorOutput io.Writer) (err error) {
	args := []string{"test", "-trimpath", "-c"}
	if command != "compile" {
		overlay, cleanup, overlayErr := compileOverlay(root, packagePath, command == "compile-performance-baseline", profile)
		if overlayErr != nil {
			return overlayErr
		}
		defer func() { err = errors.Join(err, cleanup()) }()
		if overlay != "" {
			args = append(args, "-overlay="+overlay)
		}
	}
	cmd := exec.Command("go", append(args, "./"+packagePath, "-o", binary)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(root, "go.work"))
	cmd.Stdout, cmd.Stderr = output, errorOutput
	return cmd.Run()
}

func canonical(input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	seen := make(map[string]bool)
	for scanner.Scan() {
		seen[benchlayout.Canonical(strings.TrimSpace(scanner.Text()))] = true
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		if path != "" {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		if _, err := fmt.Fprintln(output, path); err != nil {
			return err
		}
	}
	return nil
}
