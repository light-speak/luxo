package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestCodecProfilesStayIdenticalAcrossSamplingPhases(t *testing.T) {
	const codec = "pkg/lux/codec"
	const reader = "BenchmarkElementPayload"
	const writer = "BenchmarkWriteColumn"
	groups := []benchGroup{{Package: codec, Name: writer}, {Package: codec, Name: reader}, {Package: "pkg/example", Name: reader}}
	primary := formatShard(benchShard{Groups: groups})
	for _, row := range []string{codec + "\tcodec-reader\t^(" + reader + ")$\n", codec + "\tdefault\t^(" + writer + ")$\n", "pkg/example\tdefault\t^(" + reader + ")$\n"} {
		if !strings.Contains(primary, row) {
			t.Fatalf("primary lacks %q: %s", row, primary)
		}
	}
	confirmation := formatConfirmationBatch([]sampleKey{{codec, reader + "/one-2"}, {codec, writer + "/two-2"}})
	for _, row := range []string{codec + "\tcodec-reader\t^" + reader + "$/^one$\n", codec + "\tdefault\t^" + writer + "$/^two$\n"} {
		if !strings.Contains(confirmation, row) {
			t.Fatalf("confirmation lacks %q: %s", row, confirmation)
		}
	}
}

func TestCodecReaderInventoryRequiresExactPartition(t *testing.T) {
	if err := run([]string{"--verify-profile-inventory"}, io.Discard); err == nil {
		t.Fatal("accepted missing reader inventory arguments")
	}
	root := benchmarkTempDir(t)
	const read = "BenchmarkElementPayload/one-2 1 12 ns/op 0 B/op 0 allocs/op\n"
	const second = "BenchmarkElementPayload/two-2 1 12 ns/op 0 B/op 0 allocs/op\n"
	const write = "BenchmarkWriteColumn-2 1 12 ns/op 0 B/op 0 allocs/op\n"
	ordinary, partition := root+"/ordinary.log", root+"/partition.log"
	writeFixture(t, ordinary, read+second+write+"PASS\n")
	for _, test := range []struct {
		name, inventory string
		valid           bool
	}{
		{"same", read + second, true}, {"duplicate", read + second + second, false},
		{"missing", read, false}, {"wrong profile", read + second + write, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeFixture(t, partition, test.inventory+"PASS\n")
			if err := run([]string{"--verify-profile-inventory", "pkg/lux/codec", "codec-reader", ordinary, partition}, io.Discard); (err == nil) != test.valid {
				t.Fatalf("reader comparison valid=%v: %v", test.valid, err)
			}
		})
	}
}

func TestProfileBatchRejectsMisroutedShard(t *testing.T) {
	root := benchmarkTempDir(t)
	expected, batch := root+"/expected.json", root+"/batch.tsv"
	const prefix = `{"Package":"pkg/lux/codec","Name":"`
	const reader = prefix + `BenchmarkElementPayload/one-2"}`
	const writer = prefix + `BenchmarkWriteColumn/two-2"}`
	const keys = `[` + reader + `,` + writer + `]`
	writeFixture(t, expected, `{"Base":`+keys+`,"Head":`+keys+`}`)
	for _, test := range []struct{ phase, reader, writer string }{
		{"primary", "^(BenchmarkElementPayload)$", "^(BenchmarkWriteColumn)$"},
		{"confirmation", "^BenchmarkElementPayload$/^one$", "^BenchmarkWriteColumn$/^two$"},
	} {
		t.Run(test.phase, func(t *testing.T) {
			args := []string{"--verify-profile-batch", test.phase, expected, batch}
			const pkg = "pkg/lux/codec\t"
			writeFixture(t, batch, pkg+"codec-reader\t"+test.reader+"\n"+pkg+"default\t"+test.writer+"\n")
			if err := run(args, io.Discard); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, batch, pkg+"codec-reader\t"+test.writer+"\n"+pkg+"default\t"+test.reader+"\n")
			if err := run(args, io.Discard); err == nil {
				t.Fatal("accepted swapped reader and writer profiles")
			}
		})
	}
}

