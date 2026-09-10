package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type dependencyLister func(root, packagePath string) ([]string, error)

type listedPackage struct {
	ImportPath   string
	ForTest      string
	Dir          string
	GoFiles      []string
	CgoFiles     []string
	CFiles       []string
	CXXFiles     []string
	MFiles       []string
	HFiles       []string
	FFiles       []string
	SFiles       []string
	SwigFiles    []string
	SwigCXXFiles []string
	SysoFiles    []string
	EmbedFiles   []string
}

func main() {
	os.Exit(scopeCommand(os.Args[1:], os.Stdout, os.Stderr, listProductionInputs))
}

func scopeCommand(args []string, output, errorOutput io.Writer, lister dependencyLister) int {
	if err := runScope(args, output, lister); err != nil {
		fmt.Fprintln(errorOutput, err)
		return 1
	}
	return 0
}

func runScope(args []string, output io.Writer, lister dependencyLister) error {
	if len(args) != 4 {
		return errors.New("usage: benchscope <base-root> <head-root> <changed-files> <benchmark-packages>")
	}
	changed, err := readLines(args[2])
	if err != nil {
		return fmt.Errorf("read changed files: %w", err)
	}
	benchmarks, err := readLines(args[3])
	if err != nil {
		return fmt.Errorf("read benchmark packages: %w", err)
	}
	affected, err := selectAffectedPackages(args[0], args[1], changed, benchmarks, lister)
	if err != nil {
		return err
	}
	for _, packagePath := range affected {
		if _, err := fmt.Fprintln(output, packagePath); err != nil {
			return fmt.Errorf("write affected package: %w", err)
		}
	}
	return nil
}

func selectAffectedPackages(baseRoot, headRoot string, changed, benchmarks []string, lister dependencyLister) ([]string, error) {
	benchmarks = uniqueSorted(benchmarks)
	if moduleDependenciesChanged(changed) {
		return benchmarks, nil
	}
	changedSet := stringSet(changed)
	var affected []string
	for _, packagePath := range benchmarks {
		inputs, err := productionInputsForRevisions(baseRoot, headRoot, packagePath, lister)
		if err != nil {
			return nil, fmt.Errorf("list production inputs for %s: %w", packagePath, err)
		}
		if setsIntersect(changedSet, stringSet(inputs)) {
			affected = append(affected, packagePath)
		}
	}
	return affected, nil
}

func productionInputsForRevisions(baseRoot, headRoot, packagePath string, lister dependencyLister) ([]string, error) {
	baseInputs, err := lister(baseRoot, packagePath)
	if err != nil {
		return nil, fmt.Errorf("base revision: %w", err)
	}
	headInputs, err := lister(headRoot, packagePath)
	if err != nil {
		return nil, fmt.Errorf("head revision: %w", err)
	}
	return uniqueSorted(append(baseInputs, headInputs...)), nil
}

func listProductionInputs(root, packagePath string) ([]string, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	command := exec.Command("go", "list", "-deps", "-test", "-json", "./"+filepath.ToSlash(packagePath))
	command.Dir = absoluteRoot
	command.Env = append(os.Environ(), "GOWORK=off")
	result, err := command.Output()
	if err != nil {
		return nil, err
	}
	return parseProductionInputs(absoluteRoot, bytes.NewReader(result))
}

func parseProductionInputs(root string, input io.Reader) ([]string, error) {
	decoder := json.NewDecoder(input)
	var inputs []string
	for {
		var packageInfo listedPackage
		if err := decoder.Decode(&packageInfo); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}
		if isTestVariant(packageInfo) {
			continue
		}
		inputs = appendPackageInputs(inputs, root, packageInfo)
	}
	return uniqueSorted(inputs), nil
}

func appendPackageInputs(inputs []string, root string, packageInfo listedPackage) []string {
	relativeDir, err := filepath.Rel(root, packageInfo.Dir)
	if err != nil || outsideRoot(relativeDir) {
		return inputs
	}
	for _, file := range packageInfo.productionFiles() {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		path := filepath.Join(relativeDir, file)
		inputs = append(inputs, filepath.ToSlash(filepath.Clean(path)))
	}
	return inputs
}

func (packageInfo listedPackage) productionFiles() []string {
	files := append([]string{}, packageInfo.GoFiles...)
	groups := [][]string{
		packageInfo.CgoFiles, packageInfo.CFiles, packageInfo.CXXFiles,
		packageInfo.MFiles, packageInfo.HFiles, packageInfo.FFiles,
		packageInfo.SFiles, packageInfo.SwigFiles, packageInfo.SwigCXXFiles,
		packageInfo.SysoFiles, packageInfo.EmbedFiles,
	}
	for _, group := range groups {
		files = append(files, group...)
	}
	return files
}

func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, filepath.ToSlash(filepath.Clean(line)))
		}
	}
	return lines, scanner.Err()
}

func isTestVariant(packageInfo listedPackage) bool {
	return packageInfo.ForTest != "" || strings.HasSuffix(packageInfo.ImportPath, ".test") || strings.Contains(packageInfo.ImportPath, " [")
}

func outsideRoot(path string) bool {
	return path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) || filepath.IsAbs(path)
}

func moduleDependenciesChanged(changed []string) bool {
	for _, file := range changed {
		switch filepath.ToSlash(filepath.Clean(file)) {
		case "go.mod", "go.sum", "go.work", "go.work.sum":
			return true
		}
	}
	return false
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[filepath.ToSlash(filepath.Clean(value))] = struct{}{}
	}
	return result
}

func setsIntersect(first, second map[string]struct{}) bool {
	for value := range first {
		if _, exists := second[value]; exists {
			return true
		}
	}
	return false
}

func uniqueSorted(values []string) []string {
	set := stringSet(values)
	result := make([]string, 0, len(set))
	for value := range set {
		if value != "." {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
