package codegen

const integerArithmeticBenchmarks = `package generated
import("context";"math";"testing";"time")
var integerBenchmarkSink int64
var integerBenchmarkError error
func BenchmarkGeneratedIntegerArithmetic(b *testing.B){
 app:=&App{};ctx:=context.Background()
 for _,sample:=range []struct{name string;call func(context.Context,int64,int64)(int64,error);left,right int64}{
  {"add",app.add,40,2},{"subtract",app.subtract,40,-2},{"multiply",app.multiply,-7,-6},{"divide",app.divide,-5,2},{"remainder",app.remainder,-5,2},
 }{b.Run(sample.name,func(b *testing.B){b.ReportAllocs();for b.Loop(){value,err:=sample.call(ctx,sample.left,sample.right);if err!=nil{b.Fatal(err)};integerBenchmarkSink=value}})}
 b.Run("negate",func(b *testing.B){call:=app.negate;b.ReportAllocs();for b.Loop(){value,err:=call(ctx,42);if err!=nil{b.Fatal(err)};integerBenchmarkSink=value}})
 b.Run("duration-add",func(b *testing.B){call:=app.plusDuration;b.ReportAllocs();for b.Loop(){value,err:=call(ctx,time.Second,time.Millisecond);if err!=nil{b.Fatal(err)};integerBenchmarkSink=int64(value)}})
 b.Run("duration-unit",func(b *testing.B){call:=app.fromDays;b.ReportAllocs();for b.Loop(){value,err:=call(ctx,2);if err!=nil{b.Fatal(err)};integerBenchmarkSink=int64(value)}})
 b.Run("short-circuit",func(b *testing.B){call:=app.guarded;b.ReportAllocs();for b.Loop(){value,err:=call(ctx,false,math.MaxInt64,1);if err!=nil||value{b.Fatal(value,err)}}})
 b.Run("compound",func(b *testing.B){call:=app.compound;box:=&ArithmeticBox{};b.ReportAllocs();for b.Loop(){box.Value=3;value,err:=call(ctx,box);if err!=nil||value!=4{b.Fatal(value,err)};integerBenchmarkSink=value}})
}
func BenchmarkGeneratedIntegerFailure(b *testing.B){
 app:=&App{};ctx:=context.Background()
 for _,sample:=range []struct{name string;call func(context.Context,int64,int64)(int64,error);left,right int64}{
  {"overflow",app.add,math.MaxInt64,1},{"divide-zero",app.divide,1,0},
 }{b.Run(sample.name,func(b *testing.B){b.ReportAllocs();for b.Loop(){_,err:=sample.call(ctx,sample.left,sample.right);if err==nil{b.Fatal("failure lost")};integerBenchmarkError=err}})}
}
`

const integerDateBenchmarks = `package generated
import("context";"testing";"time")
var integerDateSink time.Time
func BenchmarkGeneratedDateSubtract(b *testing.B){
 app:=&App{};call:=app.dateSubtract;ctx:=context.Background();instant:=time.Unix(1700000000,123456789).UTC()
 b.ReportAllocs()
 for b.Loop(){value,err:=call(ctx,instant,time.Second);if err!=nil{b.Fatal(err)};integerDateSink=value}
}
`

const integerConditionBenchmarks = `package generated
import("os";"testing")
var integerConditionSink bool
func uncheckedIntegerMatcher(value int64,params *WatchStreamParams,_ any)(bool,error){return value+params.Limit>0,nil}
func BenchmarkGeneratedConditionArithmetic(b *testing.B){
 calls:=[]struct{name string;call func(int64,*WatchStreamParams,any)(bool,error)}{{"unchecked",uncheckedIntegerMatcher},{"generated",matchWatch}}
 if os.Getenv("LUXO_REVERSE_BENCH")=="1"{calls[0],calls[1]=calls[1],calls[0]}
 params:=&WatchStreamParams{Limit:2}
 for _,candidate:=range calls{b.Run(candidate.name,func(b *testing.B){b.ReportAllocs();for b.Loop(){value,err:=candidate.call(40,params,nil);if err!=nil||!value{b.Fatal(value,err)};integerConditionSink=value}})}
}
`