func TestProfileBatchValidationRejectsBrokenPlanningArtifacts(t *testing.T) {
	root := benchmarkTempDir(t)
	const valid = `[{"Package":"pkg/lux/codec","Name":"BenchmarkElementPayload/one-2"}]`
	const wrong = `[{"Package":"pkg/lux/codec","Name":"BenchmarkElementPayload/two-2"}]`
	const badPkg = `[{"Package":"../codec","Name":"BenchmarkElementPayload/one-2"}]`
	const badLeaf = `[{"Package":"pkg/lux/codec","Name":"BenchmarkElementPayload/one"}]`
	expected, batch := root+"/expected.log", root+"/batch.tsv"
	if err := run([]string{"--verify-profile-batch"}, io.Discard); err == nil {
		t.Fatal("accepted missing batch arguments")
	}
	for _, tc := range []struct{ phase, base, head, want string }{
		{"primary", "", "", "invalid character"},
		{"primary", "[]", valid, "requires base and head leaves"},
		{"primary", badPkg, valid, "invalid benchmark batch leaf"},
		{"primary", badLeaf, valid, "invalid benchmark batch leaf"},
		{"confirmation", valid, wrong, "require the same paired leaves"},
		{"other", valid, valid, "unknown benchmark batch phase"},
	} {
		writeFixture(t, expected, `{"Base":`+tc.base+`,"Head":`+tc.head+`}`)
		writeFixture(t, batch, "unused\n")
		err := run([]string{"--verify-profile-batch", tc.phase, expected, batch}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("phase %s failure = %v, want %q", tc.phase, err, tc.want)
		}
	}
	if err := run([]string{"--verify-profile-batch", "primary", expected, root + "/missing.tsv"}, io.Discard); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing batch read error = %v", err)
	}
}

func TestAPIAndLuviaIsolatedInventoriesMatchOnlyRoutedRoots(t *testing.T) {
	root := benchmarkTempDir(t)
	ordinary, isolated := root+"/ordinary.log", root+"/isolated.log"
	row := func(name string) string { return name + "-2 1 3 ns/op 0 B/op 0 allocs/op\n" }
	for _, tc := range []struct {
		pkg, profile, ordinaryRoot string
		isolatedRoots              []string
	}{
		{"pkg/lux/api", "api-non-identity", "BenchmarkIdentityEnumArray/valid", []string{"BenchmarkRequestLogBoundary/disabled", "BenchmarkWSOrigin/source"}},
		{"pkg/lux/luvia", "luvia-primary-trace", "BenchmarkStaticMetricsJSON", []string{"BenchmarkPrimaryKeys/row-small", "BenchmarkTraceEncodingComparison/true"}},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			args := []string{"--verify-profile-inventory", tc.pkg, tc.profile, ordinary, isolated}
			body := row(tc.isolatedRoots[0]) + row(tc.isolatedRoots[1])
			writeFixture(t, ordinary, row(tc.ordinaryRoot)+body+"PASS\n")
			writeFixture(t, isolated, body+"PASS\n")
			if err := run(args, io.Discard); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, isolated, row(tc.ordinaryRoot)+body+"PASS\n")
			if err := run(args, io.Discard); err == nil {
				t.Fatal("the isolated inventory included a benchmark assigned to the ordinary profile")
			}
			args[2] = "default"
			if err := run(args, io.Discard); err == nil {
				t.Fatal("accepted the ordinary profile as isolated")
			}
		})
	}
}

