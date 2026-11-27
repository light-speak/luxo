package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/light-speak/luxo/internal/benchlayout"
)

var apiIdentityFixtures = []string{
	"identity_reader_bench_test.go",
	"identity_reader_fuzz_test.go",
	"identity_reader_test.go",
	"identity_value_test.go",
}

const luviaObservationFixture = "metrics_retention_test.go"

var luviaPrimaryTraceFixtures = map[string]bool{
	"metrics_json_reference_test.go": true,
	"metrics_json_test.go":           true,
	luviaObservationFixture:          true,
	"primary_keys_baseline_test.go":  true,
	"primary_keys_bench_test.go":     true,
}

func writeAPIBenchmarkOverlay(pkg, workspace string) (string, error) {
	replacements, err := apiBenchmarkReplacements(pkg)
	if err != nil {
		return "", err
	}
	return writeTestOverlay(workspace, replacements)
}

func apiBenchmarkReplacements(pkg string) (map[string]string, error) {
	replacements := make(map[string]string, len(apiIdentityFixtures))
	for _, name := range apiIdentityFixtures {
		path := filepath.Join(pkg, name)
		if err := validateAPIFixture(path); err != nil {
			return nil, fmt.Errorf("API identity fixture %s: %w", name, err)
		}
		replacements[path] = ""
	}
	return replacements, nil
}

const (
	apiRequestLogFixture       = "request_log_async_test.go"
	apiRequestLogProjectionSHA = "ce47aebb7d132885536fe9eb2fb4a12b1f08d7b0d68ecc3b5f24b10b4dd720dc"
	apiRequestLogSourceSHA     = "18953d4af73fe0cdc9d8cbced337215e73f2eade18d892a31322534ca3a9c022"
)

func writeAPIRequestLogOverlay(pkg, workspace, projectionSHA, sourceSHA string) (string, error) {
	replacements, err := apiBenchmarkReplacements(pkg)
	if err != nil {
		return "", err
	}
	source := filepath.Join(pkg, apiRequestLogFixture)
	data, err := readRegularTestFixture(source)
	if err != nil {
		return "", err
	}
	projected, err := apiRequestLogProjection(data, projectionSHA, sourceSHA)
	if err != nil {
		return "", err
	}
	if projected != nil {
		path := filepath.Join(workspace, apiRequestLogFixture)
		if err := os.WriteFile(path, projected, 0o600); err != nil {
			return "", err
		}
		replacements[source] = path
	}
	return writeTestOverlay(workspace, replacements)
}

func apiRequestLogProjection(data []byte, projectionSHA, sourceSHA string) ([]byte, error) {
	switch fmt.Sprintf("%x", sha256.Sum256(data)) {
	case projectionSHA:
		return nil, nil
	case sourceSHA:
		// The two worker correctness tests and their helpers were appended together.
		// Keep the verified benchmark bodies and every other test, including HTTP completion sampling.
		prefix, _, _ := bytes.Cut(data, []byte("\ntype interruptedRequestLogWriter struct {\n"))
		projected := bytes.Replace(prefix, []byte("\t\"runtime\"\n"), nil, 1)
		if fmt.Sprintf("%x", sha256.Sum256(projected)) != projectionSHA {
			return nil, errors.New("API request-log fixture does not match the verified projection")
		}
		return projected, nil
	default:
		return nil, errors.New("unsupported API request-log fixture; use api-non-identity for stock diagnosis")
	}
}

func validateAPIFixture(path string) error {
	data, err := readRegularTestFixture(path)
	if err != nil {
		return err
	}
	file, err := parseBenchmarkFixture(token.NewFileSet(), path, data)
	if err != nil {
		return err
	}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Benchmark") &&
			!benchlayout.IsAPIIdentityBenchmark(fn.Name.Name) {
			return fmt.Errorf("non-identity benchmark %s uses the ordinary identity fixture", fn.Name.Name)
		}
	}
	return nil
}

