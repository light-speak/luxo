package codegen

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
)

func BenchmarkGenerateNumericControl(b *testing.B) {
	for _, scalar := range []string{"Int", "Float"} {
		b.Run(scalar, func(b *testing.B) {
			source := "fn add(left: " + scalar + ", right: " + scalar + "): " + scalar + " { return left + right }"
			tokens, lexErrors := lexer.New(source, "numeric-control.luxo").Tokenize()
			file, parseErrors := parser.New(tokens).Parse("numeric-control.luxo")
			if len(lexErrors) != 0 || len(parseErrors) != 0 {
				b.Fatal(lexErrors, parseErrors)
			}
			input := semantic.Analyze([]*ast.File{file})
			if len(input.Errors) != 0 {
				b.Fatal(input.Errors)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

const floatRemainderBenchmarks = `package generated
import("context";"math";"os";"testing")
var remainderBenchmarkSink float64
func(app *App)handwrittenRemainder(_ context.Context,left,right float64)(float64,error){return math.Mod(left,right),nil}
func(app *App)handwrittenMixedFI(_ context.Context,left float64,right int64)(float64,error){return math.Mod(left,float64(right)),nil}
func(app *App)handwrittenMixedIF(_ context.Context,left int64,right float64)(float64,error){return math.Mod(float64(left),right),nil}
func BenchmarkGeneratedFloatRemainder(b *testing.B){
 app:=&App{};ctx:=context.Background()
 calls:=[]struct{name string;call func(context.Context,float64,float64)(float64,error)}{
  {"manual",app.handwrittenRemainder},{"ordinary",app.remainder},{"compound",app.compoundRemainder},{"shadow",app.shadow},
 }
 if os.Getenv("LUXO_REVERSE_BENCH")=="1"{for l,r:=0,len(calls)-1;l<r;l,r=l+1,r-1{calls[l],calls[r]=calls[r],calls[l]}}
 for _,input:=range []struct{name string;left,right float64}{
  {"small",5.5,2},{"large",math.MaxFloat64,3},{"subnormal",math.SmallestNonzeroFloat64,2},{"negativeZero",math.Copysign(0,-1),2},
 }{
  for _,candidate:=range calls{
   b.Run(input.name+"/"+candidate.name,func(b *testing.B){
    got,err:=candidate.call(ctx,input.left,input.right)
    if err!=nil||math.Float64bits(got)!=math.Float64bits(math.Mod(input.left,input.right)){b.Fatal(got,err)}
    b.ReportAllocs();b.ResetTimer()
    for b.Loop(){value,err:=candidate.call(ctx,input.left,input.right);if err!=nil{b.Fatal(err)};remainderBenchmarkSink=value}
   })
  }
 }
 mixedFI:=[]struct{name string;call func(context.Context,float64,int64)(float64,error)}{
  {"manual",app.handwrittenMixedFI},{"ordinary",app.remainderMixedFI},
 }
 if os.Getenv("LUXO_REVERSE_BENCH")=="1"{mixedFI[0],mixedFI[1]=mixedFI[1],mixedFI[0]}
 for _,candidate:=range mixedFI{
  b.Run("mixedFI/"+candidate.name,func(b *testing.B){
   got,err:=candidate.call(ctx,5.5,2);if err!=nil||got!=1.5{b.Fatal(got,err)}
   b.ReportAllocs();b.ResetTimer()
   for b.Loop(){value,err:=candidate.call(ctx,5.5,2);if err!=nil{b.Fatal(err)};remainderBenchmarkSink=value}
  })
 }
 mixedIF:=[]struct{name string;call func(context.Context,int64,float64)(float64,error)}{
  {"manual",app.handwrittenMixedIF},{"ordinary",app.remainderMixedIF},
 }
 if os.Getenv("LUXO_REVERSE_BENCH")=="1"{mixedIF[0],mixedIF[1]=mixedIF[1],mixedIF[0]}
 for _,candidate:=range mixedIF{
  b.Run("mixedIF/"+candidate.name,func(b *testing.B){
   got,err:=candidate.call(ctx,5,2.5);if err!=nil||got!=0{b.Fatal(got,err)}
   b.ReportAllocs();b.ResetTimer()
   for b.Loop(){value,err:=candidate.call(ctx,5,2.5);if err!=nil{b.Fatal(err)};remainderBenchmarkSink=value}
  })
 }
}
`
