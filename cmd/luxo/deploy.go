package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy <target>",
	Short: "Generate deployment files / 生成部署文件",
	Long: `Generate deployment configuration files for the specified target.
为指定目标生成部署配置文件。

Targets / 目标:
  compose    Generate docker-compose.yml / 生成 docker-compose.yml
  helm       Generate Helm Charts + helmfile.yaml / 生成 Helm Charts + helmfile.yaml

Example / 示例:
  luxo deploy compose
  luxo deploy helm`,
	Args:      cobra.ExactArgs(1),
	ValidArgs: []string{"compose", "helm"},
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("luxo deploy %s is not yet implemented / luxo deploy %s 尚未实现\n", args[0], args[0])
		return nil
	},
}

func init() {
	rootCmd.AddCommand(deployCmd)
}
