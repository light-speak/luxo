package codegen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func BenchmarkPreparedDefaultGeneration(b *testing.B) {
	for _, count := range []int{0, 1, 65} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			source := "model Probe @noTime { id: Int @id\n"
			fields := map[string]int{"id": 1}
			for index := range max(count, 1) {
				name := "field" + strconv.Itoa(index)
				declaration := "Int @beforeSave { it + 1 }"
				if count > 0 {
					declaration = "Int = 0 @beforeSave { it + 1 }"
				}
				source += name + ": " + declaration + "\n"
				fields[name] = index + 2
			}
			input := analyzePermissionFixture(b, source+"}")
			config := GeneratorConfig{Driver: DriverPG, IDs: StableIDs{ModelFields: map[string]map[string]int{"Probe": fields}}}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Generate(input, "generated", config); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestBuildPreparedDefaultRuntimeBenchmark(t *testing.T) {
	output := os.Getenv("LUXO_PREPARED_DEFAULT_BENCH_BINARY")
	if output == "" {
		t.Skip("generated benchmark fixture")
	}
	root, err := filepath.Abs("../../.tmp/audit-default-preparation")
	if err != nil || filepath.Dir(output) != root {
		t.Fatal("benchmark output must stay inside its owned directory", output, err)
	}
	input := analyzePermissionFixture(t, preparedDefaultBenchmarkSource())
	ids := StableIDs{ModelFields: map[string]map[string]int{}}
	for _, model := range input.Files[0].Models {
		fields := map[string]int{}
		for index, field := range model.Fields {
			fields[field.Name] = index + 1
		}
		ids.ModelFields[model.Name] = fields
	}
	generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	prefix := preparedDefaultBenchmarkPrefix(input.Files[0].Models)
	dir := writeProtocolFixture(t, "package generated\n", preparedDefaultRuntimeBenchmark)
	for name, data := range generated.Files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "prefix_test.go"), []byte(prefix), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hash_probe_unix_test.go"), []byte(preparedDefaultHashProbe), 0600); err != nil {
		t.Fatal(err)
	}
	archivePreparedDefaultRuntime(t, dir, output+"-sources", generated.Files)
	command := exec.Command("go", "test", "-c", "-o", output, ".")
	command.Dir = dir
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatal(string(data), err)
	}
}

func preparedDefaultBenchmarkPrefix(models []*ast.ModelDecl) string {
	var source strings.Builder
	source.WriteString("package generated\n")
	for _, model := range models {
		if len(preparedCreateDefaults(model.Fields, model.Directives)) > 1 {
			source.WriteString("import(\"math/bits\";\"slices\")\n")
			break
		}
	}
	for _, model := range models {
		fmt.Fprintf(&source, "func prefix%s(b *%sCreateBuilder) (*%s,error) {\n", model.Name, model.Name, model.Name)
		if hasFallibleSave(model.Fields) {
			source.WriteString("if b._prepareErr != nil { return nil,b._prepareErr }\n")
		}
		source.WriteString("\tcreate := b.CreateBase\n")
		if defaults := preparedCreateDefaults(model.Fields, model.Directives); len(defaults) > 0 {
			defaultGenerator().generatePreparedCreateDefaults(&source, model.Name, defaults, nil)
		}
		source.WriteString("prefixColsSink = create.Cols\nprefixValsSink = create.Vals\nreturn nil,nil\n}\n")
	}
	return source.String()
}

