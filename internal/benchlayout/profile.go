package benchlayout

import (
	"slices"
	"strings"
)

const (
	RPCFrameReuseProfile         = "rpc-frame-reuse"
	RPCFrameReuseBenchmark       = "BenchmarkLRPCConnectionFrameReuseCandidate"
	PGPackage                    = "pkg/lux/pg"
	PGDurationWriteRejectProfile = "pg-duration-write-reject"
	PGDurationWriteReject        = "BenchmarkDurationWriteReject"
	APIStaticValueParamsProfile  = "api-static-value-params"
	APIStaticValueParams         = "BenchmarkStaticValueParams"
)

const (
	LuxShortPathProfile                = "lux-short-path"
	LuxPackage                         = "pkg/lux"
	LuxStrictFilterBenchmark           = "BenchmarkStrictFilterContract"
	LuxDecimalAlignmentBenchmark       = "BenchmarkDecimalAlignment"
	DefaultProfile                     = "default"
	CodecReaderProfile                 = "codec-reader"
	APINonIdentityProfile              = "api-non-identity"
	LuviaPrimaryTraceProfile           = "luvia-primary-trace"
	LuviaGatewayIdentityProfile        = "luvia-gateway-identity"
	LuviaAuthMiddlewareProfile         = "luvia-auth-middleware"
	CodegenLiteralProfile              = "codegen-integer-literals"
	CodegenReceiverProfile             = "codegen-global-receiver"
	CodecPackage                       = "pkg/lux/codec"
	RPCPackage                         = "pkg/lux/rpc"
	RPCOwnedRequestProfile             = "rpc-owned-request"
	RPCOwnedRequestBenchmark           = "BenchmarkLRPCOwnedRequest"
	RPCDeadlineProfile                 = "rpc-deadline-classification"
	RPCDeadlineBenchmark               = "BenchmarkRPCDeadlineClassification"
	RPCNilContextGuardProfile          = "rpc-nil-context-guard"
	RPCNilContextGuardBenchmark        = "BenchmarkRPCNilContextGuard"
	RPCPreparedFrameProfile            = "rpc-prepared-frame"
	RPCPreparedFrameBenchmark          = "BenchmarkLRPCPreparedFrame"
	RPCHandlerPanicProfile             = "rpc-handler-panic"
	RPCHandlerPanicBenchmark           = "BenchmarkHandlerPanicFormatting"
	RPCPoolReturnRejectionProfile      = "rpc-pool-return-rejection"
	RPCPoolReturnRejectionBenchmark    = "BenchmarkRPCPoolReturnRejection"
	H2TransportPackage                 = "pkg/lux/h2transport"
	H2TaskDeadlineReplacementProfile   = "h2-task-deadline-replacement"
	H2TaskDeadlineReplacementBenchmark = "BenchmarkTaskDeadlineReplacement"
	QueuePackage                       = "pkg/lux/queue"
	NATSQueuePackage                   = "pkg/lux/nats/queue"
	QueueStreamNameProfile             = "queue-stream-name"
	QueueStreamNameBenchmark           = "BenchmarkQueueStreamName"
	CodecReaderBenchmark               = "BenchmarkElementPayload"
	APIPackage                         = "pkg/lux/api"
	LuviaPackage                       = "pkg/lux/luvia"
	LuviaGatewayIdentityBenchmark      = "BenchmarkGatewayIdentity"
	LuviaAuthMiddlewareBenchmark       = "BenchmarkAuthMiddlewareIsolation"
	CodegenPackage                     = "pkg/codegen"
	CodegenLiteralBenchmark            = "BenchmarkCompileDecimalIntegerLiterals"
	CodegenReceiverBenchmark           = "BenchmarkGlobalReceiverName"

	CodegenMigrationStateProfile   = "codegen-migration-state"
	CodegenMigrationStateBenchmark = "BenchmarkMigrationStateValidation"

	CodegenRelationLoaderProfile   = "codegen-relation-loader"
	CodegenRelationLoaderBenchmark = "BenchmarkRelationLoaderGeneration"

	SemanticPackage             = "pkg/semantic"
	SemanticEventEmitsProfile   = "semantic-event-emits"
	SemanticEventEmitsBenchmark = "BenchmarkCollectEventEmits"

	CodegenFindReadProfile   = "codegen-find-read"
	CodegenFindReadBenchmark = "BenchmarkFindRead"

	CodegenNumericRangeProfile   = "codegen-numeric-range"
	CodegenNumericRangeBenchmark = "BenchmarkNumericRangeValidation"

	SemanticEventSnapshotProfile   = "semantic-event-snapshot"
	SemanticEventSnapshotBenchmark = "BenchmarkEventSnapshotContract"

	APILocalRouteExportProfile      = "api-local-route-export"
	APILocalRouteExportBenchmark    = "BenchmarkLocalRouteExport"
	APICanonicalJSONProfile         = "api-canonical-json"
	APIBinaryParamLookupProfile     = "api-binary-param-lookup"
	APIBinaryParamLookupBenchmark   = "BenchmarkBinaryParamLookup"
	APIRequestLogProfile            = "api-request-log"
	APIRequestLogBoundaryBenchmark  = "BenchmarkRequestLogBoundary"
	APIRequestLogDeliveredBenchmark = "BenchmarkRequestLogDelivered"

	APIIdentityConditionProfile        = "api-identity-condition"
	APIIdentityConditionBenchmark      = "BenchmarkIdentityConditionValidity"
	APIIdentitySubjectBenchmark        = "BenchmarkIdentitySubjectVisibility"
	APIHandlerPanicProfile             = "api-handler-panic"
	APIHandlerPanicBenchmark           = "BenchmarkHandlerPanicFormatting"
	APIHandlerResponseBufferBenchmark  = "BenchmarkHandlerResponseBuffer"
	APIWebSocketRegistrationsProfile   = "api-websocket-registrations"
	APIWebSocketRegistrationsBenchmark = "BenchmarkWSConnectionRegistrations"
	APIResponseOriginRunProfile        = "api-response-origin-run"
	APIResponseOriginRunBenchmark      = "BenchmarkResponseOriginRun"
	APIBodyReadProfile                 = "api-body-read"
	APIBodyReadBenchmark               = "BenchmarkBodyRead"
	APIJSONQuoteCandidateProfile       = "api-json-quote-candidate"
	APIJSONQuoteCandidateBenchmark     = "BenchmarkJSONQuoteCandidate"
	APIJSONListMetadataProfile         = "api-json-list-metadata"
	APIJSONListMetadataBenchmark       = "BenchmarkJSONListMetadata"

	LuviaMergeDestinationProfile   = "luvia-merge-destination"
	LuviaMergeDestinationBenchmark = "BenchmarkMergeDestination"

	LuviaResolvedFieldsProfile   = "luvia-resolved-fields"
	LuviaResolvedFieldsBenchmark = "BenchmarkValidateResolvedFields"

	LuviaConvenienceKeysProfile   = "luvia-convenience-keys"
	LuviaConvenienceKeysBenchmark = "BenchmarkConvenienceKeyParentValidation"
)