func writeLuviaBenchmarkOverlay(pkg, workspace string) (string, error) {
	entries, err := os.ReadDir(pkg)
	if err != nil {
		return "", err
	}
	replacements := make(map[string]string)
	retained := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(pkg, name)
		replacement, err := projectLuviaFixture(path, workspace)
		if err != nil {
			return "", fmt.Errorf("Luvia primary and trace fixture %s: %w", name, err)
		}
		if luviaPrimaryTraceFixtures[name] {
			retained++
			if replacement == "" {
				continue
			}
		}
		replacements[path] = replacement
	}
	if retained != len(luviaPrimaryTraceFixtures) {
		return "", errors.New("missing Luvia primary and trace test fixture")
	}
	return writeTestOverlay(workspace, replacements)
}

const apiBinaryParamLookupFixture = "binary_param_lookup_test.go"

const (
	apiIdentityConditionFixture       = "identity_reader_bench_test.go"
	apiIdentityConditionSHA           = "20d45a1c2a71419d6c2c44e33b49e691cf15f3522f6e1041541dbce5097a3c5f"
	apiHandlerPanicFixture            = "handler_panic_bench_test.go"
	apiHandlerPanicSHA                = "01122e4a09e5c6d6a20018f47566fb801ad4888db504fd9d2c932be56146e302"
	apiWSRegistrationsFixture         = "websocket_subscriptions_bench_test.go"
	apiWSRegistrationsSHA             = "5abe6b7846bb541fa73512dd924d2eec3b4a66c1ed64f5839ee658f43492b584"
	apiResponseOriginBenchmarkFixture = "response_origin_bench_test.go"
	apiResponseOriginBenchmarkSHA     = "73022ebed1b57a2f54344199b406cbc20028063819f634c74eab84a867feb1e0"
	apiResponseOriginHelperFixture    = "response_origin_test.go"
	apiResponseOriginHelperSHA        = "42fb8c4bf2d07d23138b27c10302a54e42ab5de674b0ff890c55894e06cc5973"
	apiResponseOriginProjectionSHA    = "386e2dd6bc21b04dd35e49a9201ca0df47bf727288b9e6aa2301828336cf8683"
)

var apiBenchmarkFileFixtures = map[string]struct{ name, sourceSHA string }{
	benchlayout.APIIdentityConditionProfile:      {apiIdentityConditionFixture, apiIdentityConditionSHA},
	benchlayout.APIHandlerPanicProfile:           {apiHandlerPanicFixture, apiHandlerPanicSHA},
	benchlayout.APIWebSocketRegistrationsProfile: {apiWSRegistrationsFixture, apiWSRegistrationsSHA},
}

func writeAPIBenchmarkFileOverlay(pkg, workspace, profile string) (string, error) {
	fixture := apiBenchmarkFileFixtures[profile]
	if err := verifyAPIBenchmarkFixture(pkg, profile, fixture.name, fixture.sourceSHA); err != nil {
		return "", err
	}
	// Keep the native witness's complete benchmark file, including its companion declarations.
	return retainBenchmarkTestFiles(pkg, workspace, profile, fixture.name)
}

