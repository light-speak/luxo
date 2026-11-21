// benchrun compiles and runs historical benchmark groups across module splits.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/light-speak/luxo/internal/benchlayout"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, input io.Reader, output, errorOutput io.Writer) error {
	if len(args) == 1 && args[0] == "canonical" {
		return canonical(input, output)
	}
	if len(args) < 4 || (args[0] != "compile" && args[0] != "run") {
		return errors.New("usage: benchrun canonical | {compile|run} revision binary-directory logical-package [test flags]")
	}
	root, err := filepath.Abs(args[1])
	if err != nil {
		return err
	}
	bin, err := filepath.Abs(args[2])
	if err != nil {
		return err
	}
	packages, err := benchlayout.Packages(root, args[3])
	if err != nil {
		return err
	}
	if err := os.MkdirAll(bin, 0755); err != nil {
		return err
	}
	for _, path := range packages {
		binary := filepath.Join(bin, strings.ReplaceAll(path, "/", "-")+".test")
		var cmd *exec.Cmd
		if args[0] == "compile" {
			cmd = exec.Command("go", "test", "-trimpath", "-c", "./"+path, "-o", binary)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(root, "go.work"))
		} else {
			cmd = exec.Command(binary, args[4:]...)
			cmd.Dir = filepath.Join(root, path)
		}
		cmd.Stdout, cmd.Stderr = output, errorOutput
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s %s: %w", args[0], path, err)
		}
	}
	return nil
}

func canonical(input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	seen := make(map[string]bool)
	for scanner.Scan() {
		seen[benchlayout.Canonical(strings.TrimSpace(scanner.Text()))] = true
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		if path != "" {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		if _, err := fmt.Fprintln(output, path); err != nil {
			return err
		}
	}
	return nil
}