func TestAPILocalRouteExportProfileKeepsExactLeaves(t *testing.T) {
	const root = "BenchmarkLocalRouteExport/"
	const measurement = "-2 1 3 ns/op 0 B/op 0 allocs/op\n"
	const other = "BenchmarkLocalRouteLookup/routes=1/current" + measurement
	leaves := []string{"routes=1/previous", "routes=1/current", "routes=16/previous", "routes=16/current", "routes=128/previous", "routes=128/current"}
	var measurements strings.Builder
	for _, leaf := range leaves {
		measurements.WriteString(root + leaf + measurement)
	}
	full := measurements.String()
	partial := strings.Replace(full, root+leaves[5]+measurement, "", 1)
	dir := benchmarkTempDir(t)
	ordinary, isolated := dir+"/ordinary.log", dir+"/isolated.log"
	for _, tc := range []struct {
		name, profile, ordinary, isolated string
		valid                             bool
	}{
		{"exact new partition", "api-local-route-export", full + other, full, true},
		{"missing leaf in both builds", "api-local-route-export", partial + other, partial, false},
		{"generic fixture also retains the route", "api-non-identity", full + other, full + other, true},
		{"generic fixture loses a route leaf", "api-non-identity", full + other, partial + other, false},
		{"generic fixture loses a neighbor", "api-non-identity", full + other, full, false},
		{"export profile contains the lookup", "api-local-route-export", full + other, full + other, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFixture(t, ordinary, tc.ordinary+"PASS\n")
			writeFixture(t, isolated, tc.isolated+"PASS\n")
			args := []string{"--verify-profile-inventory", "pkg/lux/api", tc.profile, ordinary, isolated}
			if err := run(args, io.Discard); (err == nil) != tc.valid {
				t.Fatalf("inventory valid=%v: %v", tc.valid, err)
			}
		})
	}
}

func TestAPICanonicalJSONProfileKeepsAllMeasuredLeaves(t *testing.T) {
	const measurement = "-2 1 3 ns/op 0 B/op 0 allocs/op\n"
	const other = "BenchmarkLocalRouteLookup/routes=1/current" + measurement
	var all strings.Builder
	for family, leaves := range canonicalJSONCases() {
		for _, leaf := range leaves {
			all.WriteString("BenchmarkCanonicalJSON" + family + "/" + leaf + measurement)
		}
	}
	full := all.String()
	if strings.Count(full, measurement) != 91 {
		t.Fatal("CanonicalJSON inventory must keep all 91 measured leaves")
	}
	partial := strings.Replace(full, "BenchmarkCanonicalJSONValue/object/after"+measurement, "", 1)
	dir := benchmarkTempDir(t)
	ordinary, isolated := dir+"/ordinary.log", dir+"/isolated.log"
	for _, tc := range []struct {
		name, profile, ordinary, isolated string
		valid                             bool
	}{
		{"dedicated keeps all leaves", "api-canonical-json", full + other, full, true},
		{"both binaries omit a leaf", "api-canonical-json", partial + other, partial, false},
		{"dedicated includes an unrelated leaf", "api-canonical-json", full + other, full + other, false},
		{"broad discovery keeps all leaves", "api-non-identity", full + other, full + other, true},
		{"broad discovery loses one leaf", "api-non-identity", full + other, partial + other, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFixture(t, ordinary, tc.ordinary+"PASS\n")
			writeFixture(t, isolated, tc.isolated+"PASS\n")
			args := []string{"--verify-profile-inventory", "pkg/lux/api", tc.profile, ordinary, isolated}
			if err := run(args, io.Discard); (err == nil) != tc.valid {
				t.Fatalf("inventory valid=%v: %v", tc.valid, err)
			}
		})
	}
}

const (
	mergeDestinationPackage = "pkg/lux/luvia"
	mergeDestinationProfile = "luvia-merge-destination"
	mergeDestinationRoot    = "BenchmarkMergeDestination"
	mergeDestinationControl = "BenchmarkGatewayStandardStartStop"
	mergeDestinationTarget  = "columnar=true/rows=1/direct=true"
)

func TestMergeDestinationProfileKeepsExactlySixLeaves(t *testing.T) {
	rows := []string{
		"columnar=false/rows=1/direct=false",
		"columnar=false/rows=1/direct=true",
		"columnar=true/rows=1/direct=false",
		mergeDestinationTarget,
		"columnar=true/rows=128/direct=false",
		"columnar=true/rows=128/direct=true",
	}
	verifyLuviaProfileInventory(t, mergeDestinationProfile, mergeDestinationRoot, rows, "columnar=false/rows=128/direct=true")
}