func verifyAPIBenchmarkFixture(pkg, profile, name, sourceSHA string) error {
	data, err := readRegularTestFixture(filepath.Join(pkg, name))
	if err != nil {
		return fmt.Errorf("%s fixture: %w", profile, err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != sourceSHA {
		return fmt.Errorf("%s fixture does not match the verified source", profile)
	}
	return nil
}

var apiResponseOriginHelper = pairedBenchmarkFixture{
	name: apiResponseOriginHelperFixture, sourceSHA: apiResponseOriginHelperSHA, projectionSHA: apiResponseOriginProjectionSHA,
	imports: []string{`"testing"`, `"time"`}, functions: []string{"testResponseOrigin"},
}

func writeAPIResponseOriginOverlay(pkg, workspace string) (string, error) {
	const profile = benchlayout.APIResponseOriginRunProfile
	if err := verifyAPIBenchmarkFixture(pkg, profile, apiResponseOriginBenchmarkFixture, apiResponseOriginBenchmarkSHA); err != nil {
		return "", err
	}
	replacements, err := retainedBenchmarkTestReplacements(pkg, profile, apiResponseOriginBenchmarkFixture, apiResponseOriginHelperFixture)
	if err != nil {
		return "", err
	}
	// The native witness retains the whole benchmark file and only its exact domain helper.
	helper := filepath.Join(pkg, apiResponseOriginHelperFixture)
	projection, err := apiResponseOriginHelper.projectFile(helper, workspace)
	if err != nil {
		return "", fmt.Errorf("%s fixture %s: %w", profile, apiResponseOriginHelperFixture, err)
	}
	replacements[helper] = projection
	return writeTestOverlay(workspace, replacements)
}

func writeAPIBinaryParamLookupOverlay(pkg, workspace string) (string, error) {
	return retainBenchmarkTestFiles(pkg, workspace, "API binary parameter lookup", apiBinaryParamLookupFixture)
}

func writeAPILocalRouteExportOverlay(pkg, workspace string) (string, error) {
	return retainBenchmarkTestFiles(pkg, workspace, "API local route export", "local_input_bench_test.go")
}

func writeLuviaMergeDestinationOverlay(pkg, workspace string) (string, error) {
	// The merge benchmark shares the primary-key input builder and its baseline fixture.
	return retainBenchmarkTestFiles(pkg, workspace, "Luvia merge destination",
		"merger_bench_test.go", "primary_keys_bench_test.go", "primary_keys_baseline_test.go")
}

func writeLuviaResolvedFieldsOverlay(pkg, workspace string) (string, error) {
	// grouped_response_test.go provides the fixture used by the retained
	// benchmark file. The remaining files keep the verified isolated link layout.
	return retainBenchmarkTestFiles(pkg, workspace, "Luvia resolved fields",
		"grouped_response_bench_test.go", "grouped_response_test.go",
		"merger_bench_test.go", "primary_keys_bench_test.go", "primary_keys_baseline_test.go")
}

func writeLuviaConvenienceKeysOverlay(pkg, workspace string) (string, error) {
	// Retain the same benchmark and input helpers as the native isolation comparison.
	return retainBenchmarkTestFiles(pkg, workspace, "Luvia convenience keys",
		"convenience_keys_bench_test.go", "primary_keys_bench_test.go", "primary_keys_baseline_test.go")
}

func retainBenchmarkTestFiles(pkg, workspace, description string, fixtures ...string) (string, error) {
	replacements, err := retainedBenchmarkTestReplacements(pkg, description, fixtures...)
	if err != nil {
		return "", err
	}
	return writeTestOverlay(workspace, replacements)
}

func retainedBenchmarkTestReplacements(pkg, description string, fixtures ...string) (map[string]string, error) {
	entries, err := os.ReadDir(pkg)
	if err != nil {
		return nil, err
	}
	replacements := make(map[string]string)
	retained := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(pkg, name)
		data, err := readRegularTestFixture(path)
		if err != nil {
			return nil, fmt.Errorf("%s fixture %s: %w", description, name, err)
		}
		if _, err := parseBenchmarkFixture(token.NewFileSet(), path, data); err != nil {
			return nil, err
		}
		if slices.Contains(fixtures, name) {
			retained++
			continue
		}
		replacements[path] = ""
	}
	if retained != len(fixtures) {
		return nil, fmt.Errorf("missing %s test fixture", description)
	}
	return replacements, nil
}

func projectLuviaFixture(path, workspace string) (string, error) {
	data, err := readRegularTestFixture(path)
	if err != nil {
		return "", err
	}
	positions := token.NewFileSet()
	file, err := parseBenchmarkFixture(positions, path, data)
	if err != nil || filepath.Base(path) != luviaObservationFixture {
		return "", err
	}
	for _, declaration := range file.Decls {
		if isFunction(declaration, "observationTestTrace") {
			return writeLuviaObservation(workspace, positions, declaration, data)
		}
	}
	return "", errors.New("missing observationTestTrace declaration")
}

func writeLuviaObservation(workspace string, positions *token.FileSet, declaration ast.Decl, data []byte) (string, error) {
	const header = "package luvia\n\nimport (\n\t\"net/http\"\n\t\"time\"\n\n\t\"github.com/light-speak/luxo/pkg/lux/api\"\n)\n\n"
	file := positions.File(declaration.Pos())
	projected := append([]byte(header), data[file.Offset(declaration.Pos()):file.Offset(declaration.End())]...)
	path := filepath.Join(workspace, luviaObservationFixture)
	return path, os.WriteFile(path, append(projected, '\n'), 0o600)
}

func readRegularTestFixture(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("fixture must be a regular file")
	}
	return os.ReadFile(path)
}
