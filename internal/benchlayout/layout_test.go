package benchlayout

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSplitPackagesPreserveLogicalIdentity(t *testing.T) {
	root := layoutFixture(t)
	for _, path := range []string{"pkg/lux/cache", "pkg/lux/cache/redis"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Packages(root, "pkg/lux/cache")
	if err != nil || !reflect.DeepEqual(got, []string{"pkg/lux/cache", "pkg/lux/cache/redis"}) {
		t.Fatalf("%v: %v", got, err)
	}
	if Canonical("pkg/lux/cache/redis") != "pkg/lux/cache" || Canonical("pkg/parser") != "pkg/parser" {
		t.Fatal("invalid canonical mapping")
	}
	if _, err := Packages(root, "../outside"); err == nil {
		t.Fatal("accepted path escape")
	}
	if _, err := Packages(root, "pkg/missing"); err == nil {
		t.Fatal("accepted absent package")
	}
}

func TestPackagesFilesystemFailures(t *testing.T) {
	root := layoutFixture(t)
	if _, err := Packages(filepath.Join(root, "missing"), "pkg/parser"); !os.IsNotExist(err) {
		t.Fatalf("missing root: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "pkg"), 0755); err != nil {
		t.Fatal(err)
	}
	// A self-referencing link reliably produces a non-ENOENT stat error, even
	// when CI runs as root; chmod-based permission tests would not do that.
	loop := filepath.Join(root, "pkg", "loop")
	if err := os.Symlink("loop", loop); err != nil {
		t.Fatal(err)
	}
	if _, err := Packages(root, "pkg/loop"); err == nil || os.IsNotExist(err) {
		t.Fatalf("stat error was swallowed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "file"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Packages(root, "pkg/file"); err == nil {
		t.Fatal("regular file accepted as package directory")
	}
}

func layoutFixture(t *testing.T) string {
	t.Helper()
	if err := os.MkdirAll("../../.tmp", 0755); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("../../.tmp", "bench-layout-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	return root
}

func TestCodecReaderProfileRequiresPackageAndExactRoot(t *testing.T) {
	for _, tc := range []struct{ pkg, name, want string }{
		{CodecPackage, CodecReaderBenchmark, CodecReaderProfile},
		{CodecPackage, CodecReaderBenchmark + "/x-2", CodecReaderProfile},
		{CodecPackage, "BenchmarkMissingElementPayload", DefaultProfile},
		{"pkg/lux/schema", CodecReaderBenchmark, DefaultProfile},
	} {
		if got := ProfileForBenchmark(tc.pkg, tc.name); got != tc.want || !ValidPerformanceProfile(tc.pkg, got) {
			t.Fatalf("%s/%s profile %q, want %q", tc.pkg, tc.name, got, tc.want)
		}
	}
	if ValidPerformanceProfile("pkg/lux/rpc", CodecReaderProfile) || ValidPerformanceProfile(CodecPackage, "bogus") {
		t.Fatal("accepted a profile outside its package or registry")
	}
}

func TestAPIAndLuviaProfilesPreserveTheirOtherReaders(t *testing.T) {
	for _, tc := range []struct{ pkg, name, want string }{
		{APIPackage, "BenchmarkIdentityConditionValidity/member/EnumChecked-2", DefaultProfile},
		{APIPackage, "BenchmarkIdentityEnumArray", DefaultProfile},
		{APIPackage, "BenchmarkIdentitySubjectVisibility", DefaultProfile},
		{APIPackage, "BenchmarkIdentitySurface", APINonIdentityProfile},
		{APIPackage, "BenchmarkCanonicalJSONBytes", APINonIdentityProfile},
		{LuviaPackage, "BenchmarkPrimaryKeys/row-small/keyLast=false/baseline=true-2", LuviaPrimaryTraceProfile},
		{LuviaPackage, "BenchmarkTraceEncodingComparison", LuviaPrimaryTraceProfile},
		{LuviaPackage, "BenchmarkStaticMetricsJSON", DefaultProfile},
		{LuviaPackage, "BenchmarkConvenienceKeyParentValidation", DefaultProfile},
	} {
		if got := ProfileForBenchmark(tc.pkg, tc.name); got != tc.want || !ValidPerformanceProfile(tc.pkg, got) {
			t.Fatalf("%s/%s profile %q, want %q", tc.pkg, tc.name, got, tc.want)
		}
	}
	for _, tc := range []struct{ pkg, profile, filter string }{
		{APIPackage, APINonIdentityProfile, "."},
		{LuviaPackage, LuviaPrimaryTraceProfile, "^BenchmarkPrimaryKeys$|^BenchmarkTraceEncodingComparison$"},
		{CodecPackage, CodecReaderProfile, "."},
		{"pkg/lux/rpc", "", ""},
	} {
		got := IsolationProfiles(tc.pkg)
		if tc.profile == "" {
			if len(got) != 0 {
				t.Fatalf("unexpected isolation for %s: %v", tc.pkg, got)
			}
			continue
		}
		if len(got) != 1 || got[0] != (IsolationSpec{tc.profile, tc.filter}) {
			t.Fatalf("isolation of %s = %v, want %q %q", tc.pkg, got, tc.profile, tc.filter)
		}
	}
}

func TestCodegenProfilesRequirePackageAndExactRoots(t *testing.T) {
	for _, tc := range []struct{ pkg, name, want string }{
		{CodegenPackage, CodegenLiteralBenchmark, CodegenLiteralProfile},
		{CodegenPackage, CodegenLiteralBenchmark + "/duration_plain-2", CodegenLiteralProfile},
		{CodegenPackage, CodegenReceiverBenchmark, CodegenReceiverProfile},
		{CodegenPackage, CodegenReceiverBenchmark + "/用户-2", CodegenReceiverProfile},
		{CodegenPackage, CodegenReceiverBenchmark + "Sibling-2", DefaultProfile},
		{CodegenPackage, "BenchmarkGenerateNumericLiteralBindings/plain-2", DefaultProfile},
		{CodecPackage, CodegenLiteralBenchmark, DefaultProfile},
		{CodecPackage, CodegenReceiverBenchmark, DefaultProfile},
	} {
		if got := ProfileForBenchmark(tc.pkg, tc.name); got != tc.want || !ValidPerformanceProfile(tc.pkg, got) {
			t.Fatalf("%s/%s profile %q, want %q", tc.pkg, tc.name, got, tc.want)
		}
	}
	profiles := IsolationProfiles(CodegenPackage)
	if len(profiles) != 2 || profiles[0] != (IsolationSpec{CodegenLiteralProfile, "^" + CodegenLiteralBenchmark + "$"}) ||
		profiles[1] != (IsolationSpec{CodegenReceiverProfile, "^" + CodegenReceiverBenchmark + "$"}) ||
		ValidPerformanceProfile(CodecPackage, CodegenLiteralProfile) ||
		ValidPerformanceProfile(CodecPackage, CodegenReceiverProfile) {
		t.Fatalf("unexpected codegen isolation: %v", profiles)
	}
}
