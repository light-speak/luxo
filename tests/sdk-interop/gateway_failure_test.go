package main

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

//go:embed gateway_failure.go.txt
var fixtureGatewayFailureTests []byte

//go:embed gateway_bench.go.txt
var fixtureGatewayBenchmarks []byte

//go:embed gateway_nested.go.txt
var fixtureGatewayNestedTests []byte

func TestGeneratedGatewayExtensionFailures(t *testing.T) {
	t.Chdir("../..")
	err := withBuiltFixture(func(_ string, scratch string) error {
		if err := writeGatewayTestSources(scratch, map[string][]byte{"gateway_failure_test.go": fixtureGatewayFailureTests}); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, "go", "test", "-race", "-count=1", "-run", "^TestProxy", scratch)
		if os.Getenv("LUXO_PROXY_REPORT") == "1" {
			command.Args = append(command.Args[:len(command.Args)-1], "-coverprofile="+filepath.Join(scratch, "coverage.out"), "-shuffle=on", "-count=20", scratch)
		}
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		if err := command.Run(); err != nil {
			return err
		}
		if os.Getenv("LUXO_PROXY_REPORT") == "1" {
			return reportGatewayProxy(ctx, scratch)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func reportGatewayProxy(ctx context.Context, scratch string) error {
	output, err := exec.CommandContext(ctx, "go", "tool", "cover", "-func="+filepath.Join(scratch, "coverage.out")).Output()
	if err != nil {
		return err
	}
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		if bytes.Contains(line, []byte("/gateway_proxy.gen.go:")) {
			fmt.Println(string(line))
		}
	}
	command := exec.CommandContext(ctx, "gocyclo", "-over", "25", filepath.Join(scratch, "gateway_proxy.gen.go"))
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	return command.Run()
}

func TestGeneratedGatewayBenchmark(t *testing.T) {
	if os.Getenv("LUXO_PROXY_BENCHMARK") != "1" {
		t.Skip("opt-in isolated gateway benchmark")
	}
	t.Chdir("../..")
	err := withBuiltFixture(func(_ string, scratch string) error {
		baseline, err := gatewayValidationBenchmarkSource(scratch)
		if err != nil {
			return err
		}
		if err := writeGatewayTestSources(scratch, map[string][]byte{
			"gateway_failure_test.go":  fixtureGatewayFailureTests,
			"gateway_bench_test.go":    fixtureGatewayBenchmarks,
			"gateway_baseline_test.go": baseline,
		}); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, "go", "test", "-run", "^$", "-bench", "^BenchmarkProxy$", "-benchtime=200ms", "-count=3", scratch)
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		return command.Run()
	})
	if err != nil {
		t.Fatal(err)
	}
}

// This synthetic baseline changes only child validation, not the wire format,
// primary checks, RPC scheduling, cancellation or merge code.
func gatewayValidationBenchmarkSource(scratch string) ([]byte, error) {
	source, err := os.ReadFile(filepath.Join(scratch, "gateway_proxy.gen.go"))
	if err != nil {
		return nil, err
	}
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, "benchmark.go", source, 0)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string)
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			names[fn.Name.Name] = fn.Name.Name + "Benchmark"
		}
	}
	replacements := 0
	ast.Inspect(file, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok && names[id.Name] != "" {
			id.Name = names[id.Name]
		}
		if call, ok := node.(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "ValidateResolvedFields" {
				call.Fun = ast.NewIdent("benchmarkAcceptResolvedFields")
				replacements++
			}
		}
		return true
	})
	if replacements != 1 {
		return nil, fmt.Errorf("expected one child validation site, found %d", replacements)
	}
	var output bytes.Buffer
	if err := format.Node(&output, positions, file); err != nil {
		return nil, err
	}
	output.WriteString("\nfunc benchmarkAcceptResolvedFields(_ [][]byte, _ luvia.ExtendStep, _ *schema.Schema) bool { return true }\n")
	return output.Bytes(), nil
}

func TestGeneratedGatewayExtensionProtocol(t *testing.T) {
	runGeneratedGatewayNestedTests(t, "^TestGatewayNestedGeneratedProtocol$")
}

func TestGeneratedGatewayPrimaryKeys(t *testing.T) {
	runGeneratedGatewayNestedTests(t, "^TestGatewayPrimaryKeys")
}

func runGeneratedGatewayNestedTests(t *testing.T, pattern string) {
	t.Helper()
	t.Chdir("../..")
	err := withBuiltFixture(func(_ string, scratch string) error {
		if err := writeGatewayTestSources(scratch, map[string][]byte{
			"gateway_failure_test.go": fixtureGatewayFailureTests,
			"gateway_nested_test.go":  fixtureGatewayNestedTests,
		}); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, "go", "test", "-race", "-run", pattern, "-count=1", scratch)
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		return command.Run()
	})
	if err != nil {
		t.Fatal(err)
	}
}

func writeGatewayTestSources(scratch string, sources map[string][]byte) error {
	for name, source := range sources {
		if err := os.WriteFile(filepath.Join(scratch, name), source, 0600); err != nil {
			return err
		}
	}
	return nil
}
