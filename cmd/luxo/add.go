package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/scanner"
	"unicode/utf8"

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

The lucis/<module>/main.go entry point is produced by 'luxo gen' (main.gen.go).
lucis/<module>/main.go 入口由 'luxo gen' 生成 (main.gen.go)。

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
	if moduleName == "." || !filepath.IsLocal(moduleName) || strings.ContainsAny(moduleName, "/\\\x00\r\n") || !utf8.ValidString(moduleName) {
		return fmt.Errorf("invalid module name %q: expected one non-empty UTF-8 path component without line breaks / 模块名必须是非空UTF-8路径片段且不能换行", moduleName)
	}
	if !validScaffoldModuleName(moduleName) {
		return fmt.Errorf("invalid module name %q: use ASCII letters or underscores, with digits after the first character, and avoid the lone underscore / 模块名须以 ASCII 字母或下划线开头，后续可含数字，且不能仅为下划线", moduleName)
	}

	switch moduleName {
	case "internal", "vendor":
		return fmt.Errorf("invalid module name %q: reserved Go import path component / 模块名 %q 是 Go 保留的导入路径片段", moduleName, moduleName)
	}
	if err := codegen.ValidateModuleScaffoldModelName(moduleName); err != nil {
		return fmt.Errorf("invalid module name %q / 无效的模块名 %q: %w", moduleName, moduleName, err)
	}

	// Must be inside a Luxo project (has go.mod)
	modulePath, err := readModulePath()
	if err != nil {
		return fmt.Errorf("not a Luxo project (no go.mod found)\n不是 Luxo 项目（未找到 go.mod）")
	}

	paths, err := writeModuleScaffold(modulePath, moduleName)
	if err != nil {
		return err
	}
	printAddedModule(moduleName, paths)
	return nil
}

// The name is emitted in both a Go import path and a Luxo model identifier.
func validScaffoldModuleName(name string) bool {
	if name == "" || name == "_" {
		return false
	}
	for index := range len(name) {
		ch := name[index]
		switch {
		case ch == '_', ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', index > 0 && ch >= '0' && ch <= '9':
		default:
			return false
		}
	}
	return true
}

func writeModuleScaffold(modulePath, moduleName string) ([]string, error) {
	root, err := os.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	defer root.Close()
	// Check if module already exists
	if err := checkModuleOutput(root, moduleName, filepath.Join("origin", moduleName)); err != nil {
		return nil, err
	}
	schemaPath := filepath.Join("origin", moduleName+".luxo")
	if err := checkModuleOutput(root, moduleName, schemaPath); err != nil {
		return nil, err
	}
	files := codegen.AddModule(modulePath, moduleName).Files
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if path != schemaPath {
			if err := checkModuleOutput(root, moduleName, path); err != nil {
				return nil, err
			}
		}
	}
	for index, path := range paths {
		if err := writeNewModuleFile(root, path, files[path]); err != nil {
			return nil, errors.Join(err, removeModuleFiles(root, paths[:index]))
		}
	}
	return paths, nil
}

func checkModuleOutput(root *os.Root, moduleName, path string) error {
	if _, err := root.Lstat(path); err == nil {
		return fmt.Errorf("module %q already exists: %s: %w / 模块已存在", moduleName, path, os.ErrExist)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func writeNewModuleFile(root *os.Root, path string, content []byte) error {
	return writeNewProjectFile(root, path, content, 0644)
}

func writeNewProjectFile(root *os.Root, path string, content []byte, mode os.FileMode) error {
	if err := root.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}
	file, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := writeProjectFileContent(file, content); err != nil {
		return errors.Join(fmt.Errorf("write %s: %w", path, err), removeModuleFiles(root, []string{path}))
	}
	return nil
}

func writeProjectFileContent(file *os.File, content []byte) error {
	_, err := file.Write(content)
	return errors.Join(err, file.Close())
}

func removeModuleFiles(root *os.Root, paths []string) error {
	var result error
	for _, path := range paths {
		if err := root.Remove(path); err != nil {
			result = errors.Join(result, fmt.Errorf("remove incomplete scaffold %s: %w", path, err))
		}
	}
	return result
}

func printAddedModule(moduleName string, paths []string) {

	green := "\033[32m"
	cyan := "\033[36m"
	dim := "\033[2m"
	bold := "\033[1m"
	reset := "\033[0m"

	for _, path := range paths {
		fmt.Printf("  %s+%s %s\n", green, reset, path)
	}

	fmt.Printf("\n%s%s✓ Module %s added / 模块已添加%s\n\n", bold, green, moduleName, reset)
	fmt.Printf("  %sNext / 下一步:%s\n\n", dim, reset)
	fmt.Printf("    %s1.%s Define %sorigin/%s.luxo%s / 定义 origin/%s.luxo\n", cyan, reset, bold, moduleName, reset, moduleName)
	fmt.Printf("    %s2.%s Run  %s$%s luxo gen   %s# generate code / 生成代码%s\n", cyan, reset, cyan, reset, dim, reset)
	fmt.Println()
}

// readModulePath reads the module path from go.mod in the current directory.
func readModulePath() (string, error) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return "", err
	}
	return parseModulePath(data)
}