func archivePreparedDefaultRuntime(t *testing.T, directory, archive string, files map[string][]byte) {
	t.Helper()
	if err := os.MkdirAll(archive, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"db.gen.go", "model.gen.go", "app.gen.go", "prefix_test.go", "wire_test.go", "hash_probe_unix_test.go"} {
		data := files[name]
		if data == nil {
			var err error
			data, err = os.ReadFile(filepath.Join(directory, name))
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(archive, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func preparedDefaultBenchmarkSource() string {
	source := `fn checked(value: String): String { return value + "!" }
model Plain @noTime { id: Int @id value: String }
model Builtin @noTime { id: Int @id value: String = "raw" @beforeSave { it + "!" } }
model Checked @noTime { id: Int @id value: String = "raw" @beforeSave { checked(it) } }
model Hashed @noTime { id: Int @id password: String = "secret" @hash }
model Wide @noTime { id: Int @id
`
	for index := range 65 {
		source += "field" + strconv.Itoa(index) + ": Int = 0 @beforeSave { it + 1 }\n"
	}
	return source + "}\n"
}

const preparedDefaultRuntimeBenchmark = `package generated
import("strconv";"testing";"github.com/light-speak/luxo/pkg/lux/pg")
var prefixColsSink []string
var prefixValsSink []any
func prefixBase[T any](capacity int)pg.CreateBase[T]{return pg.CreateBase[T]{Cols:append(make([]string,0,capacity),"id"),Vals:append(make([]any,0,capacity),int64(1))}}
func BenchmarkPreparedDefaultPrefix(b *testing.B){
 b.Run("none_control",func(b *testing.B){target:=&PlainCreateBuilder{CreateBase:prefixBase[Plain](1)};b.ReportAllocs();for b.Loop(){if _,err:=prefixPlain(target);err!=nil{b.Fatal(err)}}})
 for _,capacity:=range []int{1,4}{b.Run("builtin_missing_cap"+strconv.Itoa(capacity),func(b *testing.B){target:=&BuiltinCreateBuilder{CreateBase:prefixBase[Builtin](capacity)};b.ReportAllocs();for b.Loop(){if _,err:=prefixBuiltin(target);err!=nil{b.Fatal(err)}}})}
 b.Run("builtin_present",func(b *testing.B){target:=(&BuiltinCreateBuilder{CreateBase:prefixBase[Builtin](4)}).SetValue("given");b.ReportAllocs();for b.Loop(){if _,err:=prefixBuiltin(target);err!=nil{b.Fatal(err)}}})
 b.Run("checked_missing",func(b *testing.B){target:=&CheckedCreateBuilder{CreateBase:prefixBase[Checked](1)};b.ReportAllocs();for b.Loop(){if _,err:=prefixChecked(target);err!=nil{b.Fatal(err)}}})
 b.Run("checked_present",func(b *testing.B){target:=(&CheckedCreateBuilder{CreateBase:prefixBase[Checked](4)}).SetValue("given");b.ReportAllocs();for b.Loop(){if _,err:=prefixChecked(target);err!=nil{b.Fatal(err)}}})
 b.Run("wide_missing",func(b *testing.B){target:=&WideCreateBuilder{CreateBase:prefixBase[Wide](1)};b.ReportAllocs();for b.Loop(){if _,err:=prefixWide(target);err!=nil{b.Fatal(err)}}})
 b.Run("wide_present",func(b *testing.B){target:=&WideCreateBuilder{CreateBase:prefixBase[Wide](66)};for index:=range 65{target.Set("field"+strconv.Itoa(index),int64(1))};b.ReportAllocs();for b.Loop(){if _,err:=prefixWide(target);err!=nil{b.Fatal(err)}}})
}
func BenchmarkPreparedDefaultHash(b *testing.B){
 for _,explicit:=range []bool{false,true}{b.Run(strconv.FormatBool(explicit),func(b *testing.B){base:=prefixBase[Hashed](2);warm:=(&HashedCreateBuilder{CreateBase:base}).SetPassword("secret");if warm._prepareErr!=nil{b.Fatal(warm._prepareErr)};b.ReportAllocs();for b.Loop(){target:=&HashedCreateBuilder{CreateBase:base};if explicit{target.SetPassword("secret")};if _,err:=prefixHashed(target);err!=nil{b.Fatal(err)}};if len(prefixValsSink)!=2{b.Fatal("hash missing")}})}
}
`

const preparedDefaultHashProbe = `//go:build darwin || linux

package generated
import("encoding/json";"os";"path/filepath";"runtime";"syscall";"testing";"time";"golang.org/x/crypto/bcrypt";"github.com/light-speak/luxo/pkg/lux/pg")
type hashProbeSample struct {
 Mode string
 Pair int
 Order int
 Explicit bool
 WallNS int64
 UserNS int64
 SystemNS int64
 Bytes uint64
 Allocations uint64
 GCs uint32
 Cost int
}
func TestPreparedHashPaired(t *testing.T){
 output:=os.Getenv("LUXO_HASH_PAIRED_OUTPUT");if output==""{t.Skip("paired hash probe")}
 executable,err:=os.Executable();if err!=nil{t.Fatal(err)}
 if filepath.Dir(output)!=filepath.Dir(executable){t.Fatal("probe output must stay beside its owned executable")}
 base:=prefixBase[Hashed](2)
 for _,explicit:=range []bool{false,true}{target:=&HashedCreateBuilder{CreateBase:base};if explicit{target.SetPassword("secret")};if _,err:=prefixHashed(target);err!=nil{t.Fatal(err)}}
 var results []hashProbeSample
 for pair:=range 12 {
  order:=[]bool{false,true};if pair%2!=0{order=[]bool{true,false}}
  for step,explicit:=range order{results=append(results,measurePreparedHash(t,base,"default-explicit",pair,step,explicit))}
  if pair%2==0{for step:=range 2{results=append(results,measurePreparedHash(t,base,"explicit-explicit",pair,step,true))}}
 }
 data,err:=json.MarshalIndent(results,"","  ");if err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(output,append(data,'\n'),0600);err!=nil{t.Fatal(err)}
}
func measurePreparedHash(t *testing.T,base pg.CreateBase[Hashed],mode string,pair,order int,explicit bool)hashProbeSample{
 t.Helper()
 var before,after runtime.MemStats
 var usageBefore,usageAfter syscall.Rusage
 runtime.ReadMemStats(&before)
 if err:=syscall.Getrusage(syscall.RUSAGE_SELF,&usageBefore);err!=nil{t.Fatal(err)}
 start:=time.Now()
 target:=&HashedCreateBuilder{CreateBase:base}
 if explicit{target.SetPassword("secret")}
 _,err:=prefixHashed(target)
 elapsed:=time.Since(start)
 if usageErr:=syscall.Getrusage(syscall.RUSAGE_SELF,&usageAfter);usageErr!=nil{t.Fatal(usageErr)}
 runtime.ReadMemStats(&after)
 if err!=nil{t.Fatal(err)}
 if len(prefixValsSink)!=2{t.Fatal("missing hash")}
 encoded,ok:=prefixValsSink[1].(string);if !ok{t.Fatal("hash is not a string")}
 cost,err:=bcrypt.Cost([]byte(encoded));if err!=nil||cost!=bcrypt.DefaultCost{t.Fatal("wrong hash cost",cost,err)}
 return hashProbeSample{Mode:mode,Pair:pair,Order:order,Explicit:explicit,WallNS:int64(elapsed),UserNS:usageAfter.Utime.Nano()-usageBefore.Utime.Nano(),SystemNS:usageAfter.Stime.Nano()-usageBefore.Stime.Nano(),Bytes:after.TotalAlloc-before.TotalAlloc,Allocations:after.Mallocs-before.Mallocs,GCs:after.NumGC-before.NumGC,Cost:cost}
}
`
