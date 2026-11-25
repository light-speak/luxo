package main

import (
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

const (
	luviaGatewayIdentityFixture       = "middleware_test.go"
	luviaGatewayIdentityProjectionSHA = "ae790127f4acd6df4fe2388d239b2657c999add2e742584284c411bef514e365"
)

var luviaGatewayIdentityDeclarations = []string{"testCfg", "testClaimsJSON", "testIdentity", benchlayout.LuviaGatewayIdentityBenchmark}

func writeLuviaGatewayIdentityOverlay(pkg, workspace string) (string, error) {
	entries, err := os.ReadDir(pkg)
	if err != nil {
		return "", err
	}
	replacements := make(map[string]string)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(pkg, name)
		replacement, err := projectLuviaGatewayIdentityFixture(path, workspace)
		if err != nil {
			return "", fmt.Errorf("Luvia gateway identity fixture %s: %w", name, err)
		}
		replacements[path] = replacement
	}
	if replacements[filepath.Join(pkg, luviaGatewayIdentityFixture)] == "" {
		return "", errors.New("missing Luvia gateway identity benchmark fixture")
	}
	return writeTestOverlay(workspace, replacements)
}

func projectLuviaGatewayIdentityFixture(path, workspace string) (string, error) {
	data, err := readRegularTestFixture(path)
	if err != nil {
		return "", err
	}
	positions := token.NewFileSet()
	file, err := parseBenchmarkFixture(positions, path, data)
	if err != nil || filepath.Base(path) != luviaGatewayIdentityFixture {
		return "", err
	}
	if file.Name.Name != "luvia" {
		return "", fmt.Errorf("unexpected package %q", file.Name.Name)
	}
	projected, err := luviaGatewayIdentitySource(data, positions.File(file.Pos()), file.Decls)
	if err != nil {
		return "", err
	}
	path = filepath.Join(workspace, luviaGatewayIdentityFixture)
	return path, os.WriteFile(path, projected, 0o600)
}

func luviaGatewayIdentitySource(data []byte, positions *token.File, declarations []ast.Decl) ([]byte, error) {
	retained := make(map[string][]byte)
	for _, decl := range declarations {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !slices.Contains(luviaGatewayIdentityDeclarations, fn.Name.Name) {
			continue
		}
		if retained[fn.Name.Name] != nil {
			return nil, fmt.Errorf("duplicate %s declaration", fn.Name.Name)
		}
		retained[fn.Name.Name] = data[positions.Offset(fn.Pos()):positions.Offset(fn.End())]
	}
	const header = "package luvia\n\nimport (\n\t\"context\"\n\t\"testing\"\n\t\"time\"\n\n\t\"github.com/light-speak/luxo/pkg/lux/auth\"\n)\n"
	projected := []byte(header)
	for _, name := range luviaGatewayIdentityDeclarations {
		body := retained[name]
		if body == nil {
			return nil, fmt.Errorf("missing %s declaration", name)
		}
		projected = append(projected, '\n')
		projected = append(projected, body...)
		projected = append(projected, '\n')
	}
	if fmt.Sprintf("%x", sha256.Sum256(projected)) != luviaGatewayIdentityProjectionSHA {
		return nil, errors.New("gateway identity declarations do not match the verified projection")
	}
	return projected, nil
}
