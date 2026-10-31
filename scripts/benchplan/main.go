package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

func main() {
	os.Exit(command(os.Args[1:], os.Stdout, os.Stderr))
}

func command(args []string, output, errorOutput io.Writer) int {
	if err := run(args, output); err != nil {
		fmt.Fprintln(errorOutput, err)
		return 1
	}
	return 0
}

func run(args []string, output io.Writer) error {
	if len(args) > 0 && args[0] == "--normalize" {
		return normalizeFiles(args)
	}
	if len(args) > 0 && args[0] == "--verify" {
		return verifyFiles(args)
	}
	if len(args) != 3 {
		return errors.New("usage: benchplan <packages.txt> <inventory-dir> <output-dir>")
	}
	groups, err := loadGroups(args[0], args[1])
	if err != nil {
		return err
	}
	shards, err := makePlan(groups, 8)
	if err != nil {
		return err
	}
	if err := writePlans(args[2], shards); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(struct {
		Include []benchShard `json:"include"`
	}{shards})
}

func loadGroups(packagesPath, inventoryDir string) ([]benchGroup, error) {
	data, err := os.ReadFile(packagesPath)
	if err != nil {
		return nil, err
	}
	var groups []benchGroup
	seen := make(map[string]bool)
	for _, name := range strings.Fields(string(data)) {
		if !validPackage(name) || seen[name] {
			return nil, fmt.Errorf("invalid or repeated benchmark package %q", name)
		}
		seen[name] = true
		base, err := loadInventory(inventoryDir, "base", name)
		if err != nil {
			return nil, err
		}
		head, err := loadInventory(inventoryDir, "head", name)
		if err != nil {
			return nil, err
		}
		groups = append(groups, joinInventories(name, base, head)...)
	}
	return groups, nil
}

func validPackage(name string) bool {
	return strings.HasPrefix(name, "pkg/") && filepath.ToSlash(filepath.Clean(name)) == name &&
		!strings.ContainsAny(name, "\t\n\r\\") && !strings.Contains(name, "..")
}

func loadInventory(root, revision, name string) (inventory, error) {
	path := filepath.Join(root, revision, strings.ReplaceAll(name, "/", "-")+".log")
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	result, err := readInventory(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return result, nil
}

func writePlans(root string, shards []benchShard) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	for _, shard := range shards {
		if err := os.WriteFile(filepath.Join(root, "shard-"+shard.ID+".tsv"), []byte(formatShard(shard)), 0o644); err != nil {
			return err
		}
		data, err := json.MarshalIndent(expectedForShard(shard), "", "  ")
		if err != nil {
			// The fixed expectation contains only strings and slices; JSON requires this error branch.
			return err
		}
		if err := os.WriteFile(filepath.Join(root, "shard-"+shard.ID+".json"), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func formatShard(shard benchShard) string {
	packages := make(map[string][]string)
	for _, group := range shard.Groups {
		packages[group.Package] = append(packages[group.Package], regexp.QuoteMeta(group.Name))
	}
	names := make([]string, 0, len(packages))
	for name := range packages {
		names = append(names, name)
	}
	slices.Sort(names)
	var result strings.Builder
	for _, name := range names {
		slices.Sort(packages[name])
		fmt.Fprintf(&result, "%s\t^(%s)$\n", name, strings.Join(packages[name], "|"))
	}
	return result.String()
}
