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

A programming language with a built-in API framework.
一门自带 API 框架的编程语言。

One language, one protocol, one toolchain — replaces REST, GraphQL, and gRPC.
一种语言、一套协议、一套工具链 — 取代 REST、GraphQL 和 gRPC。

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
