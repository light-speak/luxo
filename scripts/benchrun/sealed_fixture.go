package main

import "github.com/light-speak/luxo/internal/benchlayout"

const sealedCompilerFixtureBindings = "\ncodegen-find-read-overlay-v1 codegen_bench_test.go=e78e8f6209b54ea83f55f336b46451625877d09e3017c0467caf25c852461a73 bca2f8c336051736ebb635ab0217f9c865e8551cd2126c1af5e706ffee2243d7" +
	"\ncodegen-find-read-overlay-v1 compile_test.go=02e35cb4368719d35429571cac6b44526e22927db2b6bf0b7d7c98fe4d34f2b6 4a5fdb53694d84132be177cf1abc478dd572eef99989eef32d52c13c19003584" +
	"\ncodegen-find-read-overlay-v1 handler_test.go=070a9601e730dd89d2cc70fa0fd9f44ff2475a76d374451d4b6244a89ad9bee7 898db0c8bbe0b59c070468586ac6bd91ec040ed5a7af38b145739ab1cfbe3082" +
	"\ncodegen-numeric-range-overlay-v1 compile_auth_test.go=7e79498429abd0a3d8d364e1e783bc5af00a7781f6b67c7d457e2c4e69761a84 aabfc4d4e76aa8a7e69721b62ce9c278f52b5f73404b958c77ffa6ba245a5758" +
	"\ncodegen-numeric-range-overlay-v1 numeric_literal_bench_test.go=7e978e414df41e93ff919b0396f81cdbd83ff3adaa4623881b40aff156762129 8f95bd3b1454ad4f190459b2a3184722ef7eef6717e53723ca8be5935e4ce05a" +
	"\ncodegen-numeric-range-overlay-v1 numeric_range_bench_test.go=e9c98c6cbdda0c1470ee30911b8825c935de96058689a30dfbdfe7350ac168ed e5068c469e3d1a2dd2472dc048ed2a6b19f2cfbecb7a351677e34229a2bda666" +
	"\nsemantic-event-snapshot-overlay-v1 event_contract_test.go=07f8dad5b54ebc212c0afcd207a96aca629cc4d6c9ca7093739f37b227fe6dda ac3fd6cf95db8f4ac2a51333c78aafeb8d319ab892f0b778b5f1c6d50a066f74"

var codegenFindReadFixtures = []pairedBenchmarkFixture{
	{name: "codegen_bench_test.go", sourceSHA: "bca2f8c336051736ebb635ab0217f9c865e8551cd2126c1af5e706ffee2243d7", projectionSHA: "e78e8f6209b54ea83f55f336b46451625877d09e3017c0467caf25c852461a73",
		imports: []string{"\"testing\"", "\"github.com/light-speak/luxo/pkg/ast\""}, functions: []string{"BenchmarkFindRead"}},
	{name: "compile_test.go", sourceSHA: "4a5fdb53694d84132be177cf1abc478dd572eef99989eef32d52c13c19003584", projectionSHA: "02e35cb4368719d35429571cac6b44526e22927db2b6bf0b7d7c98fe4d34f2b6",
		imports: []string{"\"strings\"", "\"github.com/light-speak/luxo/pkg/ast\""}, functions: []string{"newCompiler"}},
	{name: "handler_test.go", sourceSHA: "898db0c8bbe0b59c070468586ac6bd91ec040ed5a7af38b145739ab1cfbe3082", projectionSHA: "070a9601e730dd89d2cc70fa0fd9f44ff2475a76d374451d4b6244a89ad9bee7",
		imports: []string{"\"github.com/light-speak/luxo/pkg/ast\"", "\"github.com/light-speak/luxo/pkg/token\""}, functions: []string{"testModel", "testField", "directive"}},
}

var codegenNumericRangeFixtures = []pairedBenchmarkFixture{
	{name: "compile_auth_test.go", sourceSHA: "aabfc4d4e76aa8a7e69721b62ce9c278f52b5f73404b958c77ffa6ba245a5758", projectionSHA: "7e79498429abd0a3d8d364e1e783bc5af00a7781f6b67c7d457e2c4e69761a84",
		imports: []string{"\"testing\"", "\"github.com/light-speak/luxo/pkg/ast\"", "\"github.com/light-speak/luxo/pkg/lexer\"", "\"github.com/light-speak/luxo/pkg/parser\"", "\"github.com/light-speak/luxo/pkg/semantic\""}, functions: []string{"analyzePermissionFixture"}},
	{name: "numeric_literal_bench_test.go", sourceSHA: "8f95bd3b1454ad4f190459b2a3184722ef7eef6717e53723ca8be5935e4ce05a", projectionSHA: "7e978e414df41e93ff919b0396f81cdbd83ff3adaa4623881b40aff156762129",
		imports: []string{}, functions: []string{}, variables: []string{"numericLiteralSourceSink"}},
	{name: "numeric_range_bench_test.go", sourceSHA: "e5068c469e3d1a2dd2472dc048ed2a6b19f2cfbecb7a351677e34229a2bda666", projectionSHA: "e9c98c6cbdda0c1470ee30911b8825c935de96058689a30dfbdfe7350ac168ed",
		imports: []string{"\"strings\"", "\"testing\""}, functions: []string{"BenchmarkNumericRangeValidation"}},
}

var semanticEventSnapshotFixtures = []pairedBenchmarkFixture{
	{name: "event_contract_test.go", sourceSHA: "ac3fd6cf95db8f4ac2a51333c78aafeb8d319ab892f0b778b5f1c6d50a066f74", projectionSHA: "07f8dad5b54ebc212c0afcd207a96aca629cc4d6c9ca7093739f37b227fe6dda",
		imports: []string{"\"fmt\"", "\"testing\"", "\"github.com/light-speak/luxo/pkg/ast\""}, functions: []string{"BenchmarkEventSnapshotContract"}},
}

func compilerProfileOverlay(root, pkg string, baseline bool, profile string) (string, func() error, error) {
	switch profile {
	case benchlayout.CodegenRelationLoaderProfile:
		return pairedBenchmarkOverlay(root, pkg, profile, codegenRelationLoaderFixture)
	case benchlayout.SemanticEventEmitsProfile:
		return pairedBenchmarkOverlay(root, pkg, profile, semanticEventEmitsFixture)
	case benchlayout.CodegenFindReadProfile:
		return pairedBenchmarkGroupOverlay(root, pkg, profile, codegenFindReadFixtures)
	case benchlayout.CodegenNumericRangeProfile:
		return pairedBenchmarkGroupOverlay(root, pkg, profile, codegenNumericRangeFixtures)
	case benchlayout.SemanticEventSnapshotProfile:
		return pairedBenchmarkGroupOverlay(root, pkg, profile, semanticEventSnapshotFixtures)
	default:
		return compileCompactBenchmarkOverlay(root, pkg, baseline, profile)
	}
}