// Read only top-level module directives, preserving the surrounding mod grammar.
func parseModulePath(data []byte) (string, error) {
	if path, ok := fastModulePath(data); ok {
		return path, nil
	}
	return scanModulePath(data)
}

// Keep the fast subset narrower than the canonical scanner grammar.
func fastModulePath(data []byte) (string, bool) {
	const prefix = "module "
	if len(data) <= len(prefix) || !bytes.HasPrefix(data, []byte(prefix)) {
		return "", false
	}
	for end := len(prefix); end < len(data); end++ {
		ch := data[end]
		if ch == '\n' {
			// Scan consumes one following rune before returning the newline token.
			if end == len(prefix) || end+1 < len(data) && (data[end+1] == 0 || data[end+1] >= utf8.RuneSelf) {
				return "", false
			}
			return string(data[len(prefix):end]), true
		}
		safe := ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '_' || ch == '-' || ch == '/'
		if !safe || ch == '/' && end+1 < len(data) && data[end+1] == '/' {
			return "", false
		}
	}
	return string(data[len(prefix):]), true
}

func scanModulePath(data []byte) (string, error) {
	var lex scanner.Scanner
	lex.Init(bytes.NewReader(data))
	lex.Mode = scanner.ScanIdents | scanner.ScanStrings | scanner.ScanComments
	lex.Whitespace = 1<<' ' | 1<<'\t' | 1<<'\r'
	var lexicalError error
	lex.Error = func(s *scanner.Scanner, message string) {
		if lexicalError == nil {
			lexicalError = fmt.Errorf("parse go.mod at %s: %s", s.Position, message)
		}
	}
	depth, lineStart := 0, true
	for token := lex.Scan(); token != scanner.EOF; token = lex.Scan() {
		if lexicalError != nil {
			return "", lexicalError
		}
		switch token {
		case '\n':
			lineStart = true
		case scanner.Comment:
			if !strings.HasPrefix(lex.TokenText(), "//") {
				return "", fmt.Errorf("go.mod does not allow block comments")
			}
		case '(':
			depth++
			lineStart = false
		case ')':
			if depth == 0 {
				return "", fmt.Errorf("unexpected closing parenthesis in go.mod")
			}
			depth--
			lineStart = false
		default:
			if depth == 0 && lineStart && token == scanner.Ident && lex.TokenText() == "module" {
				path, err := moduleDirectivePath(&lex, data)
				if lexicalError != nil {
					return "", lexicalError
				}
				return path, err
			}
			lineStart = false
		}
	}
	return "", fmt.Errorf("module directive not found in go.mod")
}

func moduleDirectivePath(lex *scanner.Scanner, data []byte) (string, error) {
	token := lex.Scan()
	if token == scanner.EOF || token == '\n' || token == scanner.Comment || token == '(' || token == ')' || token == '\'' || token == '`' {
		return "", fmt.Errorf("module directive requires one path")
	}
	quoted := token == scanner.String
	start, end := lex.Position.Offset, lex.Pos().Offset
	for token = lex.Scan(); token != scanner.EOF && token != '\n'; token = lex.Scan() {
		if token == scanner.Comment {
			if strings.HasPrefix(lex.TokenText(), "//") {
				break
			}
			return "", fmt.Errorf("go.mod does not allow block comments")
		}
		if quoted || token == scanner.String || token == '\'' || token == '`' || lex.Position.Offset != end {
			return "", fmt.Errorf("module directive requires one path without extra tokens")
		}
		end = lex.Pos().Offset
	}
	path := string(data[start:end])
	if quoted {
		value, err := strconv.Unquote(path)
		if err != nil {
			return "", fmt.Errorf("invalid quoted module path: %w", err)
		}
		if value == "" {
			return "", fmt.Errorf("module path is empty")
		}
		return value, nil
	}
	return path, nil
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
