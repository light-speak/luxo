package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"

	"github.com/light-speak/luxo/internal/benchlayout"
)

const (
	staticValueFixtureName     = "request_value_bench_test.go"
	staticValueSourceSHA       = "2ea96ad21f5f589de2c05897c969c1e6c7d21a854654f84b3d27b5a9b337406b"
	staticValueProjectionSHA   = "e18ce1b22bd7037e8d987cd61f82310f52c9fa583e85b04752c5bea3c67486c3"
	staticRequestFixtureName   = "request_value_test.go"
	staticRequestSourceSHA     = "f083ebc56e1533c19409b3e8794b2c4318012cc586174f932b69680dad2b4ae5"
	staticRequestProjectionSHA = "5e977cca9dd55aff5c1c5a02a6a862c5b6c8ac21d275dd055c23cd893394f769"
	staticLegacyFixtureName    = "request_legacy_reference_test.go"
	staticLegacyFixtureSHA     = "7b9c4f7403501466b38d166e0c52b986c214a0c6e1ee3b26c6efe7a49d6251e5"

	rpcFrameReuseProjectionSHA = "c7407c7b91bd9a0f352f7059b64df3a97274f4904563c12f7fde6a3e9a683ad5"

	pgDurationFixtureName   = "duration_bind_bench_test.go"
	pgDurationSourceSHA     = "7031d417acd72838433daa1d1d19dcd920e602af5a84153521516e54b3fbb810"
	pgDurationProjectionSHA = "549c6791300094dbf71c0c220bbd6a1bd86b6d0c1e7c7b7da97b90506ae2b322"

	compactBenchmarkFingerprint = "\n" + benchlayout.APIStaticValueParamsProfile + "-overlay-v1 " +
		staticValueFixtureName + "=" + staticValueProjectionSHA + " " + staticValueSourceSHA + " " +
		staticRequestFixtureName + "=" + staticRequestProjectionSHA + " " + staticRequestSourceSHA + " " +
		staticLegacyFixtureName + "=" + staticLegacyFixtureSHA +
		"\n" + benchlayout.RPCFrameReuseProfile + "-overlay-v1 " +
		rpcPreparedFrameFilename + "=" + rpcFrameReuseProjectionSHA + " " + rpcPreparedFrameSourceSHA +
		"\n" + benchlayout.PGDurationWriteRejectProfile + "-overlay-v1 " +
		pgDurationFixtureName + "=" + pgDurationProjectionSHA + " " + pgDurationSourceSHA
)

var compactBenchmarkFixtures = map[string][]pairedBenchmarkFixture{
	benchlayout.APIStaticValueParamsProfile: {
		{name: staticValueFixtureName, sourceSHA: staticValueSourceSHA, projectionSHA: staticValueProjectionSHA,
			functions: []string{benchlayout.APIStaticValueParams, "benchmarkStaticList", "benchmarkStaticValue"}},
		{name: staticRequestFixtureName, sourceSHA: staticRequestSourceSHA, projectionSHA: staticRequestProjectionSHA,
			imports: []string{`"encoding/json"`}, functions: []string{"staticValueRequest"}},
		// The native build used the entire frozen previous reader without registering extra benchmarks.
		{name: staticLegacyFixtureName, sourceSHA: staticLegacyFixtureSHA, projectionSHA: staticLegacyFixtureSHA},
	},
	benchlayout.RPCFrameReuseProfile: {
		{name: rpcPreparedFrameFilename, sourceSHA: rpcPreparedFrameSourceSHA, projectionSHA: rpcFrameReuseProjectionSHA,
			functions: []string{"prepareReusedFrameCandidate", benchlayout.RPCFrameReuseBenchmark}},
	},
	benchlayout.PGDurationWriteRejectProfile: {
		{name: pgDurationFixtureName, sourceSHA: pgDurationSourceSHA, projectionSHA: pgDurationProjectionSHA,
			imports:   []string{`"errors"`, `"testing"`, `"time"`, `"github.com/shopspring/decimal"`},
			functions: []string{benchlayout.PGDurationWriteReject}},
	},
}

func compileCompactBenchmarkOverlay(root, pkg string, baseline bool, profile string) (string, func() error, error) {
	fixtures, isolated := compactBenchmarkFixtures[profile]
	if !isolated {
		return performanceOverlay(root, pkg, baseline)
	}
	return packageTestOverlay(root, pkg, "bench-"+profile+"-*", func(pkg, workspace string) (string, error) {
		names := make([]string, 0, len(fixtures))
		byName := make(map[string]pairedBenchmarkFixture, len(fixtures))
		for _, fixture := range fixtures {
			names = append(names, fixture.name)
			byName[fixture.name] = fixture
		}
		project := func(path, output string) (string, error) {
			return byName[filepath.Base(path)].writeProjection(path, output, projectCompactBenchmark)
		}
		return writeProjectedBenchmarkOverlay(pkg, workspace, profile, project, names...)
	})
}

func projectCompactBenchmark(path string, source []byte, fixture pairedBenchmarkFixture) ([]byte, error) {
	if len(fixture.functions) == 0 {
		return source, nil
	}
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, path, source, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	offsets := positions.File(file.Package)
	var out bytes.Buffer
	// Reproduce the verified native fixture's import and declaration bytes.
	fmt.Fprintf(&out, "package %s\n\nimport (\n", file.Name.Name)
	for _, item := range file.Imports {
		if fixture.imports == nil || slices.Contains(fixture.imports, item.Path.Value) {
			fmt.Fprintf(&out, "\t%s\n", source[offsets.Offset(item.Pos()):offsets.Offset(item.End())])
		}
	}
	out.WriteString(")\n")
	for _, node := range file.Decls {
		function, ok := node.(*ast.FuncDecl)
		if !ok || function.Recv != nil || !slices.Contains(fixture.functions, function.Name.Name) {
			continue
		}
		first := function.Pos()
		if function.Doc != nil {
			first = function.Doc.Pos()
		}
		fmt.Fprintf(&out, "\n%s\n", source[offsets.Offset(first):offsets.Offset(function.End())])
	}
	return out.Bytes(), nil
}
