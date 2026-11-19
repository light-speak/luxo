package codegen

import (
	"fmt"
	goast "go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const automaticBenchmarkSource = `
fn shiftDate(value: DateTime): DateTime { return value + 1000h }
model Timed { id: Int @id value: Int createdAt: DateTime updatedAt: DateTime }
model Shifted { id: Int @id value: Int createdAt: DateTime @beforeSave { it + 1000h } updatedAt: DateTime @beforeSave { it + 1000h } }
model Checked { id: Int @id value: Int createdAt: DateTime @beforeSave { shiftDate(it) } updatedAt: DateTime @beforeSave { shiftDate(it) } }
`

func BenchmarkAutomaticSaveGeneration(b *testing.B) {
	for _, count := range []int{1, 128} {
		for _, hooked := range []bool{false, true} {
			b.Run(strconv.Itoa(count)+"/hook="+strconv.FormatBool(hooked), func(b *testing.B) {
				var source strings.Builder
				ids := StableIDs{ModelFields: map[string]map[string]int{}}
				for index := range count {
					name := "Record" + strconv.Itoa(index)
					hook := ""
					if hooked {
						hook = " @beforeSave { it + 1s }"
					}
					fmt.Fprintf(&source, "model %s { id: Int @id value: Int createdAt: DateTime%s updatedAt: DateTime%s }\n", name, hook, hook)
					ids.ModelFields[name] = map[string]int{"id": 1, "value": 2, "createdAt": 3, "updatedAt": 4}
				}
				input := analyzePermissionFixture(b, source.String())
				b.ReportAllocs()
				for b.Loop() {
					if _, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: ids}); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func TestBuildAutomaticSaveRuntimeBenchmark(t *testing.T) {
	output := os.Getenv("LUXO_AUTO_SAVE_BENCH_OUTPUT")
	if output == "" {
		t.Skip("generated benchmark fixture")
	}
	root, err := filepath.Abs("../../.tmp/audit-auto-save")
	if err != nil || filepath.Dir(output) != root {
		t.Fatal("benchmark output must stay inside its owned directory", output, err)
	}
	buildAutomaticRuntimeBenchmark(t, output, automaticBenchmarkSource, automaticRuntimeBenchmark)
}

func buildAutomaticRuntimeBenchmark(t *testing.T, output, source, benchmark string) {
	t.Helper()
	input := analyzePermissionFixture(t, source)
	ids := StableIDs{ModelFields: map[string]map[string]int{}}
	for _, model := range input.Files[0].Models {
		fields := map[string]int{}
		for index, field := range model.Fields {
			fields[field.Name] = index + 1
		}
		ids.ModelFields[model.Name] = fields
	}
	ids = completeFixtureAPIIDs(input, ids)
	generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	archive := output + "-sources"
	if err := os.MkdirAll(archive, 0755); err != nil {
		t.Fatal(err)
	}
	dir := writeProtocolFixture(t, "package generated\n", benchmark)
	for name, data := range generated.Files {
		writeAutomaticBenchmarkFile(t, archive, name, data)
		if name == "db.gen.go" {
			data = instrumentAutomaticBenchmark(t, data)
		}
		writeAutomaticBenchmarkFile(t, dir, name, data)
	}
	writeAutomaticBenchmarkFile(t, archive, "instrumented_db.gen.go", instrumentAutomaticBenchmark(t, generated.Files["db.gen.go"]))
	writeAutomaticBenchmarkFile(t, archive, "wire_test.go", []byte(benchmark))
	command := exec.Command("go", "test", "-c", "-o", output, ".")
	command.Dir = dir
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatal(string(data), err)
	}
}

func writeAutomaticBenchmarkFile(t *testing.T, directory, name string, data []byte) {
	t.Helper()
	if filepath.Base(name) != name {
		t.Fatal("unexpected generated path", name)
	}
	if err := os.WriteFile(filepath.Join(directory, name), data, 0600); err != nil {
		t.Fatal(err)
	}
}

// Replace only the terminal database call; all generated preparation remains.
func instrumentAutomaticBenchmark(t *testing.T, source []byte) []byte {
	t.Helper()
	source = slices.Clone(source)
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, "db.gen.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range slices.Backward(file.Decls) {
		function, ok := declaration.(*goast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		replacement := automaticBenchmarkReturn(function)
		if replacement == "" {
			continue
		}
		last := function.Body.List[len(function.Body.List)-1]
		if _, ok := last.(*goast.ReturnStmt); !ok {
			t.Fatal("database boundary is no longer a terminal return", function.Name)
		}
		start, end := positions.Position(last.Pos()).Offset, positions.Position(last.End()).Offset
		source = slices.Replace(source, start, end, []byte(replacement)...)
		// Keep the removed SQL call from creating an artificial inlining gap.
		start = positions.Position(function.Pos()).Offset
		source = slices.Insert(source, start, []byte("//go:noinline\n")...)
	}
	return source
}

func automaticBenchmarkReturn(function *goast.FuncDecl) string {
	if function.Recv == nil {
		return ""
	}
	pointer, ok := function.Recv.List[0].Type.(*goast.StarExpr)
	if !ok {
		return ""
	}
	receiver, ok := pointer.X.(*goast.Ident)
	if !ok || strings.Contains(receiver.Name, "QueryUpdateBuilder") {
		return ""
	}
	switch {
	case function.Name.Name == "updatePrepared":
		return "automaticSetsSink = sets; return 0, nil"
	case function.Name.Name != "Exec" && function.Name.Name != "ExecWhere":
		return ""
	case strings.HasSuffix(receiver.Name, "CreateManyBuilder"):
		return "automaticColsSink = cols; automaticRowsSink = rows; return nil, nil"
	case strings.HasSuffix(receiver.Name, "CreateBuilder"):
		return "automaticColsSink = create.Cols; automaticValsSink = create.Vals; return nil, nil"
	case strings.HasSuffix(receiver.Name, "UpdateBuilder"):
		return "automaticSetsSink = update.Sets; return nil, nil"
	}
	return ""
}

const automaticRuntimeBenchmark = `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux";"github.com/light-speak/luxo/pkg/lux/pg")
var automaticColsSink []string
var automaticValsSink []any
var automaticSetsSink []lux.SetField
var automaticRowsSink [][]any
func automaticCreateBase[T any](capacity int)pg.CreateBase[T]{return pg.CreateBase[T]{Cols:append(make([]string,0,capacity),"id","value"),Vals:append(make([]any,0,capacity),int64(1),int64(1))}}
func BenchmarkAutomaticRuntime(b *testing.B){
 for _,capacity:=range []int{2,4}{
  name:="spare";if capacity==2{name="grow"}
  b.Run("create/control/"+name,func(b *testing.B){target:=&TimedCreateBuilder{CreateBase:automaticCreateBase[Timed](capacity)};b.ReportAllocs();for b.Loop(){if _,err:=target.Exec(b.Context());err!=nil{b.Fatal(err)}}})
  b.Run("create/builtin/"+name,func(b *testing.B){target:=&ShiftedCreateBuilder{CreateBase:automaticCreateBase[Shifted](capacity)};b.ReportAllocs();for b.Loop(){if _,err:=target.Exec(b.Context());err!=nil{b.Fatal(err)}}})
  b.Run("create/function/"+name,func(b *testing.B){target:=&CheckedCreateBuilder{CreateBase:automaticCreateBase[Checked](capacity)};b.ReportAllocs();for b.Loop(){if _,err:=target.Exec(b.Context());err!=nil{b.Fatal(err)}}})
 }
 b.Run("update/builtin",func(b *testing.B){target:=newShiftedUpdateBuilder(nil,"shifteds",1);target.Sets=make([]lux.SetField,0,2);target.SetValue(1);b.ReportAllocs();for b.Loop(){if _,err:=target.Exec(b.Context());err!=nil{b.Fatal(err)}}})
 b.Run("update/function",func(b *testing.B){target:=newCheckedUpdateBuilder(nil,"checkeds",1);target.Sets=make([]lux.SetField,0,2);target.SetValue(1);b.ReportAllocs();for b.Loop(){if _,err:=target.Exec(b.Context());err!=nil{b.Fatal(err)}}})
 b.Run("query/builtin",func(b *testing.B){target:=(&ShiftedQuery{}).Update();target.state.Sets=make([]lux.SetField,0,2);target.SetValue(1);b.ReportAllocs();for b.Loop(){if _,err:=target.Exec(b.Context());err!=nil{b.Fatal(err)}}})
 b.Run("dsl/builtin",func(b *testing.B){target:=&ShiftedQuery{};sets:=[]lux.SetField{{Col:"value",Val:int64(1)}};b.ReportAllocs();for b.Loop(){if _,err:=target.updatePrepared(b.Context(),sets...);err!=nil{b.Fatal(err)}}})
 b.Run("batch/builtin/128",func(b *testing.B){target:=&ShiftedCreateManyBuilder{rows:make([]ShiftedCreateInput,128)};b.ReportAllocs();for b.Loop(){if _,err:=target.Exec(b.Context());err!=nil{b.Fatal(err)}}})
 b.Run("batch/function/128",func(b *testing.B){target:=&CheckedCreateManyBuilder{rows:make([]CheckedCreateInput,128)};b.ReportAllocs();for b.Loop(){if _,err:=target.Exec(b.Context());err!=nil{b.Fatal(err)}}})
}
`
