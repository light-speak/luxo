package scripts_test

import (
	"strings"
	"testing"
)

func TestCompilerBenchmarksUseFrozenInput(t *testing.T) {
	for _, path := range []string{
		"../pkg/lexer/lexer_bench_test.go", "../pkg/parser/parser_bench_test.go",
		"../pkg/semantic/analyzer_bench_test.go", "../pkg/formatter/formatter_bench_test.go",
	} {
		source := readQualityFile(t, path)
		if !strings.Contains(source, "testutil.CompilerDemoV1()") {
			t.Errorf("%s must use the fixed compiler corpus", path)
		}
		if strings.Contains(source, "editors/vscode/examples/demo.luxo") {
			t.Errorf("%s must not benchmark changing documentation", path)
		}
		if strings.Contains(source, "b.Skip") {
			t.Errorf("%s must not silently skip missing benchmark inputs", path)
		}
	}
}
