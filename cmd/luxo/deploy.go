package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/light-speak/luxo/pkg/codegen"
	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy <target>",
	Short: "Generate deployment files / 生成部署文件",
	Long: `Generate deployment configuration files for the specified target.
为指定目标生成部署配置文件。

Targets / 目标:
  compose    Generate Dockerfile + docker-compose.yml
  helm       Generate Helm Chart in deploy/helm/

Example / 示例:
  luxo deploy compose
  luxo deploy helm`,
	Args:      cobra.ExactArgs(1),
	ValidArgs: []string{"compose", "helm"},
	RunE:      runDeploy,
}

func init() {
	rootCmd.AddCommand(deployCmd)
}

func runDeploy(cmd *cobra.Command, args []string) error {
	target := args[0]
	switch target {
	case "compose":
		return runDeployCompose()
	case "helm":
		ctx := context.Background()
		if cmd != nil && cmd.Context() != nil {
			ctx = cmd.Context()
		}
		return runDeployHelm(ctx)
	default:
		return fmt.Errorf("unknown deploy target: %s", target)
	}
}

func runDeployCompose() error {
	if err := loadGenerationEnvironment(".env"); err != nil {
		return err
	}
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

	modulePath, err := goModulePath()
	if err != nil {
		return err
	}
	projectName := modulePath
	if idx := strings.LastIndex(projectName, "/"); idx >= 0 {
		projectName = projectName[idx+1:]
	}
	if projectName == "" {
		projectName = "luxo"
	}

	deploy, err := codegen.GenerateDeployFilesChecked(result, projectName, generationBackends())
	if err != nil {
		return err
	}
	if deploy == nil {
		fmt.Println("No modules found / 未找到模块")
		return nil
	}

	green := "\033[32m"
	reset := "\033[0m"

	deployFiles := map[string][]byte{
		"Dockerfile":         deploy.Dockerfile,
		"docker-compose.yml": deploy.DockerCompose,
	}
	for name, data := range deployFiles {
		if err := os.WriteFile(name, data, 0755); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
		fmt.Printf("  %s+%s %s\n", green, reset, name)
	}

	fmt.Printf("\n%s✓ Deploy files generated%s\n", green, reset)
	fmt.Printf("  Run: docker-compose up -d\n\n")
	return nil
}

func runDeployHelm(ctx context.Context) error {
	if err := loadGenerationEnvironment(".env"); err != nil {
		return err
	}
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

	modulePath, err := goModulePath()
	if err != nil {
		return err
	}
	projectName := modulePath
	if idx := strings.LastIndex(projectName, "/"); idx >= 0 {
		projectName = projectName[idx+1:]
	}
	if projectName == "" {
		projectName = "luxo"
	}

	helm, err := codegen.GenerateHelmFilesChecked(result, projectName, generationBackends())
	if err != nil {
		return err
	}
	if helm == nil {
		fmt.Println("No modules found / 未找到模块")
		return nil
	}

	green := "\033[32m"
	reset := "\033[0m"

	paths, retired, err := publishHelmChart(ctx, helm.Files)
	if err != nil {
		return err
	}
	for _, path := range paths {
		fmt.Printf("  %s+%s %s/%s\n", green, reset, helmChartDirectory, filepath.ToSlash(path))
	}
	for _, path := range retired {
		fmt.Printf("  - %s/%s\n", helmChartDirectory, filepath.ToSlash(path))
	}

	fmt.Printf("\n%s✓ Helm chart generated%s\n", green, reset)
	fmt.Printf("  Run: helm install %s deploy/helm/\n\n", projectName)
	return nil
}