var apiIdentityRoots = []string{
	APIIdentityConditionBenchmark, "BenchmarkIdentityEnumArray", APIIdentitySubjectBenchmark,
}

// IsAPIIdentityBenchmark reports whether the broad API binary must omit the root.
func IsAPIIdentityBenchmark(root string) bool {
	return slices.Contains(apiIdentityRoots, root)
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
	case PGPackage:
		if root == PGDurationWriteReject {
			return PGDurationWriteRejectProfile
		}
	case RPCPackage:
		return rpcBenchmarkProfile(root)
	case H2TransportPackage:
		if root == H2TaskDeadlineReplacementBenchmark {
			return H2TaskDeadlineReplacementProfile
		}
	case QueuePackage:
		if root == QueueStreamNameBenchmark {
			return QueueStreamNameProfile
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
		return codegenBenchmarkProfile(root)
	case SemanticPackage:
		switch root {
		case SemanticEventEmitsBenchmark:
			return SemanticEventEmitsProfile
		case SemanticEventSnapshotBenchmark:
			return SemanticEventSnapshotProfile
		}
	}
	return DefaultProfile
}

func codegenBenchmarkProfile(root string) string {
	switch root {
	case CodegenLiteralBenchmark:
		return CodegenLiteralProfile
	case CodegenReceiverBenchmark:
		return CodegenReceiverProfile
	case CodegenMigrationStateBenchmark:
		return CodegenMigrationStateProfile
	case CodegenRelationLoaderBenchmark:
		return CodegenRelationLoaderProfile
	case CodegenFindReadBenchmark:
		return CodegenFindReadProfile
	case CodegenNumericRangeBenchmark:
		return CodegenNumericRangeProfile
	default:
		return DefaultProfile
	}
}

