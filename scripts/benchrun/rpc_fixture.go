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
	"strconv"
	"strings"
)

const (
	rpcBoundaryPath   = "pkg/lux/rpc/public_boundary_test.go"
	rpcBoundaryMarker = "// luxo:benchmark-isolated-correctness"
	rpcBoundarySHA    = "99658ed681f4110df0db3fc688931fa61cd656f3c62477c04a1ffd4bb567ca8e"
	// Discovery and sampling must consume the same benchmark compilation profile.
	performanceProfile = "rpc-boundary-overlay-v2 " + rpcBoundarySHA + "\ncodec-reader-overlay-v1\napi-non-identity-overlay-v1\nluvia-primary-trace-overlay-v1"
)

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