const (
	resolvedFieldsProfile = "luvia-resolved-fields"
	resolvedFieldsRoot    = "BenchmarkValidateResolvedFields"
)

func TestResolvedFieldsProfileKeepsExactlySixLeaves(t *testing.T) {
	rows := []string{
		"object-2/per-value", "object-2/column-check",
		"list-small-128x16/per-value", "list-small-128x16/column-check",
		"list-wide-128x16/per-value", "list-wide-128x16/column-check",
	}
	verifyLuviaProfileInventory(t, resolvedFieldsProfile, resolvedFieldsRoot, rows, "object-3/column-check")
}

func verifyLuviaProfileInventory(t *testing.T, profile, benchmark string, rows []string, extraLeaf string) {
	t.Helper()
	root := benchmarkTempDir(t)
	ordinary, isolated := root+"/ordinary.log", root+"/isolated.log"
	const measurement = "-2 1 3 ns/op 0 B/op 0 allocs/op\n"
	measure := func(leaves []string) string {
		var output strings.Builder
		for _, leaf := range leaves {
			output.WriteString(benchmark + "/" + leaf + measurement)
		}
		return output.String()
	}
	full, shortened := measure(rows), measure(rows[:len(rows)-1])
	extra := measure([]string{extraLeaf})
	const control = mergeDestinationControl + measurement
	args := []string{"--verify-profile-inventory", mergeDestinationPackage, profile, ordinary, isolated}
	for _, tc := range []struct {
		name, ordinary, isolated string
		valid                    bool
	}{
		{"exact", full, full, true},
		{"missing on both", shortened, shortened, false},
		{"changed on both", shortened + extra, shortened + extra, false},
		{"extra on both", full + extra, full + extra, false},
		{"default benchmark leaks", full, full + control, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFixture(t, ordinary, tc.ordinary+control+"PASS\n")
			writeFixture(t, isolated, tc.isolated+"PASS\n")
			if err := run(args, io.Discard); (err == nil) != tc.valid {
				t.Fatalf("inventory valid=%v: %v", tc.valid, err)
			}
		})
	}
}

func TestMergeDestinationPrimaryAndConfirmationStayExclusive(t *testing.T) {
	verifyLuviaProfileRouting(t, mergeDestinationProfile, mergeDestinationRoot,
		mergeDestinationTarget, "^columnar=true$/^rows=1$/^direct=true$")
}

func TestResolvedFieldsPrimaryAndConfirmationStayExclusive(t *testing.T) {
	verifyLuviaProfileRouting(t, resolvedFieldsProfile, resolvedFieldsRoot,
		"list-small-128x16/per-value", "^list-small-128x16$/^per-value$")
}

func verifyLuviaProfileRouting(t *testing.T, profile, root, target, targetFilter string) {
	t.Helper()
	const pkg = mergeDestinationPackage
	const control = mergeDestinationControl
	primary := formatShard(benchShard{Groups: []benchGroup{{Package: pkg, Name: root}, {Package: pkg, Name: control}}})
	keys := []sampleKey{{pkg, root + "/" + target + "-2"}, {pkg, control + "-2"}}
	confirmation := formatConfirmationBatch(keys)
	data, err := json.Marshal(expectedSamples{Base: keys, Head: keys})
	if err != nil {
		t.Fatal(err)
	}
	dir := benchmarkTempDir(t)
	expected, batch := dir+"/expected.json", dir+"/batch.tsv"
	writeFixture(t, expected, string(data))
	for _, tc := range []struct{ phase, batch, selected, sibling string }{
		{"primary", primary, "^(" + root + ")$", "^(" + control + ")$"},
		{"confirmation", confirmation, "^" + root + "$/" + targetFilter, "^" + control + "$"},
	} {
		t.Run(tc.phase, func(t *testing.T) {
			if !strings.Contains(tc.batch, pkg+"\t"+profile+"\t"+tc.selected+"\n") ||
				!strings.Contains(tc.batch, pkg+"\tdefault\t"+tc.sibling+"\n") {
				t.Fatalf("wrong profile routing:\n%s", tc.batch)
			}
			args := []string{"--verify-profile-batch", tc.phase, expected, batch}
			writeFixture(t, batch, tc.batch)
			if err := run(args, io.Discard); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, batch, strings.Replace(tc.batch, "\t"+profile+"\t", "\tdefault\t", 1))
			if err := run(args, io.Discard); err == nil {
				t.Fatal("accepted the isolated workload in the default profile")
			}
		})
	}
}

