package codegen

import "testing"

func TestGeneratedMixedFloatArithmetic(t *testing.T) {
	const source = `
fn addFI(left: Float, right: Int): Float { return left + right }
fn subtractFI(left: Float, right: Int): Float { return left - right }
fn multiplyFI(left: Float, right: Int): Float { return left * right }
fn divideFI(left: Float, right: Int): Float { return left / right }
fn remainderFI(left: Float, right: Int): Float { return left % right }
fn addIF(left: Int, right: Float): Float { return left + right }
fn subtractIF(left: Int, right: Float): Float { return left - right }
fn multiplyIF(left: Int, right: Float): Float { return left * right }
fn divideIF(left: Int, right: Float): Float { return left / right }
fn remainderIF(left: Int, right: Float): Float { return left % right }
fn compoundFI(left: Float, right: Int): Float { left %= right return left }
fn compoundAddFI(left: Float, right: Int): Float { left += right return left }
fn nullableFI(left: Float?, right: Int): Float { if left != null { return left % right } return 0.0 }
fn nullableFF(left: Float?, right: Float): Float { if left != null { return left % right } return 0.0 }
`
	runGeneratedFixture(t, source, `package generated
import("context";"math";"testing")
func TestMixedFloat(t *testing.T){
 app:=&App{}
 for _,sample:=range []struct{call func(context.Context,float64,int64)(float64,error);want float64}{
  {app.addFI,7.5},{app.subtractFI,3.5},{app.multiplyFI,11},{app.divideFI,2.75},{app.remainderFI,1.5},{app.compoundFI,1.5},{app.compoundAddFI,7.5},
 }{
  got,err:=sample.call(t.Context(),5.5,2)
  if err!=nil||math.Float64bits(got)!=math.Float64bits(sample.want){t.Fatal(got,sample.want,err)}
 }
 for _,sample:=range []struct{call func(context.Context,int64,float64)(float64,error);want float64}{
  {app.addIF,7.5},{app.subtractIF,2.5},{app.multiplyIF,12.5},{app.divideIF,2},{app.remainderIF,0},
 }{
  got,err:=sample.call(t.Context(),5,2.5)
  if err!=nil||math.Float64bits(got)!=math.Float64bits(sample.want){t.Fatal(got,sample.want,err)}
 }
 got,err:=app.remainderIF(t.Context(),math.MaxInt64,3)
 if err!=nil||math.Float64bits(got)!=math.Float64bits(math.Mod(float64(math.MaxInt64),3)){t.Fatal("large Int promotion",got,err)}
 got,err=app.remainderFI(t.Context(),math.Copysign(0,-1),2)
 if err!=nil||math.Float64bits(got)!=uint64(1)<<63{t.Fatal("mixed signed zero",got,err)}
 negative:=math.Copysign(0,-1)
 for _,call:=range []func()(float64,error){func()(float64,error){return app.nullableFI(t.Context(),&negative,2)},func()(float64,error){return app.nullableFF(t.Context(),&negative,2)}}{
  got,err:=call();if err!=nil||math.Float64bits(got)!=uint64(1)<<63{t.Fatal("narrowed nullable",got,err)}
 }
 if got,err:=app.nullableFI(t.Context(),nil,2);err!=nil||got!=0{t.Fatal(got,err)}
}`, StableIDs{})
}
