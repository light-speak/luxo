package codegen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkModelIdentityGeneration(b *testing.B) {
	for _, sample := range []struct{ name, body string }{
		{"scalar-control", "return value == value"},
		{"model-control", "return item == item"},
		{"model-literals", "return Item { id: value } == Item { id: value }"},
	} {
		b.Run(sample.name, func(b *testing.B) {
			input := analyzePermissionFixture(b, "model Item @noTime { id: Int @id } fn same(value: Int, item: Item): Boolean { "+sample.body+" }")
			config := GeneratorConfig{Driver: DriverPG, IDs: StableIDs{ModelFields: map[string]map[string]int{"Item": {"id": 1}}}}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Generate(input, "generated", config); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkYieldStatementGeneration(b *testing.B) {
	input := analyzePermissionFixture(b, "fn first(items: [Int]): Int? { return for item in items { yield item } }")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDiscardStatementGeneration(b *testing.B) {
	input := analyzePermissionFixture(b, "fn discard(value: Int): Int { value + 1 return value }")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkInlineTransformGeneration(b *testing.B) {
	for _, sample := range []struct{ name, field string }{
		{"date", "DateTime @transform { it - 30m }"},
		{"string-control", "String @transform { it.uppercase() }"},
		{"float-control", "Float @transform { it % 2.0 }"},
		{"plain-control", "DateTime"},
	} {
		b.Run(sample.name, func(b *testing.B) {
			input := analyzePermissionFixture(b, "type Snapshot { value: "+sample.field+" }")
			config := GeneratorConfig{Driver: DriverPG, IDs: StableIDs{ModelFields: map[string]map[string]int{"Snapshot": {"value": 1}}}}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Generate(input, "generated", config); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkObjectDefaultGeneration(b *testing.B) {
	for _, sample := range []struct {
		name  string
		count int
		value string
	}{{"no_defaults", 2, "Int"}, {"two_defaults", 2, "Int = 42"}, {"nullable16", 16, "Int? = 42"}, {"wide65", 65, "Int = 42"}} {
		b.Run(sample.name, func(b *testing.B) {
			var source strings.Builder
			source.WriteString("type Snapshot {\n")
			fields := map[string]int{}
			for index := range sample.count {
				fmt.Fprintf(&source, "f%d: %s\n", index, sample.value)
				fields[fmt.Sprintf("f%d", index)] = index + 1
			}
			source.WriteString("}\nfn build(): Snapshot { return Snapshot {")
			if sample.name == "no_defaults" {
				source.WriteString("f0: 42, f1: 42")
			}
			source.WriteString("} }")
			input := analyzePermissionFixture(b, source.String())
			config := GeneratorConfig{Driver: DriverPG, IDs: StableIDs{ModelFields: map[string]map[string]int{"Snapshot": fields}}}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Generate(input, "generated", config); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkStructuredFieldGeneration(b *testing.B) {
	for _, count := range []int{1, 16} {
		b.Run(fmt.Sprintf("constructors%d", count), func(b *testing.B) {
			var source strings.Builder
			source.WriteString("type Child { value: Int }\ntype Holder { child: Child }\napi inspect(): Int { return 0 }\n")
			for index := range count {
				fmt.Fprintf(&source, "fn build%d(): Holder { return Holder { child: Child { value: 42 } } }\n", index)
			}
			input := analyzePermissionFixture(b, source.String())
			config := GeneratorConfig{Driver: DriverPG, IDs: StableIDs{
				APIs:        map[string]int{"inspect": 1},
				ModelFields: map[string]map[string]int{"Child": {"value": 1}, "Holder": {"child": 1}},
			}}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Generate(input, "generated", config); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkNullableWhenGeneration(b *testing.B) {
	for _, sample := range []struct {
		name, result, fallback string
		count                  int
	}{{"nonnull", "Int", "7", 1}, {"nullable1", "Int?", "null", 1}, {"nullable16", "Int?", "null", 16}} {
		b.Run(sample.name, func(b *testing.B) {
			var source strings.Builder
			source.WriteString("api sanity(): Int { return 1 }\n")
			for index := range sample.count {
				fmt.Fprintf(&source, "fn choose%d(flag: Boolean, value: Int): %s { return when { flag -> value else -> %s } }\n", index, sample.result, sample.fallback)
			}
			input := analyzePermissionFixture(b, source.String())
			config := GeneratorConfig{Driver: DriverPG, IDs: StableIDs{APIs: map[string]int{"sanity": 1}}}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Generate(input, "generated", config); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestBuildObjectDefaultRuntimeBenchmark(t *testing.T) {
	output := os.Getenv("LUXO_OBJECT_DEFAULT_BENCH_BINARY")
	if output == "" {
		t.Skip("standalone benchmark build requires an explicit owned output path")
	}
	root, err := filepath.Abs("../../.tmp/audit-object-defaults")
	if err != nil || filepath.Dir(output) != root {
		t.Fatal("benchmark output must stay inside its owned directory", output, err)
	}
	input := analyzePermissionFixture(t, objectDefaultRuntimeSource)
	ids := StableIDs{ModelFields: map[string]map[string]int{"Scalar": {"count": 1, "label": 2}, "Pointer": {"value": 1, "payload": 2}, "Plain": {"count": 1, "label": 2}}}
	generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	dir := writeProtocolFixture(t, "package generated\n", objectDefaultRuntimeBenchmark)
	archiveObjectDefaultBenchmark(t, dir, output+"-sources", generated)
	command := exec.Command("go", "test", "-c", "-o", output, ".")
	command.Dir = dir
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatal(string(data), err)
	}
}

func archiveObjectDefaultBenchmark(t *testing.T, dir, archive string, generated *GenerateResult) {
	t.Helper()
	if err := os.MkdirAll(archive, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range generated.Files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"model.gen.go", "handler.gen.go", "writejson.gen.go", "wire_test.go"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(archive, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

const objectDefaultRuntimeSource = `
type Scalar { count: Int = 42 label: String = "guest" }
type Pointer { value: Int? = 42 payload: JSON = null }
type Plain { count: Int label: String }
fn buildScalar(): Scalar { return Scalar {} }
fn buildPointer(): Pointer { return Pointer {} }
fn buildPlain(): Plain { return Plain { count: 7, label: "given" } }
`

const objectDefaultRuntimeBenchmark = `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/codec")

var scalarSink Scalar
var pointerSink Pointer
var plainSink Plain
func BenchmarkObjectDefaultRuntime(b *testing.B){
 app:=&App{};ctx:=b.Context()
 b.Run("constructor_scalar",func(b *testing.B){b.ReportAllocs();for b.Loop(){value,err:=app.buildScalar(ctx);if err!=nil{b.Fatal(err)};scalarSink=value}})
 b.Run("constructor_pointer",func(b *testing.B){b.ReportAllocs();for b.Loop(){value,err:=app.buildPointer(ctx);if err!=nil{b.Fatal(err)};pointerSink=value}})
 b.Run("no_default_constructor_control",func(b *testing.B){b.ReportAllocs();for b.Loop(){value,err:=app.buildPlain(ctx);if err!=nil{b.Fatal(err)};plainSink=value}})
 for _,sample:=range []struct{name,raw string}{{"missing","{}"},{"present","{\"count\":7,\"label\":\"given\"}"}}{
  b.Run("json_scalar_"+sample.name,func(b *testing.B){raw:=[]byte(sample.raw);b.ReportAllocs();for b.Loop(){var value Scalar;if err:=value.ReadJSONInput(raw);err!=nil{b.Fatal(err)};scalarSink=value}})
 }
 encoder:=codec.NewEncoder(nil);encoder.WriteVarint(0);encoder.WriteFieldInt(1,7);if err:=encoder.WriteFieldString(2,"given");err!=nil{b.Fatal(err)};encoder.WriteEnd()
 for _,sample:=range []struct{name string;data []byte}{{"missing",[]byte{0,0}},{"present",encoder.Bytes()}}{
  b.Run("binary_scalar_"+sample.name,func(b *testing.B){b.ReportAllocs();for b.Loop(){decoder:=codec.NewDecoder(sample.data);var value Scalar;value.ReadLuxoInput(decoder);if err:=decoder.Finish();err!=nil{b.Fatal(err)};scalarSink=value}})
 }
 b.Run("json_pointer_missing",func(b *testing.B){raw:=[]byte("{}");b.ReportAllocs();for b.Loop(){var value Pointer;if err:=value.ReadJSONInput(raw);err!=nil{b.Fatal(err)};pointerSink=value}})
 b.Run("binary_pointer_missing",func(b *testing.B){raw:=[]byte{0,0};b.ReportAllocs();for b.Loop(){decoder:=codec.NewDecoder(raw);var value Pointer;value.ReadLuxoInput(decoder);if err:=decoder.Finish();err!=nil{b.Fatal(err)};pointerSink=value}})
 b.Run("projection_control",func(b *testing.B){raw:=[]byte{0,0};b.ReportAllocs();for b.Loop(){decoder:=codec.NewDecoder(raw);var value Scalar;value.ReadLuxo(decoder);if err:=decoder.Finish();err!=nil{b.Fatal(err)};scalarSink=value}})
}
`
