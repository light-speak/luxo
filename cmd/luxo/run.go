package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the development server / 启动开发服务器",
	Long: `Start the Luxo development server with hot reload.
启动 Luxo 开发服务器，支持热重载。

Watches origin/*.luxo for changes, auto-regenerates, and restarts the server.
监听 origin/*.luxo 文件变更，自动重新生成代码并重启服务。

Example / 示例:
  luxo run`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("luxo run is not yet implemented / luxo run 尚未实现")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
}
