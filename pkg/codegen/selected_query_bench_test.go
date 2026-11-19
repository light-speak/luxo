package codegen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const selectedQueryBenchmarkModel = `model Product @noTime { id: Int @id name: String price: Int secret: Int @hidden }`

const closureQueryBenchmarkFunction = `
fn closureStored(): [Product] { return [1].map { number -> Product.select().first() ?: throw error.NotFound } }
`

const ownQueryBenchmarkFunctions = `
fn ownStored(): [Product] { return Product.select().all().filter { it.price > 0 } }
fn privateStored(): [Product] { return Product.select().all().filter { it.secret > 0 } }
fn combinedStored(): [Product] { return ownStored().filter { it.secret > 0 } }
fn forwardedOwn(): [Product] { val rows = combinedStored() val alias = rows return alias }
`

var ownQueryGenerationCases = []struct{ name, result, body string }{
	{"own", "[Product]", `return ownStored()`},
	{"ownPrivate", "[Product]", `return privateStored()`},
	{"callerPrivate", "[Product]", `return ownStored().filter { it.secret > 0 }`},
	{"forwardedOwn", "[Product]", `return forwardedOwn()`},
	{"scalarOwn", "[String]", `return ownStored().map { it.name }`},
	{"aliasedOwn", "[Product]", `val rows = privateStored() return rows.filter { it.price > 0 }`},
}

var selectedQueryGenerationCases = []struct{ name, result, body string }{
	{"scalar", "Int", `return 1`},
	{"full", "[Product]", `return Product.all()`},
	{"selected", "[Product]", `return Product.select().all()`},
	{"filter", "[Product]", `return Product.select().all().filter { it.price > 0 }`},
	{"scalar_map", "[String]", `return Product.select().all().filter { it.price > 0 }.map { it.name }`},
	{"private", "[Product]", `return Product.select().all().filter { it.secret > 0 }`},
	{"multiple", "[Product]", `return Product.select().all().filter { it.price > 0 && it.secret > 0 }`},
	{"named_capture", "[String]", `val it = Product.select().first() ?: throw error.NotFound return [1].map { number -> it.name }`},
	{"nested_capture", "[Product]", `return Product.select().all().filter { [0].any { number -> it.price > number } }`},
	{"explicit_shadow", "Boolean", `val it = Product.select().first() ?: throw error.NotFound if it.id < 0 { throw error.NotFound } return [1].any { it -> it > 0 }`},
	{"implicit_shadow", "Boolean", `val it = Product.select().first() ?: throw error.NotFound if it.id < 0 { throw error.NotFound } return [1].any { it > 0 }`},
}

