package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func BenchmarkUUIDGeneration(b *testing.B) {
	for _, count := range []int{1, 128} {
		var source strings.Builder
		ids := StableIDs{ModelFields: map[string]map[string]int{}}
		for index := range count {
			name := "Record" + strconv.Itoa(index)
			fmt.Fprintf(&source, "model %s @noTime { id: UUID @id @auto value: Int }\n", name)
			ids.ModelFields[name] = map[string]int{"id": 1, "value": 2}
		}
		input := analyzePermissionFixture(b, source.String())
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: ids}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestBuildUUIDRuntimeBenchmark(t *testing.T) {
	output := os.Getenv("LUXO_UUID_BENCH_OUTPUT")
	if output == "" {
		t.Skip("generated benchmark fixture")
	}
	root, err := filepath.Abs("../../.tmp/audit-uuid-errors")
	if err != nil || filepath.Dir(output) != root {
		t.Fatal("benchmark output must stay inside its owned directory", output, err)
	}
	buildAutomaticRuntimeBenchmark(t, output, uuidFailureSource, uuidRuntimeBenchmark)
}

const uuidRuntimeBenchmark = `package generated
import("testing";"github.com/google/uuid";"github.com/light-speak/luxo/pkg/lux";"github.com/light-speak/luxo/pkg/lux/pg")
var automaticColsSink []string
var automaticValsSink []any
var automaticSetsSink []lux.SetField
var automaticRowsSink [][]any
var uuidResultSink uuid.UUID
func uuidCreateBase(capacity int)pg.CreateBase[Entry]{return pg.CreateBase[Entry]{Cols:append(make([]string,0,capacity),"value"),Vals:append(make([]any,0,capacity),int64(1))}}
func BenchmarkUUIDRuntime(b *testing.B){
 b.Run("raw_control",func(b *testing.B){b.ReportAllocs();for b.Loop(){value,err:=uuid.NewV7();if err!=nil{b.Fatal(err)};uuidResultSink=value}})
 for _,capacity:=range []int{1,2}{
  name:="spare";if capacity==1{name="grow"}
  b.Run("create/"+name,func(b *testing.B){target:=&EntryCreateBuilder{CreateBase:uuidCreateBase(capacity)};b.ReportAllocs();for b.Loop(){if _,err:=target.Exec(b.Context());err!=nil{b.Fatal(err)}}})
 }
 b.Run("create/two",func(b *testing.B){target:=&TwinCreateBuilder{CreateBase:pg.CreateBase[Twin]{Cols:make([]string,0,2),Vals:make([]any,0,2)}};b.ReportAllocs();for b.Loop(){if _,err:=target.Exec(b.Context());err!=nil{b.Fatal(err)}}})
 b.Run("create/prepared",func(b *testing.B){target:=&PreparedUUIDCreateBuilder{CreateBase:pg.CreateBase[PreparedUUID]{Cols:make([]string,0,1),Vals:make([]any,0,1)}};b.ReportAllocs();for b.Loop(){if _,err:=target.Exec(b.Context());err!=nil{b.Fatal(err)}}})
 b.Run("batch/one/128",func(b *testing.B){target:=&EntryCreateManyBuilder{rows:make([]EntryCreateInput,128)};b.ReportAllocs();for b.Loop(){if _,err:=target.Exec(b.Context());err!=nil{b.Fatal(err)}}})
 b.Run("batch/two/128",func(b *testing.B){target:=&TwinCreateManyBuilder{rows:make([]TwinCreateInput,128)};b.ReportAllocs();for b.Loop(){if _,err:=target.Exec(b.Context());err!=nil{b.Fatal(err)}}})
 b.Run("batch/prepared/128",func(b *testing.B){target:=&PreparedUUIDCreateManyBuilder{rows:make([]PreparedUUIDCreateInput,128)};b.ReportAllocs();for b.Loop(){if _,err:=target.Exec(b.Context());err!=nil{b.Fatal(err)}}})
}
`
