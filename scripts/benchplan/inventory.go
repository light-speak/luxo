package main

import (
	"bufio"
	"errors"
	"fmt"
	"go/token"
	"io"
	"maps"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/internal/benchlayout"
)

type inventory map[string]map[string]struct{}

// Both test binaries and go test package summaries can report failure.
func isBenchmarkFailure(line string) bool {
	return line == "FAIL" || strings.HasPrefix(line, "FAIL\t") ||
		strings.HasPrefix(line, "FAIL ") || strings.HasPrefix(line, "--- FAIL:")
}

type benchmarkCompletion struct {
	pending bool
	passed  bool
}

func (state *benchmarkCompletion) observe(line string) error {
	switch {
	case line == "PASS":
		state.pending, state.passed = false, true
	case strings.HasPrefix(line, "pkg: "):
		if state.pending {
			return errors.New("benchmark run has no PASS before next package")
		}
	case strings.HasPrefix(line, "Benchmark"):
		state.pending = true
	}
	return nil
}

func (state benchmarkCompletion) finish() error {
	if state.pending || !state.passed {
		return errors.New("benchmark run requires a final PASS after measurements")
	}
	return nil
}

func readInventory(input io.Reader) (inventory, error) {
	result := make(inventory)
	scanner := bufio.NewScanner(input)
	var completion benchmarkCompletion
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if isBenchmarkFailure(line) {
			return nil, errors.New("benchmark inventory failed")
		}
		if err := completion.observe(line); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(line, "Benchmark") {
			continue
		}
		if err := addMeasurement(result, strings.Fields(line)); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if err := completion.finish(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, errors.New("inventory requires successful benchmark measurements and PASS")
	}
	return result, nil
}

func verifyEqualInventories(args []string) error {
	if len(args) != 3 {
		return errors.New("usage: benchplan --verify-equal-inventories <ordinary.log> <performance.log>")
	}
	ordinary, performance, err := readInventoryPair(args[1], args[2])
	if err != nil {
		return err
	}
	if !maps.EqualFunc(ordinary, performance, maps.Equal[map[string]struct{}]) {
		return errors.New("performance compilation changed the benchmark inventory")
	}
	return nil
}

func verifyProfileInventory(args []string) error {
	if len(args) != 5 {
		return errors.New("usage: benchplan --verify-profile-inventory <package> <profile> <ordinary.log> <isolated.log>")
	}
	pkg, profile := args[1], args[2]
	if profile == benchlayout.DefaultProfile || !benchlayout.ValidPerformanceProfile(pkg, profile) {
		return fmt.Errorf("unknown isolated benchmark profile %q for %s", profile, pkg)
	}
	ordinary, isolated, err := readInventoryPair(args[3], args[4])
	if err != nil {
		return err
	}
	for root := range ordinary {
		if benchlayout.ProfileForBenchmark(pkg, root) != profile {
			delete(ordinary, root)
		}
	}
	if len(ordinary) == 0 || !maps.EqualFunc(ordinary, isolated, maps.Equal[map[string]struct{}]) {
		return fmt.Errorf("benchmark profile %q must reproduce its exact ordinary benchmark inventory", profile)
	}
	switch profile {
	case benchlayout.LuxShortPathProfile:
		return verifyLuxShortPathInventory(isolated)
	case benchlayout.CodegenLiteralProfile:
		const root = benchlayout.CodegenLiteralBenchmark
		if !verifiedBenchmarkLeaves(isolated[root], root, "duration_leading_zero", "duration_plain", "int_leading_zero", "int_plain") {
			return errors.New("codegen integer literal profile requires the four verified integer and duration leaves")
		}
	case benchlayout.CodegenReceiverProfile:
		const root = benchlayout.CodegenReceiverBenchmark
		if !verifiedBenchmarkLeaves(isolated[root], root, "User", "_Record", "用户") {
			return errors.New("codegen global receiver profile requires the three verified receiver leaves")
		}
	case benchlayout.LuviaGatewayIdentityProfile:
		const root = benchlayout.LuviaGatewayIdentityBenchmark
		if !verifiedBenchmarkLeaves(isolated[root], root, "anonymous/before", "anonymous/after", "authenticated/before",
			"authenticated/after", "typed-nil/before", "typed-nil/after") {
			return errors.New("Luvia gateway identity profile requires the six verified identity leaves")
		}
	}
	return nil
}

