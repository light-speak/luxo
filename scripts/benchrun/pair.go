package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/light-speak/luxo/internal/benchlayout"
)

type productionFiles struct {
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

func (files productionFiles) groups() [][]string {
	return [][]string{files.GoFiles, files.CgoFiles, files.CFiles, files.CXXFiles, files.MFiles,
		files.HFiles, files.FFiles, files.SFiles, files.SwigFiles, files.SwigCXXFiles, files.SysoFiles, files.EmbedFiles}
}

func (files productionFiles) equal(other productionFiles) bool {
	groups := other.groups()
	for i, names := range files.groups() {
		if !slices.Equal(names, groups[i]) {
			return false
		}
	}
	return true
}

type packageFixtures struct {
	productionFiles
	ImportPath       string
	Name             string
	TestGoFiles      []string
	XTestGoFiles     []string
	TestEmbedFiles   []string
	XTestEmbedFiles  []string
	ProductionEmbeds []string
}

type pinnedPackage struct {
	Physical   string
	Baseline   packageFixtures
	HeadNative packageFixtures
	FixtureSHA map[string]string
	DataSHA    map[string]string
	OverlaySHA [2]string
	BinarySHA  [2]string
}

type pairReceipt struct {
	Base, Head, Logical string
	Packages            []pinnedPackage
	GoEnvironment       [2]map[string]string
	Complete            bool
	Error               string `json:",omitempty"`
}

func runCompilePair(args []string, output, errorOutput io.Writer) error {
	if len(args) != 4 {
		return errors.New("usage: benchrun compile-pair <base> <head> <new-output> <logical-package>")
	}
	var roots [2]string
	for i := range roots {
		root, err := filepath.Abs(args[i])
		if err != nil {
			return err
		}
		roots[i], err = filepath.EvalSymlinks(root)
		if err != nil {
			return err
		}
	}
	destination, err := filepath.Abs(args[2])
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return err
	}
	destination = filepath.Join(parent, filepath.Base(destination))
	for _, root := range roots {
		relative, err := filepath.Rel(root, destination)
		if err == nil && filepath.IsLocal(relative) {
			return errors.New("paired output must be outside both source snapshots")
		}
	}
	if err := os.Mkdir(destination, 0755); err != nil {
		return err
	}
	receipt := pairReceipt{Base: roots[0], Head: roots[1], Logical: args[3]}
	err = compilePair(roots, destination, &receipt, output, errorOutput)
	receipt.Complete = err == nil
	if err != nil {
		receipt.Error = err.Error()
	}
	return errors.Join(err, writePairJSON(filepath.Join(destination, "fixtures.json"), receipt))
}

func compilePair(roots [2]string, destination string, receipt *pairReceipt, output, errorOutput io.Writer) error {
	for index, root := range roots {
		environment, err := pairedGoEnvironment(root)
		if err != nil {
			return err
		}
		receipt.GoEnvironment[index] = environment
	}
	if !maps.Equal(receipt.GoEnvironment[0], receipt.GoEnvironment[1]) {
		return errors.New("paired Go build environments differ")
	}
	base, err := benchlayout.Packages(roots[0], receipt.Logical)
	if err != nil {
		return err
	}
	head, err := benchlayout.Packages(roots[1], receipt.Logical)
	if err != nil {
		return err
	}
	if !slices.Equal(base, head) {
		return fmt.Errorf("paired benchmark needs an explicit adapter for changed physical packages: %v -> %v", base, head)
	}
	for _, side := range []string{"base", "head"} {
		if err := os.Mkdir(filepath.Join(destination, side), 0755); err != nil {
			return err
		}
	}
	for _, physical := range base {
		plan := pinnedPackage{Physical: physical}
		err := compilePinnedPackage(roots, destination, &plan, output, errorOutput)
		receipt.Packages = append(receipt.Packages, plan)
		if err != nil {
			return fmt.Errorf("paired benchmark %s: %w", physical, err)
		}
	}
	return nil
}

