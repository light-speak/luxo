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

func TestRPCOwnedRequestProfileRequiresPackageAndExactRoot(t *testing.T) {
	for _, tc := range []struct{ pkg, name, want string }{
		{"pkg/lux/rpc", "BenchmarkLRPCOwnedRequest", "rpc-owned-request"},
		{"pkg/lux/rpc", "BenchmarkLRPCOwnedRequest/4096/owned-2", "rpc-owned-request"},
		{"pkg/lux/rpc", "BenchmarkLRPCOwnedRequestSibling", DefaultProfile},
		{"pkg/lux/rpc", "BenchmarkRPC_Ping", DefaultProfile},
		{CodecPackage, "BenchmarkLRPCOwnedRequest", DefaultProfile},
	} {
		if got := ProfileForBenchmark(tc.pkg, tc.name); got != tc.want || !ValidPerformanceProfile(tc.pkg, got) {
			t.Fatalf("%s/%s profile %q, want %q", tc.pkg, tc.name, got, tc.want)
		}
	}
	if ValidPerformanceProfile(CodecPackage, "rpc-owned-request") {
		t.Fatal("accepted the RPC owned-request profile outside its package")
	}
}

func TestRPCDeadlineProfileRequiresPackageAndExactRoot(t *testing.T) {
	const root = "BenchmarkRPCDeadlineClassification"
	const profile = "rpc-deadline-classification"
	for _, tc := range []struct{ pkg, name, want string }{
		{RPCPackage, root, profile},
		{RPCPackage, root + "/ExpiredEOF/After-2", profile},
		{RPCPackage, root + "Sibling", DefaultProfile},
		{RPCPackage, "BenchmarkRPC_Ping", DefaultProfile},
		{RPCPackage, "BenchmarkLRPCOwnedRequest", RPCOwnedRequestProfile},
		{LuviaPackage, root, DefaultProfile},
	} {
		if got := ProfileForBenchmark(tc.pkg, tc.name); got != tc.want || !ValidPerformanceProfile(tc.pkg, got) {
			t.Fatalf("%s/%s profile %q, want %q", tc.pkg, tc.name, got, tc.want)
		}
	}
	if ValidPerformanceProfile(LuviaPackage, profile) {
		t.Fatal("accepted the RPC deadline profile outside its package")
	}
}

func TestRPCNilContextGuardProfileRequiresPackageAndExactRoot(t *testing.T) {
	const root = "BenchmarkRPCNilContextGuard"
	const profile = "rpc-nil-context-guard"
	for _, tc := range []struct{ pkg, name, want string }{
		{RPCPackage, root, profile},
		{RPCPackage, root + "/Rejected/After-2", profile},
		{RPCPackage, root + "Sibling", DefaultProfile},
		{RPCPackage, "BenchmarkRPCAuthenticationErrorRecovery/Long/After-2", DefaultProfile},
		{RPCPackage, "BenchmarkRPCDeadlineClassification", RPCDeadlineProfile},
		{LuviaPackage, root, DefaultProfile},
	} {
		if got := ProfileForBenchmark(tc.pkg, tc.name); got != tc.want || !ValidPerformanceProfile(tc.pkg, got) {
			t.Fatalf("%s/%s profile %q, want %q", tc.pkg, tc.name, got, tc.want)
		}
	}
	if ValidPerformanceProfile(LuviaPackage, profile) {
		t.Fatal("accepted the RPC nil-context profile outside its package")
	}
}

func TestLuviaAuthProfileRequiresPackageAndExactRoot(t *testing.T) {
	const root = "BenchmarkAuthMiddlewareIsolation"
	const profile = "luvia-auth-middleware"
	for _, tc := range []struct{ pkg, name, want string }{
		{LuviaPackage, root, profile},
		{LuviaPackage, root + "/inherited-other-key/after-2", profile},
		{LuviaPackage, root + "Sibling", DefaultProfile},
		{LuviaPackage, "BenchmarkGatewayStandardStartStop", DefaultProfile},
		{LuviaPackage, "BenchmarkGatewayIdentity", LuviaGatewayIdentityProfile},
		{RPCPackage, root, DefaultProfile},
	} {
		if got := ProfileForBenchmark(tc.pkg, tc.name); got != tc.want || !ValidPerformanceProfile(tc.pkg, got) {
			t.Fatalf("%s/%s profile %q, want %q", tc.pkg, tc.name, got, tc.want)
		}
	}
	if ValidPerformanceProfile(RPCPackage, profile) {
		t.Fatal("accepted the Luvia auth profile outside its package")
	}
}

