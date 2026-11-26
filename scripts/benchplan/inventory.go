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
	if profile == benchlayout.APINonIdentityProfile {
		if err := verifyAPINonIdentityCompanions(ordinary, isolated); err != nil {
			return err
		}
	}
	for root := range ordinary {
		receiverCompanion := profile == benchlayout.CodegenMigrationStateProfile && root == benchlayout.CodegenReceiverBenchmark
		if benchlayout.ProfileForBenchmark(pkg, root) != profile && !receiverCompanion {
			delete(ordinary, root)
		}
	}
	if len(ordinary) == 0 || !maps.EqualFunc(ordinary, isolated, maps.Equal[map[string]struct{}]) {
		return fmt.Errorf("benchmark profile %q must reproduce its exact ordinary benchmark inventory", profile)
	}
	return verifyProfileLeaves(profile, isolated)
}

func verifyProfileLeaves(profile string, isolated inventory) error {
	switch profile {
	case benchlayout.LuxShortPathProfile:
		return verifyLuxShortPathInventory(isolated)
	case benchlayout.CodegenLiteralProfile, benchlayout.CodegenReceiverProfile,
		benchlayout.CodegenMigrationStateProfile, benchlayout.CodegenRelationLoaderProfile:
		return verifyCodegenProfileLeaves(profile, isolated)
	case benchlayout.SemanticEventEmitsProfile:
		const root = benchlayout.SemanticEventEmitsBenchmark
		leaves := benchmarkCaseProduct([]string{"direct_0", "direct_1", "direct_128"}, []string{"recursive=false", "recursive=true"})
		if !verifiedBenchmarkLeaves(isolated[root], root, leaves...) {
			return errors.New("semantic event collection profile requires the six verified benchmark leaves")
		}
	case benchlayout.APILocalRouteExportProfile:
		const root = benchlayout.APILocalRouteExportBenchmark
		if !verifiedBenchmarkLeaves(isolated[root], root, "routes=1/previous", "routes=1/current", "routes=16/previous",
			"routes=16/current", "routes=128/previous", "routes=128/current") {
			return errors.New("API local route export profile requires the six verified export leaves")
		}
	case benchlayout.APICanonicalJSONProfile:
		return verifyAPICanonicalJSONInventory(isolated)
	case benchlayout.APIBinaryParamLookupProfile:
		return verifyAPIBinaryParamLookupInventory(isolated)
	case benchlayout.APIRequestLogProfile:
		return verifyAPIRequestLogInventory(isolated)
	case benchlayout.LuviaGatewayIdentityProfile:
		const root = benchlayout.LuviaGatewayIdentityBenchmark
		if !verifiedBenchmarkLeaves(isolated[root], root, "anonymous/before", "anonymous/after", "authenticated/before",
			"authenticated/after", "typed-nil/before", "typed-nil/after") {
			return errors.New("Luvia gateway identity profile requires the six verified identity leaves")
		}
	case benchlayout.LuviaAuthMiddlewareProfile:
		const root = benchlayout.LuviaAuthMiddlewareBenchmark
		leaves := benchmarkCaseProduct([]string{"valid", "anonymous", "invalid", "inherited-missing", "inherited-other-key"},
			[]string{"before", "after"})
		if !verifiedBenchmarkLeaves(isolated[root], root, leaves...) {
			return errors.New("Luvia auth middleware profile requires the ten verified benchmark leaves")
		}
	case benchlayout.LuviaMergeDestinationProfile:
		const root = benchlayout.LuviaMergeDestinationBenchmark
		if !verifiedBenchmarkLeaves(isolated[root], root,
			"columnar=false/rows=1/direct=false", "columnar=false/rows=1/direct=true",
			"columnar=true/rows=1/direct=false", "columnar=true/rows=1/direct=true",
			"columnar=true/rows=128/direct=false", "columnar=true/rows=128/direct=true") {
			return errors.New("Luvia merge destination profile requires the six verified merge leaves")
		}
	case benchlayout.LuviaResolvedFieldsProfile:
		return verifyResolvedFieldsInventory(isolated)
	case benchlayout.RPCOwnedRequestProfile:
		return verifyRPCOwnedRequestInventory(isolated)
	case benchlayout.RPCDeadlineProfile:
		return verifyRPCDeadlineInventory(isolated)
	case benchlayout.RPCNilContextGuardProfile:
		return verifyRPCNilContextGuardInventory(isolated)
	case benchlayout.RPCPreparedFrameProfile:
		return verifyPairedBenchmarkInventory(isolated, benchlayout.RPCPreparedFrameBenchmark, "0", "128", "4096", "65536")
	case benchlayout.QueueStreamNameProfile:
		return verifyPairedBenchmarkInventory(isolated, benchlayout.QueueStreamNameBenchmark, "tasks", "email.send", "email_send")
	case benchlayout.LuviaConvenienceKeysProfile:
		const root = benchlayout.LuviaConvenienceKeysBenchmark
		leaves := benchmarkCaseProduct([]string{"row-small", "row-wide", "column-small", "column-batch", "column-many"},
			[]string{"keyLast=false/before", "keyLast=false/current", "keyLast=true/before", "keyLast=true/current"})
		if !verifiedBenchmarkLeaves(isolated[root], root, leaves...) {
			return errors.New("Luvia convenience keys profile requires the twenty verified benchmark leaves")
		}
	}
	return nil
}

