package codegen

import "testing"

const integerArithmeticSource = `
model ArithmeticBox @noTime { id: Int @id value: Int touches: Int mutations: Int }
fn add(left: Int, right: Int): Int { return left + right }
fn subtract(left: Int, right: Int): Int { return left - right }
fn multiply(left: Int, right: Int): Int { return left * right }
fn divide(left: Int, right: Int): Int { return left / right }
fn remainder(left: Int, right: Int): Int { return left % right }
fn negate(value: Int): Int { return -value }
fn plusDuration(left: Duration, right: Duration): Duration { return left + right }
fn minusDuration(left: Duration, right: Duration): Duration { return left - right }
fn negateDuration(value: Duration): Duration { return -value }
fn dateSubtract(value: DateTime, amount: Duration): DateTime { return value - amount }
fn fromDays(value: Int): Duration { return value.days }
fn fromHours(value: Int): Duration { return value.hours }
fn fromMinutes(value: Int): Duration { return value.minutes }
fn fromSeconds(value: Int): Duration { return value.seconds }
fn fromMilliseconds(value: Int): Duration { return value.milliseconds }
fn guarded(flag: Boolean, left: Int, right: Int): Boolean { return flag && (left + right) > 0 }
fn orGuarded(flag: Boolean, left: Int, right: Int): Boolean { return flag || (left + right) > 0 }
fn target(box: ArithmeticBox): ArithmeticBox { box.touches += 1 return box }
fn mutate(box: ArithmeticBox): Int { box.mutations += 1 box.value = 99 return 1 }
fn compound(box: ArithmeticBox): Int { target(box).value += mutate(box) return box.value }
api checked(left: Int, right: Int): Int { return left + right }
`

func TestGeneratedCheckedArithmetic(t *testing.T) {
	runGeneratedFixture(t, integerArithmeticSource, integerArithmeticAssertions, StableIDs{
		ModelFields: map[string]map[string]int{"ArithmeticBox": {"id": 1, "value": 2, "touches": 3, "mutations": 4}},
	}, integerArithmeticBenchmarks, integerDateBenchmarks)
}

const integerArithmeticAssertions = `package generated
import("context";"errors";"math";"testing";"time";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")
func TestCheckedOperations(t *testing.T){
 app:=&App{}
 for _,sample:=range []struct{name string;call func(context.Context,int64,int64)(int64,error);left,right int64}{
  {"add",app.add,math.MaxInt64,1},{"subtract",app.subtract,math.MinInt64,1},{"multiply",app.multiply,math.MaxInt64,2},
  {"divide zero",app.divide,1,0},{"divide overflow",app.divide,math.MinInt64,-1},{"remainder zero",app.remainder,1,0},
 }{
  t.Run(sample.name,func(t *testing.T){
   defer func(){if failure:=recover();failure!=nil{t.Errorf("unexpected panic: %v",failure)}}()
   _,err:=sample.call(t.Context(),sample.left,sample.right);assertArithmeticError(t,err)
  })
 }
 t.Run("negate",func(t *testing.T){_,err:=app.negate(t.Context(),math.MinInt64);assertArithmeticError(t,err)})
 t.Run("duration add",func(t *testing.T){_,err:=app.plusDuration(t.Context(),time.Duration(math.MaxInt64),1);assertArithmeticError(t,err)})
 t.Run("duration subtract",func(t *testing.T){_,err:=app.minusDuration(t.Context(),time.Duration(math.MinInt64),1);assertArithmeticError(t,err)})
 t.Run("duration negate",func(t *testing.T){_,err:=app.negateDuration(t.Context(),time.Duration(math.MinInt64));assertArithmeticError(t,err)})
 if got,err:=app.dateSubtract(t.Context(),time.Unix(0,0),time.Duration(math.MinInt64));err!=nil||!got.Equal(time.Unix(9223372036,854775808)){t.Fatal("minimum Duration date subtraction",got,err)}
 for _,sample:=range []struct{call func(context.Context,int64)(time.Duration,error);unit time.Duration}{{app.fromDays,24*time.Hour},{app.fromHours,time.Hour},{app.fromMinutes,time.Minute},{app.fromSeconds,time.Second},{app.fromMilliseconds,time.Millisecond}}{
  if _,err:=sample.call(t.Context(),math.MaxInt64);err==nil{t.Fatal("unit multiplication overflow accepted")}
  if got,err:=sample.call(t.Context(),2);err!=nil||got!=2*sample.unit{t.Fatal("duration unit",got,err)}
 }
 if got,err:=app.divide(t.Context(),-5,2);err!=nil||got!=-2{t.Fatal(got,err)}
 if got,err:=app.remainder(t.Context(),-5,2);err!=nil||got!=-1{t.Fatal(got,err)}
 if got,err:=app.remainder(t.Context(),math.MinInt64,-1);err!=nil||got!=0{t.Fatal(got,err)}
 if got,err:=app.guarded(t.Context(),false,math.MaxInt64,1);err!=nil||got{t.Fatal("short circuit",got,err)}
 if got,err:=app.orGuarded(t.Context(),true,math.MaxInt64,1);err!=nil||!got{t.Fatal("short circuit",got,err)}
 if _,err:=app.guarded(t.Context(),true,math.MaxInt64,1);err==nil{t.Fatal("checked branch accepted overflow")}
 box:=&ArithmeticBox{Value:math.MaxInt64}
 _,err:=app.compound(t.Context(),box);assertArithmeticError(t,err)
 if box.Value!=99||box.Touches!=1||box.Mutations!=1{t.Fatal("assignment/error order",box)}
 box=&ArithmeticBox{Value:3};if got,err:=app.compound(t.Context(),box);err!=nil||got!=4||box.Value!=4||box.Touches!=1||box.Mutations!=1{t.Fatal(got,err,box)}
}
func assertArithmeticError(t *testing.T,err error){t.Helper();var failure *luxerrors.AppError;if !errors.As(err,&failure)||failure.Code!=400{t.Fatalf("expected explicit BadRequest, got %v",err)}}
`
