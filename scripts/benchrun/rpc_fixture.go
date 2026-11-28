package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/internal/benchlayout"
)

const (
	rpcBoundaryPath             = "pkg/lux/rpc/public_boundary_test.go"
	rpcBoundaryMarker           = "// luxo:benchmark-isolated-correctness"
	rpcBoundarySHA              = "99658ed681f4110df0db3fc688931fa61cd656f3c62477c04a1ffd4bb567ca8e"
	rpcOwnedRequestFixture      = "request_reuse_test.go"
	rpcDeadlineBenchmarkFixture = "stream_termination_bench_test.go"
	rpcDeadlineContextFixture   = "stream_termination_test.go"
	rpcDeadlineBenchmarkSHA     = "d73d6805aa1aa0c5a0212d3cb246adc17ce92a116c4f31f83cf10b32ce531efb"
	rpcDeadlineContextSHA       = "237955931ab9e5c7791eed9965a0262c8e0902d0e0db328d5c0ec31b84ed446a"
	rpcNilContextGuardFixture   = "auth_context_bench_test.go"
	rpcNilContextGuardSHA       = "cd1b4a5dd0d0622ce19cfc144057482a1059cd61245cf38cf9205f46aaece2c3"
	// Discovery and sampling must consume the same benchmark compilation profile.
	performanceProfile = "rpc-boundary-overlay-v2 " + rpcBoundarySHA +
		compactBenchmarkFingerprint +
		"\ncodec-reader-overlay-v1\napi-non-identity-overlay-v1\nluvia-primary-trace-overlay-v1\ncodegen-integer-literals-overlay-v1" +
		"\ncodegen-global-receiver-overlay-v1 " + codegenReceiverProjectionSHA +
		"\nluvia-gateway-identity-overlay-v1 " + luviaGatewayIdentityProjectionSHA +
		"\nlux-short-path-overlay-v1 " + luxFilterFixture + "=" + luxFilterProjectionSHA +
		" " + luxDecimalFixture + "=" + luxDecimalProjectionSHA +
		"\ncodegen-migration-state-overlay-v1 " + codegenMigrationStateFixture + "=" + codegenMigrationStateFixtureSHA +
		" " + codegenReceiverFixture + "=" + codegenReceiverProjectionSHA +
		"\nluvia-merge-destination-overlay-v1\napi-local-route-export-overlay-v1" +
		"\napi-canonical-json-overlay-v1\nluvia-resolved-fields-overlay-v1\nluvia-convenience-keys-overlay-v1" +
		"\n" + benchlayout.APIIdentityConditionProfile + "-overlay-v1 " + apiIdentityConditionFixture + "=" + apiIdentityConditionSHA +
		"\n" + benchlayout.APIHandlerPanicProfile + "-overlay-v1 " + apiHandlerPanicFixture + "=" + apiHandlerPanicSHA +
		"\n" + benchlayout.APIWebSocketRegistrationsProfile + "-overlay-v1 " + apiWSRegistrationsFixture + "=" + apiWSRegistrationsSHA +
		"\n" + benchlayout.APIBodyReadProfile + "-overlay-v1 " + apiBodyReadFixture + "=" + apiBodyReadSHA +
		"\n" + benchlayout.APIJSONQuoteCandidateProfile + "-overlay-v1 " + apiJSONQuoteFixture + "=" + apiJSONQuoteSHA +
		" " + apiJSONQuoteReferenceFixture + "=" + apiJSONQuoteReferenceSHA +
		"\n" + benchlayout.APIJSONListMetadataProfile + "-overlay-v1 " + apiJSONListMetadataFilename + "=" + apiJSONListMetadataProjectionSHA + " " + apiJSONListMetadataSourceSHA +
		"\n" + benchlayout.APIResponseOriginRunProfile + "-overlay-v1 " + apiResponseOriginBenchmarkFixture + "=" + apiResponseOriginBenchmarkSHA +
		" " + apiResponseOriginHelperFixture + "=" + apiResponseOriginHelperSHA + ":" + apiResponseOriginProjectionSHA +
		"\nrpc-owned-request-overlay-v1 " + rpcOwnedRequestFixture +
		"\nrpc-deadline-classification-overlay-v1 " + rpcDeadlineBenchmarkFixture + "=" + rpcDeadlineBenchmarkSHA +
		" " + rpcDeadlineContextFixture + "=" + rpcDeadlineContextSHA +
		"\nrpc-nil-context-guard-overlay-v1 " + rpcNilContextGuardFixture + "=" + rpcNilContextGuardSHA +
		"\nrpc-prepared-frame-overlay-v1 " + rpcPreparedFrameFilename + "=" + rpcPreparedFrameProjectionSHA + " " + rpcPreparedFrameSourceSHA +
		"\nrpc-handler-panic-overlay-v1 " + rpcHandlerPanicFixture + "=" + rpcHandlerPanicSHA +
		"\nqueue-stream-name-overlay-v1 " + queueStreamNameFilename + "=" + queueStreamNameProjectionSHA + " " + queueStreamNameSourceSHA +
		"\nluvia-auth-middleware-overlay-v1 " + luviaAuthBenchmarkFixture + "=" + luviaAuthBenchmarkSHA +
		" " + luviaAuthHelperFixture + "=" + luviaAuthHelperSHA +
		"\napi-binary-param-lookup-overlay-v1 " + apiBinaryParamLookupFixture +
		"\napi-request-log-overlay-v1 " + apiRequestLogFixture + "=" + apiRequestLogProjectionSHA + " " + apiRequestLogSourceSHA +
		"\ncodegen-relation-loader-overlay-v1 " + codegenRelationLoaderFilename + "=" + codegenRelationLoaderProjectionSHA + " " + codegenRelationLoaderSourceSHA +
		"\nsemantic-event-emits-overlay-v1 " + semanticEventEmitsFilename + "=" + semanticEventEmitsProjectionSHA + " " + semanticEventEmitsSourceSHA + transportBenchmarkFingerprint + sealedCompilerFixtureBindings
)

