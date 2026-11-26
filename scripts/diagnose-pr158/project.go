// Project the exact #158 fixture declarations for a diagnostic-only compile.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
)

type fixture struct {
	file, sha      string
	imports, decls []string
}

var families = map[string][]fixture{
	"api": {
		{"request_value_bench_test.go", "2ea96ad21f5f589de2c05897c969c1e6c7d21a854654f84b3d27b5a9b337406b", nil,
			[]string{"BenchmarkStaticValueParams", "benchmarkStaticList", "benchmarkStaticValue"}},
		{"request_value_test.go", "f083ebc56e1533c19409b3e8794b2c4318012cc586174f932b69680dad2b4ae5", []string{"encoding/json"},
			[]string{"staticValueRequest"}},
		{"request_legacy_reference_test.go", "7b9c4f7403501466b38d166e0c52b986c214a0c6e1ee3b26c6efe7a49d6251e5", nil, nil},
	},
	"rpc": {
		{"frame_prepared_bench_test.go", "bae1bf813d8341c8eb3483a01187871add87f5c26396d7256fdaac62cb7b583c", nil,
			[]string{"prepareReusedFrameCandidate", "BenchmarkLRPCConnectionFrameReuseCandidate"}},
	},
	"pg": {
		{"duration_bind_bench_test.go", "7031d417acd72838433daa1d1d19dcd920e602af5a84153521516e54b3fbb810",
			[]string{"errors", "testing", "time", "github.com/shopspring/decimal"}, []string{"BenchmarkDurationWriteReject"}},
	},
}

func expect(ok bool, message string, args ...any) {
	if !ok {
		panic(fmt.Sprintf(message, args...))
	}
}

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func projectedSource(dir, pkg string, f fixture) []byte {
	input := must(os.ReadFile(filepath.Join(dir, f.file)))
	expect(fmt.Sprintf("%x", sha256.Sum256(input)) == f.sha, "fixture source differs: %s", f.file)
	if f.decls == nil {
		// This frozen companion defines the complete previous reader without registering tests.
		return input
	}
	pos := token.NewFileSet()
	parsed := must(parser.ParseFile(pos, f.file, input, parser.ParseComments))
	expect(parsed.Name.Name == pkg, "wrong package in %s", f.file)

	var out bytes.Buffer
	fmt.Fprintf(&out, "package %s\n\nimport (\n", pkg)
	imports := 0
	for _, spec := range parsed.Imports {
		path := must(strconv.Unquote(spec.Path.Value))
		if f.imports == nil || slices.Contains(f.imports, path) {
			fmt.Fprintf(&out, "\t%s\n", input[pos.Position(spec.Pos()).Offset:pos.Position(spec.End()).Offset])
			imports++
		}
	}
	expect(f.imports == nil || len(f.imports) == imports, "missing original import in %s", f.file)
	out.WriteString(")\n")
	var decls []string
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !slices.Contains(f.decls, fn.Name.Name) {
			continue
		}
		expect(fn.Recv == nil, "unexpected method in %s", f.file)
		start := fn.Pos()
		if fn.Doc != nil {
			start = fn.Doc.Pos()
		}
		fmt.Fprintf(&out, "\n%s\n", input[pos.Position(start).Offset:pos.Position(fn.End()).Offset])
		decls = append(decls, fn.Name.Name)
	}
	expect(slices.Equal(decls, f.decls), "benchmark closure differs in %s", f.file)
	return out.Bytes()
}

type overlay struct{ Replace map[string]string }

func originalOverlay(old, dir, pkg string) overlay {
	result := overlay{Replace: map[string]string{}}
	if old != "-" {
		check(json.Unmarshal(must(os.ReadFile(old)), &result))
	}
	switch pkg {
	case "api":
		files := []string{
			"identity_reader_bench_test.go", "identity_reader_fuzz_test.go",
			"identity_reader_test.go", "identity_value_test.go",
		}
		expect(len(result.Replace) == len(files), "expected exact api-non-identity removals")
		for _, file := range files {
			newFile, ok := result.Replace[filepath.Join(dir, file)]
			expect(ok && newFile == "", "unexpected stock API fixture replacement: %s", file)
		}
	case "rpc":
		replacement, exists := result.Replace[filepath.Join(dir, "public_boundary_test.go")]
		expect(len(result.Replace) == 1 && exists && replacement == "", "expected exact stock RPC boundary removal")
	default:
		expect(len(result.Replace) == 0, "expected unoverlaid stock benchmark for %s", pkg)
	}
	return result
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	defer func() {
		if failure := recover(); failure != nil {
			fmt.Fprintln(os.Stderr, failure)
			os.Exit(1)
		}
	}()
	expect(len(os.Args) == 5, "usage: project SOURCE api|rpc|pg STOCK_OVERLAY_OR_DASH NEW_DIR")
	source := must(filepath.Abs(os.Args[1]))
	pkg := os.Args[2]
	out := must(filepath.Abs(os.Args[4]))
	files := families[pkg]
	expect(len(files) > 0, "unknown family %s", pkg)
	dir := filepath.Join(source, "pkg", "lux", pkg)
	result := originalOverlay(os.Args[3], dir, pkg)
	for _, entry := range must(os.ReadDir(dir)) {
		if entry.IsDir() || !bytes.HasSuffix([]byte(entry.Name()), []byte("_test.go")) {
			continue
		}
		expect(entry.Type().IsRegular(), "non-regular test input %s", entry.Name())
		result.Replace[filepath.Join(dir, entry.Name())] = ""
	}
	projected := make([][]byte, len(files))
	for i, f := range files {
		projected[i] = projectedSource(dir, pkg, f)
	}
	check(os.Mkdir(out, 0o700))
	for i, f := range files {
		replacement := filepath.Join(out, f.file)
		check(os.WriteFile(replacement, projected[i], 0o600))
		result.Replace[filepath.Join(dir, f.file)] = replacement
	}
	overlayPath := filepath.Join(out, "overlay.json")
	data := must(json.MarshalIndent(result, "", "  "))
	check(os.WriteFile(overlayPath, append(data, '\n'), 0o600))
	fmt.Println(overlayPath)
}
