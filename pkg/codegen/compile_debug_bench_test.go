package codegen

import (
	"crypto/sha256"
	"testing"
)

func TestDebugBenchmarkArtifactIdentity(t *testing.T) {
	input := analyzePermissionFixture(t, debugBenchmarkSource)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	ids := StableIDs{ModelFields: map[string]map[string]int{"DebugSample": {"id": 1, "name": 2, "enabled": 3}}}
	generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: completeFixtureAPIIDs(input, ids)})
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range generated.Files {
		t.Logf("artifact:%s:%x", name, sha256.Sum256(content))
	}
}

func TestGeneratedDebugBenchmarkControls(t *testing.T) {
	runGeneratedFixture(t, debugBenchmarkSource, debugBenchmarkAssertions, StableIDs{
		ModelFields: map[string]map[string]int{"DebugSample": {"id": 1, "name": 2, "enabled": 3}},
	})
}

const debugBenchmarkSource = `
type DebugSample { id: Int name: String enabled: Boolean }
fn plainNumber(value: Int): Int { return value }
fn tracedNumber(value: Int): Int { return value.d }
fn plainText(value: String): String { return value }
fn tracedText(value: String): String { return value.d }
fn plainNumbers(value: [Int]): [Int] { return value }
fn tracedNumbers(value: [Int]): [Int] { return value.d }
fn plainSample(value: DebugSample): DebugSample { return value }
fn tracedSample(value: DebugSample): DebugSample { return value.d }
`

// The plain control has no diagnostic feature. Enabled-minus-plain is a feature
// cost, not a regression against an equivalent logger. File writes are real.
const debugBenchmarkAssertions = `package generated
import("context";"os";"strings";"testing";"github.com/light-speak/luxo/pkg/lux/luxolog")
var debugNumberSink int64
var debugTextSink string
var debugNumbersSink []int64
var debugSampleSink DebugSample
func TestGeneratedDebugControls(t *testing.T) {
 old:=luxolog.GetLevel();defer luxolog.SetLevel(old)
 if err:=luxolog.SetLevel(luxolog.LevelError);err!=nil{t.Fatal(err)}
 app:=&App{};ctx:=t.Context()
 if value,err:=app.tracedNumber(ctx,7);err!=nil||value!=7{t.Fatal(value,err)}
 if value,err:=app.tracedText(ctx,"text");err!=nil||value!="text"{t.Fatal(value,err)}
 values:=[]int64{1,2};if value,err:=app.tracedNumbers(ctx,values);err!=nil||&value[0]!=&values[0]{t.Fatal(value,err)}
 sample:=DebugSample{Id:7,Name:"sample",Enabled:true}
 if value,err:=app.tracedSample(ctx,sample);err!=nil||value!=sample{t.Fatal(value,err)}
}
func BenchmarkGeneratedDebug(b *testing.B) {
 file,err:=os.OpenFile(os.DevNull,os.O_WRONLY,0);if err!=nil{b.Fatal(err)}
 oldFile,oldLevel:=os.Stderr,luxolog.GetLevel();os.Stderr=file
 b.Cleanup(func(){os.Stderr=oldFile;_ = luxolog.SetLevel(oldLevel);_ = file.Close()})
 app:=&App{};ctx:=context.Background();longText:=strings.Repeat("x",4096)
 numbers:=make([]int64,128);for i:=range numbers{numbers[i]=int64(i)}
 sample:=DebugSample{Id:7,Name:"sample",Enabled:true}
 for _,mode:=range []string{"plain","filtered","enabled"}{
  level:=luxolog.LevelError;if mode=="enabled"{level=luxolog.LevelDebug}
  if err:=luxolog.SetLevel(level);err!=nil{b.Fatal(err)}
  b.Run(mode+"/Int",func(b *testing.B){b.ReportAllocs();for b.Loop(){
   var err error;if mode=="plain"{debugNumberSink,err=app.plainNumber(ctx,42)}else{debugNumberSink,err=app.tracedNumber(ctx,42)};if err!=nil{b.Fatal(err)}
  }})
  for _,text:=range []struct{name,value string}{{"short","message"},{"4KiB",longText}}{b.Run(mode+"/"+text.name,func(b *testing.B){b.ReportAllocs();for b.Loop(){
   var err error;if mode=="plain"{debugTextSink,err=app.plainText(ctx,text.value)}else{debugTextSink,err=app.tracedText(ctx,text.value)};if err!=nil{b.Fatal(err)}
  }})}
  b.Run(mode+"/list128",func(b *testing.B){b.ReportAllocs();for b.Loop(){
   var err error;if mode=="plain"{debugNumbersSink,err=app.plainNumbers(ctx,numbers)}else{debugNumbersSink,err=app.tracedNumbers(ctx,numbers)};if err!=nil{b.Fatal(err)}
  }})
  b.Run(mode+"/object",func(b *testing.B){b.ReportAllocs();for b.Loop(){
   var err error;if mode=="plain"{debugSampleSink,err=app.plainSample(ctx,sample)}else{debugSampleSink,err=app.tracedSample(ctx,sample)};if err!=nil{b.Fatal(err)}
  }})
 }
}
`
