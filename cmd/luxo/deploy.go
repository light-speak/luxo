package main

import (
	"fmt"
	"os"
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
  compose    Generate docker-compose.yml + Dockerfile + init-db.sh
  helm       Generate Helm Charts + helmfile.yaml (coming soon)

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
		return runDeployHelm()
	default:
		return fmt.Errorf("unknown deploy target: %s", target)
	}
}

func runDeployCompose() error {
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

	modulePath, _ := readModulePath()
	projectName := modulePath
	if idx := strings.LastIndex(projectName, "/"); idx >= 0 {
		projectName = projectName[idx+1:]
	}
	if projectName == "" {
		projectName = "luxo"
	}

	deploy := codegen.GenerateDeployFiles(result, projectName)
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

func runDeployHelm() error {
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

	modulePath, _ := readModulePath()
	projectName := modulePath
	if idx := strings.LastIndex(projectName, "/"); idx >= 0 {
		projectName = projectName[idx+1:]
	}
	if projectName == "" {
		projectName = "luxo"
	}

	helm := codegen.GenerateHelmFiles(result, projectName)
	if helm == nil {
		fmt.Println("No modules found / 未找到模块")
		return nil
	}

	green := "\033[32m"
	reset := "\033[0m"

	for path, data := range helm.Files {
		fullPath := "deploy/helm/" + path
		// Ensure parent directory exists
		if dir := fullPath[:strings.LastIndex(fullPath, "/")]; dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("mkdir %s: %w", dir, err)
			}
		}
		if err := os.WriteFile(fullPath, data, 0644); err != nil {
			return fmt.Errorf("write %s: %w", fullPath, err)
		}
		fmt.Printf("  %s+%s %s\n", green, reset, fullPath)
	}

	fmt.Printf("\n%s✓ Helm chart generated%s\n", green, reset)
	fmt.Printf("  Run: helm install %s deploy/helm/\n\n", projectName)
	return nil
}