func writeRPCOwnedRequestOverlay(pkg, workspace string) (string, error) {
	entries, err := os.ReadDir(pkg)
	if err != nil {
		return "", err
	}
	replacements := make(map[string]string)
	fixture := filepath.Join(pkg, rpcOwnedRequestFixture)
	for _, entry := range entries {
		if name := entry.Name(); strings.HasSuffix(name, "_test.go") {
			path := filepath.Join(pkg, name)
			replacement, err := projectRPCOwnedRequestFixture(path, workspace)
			if err != nil {
				return "", fmt.Errorf("RPC owned-request fixture %s: %w", name, err)
			}
			replacements[path] = replacement
		}
	}
	if replacements[fixture] == "" {
		return "", errors.New("missing RPC owned-request test fixture")
	}
	return writeTestOverlay(workspace, replacements)
}

func projectRPCOwnedRequestFixture(path, workspace string) (string, error) {
	data, err := readRegularTestFixture(path)
	if err != nil {
		return "", err
	}
	positions := token.NewFileSet()
	file, err := parseBenchmarkFixture(positions, path, data)
	if err != nil || filepath.Base(path) != rpcOwnedRequestFixture {
		return "", err
	}
	if file.Name.Name != "rpc" {
		return "", errors.New("owned-request fixture requires package rpc")
	}
	for _, name := range []string{"fmt", "io", "testing"} {
		if !slices.ContainsFunc(file.Imports, func(spec *ast.ImportSpec) bool {
			return spec.Name == nil && spec.Path.Value == strconv.Quote(name)
		}) {
			return "", fmt.Errorf("owned-request fixture requires the ordinary %s import", name)
		}
	}
	var benchmark ast.Decl
	for _, declaration := range file.Decls {
		if isFunction(declaration, benchlayout.RPCOwnedRequestBenchmark) {
			if benchmark != nil {
				return "", errors.New("owned-request fixture requires one BenchmarkLRPCOwnedRequest declaration")
			}
			benchmark = declaration
		}
	}
	if benchmark == nil {
		return "", errors.New("owned-request fixture requires one BenchmarkLRPCOwnedRequest declaration")
	}
	// Use the same source bytes and imports as the native isolation comparison.
	const header = "package rpc\nimport (\"fmt\"; \"io\"; \"testing\")\n"
	source := positions.File(benchmark.Pos())
	projected := append([]byte(header), data[source.Offset(benchmark.Pos()):source.Offset(benchmark.End())]...)
	output := filepath.Join(workspace, rpcOwnedRequestFixture)
	return output, os.WriteFile(output, append(projected, '\n', '\n'), 0o600)
}

