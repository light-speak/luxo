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
	LuviaAuthMiddlewareProfile    = "luvia-auth-middleware"
	CodegenLiteralProfile         = "codegen-integer-literals"
	CodegenReceiverProfile        = "codegen-global-receiver"
	CodecPackage                  = "pkg/lux/codec"
	RPCPackage                    = "pkg/lux/rpc"
	RPCOwnedRequestProfile        = "rpc-owned-request"
	RPCOwnedRequestBenchmark      = "BenchmarkLRPCOwnedRequest"
	RPCDeadlineProfile            = "rpc-deadline-classification"
	RPCDeadlineBenchmark          = "BenchmarkRPCDeadlineClassification"
	RPCNilContextGuardProfile     = "rpc-nil-context-guard"
	RPCNilContextGuardBenchmark   = "BenchmarkRPCNilContextGuard"
	CodecReaderBenchmark          = "BenchmarkElementPayload"
	APIPackage                    = "pkg/lux/api"
	LuviaPackage                  = "pkg/lux/luvia"
	LuviaGatewayIdentityBenchmark = "BenchmarkGatewayIdentity"
	LuviaAuthMiddlewareBenchmark  = "BenchmarkAuthMiddlewareIsolation"
	CodegenPackage                = "pkg/codegen"
	CodegenLiteralBenchmark       = "BenchmarkCompileDecimalIntegerLiterals"
	CodegenReceiverBenchmark      = "BenchmarkGlobalReceiverName"

	CodegenMigrationStateProfile   = "codegen-migration-state"
	CodegenMigrationStateBenchmark = "BenchmarkMigrationStateValidation"

	APILocalRouteExportProfile      = "api-local-route-export"
	APILocalRouteExportBenchmark    = "BenchmarkLocalRouteExport"
	APICanonicalJSONProfile         = "api-canonical-json"
	APIBinaryParamLookupProfile     = "api-binary-param-lookup"
	APIBinaryParamLookupBenchmark   = "BenchmarkBinaryParamLookup"
	APIRequestLogProfile            = "api-request-log"
	APIRequestLogBoundaryBenchmark  = "BenchmarkRequestLogBoundary"
	APIRequestLogDeliveredBenchmark = "BenchmarkRequestLogDelivered"

	LuviaMergeDestinationProfile   = "luvia-merge-destination"
	LuviaMergeDestinationBenchmark = "BenchmarkMergeDestination"

	LuviaResolvedFieldsProfile   = "luvia-resolved-fields"
	LuviaResolvedFieldsBenchmark = "BenchmarkValidateResolvedFields"

	LuviaConvenienceKeysProfile   = "luvia-convenience-keys"
	LuviaConvenienceKeysBenchmark = "BenchmarkConvenienceKeyParentValidation"
)

var apiIdentityRoots = []string{
	"BenchmarkIdentityConditionValidity", "BenchmarkIdentityEnumArray", "BenchmarkIdentitySubjectVisibility",
}

const apiCanonicalJSONPrefix = "BenchmarkCanonicalJSON"

var apiCanonicalJSONFamilies = []string{"Structured", "Typed", "Bytes", "Float", "Value", "Integer"}

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
	case RPCPackage:
		switch root {
		case RPCOwnedRequestBenchmark:
			return RPCOwnedRequestProfile
		case RPCDeadlineBenchmark:
			return RPCDeadlineProfile
		case RPCNilContextGuardBenchmark:
			return RPCNilContextGuardProfile
		}
	case APIPackage:
		return apiBenchmarkProfile(root)
	case LuviaPackage:
		switch root {
		case LuviaGatewayIdentityBenchmark:
			return LuviaGatewayIdentityProfile
		case LuviaAuthMiddlewareBenchmark:
			return LuviaAuthMiddlewareProfile
		case LuviaMergeDestinationBenchmark:
			return LuviaMergeDestinationProfile
		case LuviaResolvedFieldsBenchmark:
			return LuviaResolvedFieldsProfile
		case LuviaConvenienceKeysBenchmark:
			return LuviaConvenienceKeysProfile
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

func apiBenchmarkProfile(root string) string {
	if root == APIRequestLogBoundaryBenchmark || root == APIRequestLogDeliveredBenchmark {
		return APIRequestLogProfile
	}
	if root == APIBinaryParamLookupBenchmark {
		return APIBinaryParamLookupProfile
	}
	if root == APILocalRouteExportBenchmark {
		return APILocalRouteExportProfile
	}
	if family, ok := strings.CutPrefix(root, apiCanonicalJSONPrefix); ok && slices.Contains(apiCanonicalJSONFamilies, family) {
		return APICanonicalJSONProfile
	}
	if !slices.Contains(apiIdentityRoots, root) {
		return APINonIdentityProfile
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
	case RPCPackage:
		return []IsolationSpec{
			{RPCOwnedRequestProfile, "^" + RPCOwnedRequestBenchmark + "$"},
			{RPCDeadlineProfile, "^" + RPCDeadlineBenchmark + "$"},
			{RPCNilContextGuardProfile, "^" + RPCNilContextGuardBenchmark + "$"},
		}
	case APIPackage:
		return []IsolationSpec{
			{APINonIdentityProfile, "."},
			{APILocalRouteExportProfile, "^" + APILocalRouteExportBenchmark + "$"},
			{APICanonicalJSONProfile, "^" + apiCanonicalJSONPrefix + "(" + strings.Join(apiCanonicalJSONFamilies, "|") + ")$"},
			{APIBinaryParamLookupProfile, "^" + APIBinaryParamLookupBenchmark + "$"},
			{APIRequestLogProfile, "^" + APIRequestLogBoundaryBenchmark + "$|^" + APIRequestLogDeliveredBenchmark + "$"},
		}
	case LuviaPackage:
		return []IsolationSpec{
			{LuviaPrimaryTraceProfile, "^" + strings.Join(luviaIsolatedRoots, "$|^") + "$"},
			{LuviaGatewayIdentityProfile, "^" + LuviaGatewayIdentityBenchmark + "$"},
			{LuviaAuthMiddlewareProfile, "^" + LuviaAuthMiddlewareBenchmark + "$"},
			{LuviaMergeDestinationProfile, "^" + LuviaMergeDestinationBenchmark + "$"},
			{LuviaResolvedFieldsProfile, "^" + LuviaResolvedFieldsBenchmark + "$"},
			{LuviaConvenienceKeysProfile, "^" + LuviaConvenienceKeysBenchmark + "$"},
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
