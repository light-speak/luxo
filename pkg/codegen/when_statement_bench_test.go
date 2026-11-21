package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func BenchmarkGenerateGenericWhenControls(b *testing.B) {
	for _, sample := range []struct{ name, body string }{
		{"scalar", "return value"},
		{"native", "return nativeEcho(value)?"},
		{"channel", "val channel: Channel<Int> = Channel(1) channel <- value return <-channel"},
	} {
		b.Run(sample.name, func(b *testing.B) {
			input := analyzePermissionFixture(b, "fn nativeEcho(value: Int): Result<Int> @native\napi probe(value: Int): Int { "+sample.body+" }")
			if len(input.Errors) != 0 {
				b.Fatal(input.Errors)
			}
			config := GeneratorConfig{IDs: completeFixtureAPIIDs(input, StableIDs{})}
			if directory := os.Getenv("LUXO_GENERIC_WHEN_OUTPUT"); directory != "" {
				generated, err := Generate(input, "generated", config)
				if err != nil {
					b.Fatal(err)
				}
				directory = filepath.Join(directory, sample.name)
				if err := os.MkdirAll(directory, 0o755); err != nil {
					b.Fatal(err)
				}
				for name, data := range generated.Files {
					if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Generate(input, "generated", config); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkGenerateChannelSelect(b *testing.B) {
	for _, sample := range []struct {
		name, source            string
		beforeError, afterError bool
	}{
		{"scalar-control", "api run(value: Int): Int { return value }", false, false},
		{"channel", "api run(): Int { val channel: Channel<Int> = Channel(1) channel <- 42 return when { <-channel -> { item -> item } else -> 0 } }", false, false},
		{"nullable", "api run(value: Int?): Int { val channel: Channel<Int?> = Channel(1) channel <- value return when { <-channel -> { item -> if item != null { return item + 1 } 42 } else -> 0 } }", false, false},
		{"native", channelBindingNativeSource + "\napi run(flag: Boolean, value: Int?): Int { val channel = source(flag,value)? return when { <-channel -> { item -> if item != null { return item + 1 } 42 } else -> 0 } }", false, false},
		{"inline", channelBindingFunctionSource + "\napi run(flag: Boolean, value: Int?): Int { return when { <-source(flag,value) -> { item -> if item != null { return item + 1 } 42 } else -> 0 } }", true, false},
		{"nested", "api run(): Int { val channel: Channel<Int?> = Channel(1) val outer: Channel<Channel<Int?>> = Channel(1) outer <- channel return when { <-outer -> { message -> message <- 7 val item = <-message return item ?: 0 } else -> 0 } }", false, false},
		{"nine-cases", "api run(): Int { val channel: Channel<Int> = Channel(1) channel <- 42 return when {\n" + strings.Repeat("<-channel -> { item -> item }\n", 9) + "else -> 0 } }", false, false},
		{"invalid-ast", "api run(): Int { val channel: Channel<Int> = Channel(1) return when { <-channel -> { item -> item } <-channel -> { other -> other } else -> 0 } }", false, true},
	} {
		b.Run(sample.name, func(b *testing.B) {
			input := analyzePermissionFixture(b, sample.source)
			if len(input.Errors) != 0 {
				b.Fatal(input.Errors)
			}
			if sample.name == "invalid-ast" {
				body := input.Files[0].APIs[0].Body
				expr := body.Stmts[len(body.Stmts)-1].(*ast.ReturnStmt).Value.(*ast.WhenExpr)
				expr.Branches[0].Condition = &ast.Ident{Name: "invalid"}
			}
			config := GeneratorConfig{Driver: DriverPG, IDs: completeFixtureAPIIDs(input, StableIDs{APIs: map[string]int{"run": 1}})}
			expectedError := sample.afterError
			if os.Getenv("LUXO_SELECT_BENCH_BASELINE") == "1" {
				expectedError = sample.beforeError
			}
			b.ReportAllocs()
			for b.Loop() {
				output, err := Generate(input, "generated", config)
				if (err != nil) != expectedError || (output == nil) != (err != nil) {
					b.Fatalf("inconsistent output=%t error=%v", output != nil, err)
				}
			}
		})
	}
}

const discardedWhenBenchmarkSource = `package generated
import("context";"os";"slices";"testing";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")

func(app *App) referenceAssigned(_ context.Context,flag bool)(int64,error){
 state:=State{Value:10};if flag{state.Value++}else{state.Value+=2};return state.Value,nil
}
func(app *App) referenceEmpty(_ context.Context,flag bool)(int64,error){
 state:=State{Value:10};if !flag{state.Value=42};return state.Value,nil
}
func(app *App) referenceLocalReturn(_ context.Context,flag bool)(int64,error){
 state:=State{Value:10};if flag{state.Value=41}else{state.Value=42};return state.Value,nil
}
func(app *App) referenceNested(_ context.Context,flag bool)(int64,error){
 state:=State{Value:10};if flag{state.Value=1};return state.Value,nil
}
func(app *App) referenceCondition(ctx context.Context,value int64)(int64,error){
 state:=State{Value:10};accepted,err:=app.accepted(ctx,value);if err!=nil{return 0,err}
 if accepted{state.Value=41}else{state.Value=42};return state.Value,nil
}
func(app *App) referenceBranchError(_ context.Context,value int64)(int64,error){
 if value>0{return 0,luxerrors.BadRequest};return 42,nil
}
func(app *App) referenceChannel(_ context.Context,ready bool)(int64,error){
 state:=State{Value:10};capacity:=int64(1)
 if capacity<0||uint64(capacity)>uint64(^uint(0)>>1){return 0,luxerrors.BadRequest}
 channel:=make(chan int64,capacity);if ready{channel<-42}
 select{case value:=<-channel:state.Value=value;default:};return state.Value,nil
}

func BenchmarkDiscardedWhen(b *testing.B){
 app:=&App{};benchWhenBooleans(b,app);benchWhenIntegers(b,app)
}

func benchWhenBooleans(b *testing.B,app *App){
 for _,sample:=range []struct{
  name string
  generated,reference func(context.Context,bool)(int64,error)
  whenTrue,whenFalse int64
 }{
  {"assigned",app.assigned,app.referenceAssigned,11,12},
  {"empty",app.emptyBranch,app.referenceEmpty,10,42},
  {"local-return",app.localReturn,app.referenceLocalReturn,41,42},
  {"nested-value",app.nestedValue,app.referenceNested,1,10},
  {"channel-lifecycle",app.channelStatement,app.referenceChannel,42,10},
 }{
  b.Run(sample.name,func(b *testing.B){
   calls:=[]struct{name string;call func(context.Context,bool)(int64,error)}{{"reference",sample.reference},{"generated",sample.generated}}
   if os.Getenv("LUXO_WHEN_BENCH_ORDER")=="generated-first"{slices.Reverse(calls)}
   for _,target:=range calls{b.Run(target.name,func(b *testing.B){
    ctx:=b.Context();flag:=false;b.ReportAllocs()
    for b.Loop(){
     flag=!flag;value,err:=target.call(ctx,flag);want:=sample.whenFalse;if flag{want=sample.whenTrue}
     if err!=nil||value!=want{b.Fatal(value,err)}
    }
   })}
  })
 }
}

func benchWhenIntegers(b *testing.B,app *App){
 for _,sample:=range []struct{
  name string
  generated,reference func(context.Context,int64)(int64,error)
  input,want int64
  expectedError error
 }{
  {"condition-success",app.conditionError,app.referenceCondition,1,41,nil},
  {"condition-error",app.conditionError,app.referenceCondition,-1,0,luxerrors.BadRequest},
  {"branch-success",app.branchError,app.referenceBranchError,0,42,nil},
  {"branch-error",app.branchError,app.referenceBranchError,1,0,luxerrors.BadRequest},
 }{
  b.Run(sample.name,func(b *testing.B){
   calls:=[]struct{name string;call func(context.Context,int64)(int64,error)}{{"reference",sample.reference},{"generated",sample.generated}}
   if os.Getenv("LUXO_WHEN_BENCH_ORDER")=="generated-first"{slices.Reverse(calls)}
   for _,target:=range calls{b.Run(target.name,func(b *testing.B){
    ctx:=b.Context();b.ReportAllocs()
    for b.Loop(){value,err:=target.call(ctx,sample.input);if err!=sample.expectedError||value!=sample.want{b.Fatal(value,err)}}
   })}
  })
 }
}
`

const whenInfallibleCostSource = `api probe(value: Int): Int {
 return when { value > 0 -> value else -> 0 }
}`

const whenBareThrowCostSource = `api probe(value: Int): Int {
 return when { value > 0 -> value else -> throw error.BadRequest }
}`

func BenchmarkGenerateWhenExpressionThrows(b *testing.B) {
	for _, sample := range []struct{ name, source string }{
		{"infallible", whenInfallibleCostSource},
		{"bare-throw", whenBareThrowCostSource},
	} {
		b.Run(sample.name, func(b *testing.B) {
			input := analyzePermissionFixture(b, sample.source)
			if len(input.Errors) != 0 {
				b.Fatal(input.Errors)
			}
			config := GeneratorConfig{Driver: DriverPG, IDs: completeFixtureAPIIDs(input, StableIDs{})}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Generate(input, "generated", config); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