func verifyRPCOwnedRequestInventory(isolated inventory) error {
	const root = benchlayout.RPCOwnedRequestBenchmark
	leaves := benchmarkCaseProduct([]string{"0", "128", "4096", "65536"}, []string{"before", "owned"})
	if !verifiedBenchmarkLeaves(isolated[root], root, leaves...) {
		return errors.New("RPC owned-request profile requires the eight verified benchmark leaves")
	}
	return nil
}

func verifyRPCDeadlineInventory(isolated inventory) error {
	const root = benchlayout.RPCDeadlineBenchmark
	leaves := benchmarkCaseProduct([]string{"NoDeadline", "Cancelled", "ExpiredEOF", "FutureTimeout", "ExpiredTimeout", "WrappedTimeout"},
		[]string{"Before", "After"})
	if !verifiedBenchmarkLeaves(isolated[root], root, leaves...) {
		return errors.New("RPC deadline classification profile requires the twelve verified benchmark leaves")
	}
	return nil
}

func verifyPairedBenchmarkInventory(isolated inventory, root string, inputs ...string) error {
	leaves := benchmarkCaseProduct(inputs, []string{"before", "after"})
	if !verifiedBenchmarkLeaves(isolated[root], root, leaves...) {
		return fmt.Errorf("%s profile requires the %d verified benchmark leaves", root, len(leaves))
	}
	return nil
}

func verifyRPCNilContextGuardInventory(isolated inventory) error {
	const root = benchlayout.RPCNilContextGuardBenchmark
	leaves := benchmarkCaseProduct([]string{"NoToken", "Verified", "Rejected", "Unconfigured"},
		[]string{"Before", "GuardOnly", "After"})
	if !verifiedBenchmarkLeaves(isolated[root], root, leaves...) {
		return errors.New("RPC nil-context guard profile requires the twelve verified benchmark leaves")
	}
	return nil
}

func verifyResolvedFieldsInventory(isolated inventory) error {
	const root = benchlayout.LuviaResolvedFieldsBenchmark
	if !verifiedBenchmarkLeaves(isolated[root], root,
		"object-2/per-value", "object-2/column-check",
		"list-small-128x16/per-value", "list-small-128x16/column-check",
		"list-wide-128x16/per-value", "list-wide-128x16/column-check") {
		return errors.New("Luvia resolved fields profile requires the six verified validation leaves")
	}
	return nil
}

func verifyAPINonIdentityCompanions(ordinary, isolated inventory) error {
	// Discovery still checks the broad binary before assigning these roots
	// exclusively to their corresponding sampling binaries.
	for root, leaves := range ordinary {
		switch benchlayout.ProfileForBenchmark(benchlayout.APIPackage, root) {
		case benchlayout.APILocalRouteExportProfile, benchlayout.APICanonicalJSONProfile,
			benchlayout.APIBinaryParamLookupProfile, benchlayout.APIRequestLogProfile:
			if !maps.Equal(leaves, isolated[root]) {
				return fmt.Errorf("API non-identity profile must retain the ordinary %s inventory", root)
			}
			delete(isolated, root)
		}
	}
	return nil
}

func verifyAPIRequestLogInventory(isolated inventory) error {
	const boundary = benchlayout.APIRequestLogBoundaryBenchmark
	const delivered = benchlayout.APIRequestLogDeliveredBenchmark
	if !verifiedBenchmarkLeaves(isolated[boundary], boundary, "disabled", "full", "synchronous", "unsampled") {
		return errors.New("API request-log profile requires the four verified boundary leaves")
	}
	if _, ok := isolated[delivered][delivered+"-2"]; !ok || len(isolated[delivered]) != 1 {
		return errors.New("API request-log profile requires the verified delivery leaf")
	}
	return nil
}