func TestCodegenLiteralProfileKeepsExactlyFourLeaves(t *testing.T) {
	const pkg = "pkg/codegen"
	const profile = "codegen-integer-literals"
	const literals = "BenchmarkCompileDecimalIntegerLiterals"
	const other = "BenchmarkGenerateNumericLiteralBindings"
	const measurement = "-2 1 3 ns/op 0 B/op 0 allocs/op\n"
	const ordinaryBenchmark = other + "/plain" + measurement
	const restricted = literals + "/duration_leading_zero" + measurement +
		literals + "/duration_plain" + measurement + literals + "/int_leading_zero" + measurement +
		literals + "/int_plain" + measurement
	const extra = literals + "/unverified" + measurement
	substituted := strings.Replace(restricted, literals+"/duration_plain"+measurement, extra, 1)
	wrongCPU := strings.ReplaceAll(restricted, "-2 1 3", "-4 1 3")
	root := benchmarkTempDir(t)
	ordinary, isolated := root+"/ordinary.log", root+"/isolated.log"
	args := []string{"--verify-profile-inventory", pkg, profile, ordinary, isolated}
	for _, tc := range []struct {
		name, ordinary, isolated string
		valid                    bool
	}{
		{"exact", restricted, restricted, true},
		{"missing on isolated", restricted, literals + "/int_plain" + measurement, false},
		{"extra on both", restricted + extra, restricted + extra, false},
		{"unverified replacement on both", substituted, substituted, false},
		{"wrong CPU suffix", wrongCPU, wrongCPU, false},
		{"unrelated root", restricted, restricted + ordinaryBenchmark, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFixture(t, ordinary, tc.ordinary+ordinaryBenchmark+"PASS\n")
			writeFixture(t, isolated, tc.isolated+"PASS\n")
			if err := run(args, io.Discard); (err == nil) != tc.valid {
				t.Fatalf("literal inventory valid=%v: %v", tc.valid, err)
			}
		})
	}
	primary := formatShard(benchShard{Groups: []benchGroup{{Package: pkg, Name: literals}, {Package: pkg, Name: other}}})
	confirmation := formatConfirmationBatch([]sampleKey{{pkg, literals + "/duration_plain-2"}, {pkg, other + "/plain-2"}})
	for _, tc := range []struct{ batch, selected, sibling string }{
		{primary, "^(" + literals + ")$", "^(" + other + ")$"},
		{confirmation, "^" + literals + "$/^duration_plain$", "^" + other + "$/^plain$"},
	} {
		if !strings.Contains(tc.batch, pkg+"\t"+profile+"\t"+tc.selected+"\n") ||
			!strings.Contains(tc.batch, pkg+"\tdefault\t"+tc.sibling+"\n") {
			t.Fatalf("literal profile routes the wrong benchmarks:\n%s", tc.batch)
		}
	}
}