type rpcDeadlineSource struct {
	header, start, lastFunction, receiver, end, sha string
}

var rpcDeadlineSources = map[string]rpcDeadlineSource{
	rpcDeadlineBenchmarkFixture: {
		header:       "package rpc\n\nimport (\n\t\"context\"\n\t\"fmt\"\n\t\"io\"\n\t\"os\"\n\t\"testing\"\n\t\"time\"\n)\n\n",
		start:        "var deadlineClassificationSink error\n",
		lastFunction: benchlayout.RPCDeadlineBenchmark,
		end:          "\n",
		sha:          rpcDeadlineBenchmarkSHA,
	},
	rpcDeadlineContextFixture: {
		header:       "package rpc\n\nimport (\n\t\"context\"\n\t\"time\"\n)\n\n",
		start:        "// Models the interval after a socket deadline fires",
		lastFunction: "Deadline",
		receiver:     "pendingDeadlineContext",
		end:          "\n\n",
		sha:          rpcDeadlineContextSHA,
	},
}

func writeRPCDeadlineOverlay(pkg, workspace string) (string, error) {
	return writeProjectedBenchmarkOverlay(pkg, workspace, "RPC deadline", projectRPCDeadlineFixture,
		rpcDeadlineBenchmarkFixture, rpcDeadlineContextFixture)
}

func writeRPCNilContextGuardOverlay(pkg, workspace string) (string, error) {
	return writeProjectedBenchmarkOverlay(pkg, workspace, "RPC nil-context guard", projectRPCNilContextGuardFixture, rpcNilContextGuardFixture)
}

func writeProjectedBenchmarkOverlay(pkg, workspace, description string, project func(string, string) (string, error), fixtures ...string) (string, error) {
	entries, err := os.ReadDir(pkg)
	if err != nil {
		return "", err
	}
	replacements := make(map[string]string)
	retained := make(map[string]bool)
	for _, entry := range entries {
		if name := entry.Name(); strings.HasSuffix(name, "_test.go") {
			path := filepath.Join(pkg, name)
			projection, err := project(path, workspace)
			if err != nil {
				return "", fmt.Errorf("%s fixture %s: %w", description, name, err)
			}
			retained[name] = projection != ""
			// The projector returns the source path when it retains the original test file.
			if projection != path {
				replacements[path] = projection
			}
		}
	}
	for _, name := range fixtures {
		if !retained[name] {
			return "", fmt.Errorf("missing %s fixture %s", description, name)
		}
	}
	return writeTestOverlay(workspace, replacements)
}