func compilePinnedPackage(roots [2]string, destination string, plan *pinnedPackage, output, errorOutput io.Writer) error {
	var native [2]packageFixtures
	for i, root := range roots {
		physicalDir := filepath.Join(root, plan.Physical)
		resolved, err := filepath.EvalSymlinks(physicalDir)
		if err != nil || resolved != physicalDir {
			return fmt.Errorf("benchmark package requires a regular directory: %s", physicalDir)
		}
		native[i], err = listFixtures(root, plan.Physical, "")
		if err != nil {
			return err
		}
	}
	plan.Baseline, plan.HeadNative = native[0], native[1]
	if native[0].ImportPath != native[1].ImportPath || native[0].Name != native[1].Name {
		return errors.New("changed Go package identity requires an explicit fixture adapter")
	}
	if len(testSources(native[0])) == 0 {
		return errors.New("baseline has no build-enabled Go test sources")
	}
	dir := filepath.Join(destination, "fixtures", plan.Physical)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if err := stageFixtures(roots, dir, plan); err != nil {
		return err
	}
	for index, side := range []string{"base", "head"} {
		if err := compilePinnedSide(roots[index], destination, dir, side, index, native[index], plan, output, errorOutput); err != nil {
			return fmt.Errorf("%s fixture does not match the paired contract: %w", side, err)
		}
	}
	return nil
}

func compilePinnedSide(root, destination, dir, side string, index int, native packageFixtures, plan *pinnedPackage, output, errorOutput io.Writer) error {
	file := filepath.Join(dir, side+".json")
	replacements := make(map[string]string)
	for _, name := range testSources(native) {
		replacements[filepath.Join(root, plan.Physical, name)] = ""
	}
	for _, name := range testSources(plan.Baseline) {
		replacements[filepath.Join(root, plan.Physical, name)] = filepath.Join(dir, name)
	}
	if err := writePairJSON(file, struct{ Replace map[string]string }{replacements}); err != nil {
		return err
	}
	var err error
	plan.OverlaySHA[index], err = digestPath(file)
	if err != nil {
		return err
	}
	seen, err := listFixtures(root, plan.Physical, file)
	if err != nil {
		return err
	}
	if !sameFixtures(plan.Baseline, seen) {
		return errors.New("build-enabled fixture files differ from the baseline snapshot")
	}
	if !native.productionFiles.equal(seen.productionFiles) {
		return errors.New("test overlay changes the production file inventory")
	}
	for _, name := range slices.Concat(native.ProductionEmbeds, seen.ProductionEmbeds) {
		if _, overlaid := replacements[filepath.Join(root, plan.Physical, name)]; overlaid {
			return fmt.Errorf("test source is also used as a production embed: %s", name)
		}
	}
	binary := binaryPath(filepath.Join(destination, side), plan.Physical)
	cmd := compileCommand(root, plan.Physical, binary, file)
	cmd.Stdout, cmd.Stderr = output, errorOutput
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("compile pinned benchmark tests: %w", err)
	}
	plan.BinarySHA[index], err = digestPath(binary)
	return err
}

func compileCommand(root, pkg, binary, overlay string) *exec.Cmd {
	args := []string{"test", "-trimpath", "-c"}
	if overlay != "" {
		args = append(args, "-overlay="+overlay)
	}
	args = append(args, "./"+pkg, "-o", binary)
	cmd := goCommand(root, args...)
	if overlay != "" {
		cmd.Env = append(cmd.Env, "GOTOOLCHAIN=local")
	}
	return cmd
}

func binaryPath(bin, pkg string) string {
	return filepath.Join(bin, strings.ReplaceAll(pkg, "/", "-")+".test")
}

func goCommand(root string, args ...string) *exec.Cmd {
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(root, "go.work"))
	return cmd
}

func pairedGoEnvironment(root string) (map[string]string, error) {
	cmd := goCommand(root, "env", "-json", "GOVERSION", "GOOS", "GOARCH", "GOAMD64", "GOARM64",
		"GOEXPERIMENT", "GOFLAGS", "CGO_ENABLED", "CC", "CXX")
	cmd.Env = append(cmd.Env, "GOTOOLCHAIN=local")
	raw, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var environment map[string]string
	if err := json.Unmarshal(raw, &environment); err != nil {
		return nil, err
	}
	if flags := environment["GOFLAGS"]; flags != "" && flags != "-trimpath" {
		return nil, errors.New("paired compilation requires default Go build flags")
	}
	if environment["GOVERSION"] != runtime.Version() {
		return nil, fmt.Errorf("paired compiler %q differs from benchrun toolchain %q",
			environment["GOVERSION"], runtime.Version())
	}
	return environment, nil
}

func listFixtures(root, pkg, overlay string) (packageFixtures, error) {
	args := []string{"list", "-deps", "-test", "-json"}
	if overlay != "" {
		args = append(args, "-overlay="+overlay)
	}
	cmd := goCommand(root, append(args, "./"+pkg)...)
	cmd.Env = append(cmd.Env, "GOTOOLCHAIN=local")
	var info packageFixtures
	raw, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return info, fmt.Errorf("list %s: %w: %s", pkg, err, exit.Stderr)
		}
		return info, err
	}
	return readPackageFixtures(filepath.Join(root, pkg), raw)
}

