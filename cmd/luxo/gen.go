package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/codegen"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/lockfile"
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
	schemaFiles, err := findSchemaFiles()
	if err != nil {
		return err
	}

	files, err := parseAllFiles(schemaFiles)
	if err != nil {
		return err
	}

	result, err := analyzeFiles(files)
	if err != nil {
		return err
	}

	if err := updateLockFile(files); err != nil {
		return err
	}

	totalFiles, err := generateModules(files, result)
	if err != nil {
		return err
	}

	// Generate embedded entry point: luxis/app/main.gen.go
	modulePath, _ := readModulePath()
	if modulePath != "" {
		if err := generateEntry(result, modulePath); err != nil {
			return err
		}
		totalFiles++
	}

	green := "\033[32m"
	dim := "\033[2m"
	bold := "\033[1m"
	reset := "\033[0m"
	fmt.Printf("\n%s%s✓ %d file(s) generated from %d schema(s)%s\n", bold, green, totalFiles, len(schemaFiles), reset)
	fmt.Printf("  %s%d 个 schema 生成了 %d 个文件%s\n\n", dim, len(schemaFiles), totalFiles, reset)
	return nil
}

func findSchemaFiles() ([]string, error) {
	entries, err := os.ReadDir("origin")
	if err != nil {
		return nil, fmt.Errorf("read origin/: %w\n读取 origin/ 失败: %w", err, err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".luxo") {
			files = append(files, filepath.Join("origin", e.Name()))
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .luxo files found in origin/\norigin/ 下没有 .luxo 文件")
	}
	return files, nil
}

func parseAllFiles(paths []string) ([]*ast.File, error) {
	var files []*ast.File
	for _, path := range paths {
		file, err := parseFile(path)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func analyzeFiles(files []*ast.File) (*semantic.Result, error) {
	a := semantic.New()
	result := a.Analyze(files)
	if len(result.Errors) > 0 {
		for _, e := range result.Errors {
			fmt.Fprintf(os.Stderr, "error: %s:%d: %s\n", e.Pos.File, e.Pos.Line, e.Message)
		}
		return nil, fmt.Errorf("%d error(s) / %d 个错误", len(result.Errors), len(result.Errors))
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s:%d: %s\n", w.Pos.File, w.Pos.Line, w.Message)
	}
	return result, nil
}

func updateLockFile(files []*ast.File) error {
	green := "\033[32m"
	reset := "\033[0m"
	lf, err := lockfile.Load("luxo.lock")
	if err != nil {
		return fmt.Errorf("load luxo.lock: %w\n加载 luxo.lock 失败: %w", err, err)
	}
	lf.Update(files)
	if err := lf.Save("luxo.lock"); err != nil {
		return fmt.Errorf("save luxo.lock: %w\n保存 luxo.lock 失败: %w", err, err)
	}
	fmt.Printf("  %s✓%s luxo.lock\n", green, reset)
	return nil
}

func generateModules(files []*ast.File, result *semantic.Result) (int, error) {
	green := "\033[32m"
	reset := "\033[0m"
	total := 0
	for _, file := range files {
		moduleName := strings.TrimSuffix(filepath.Base(file.Name), ".luxo")
		outDir := filepath.Join("service", moduleName, "luxo")
		if err := os.MkdirAll(outDir, 0755); err != nil {
			return 0, fmt.Errorf("create %s: %w", outDir, err)
		}
		singleResult := &semantic.Result{Files: []*ast.File{file}}
		gr := codegen.Generate(singleResult, "luxo")
		for name, src := range gr.Files {
			outPath := filepath.Join(outDir, name)
			if err := os.WriteFile(outPath, src, 0644); err != nil {
				return 0, fmt.Errorf("write %s: %w", outPath, err)
			}
			fmt.Printf("  %s+%s %s\n", green, reset, outPath)
			total++
		}
	}
	return total, nil
}

func generateEntry(result *semantic.Result, modulePath string) error {
	green := "\033[32m"
	reset := "\033[0m"
	src := codegen.GenerateEntryFile(result, modulePath)
	if src == nil {
		return nil
	}
	outDir := filepath.Join("luxis", "app")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("create %s: %w", outDir, err)
	}
	outPath := filepath.Join(outDir, "main.gen.go")
	if err := os.WriteFile(outPath, src, 0644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	fmt.Printf("  %s+%s %s\n", green, reset, outPath)
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