func TestCodegenReceiverInventoryAndIndependentRouting(t *testing.T) {
	const pkg = "pkg/codegen"
	const profile = "codegen-global-receiver"
	const receiver = "BenchmarkGlobalReceiverName"
	const literal = "BenchmarkCompileDecimalIntegerLiterals"
	const measurement = "-2 1 3 ns/op 0 B/op 0 allocs/op\n"
	const selected = receiver + "/User" + measurement + receiver + "/_Record" + measurement + receiver + "/用户" + measurement
	const literalMeasurement = literal + "/int_plain" + measurement
	const extra = receiver + "/Other" + measurement
	root := benchmarkTempDir(t)
	ordinary, isolated := root+"/ordinary.log", root+"/isolated.log"
	for _, tc := range []struct {
		name, ordinary, isolated string
		valid                    bool
	}{
		{"exact", selected, selected, true},
		{"missing unicode", selected, strings.ReplaceAll(selected, receiver+"/用户"+measurement, ""), false},
		{"same wrong name", strings.ReplaceAll(selected, "用户", "Other"), strings.ReplaceAll(selected, "用户", "Other"), false},
		{"same extra leaf", selected + extra, selected + extra, false},
		{"literal in isolation", selected, selected + literalMeasurement, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFixture(t, ordinary, tc.ordinary+literalMeasurement+"PASS\n")
			writeFixture(t, isolated, tc.isolated+"PASS\n")
			if err := run([]string{"--verify-profile-inventory", pkg, profile, ordinary, isolated}, io.Discard); (err == nil) != tc.valid {
				t.Fatalf("receiver inventory valid=%v: %v", tc.valid, err)
			}
		})
	}
	primary := formatShard(benchShard{Groups: []benchGroup{{Package: pkg, Name: receiver}, {Package: pkg, Name: literal}}})
	confirmation := formatConfirmationBatch([]sampleKey{{pkg, receiver + "/用户-2"}, {pkg, literal + "/int_plain-2"}})
	for _, tc := range []struct{ batch, a, b string }{
		{primary, "^(" + receiver + ")$", "^(" + literal + ")$"},
		{confirmation, "^" + receiver + "$/^用户$", "^" + literal + "$/^int_plain$"},
	} {
		if !strings.Contains(tc.batch, pkg+"\t"+profile+"\t"+tc.a+"\n") ||
			!strings.Contains(tc.batch, pkg+"\tcodegen-integer-literals\t"+tc.b+"\n") {
			t.Fatalf("codegen profiles must go to separate binaries:\n%s", tc.batch)
		}
	}
	expected, batch := root+"/expected.json", root+"/batch.tsv"
	const items = `[{"Package":"pkg/codegen","Name":"BenchmarkGlobalReceiverName/用户-2"},{"Package":"pkg/codegen","Name":"BenchmarkCompileDecimalIntegerLiterals/int_plain-2"}]`
	writeFixture(t, expected, `{"Base":`+items+`,"Head":`+items+"}")
	writeFixture(t, batch, confirmation)
	args := []string{"--verify-profile-batch", "confirmation", expected, batch}
	if err := run(args, io.Discard); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, batch, strings.NewReplacer(profile, "codegen-integer-literals", "codegen-integer-literals", profile).Replace(confirmation))
	if err := run(args, io.Discard); err == nil {
		t.Fatal("accepted swapped codegen profiles")
	}
}

