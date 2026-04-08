package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/light-speak/luxo/pkg/codegen"
	"github.com/spf13/cobra"
)

var addCmd = &cobra.Command{
	Use:   "add <module-name>",
	Short: "Add a service module / 添加服务模块",
	Long: `Add a new service module with schema, resolver, and entry point.
添加一个新的服务模块，包含 schema、resolver 和启动入口。

Generated files / 生成的文件:
  origin/<module>.luxo                    Schema definition / Schema 定义
  service/<module>/resolver/resolver.go   Resolver scaffold / Resolver 骨架
  luxis/<module>/main.go                  Service entry point / 服务入口

Example / 示例:
  luxo add user
  luxo add order
  luxo add payment`,
	Args: cobra.ExactArgs(1),
	RunE: runAdd,
}

func init() {
	rootCmd.AddCommand(addCmd)
}

func runAdd(cmd *cobra.Command, args []string) error {
	moduleName := args[0]

	// Must be inside a Luxo project (has go.mod)
	modulePath, err := readModulePath()
	if err != nil {
		return fmt.Errorf("not a Luxo project (no go.mod found)\n不是 Luxo 项目（未找到 go.mod）")
	}

	// Check if module already exists
	schemaPath := filepath.Join("origin", moduleName+".luxo")
	if _, err := os.Stat(schemaPath); err == nil {
		return fmt.Errorf("module %q already exists: %s\n模块 %q 已存在: %s", moduleName, schemaPath, moduleName, schemaPath)
	}

	sr := codegen.AddModule(modulePath, moduleName)

	green := "\033[32m"
	cyan := "\033[36m"
	dim := "\033[2m"
	bold := "\033[1m"
	reset := "\033[0m"

	for relPath, content := range sr.Files {
		if err := os.MkdirAll(filepath.Dir(relPath), 0755); err != nil {
			return fmt.Errorf("create directory for %s: %w", relPath, err)
		}
		if err := os.WriteFile(relPath, content, 0644); err != nil {
			return fmt.Errorf("write %s: %w", relPath, err)
		}
		fmt.Printf("  %s+%s %s\n", green, reset, relPath)
	}

	fmt.Printf("\n%s%s✓ Module %s added / 模块已添加%s\n\n", bold, green, moduleName, reset)
	fmt.Printf("  %sNext / 下一步:%s\n\n", dim, reset)
	fmt.Printf("    %s1.%s Define %sorigin/%s.luxo%s / 定义 origin/%s.luxo\n", cyan, reset, bold, moduleName, reset, moduleName)
	fmt.Printf("    %s2.%s Run  %s$%s luxo gen   %s# generate code / 生成代码%s\n", cyan, reset, cyan, reset, dim, reset)
	fmt.Println()

	return nil
}

// readModulePath reads the module path from go.mod in the current directory.
func readModulePath() (string, error) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return "", err
	}
	for _, line := range splitLines(data) {
		if len(line) > 7 && string(line[:7]) == "module " {
			return string(line[7:]), nil
		}
	}
	return "", fmt.Errorf("module directive not found in go.mod")
}

// splitLines splits bytes into lines.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
