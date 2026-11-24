package main

import (
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