func TestAPIAndLuviaProfilesPreserveTheirOtherReaders(t *testing.T) {
	for _, tc := range []struct{ pkg, name, want string }{
		{APIPackage, "BenchmarkIdentityConditionValidity/member/EnumChecked-2", "api-identity-condition"},
		{APIPackage, "BenchmarkHandlerPanicFormatting/OpaquePanic/After-2", "api-handler-panic"},
		{APIPackage, "BenchmarkWSConnectionRegistrations/size=64/checked-2", "api-websocket-registrations"},
		{APIPackage, "BenchmarkHandlerDetachedResponseBuffer-2", APINonIdentityProfile},
		{APIPackage, "BenchmarkHandlerResponseBuffer/Int-2", APIHandlerPanicProfile},
		{APIPackage, "BenchmarkWSConnectionRegistrationCold/false-2", APINonIdentityProfile},
		{APIPackage, "BenchmarkResponseOriginRun/bounded-2", "api-response-origin-run"},
		{APIPackage, "BenchmarkResponseOriginRun/unbounded-2", "api-response-origin-run"},
		{APIPackage, "BenchmarkResponseOriginContended", APINonIdentityProfile},
		{APIPackage, "BenchmarkBodyRead/0/direct-2", "api-body-read"},
		{APIPackage, "BenchmarkBodyReadReverse/4096/direct-2", APINonIdentityProfile},
		{APIPackage, "BenchmarkJSONQuoteCandidate/empty/current-2", "api-json-quote-candidate"},
		{APIPackage, "BenchmarkJSONQuoteInvalidCandidate", APINonIdentityProfile},
		{APIPackage, "BenchmarkJSONListMetadata/escaped/0/metadata-2", "api-json-list-metadata"},
		{APIPackage, "BenchmarkJSONListMetadataSibling", APINonIdentityProfile},
		{APIPackage, "BenchmarkStreamAPIClassification/binary-2", APINonIdentityProfile},
		{APIPackage, "BenchmarkWSJSONSubscriptionMetadata", APINonIdentityProfile},
		{RPCPackage, "BenchmarkJSONListMetadata", DefaultProfile},
		{RPCPackage, "BenchmarkResponseOriginRun", DefaultProfile},
		{APIPackage, "BenchmarkWSConnectionRegistrationsSibling-2", APINonIdentityProfile},
		{RPCPackage, "BenchmarkHandlerPanicFormatting", RPCHandlerPanicProfile},
		{APIPackage, "BenchmarkIdentityEnumArray", DefaultProfile},
		{APIPackage, "BenchmarkIdentitySubjectVisibility", APIIdentityConditionProfile},
		{APIPackage, "BenchmarkIdentitySurface", APINonIdentityProfile},
		{APIPackage, "BenchmarkLocalRouteExport", "api-local-route-export"},
		{APIPackage, "BenchmarkLocalRouteExport/routes=128/previous-2", "api-local-route-export"},
		{APIPackage, "BenchmarkLocalRouteLookup", APINonIdentityProfile},
		{APIPackage, "BenchmarkLocalRouteExportSibling", APINonIdentityProfile},
		{APIPackage, "BenchmarkCanonicalJSONBytes", "api-canonical-json"},
		{APIPackage, "BenchmarkCanonicalJSONTyped/boolean/1-2", "api-canonical-json"},
		{APIPackage, "BenchmarkCanonicalJSONValue/object/after-2", "api-canonical-json"},
		{APIPackage, "BenchmarkCanonicalJSONSibling", APINonIdentityProfile},
		{APIPackage, "BenchmarkBinaryParamLookup", "api-binary-param-lookup"},
		{APIPackage, "BenchmarkBinaryParamLookup/n=16/stride=65537/direct=false-2", "api-binary-param-lookup"},
		{APIPackage, "BenchmarkBinaryParamLookupSibling", APINonIdentityProfile},
		{RPCPackage, "BenchmarkBinaryParamLookup", DefaultProfile},
		{APIPackage, "BenchmarkRequestLogBoundary/full-2", APIRequestLogProfile},
		{APIPackage, "BenchmarkRequestLogDelivered-2", APIRequestLogProfile},
		{APIPackage, "BenchmarkHTTPCompletionSampling", APINonIdentityProfile},
		{APIPackage, "BenchmarkRequestLogDeliveredSibling", APINonIdentityProfile},
		{RPCPackage, "BenchmarkRequestLogDelivered", DefaultProfile},
		{LuviaPackage, "BenchmarkPrimaryKeys/row-small/keyLast=false/baseline=true-2", LuviaPrimaryTraceProfile},
		{LuviaPackage, "BenchmarkTraceEncodingComparison", LuviaPrimaryTraceProfile},
		{LuviaPackage, "BenchmarkGatewayIdentity", LuviaGatewayIdentityProfile},
		{LuviaPackage, "BenchmarkGatewayIdentity/authenticated/after-2", LuviaGatewayIdentityProfile},
		{LuviaPackage, "BenchmarkGatewayIdentitySibling", DefaultProfile},
		{LuviaPackage, "BenchmarkMergeDestination", "luvia-merge-destination"},
		{LuviaPackage, "BenchmarkMergeDestination/columnar=true/rows=1/direct=true-2", "luvia-merge-destination"},
		{LuviaPackage, "BenchmarkMergeDestinationSibling", DefaultProfile},
		{LuviaPackage, "BenchmarkValidateResolvedFields", "luvia-resolved-fields"},
		{LuviaPackage, "BenchmarkValidateResolvedFields/list-small-128x16/per-value-2", "luvia-resolved-fields"},
		{LuviaPackage, "BenchmarkValidateResolvedFieldsSibling", DefaultProfile},
		{LuviaPackage, "BenchmarkGroupedResponse", DefaultProfile},
		{CodegenPackage, "BenchmarkValidateResolvedFields", DefaultProfile},
		{LuviaPackage, "BenchmarkGatewayStandardStartStop", DefaultProfile},
		{CodegenPackage, "BenchmarkMergeDestination", DefaultProfile},
		{CodegenPackage, "BenchmarkGatewayIdentity", DefaultProfile},
		{LuviaPackage, "BenchmarkStaticMetricsJSON", DefaultProfile},
		{LuviaPackage, "BenchmarkConvenienceKeyParentValidation", "luvia-convenience-keys"},
		{LuviaPackage, "BenchmarkConvenienceKeyParentValidation/column-small/keyLast=false/current-2", "luvia-convenience-keys"},
		{LuviaPackage, "BenchmarkConvenienceKeyParentValidation/column-small/keyLast=true/current-2", "luvia-convenience-keys"},
		{LuviaPackage, "BenchmarkConvenienceKeyParentValidationSibling", DefaultProfile},
		{CodegenPackage, "BenchmarkConvenienceKeyParentValidation", DefaultProfile},
	} {
		if got := ProfileForBenchmark(tc.pkg, tc.name); got != tc.want || !ValidPerformanceProfile(tc.pkg, got) {
			t.Fatalf("%s/%s profile %q, want %q", tc.pkg, tc.name, got, tc.want)
		}
	}
	for _, tc := range []struct{ pkg, profile, filter string }{
		{CodecPackage, CodecReaderProfile, "."},
		{"pkg/lux/schema", "", ""},
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
	apiProfiles := IsolationProfiles(APIPackage)
	wantAPI := []IsolationSpec{
		{APINonIdentityProfile, "."},
		{APILocalRouteExportProfile, "^BenchmarkLocalRouteExport$"},
		{"api-static-value-params", "^BenchmarkStaticValueParams$"},
		{APICanonicalJSONProfile, "^BenchmarkCanonicalJSON(Structured|Typed|Bytes|Float|Value|Integer)$"},
		{APIBinaryParamLookupProfile, "^BenchmarkBinaryParamLookup$"},
		{APIRequestLogProfile, "^BenchmarkRequestLogBoundary$|^BenchmarkRequestLogDelivered$"},
		{"api-identity-condition", "^BenchmarkIdentityConditionValidity$|^BenchmarkIdentitySubjectVisibility$"},
		{"api-handler-panic", "^BenchmarkHandlerPanicFormatting$|^BenchmarkHandlerResponseBuffer$"},
		{"api-websocket-registrations", "^BenchmarkWSConnectionRegistrations$"},
		{"api-response-origin-run", "^BenchmarkResponseOriginRun$"},
		{"api-body-read", "^BenchmarkBodyRead$"},
		{"api-json-quote-candidate", "^BenchmarkJSONQuoteCandidate$"},
		{"api-json-list-metadata", "^BenchmarkJSONListMetadata$"},
	}
	if !reflect.DeepEqual(apiProfiles, wantAPI) || ValidPerformanceProfile(RPCPackage, APIBinaryParamLookupProfile) ||
		ValidPerformanceProfile(RPCPackage, APIRequestLogProfile) {
		t.Fatalf("unexpected API profiles: %v", apiProfiles)
	}
	profiles := IsolationProfiles(LuviaPackage)
	wantLuviaProfiles := []IsolationSpec{
		{LuviaPrimaryTraceProfile, "^BenchmarkPrimaryKeys$|^BenchmarkTraceEncodingComparison$"},
		{LuviaGatewayIdentityProfile, "^BenchmarkGatewayIdentity$"},
		{LuviaAuthMiddlewareProfile, "^BenchmarkAuthMiddlewareIsolation$"},
		{"luvia-merge-destination", "^BenchmarkMergeDestination$"},
		{"luvia-resolved-fields", "^BenchmarkValidateResolvedFields$"},
		{"luvia-convenience-keys", "^BenchmarkConvenienceKeyParentValidation$"},
	}
	if !reflect.DeepEqual(profiles, wantLuviaProfiles) ||
		ValidPerformanceProfile(CodegenPackage, "luvia-convenience-keys") ||
		ValidPerformanceProfile(CodegenPackage, "luvia-resolved-fields") ||
		ValidPerformanceProfile(CodegenPackage, LuviaGatewayIdentityProfile) ||
		ValidPerformanceProfile(CodegenPackage, "luvia-merge-destination") {
		t.Fatalf("unexpected Luvia isolation: %v", profiles)
	}
}

func TestCodegenProfilesRequirePackageAndExactRoots(t *testing.T) {
	const migration, migrationProfile = "BenchmarkMigrationStateValidation", "codegen-migration-state"
	for _, tc := range []struct{ pkg, name, want string }{
		{CodegenPackage, CodegenLiteralBenchmark, CodegenLiteralProfile},
		{CodegenPackage, CodegenLiteralBenchmark + "/duration_plain-2", CodegenLiteralProfile},
		{CodegenPackage, CodegenReceiverBenchmark, CodegenReceiverProfile},
		{CodegenPackage, CodegenReceiverBenchmark + "/用户-2", CodegenReceiverProfile},
		{CodegenPackage, CodegenReceiverBenchmark + "Sibling-2", DefaultProfile},
		{CodegenPackage, migration, migrationProfile},
		{CodegenPackage, migration + "/empty/validate-2", migrationProfile},
		{CodegenPackage, migration + "Sibling-2", DefaultProfile},
		{CodegenPackage, "BenchmarkGenerateNumericLiteralBindings/plain-2", DefaultProfile},
		{CodecPackage, CodegenLiteralBenchmark, DefaultProfile},
		{CodecPackage, CodegenReceiverBenchmark, DefaultProfile},
		{CodecPackage, migration, DefaultProfile},
	} {
		if got := ProfileForBenchmark(tc.pkg, tc.name); got != tc.want || !ValidPerformanceProfile(tc.pkg, got) {
			t.Fatalf("%s/%s profile %q, want %q", tc.pkg, tc.name, got, tc.want)
		}
	}
	profiles := IsolationProfiles(CodegenPackage)
	if len(profiles) != 6 || profiles[0] != (IsolationSpec{CodegenLiteralProfile, "^" + CodegenLiteralBenchmark + "$"}) ||
		profiles[1] != (IsolationSpec{CodegenReceiverProfile, "^" + CodegenReceiverBenchmark + "$"}) ||
		profiles[2] != (IsolationSpec{migrationProfile, "^BenchmarkGlobalReceiverName$|^BenchmarkMigrationStateValidation$"}) ||
		profiles[3] != (IsolationSpec{"codegen-relation-loader", "^BenchmarkRelationLoaderGeneration$"}) ||
		ValidPerformanceProfile(CodecPackage, CodegenLiteralProfile) ||
		ValidPerformanceProfile(CodecPackage, CodegenReceiverProfile) || ValidPerformanceProfile(CodecPackage, migrationProfile) {
		t.Fatalf("unexpected codegen isolation: %v", profiles)
	}
}

func TestRelationAndEventBenchmarkProfilesRequireExactRoots(t *testing.T) {
	for _, tc := range []struct{ pkg, root, sibling, profile string }{
		{"pkg/codegen", "BenchmarkRelationLoaderGeneration", "BenchmarkRelationResolverEmission", "codegen-relation-loader"},
		{"pkg/semantic", "BenchmarkCollectEventEmits", "BenchmarkReachableEventEmits", "semantic-event-emits"},
	} {
		if got := ProfileForBenchmark(tc.pkg, tc.root+"/nested-2"); got != tc.profile ||
			!ValidPerformanceProfile(tc.pkg, tc.profile) || ValidPerformanceProfile("pkg/lux", tc.profile) {
			t.Fatalf("%s/%s profile = %q, want %q", tc.pkg, tc.root, got, tc.profile)
		}
		if ProfileForBenchmark(tc.pkg, tc.sibling) != DefaultProfile ||
			ProfileForBenchmark(tc.pkg, tc.root+"Sibling-2") != DefaultProfile ||
			ProfileForBenchmark("pkg/lux", tc.root) != DefaultProfile {
			t.Fatalf("profile %s accepted a different root or package", tc.profile)
		}
	}

}

func TestLuxShortPathProfileRequiresPackageAndExactRoots(t *testing.T) {
	for _, tc := range []struct{ pkg, name, want string }{
		{LuxPackage, LuxStrictFilterBenchmark, LuxShortPathProfile},
		{LuxPackage, LuxDecimalAlignmentBenchmark + "/Add/zero/after-2", LuxShortPathProfile},
		{LuxPackage, LuxStrictFilterBenchmark + "Sibling-2", DefaultProfile},
		{LuxPackage, "BenchmarkDecimalPrecision-2", DefaultProfile},
		{CodecPackage, LuxDecimalAlignmentBenchmark, DefaultProfile},
	} {
		if got := ProfileForBenchmark(tc.pkg, tc.name); got != tc.want || !ValidPerformanceProfile(tc.pkg, got) {
			t.Fatalf("%s/%s profile %q, want %q", tc.pkg, tc.name, got, tc.want)
		}
	}
	profiles := IsolationProfiles(LuxPackage)
	want := []IsolationSpec{{LuxShortPathProfile, "^BenchmarkStrictFilterContract$|^BenchmarkDecimalAlignment$"}}
	if !reflect.DeepEqual(profiles, want) || ValidPerformanceProfile(CodecPackage, LuxShortPathProfile) {
		t.Fatalf("unexpected Lux isolation: %v", profiles)
	}
}

func TestReadinessBenchmarkProfileIsolation(t *testing.T) {
	cases := []struct {
		pkg, leaf, profile string
	}{
		{"pkg/lux/rpc", "BenchmarkLRPCPreparedFrame/0/before-2", "rpc-prepared-frame"},
		{"pkg/lux/queue", "BenchmarkQueueStreamName/tasks/after-2", "queue-stream-name"},
		{"pkg/lux/rpc", "BenchmarkLRPCFrame/0-2", DefaultProfile},
		{"pkg/lux/queue", "BenchmarkMemoryQueue/1-2", DefaultProfile},
	}
	for _, tc := range cases {
		t.Run(tc.leaf, func(t *testing.T) {
			if got := ProfileForBenchmark(tc.pkg, tc.leaf); got != tc.profile {
				t.Fatalf("profile = %q, want %q", got, tc.profile)
			}
			if !ValidPerformanceProfile(tc.pkg, tc.profile) {
				t.Fatalf("discovery does not accept %q for %q", tc.profile, tc.pkg)
			}
		})
	}
}

func TestIntelBenchmarkProfilesKeepRootsAndPackagesIndependent(t *testing.T) {
	for _, tc := range []struct{ pkg, name, want string }{
		{"pkg/lux/api", "BenchmarkStaticValueParams/Bytes/JSON/previous-2", "api-static-value-params"},
		{"pkg/lux/api", "BenchmarkStaticParamSlot/static-2", APINonIdentityProfile},
		{"pkg/lux/api", "BenchmarkHandlerResponseBufferSibling-2", APINonIdentityProfile},
		{"pkg/lux/rpc", "BenchmarkHandlerResponseBuffer/Int-2", DefaultProfile},
		{"pkg/lux/api", "BenchmarkIdentitySubjectVisibility/value/reference-2", APIIdentityConditionProfile},
		{"pkg/lux/api", "BenchmarkIdentitySubjectVisibilitySibling-2", APINonIdentityProfile},
		{"pkg/lux/api", "BenchmarkIdentityEnumArray/empty-2", DefaultProfile},
		{"pkg/lux/rpc", "BenchmarkIdentitySubjectVisibility/value/reference-2", DefaultProfile},
		{"pkg/lux/rpc", "BenchmarkLRPCPreparedFrame/128/after-2", RPCPreparedFrameProfile},
		{"pkg/lux/rpc", "BenchmarkLRPCConnectionFrameReuseCandidate/128/reuse-2", "rpc-frame-reuse"},
		{"pkg/lux/rpc", "BenchmarkLRPCConnectionFrameReuseCandidateSibling", DefaultProfile},
		{"pkg/lux/pg", "BenchmarkDurationWriteReject/List128Last-2", "pg-duration-write-reject"},
		{"pkg/lux/pg", "BenchmarkRewrittenDuration", DefaultProfile},
		{"pkg/lux/dialect/pg", "BenchmarkDurationWriteReject", DefaultProfile},
	} {
		if got := ProfileForBenchmark(tc.pkg, tc.name); got != tc.want || !ValidPerformanceProfile(tc.pkg, got) {
			t.Fatalf("%s/%s profile = %q, want %q", tc.pkg, tc.name, got, tc.want)
		}
	}
	if ValidPerformanceProfile("pkg/lux/dialect/pg", "pg-duration-write-reject") {
		t.Fatal("the dialect package cannot compile the concrete PG fixture")
	}
}

func TestRPCHandlerPanicProfileRequiresItsExactPackageAndRoot(t *testing.T) {
	const root, profile = "BenchmarkHandlerPanicFormatting", "rpc-handler-panic"
	for _, name := range []string{root, root + "/OpaquePanic/After-2"} {
		if got := ProfileForBenchmark(RPCPackage, name); got != profile || !ValidPerformanceProfile(RPCPackage, got) {
			t.Fatalf("RPC handler profile=%s", got)
		}
	}
	if ProfileForBenchmark(RPCPackage, root+"Sibling-2") != DefaultProfile || ValidPerformanceProfile(APIPackage, profile) || ValidPerformanceProfile(RPCPackage, "api-handler-panic") || ProfileForBenchmark(APIPackage, root) == profile {
		t.Fatal("RPC profile leaked across root or package")
	}
}

func TestTransportRejectionAndReplacementProfiles(t *testing.T) {
	const rpc, h2 = "pkg/lux/rpc", "pkg/lux/h2transport"
	for _, tc := range []struct{ pkg, other, root, leaf, profile string }{
		{rpc, h2, "BenchmarkRPCPoolReturnRejection", "/full/after-2", "rpc-pool-return-rejection"},
		{h2, rpc, "BenchmarkTaskDeadlineReplacement", "/128/conditional-2", "h2-task-deadline-replacement"},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			if ProfileForBenchmark(tc.pkg, tc.root) != tc.profile ||
				ProfileForBenchmark(tc.pkg, tc.root+tc.leaf) != tc.profile ||
				ProfileForBenchmark(tc.other, tc.root) != DefaultProfile ||
				ProfileForBenchmark(tc.pkg, tc.root+"Sibling") != DefaultProfile ||
				!ValidPerformanceProfile(tc.pkg, tc.profile) || ValidPerformanceProfile(tc.other, tc.profile) {
				t.Fatalf("incorrect root or package binding for %s", tc.profile)
			}
		})
	}
}

func TestSealedCompilerProfilesRequireExactRoots(t *testing.T) {
	for _, tc := range []struct{ pkg, root, profile string }{
		{"pkg/codegen", "BenchmarkFindRead", "codegen-find-read"},
		{"pkg/codegen", "BenchmarkNumericRangeValidation", "codegen-numeric-range"},
		{"pkg/semantic", "BenchmarkEventSnapshotContract", "semantic-event-snapshot"},
	} {
		for _, name := range []string{tc.root, tc.root + "/leaf-2"} {
			if got := ProfileForBenchmark(tc.pkg, name); got != tc.profile || !ValidPerformanceProfile(tc.pkg, got) {
				t.Fatalf("%s/%s = %s", tc.pkg, name, got)
			}
		}
		if ProfileForBenchmark(tc.pkg, tc.root+"Sibling-2") != DefaultProfile || ProfileForBenchmark("pkg/lux", tc.root) != DefaultProfile || ValidPerformanceProfile("pkg/lux", tc.profile) {
			t.Fatal("profile escaped exact root/package")
		}
	}
}
