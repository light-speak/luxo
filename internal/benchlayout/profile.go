package benchlayout

import (
	"slices"
	"strings"
)

const (
	DefaultProfile           = "default"
	CodecReaderProfile       = "codec-reader"
	APINonIdentityProfile    = "api-non-identity"
	LuviaPrimaryTraceProfile = "luvia-primary-trace"
	CodegenLiteralProfile    = "codegen-integer-literals"
	CodecPackage             = "pkg/lux/codec"
	CodecReaderBenchmark     = "BenchmarkElementPayload"
	APIPackage               = "pkg/lux/api"
	LuviaPackage             = "pkg/lux/luvia"
	CodegenPackage           = "pkg/codegen"
	CodegenLiteralBenchmark  = "BenchmarkCompileDecimalIntegerLiterals"
)

var apiIdentityRoots = []string{
	"BenchmarkIdentityConditionValidity", "BenchmarkIdentityEnumArray", "BenchmarkIdentitySubjectVisibility",
}

var luviaIsolatedRoots = []string{"BenchmarkPrimaryKeys", "BenchmarkTraceEncodingComparison"}

// ProfileForBenchmark returns the binary profile for an inventory root or leaf.
func ProfileForBenchmark(pkg, name string) string {
	root, _, _ := strings.Cut(strings.TrimSuffix(name, "-2"), "/")
	switch pkg {
	case CodecPackage:
		if root == CodecReaderBenchmark {
			return CodecReaderProfile
		}
	case APIPackage:
		if !slices.Contains(apiIdentityRoots, root) {
			return APINonIdentityProfile
		}
	case LuviaPackage:
		if slices.Contains(luviaIsolatedRoots, root) {
			return LuviaPrimaryTraceProfile
		}
	case CodegenPackage:
		if root == CodegenLiteralBenchmark {
			return CodegenLiteralProfile
		}
	}
	return DefaultProfile
}

// IsolationProfile gives discovery the profile and exact root filter for a package.
func IsolationProfile(pkg string) (profile, filter string) {
	switch pkg {
	case CodecPackage:
		return CodecReaderProfile, "."
	case APIPackage:
		return APINonIdentityProfile, "."
	case LuviaPackage:
		return LuviaPrimaryTraceProfile, "^" + strings.Join(luviaIsolatedRoots, "$|^") + "$"
	case CodegenPackage:
		return CodegenLiteralProfile, "^" + CodegenLiteralBenchmark + "$"
	default:
		return "", ""
	}
}

func ValidPerformanceProfile(pkg, profile string) bool {
	isolation, _ := IsolationProfile(pkg)
	return profile == DefaultProfile || (isolation != "" && profile == isolation)
}
