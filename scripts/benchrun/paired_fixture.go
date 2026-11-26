package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"

	"github.com/light-speak/luxo/internal/benchlayout"
)

const (
	rpcPreparedFrameFilename      = "frame_prepared_bench_test.go"
	rpcPreparedFrameSourceSHA     = "bae1bf813d8341c8eb3483a01187871add87f5c26396d7256fdaac62cb7b583c"
	rpcPreparedFrameProjectionSHA = "8f4ca28038d34633c5ef5b4379d2621a51272237aaf249b9e4d769538db2053e"
	queueStreamNameFilename       = "nats_retry_bench_test.go"
	queueStreamNameSourceSHA      = "bc0a864510ecd60c11949f5b23df58d734dc285d256c1335716c0f063cd03c6e"
	queueStreamNameProjectionSHA  = "0f0a5195bb203151b16c1ec768d139153e822c29c7143da8193de9369ac8d8e6"

	codegenRelationLoaderFilename      = "dataloader_nullable_bench_test.go"
	codegenRelationLoaderSourceSHA     = "4f58c514dc0f2ac9c310a3142e961c15cbaffa47b2dc745192ea3b30be95729e"
	codegenRelationLoaderProjectionSHA = "9e1e181a3e49000a4543842e444ac9387b0a30503a524eb218cd7f651276957d"
	semanticEventEmitsFilename         = "analyzer_bench_test.go"
	semanticEventEmitsSourceSHA        = "ee8320cdca18a8700a37b504b8ee2f3ff05624c8e25a6ef35fe9ea8d4bad2a51"
	semanticEventEmitsProjectionSHA    = "94ef2558fb21f50051ac83b9fe1ca7baa7ec52456bfb16735879344b71d5d4ca"
)

type pairedBenchmarkFixture struct {
	name, sourceSHA, projectionSHA string
	imports, functions             []string
}

var rpcPreparedFrameFixture = pairedBenchmarkFixture{
	name: rpcPreparedFrameFilename, sourceSHA: rpcPreparedFrameSourceSHA, projectionSHA: rpcPreparedFrameProjectionSHA,
	imports:   []string{`"fmt"`, `"io"`, `"testing"`},
	functions: []string{"prepareUnframedReference", benchlayout.RPCPreparedFrameBenchmark},
}

var queueStreamNameFixture = pairedBenchmarkFixture{
	name: queueStreamNameFilename, sourceSHA: queueStreamNameSourceSHA, projectionSHA: queueStreamNameProjectionSHA,
	imports:   []string{`"testing"`},
	functions: []string{benchlayout.QueueStreamNameBenchmark, "legacyStreamNameForBenchmark"},
}

var codegenRelationLoaderFixture = pairedBenchmarkFixture{
	name: codegenRelationLoaderFilename, sourceSHA: codegenRelationLoaderSourceSHA, projectionSHA: codegenRelationLoaderProjectionSHA,
	imports:   []string{`"strconv"`, `"testing"`, `"github.com/light-speak/luxo/pkg/ast"`, `"github.com/light-speak/luxo/pkg/semantic"`},
	functions: []string{"relationBenchmarkInput", benchlayout.CodegenRelationLoaderBenchmark},
}

var semanticEventEmitsFixture = pairedBenchmarkFixture{
	name: semanticEventEmitsFilename, sourceSHA: semanticEventEmitsSourceSHA, projectionSHA: semanticEventEmitsProjectionSHA,
	imports:   []string{`"fmt"`, `"testing"`, `"github.com/light-speak/luxo/pkg/ast"`},
	functions: []string{benchlayout.SemanticEventEmitsBenchmark},
}

func pairedBenchmarkOverlay(root, pkg, profile string, fixture pairedBenchmarkFixture) (string, func() error, error) {
	return packageTestOverlay(root, pkg, "bench-"+profile+"-*", func(pkg, workspace string) (string, error) {
		return writeProjectedBenchmarkOverlay(pkg, workspace, profile, fixture.projectFile, fixture.name)
	})
}

func (fixture pairedBenchmarkFixture) projectFile(path, workspace string) (string, error) {
	source, err := readRegularTestFixture(path)
	if err != nil {
		return "", err
	}
	if filepath.Base(path) != fixture.name {
		_, err := parseBenchmarkFixture(token.NewFileSet(), path, source)
		return "", err
	}
	sourceSHA := sha256.Sum256(source)
	if hex.EncodeToString(sourceSHA[:]) != fixture.sourceSHA {
		return "", errors.New("fixture does not match the verified source")
	}
	projected, err := projectPairedBenchmark(path, source, fixture)
	if err != nil {
		return "", err
	}
	projectionSHA := sha256.Sum256(projected)
	if hex.EncodeToString(projectionSHA[:]) != fixture.projectionSHA {
		return "", errors.New("fixture does not match the verified projection")
	}
	output := filepath.Join(workspace, fixture.name)
	return output, os.WriteFile(output, projected, 0o600)
}

func projectPairedBenchmark(path string, source []byte, fixture pairedBenchmarkFixture) ([]byte, error) {
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, path, source, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	// Retain the source positions and the exact declarations measured on the native runner.
	projected := bytes.Clone(source)
	for i, ch := range projected {
		if ch != '\n' && ch != '\r' {
			projected[i] = ' '
		}
	}
	position := positions.File(file.Package)
	keep := func(first, last token.Pos) {
		start, end := position.Offset(first), position.Offset(last)
		copy(projected[start:end], source[start:end])
	}
	keep(file.Package, file.Name.End())
	keepPairedDeclarations(file, fixture, keep)
	return projected, nil
}

func keepPairedDeclarations(file *ast.File, fixture pairedBenchmarkFixture, keep func(token.Pos, token.Pos)) {
	for _, declaration := range file.Decls {
		switch node := declaration.(type) {
		case *ast.GenDecl:
			if node.Tok == token.IMPORT && node.Lparen.IsValid() {
				keep(node.Pos(), node.Lparen+1)
				keep(node.Rparen, node.End())
			}
		case *ast.FuncDecl:
			if node.Recv == nil && slices.Contains(fixture.functions, node.Name.Name) {
				first := node.Pos()
				if node.Doc != nil {
					first = node.Doc.Pos()
				}
				keep(first, node.End())
			}
		}
	}
	for _, item := range file.Imports {
		if item.Name == nil && slices.Contains(fixture.imports, item.Path.Value) {
			keep(item.Pos(), item.End())
		}
	}
}