func projectRPCNilContextGuardFixture(path, workspace string) (string, error) {
	data, err := readRegularTestFixture(path)
	if err != nil {
		return "", err
	}
	positions := token.NewFileSet()
	file, err := parseBenchmarkFixture(positions, path, data)
	if err != nil || filepath.Base(path) != rpcNilContextGuardFixture {
		return "", err
	}
	if file.Name.Name != "rpc" {
		return "", errors.New("nil-context guard fixture requires package rpc")
	}
	end := rpcFixtureFunctionEnd(file, benchlayout.RPCNilContextGuardBenchmark, "", positions.File(file.Pos()))
	if end < 0 {
		return "", errors.New("missing or repeated fixture end")
	}
	// Keep the native witness's original source lines and omit the separate error encoding benchmark.
	projected := bytes.Replace(data[:end], []byte("\t\"bytes\"\n"), []byte("\n"), 1)
	projected = bytes.Replace(projected, []byte("\t\"strings\"\n"), []byte("\n"), 1)
	projected = append(projected, '\n', '\n')
	if fmt.Sprintf("%x", sha256.Sum256(projected)) != rpcNilContextGuardSHA {
		return "", errors.New("nil-context guard fixture does not match the verified projection")
	}
	output := filepath.Join(workspace, rpcNilContextGuardFixture)
	return output, os.WriteFile(output, projected, 0o600)
}

func projectRPCDeadlineFixture(path, workspace string) (string, error) {
	data, err := readRegularTestFixture(path)
	if err != nil {
		return "", err
	}
	positions := token.NewFileSet()
	file, err := parseBenchmarkFixture(positions, path, data)
	name := filepath.Base(path)
	source, retained := rpcDeadlineSources[name]
	if err != nil || !retained {
		return "", err
	}
	if file.Name.Name != "rpc" {
		return "", errors.New("deadline fixture requires package rpc")
	}
	start := []byte(source.start)
	if bytes.Count(data, start) != 1 {
		return "", errors.New("missing or repeated fixture start")
	}
	first := bytes.Index(data, start)
	last := rpcFixtureFunctionEnd(file, source.lastFunction, source.receiver, positions.File(file.Pos()))
	if last <= first {
		return "", errors.New("missing or repeated fixture end")
	}
	// Keep the exact test declarations and imports measured by the native isolation witness.
	projected := append([]byte(source.header), data[first:last]...)
	projected = append(projected, source.end...)
	if fmt.Sprintf("%x", sha256.Sum256(projected)) != source.sha {
		return "", errors.New("deadline fixture does not match the verified projection")
	}
	output := filepath.Join(workspace, name)
	return output, os.WriteFile(output, projected, 0o600)
}

func rpcFixtureFunctionEnd(file *ast.File, name, receiver string, positions *token.File) int {
	end := -1
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name || !rpcFixtureReceiverMatches(fn.Recv, receiver) {
			continue
		}
		if end != -1 {
			return -1
		}
		end = positions.Offset(fn.End())
	}
	return end
}

func rpcFixtureReceiverMatches(fields *ast.FieldList, receiver string) bool {
	if receiver == "" {
		return fields == nil
	}
	if fields == nil || len(fields.List) != 1 {
		return false
	}
	name, ok := fields.List[0].Type.(*ast.Ident)
	return ok && name.Name == receiver
}

func performanceOverlay(root, packagePath string, baseline bool) (string, func() error, error) {
	return performanceOverlayWithPathResolver(root, packagePath, baseline, filepath.EvalSymlinks)
}