func verifyAPIBinaryParamLookupInventory(isolated inventory) error {
	const root = benchlayout.APIBinaryParamLookupBenchmark
	strideAndLookup := benchmarkCaseProduct([]string{"stride=1", "stride=1001", "stride=65537"}, []string{"direct=false", "direct=true"})
	leaves := benchmarkCaseProduct([]string{"n=1", "n=16"}, strideAndLookup)
	if !verifiedBenchmarkLeaves(isolated[root], root, leaves...) {
		return errors.New("API binary parameter lookup profile requires the twelve verified lookup leaves")
	}
	return nil
}

func verifyAPICanonicalJSONInventory(isolated inventory) error {
	for family, cases := range canonicalJSONCases() {
		root := "BenchmarkCanonicalJSON" + family
		if !verifiedBenchmarkLeaves(isolated[root], root, cases...) {
			return fmt.Errorf("API CanonicalJSON profile requires the exact %s leaf inventory", family)
		}
	}
	return nil
}

func canonicalJSONCases() map[string][]string {
	counts := []string{"-1", "0", "1", "128"}
	sizes := []string{"0", "1", "16", "128"}
	versions := []string{"before", "after"}
	return map[string][]string{
		"Structured": counts,
		"Typed": append(benchmarkCaseProduct(
			[]string{"string", "escaped", "boolean", "date", "uuid", "decimal", "enum", "unique", "long1KB"}, counts), "chinese72KB/-1"),
		"Bytes": benchmarkCaseProduct(
			[]string{"empty", "small", "escaped", "long72KB", "escaped4096", "list0", "list1", "list128", "escapedList128"},
			[]string{"decode", "canonical"}),
		"Float": benchmarkCaseProduct(sizes, versions),
		"Value": benchmarkCaseProduct(
			[]string{"integer", "object", "list0", "integerList1", "stringList1", "list1", "list16", "list128"}, versions),
		"Integer": benchmarkCaseProduct(sizes, versions),
	}
}

func benchmarkCaseProduct(values, variants []string) []string {
	cases := make([]string, 0, len(values)*len(variants))
	for _, value := range values {
		for _, variant := range variants {
			cases = append(cases, value+"/"+variant)
		}
	}
	return cases
}

func verifyCodegenProfileLeaves(profile string, isolated inventory) error {
	switch profile {
	case benchlayout.CodegenLiteralProfile:
		const root = benchlayout.CodegenLiteralBenchmark
		if !verifiedBenchmarkLeaves(isolated[root], root, "duration_leading_zero", "duration_plain", "int_leading_zero", "int_plain") {
			return errors.New("codegen integer literal profile requires the four verified integer and duration leaves")
		}
	case benchlayout.CodegenReceiverProfile:
		return verifyCodegenReceiverInventory(isolated)
	case benchlayout.CodegenMigrationStateProfile:
		return verifyCodegenMigrationStateInventory(isolated)
	case benchlayout.CodegenRelationLoaderProfile:
		const root = benchlayout.CodegenRelationLoaderBenchmark
		if !verifiedBenchmarkLeaves(isolated[root], root, "1/metadata", "1/source", "128/metadata", "128/source") {
			return errors.New("codegen relation loader profile requires the four verified benchmark leaves")
		}
	}
	return nil
}

func verifyCodegenReceiverInventory(isolated inventory) error {
	const root = benchlayout.CodegenReceiverBenchmark
	if !verifiedBenchmarkLeaves(isolated[root], root, "User", "_Record", "用户") {
		return errors.New("codegen global receiver profile requires the three verified receiver leaves")
	}
	return nil
}

func verifyCodegenMigrationStateInventory(isolated inventory) error {
	const migration = benchlayout.CodegenMigrationStateBenchmark
	if !verifiedBenchmarkLeaves(isolated[migration], migration,
		"empty/validate", "empty/load/reference", "empty/load/strict", "empty/save/reference", "empty/save/strict",
		"small/validate", "small/load/reference", "small/load/strict", "small/save/reference", "small/save/strict",
		"large/validate", "large/load/reference", "large/load/strict", "large/save/reference", "large/save/strict") {
		return errors.New("codegen migration state profile requires the 15 verified migration leaves")
	}
	return verifyCodegenReceiverInventory(isolated)
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