func verifyLuxShortPathInventory(isolated map[string]map[string]struct{}) error {
	const strict = benchlayout.LuxStrictFilterBenchmark
	const decimal = benchlayout.LuxDecimalAlignmentBenchmark
	if !verifiedBenchmarkLeaves(isolated[strict], strict,
		"Bool/Before", "Bool/After", "Contains/Before", "Contains/After", "Float/Before", "Float/After",
		"Int/Before", "Int/After", "Search/Before", "Search/After", "String/Before", "String/After",
		"String1KiB/Before", "String1KiB/After", "Time/Before", "Time/After") {
		return errors.New("Lux short-path profile requires the 16 verified filter leaves")
	}
	if !verifiedBenchmarkLeaves(isolated[decimal], decimal,
		"Add/near/before", "Add/near/after", "Add/same/before", "Add/same/after", "Add/zero/before", "Add/zero/after",
		"Sub/near/before", "Sub/near/after", "Sub/same/before", "Sub/same/after", "Sub/zero/before", "Sub/zero/after") {
		return errors.New("Lux short-path profile requires the 12 verified decimal alignment leaves")
	}
	return nil
}

func verifiedBenchmarkLeaves(leaves map[string]struct{}, root string, cases ...string) bool {
	if len(leaves) != len(cases) {
		return false
	}
	for _, name := range cases {
		if _, ok := leaves[root+"/"+name+"-2"]; !ok {
			return false
		}
	}
	return true
}

func readInventoryPair(first, second string) (inventory, inventory, error) {
	var inventories [2]inventory
	for i, path := range []string{first, second} {
		file, err := os.Open(path)
		if err != nil {
			return nil, nil, err
		}
		inventories[i], err = readInventory(file)
		if err := errors.Join(err, file.Close()); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return inventories[0], inventories[1], nil
}

func addMeasurement(result inventory, fields []string) error {
	if len(fields) < 4 || !strings.HasSuffix(fields[0], "-2") || !measurementTail(fields[1:]) {
		return fmt.Errorf("invalid benchmark measurement: %q", fields)
	}
	name, _, _ := strings.Cut(strings.TrimSuffix(fields[0], "-2"), "/")
	if !token.IsIdentifier(name) || name == "Benchmark" {
		return fmt.Errorf("invalid benchmark name %q", name)
	}
	if result[name] == nil {
		result[name] = make(map[string]struct{})
	}
	if _, exists := result[name][fields[0]]; exists {
		return fmt.Errorf("duplicate benchmark measurement %q", fields[0])
	}
	result[name][fields[0]] = struct{}{}
	return nil
}

func measurementTail(fields []string) bool {
	if len(fields) < 3 || len(fields)%2 != 1 {
		return false
	}
	iterations, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil || iterations == 0 {
		return false
	}
	if fields[2] != "ns/op" && fields[2] != "B/op" {
		return false
	}
	for i := 1; i < len(fields); i += 2 {
		value, err := strconv.ParseFloat(fields[i], 64)
		if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}

func joinInventories(packagePath string, base, head inventory) []benchGroup {
	groups := make(map[string]benchGroup)
	for name, leaves := range base {
		groups[name] = benchGroup{Package: packagePath, Name: name, Base: len(leaves), BaseNames: leafNames(leaves)}
	}
	for name, leaves := range head {
		group := groups[name]
		group.Package, group.Name, group.Head = packagePath, name, len(leaves)
		group.HeadNames = leafNames(leaves)
		for leaf := range leaves {
			if _, exists := base[name][leaf]; exists {
				group.Common = true
				break
			}
		}
		groups[name] = group
	}
	result := make([]benchGroup, 0, len(groups))
	for _, group := range groups {
		result = append(result, group)
	}
	return result
}

func leafNames(leaves map[string]struct{}) []string {
	result := make([]string, 0, len(leaves))
	for name := range leaves {
		result = append(result, name)
	}
	slices.Sort(result)
	return result
}
