package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/codegen"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/spf13/cobra"
)

var genCmd = &cobra.Command{
	Use:   "gen",
	Short: "Generate Go code from .luxo schemas / 从 .luxo 生成 Go 代码",
	Long: `Parse all .luxo files in origin/ and generate Go code for each service module.
解析 origin/ 下的所有 .luxo 文件，为每个服务模块生成 Go 代码。

Generated files (*.gen.go) are written to service/<module>/luxo/.
生成的文件 (*.gen.go) 写入 service/<module>/luxo/。

These files are fully overwritten on each run — do not edit them.
这些文件每次运行时完全覆盖 — 请勿编辑。

Example / 示例:
  luxo gen`,
	Args: cobra.NoArgs,
	RunE: runGen,
}

func init() {
	rootCmd.AddCommand(genCmd)
}

func runGen(cmd *cobra.Command, args []string) error {
	// Find all .luxo files
	entries, err := os.ReadDir("origin")
	if err != nil {
		return fmt.Errorf("read origin/: %w\n读取 origin/ 失败: %w", err, err)
	}

	var schemaFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".luxo") {
			schemaFiles = append(schemaFiles, filepath.Join("origin", e.Name()))
		}
	}

	if len(schemaFiles) == 0 {
		return fmt.Errorf("no .luxo files found in origin/\norigin/ 下没有 .luxo 文件")
	}

	// Parse all files
	var files []*ast.File
	for _, path := range schemaFiles {
		file, err := parseFile(path)
		if err != nil {
			return err
		}
		files = append(files, file)
	}

	// Semantic analysis
	a := semantic.New()
	result := a.Analyze(files)

	if len(result.Errors) > 0 {
		for _, e := range result.Errors {
			fmt.Fprintf(os.Stderr, "error: %s:%d: %s\n", e.Pos.File, e.Pos.Line, e.Message)
		}
		return fmt.Errorf("%d error(s) / %d 个错误", len(result.Errors), len(result.Errors))
	}

	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s:%d: %s\n", w.Pos.File, w.Pos.Line, w.Message)
	}

	// Generate code per module
	totalFiles := 0
	for _, file := range files {
		moduleName := strings.TrimSuffix(filepath.Base(file.Name), ".luxo")
		outDir := filepath.Join("service", moduleName, "luxo")

		if err := os.MkdirAll(outDir, 0755); err != nil {
			return fmt.Errorf("create %s: %w", outDir, err)
		}

		singleResult := &semantic.Result{Files: []*ast.File{file}}
		gr := codegen.Generate(singleResult, "luxo")

		for name, src := range gr.Files {
			outPath := filepath.Join(outDir, name)
			if err := os.WriteFile(outPath, src, 0644); err != nil {
				return fmt.Errorf("write %s: %w", outPath, err)
			}
			fmt.Printf("  generated %s\n", outPath)
			totalFiles++
		}
	}

	fmt.Printf("\n%d file(s) generated from %d schema(s)\n", totalFiles, len(schemaFiles))
	fmt.Printf("从 %d 个 schema 生成了 %d 个文件\n", len(schemaFiles), totalFiles)
	return nil
}

func parseFile(path string) (*ast.File, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	l := lexer.New(string(content), path)
	tokens, lexErrs := l.Tokenize()
	if len(lexErrs) > 0 {
		for _, e := range lexErrs {
			fmt.Fprintf(os.Stderr, "error: %s\n", e)
		}
		return nil, fmt.Errorf("%d lexer error(s) in %s", len(lexErrs), path)
	}

	p := parser.New(tokens)
	file, parseErrs := p.Parse(path)
	if len(parseErrs) > 0 {
		for _, e := range parseErrs {
			fmt.Fprintf(os.Stderr, "error: %s\n", e)
		}
		return nil, fmt.Errorf("%d parser error(s) in %s", len(parseErrs), path)
	}

	return file, nil
}