func TestCodegenMigrationStateInventoryRequiresNativeCohort(t *testing.T) {
	const pkg, profile = "pkg/codegen", "codegen-migration-state"
	const migration, receiver = "BenchmarkMigrationStateValidation", "BenchmarkGlobalReceiverName"
	const measurement = "-2 1 3 ns/op 0 B/op 0 allocs/op\n"
	const receiverRows = receiver + "/User" + measurement + receiver + "/_Record" + measurement + receiver + "/用户" + measurement
	const unrelated = "BenchmarkCompileDecimalIntegerLiterals/int_plain" + measurement
	var migrationRows strings.Builder
	for _, size := range []string{"empty", "small", "large"} {
		for _, op := range []string{"validate", "load/reference", "load/strict", "save/reference", "save/strict"} {
			migrationRows.WriteString(migration + "/" + size + "/" + op + measurement)
		}
	}
	cohort := migrationRows.String() + receiverRows
	missingMigration := strings.ReplaceAll(cohort, migration+"/large/save/strict"+measurement, "")
	missingReceiver := strings.ReplaceAll(cohort, receiver+"/用户"+measurement, "")
	work := benchmarkTempDir(t)
	ordinary, isolated := work+"/ordinary.log", work+"/isolated.log"
	for _, tc := range []struct {
		label, ordinary, isolated string
		valid                     bool
	}{
		{"exact cohort", cohort, cohort, true},
		{"migration leaf omitted", missingMigration, missingMigration, false},
		{"receiver leaf omitted", missingReceiver, missingReceiver, false},
		{"receiver root omitted", migrationRows.String(), migrationRows.String(), false},
		{"unexpected isolated leaf", cohort, cohort + unrelated, false},
	} {
		t.Run(tc.label, func(t *testing.T) {
			writeFixture(t, ordinary, tc.ordinary+unrelated+"PASS\n")
			writeFixture(t, isolated, tc.isolated+"PASS\n")
			if err := run([]string{"--verify-profile-inventory", pkg, profile, ordinary, isolated}, io.Discard); (err == nil) != tc.valid {
				t.Fatalf("migration inventory valid=%v: %v", tc.valid, err)
			}
		})
	}
	primary := formatShard(benchShard{Groups: []benchGroup{{Package: pkg, Name: migration}, {Package: pkg, Name: receiver}}})
	confirmation := formatConfirmationBatch([]sampleKey{{pkg, migration + "/small/load/strict-2"}, {pkg, receiver + "/用户-2"}})
	for _, tc := range []struct{ batch, migrationFilter, receiverFilter string }{
		{primary, "^(" + migration + ")$", "^(" + receiver + ")$"},
		{confirmation, "^" + migration + "$/^small$/^load$/^strict$", "^" + receiver + "$/^用户$"},
	} {
		if !strings.Contains(tc.batch, pkg+"\t"+profile+"\t"+tc.migrationFilter+"\n") ||
			!strings.Contains(tc.batch, pkg+"\tcodegen-global-receiver\t"+tc.receiverFilter+"\n") {
			t.Fatalf("migration and receiver must use separate measured binaries:\n%s", tc.batch)
		}
	}
}

func TestLuviaGatewayIdentityHasSixSeparateLeaves(t *testing.T) {
	const pkg, profile = "pkg/lux/luvia", "luvia-gateway-identity"
	const root, primaryKeys = "BenchmarkGatewayIdentity", "BenchmarkPrimaryKeys"
	const measurement = "-2 1 3 ns/op 0 B/op 0 allocs/op\n"
	var six strings.Builder
	for _, kind := range []string{"anonymous", "authenticated", "typed-nil"} {
		for _, stage := range []string{"before", "after"} {
			six.WriteString(root + "/" + kind + "/" + stage + measurement)
		}
	}
	all := six.String()
	missing := strings.ReplaceAll(all, root+"/typed-nil/before"+measurement, "")
	changed := strings.ReplaceAll(all, "/before-2", "/previous-2")
	const trace = primaryKeys + "/rows" + measurement
	dir := benchmarkTempDir(t)
	ordinary, isolated := dir+"/ordinary.log", dir+"/isolated.log"
	for _, tc := range []struct {
		name, ordinary, isolated string
		valid                    bool
	}{
		{"exact", all, all, true},
		{"missing in isolation", all, missing, false},
		{"missing on both", missing, missing, false},
		{"same renamed leaves", changed, changed, false},
		{"primary trace included", all, all + trace, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFixture(t, ordinary, tc.ordinary+trace+"PASS\n")
			writeFixture(t, isolated, tc.isolated+"PASS\n")
			if err := run([]string{"--verify-profile-inventory", pkg, profile, ordinary, isolated}, io.Discard); (err == nil) != tc.valid {
				t.Fatalf("GatewayIdentity inventory valid=%v: %v", tc.valid, err)
			}
		})
	}
	primary := formatShard(benchShard{Groups: []benchGroup{{Package: pkg, Name: root}, {Package: pkg, Name: primaryKeys}}})
	if !strings.Contains(primary, pkg+"\t"+profile+"\t^("+root+")$\n") ||
		!strings.Contains(primary, pkg+"\tluvia-primary-trace\t^("+primaryKeys+")$\n") {
		t.Fatalf("Luvia profiles must go to separate binaries:\n%s", primary)
	}
	required := []sampleKey{{pkg, root + "/authenticated/before-2"}, {pkg, root + "/authenticated/after-2"}, {pkg, primaryKeys + "/rows-2"}}
	confirmation := formatConfirmationBatch(required)
	expected, batch := dir+"/expected.json", dir+"/batch.tsv"
	data, err := json.Marshal(expectedSamples{Base: required, Head: required})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, expected, string(data))
	writeFixture(t, batch, confirmation)
	args := []string{"--verify-profile-batch", "confirmation", expected, batch}
	if err := run(args, io.Discard); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, batch, strings.NewReplacer(profile, "luvia-primary-trace", "luvia-primary-trace", profile).Replace(confirmation))
	if err := run(args, io.Discard); err == nil {
		t.Fatal("accepted swapped Luvia profiles")
	}
}