func readPackageFixtures(directory string, data []byte) (packageFixtures, error) {
	var info packageFixtures
	embeds := make(map[string]struct{})
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var entry struct {
			packageFixtures
			Dir, ForTest string
		}
		err := decoder.Decode(&entry)
		if err == io.EOF {
			break
		}
		if err != nil {
			return info, err
		}
		if entry.Dir == directory {
			// The target test variant merges test embeds into EmbedFiles.
			// Dependencies recompiled for that test can still have production embeds.
			if entry.ForTest != "" || strings.HasSuffix(entry.ImportPath, ".test") {
				continue
			}
			info = entry.packageFixtures
		}
		for _, name := range entry.EmbedFiles {
			relative, err := filepath.Rel(directory, filepath.Join(entry.Dir, name))
			if err == nil && filepath.IsLocal(relative) {
				embeds[relative] = struct{}{}
			}
		}
	}
	if info.ImportPath == "" {
		return info, fmt.Errorf("benchmark target missing from Go package list: %s", directory)
	}
	info.ProductionEmbeds = slices.Sorted(maps.Keys(embeds))
	return info, nil
}

func testSources(info packageFixtures) []string {
	return slices.Concat(info.TestGoFiles, info.XTestGoFiles)
}

func sameFixtures(expected, actual packageFixtures) bool {
	return expected.Name == actual.Name && expected.ImportPath == actual.ImportPath &&
		slices.Equal(expected.TestGoFiles, actual.TestGoFiles) && slices.Equal(expected.XTestGoFiles, actual.XTestGoFiles) &&
		slices.Equal(expected.TestEmbedFiles, actual.TestEmbedFiles) && slices.Equal(expected.XTestEmbedFiles, actual.XTestEmbedFiles)
}

func stageFixtures(roots [2]string, dir string, plan *pinnedPackage) error {
	base, err := os.OpenRoot(filepath.Join(roots[0], plan.Physical))
	if err != nil {
		return err
	}
	defer base.Close()
	head, err := os.OpenRoot(filepath.Join(roots[1], plan.Physical))
	if err != nil {
		return err
	}
	defer head.Close()
	plan.FixtureSHA = make(map[string]string)
	for _, name := range testSources(plan.Baseline) {
		if !strings.HasSuffix(name, "_test.go") || filepath.Base(name) != name {
			return fmt.Errorf("invalid Go test file %q", name)
		}
		data, err := readFixture(base, name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
			return err
		}
		plan.FixtureSHA[name] = digest(data)
	}
	embeds := slices.Concat(plan.Baseline.TestEmbedFiles, plan.Baseline.XTestEmbedFiles)
	plan.DataSHA, err = compareFixtureData(base, head, embeds)
	return err
}

func compareFixtureData(base, head *os.Root, embeds []string) (map[string]string, error) {
	left, err := runtimeFixturePaths(base, embeds)
	if err != nil {
		return nil, err
	}
	right, err := runtimeFixturePaths(head, embeds)
	if err != nil {
		return nil, err
	}
	if !maps.Equal(left, right) {
		for _, name := range slices.Concat(slices.Sorted(maps.Keys(left)), slices.Sorted(maps.Keys(right))) {
			if left[name] != right[name] {
				return nil, fmt.Errorf("runtime or embedded fixture differs: %s", name)
			}
		}
	}
	return left, nil
}

func runtimeFixturePaths(root *os.Root, embeds []string) (map[string]string, error) {
	data := make(map[string]string)
	record := func(name string) error {
		value, err := readFixture(root, name)
		if err != nil {
			return err
		}
		data[name] = digest(value)
		return nil
	}
	for _, name := range embeds {
		if err := record(name); err != nil {
			return nil, err
		}
	}
	err := fs.WalkDir(root.FS(), "testdata", func(path string, entry fs.DirEntry, err error) error {
		if path == "testdata" && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			data[path] = "directory"
			return nil
		}
		return record(path)
	})
	if err != nil {
		return nil, err
	}
	return data, nil
}

func readFixture(root *os.Root, name string) ([]byte, error) {
	if !filepath.IsLocal(name) || filepath.ToSlash(filepath.Clean(name)) != filepath.ToSlash(name) {
		return nil, fmt.Errorf("invalid fixture path %q", name)
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("paired fixture must be a regular file: %s", name)
	}
	return root.ReadFile(name)
}

func writePairJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func digestPath(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func digest(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