func BenchmarkSelectedQueryGeneration(b *testing.B) {
	for _, sample := range selectedQueryGenerationCases {
		for _, count := range []int{1, 128} {
			b.Run(fmt.Sprintf("%s/%d", sample.name, count), func(b *testing.B) {
				var source strings.Builder
				source.WriteString(selectedQueryBenchmarkModel + "\n")
				for index := range count {
					fmt.Fprintf(&source, "api lookup%d(): %s { %s }\n", index, sample.result, sample.body)
				}
				input := analyzePermissionFixture(b, source.String())
				if len(input.Errors) != 0 {
					b.Fatal(input.Errors)
				}
				ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Product": {"id": 1, "name": 2, "price": 3, "secret": 4}}})
				generator := mustNewGenerator(b, GeneratorConfig{Driver: DriverPG, IDs: ids})
				b.ReportAllocs()
				for b.Loop() {
					if _, err := generator.Generate(input, "generated", nil); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkSelectedQueryFunctionGeneration(b *testing.B) {
	cases := []struct{ name, result, body, functions string }{
		{"filter", "[Product]", `return stored().filter { it.price > 0 }`, ""},
		{"scalar_map", "[String]", `return stored().filter { it.price > 0 }.map { it.name }`, ""},
		{"private", "[Product]", `return stored().filter { it.secret > 0 }`, ""},
		{"forwarded", "[Product]", `return forwarded().filter { it.price > 0 }`, ""},
		{"closurePrivate", "[Product]", `return closureStored().filter { it.secret > 0 }`, closureQueryBenchmarkFunction},
	}
	for _, sample := range ownQueryGenerationCases {
		cases = append(cases, struct{ name, result, body, functions string }{sample.name, sample.result, sample.body, ownQueryBenchmarkFunctions})
	}
	for _, sample := range cases {
		for _, count := range []int{1, 128} {
			b.Run(fmt.Sprintf("%s/%d", sample.name, count), func(b *testing.B) {
				var source strings.Builder
				functions := sample.functions
				if functions == "" {
					functions = "\nfn stored(): [Product] { return Product.select().all() }\nfn forwarded(): [Product] { return stored() }\n"
				}
				source.WriteString(selectedQueryBenchmarkModel + functions)
				for index := range count {
					fmt.Fprintf(&source, "api lookup%d(): %s { %s }\n", index, sample.result, sample.body)
				}
				input := analyzePermissionFixture(b, source.String())
				if len(input.Errors) != 0 {
					b.Fatal(input.Errors)
				}
				ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Product": {"id": 1, "name": 2, "price": 3, "secret": 4}}})
				generator := mustNewGenerator(b, GeneratorConfig{Driver: DriverPG, IDs: ids})
				b.ReportAllocs()
				for b.Loop() {
					if _, err := generator.Generate(input, "generated", nil); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func TestBuildSelectedQueryRuntimeBenchmark(t *testing.T) {
	output := os.Getenv("LUXO_QUERY_BENCH_OUTPUT")
	if output == "" {
		t.Skip("generated benchmark fixture")
	}
	root, err := filepath.Abs("../../.tmp/audit-inferred-sql-chain")
	if err != nil || filepath.Dir(output) != root {
		t.Fatal("benchmark output must stay inside its owned directory", output, err)
	}
	var source strings.Builder
	source.WriteString(selectedQueryBenchmarkModel + "\n")
	for _, sample := range selectedQueryGenerationCases {
		fmt.Fprintf(&source, "api %s(): %s { %s }\n", sample.name, sample.result, sample.body)
	}
	source.WriteString(`
fn stored(): [Product] { return Product.select().all() }
api fnFilter(): [Product] { return stored().filter { it.price > 0 } }
api fnPrivate(): [Product] { return stored().filter { it.secret > 0 } }
api fnMap(): [String] { return stored().filter { it.price > 0 }.map { it.name } }
`)
	assertions := selectedQueryRuntimeBenchmark
	ownMode := os.Getenv("LUXO_QUERY_BENCH_OWN")
	if ownMode == "1" || ownMode == "closure" {
		source.Reset()
		functions, cases := ownQueryBenchmarkFunctions, ownQueryGenerationCases
		if ownMode == "closure" {
			functions = closureQueryBenchmarkFunction
			cases = []struct{ name, result, body string }{{"closurePrivate", "[Product]", `return closureStored().filter { it.secret > 0 }`}}
		}
		source.WriteString(selectedQueryBenchmarkModel + "\n" + functions)
		for _, sample := range cases {
			fmt.Fprintf(&source, "api %s(): %s { %s }\n", sample.name, sample.result, sample.body)
		}
		assertions = ownQueryRuntimeBenchmark(ownMode == "closure")
	}
	input := analyzePermissionFixture(t, source.String())
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Product": {"id": 1, "name": 2, "price": 3, "secret": 4}}})
	generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	archive := output + "-sources"
	if err := os.MkdirAll(archive, 0755); err != nil {
		t.Fatal(err)
	}
	dir := writeProtocolFixture(t, "package generated\n", assertions)
	for name, data := range generated.Files {
		writeAutomaticBenchmarkFile(t, archive, name, data)
		if name == "db.gen.go" {
			for _, terminal := range []struct{ original, replacement string }{
				{"return (*pg.Query[Product])(q).All(ctx)", "return selectedQueryBenchmarkRows, nil"},
				{"return (*pg.Query[Product])(q).First(ctx)", "return selectedQueryBenchmarkRows[0], nil"},
			} {
				if strings.Count(string(data), terminal.original) != 1 {
					t.Fatal("query terminal no longer has one known database call", terminal.original)
				}
				data = []byte(strings.Replace(string(data), terminal.original, terminal.replacement, 1))
			}
			writeAutomaticBenchmarkFile(t, archive, "instrumented_db.gen.go", data)
		}
		writeAutomaticBenchmarkFile(t, dir, name, data)
	}
	writeAutomaticBenchmarkFile(t, archive, "wire_test.go", []byte(assertions))
	command := exec.Command("go", "test", "-c", "-o", output, ".")
	command.Dir = dir
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatal(string(data), err)
	}
}

func ownQueryRuntimeBenchmark(closure bool) string {
	current := `  {"own",handleOwn},{"ownPrivate",handleOwnPrivate},{"callerPrivate",handleCallerPrivate},
  {"forwardedOwn",handleForwardedOwn},{"scalarOwn",handleScalarOwn},{"aliasedOwn",handleAliasedOwn},`
	if closure {
		current = `  {"closurePrivate",handleClosurePrivate},`
	}
	assertions := strings.Replace(selectedQueryRuntimeBenchmark, selectedQueryBenchmarkFactories, current, 1)
	if closure {
		return strings.Replace(assertions, `count:=128;if sample.name=="named_capture"{count=1}`, `count:=1`, 1)
	}
	return strings.Replace(assertions, `sample.name=="fn_map"||sample.name=="named_capture"`, `sample.name=="scalarOwn"`, 1)
}

const selectedQueryBenchmarkFactories = `  {"selected",handleSelected},{"filter",handleFilter},{"private",handlePrivate},{"multiple",handleMultiple},
  {"fn_filter",handleFnFilter},{"fn_private",handleFnPrivate},{"fn_map",handleFnMap},
  {"named_capture",handleNamed_capture},{"nested_capture",handleNested_capture},`

// Only the database terminal is replaced. Generated query construction,
// projection merging, filtering and response encoding execute unchanged.
const selectedQueryRuntimeBenchmark = `package generated
import("bytes";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/selection")
var selectedQueryBenchmarkRows = func()[]*Product{rows:=make([]*Product,128);for i:=range rows{rows[i]=&Product{Id:int64(i+1),Name:"wanted",Price:99,Secret:99}};return rows}()
var selectedQueryBenchmarkHandler api.HandlerFunc
func BenchmarkSelectedQueryHandler(b *testing.B){
 app:=NewFromDB(nil)
 for _,sample:=range []struct{name string;factory func(*App)api.HandlerFunc}{
` + selectedQueryBenchmarkFactories + `
 }{
  b.Run(sample.name+"/init",func(b *testing.B){b.ReportAllocs();for b.Loop(){selectedQueryBenchmarkHandler=sample.factory(app)}})
  b.Run(sample.name+"/call",func(b *testing.B){
   mask,err:=codec.FieldMaskSet(nil,2);if err!=nil{b.Fatal(err)};mask,err=codec.AppendSelectionMask(nil,mask,nil);if err!=nil{b.Fatal(err)}
   req:=&api.Request{Select:[]*selection.Field{{Name:"name"}},FieldMask:mask,Buf:&api.ResponseBuf{B:make([]byte,0,8192)}}
   if sample.name=="fn_map"||sample.name=="named_capture"{req.Select=nil;req.FieldMask=nil}
   handler:=sample.factory(app);b.ReportAllocs()
   for b.Loop(){req.Buf.B=req.Buf.B[:0];if err:=handler(b.Context(),req);err!=nil{b.Fatal(err)}}
   count:=128;if sample.name=="named_capture"{count=1}
   if bytes.Count(req.Buf.B,[]byte("wanted"))!=count{b.Fatal("wrong response rows")}
  })
 }
}
`