func performanceOverlayWithPathResolver(root, packagePath string, baseline bool, resolveFixturePath func(string) (string, error)) (string, func() error, error) {
	if packagePath != "pkg/lux/rpc" {
		return "", func() error { return nil }, nil
	}
	physicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(physicalRoot, rpcBoundaryPath)
	info, err := os.Lstat(path)
	if err != nil {
		return "", nil, fmt.Errorf("reviewed RPC correctness fixture: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("reviewed RPC correctness fixture must be regular: %s", path)
	}
	resolved, err := resolveFixturePath(path)
	if err != nil {
		return "", nil, err
	}
	if resolved != path {
		return "", nil, fmt.Errorf("reviewed RPC correctness fixture must stay at %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	historicalDigest := ""
	if baseline {
		historicalDigest = rpcBoundarySHA
	}
	if err := validateRPCFixture(path, data, historicalDigest); err != nil {
		return "", nil, fmt.Errorf("reviewed RPC correctness fixture %s: %w", path, err)
	}
	return writePerformanceOverlay(filepath.Join(physicalRoot, ".tmp"), path)
}

func validateRPCFixture(path string, data []byte, historicalDigest string) error {
	line, _, _ := bytes.Cut(data, []byte("\n"))
	if string(bytes.TrimSuffix(line, []byte("\r"))) != rpcBoundaryMarker {
		if historicalDigest != "" && fmt.Sprintf("%x", sha256.Sum256(data)) == historicalDigest {
			return nil
		}
		return errors.New("missing marker or unrecognized historical baseline")
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, data, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return err
	}
	if file.Name.Name != "rpc" {
		return fmt.Errorf("unexpected package %s", file.Name.Name)
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			text := comment.Text
			if strings.HasPrefix(text, "//go:") || strings.HasPrefix(text, "//line ") ||
				strings.HasPrefix(text, "/*line ") || constraint.IsPlusBuild(text) {
				return fmt.Errorf("unsupported directive %s", text)
			}
		}
	}
	for _, entry := range file.Imports {
		path, err := strconv.Unquote(entry.Path.Value)
		if err != nil || !reviewedRPCFixtureImport(path) || entry.Name != nil && (entry.Name.Name == "_" || entry.Name.Name == ".") {
			return fmt.Errorf("unreviewed import %s", entry.Path.Value)
		}
	}
	return validateRPCFixtureDeclarations(file.Decls)
}

func validateRPCFixtureDeclarations(declarations []ast.Decl) error {
	var dispatch, stream bool
	for _, declaration := range declarations {
		switch d := declaration.(type) {
		case *ast.GenDecl:
			if d.Tok != token.IMPORT && d.Tok != token.TYPE {
				return fmt.Errorf("unsupported package declaration %s", d.Tok)
			}
		case *ast.FuncDecl:
			name := d.Name.Name
			if d.Recv != nil || d.Body == nil || name == "init" || name == "TestMain" || strings.HasPrefix(name, "Benchmark") {
				return fmt.Errorf("unsupported fixture function %s", name)
			}
			dispatch = dispatch || name == "TestInternalDispatchCannotBeForgedAcrossPublicTransports"
			stream = stream || name == "TestSchemaFreeRouterStreamBoundary"
		default:
			return fmt.Errorf("unsupported declaration %T", declaration)
		}
	}
	if !dispatch || !stream {
		return errors.New("required RPC correctness tests are missing")
	}
	return nil
}

// New imports can introduce initialization used by other tests or benchmarks.
func reviewedRPCFixtureImport(path string) bool {
	switch path {
	case "bytes", "context", "crypto/tls", "encoding/json", "errors", "io",
		"net/http", "net/http/httptest", "strconv", "strings", "sync/atomic", "testing", "time",
		"github.com/light-speak/luxo/pkg/lux/api", "github.com/light-speak/luxo/pkg/lux/codec",
		"github.com/light-speak/luxo/pkg/lux/errors", "github.com/light-speak/luxo/pkg/lux/h2transport",
		"github.com/light-speak/luxo/pkg/lux/schema", "golang.org/x/net/http2", "nhooyr.io/websocket":
		return true
	}
	return false
}

func writePerformanceOverlay(directory, fixture string) (string, func() error, error) {
	file, err := os.CreateTemp(directory, "bench-rpc-overlay-*.json")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() error {
		err := os.Remove(file.Name())
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	// This reviewed file has no benchmark, initializer, or TestMain. Keeping its
	// transport-only imports out of benchmark linkage preserves every RPC leaf.
	overlay := struct{ Replace map[string]string }{map[string]string{fixture: ""}}
	writeErr := json.NewEncoder(file).Encode(overlay)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return "", nil, errors.Join(err, cleanup())
	}
	return file.Name(), cleanup, nil
}
