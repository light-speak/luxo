package benchlayout

import (
	"slices"
	"strings"
)

const (
	LuxShortPathProfile           = "lux-short-path"
	LuxPackage                    = "pkg/lux"
	LuxStrictFilterBenchmark      = "BenchmarkStrictFilterContract"
	LuxDecimalAlignmentBenchmark  = "BenchmarkDecimalAlignment"
	DefaultProfile                = "default"
	CodecReaderProfile            = "codec-reader"
	APINonIdentityProfile         = "api-non-identity"
	LuviaPrimaryTraceProfile      = "luvia-primary-trace"
	LuviaGatewayIdentityProfile   = "luvia-gateway-identity"
	CodegenLiteralProfile         = "codegen-integer-literals"
	CodegenReceiverProfile        = "codegen-global-receiver"
	CodecPackage                  = "pkg/lux/codec"
	CodecReaderBenchmark          = "BenchmarkElementPayload"
	APIPackage                    = "pkg/lux/api"
	LuviaPackage                  = "pkg/lux/luvia"
	LuviaGatewayIdentityBenchmark = "BenchmarkGatewayIdentity"
	CodegenPackage                = "pkg/codegen"
	CodegenLiteralBenchmark       = "BenchmarkCompileDecimalIntegerLiterals"
	CodegenReceiverBenchmark      = "BenchmarkGlobalReceiverName"

	CodegenMigrationStateProfile   = "codegen-migration-state"
	CodegenMigrationStateBenchmark = "BenchmarkMigrationStateValidation"

	LuviaMergeDestinationProfile   = "luvia-merge-destination"
	LuviaMergeDestinationBenchmark = "BenchmarkMergeDestination"
)

var apiIdentityRoots = []string{
	"BenchmarkIdentityConditionValidity", "BenchmarkIdentityEnumArray", "BenchmarkIdentitySubjectVisibility",
}

var luviaIsolatedRoots = []string{"BenchmarkPrimaryKeys", "BenchmarkTraceEncodingComparison"}

// ProfileForBenchmark returns the binary profile for an inventory root or leaf.
func ProfileForBenchmark(pkg, name string) string {
	root, _, _ := strings.Cut(strings.TrimSuffix(name, "-2"), "/")
	switch pkg {
	case LuxPackage:
		if root == LuxStrictFilterBenchmark || root == LuxDecimalAlignmentBenchmark {
			return LuxShortPathProfile
		}
	case CodecPackage:
		if root == CodecReaderBenchmark {
			return CodecReaderProfile
		}
	case APIPackage:
		if !slices.Contains(apiIdentityRoots, root) {
			return APINonIdentityProfile
		}
	case LuviaPackage:
		switch root {
		case LuviaGatewayIdentityBenchmark:
			return LuviaGatewayIdentityProfile
		case LuviaMergeDestinationBenchmark:
			return LuviaMergeDestinationProfile
		}
		if slices.Contains(luviaIsolatedRoots, root) {
			return LuviaPrimaryTraceProfile
		}
	case CodegenPackage:
		switch root {
		case CodegenLiteralBenchmark:
			return CodegenLiteralProfile
		case CodegenReceiverBenchmark:
			return CodegenReceiverProfile
		case CodegenMigrationStateBenchmark:
			return CodegenMigrationStateProfile
		}
	}
	return DefaultProfile
}

// IsolationSpec pairs a benchmark compilation profile with its discovery filter.
type IsolationSpec struct {
	Name, Filter string
}

// IsolationProfiles gives discovery the isolated binaries for a package.
func IsolationProfiles(pkg string) []IsolationSpec {
	switch pkg {
	case LuxPackage:
		return []IsolationSpec{{LuxShortPathProfile, "^" + LuxStrictFilterBenchmark + "$|^" + LuxDecimalAlignmentBenchmark + "$"}}
	case CodecPackage:
		return []IsolationSpec{{CodecReaderProfile, "."}}
	case APIPackage:
		return []IsolationSpec{{APINonIdentityProfile, "."}}
	case LuviaPackage:
		return []IsolationSpec{
			{LuviaPrimaryTraceProfile, "^" + strings.Join(luviaIsolatedRoots, "$|^") + "$"},
			{LuviaGatewayIdentityProfile, "^" + LuviaGatewayIdentityBenchmark + "$"},
			{LuviaMergeDestinationProfile, "^" + LuviaMergeDestinationBenchmark + "$"},
		}
	case CodegenPackage:
		return []IsolationSpec{
			{CodegenLiteralProfile, "^" + CodegenLiteralBenchmark + "$"},
			{CodegenReceiverProfile, "^" + CodegenReceiverBenchmark + "$"},
			{CodegenMigrationStateProfile, "^" + CodegenReceiverBenchmark + "$|^" + CodegenMigrationStateBenchmark + "$"},
		}
	default:
		return nil
	}
}

func ValidPerformanceProfile(pkg, profile string) bool {
	if profile == DefaultProfile {
		return true
	}
	return slices.ContainsFunc(IsolationProfiles(pkg), func(spec IsolationSpec) bool {
		return spec.Name == profile
	})
}
