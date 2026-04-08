package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Build and start the service / 编译并启动服务",
	Long: `Build and run the embedded entry point (luxis/app/).
编译并运行 embedded 入口（luxis/app/）。

Run 'luxo gen' first to generate luxis/app/main.gen.go.
请先运行 'luxo gen' 生成 luxis/app/main.gen.go。

Example / 示例:
  luxo gen && luxo run`,
	Args: cobra.NoArgs,
	RunE: runServer,
}

func init() {
	rootCmd.AddCommand(runCmd)
}

func runServer(cmd *cobra.Command, args []string) error {
	entryDir := filepath.Join("luxis", "app")

	// Check entry point exists
	if _, err := os.Stat(filepath.Join(entryDir, "main.gen.go")); err != nil {
		return fmt.Errorf("luxis/app/main.gen.go not found — run 'luxo gen' first\n未找到 luxis/app/main.gen.go — 请先运行 'luxo gen'")
	}

	// go run ./luxis/app/
	goCmd := exec.Command("go", "run", "./"+entryDir+"/")
	goCmd.Stdout = os.Stdout
	goCmd.Stderr = os.Stderr
	goCmd.Stdin = os.Stdin

	return goCmd.Run()
}