func TestLuxShortPathPreservesAllNativeLeaves(t *testing.T) {
	const pkg, profile = "pkg/lux", "lux-short-path"
	const strict, decimal, sibling = "BenchmarkStrictFilterContract", "BenchmarkDecimalAlignment", "BenchmarkFormatDecimal"
	const measurement = "-2 1 3 ns/op 0 B/op 0 allocs/op\n"
	var selected strings.Builder
	for _, kind := range []string{"Bool", "Contains", "Float", "Int", "Search", "String", "String1KiB", "Time"} {
		for _, stage := range []string{"Before", "After"} {
			selected.WriteString(strict + "/" + kind + "/" + stage + measurement)
		}
	}
	for _, op := range []string{"Add", "Sub"} {
		for _, mode := range []string{"near", "same", "zero"} {
			for _, stage := range []string{"before", "after"} {
				selected.WriteString(decimal + "/" + op + "/" + mode + "/" + stage + measurement)
			}
		}
	}
	all := selected.String()
	missingStrict := strings.ReplaceAll(all, strict+"/Time/After"+measurement, "")
	missingDecimal := strings.ReplaceAll(all, decimal+"/Add/zero/after"+measurement, "")
	const untouched = sibling + "/example" + measurement
	dir := benchmarkTempDir(t)
	ordinary, isolated := dir+"/ordinary.log", dir+"/isolated.log"
	for _, tc := range []struct {
		name, normal, projected string
		valid                   bool
	}{
		{"exact", all, all, true},
		{"missing strict", missingStrict, missingStrict, false},
		{"missing decimal", missingDecimal, missingDecimal, false},
		{"sibling joined isolation", all, all + untouched, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFixture(t, ordinary, tc.normal+untouched+"PASS\n")
			writeFixture(t, isolated, tc.projected+"PASS\n")
			if err := run([]string{"--verify-profile-inventory", pkg, profile, ordinary, isolated}, io.Discard); (err == nil) != tc.valid {
				t.Fatalf("Lux short-path inventory valid=%v: %v", tc.valid, err)
			}
		})
	}
	confirmation := formatConfirmationBatch([]sampleKey{{pkg, strict + "/Time/After-2"}, {pkg, decimal + "/Add/zero/after-2"}, {pkg, sibling + "/example-2"}})
	for _, route := range []string{
		pkg + "\t" + profile + "\t^" + strict + "$/^Time$/^After$|^" + decimal + "$/^Add$/^zero$/^after$\n",
		pkg + "\tdefault\t^" + sibling + "$/^example$\n",
	} {
		if !strings.Contains(confirmation, route) {
			t.Fatalf("Lux confirmation did not preserve route %q in:\n%s", route, confirmation)
		}
	}
}
