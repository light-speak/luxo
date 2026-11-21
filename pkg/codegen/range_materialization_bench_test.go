package codegen

import "testing"

func BenchmarkGenerateMaterializedRange(b *testing.B) {
	input := analyzePermissionFixture(b, rangeMaterializationSource)
	if len(input.Errors) != 0 {
		b.Fatal(input.Errors)
	}
	config := GeneratorConfig{Driver: DriverPG, IDs: completeFixtureAPIIDs(input, StableIDs{
		APIs: map[string]int{"rangeList": 101, "rangeThroughFunction": 102},
		APIParams: map[string]map[string]int{
			"rangeList": {"start": 1, "end": 2}, "rangeThroughFunction": {"start": 1, "end": 2},
		},
	})}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Generate(input, "generated", config); err != nil {
			b.Fatal(err)
		}
	}
}

const rangeMaterializationBenchmarks = `package generated
import("context";"math";"testing")
var rangeValuesSink []int64
var rangeFailureSink error
func BenchmarkGeneratedMaterializedRange(b *testing.B) {
 app:=&App{};ctx:=context.Background()
 for _,sample:=range []struct{name string;start,end int64;failure bool}{
  {"empty",1,0,false},{"single",42,42,false},{"cross_zero",-64,63,false},
  {"items_4096",0,4095,false},{"maximum",math.MaxInt64,math.MaxInt64,false},
  {"overflow",math.MinInt64,math.MaxInt64,true},
 }{b.Run(sample.name,func(b *testing.B){
  b.ReportAllocs()
  for b.Loop(){
   values,err:=app.materializedRange(ctx,sample.start,sample.end)
   if (err!=nil)!=sample.failure{b.Fatal("unexpected range result",err)}
   rangeValuesSink,rangeFailureSink=values,err
  }
 })}
}
`
