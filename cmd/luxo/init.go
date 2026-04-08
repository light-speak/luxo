package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/light-speak/luxo/pkg/codegen"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init <project-name>",
	Short: "Create a new Luxo project / 创建新项目",
	Long: `Create a new Luxo project with the standard directory structure.
创建一个标准目录结构的 Luxo 项目。

Generated files / 生成的文件:
  origin/         Schema directory / Schema 目录
  .env.example    Environment config template / 环境配置模板
  .gitignore      Git ignore rules / Git 忽略规则
  go.mod          Go module file / Go 模块文件

Next steps / 下一步:
  luxo add <module>    Add a service module / 添加服务模块
  luxo gen             Generate Go code / 生成 Go 代码

Example / 示例:
  luxo init my-app
  cd my-app
  luxo add user
  luxo gen`,
	Args: cobra.ExactArgs(1),
	RunE: runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	projectName := args[0]
	projectDir, err := filepath.Abs(projectName)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	// Check if directory already exists and is not empty
	if entries, err := os.ReadDir(projectDir); err == nil && len(entries) > 0 {
		return fmt.Errorf("directory %s already exists and is not empty\n目录 %s 已存在且不为空", projectName, projectName)
	}

	// Use project name as module path (user can change go.mod later)
	modulePath := projectName

	sr := codegen.InitProject(modulePath)

	// Create project directory and origin/
	if err := os.MkdirAll(filepath.Join(projectDir, "origin"), 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	// Write files
	for relPath, content := range sr.Files {
		fullPath := filepath.Join(projectDir, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return fmt.Errorf("create directory for %s: %w", relPath, err)
		}
		if err := os.WriteFile(fullPath, content, 0644); err != nil {
			return fmt.Errorf("write %s: %w", relPath, err)
		}
	}

	green := "\033[32m"
	cyan := "\033[36m"
	dim := "\033[2m"
	bold := "\033[1m"
	reset := "\033[0m"

	fmt.Printf("\n%s%s✓ Project %s created / 项目已创建%s\n\n", bold, green, projectName, reset)
	fmt.Printf("  %sGet started / 开始使用:%s\n\n", dim, reset)
	fmt.Printf("    %s$%s cd %s\n", cyan, reset, projectName)
	fmt.Printf("    %s$%s luxo add user          %s# add a module / 添加模块%s\n", cyan, reset, dim, reset)
	fmt.Printf("    %s$%s luxo gen               %s# generate code / 生成代码%s\n", cyan, reset, dim, reset)
	fmt.Printf("    %s$%s cp .env.example .env   %s# configure env / 配置环境%s\n", cyan, reset, dim, reset)
	fmt.Printf("    %s$%s luxo run               %s# start server / 启动服务%s\n", cyan, reset, dim, reset)
	fmt.Println()

	return nil
}