func apiBenchmarkProfile(root string) string {
	switch root {
	case APIIdentityConditionBenchmark, APIIdentitySubjectBenchmark:
		return APIIdentityConditionProfile
	case APIHandlerPanicBenchmark, APIHandlerResponseBufferBenchmark:
		return APIHandlerPanicProfile
	case APIWebSocketRegistrationsBenchmark:
		return APIWebSocketRegistrationsProfile
	case APIResponseOriginRunBenchmark:
		return APIResponseOriginRunProfile
	case APIBodyReadBenchmark:
		return APIBodyReadProfile
	case APIJSONQuoteCandidateBenchmark:
		return APIJSONQuoteCandidateProfile
	case APIJSONListMetadataBenchmark:
		return APIJSONListMetadataProfile
	}
	if root == APIRequestLogBoundaryBenchmark || root == APIRequestLogDeliveredBenchmark {
		return APIRequestLogProfile
	}
	if root == APIBinaryParamLookupBenchmark {
		return APIBinaryParamLookupProfile
	}
	if root == APIStaticValueParams {
		return APIStaticValueParamsProfile
	}
	if root == APILocalRouteExportBenchmark {
		return APILocalRouteExportProfile
	}
	if family, ok := strings.CutPrefix(root, apiCanonicalJSONPrefix); ok && slices.Contains(apiCanonicalJSONFamilies, family) {
		return APICanonicalJSONProfile
	}
	if !IsAPIIdentityBenchmark(root) {
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
	case PGPackage:
		return []IsolationSpec{{PGDurationWriteRejectProfile, "^" + PGDurationWriteReject + "$"}}
	case RPCPackage:
		return []IsolationSpec{
			{RPCOwnedRequestProfile, "^" + RPCOwnedRequestBenchmark + "$"},
			{RPCDeadlineProfile, "^" + RPCDeadlineBenchmark + "$"},
			{RPCNilContextGuardProfile, "^" + RPCNilContextGuardBenchmark + "$"},
			{RPCPreparedFrameProfile, "^" + RPCPreparedFrameBenchmark + "$"},
			{RPCFrameReuseProfile, "^" + RPCFrameReuseBenchmark + "$"},
			{RPCHandlerPanicProfile, "^" + RPCHandlerPanicBenchmark + "$"},
			{RPCPoolReturnRejectionProfile, "^" + RPCPoolReturnRejectionBenchmark + "$"},
		}
	case H2TransportPackage:
		return []IsolationSpec{{H2TaskDeadlineReplacementProfile, "^" + H2TaskDeadlineReplacementBenchmark + "$"}}
	case QueuePackage:
		return []IsolationSpec{{QueueStreamNameProfile, "^" + QueueStreamNameBenchmark + "$"}}
	case APIPackage:
		return []IsolationSpec{
			{APINonIdentityProfile, "."},
			{APILocalRouteExportProfile, "^" + APILocalRouteExportBenchmark + "$"},
			{APIStaticValueParamsProfile, "^" + APIStaticValueParams + "$"},
			{APICanonicalJSONProfile, "^" + apiCanonicalJSONPrefix + "(" + strings.Join(apiCanonicalJSONFamilies, "|") + ")$"},
			{APIBinaryParamLookupProfile, "^" + APIBinaryParamLookupBenchmark + "$"},
			{APIRequestLogProfile, "^" + APIRequestLogBoundaryBenchmark + "$|^" + APIRequestLogDeliveredBenchmark + "$"},
			{APIIdentityConditionProfile, "^" + APIIdentityConditionBenchmark + "$|^" + APIIdentitySubjectBenchmark + "$"},
			{APIHandlerPanicProfile, "^" + APIHandlerPanicBenchmark + "$|^" + APIHandlerResponseBufferBenchmark + "$"},
			{APIWebSocketRegistrationsProfile, "^" + APIWebSocketRegistrationsBenchmark + "$"},
			{APIResponseOriginRunProfile, "^" + APIResponseOriginRunBenchmark + "$"},
			{APIBodyReadProfile, "^" + APIBodyReadBenchmark + "$"},
			{APIJSONQuoteCandidateProfile, "^" + APIJSONQuoteCandidateBenchmark + "$"},
			{APIJSONListMetadataProfile, "^" + APIJSONListMetadataBenchmark + "$"},
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
			{CodegenRelationLoaderProfile, "^" + CodegenRelationLoaderBenchmark + "$"},
			{CodegenFindReadProfile, "^" + CodegenFindReadBenchmark + "$"},
			{CodegenNumericRangeProfile, "^" + CodegenNumericRangeBenchmark + "$"},
		}
	case SemanticPackage:
		return []IsolationSpec{{SemanticEventEmitsProfile, "^" + SemanticEventEmitsBenchmark + "$"}, {SemanticEventSnapshotProfile, "^" + SemanticEventSnapshotBenchmark + "$"}}
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

func rpcBenchmarkProfile(root string) string {
	switch root {
	case RPCOwnedRequestBenchmark:
		return RPCOwnedRequestProfile
	case RPCDeadlineBenchmark:
		return RPCDeadlineProfile
	case RPCNilContextGuardBenchmark:
		return RPCNilContextGuardProfile
	case RPCPreparedFrameBenchmark:
		return RPCPreparedFrameProfile
	case RPCFrameReuseBenchmark:
		return RPCFrameReuseProfile
	case RPCHandlerPanicBenchmark:
		return RPCHandlerPanicProfile
	case RPCPoolReturnRejectionBenchmark:
		return RPCPoolReturnRejectionProfile
	default:
		return DefaultProfile
	}
}
