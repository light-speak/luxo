package codegen

import "testing"

func TestGeneratedCheckedArithmeticScopes(t *testing.T) {
	runGeneratedFixture(t, `
type Constants { value: String? @visible(1 + 1 == 2) duration: String? @visible(1h == 60m) }
fn mapped(values: [Int]): [Int] { return values.map { it + 1 } }
fn visit(values: [Int]): Int { var total = 0 values.forEach { total += it } return total }
fn fallback(value: Int?): Int { return (value ?: 0) + 1 }
fn concurrent(value: Int): Int { val (left, right) = await { value + 1 1 } return left + right }
`, `package generated
import("errors";"math";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")
func TestArithmeticScopes(t *testing.T){
 app:=&App{};var failure *luxerrors.AppError
 _,err:=app.mapped(t.Context(),[]int64{math.MaxInt64});if !errors.As(err,&failure)||failure.Code!=400{t.Fatal("map lost failure",err)}
 _,err=app.visit(t.Context(),[]int64{math.MaxInt64,1});if !errors.As(err,&failure)||failure.Code!=400{t.Fatal("callback lost failure",err)}
 value:=int64(math.MaxInt64);_,err=app.fallback(t.Context(),&value);if !errors.As(err,&failure)||failure.Code!=400{t.Fatal("nullable branch lost failure",err)}
 _,err=app.concurrent(t.Context(),math.MaxInt64);if !errors.As(err,&failure)||failure.Code!=400{t.Fatal("await lost failure",err)}
 if result,err:=app.mapped(t.Context(),[]int64{40});err!=nil||len(result)!=1||result[0]!=41{t.Fatal(result,err)}
 if result,err:=app.visit(t.Context(),[]int64{40,2});err!=nil||result!=42{t.Fatal(result,err)}
 if result,err:=app.fallback(t.Context(),nil);err!=nil||result!=1{t.Fatal(result,err)}
 if result,err:=app.concurrent(t.Context(),40);err!=nil||result!=42{t.Fatal(result,err)}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 text,duration:="value","duration";constants:=Constants{Value:&text,Duration:&duration};if err:=constants.WriteLuxo(&api.ResponseBuf{},mask);err!=nil{t.Fatal(err)}
}`, StableIDs{ModelFields: map[string]map[string]int{"Constants": {"value": 1, "duration": 2}}})
}
