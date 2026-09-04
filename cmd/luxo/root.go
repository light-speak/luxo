package main

import (
	"github.com/spf13/cobra"
)

// Version is set at build time via ldflags.
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:   "luxo",
	Short: "Build APIs at the speed of light / 光速构建 API",
	Long: `Luxo — Build APIs at the speed of light.
Luxo — 光速构建 API。

A schema-first compiled backend language and platform.
一门 Schema-first 的编译型后端语言与平台。

One language, one protocol, one toolchain for API and data services.
一种语言、一套协议、一套面向 API 与数据服务的工具链。

Commands / 命令:
  init <project>     Create a new project / 创建新项目
  add <module>       Add a service module / 添加服务模块
  gen                Generate Go code / 生成 Go 代码
  run                Start server / 启动服务
  deploy <target>    Generate deploy files / 生成部署文件`,
	SilenceUsage:  true,
	SilenceErrors: true,
	Version:       Version,
}

func init() {
	rootCmd.SetVersionTemplate("luxo version {{.Version}}\n")
}
