package codegen

import (
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/formatter"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGeneratedDecimalAlignmentBudget(t *testing.T) {
	runGeneratedFixture(t, `
model Meter @noTime { id: Int @id amount: Decimal }
fn take(meter: Meter): Decimal { meter.id += 1 return meter.amount }
fn add(left: Decimal, right: Decimal): Decimal { return left + right }
fn subtract(left: Decimal, right: Decimal): Decimal { return left - right }
fn update(left: Meter, right: Meter): Decimal { left.amount += take(right) return left.amount }
fn reduce(left: Meter, right: Meter): Decimal { left.amount -= take(right) return left.amount }
fn total(values: [Decimal]): Decimal { return values.sumOf { it } }
fn lazy(enabled: Boolean, left: Decimal, right: Decimal): Boolean { return enabled && left + right == left }
`, `package generated
import("context";"testing";"github.com/shopspring/decimal";"github.com/light-speak/luxo/pkg/lux";"github.com/light-speak/luxo/pkg/lux/errors")
func requireAlignmentError(t *testing.T,err error){t.Helper();failure,ok:=err.(*errors.AppError);if !ok||failure.Code!=400{t.Fatalf("expected 400, got %v",err)}}
func TestAlignmentBudget(t *testing.T){
 app:=&App{};ctx:=t.Context();one:=decimal.New(1,0);large:=decimal.New(1,2147483647)
 for _,call:=range []func(context.Context,decimal.Decimal,decimal.Decimal)(decimal.Decimal,error){app.add,app.subtract}{
  for _,exponent:=range []int32{-4097,4097,-2147483648,2147483647}{_,err:=call(ctx,decimal.New(1,exponent),one);requireAlignmentError(t,err)}
  for _,exponent:=range []int32{-4096,4096}{if _,err:=call(ctx,decimal.New(1,exponent),one);err!=nil{t.Fatal("boundary rejected",err)}}
  if got,err:=call(ctx,large,decimal.Decimal{});err!=nil||lux.CompareDecimal(got,large)!=0{t.Fatal("zero operand",err)}
 }
 for _,call:=range []func(context.Context,*Meter,*Meter)(decimal.Decimal,error){app.update,app.reduce}{
  left,right:=&Meter{Amount:large},&Meter{Amount:one}
  _,err:=call(ctx,left,right);requireAlignmentError(t,err)
  if right.Id!=1||lux.CompareDecimal(left.Amount,large)!=0{t.Fatal("evaluation or failed assignment changed")}
 }
 if got,err:=app.total(ctx,[]decimal.Decimal{large,decimal.Decimal{}});err!=nil||lux.CompareDecimal(got,large)!=0{t.Fatal("zero-seeded aggregate expanded",err)}
 if got,err:=app.total(ctx,[]decimal.Decimal{large,large.Neg(),one});err!=nil||lux.CompareDecimal(got,one)!=0{t.Fatal("zero intermediate aggregate",err)}
 _,err:=app.total(ctx,[]decimal.Decimal{large,one,large.Neg()});requireAlignmentError(t,err)
 if got,err:=app.lazy(ctx,false,large,one);err!=nil||got{t.Fatal("short circuit",err)}
 _,err=app.lazy(ctx,true,large,one);requireAlignmentError(t,err)
}
`, StableIDs{ModelFields: map[string]map[string]int{"Meter": {"id": 1, "amount": 2}}})
}

func TestGeneratedDecimalCompactOutput(t *testing.T) {
	runGeneratedFixture(t, `
type Snapshot { value: Decimal optional: Decimal? values: [Decimal] }
event Changed(value: Decimal, optional: Decimal?, values: [Decimal])
error Rejected(value: Decimal) { code: 422 message: "error.rejected" }
fn render(value: Decimal): String { return "value=${value}" }
`, `package generated
import("bytes";"testing";"github.com/shopspring/decimal";"github.com/light-speak/luxo/pkg/lux";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestCompactOutput(t *testing.T){
 for _,input:=range []string{"1e2147483647","-1e-2147483648","1.2300"}{
  value:=decimal.RequireFromString(input);text:=lux.FormatDecimal(value)
  item:=Snapshot{Value:value,Optional:&value,Values:[]decimal.Decimal{value}}
  var buf api.ResponseBuf
  if err:=item.WriteLuxo(&buf,nil);err!=nil{t.Fatal(err)}
  if len(buf.B)>512{t.Fatal("row expanded",len(buf.B))}
  dec:=codec.NewDecoder(buf.B);var row Snapshot;row.ReadLuxo(dec)
  if err:=dec.Finish();err!=nil||row.Optional==nil||len(row.Values)!=1||lux.CompareDecimal(row.Value,value)!=0||lux.CompareDecimal(*row.Optional,value)!=0||lux.CompareDecimal(row.Values[0],value)!=0{t.Fatal("row roundtrip",err)}
  buf.Reset();if err:=WriteColumnarSnapshot(&buf,[]Snapshot{item},nil);err!=nil{t.Fatal(err)}
  if len(buf.B)>512{t.Fatal("column expanded",len(buf.B))}
  items,err:=ReadColumnarSnapshot(buf.B);if err!=nil||len(items)!=1||lux.CompareDecimal(items[0].Value,value)!=0{t.Fatal("column roundtrip",err)}
  event:=ChangedEvent{Value:value,Optional:&value,Values:[]decimal.Decimal{value}}
  raw,err:=event.MarshalLuxo();if err!=nil||len(raw)>512{t.Fatal("event expanded",len(raw),err)}
  var decoded ChangedEvent;if err:=decoded.UnmarshalLuxo(raw);err!=nil||lux.CompareDecimal(decoded.Value,value)!=0{t.Fatal("event roundtrip",err)}
  raw,err=NewRejected(value).Data.AppendJSON(nil);if err!=nil||!bytes.Contains(raw,[]byte(text))||len(raw)>128{t.Fatal("error text",len(raw),err)}
  got,err:=(&App{}).render(t.Context(),value);if err!=nil||got!="value="+text{t.Fatal("template",err)}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"Snapshot": {"value": 1, "optional": 2, "values": 3}}, EventFields: map[string]map[string]int{"Changed": {"value": 1, "optional": 2, "values": 3}}})
}

func TestGeneratedDecimalExtremeComparisons(t *testing.T) {
	runGeneratedFixture(t, `
fn less(left: Decimal, right: Decimal): Boolean { return left < right }
fn equal(left: Decimal?, right: Decimal?): Boolean { return left == right }
fn matches(left: Decimal, right: Decimal): Boolean { return when left { right -> true else -> false } }
fn ordered(values: [Decimal]): [Decimal] { return values.sortBy { it } }
`, `package generated
import("testing";"github.com/shopspring/decimal")

func TestExtremeComparisons(t *testing.T){
 app:=&App{};small:=decimal.New(1,-2147483648);large:=decimal.New(1,2147483647)
 if got,err:=app.less(t.Context(),small,large);err!=nil||!got{t.Fatal("order",got,err)}
 if got,err:=app.equal(t.Context(),&small,&large);err!=nil||got{t.Fatal("nullable inequality",got,err)}
 if got,err:=app.equal(t.Context(),nil,nil);err!=nil||!got{t.Fatal("null equality",got,err)}
 if got,err:=app.matches(t.Context(),small,large);err!=nil||got{t.Fatal("when mismatch",got,err)}
 one:=decimal.RequireFromString("1e30");same:=decimal.RequireFromString("1000000000000000000000000000000")
 if got,err:=app.matches(t.Context(),one,same);err!=nil||!got{t.Fatal("when equality",got,err)}
 if got,err:=app.equal(t.Context(),&one,&same);err!=nil||!got{t.Fatal("nullable equality",got,err)}
 values,err:=app.ordered(t.Context(),[]decimal.Decimal{large,small,decimal.New(1,0)})
 if err!=nil||len(values)!=3{t.Fatal("sort failed",err)}
 for index,want:=range []int32{-2147483648,0,2147483647}{if values[index].Exponent()!=want{t.Fatalf("index=%d exponent=%d",index,values[index].Exponent())}}
}
`, StableIDs{})
}

func TestGeneratedDecimalMultiplicationExponent(t *testing.T) {
	runGeneratedFixture(t, `
fn multiply(left: Decimal, right: Decimal): Decimal { return left * right }
fn compound(left: Decimal, right: Decimal): Decimal { left *= right return left }
`, `package generated
import("context";"testing";"github.com/shopspring/decimal";"github.com/light-speak/luxo/pkg/lux/errors")

func TestExponentBoundary(t *testing.T){
 app:=&App{}
 for _,call:=range []func(context.Context,decimal.Decimal,decimal.Decimal)(decimal.Decimal,error){app.multiply,app.compound}{
  for _,sample:=range []struct{left,right int32; invalid bool}{
   {2147483647,1,true},{-2147483648,-1,true},{2147483647,2147483647,true},
   {2147483647,0,false},{-2147483648,0,false},{2147483647,-2147483648,false},{-2,-3,false},
  }{
   got,err:=call(t.Context(),decimal.New(2,sample.left),decimal.New(3,sample.right))
   if sample.invalid{if appErr,ok:=err.(*errors.AppError);!ok||appErr.Code!=400{t.Fatalf("expected range error: %v",err)};continue}
   if err!=nil||got.Exponent()!=int32(int64(sample.left)+int64(sample.right))||got.Coefficient().Int64()!=6{t.Fatalf("incorrect result exponent=%d err=%v",got.Exponent(),err)}
  }
 }
}
func (app *App) oldMultiply(ctx context.Context,left,right decimal.Decimal)(decimal.Decimal,error){return left.Mul(right),nil}
func BenchmarkDecimalMultiplication(b *testing.B){
 app:=&App{}
 for _,sample:=range []struct{name string;left,right decimal.Decimal}{
  {"small",decimal.New(123,-2),decimal.New(456,-2)},
  {"precise",decimal.RequireFromString("12345678901234567890.123456789"),decimal.New(123456789,-9)},
 }{
  for _,version:=range []struct{name string;call func(context.Context,decimal.Decimal,decimal.Decimal)(decimal.Decimal,error)}{{"before",app.oldMultiply},{"after",app.multiply}}{
   b.Run(sample.name+"/"+version.name,func(b *testing.B){
    ctx:=b.Context();var got decimal.Decimal;var err error;b.ReportAllocs()
    for b.Loop(){got,err=version.call(ctx,sample.left,sample.right)}
    b.StopTimer();if err!=nil||!got.Equal(sample.left.Mul(sample.right)){b.Fatal("invalid result")}
   })
  }
 }
}
`, StableIDs{})
}

func TestGeneratedDecimalMultiplicationEvaluation(t *testing.T) {
	runGeneratedFixture(t, `
model Box @noTime { id: Int @id amount: Decimal }
fn take(box: Box): Decimal { box.id += 1 return box.amount }
fn multiply(left: Box, right: Box): Decimal { return take(left) * take(right) }
fn compound(left: Box, right: Box): Decimal { left.amount *= take(right) return left.amount }
fn lazy(enabled: Boolean, left: Decimal, right: Decimal): Boolean { return enabled && left * right == left }
`, `package generated
import("testing";"github.com/shopspring/decimal")
func TestEvaluation(t *testing.T){
 app:=&App{};left:=&Box{Amount:decimal.New(2,2147483647)};right:=&Box{Amount:decimal.New(3,1)}
 if _,err:=app.multiply(t.Context(),left,right);err==nil||left.Id!=1||right.Id!=1{t.Fatal("operands not evaluated once",left.Id,right.Id,err)}
 if _,err:=app.compound(t.Context(),left,right);err==nil||right.Id!=2||left.Amount.Exponent()!=2147483647||left.Amount.Coefficient().Int64()!=2{t.Fatal("failed assignment changed destination",right.Id,err)}
 if got,err:=app.lazy(t.Context(),false,left.Amount,right.Amount);err!=nil||got{t.Fatal("short circuit evaluated invalid product",err)}
 if _,err:=app.lazy(t.Context(),true,left.Amount,right.Amount);err==nil{t.Fatal("missing product error")}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Box": {"id": 1, "amount": 2}}})
}

func TestGeneratedDecimalExactArithmetic(t *testing.T) {
	runGeneratedFixture(t, `
fn add(left: Decimal, right: Decimal): Decimal { return left + right }
fn subtract(left: Decimal, right: Decimal): Decimal { return left - right }
fn multiply(left: Decimal, right: Decimal): Decimal { return left * right }
fn addAssign(left: Decimal, right: Decimal): Decimal { left += right return left }
fn subtractAssign(left: Decimal, right: Decimal): Decimal { left -= right return left }
fn multiplyAssign(left: Decimal, right: Decimal): Decimal { left *= right return left }
`, `package generated
import("testing";"github.com/shopspring/decimal")

func TestExactArithmetic(t *testing.T){
 app:=&App{};ctx:=t.Context()
 left:=decimal.RequireFromString("12345678901234567890.123456789")
 right:=decimal.RequireFromString("0.000000001")
 got,err:=app.add(ctx,left,right);if err!=nil||!got.Equal(left.Add(right)){t.Fatal("add",got,err)}
 got,err=app.subtract(ctx,left,right);if err!=nil||!got.Equal(left.Sub(right)){t.Fatal("subtract",got,err)}
 got,err=app.multiply(ctx,left,right);if err!=nil||!got.Equal(left.Mul(right)){t.Fatal("multiply",got,err)}
 got,err=app.addAssign(ctx,left,right);if err!=nil||!got.Equal(left.Add(right)){t.Fatal("add assign",got,err)}
 got,err=app.subtractAssign(ctx,left,right);if err!=nil||!got.Equal(left.Sub(right)){t.Fatal("subtract assign",got,err)}
 got,err=app.multiplyAssign(ctx,left,right);if err!=nil||!got.Equal(left.Mul(right)){t.Fatal("multiply assign",got,err)}
}
`, StableIDs{})
}

func TestGeneratedDecimalCompoundEvaluation(t *testing.T) {
	runGeneratedFixture(t, `
model Box @noTime { id: Int @id amount: Decimal }
fn replace(box: Box, value: Decimal): Decimal { box.amount = value return value }
fn update(box: Box, value: Decimal): Decimal { box.amount += replace(box, value) return box.amount }
fn failed(box: Box, value: Decimal): Decimal { box.amount = value throw error.Conflict }
fn rejected(box: Box, value: Decimal): Decimal { box.amount += failed(box, value) return box.amount }
`, `package generated
import("testing";"github.com/shopspring/decimal")
func TestCompoundEvaluation(t *testing.T){
 app:=&App{};ctx:=t.Context()
 left:=decimal.RequireFromString("12345678901234567890.123456789");right:=decimal.RequireFromString("0.000000001")
 box:=&Box{Amount:left}
 got,err:=app.update(ctx,box,right);if err!=nil||!got.Equal(left.Add(right))||!box.Amount.Equal(got){t.Fatal("old value lost",got,err)}
 box.Amount=left
 if _,err:=app.rejected(ctx,box,right);err==nil||!box.Amount.Equal(right){t.Fatal("failed assignment applied",box.Amount,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Box": {"id": 1, "amount": 2}}})
}

func TestGeneratedCompoundAssignmentEvaluation(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id amount: Int moment: DateTime }
fn replace(counter: Counter, value: Int): Int { counter.amount = value return value }
fn update(counter: Counter, value: Int): Int { counter.amount += replace(counter, value) return counter.amount }
fn move(counter: Counter, value: Duration): Duration { counter.moment = counter.moment + value return value }
fn advance(counter: Counter, value: Duration): DateTime { counter.moment += move(counter, value) return counter.moment }
fn locate(counter: Counter): Counter { counter.id += 1 return counter }
fn updateReceiver(counter: Counter, value: Int): Int { locate(counter).amount += replace(counter, value) return counter.amount }
fn failed(counter: Counter, value: Int): Int { counter.amount = value throw error.Conflict }
fn rejected(counter: Counter, value: Int): Int { counter.amount += failed(counter, value) return counter.amount }
`, `package generated
import("testing";"time")
func TestCompoundEvaluation(t *testing.T){
 app:=&App{};ctx:=t.Context();date:=time.Date(2026,1,2,3,4,5,123456789,time.UTC)
 counter:=&Counter{Amount:10,Moment:date}
 if got,err:=app.update(ctx,counter,3);err!=nil||got!=13{t.Error("integer old value lost",got,err)}
 if got,err:=app.advance(ctx,counter,time.Second);err!=nil||!got.Equal(date.Add(time.Second)){t.Error("date old value lost",got,err)}
 counter.Amount=10
 if got,err:=app.updateReceiver(ctx,counter,3);err!=nil||got!=13||counter.Id!=1{t.Error("receiver evaluation",got,counter.Id,err)}
 counter.Amount=10
 if _,err:=app.rejected(ctx,counter,3);err==nil||counter.Amount!=3{t.Error("failed assignment",counter.Amount,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "amount": 2, "moment": 3}}})
}

func TestGeneratedAssignmentPreservesDestination(t *testing.T) {
	source, err := formatter.Format(`
model Cell @noTime { id: Int @id value: Int }
model Holder @noTime { id: Int @id cellId: Int cell: Cell }
fn swap(holder: Holder, other: Cell): Int { holder.cell = other return 7 }
fn write(holder: Holder, other: Cell): Int { holder.cell.value = swap(holder, other) return holder.cell.value }
fn locate(holder: Holder): Holder { holder.id += 1 return holder }
fn writeReceiver(holder: Holder, other: Cell): Int { locate(holder).cell.value = swap(holder, other) return holder.cell.value }
fn fail(holder: Holder, other: Cell): Int { holder.cell = other throw error.Conflict }
fn rejected(holder: Holder, other: Cell): Int { holder.cell.value = fail(holder, other) return holder.cell.value }
`, "assignment.luxo")
	if err != nil {
		t.Fatal(err)
	}
	runGeneratedFixture(t, source, `package generated
import "testing"
func TestDestination(t *testing.T){
 app:=&App{};old:=&Cell{Value:10};other:=&Cell{Value:20};holder:=&Holder{Cell:old}
 got,err:=app.write(t.Context(),holder,other)
 if err!=nil||got!=20||old.Value!=7||other.Value!=20||holder.Cell!=other{t.Fatal("destination changed",got,old.Value,other.Value,err)}
 old.Value=10;holder.Cell=old
 got,err=app.writeReceiver(t.Context(),holder,other)
 if err!=nil||got!=20||old.Value!=7||other.Value!=20||holder.Id!=1{t.Fatal("receiver evaluation",got,old.Value,other.Value,holder.Id,err)}
 old.Value=10;holder.Cell=old
 if _,err:=app.rejected(t.Context(),holder,other);err==nil||old.Value!=10||other.Value!=20||holder.Cell!=other{t.Fatal("failed assignment applied",old.Value,other.Value,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Cell": {"id": 1, "value": 2}, "Holder": {"id": 1, "cellId": 2, "cell": 3}}})
}

func TestGeneratedNumericNegation(t *testing.T) {
	runGeneratedFixture(t, `
fn negative(value: Decimal): Decimal { return -value }
fn twice(value: Int): Int { return -(-value) }
fn twiceFloat(value: Float): Float { return -(-value) }
fn twiceDuration(value: Duration): Duration { return -(-value) }
`, `package generated
import("math";"testing";"time";"github.com/shopspring/decimal")

func TestNegation(t *testing.T){
 app:=&App{};ctx:=t.Context();value:=decimal.RequireFromString("12345678901234567890.123456789")
 got,err:=app.negative(ctx,value);if err!=nil||!got.Equal(value.Neg()){t.Fatal(got,err)}
 if got,err:=app.twice(ctx,9007199254740993);err!=nil||got!=9007199254740993{t.Fatal(got,err)}
 if got,err:=app.twiceFloat(ctx,math.Copysign(0,-1));err!=nil||!math.Signbit(got){t.Fatal(got,err)}
 if got,err:=app.twiceDuration(ctx,time.Nanosecond);err!=nil||got!=time.Nanosecond{t.Fatal(got,err)}
}
`, StableIDs{})
}

func TestGeneratedWhenScalarEquality(t *testing.T) {
	runGeneratedFixture(t, `
fn matchDate(value: DateTime, expected: DateTime): Int { return when value { expected -> 1 else -> 0 } }
fn matchDecimal(value: Decimal, expected: Decimal): Int { return when value { expected -> 1 else -> 0 } }
fn matchInt(value: Int?, expected: Int?): Int { return when value { expected -> 1 else -> 0 } }
fn matchMixedInt(value: Int?, expected: Int): Int { return when value { expected -> 1 else -> 0 } }
fn matchReverseInt(value: Int, expected: Int?): Int { return when value { expected -> 1 else -> 0 } }
fn matchNullableDate(value: DateTime?, expected: DateTime?): Int { return when value { expected -> 1 else -> 0 } }
fn matchReverseDate(value: DateTime, expected: DateTime?): Int { return when value { expected -> 1 else -> 0 } }
fn matchNullableDecimal(value: Decimal?, expected: Decimal?): Int { return when value { expected -> 1 else -> 0 } }
fn matchReverseDecimal(value: Decimal, expected: Decimal?): Int { return when value { expected -> 1 else -> 0 } }
fn matchNull(value: Int?): Int { return when value { null -> 1 else -> 0 } }
fn forwardDate(value: DateTime): DateTime { return value }
fn failedDate(): DateTime { throw error.NotFound }
fn matchLazyDate(value: DateTime, expected: DateTime): Int {
 return when value { forwardDate(expected) -> 1 failedDate() -> 2 else -> 0 }
}
fn yieldDate(value: DateTime, expected: DateTime): Int? {
 return for item in [7] { when value { expected -> yield item else -> null } }
}
`, `package generated
import("testing";"time";"github.com/shopspring/decimal")

func TestWhenScalarValues(t *testing.T){
 app:=&App{};ctx:=t.Context();date:=time.Now();same:=date.In(time.FixedZone("offset",9*3600))
 if got,err:=app.matchDate(ctx,date,same);err!=nil||got!=1{t.Fatal("date",got,err)}
 if got,err:=app.matchDate(ctx,date,date.Add(time.Nanosecond));err!=nil||got!=0{t.Fatal("different date",got,err)}
 if got,err:=app.yieldDate(ctx,date,same);err!=nil||got==nil||*got!=7{t.Fatal("yield date",got,err)}
 if got,err:=app.yieldDate(ctx,date,date.Add(time.Nanosecond));err!=nil||got!=nil{t.Fatal("yield mismatch",got,err)}
 if got,err:=app.matchLazyDate(ctx,date,same);err!=nil||got!=1{t.Fatal("later branch evaluated",got,err)}
 if _,err:=app.matchLazyDate(ctx,date,date.Add(time.Nanosecond));err==nil{t.Fatal("branch error lost")}
 if got,err:=app.matchDecimal(ctx,decimal.RequireFromString("1"),decimal.RequireFromString("1.00"));err!=nil||got!=1{t.Fatal("decimal",got,err)}
 a,b:=int64(42),int64(42)
 for _,pair:=range [][2]*int64{{&a,&b},{nil,nil}}{if got,err:=app.matchInt(ctx,pair[0],pair[1]);err!=nil||got!=1{t.Fatal("nullable int",got,err)}}
 if got,err:=app.matchInt(ctx,&a,nil);err!=nil||got!=0{t.Fatal("nullable mismatch",got,err)}
 if got,err:=app.matchMixedInt(ctx,&a,b);err!=nil||got!=1{t.Fatal("mixed int",got,err)}
 if got,err:=app.matchMixedInt(ctx,nil,b);err!=nil||got!=0{t.Fatal("mixed null",got,err)}
 if got,err:=app.matchNull(ctx,nil);err!=nil||got!=1{t.Fatal("null branch",got,err)}
 for _,expected:=range []*int64{nil,&b}{
  want:=int64(0);if expected!=nil{want=1}
  if got,err:=app.matchReverseInt(ctx,a,expected);err!=nil||got!=want{t.Fatal("reverse nullable int",got,err)}
 }
 for _,pair:=range [][2]*time.Time{{nil,nil},{&date,&same},{&date,nil},{nil,&same}}{
  want:=int64(0);if pair[0]==nil&&pair[1]==nil||pair[0]!=nil&&pair[1]!=nil{want=1}
  if got,err:=app.matchNullableDate(ctx,pair[0],pair[1]);err!=nil||got!=want{t.Fatal("nullable date",got,err)}
 }
 for _,expected:=range []*time.Time{nil,&same}{
  want:=int64(0);if expected!=nil{want=1}
  if got,err:=app.matchReverseDate(ctx,date,expected);err!=nil||got!=want{t.Fatal("reverse nullable date",got,err)}
 }
 d,e:=decimal.RequireFromString("1"),decimal.RequireFromString("1.00")
 for _,pair:=range [][2]*decimal.Decimal{{nil,nil},{&d,&e},{&d,nil},{nil,&e}}{
  want:=int64(0);if pair[0]==nil&&pair[1]==nil||pair[0]!=nil&&pair[1]!=nil{want=1}
  if got,err:=app.matchNullableDecimal(ctx,pair[0],pair[1]);err!=nil||got!=want{t.Fatal("nullable decimal",got,err)}
 }
 for _,expected:=range []*decimal.Decimal{nil,&e}{
  want:=int64(0);if expected!=nil{want=1}
  if got,err:=app.matchReverseDecimal(ctx,d,expected);err!=nil||got!=want{t.Fatal("reverse nullable decimal",got,err)}
 }
}
`, StableIDs{})
}

func TestGeneratedWhenComparisonEvaluation(t *testing.T) {
	runGeneratedFixture(t, `
fn fetchDate(index: Int): Result<DateTime> @native
fn match(): Int { return when fetchDate(1)? { fetchDate(2)? -> 1 fetchDate(3)? -> 2 else -> 0 } }
fn matchYield(): Int? {
 return for item in [7] { when fetchDate(1)? { fetchDate(2)? -> yield item fetchDate(3)? -> yield item + 1 else -> null } }
}
`, `package generated
import("testing";"time";"context";"errors";"slices")
type whenResolver struct{calls []int64;failAt int64;values [3]time.Time}
var whenFailure=errors.New("when failed")
func(r *whenResolver)FetchDate(_ context.Context,index int64)(time.Time,error){
 r.calls=append(r.calls,index);if index==r.failAt{return time.Time{},whenFailure};return r.values[index-1],nil
}
func TestWhenEvaluation(t *testing.T){
 date:=time.Now();same:=date.In(time.FixedZone("offset",9*3600));later:=date.Add(time.Nanosecond)
 for _,sample:=range []struct{values [3]time.Time;failAt,want int64;calls []int64}{
  {[3]time.Time{date,same,later},0,1,[]int64{1,2}},
  {[3]time.Time{date,later,same},0,2,[]int64{1,2,3}},
  {[3]time.Time{date,later,later},0,0,[]int64{1,2,3}},
  {[3]time.Time{date,later,same},1,0,[]int64{1}},
  {[3]time.Time{date,later,same},2,0,[]int64{1,2}},
  {[3]time.Time{date,later,same},3,0,[]int64{1,2,3}},
 }{for _,yield:=range []bool{false,true}{
  r:=&whenResolver{values:sample.values,failAt:sample.failAt};app:=&App{Resolver:r};got,err:=int64(0),error(nil)
  if yield{var value *int64;value,err=app.matchYield(t.Context());if value!=nil{got=*value-6}}else{got,err=app.match(t.Context())}
  if !slices.Equal(r.calls,sample.calls){t.Fatal("evaluation order",r.calls,sample.calls)}
  if sample.failAt!=0{if !errors.Is(err,whenFailure){t.Fatal("error lost",err)};continue}
  if err!=nil||got!=sample.want{t.Fatal(got,sample.want,err)}
 }}
}
`, StableIDs{})
}

func TestGeneratedNullableComparisonEvaluation(t *testing.T) {
	runGeneratedFixture(t, `
fn fetchNumber(index: Int): Result<Int?> @native
fn equalCalls(): Boolean { return fetchNumber(1)? == fetchNumber(2)? }
fn unequalCalls(): Boolean { return fetchNumber(1)? != fetchNumber(2)? }
fn guardedAnd(enabled: Boolean): Boolean { return enabled && (fetchNumber(1)? == fetchNumber(2)?) }
fn guardedOr(enabled: Boolean): Boolean { return enabled || (fetchNumber(1)? == fetchNumber(2)?) }
`, `package generated
import("testing";"context";"errors";"slices")
type comparisonResolver struct{calls []int64;failAt int64;values [2]*int64}
var comparisonFailure=errors.New("comparison failed")
func(r *comparisonResolver)FetchNumber(_ context.Context,index int64)(*int64,error){
 r.calls=append(r.calls,index);if index==r.failAt{return nil,comparisonFailure};return r.values[index-1],nil
}
func TestComparisonEvaluation(t *testing.T){
 a,b,c:=int64(9007199254740993),int64(9007199254740993),int64(9007199254740992)
 for _,values:=range [][2]*int64{{nil,nil},{&a,nil},{nil,&b},{&a,&b},{&a,&c}}{
  for _,failAt:=range []int64{0,1,2}{for mode:=0;mode<6;mode++{
   r:=&comparisonResolver{values:values,failAt:failAt};app:=&App{Resolver:r};ctx:=t.Context()
   var got bool;var err error
   switch mode{case 0:got,err=app.equalCalls(ctx);case 1:got,err=app.unequalCalls(ctx);case 2:got,err=app.guardedAnd(ctx,false);case 3:got,err=app.guardedAnd(ctx,true);case 4:got,err=app.guardedOr(ctx,false);case 5:got,err=app.guardedOr(ctx,true)}
   skipped:=mode==2||mode==5
   wantCalls:=[]int64{1,2};if skipped{wantCalls=nil}else if failAt==1{wantCalls=[]int64{1}}
   if !slices.Equal(r.calls,wantCalls){t.Fatal("evaluation order",mode,failAt,r.calls,wantCalls)}
   if !skipped&&failAt!=0{if !errors.Is(err,comparisonFailure){t.Fatal("error lost",mode,err)};continue}
   want:=values[0]==values[1]||values[0]!=nil&&values[1]!=nil&&*values[0]==*values[1]
   if mode==1{want=!want};if mode==2{want=false};if mode==5{want=true}
   if err!=nil||got!=want{t.Fatal("value",mode,got,want,err)}
  }}
 }
}
`, StableIDs{})
}

func TestGeneratedMixedNumericComparison(t *testing.T) {
	for _, pair := range [][2]string{{"Int", "Float"}, {"Int", "Decimal"}, {"Float", "Decimal"}} {
		for _, op := range []string{"==", "!=", "<", "<=", ">", ">="} {
			t.Run(pair[0]+op+pair[1], func(t *testing.T) {
				source := "fn compare(left: " + pair[0] + ", right: " + pair[1] + "): Boolean { return left " + op + " right }"
				result := analyzePermissionFixture(t, source)
				output, err := Generate(result, "generated", GeneratorConfig{})
				if err == nil || output != nil || !strings.Contains(err.Error(), "cannot compare incompatible types") {
					t.Fatalf("mixed comparison emitted code: %v", err)
				}
			})
		}
	}
}

func TestGenerateRejectsNonBooleanBangElvis(t *testing.T) {
	for _, kind := range []string{"Int", "String", "Boolean?", "[Boolean]"} {
		t.Run(kind, func(t *testing.T) {
			result := analyzePermissionFixture(t, "fn guard(value: "+kind+"): Boolean { value !: throw error.Conflict return true }")
			output, err := Generate(result, "generated", GeneratorConfig{})
			if err == nil || output != nil || !strings.Contains(err.Error(), "requires non-null Boolean") {
				t.Fatalf("invalid guard emitted code: %v", err)
			}
		})
	}
}

func TestGenerateRejectsNonErrorThrow(t *testing.T) {
	for _, value := range []string{"42", "\"failure\"", "null", "true", "[1]"} {
		for _, body := range []string{"throw " + value, "true !: throw " + value + " return true", "return false ?: throw " + value} {
			result := analyzePermissionFixture(t, "fn guard(): Boolean { "+body+" }")
			output, err := Generate(result, "generated", GeneratorConfig{})
			if err == nil || output != nil || !strings.Contains(err.Error(), "throw requires") {
				t.Fatalf("invalid throw emitted code (%s): %v", body, err)
			}
		}
	}
}

func TestGeneratedBangElvisGuardBoundary(t *testing.T) {
	for _, body := range []string{"true !: false return true", "return true !: throw error.Conflict", "val result = true !: throw error.Conflict return result"} {
		result := analyzePermissionFixture(t, "fn guard(): Boolean { "+body+" }")
		output, err := Generate(result, "generated", GeneratorConfig{})
		if err == nil || output != nil || !strings.Contains(err.Error(), "!:") {
			t.Fatalf("invalid guard emitted code: %v", err)
		}
	}
	runGeneratedFixture(t, `fn guard(value: Boolean): Boolean { value !: throw error.Conflict return true }`, `package generated
import "testing"

func TestGuard(t *testing.T){
 app:=&App{}
 if got,err:=app.guard(t.Context(),false);err!=nil||!got{t.Fatal("false rejected",got,err)}
 if _,err:=app.guard(t.Context(),true);err==nil{t.Fatal("true accepted")}
}
`, StableIDs{})
}

func TestGeneratedBangElvisEvaluation(t *testing.T) {
	runGeneratedFixture(t, `
error Rejected(detail: String) { code: 409 message: "error.rejected" }
fn condition(): Result<Boolean> @native
fn detail(): Result<String> @native
fn guarded(): Boolean { condition()? !: throw Rejected(detail: detail()?) return true }
`, `package generated
import("context";"errors";"slices";"testing")
type guardResolver struct{value bool;failAt string;calls []string}
var guardFailure=errors.New("native guard failure")
func(r *guardResolver)Condition(context.Context)(bool,error){
 r.calls=append(r.calls,"condition");if r.failAt=="condition"{return false,guardFailure};return r.value,nil
}
func(r *guardResolver)Detail(context.Context)(string,error){
 r.calls=append(r.calls,"detail");if r.failAt=="detail"{return "",guardFailure};return "rejected",nil
}
func TestGuardEvaluation(t *testing.T){
 for _,value:=range []bool{false,true}{for _,failAt:=range []string{"","condition","detail"}{
  r:=&guardResolver{value:value,failAt:failAt};app:=&App{Resolver:r}
  got,err:=app.guarded(t.Context())
  wantCalls:=[]string{"condition"};if value&&failAt!="condition"{wantCalls=append(wantCalls,"detail")}
  if !slices.Equal(r.calls,wantCalls){t.Fatal("calls",r.calls,wantCalls)}
  nativeFailure:=failAt=="condition"||value&&failAt=="detail"
  if nativeFailure{if !errors.Is(err,guardFailure){t.Fatal("native error lost",err)};continue}
  if value{if err==nil||errors.Is(err,guardFailure){t.Fatal("rejection lost",err)}}else if err!=nil||!got{t.Fatal("false rejected",got,err)}
 }}
}
`, StableIDs{})
}

func TestGeneratedNullablePrimitiveEquality(t *testing.T) {
	for _, sample := range []struct{ kind, goType, value, other, imports string }{
		{"Int", "int64", "int64(9007199254740993)", "int64(9007199254740992)", ""},
		{"Float", "float64", "1.25", "1.5", ""},
		{"String", "string", `"東京"`, `"東京2"`, ""},
		{"Boolean", "bool", "true", "false", ""},
		{"Duration", "time.Duration", "time.Nanosecond", "2*time.Nanosecond", `;"time"`},
		{"UUID", "uuid.UUID", "uuid.UUID{1}", "uuid.UUID{2}", `;"github.com/google/uuid"`},
		{"State", "State", "StateOPEN", "StateCLOSED", ""},
	} {
		t.Run(sample.kind, func(t *testing.T) {
			source := strings.ReplaceAll(`enum State { OPEN CLOSED }
fn same(left: KIND?, right: KIND?): Boolean { return left == right }
fn different(left: KIND?, right: KIND?): Boolean { return left != right }
fn sameLeft(left: KIND?, right: KIND): Boolean { return left == right }
fn sameRight(left: KIND, right: KIND?): Boolean { return left == right }
fn guardedSame(left: KIND?, right: KIND?): Boolean { return left != null && right != null && left == right }
fn guardedWhen(left: KIND?, right: KIND?): Boolean {
 if left != null && right != null { return when left { right -> true else -> false } }
 return false
}
`, "KIND", sample.kind)
			assertions := strings.NewReplacer("IMPORTS", sample.imports, "GO_TYPE", sample.goType, "VALUE", sample.value, "OTHER", sample.other).Replace(`package generated
import("testing" IMPORTS)

func TestPrimitiveValues(t *testing.T){
 app:=&App{};ctx:=t.Context();a:=VALUE;b:=a;c:=OTHER
 values:=[]*GO_TYPE{nil,&a,&b,&c}
 for _,left:=range values{for _,right:=range values{
  want:=left==nil&&right==nil||left!=nil&&right!=nil&&*left==*right
  got,err:=app.same(ctx,left,right);if err!=nil||got!=want{t.Fatal("same",got,want,err)}
  got,err=app.different(ctx,left,right);if err!=nil||got==want{t.Fatal("different",got,want,err)}
  guardedWant:=left!=nil&&right!=nil&&*left==*right
  got,err=app.guardedSame(ctx,left,right);if err!=nil||got!=guardedWant{t.Fatal("guarded comparison",got,guardedWant,err)}
  got,err=app.guardedWhen(ctx,left,right);if err!=nil||got!=guardedWant{t.Fatal("guarded when",got,guardedWant,err)}
 }}
 for _,value:=range values{
  want:=value!=nil&&*value==a
  got,err:=app.sameLeft(ctx,value,a);if err!=nil||got!=want{t.Fatal("left",got,want,err)}
  got,err=app.sameRight(ctx,a,value);if err!=nil||got!=want{t.Fatal("right",got,want,err)}
 }
}
`)
			runGeneratedFixture(t, source, assertions, StableIDs{})
		})
	}
}

func TestGeneratedDateTimeComparisons(t *testing.T) {
	for _, operator := range []string{"==", "!=", "<", "<=", ">", ">="} {
		t.Run(operator, func(t *testing.T) {
			runGeneratedFixture(t, `fn compare(left: DateTime, right: DateTime): Boolean { return left `+operator+` right }`, `package generated
import("testing";"time")

func TestCompareInstants(t *testing.T){
 base:=time.Date(2026,1,2,3,4,5,123456789,time.UTC)
 for _,left:=range []time.Time{base,time.Now()}{
  for _,right:=range []time.Time{left.In(time.FixedZone("offset",9*3600)),left.Round(0),left.Add(-time.Nanosecond),left.Add(time.Nanosecond)}{
   order:=left.Compare(right)
   want:=order `+operator+` 0
   got,err:=(&App{}).compare(t.Context(),left,right)
   if err!=nil||got!=want{t.Fatalf("left=%v right=%v got=%v want=%v err=%v",left,right,got,want,err)}
  }
 }
}
`, StableIDs{})
		})
	}
}

func TestGeneratedNullableDateTimeEquality(t *testing.T) {
	runGeneratedFixture(t, `
fn same(left: DateTime?, right: DateTime?): Boolean { return left == right }
fn different(left: DateTime?, right: DateTime?): Boolean { return left != right }
fn sameLeft(left: DateTime?, right: DateTime): Boolean { return left == right }
fn sameRight(left: DateTime, right: DateTime?): Boolean { return left == right }
fn missing(value: DateTime?): Boolean { return value == null }
fn narrowed(left: DateTime?, right: DateTime): Boolean { if left != null { return left == right } return false }
fn nextDate(value: Int): Result<DateTime?> @native
fn compareCalls(): Boolean { return nextDate(1)? == nextDate(2)? }
`, `package generated
import("testing";"time";"context";"errors";"slices")
type dateResolver struct{calls []int64;failAt int64;values [2]*time.Time}
var dateFailure=errors.New("date failed")
func(r *dateResolver)NextDate(_ context.Context,value int64)(*time.Time,error){
 r.calls=append(r.calls,value);if value==r.failAt{return nil,dateFailure};return r.values[value-1],nil
}
func TestDateEvaluation(t *testing.T){
 a:=time.Now();b:=a.In(time.FixedZone("offset",9*3600))
 for _,values:=range [][2]*time.Time{{nil,nil},{nil,&a},{&a,nil},{&a,&b}}{for _,failAt:=range []int64{0,1,2}{
  r:=&dateResolver{values:values,failAt:failAt};got,err:=(&App{Resolver:r}).compareCalls(t.Context())
  calls:=[]int64{1,2};if failAt==1{calls=[]int64{1}}
  if !slices.Equal(r.calls,calls){t.Fatal("evaluation order",r.calls,calls)}
  if failAt!=0{if !errors.Is(err,dateFailure){t.Fatal("lost error",err)};continue}
  want:=values[0]==nil&&values[1]==nil||values[0]!=nil&&values[1]!=nil&&values[0].Equal(*values[1])
  if err!=nil||got!=want{t.Fatal(got,want,err)}
 }}
}
func TestNullableInstants(t *testing.T){
 app:=&App{};ctx:=t.Context()
 a:=time.Date(2026,1,2,3,4,5,123456789,time.UTC);b:=a.In(time.FixedZone("offset",9*3600));c:=a.Add(time.Nanosecond)
 values:=[]*time.Time{nil,&a,&b,&c}
 for _,left:=range values{for _,right:=range values{
  want:=left==nil&&right==nil||left!=nil&&right!=nil&&left.Equal(*right)
  got,err:=app.same(ctx,left,right);if err!=nil||got!=want{t.Fatal("same",left,right,got,want,err)}
  got,err=app.different(ctx,left,right);if err!=nil||got==want{t.Fatal("different",got,want,err)}
 }}
 for _,value:=range values{
  want:=value!=nil&&value.Equal(a)
  for _,call:=range []func()(bool,error){func()(bool,error){return app.sameLeft(ctx,value,a)},func()(bool,error){return app.sameRight(ctx,a,value)},func()(bool,error){return app.narrowed(ctx,value,a)}}{
   got,err:=call();if err!=nil||got!=want{t.Fatal("mixed/narrowed",got,want,err)}
  }
  got,err:=app.missing(ctx,value);if err!=nil||got!=(value==nil){t.Fatal("null",got,err)}
 }
}
var dateComparisonSink bool
func(a *App)referenceDate(_ context.Context,left,right *time.Time)(bool,error){return left==right||left!=nil&&right!=nil&&left.Equal(*right),nil}
func(a *App)oldPointerDate(_ context.Context,left,right *time.Time)(bool,error){return left==right,nil}
func BenchmarkNullableDateEquality(b *testing.B){
 app:=&App{};ctx:=context.Background()
 a:=time.Date(2026,1,2,3,4,5,123456789,time.UTC);same:=a.In(time.FixedZone("offset",9*3600));different:=a.Add(time.Nanosecond)
 for _,input:=range []struct{name string;left,right *time.Time}{
  {"null",nil,nil},{"one-null",&a,nil},{"same-pointer",&a,&a},{"same-instant",&a,&same},{"different",&a,&different},
 }{for _,implementation:=range []struct{name string;call func(context.Context,*time.Time,*time.Time)(bool,error)}{
  {"generated",app.same},
  {"reference",app.referenceDate},
  {"old-pointer",app.oldPointerDate},
 }{b.Run(input.name+"/"+implementation.name,func(b *testing.B){
  b.ReportAllocs();for b.Loop(){var err error;dateComparisonSink,err=implementation.call(ctx,input.left,input.right);if err!=nil{b.Fatal(err)}}
 })}}
}
`, StableIDs{})
}

func TestGeneratedDecimalComparisons(t *testing.T) {
	for _, operator := range []string{"==", "!=", "<", "<=", ">", ">="} {
		t.Run(operator, func(t *testing.T) {
			runGeneratedFixture(t, `fn compare(left: Decimal, right: Decimal): Boolean { return left `+operator+` right }`, `package generated
import("testing";"github.com/shopspring/decimal")

func TestDecimalValues(t *testing.T){
 values:=[]string{"0","0.00","1","1.000","-1","12345678901234567890.123456789","12345678901234567890.123456788"}
 for _,leftText:=range values{for _,rightText:=range values{
  left,right:=decimal.RequireFromString(leftText),decimal.RequireFromString(rightText)
  want:=left.Cmp(right) `+operator+` 0
  got,err:=(&App{}).compare(t.Context(),left,right)
  if err!=nil||got!=want{t.Fatalf("%s / %s: got=%v want=%v err=%v",leftText,rightText,got,want,err)}
 }}
}
`, StableIDs{})
		})
	}
}

func TestGeneratedNullableDecimalEquality(t *testing.T) {
	runGeneratedFixture(t, `
fn same(left: Decimal?, right: Decimal?): Boolean { return left == right }
fn different(left: Decimal?, right: Decimal?): Boolean { return left != right }
fn sameLeft(left: Decimal?, right: Decimal): Boolean { return left == right }
fn sameRight(left: Decimal, right: Decimal?): Boolean { return left == right }
fn narrowed(left: Decimal?, right: Decimal): Boolean { if left != null { return left == right } return false }
`, `package generated
import("testing";"context";"github.com/shopspring/decimal")

func TestNullableDecimalValues(t *testing.T){
 app:=&App{};ctx:=t.Context()
 a:=decimal.RequireFromString("12345678901234567890.123456789")
 b:=decimal.RequireFromString("12345678901234567890.1234567890")
 c:=decimal.RequireFromString("12345678901234567890.123456788")
 values:=[]*decimal.Decimal{nil,&a,&b,&c}
 for _,left:=range values{for _,right:=range values{
  want:=left==nil&&right==nil||left!=nil&&right!=nil&&left.Equal(*right)
  got,err:=app.same(ctx,left,right);if err!=nil||got!=want{t.Fatal("same",got,want,err)}
  got,err=app.different(ctx,left,right);if err!=nil||got==want{t.Fatal("different",got,want,err)}
 }}
 for _,value:=range values{
  want:=value!=nil&&value.Equal(a)
  for _,call:=range []func()(bool,error){func()(bool,error){return app.sameLeft(ctx,value,a)},func()(bool,error){return app.sameRight(ctx,a,value)},func()(bool,error){return app.narrowed(ctx,value,a)}}{
   got,err:=call();if err!=nil||got!=want{t.Fatal("mixed/narrowed",got,want,err)}
  }
 }
}
var decimalComparisonSink bool
func(a *App)referenceDecimal(_ context.Context,left,right *decimal.Decimal)(bool,error){return left==right||left!=nil&&right!=nil&&left.Equal(*right),nil}
func(a *App)oldPointerDecimal(_ context.Context,left,right *decimal.Decimal)(bool,error){return left==right,nil}
func BenchmarkNullableDecimalEquality(b *testing.B){
 app:=&App{};ctx:=context.Background()
 a:=decimal.RequireFromString("12345678901234567890.123456789")
 same:=decimal.RequireFromString("12345678901234567890.123456789")
 scaled:=decimal.RequireFromString("12345678901234567890.1234567890")
 different:=decimal.RequireFromString("12345678901234567890.123456788")
 for _,input:=range []struct{name string;left,right *decimal.Decimal}{
  {"null",nil,nil},{"one-null",&a,nil},{"same-pointer",&a,&a},{"same-value",&a,&same},{"different-scale",&a,&scaled},{"different",&a,&different},
 }{for _,implementation:=range []struct{name string;call func(context.Context,*decimal.Decimal,*decimal.Decimal)(bool,error)}{
  {"generated",app.same},{"reference",app.referenceDecimal},{"old-pointer",app.oldPointerDecimal},
 }{b.Run(input.name+"/"+implementation.name,func(b *testing.B){
  b.ReportAllocs();for b.Loop(){var err error;decimalComparisonSink,err=implementation.call(ctx,input.left,input.right);if err!=nil{b.Fatal(err)}}
 })}}
}
`, StableIDs{})
}

func TestGeneratedNumericAggregates(t *testing.T) {
	runGeneratedFixture(t, `
model Metric @noTime { id: Int @id i: Int f: Float d: Decimal }
type Totals { integer: Int average: Float exact: Decimal }
fn totals(): Totals {
 val (ctx, req, err) = await {
  Metric.where(id > 0).sum { it.i }
  Metric.where(id > 0).avg { it.f }
  Metric.where(id > 0).sum { it.d }
 }
 return Totals { integer: ctx, average: req, exact: err }
}
fn intTotal(): Int { return Metric.where(id > 0).sum { row -> row.i } }
fn floatTotal(): Float { return Metric.where(id > 0).sum { it.f } }
fn decimalTotal(): Decimal { return Metric.where(id > 0).sum { it.d } }
fn fieldReferenceTotal(): Int { return Metric.where(id > 0).sum(it.i) }
fn aliasTotal(): Decimal { val query = Metric.where(id > 0) return query.sum(d) }
api summary(): Totals { return totals() }
`, `package generated
import (
 "testing"
 "github.com/light-speak/luxo/pkg/lux/pg"
 "github.com/testcontainers/testcontainers-go"
 "github.com/testcontainers/testcontainers-go/modules/postgres"
 "github.com/light-speak/luxo/internal/testutil"
)
func TestNumericAggregates(t *testing.T) {
 ctx:=t.Context()
 container,err:=postgres.Run(ctx,testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container)
 if err!=nil {t.Fatal(err)}
 connection,err:=container.ConnectionString(ctx,"sslmode=disable");if err!=nil {t.Fatal(err)}
 db,err:=pg.NewDB(ctx,connection);if err!=nil {t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE metrics(id bigint PRIMARY KEY,i bigint,f double precision,d numeric); INSERT INTO metrics VALUES(1,9007199254740993,1.25,12345678901234567890.123456789),(2,2,2.5,0.000000001)");err!=nil {t.Fatal(err)}
 app:=NewFromDB(db)
 got,err:=app.totals(ctx)
 if err!=nil||got.Integer!=9007199254740995||got.Average!=1.875||got.Exact.String()!="12345678901234567890.12345679" {t.Fatal(got,err)}
 if value,err:=app.fieldReferenceTotal(ctx);err!=nil||value!=got.Integer {t.Fatal(value,err)}
 if value,err:=app.intTotal(ctx);err!=nil||value!=got.Integer {t.Fatal(value,err)}
 if value,err:=app.floatTotal(ctx);err!=nil||value!=3.75 {t.Fatal(value,err)}
 if value,err:=app.decimalTotal(ctx);err!=nil||!value.Equal(got.Exact) {t.Fatal(value,err)}
 if value,err:=app.aliasTotal(ctx);err!=nil||!value.Equal(got.Exact) {t.Fatal(value,err)}
}
`, StableIDs{APIs: map[string]int{"summary": 1}, ModelFields: map[string]map[string]int{
		"Metric": {"id": 1, "i": 2, "f": 3, "d": 4}, "Totals": {"integer": 1, "average": 2, "exact": 3},
	}})
}

func TestAggregateFieldName(t *testing.T) {
	cases := []struct {
		expr ast.Expr
		want string
	}{
		{&ast.Ident{Name: "req"}, "req"},
		{&ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "amount"}, "amount"},
		{&ast.MemberExpr{Object: &ast.Ident{Name: "other"}, Field: "amount"}, ""},
		{&ast.MemberExpr{Object: &ast.Literal{}, Field: "amount"}, ""},
		{&ast.LambdaExpr{}, ""},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := aggregateFieldName(tc.expr); got != tc.want {
			t.Fatalf("%T: %q != %q", tc.expr, got, tc.want)
		}
	}
}

func TestGeneratedArithmeticPreservesGrouping(t *testing.T) {
	runGeneratedFixture(t, `
fn scaled(a: Int, b: Int, c: Int): Int { return (a + b) * c }
fn difference(a: Int, b: Int, c: Int): Int { return a - (b - c) }
fn quotient(a: Int, b: Int, c: Int): Int { return a / (b / c) }
fn negated(a: Int, b: Int): Int { return -(a + b) }
fn shifted(value: DateTime, a: Duration, b: Duration): DateTime { return value - (a + b) }
`, `package generated
import("context";"testing";"time")

func TestArithmeticGrouping(t *testing.T){
 app:=&App{}
 for _,sample:=range []struct{name string;call func(context.Context,int64,int64,int64)(int64,error);want int64}{
  {"scaled",app.scaled,112},{"difference",app.difference,16},{"quotient",app.quotient,10},
 }{got,err:=sample.call(t.Context(),20,8,4);if err!=nil||got!=sample.want{t.Errorf("%s got %d want %d err=%v",sample.name,got,sample.want,err)}}
 if got,err:=app.negated(t.Context(),20,8);err!=nil||got != -28 {t.Errorf("negated=%d err=%v",got,err)}
 origin:=time.Date(2026,1,1,12,0,0,0,time.UTC);got,err:=app.shifted(t.Context(),origin,time.Hour,2*time.Hour);if err!=nil||!got.Equal(origin.Add(-3*time.Hour)){t.Errorf("shifted=%v err=%v",got,err)}
}
`, StableIDs{})
}

func TestGeneratedBooleanTransforms(t *testing.T) {
	runGeneratedFixture(t, `
model Flag @noTime { id: Int @id enabled: Boolean @transform { true } disabled: Boolean @transform { false } }
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestBooleanTransforms(t *testing.T) {
 input:=&Flag{Id:1,Enabled:false,Disabled:true}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{7},nil);if err!=nil{t.Fatal(err)}
 check:=func(value *Flag){t.Helper();if !value.Enabled||value.Disabled{t.Fatal(value)}}
 buf:=&api.ResponseBuf{};if err:=input.WriteLuxo(buf,mask);err!=nil{t.Fatal(err)}
 dec:=codec.NewDecoder(buf.B);var output Flag;output.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)};check(&output)
 for _,write:=range []func(*api.ResponseBuf)error{
 func(buf *api.ResponseBuf)error{return WriteColumnarFlag(buf,[]*Flag{input},mask)},
 func(buf *api.ResponseBuf)error{return WriteColumnarFlagValues(buf,[]Flag{*input},mask)},
 } {buf:=&api.ResponseBuf{};if err:=write(buf);err!=nil{t.Fatal(err)};rows,err:=ReadColumnarFlag(buf.B);if err!=nil||len(rows)!=1{t.Fatal(rows,err)};check(&rows[0])}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Flag": {"id": 1, "enabled": 2, "disabled": 3}}})
}

func TestGeneratedArithmeticTransform(t *testing.T) {
	runGeneratedFixture(t, `model Counter @noTime { id: Int @id amount: Int @transform { -(it + 2) * 3 } }`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestArithmeticTransform(t *testing.T){
 value:=&Counter{Id:1,Amount:4};mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 buf:=&api.ResponseBuf{};if err:=value.WriteLuxo(buf,mask);err!=nil{t.Fatal(err)}
 dec:=codec.NewDecoder(buf.B);var got Counter;got.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)};if got.Amount != -18{t.Fatal(got.Amount)}
 for _,write:=range []func(*api.ResponseBuf)error{
 func(buf *api.ResponseBuf)error{return WriteColumnarCounter(buf,[]*Counter{value},mask)},
 func(buf *api.ResponseBuf)error{return WriteColumnarCounterValues(buf,[]Counter{*value},mask)},
 }{buf:=&api.ResponseBuf{};if err:=write(buf);err!=nil{t.Fatal(err)};rows,err:=ReadColumnarCounter(buf.B);if err!=nil||len(rows)!=1{t.Fatal(rows,err)};if rows[0].Amount != -18{t.Fatal(rows[0].Amount)}}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "amount": 2}}})
}

func TestGeneratedBeforeSaveDatabaseUpdate(t *testing.T) {
	runGeneratedTransactionFixture(t, `model User @noTime @crud { id: Int @id name: Int @beforeSave { it += 2 it *= 3 } }
fn directCreate(id: Int): User { return User.create(id: id, name: 4) }`, `package generated
import("bytes";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres"
 "github.com/light-speak/luxo/internal/testutil")
func TestBeforeSaveDatabaseUpdate(t *testing.T){
 ctx:=t.Context();container,err:=postgres.Run(ctx,testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies());testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(ctx,"sslmode=disable");if err!=nil{t.Fatal(err)};db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE users(id bigint PRIMARY KEY,name bigint NOT NULL); INSERT INTO users VALUES(1,0)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,binary:=range []bool{false,true}{
 body:=[]byte("{\"$api\":\"updateUser\",\"$select\":\"id,name\",\"id\":1,\"name\":4}")
 if binary{mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)};body,err=api.EncodeBinaryRequest(router.Schema.APIs["updateUser"].ID,mask,map[string]any{"id":int64(1),"name":int64(4)},router.Registry.ParamOrder("updateUser"));if err!=nil{t.Fatal(err)}}
 request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
 response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatal(binary,response.Code,response.Body.String())}
 row,err:=app.User.Find(ctx,1);if err!=nil||row.Name!=18{t.Fatal(binary,row,err)}
 }
 direct,err:=app.directCreate(ctx,2);if err!=nil{t.Fatal(err)};persisted,err:=app.User.Find(ctx,direct.Id);if err!=nil||persisted.Name!=18{t.Fatalf("direct create value=%v err=%v",persisted,err)}
}
`)
}

func TestGeneratedBeforeSaveNullableMutations(t *testing.T) {
	runGeneratedTransactionFixture(t, `model User @noTime @crud { id: Int @id name: Int? @beforeSave { it += 2 it *= 3 } }`, `package generated
import("bytes";"encoding/json";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres"
 "github.com/light-speak/luxo/internal/testutil")
func TestBeforeSaveNullableMutations(t *testing.T){
 ctx:=t.Context();container,err:=postgres.Run(ctx,testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies());testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(ctx,"sslmode=disable");if err!=nil{t.Fatal(err)};db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE users(id bigserial PRIMARY KEY,name bigint)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 var nextID int64
 for _,binary:=range []bool{false,true}{
 call:=func(name string,params map[string]any)*User{t.Helper();var body []byte;var err error
 if binary{mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)};body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,mask,params,router.Registry.ParamOrder(name));if err!=nil{t.Fatal(err)}}else{payload:=map[string]any{"$api":name,"$select":"id,name"};for key,value:=range params{payload[key]=value};body,err=json.Marshal(payload);if err!=nil{t.Fatal(err)}}
 request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")};response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatal(binary,name,response.Code,response.Body.String())}
 var result User;if binary{dec:=codec.NewDecoder(response.Body.Bytes());result.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)}}else{var envelope struct{Data User};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil{t.Fatal(err)};result=envelope.Data};return &result
 }
 for _,params:=range []map[string]any{{},{"name":nil},{"name":int64(4)}}{
 nextID++;params["id"]=nextID
 created:=call("createUser",params);row,err:=app.User.Find(ctx,created.Id);if err!=nil{t.Fatal(err)}
 if params["name"]==nil{if row.Name!=nil{t.Fatal("null transformed",row)}}else if row.Name==nil||*row.Name!=18{t.Fatal("create transform",row)}
 call("updateUser",map[string]any{"id":created.Id,"name":int64(10)});row,err=app.User.Find(ctx,created.Id);if err!=nil||row.Name==nil||*row.Name!=36{t.Fatal("update transform",row,err)}
 call("updateUser",map[string]any{"id":created.Id,"name":nil});row,err=app.User.Find(ctx,created.Id);if err!=nil||row.Name!=nil{t.Fatal("null update",row,err)}
 }
 }
}
`)
}

func TestGeneratedDocumentedStringTransform(t *testing.T) {
	runGeneratedTransactionFixture(t, `model User @noTime { id: Int @id name: String @transform { it.trim().lowercase().replace(" ", "-") } }`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestDocumentedStringTransform(t *testing.T){
 value:=&User{Id:1,Name:" A B "};mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 buf:=&api.ResponseBuf{};if err:=value.WriteLuxo(buf,mask);err!=nil{t.Fatal(err)}
 dec:=codec.NewDecoder(buf.B);var got User;got.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)};if got.Name!="a-b"{t.Fatal(got.Name)}
 for _,write:=range []func(*api.ResponseBuf)error{
 func(buf *api.ResponseBuf)error{return WriteColumnarUser(buf,[]*User{value},mask)},
 func(buf *api.ResponseBuf)error{return WriteColumnarUserValues(buf,[]User{*value},mask)},
 }{buf:=&api.ResponseBuf{};if err:=write(buf);err!=nil{t.Fatal(err)};rows,err:=ReadColumnarUser(buf.B);if err!=nil||len(rows)!=1{t.Fatal(rows,err)};if rows[0].Name!="a-b"{t.Fatal(rows[0].Name)}}
 if value.Name!=" A B "{t.Fatal("serialization modified the source model")}
}
`)
}

func TestGeneratedTransformFunctionCall(t *testing.T) {
	runGeneratedTransactionFixture(t, `
fn normalize(value: String): String { return value.trim() }
model User @noTime { id: Int @id name: String @transform { normalize(it) } }
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestTransformFunctionCall(t *testing.T){
 value:=&User{Id:1,Name:" normalized "};mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 buf:=&api.ResponseBuf{};if err:=value.WriteLuxo(buf,mask);err!=nil{t.Fatal(err)}
 dec:=codec.NewDecoder(buf.B);var got User;got.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)};if got.Name!="normalized"{t.Fatal(got.Name)}
 for _,write:=range []func(*api.ResponseBuf)error{
  func(out *api.ResponseBuf)error{return WriteColumnarUser(out,[]*User{value},mask)},
  func(out *api.ResponseBuf)error{return WriteColumnarUserValues(out,[]User{*value},mask)},
 }{
  out:=&api.ResponseBuf{};if err:=write(out);err!=nil{t.Fatal(err)}
  rows,err:=ReadColumnarUser(out.B);if err!=nil||len(rows)!=1{t.Fatal(rows,err)}
  if rows[0].Name!="normalized"{t.Fatal(rows[0].Name)}
 }
 if value.Name!=" normalized "{t.Fatal("transformation changed the model")}
}
`)
}

func TestGeneratedBeforeSaveFunctionCall(t *testing.T) {
	runGeneratedTransactionFixture(t, `
fn normalize(value: String): String { return value.trim() }
model User @noTime { id: Int @id name: String @beforeSave { normalize(it) } }
`, `package generated
import "testing"
func TestBeforeSaveFunctionCall(t *testing.T) {
 value := " normalized "
 if got,err := prepareSave4UserName(value); err!=nil||got != "normalized" { t.Fatal(got,err) }
 if value != " normalized " { t.Fatal("preparation changed the input") }
}
`)
}

func TestGeneratedFieldDirectiveStringProperties(t *testing.T) {
	runGeneratedFixture(t, `model User @noTime {
 id: Int @id
 length: Int @transform { "abc".length } @beforeSave { "abc".length }
 size: Int @transform { "abc".trim().size } @beforeSave { "abc".trim().size }
 empty: Boolean @transform { " ".trim().isEmpty } @beforeSave { " ".trim().isEmpty }
 }`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestStringProperties(t *testing.T) {
 if prepareSave4UserLength(0)!=3 || prepareSave4UserSize(0)!=3 || !prepareSave4UserEmpty(false){t.Fatal("save properties")}
 value:=&User{Id:1}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{15},nil);if err!=nil{t.Fatal(err)}
 check:=func(row *User){t.Helper();if row.Length!=3||row.Size!=3||!row.Empty{t.Fatal(row)}}
 out:=&api.ResponseBuf{};if err:=value.WriteLuxo(out,mask);err!=nil{t.Fatal(err)}
 dec:=codec.NewDecoder(out.B);var row User;row.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)};check(&row)
 for _,write:=range []func(*api.ResponseBuf)error{
  func(out *api.ResponseBuf)error{return WriteColumnarUser(out,[]*User{value},mask)},
  func(out *api.ResponseBuf)error{return WriteColumnarUserValues(out,[]User{*value},mask)},
 }{out:=&api.ResponseBuf{};if err:=write(out);err!=nil{t.Fatal(err)};rows,err:=ReadColumnarUser(out.B);if err!=nil||len(rows)!=1{t.Fatal(rows,err)};check(&rows[0])}
 if value.Length!=0||value.Size!=0||value.Empty{t.Fatal("changed source model")}
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "length": 2, "size": 3, "empty": 4}}})
}

func TestGeneratedFieldDirectiveEnumConstant(t *testing.T) {
	runGeneratedFixture(t, `
enum State { OPEN CLOSED }
model User @noTime {
 id: Int @id
 state: State @transform { State.CLOSED } @beforeSave { State.CLOSED }
}
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestEnumConversion(t *testing.T) {
 value:=&User{Id:1,State:StateOPEN}
 if got:=prepareSave4UserState(StateOPEN);got!=StateCLOSED{t.Fatal(got)}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 out:=&api.ResponseBuf{};if err:=value.WriteLuxo(out,mask);err!=nil{t.Fatal(err)}
 dec:=codec.NewDecoder(out.B);var got User;got.ReadLuxo(dec)
 if err:=dec.Finish();err!=nil{t.Fatal(err)};if got.State!=StateCLOSED{t.Fatal(got.State)}
 for _,write:=range []func(*api.ResponseBuf)error{
  func(out *api.ResponseBuf)error{return WriteColumnarUser(out,[]*User{value},mask)},
  func(out *api.ResponseBuf)error{return WriteColumnarUserValues(out,[]User{*value},mask)},
 }{
  out:=&api.ResponseBuf{};if err:=write(out);err!=nil{t.Fatal(err)}
  rows,err:=ReadColumnarUser(out.B);if err!=nil||len(rows)!=1{t.Fatal(rows,err)}
  if rows[0].State!=StateCLOSED{t.Fatal(rows[0].State)}
 }
 if value.State!=StateOPEN{t.Fatal("conversion changed source model")}
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "state": 2}}})
}

func TestGeneratedEnumMemberCase(t *testing.T) {
	runGeneratedFixture(t, `
enum State { Open OPEN }
fn member(): State { return State.Open }
fn defaultMember(value: State = State.Open): State { return value }
fn useDefault(): State { return defaultMember() }
api literal(): State { return member() }
api echo(value: State = State.Open): State { return value }
`, `package generated
import("testing";"bytes";"encoding/json";"net/http/httptest";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")

func TestMemberCase(t *testing.T) {
 app:=&App{}
 for _,call:=range []func()(State,error){
  func()(State,error){return app.member(t.Context())},
  func()(State,error){return app.useDefault(t.Context())},
 }{
  value,err:=call();if err!=nil||value!=StateOpen||value==StateOPEN{t.Fatalf("enum member changed: %q %v",value,err)}
 }
}
func TestHTTPMemberCase(t *testing.T) {
 router:=api.NewRouter()
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,sample:=range []struct{api,input,want string}{
  {"literal","","Open"},{"echo","","Open"},{"echo","Open","Open"},{"echo","OPEN","OPEN"},
 }{for _,binary:=range []bool{false,true}{
  params:=map[string]any{};if sample.input!=""{params["value"]=sample.input}
  var body []byte;var err error
  if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs[sample.api].ID,nil,params,router.Registry.ParamOrder(sample.api))
  }else{params["$api"]=sample.api;body,err=json.Marshal(params)}
  if err!=nil{t.Fatal(err)}
  req:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));req.Header.Set("Content-Type","application/json")
  if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")}
  out:=httptest.NewRecorder();router.ServeHTTP(out,req)
  if out.Code!=200{t.Fatalf("%s binary=%v status=%d body=%s",sample.api,binary,out.Code,out.Body)}
  var got string
  if binary{dec:=codec.NewDecoder(out.Body.Bytes());got=dec.ReadString();if err:=dec.Finish();err!=nil{t.Fatal(err)}
  }else{var value struct{Data string};if err:=json.Unmarshal(out.Body.Bytes(),&value);err!=nil{t.Fatal(err)};got=value.Data}
  if got!=sample.want{t.Fatalf("%s input=%q binary=%v got=%q want=%q",sample.api,sample.input,binary,got,sample.want)}
 }}
}
`, StableIDs{APIs: map[string]int{"literal": 1, "echo": 2}, APIParams: map[string]map[string]int{"echo": {"value": 1}}})
}

func TestGeneratedUnusedLocalEffects(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id value: Int }
fn advanceUnused(counter: Counter): Int { counter.value += 1 return counter.value }
fn failUnused(): Int { throw error.NotFound }
fn unusedLocals(counter: Counter, fail: Boolean): Int {
 val absent: Int? = null
 val empty: [Int] = []
 var assigned: Int = 1
 assigned = 2
 val ignored = advanceUnused(counter)
 if fail { val failed = failUnused() }
 return counter.value
}
`, `package generated
import "testing"
func TestUnusedEffects(t *testing.T){
 app:=&App{}
 for _,fail:=range []bool{false,true}{
  counter:=&Counter{Id:1}
  value,err:=app.unusedLocals(t.Context(),counter,fail)
  if counter.Value!=1{t.Fatalf("initializer ran %d times",counter.Value)}
  if fail { if err==nil{t.Fatal("unused initializer swallowed error")} } else if err!=nil||value!=1{t.Fatal(value,err)}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}})
}

func TestGeneratedTypedAssignments(t *testing.T) {
	runGeneratedFixture(t, `
fn numberAssignment(): Int { var value: Int = 1 value = 2 value += 3 value *= 4 return value }
fn textAssignment(): String { var value: String = "a" value += "b" return value }
fn nullableAssignment(): Int? { var value: Int? = 1 value = null return value }
fn narrowedAssignment(): Int? { var value: Int? = 1 if value != null { value += 2 } return value }
fn nullableValueAssignment(): Int? { var value: Int? = null value = 7 return value }
fn listAssignment(): [Int] { var values: [Int] = [1] values = [] return values }
`, `package generated
import "testing"

func TestTypedAssignments(t *testing.T){
 app:=&App{};ctx:=t.Context()
 if value,err:=app.numberAssignment(ctx);err!=nil||value!=20{t.Fatal(value,err)}
 if value,err:=app.textAssignment(ctx);err!=nil||value!="ab"{t.Fatal(value,err)}
 if value,err:=app.nullableAssignment(ctx);err!=nil||value!=nil{t.Fatal(value,err)}
 if value,err:=app.narrowedAssignment(ctx);err!=nil||value==nil||*value!=3{t.Fatal(value,err)}
 if value,err:=app.nullableValueAssignment(ctx);err!=nil||value==nil||*value!=7{t.Fatal(value,err)}
 if value,err:=app.listAssignment(ctx);err!=nil||len(value)!=0{t.Fatal(value,err)}
}
`, StableIDs{})
}

func TestGeneratedDateCompoundAssignment(t *testing.T) {
	runGeneratedFixture(t, `
fn shiftCompound(value: DateTime, forward: Duration, back: Duration): DateTime {
 var result: DateTime = value
 result += forward
 result -= back
 return result
}
fn shiftNullable(value: DateTime?, amount: Duration): DateTime? {
 var result: DateTime? = value
 if result != null { result += amount }
 return result
}
`, `package generated
import("testing";"time")

func TestDateCompoundAssignment(t *testing.T){
 app:=&App{};ctx:=t.Context();origin:=time.Date(2026,1,1,12,0,0,123456789,time.UTC)
 got,err:=app.shiftCompound(ctx,origin,2*time.Hour,time.Hour)
 if err!=nil||!got.Equal(origin.Add(time.Hour)){t.Fatal(got,err)}
 nullable,err:=app.shiftNullable(ctx,&origin,time.Nanosecond)
 if err!=nil||nullable==nil||!nullable.Equal(time.Date(2026,1,1,12,0,0,123456790,time.UTC)){t.Fatal(nullable,err)}
 nullable,err=app.shiftNullable(ctx,nil,time.Hour)
 if err!=nil||nullable!=nil{t.Fatal(nullable,err)}
}
`, StableIDs{})
}

func TestGeneratedTypeDateTransform(t *testing.T) {
	runGeneratedFixture(t, `type Clock { value: DateTime @transform { it - 30m } }`, `package generated
import("testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestTypeDateTransform(t *testing.T){
 origin:=time.Unix(3600,123456789);input:=Clock{Value:origin};mask,err:=codec.AppendSelectionMask(nil,[]byte{1},nil);if err!=nil{t.Fatal(err)}
 buf:=&api.ResponseBuf{};if err:=input.WriteLuxo(buf,mask);err!=nil{t.Fatal(err)};dec:=codec.NewDecoder(buf.B);var got Clock;got.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)}
 if !got.Value.Equal(origin.Add(-30*time.Minute))||!input.Value.Equal(origin){t.Fatal(got,input)}
 buf.B=buf.B[:0];if err:=WriteColumnarClock(buf,[]Clock{input},mask);err!=nil{t.Fatal(err)};rows,err:=ReadColumnarClock(buf.B);if err!=nil||len(rows)!=1||!rows[0].Value.Equal(got.Value){t.Fatal(rows,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Clock": {"value": 1}}})
}

func TestGeneratedDateTransform(t *testing.T) {
	runGeneratedFixture(t, `model Clock @noTime { id: Int @id value: DateTime @transform { it - 30m } }`, `package generated
import("testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestDateTransform(t *testing.T){
 origin:=time.Unix(3600,123456789);input:=&Clock{Id:1,Value:origin};mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 check:=func(got Clock){t.Helper();if !got.Value.Equal(origin.Add(-30*time.Minute)){t.Fatal(got.Value)}}
 buf:=&api.ResponseBuf{};if err:=input.WriteLuxo(buf,mask);err!=nil{t.Fatal(err)};dec:=codec.NewDecoder(buf.B);var got Clock;got.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)};check(got)
 for _,write:=range []func(*api.ResponseBuf)error{
 func(buf *api.ResponseBuf)error{return WriteColumnarClock(buf,[]*Clock{input},mask)},
 func(buf *api.ResponseBuf)error{return WriteColumnarClockValues(buf,[]Clock{*input},mask)},
 }{buf:=&api.ResponseBuf{};if err:=write(buf);err!=nil{t.Fatal(err)};rows,err:=ReadColumnarClock(buf.B);if err!=nil||len(rows)!=1{t.Fatal(rows,err)};check(rows[0])}
 if !input.Value.Equal(origin){t.Fatal("transform mutated source")}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Clock": {"id": 1, "value": 2}}})
}

func TestGeneratedWritableFieldAssignments(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime { id: Int @id name: String }
type Data { value: Int }
fn updateUser(user: User): Int { user.id = 2 user.id += 1 return user.id }
fn updateData(data: Data): Int { data.value = 7 return data.value }
fn updateNullable(user: User?): Int { if user != null { user.id = 9 } return 1 }
`, `package generated
import "testing"
func TestWritableFields(t *testing.T){
 app:=&App{};ctx:=t.Context();user:=&User{Id:1}
 if got,err:=app.updateUser(ctx,user);err!=nil||got!=3||user.Id!=3{t.Fatal(got,user,err)}
 if got,err:=app.updateData(ctx,Data{Value:1});err!=nil||got!=7{t.Fatal(got,err)}
 if _,err:=app.updateNullable(ctx,user);err!=nil||user.Id!=9{t.Fatal(user,err)}
 if _,err:=app.updateNullable(ctx,nil);err!=nil{t.Fatal(err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2}, "Data": {"value": 1}}})
}

func TestGeneratedSafeMemberAccess(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime { id: Int @id name: String nickname: String? }
fn safeName(user: User?): String? { return user?.name }
fn safeLength(text: String?): Int? { return text?.length }
fn safeNickname(user: User?): String? { return user?.nickname }
fn safeNestedNickname(user: User?): String? { return user?.nickname?.trim() }
fn safeSeconds(value: Int?): Duration? { return value?.seconds }
fn safeTrim(text: String?): String? { return text?.trim() }
fn safeNested(user: User?): String? { return user?.name?.trim() }
fn replacement(): String { throw error.NotFound }
fn lazyReplace(text: String?): String? { return text?.replace("x", replacement()) }
fn nativeText(): Result<String> @native
fn loadText(): String? { val text: String? = nativeText()? return text }
fn safeFromCall(): String? { return loadText()?.trim() }
`, `package generated
import("context";"errors";"testing";"time")
type safeResolver struct{calls int;failure error}
func(r *safeResolver)NativeText(context.Context)(string,error){r.calls++;return " hello ",r.failure}
func TestSafeMembers(t *testing.T){
 app:=&App{};ctx:=t.Context()
 if value,err:=app.safeName(ctx,nil);err!=nil||value!=nil{t.Fatal(value,err)}
 if value,err:=app.safeName(ctx,&User{Name:"hello"});err!=nil||value==nil||*value!="hello"{t.Fatal(value,err)}
 if value,err:=app.safeLength(ctx,nil);err!=nil||value!=nil{t.Fatal(value,err)}
 text:="hello";if value,err:=app.safeLength(ctx,&text);err!=nil||value==nil||*value!=5{t.Fatal(value,err)}
 if value,err:=app.safeNickname(ctx,&User{});err!=nil||value!=nil{t.Fatal(value,err)}
 if value,err:=app.safeNickname(ctx,&User{Nickname:&text});err!=nil||value==nil||*value!=text{t.Fatal(value,err)}
 padded:=" hello ";if value,err:=app.safeTrim(ctx,&padded);err!=nil||value==nil||*value!=text{t.Fatal(value,err)}
 if value,err:=app.safeTrim(ctx,nil);err!=nil||value!=nil{t.Fatal(value,err)}
 if value,err:=app.safeNested(ctx,&User{Name:padded});err!=nil||value==nil||*value!=text{t.Fatal(value,err)}
 if value,err:=app.safeNested(ctx,nil);err!=nil||value!=nil{t.Fatal(value,err)}
 if value,err:=app.lazyReplace(ctx,nil);err!=nil||value!=nil{t.Fatal(value,err)}
 if _,err:=app.lazyReplace(ctx,&text);err==nil{t.Fatal("argument failure was lost")}
 resolver:=&safeResolver{};app.Resolver=resolver
 if value,err:=app.safeFromCall(ctx);err!=nil||value==nil||*value!=text||resolver.calls!=1{t.Fatal(value,err,resolver.calls)}
 if value,err:=app.safeNestedNickname(ctx,&User{});err!=nil||value!=nil{t.Fatal(value,err)}
 if value,err:=app.safeNestedNickname(ctx,&User{Nickname:&padded});err!=nil||value==nil||*value!=text{t.Fatal(value,err)}
 seconds:=int64(2);if value,err:=app.safeSeconds(ctx,&seconds);err!=nil||value==nil||*value!=2*time.Second{t.Fatal(value,err)}
 if value,err:=app.safeSeconds(ctx,nil);err!=nil||value!=nil{t.Fatal(value,err)}
 failure:=errors.New("receiver failed");resolver.failure=failure
 if _,err:=app.safeFromCall(ctx);!errors.Is(err,failure)||resolver.calls!=2{t.Fatal(err,resolver.calls)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2, "nickname": 3}}})
}

func TestGeneratedInstanceMutationCounts(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime { id: Int @id name: String }
fn removeUser(user: User): Int { return user.delete() }
fn renameUser(user: User, name: String): Int { return user.update(name: name) }
fn safeRemoveUser(user: User?): Int? { return user?.delete() }
fn safeRenameUser(user: User?, name: String): Int? { return user?.update(name: name) }
fn inferredMutation(user: User): Int { val count = user.delete() return count }
fn mutationName(): Result<String> @native
fn lazyRename(user: User?): Int? { return user?.update(name: mutationName()?) }
`, `package generated
import("testing";"os";"context";"errors";"github.com/light-speak/luxo/pkg/lux/pg")
type mutationResolver struct{calls int; failure error}
func(r *mutationResolver)MutationName(context.Context)(string,error){r.calls++;return "",r.failure}
func TestMutationArgumentShortCircuit(t *testing.T){
 failure:=errors.New("argument failure");resolver:=&mutationResolver{failure:failure};app:=&App{Resolver:resolver}
 if value,err:=app.lazyRename(t.Context(),nil);err!=nil||value!=nil||resolver.calls!=0{t.Fatal(value,err,resolver.calls)}
 if value,err:=app.lazyRename(t.Context(),&User{Id:1});!errors.Is(err,failure)||value!=nil||resolver.calls!=1{t.Fatal(value,err,resolver.calls)}
}
func TestNullMutation(t *testing.T){
 app:=&App{}
 if value,err:=app.safeRemoveUser(t.Context(),nil);err!=nil||value!=nil{t.Fatal(value,err)}
 if value,err:=app.safeRenameUser(t.Context(),nil,"new");err!=nil||value!=nil{t.Fatal(value,err)}
}

func TestDatabaseMutationCounts(t *testing.T){
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE users(id bigint PRIMARY KEY,name text NOT NULL); INSERT INTO users VALUES(1,'old')");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);user:=&User{Id:1}
 if count,err:=app.renameUser(ctx,user,"renamed");err!=nil||count!=1{t.Fatal(count,err)}
 if name,err:=db.QueryScalar[string](ctx,"SELECT name FROM users WHERE id=1");err!=nil||name!="renamed"{t.Fatal(name,err)}
 if count,err:=app.safeRenameUser(ctx,user,"safe");err!=nil||count==nil||*count!=1{t.Fatal(count,err)}
 if name,err:=db.QueryScalar[string](ctx,"SELECT name FROM users WHERE id=1");err!=nil||name!="safe"{t.Fatal(name,err)}
 if count,err:=app.renameUser(ctx,&User{Id:2},"missing");err!=nil||count!=0{t.Fatal(count,err)}
 if count,err:=app.safeRemoveUser(ctx,user);err!=nil||count==nil||*count!=1{t.Fatal(count,err)}
 if count,err:=app.removeUser(ctx,user);err!=nil||count!=0{t.Fatal(count,err)}
 if count,err:=app.safeRemoveUser(ctx,user);err!=nil||count==nil||*count!=0{t.Fatal(count,err)}
 if _,err:=pg.Exec(ctx,db,"DROP TABLE users");err!=nil{t.Fatal(err)}
 if _,err:=app.removeUser(ctx,user);err==nil{t.Fatal("delete swallowed database error")}
 if _,err:=app.renameUser(ctx,user,"bad");err==nil{t.Fatal("update swallowed database error")}
 if value,err:=app.safeRemoveUser(ctx,user);err==nil||value!=nil{t.Fatal("safe delete swallowed error",value,err)}
 if value,err:=app.safeRenameUser(ctx,user,"bad");err==nil||value!=nil{t.Fatal("safe update swallowed error",value,err)}
}

`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2}}})
}

func TestGeneratedNullableFunctionArguments(t *testing.T) {
	runGeneratedFixture(t, `
fn optionalInt(value: Int?): Int { return value ?: 0 }
fn passInt(value: Int): Int { return optionalInt(value) }
fn listIsNull(value: [Int]?): Boolean { return value == null }
fn passList(value: [Int]): Boolean { return listIsNull(value) }
fn optionalDefault(value: Int? = 7): Int { return value ?: 0 }
fn defaultArgumentCall(): Int { return optionalDefault() }
fn nativeOptional(value: Int?): Result<Int> @native
fn passNative(value: Int): Int { return nativeOptional(value)? }
`, `package generated
import("context";"testing")
type nullableResolver struct{}
func(*nullableResolver)NativeOptional(_ context.Context,value *int64)(int64,error){if value==nil{return 0,nil};return *value,nil}
func TestNullableFunctionArguments(t *testing.T){
 app:=&App{Resolver:&nullableResolver{}};ctx:=t.Context()
 if value,err:=app.passInt(ctx,42);err!=nil||value!=42{t.Fatal(value,err)}
 for _,input:=range [][]int64{nil,{}, {1}}{if value,err:=app.passList(ctx,input);err!=nil||value{t.Fatal(value,err)}}
 if value,err:=app.defaultArgumentCall(ctx);err!=nil||value!=7{t.Fatal(value,err)}
 if value,err:=app.passNative(ctx,42);err!=nil||value!=42{t.Fatal(value,err)}
}
`, StableIDs{})
}

func TestGeneratedListArgumentEvaluationOrder(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id value: Int }
type Snapshot { first: Int values: [Int] }
fn mutate(counter: Counter): Int { counter.value = 9 return 2 }
fn rejectValue(): Int { throw error.NotFound }
fn pair(first: Int, values: [Int], counter: Counter): Int {
 counter.id += 1
 return first * 10 + values.sumOf { it }
}
fn capture(counter: Counter): Int { return pair(counter.value, [mutate(counter)], counter) }
fn named(counter: Counter): Int { return pair(values: [mutate(counter)], first: counter.value, counter: counter) }
fn fail(counter: Counter): Int { return pair(counter.value, [mutate(counter), rejectValue()], counter) }
fn object(counter: Counter): Int {
 val snapshot = Snapshot { first: counter.value, values: [mutate(counter)] }
 counter.id += 1
 return snapshot.first * 10 + snapshot.values.sumOf { it }
}
`, `package generated
import("context";"testing";"github.com/light-speak/luxo/pkg/lux/errors")
func TestListArgumentOrder(t *testing.T){
 app:=&App{}
 for _,sample:=range []struct{name string;call func(context.Context,*Counter)(int64,error);want int64}{
  {"capture",app.capture,12},{"named",app.named,92},{"object",app.object,12},
 }{t.Run(sample.name,func(t *testing.T){
  counter:=&Counter{Value:1}
  value,err:=sample.call(t.Context(),counter)
  if err!=nil||value!=sample.want||counter.Value!=9||counter.Id!=1{t.Fatal(value,err,counter)}
 })}
 t.Run("failure",func(t *testing.T){
  counter:=&Counter{Value:1}
  _,err:=app.fail(t.Context(),counter)
  failure,ok:=err.(*errors.AppError)
  if !ok||failure.Code!=404||counter.Value!=9||counter.Id!=0{t.Fatal(err,counter)}
 })
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}, "Snapshot": {"first": 1, "values": 2}}})
}

func TestGeneratedFunctionArgumentEvaluationOrder(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id value: Int }
fn nextValue(): Result<Int> @native
fn nativePair(first: Int, second: Int): Result<Int> @native
fn pair(first: Int, second: Int): Int { return first * 10 + second }
fn namedOrder(): Int { return pair(second: nextValue()?, first: nextValue()?) }
fn nativeNamedOrder(): Int { return nativePair(second: nextValue()?, first: nextValue()?)? }
fn mutate(counter: Counter): Int { counter.value = 9 return 2 }
fn captureBeforeCall(counter: Counter): Int { return pair(counter.value, mutate(counter)) }
`, `package generated
import("context";"testing")
type orderResolver struct{calls int64}
func(r *orderResolver)NextValue(context.Context)(int64,error){r.calls++;return r.calls,nil}
func(*orderResolver)NativePair(_ context.Context,first,second int64)(int64,error){return first*10+second,nil}
func TestArgumentOrder(t *testing.T){
 t.Run("named",func(t *testing.T){r:=&orderResolver{};app:=&App{Resolver:r};value,err:=app.namedOrder(t.Context());if err!=nil||value!=21||r.calls!=2{t.Fatal(value,err,r.calls)}})
 t.Run("native",func(t *testing.T){r:=&orderResolver{};app:=&App{Resolver:r};value,err:=app.nativeNamedOrder(t.Context());if err!=nil||value!=21||r.calls!=2{t.Fatal(value,err,r.calls)}})
 t.Run("capture",func(t *testing.T){app:=&App{};counter:=&Counter{Id:1,Value:1};value,err:=app.captureBeforeCall(t.Context(),counter);if err!=nil||value!=12||counter.Value!=9{t.Fatal(value,err,counter)}})
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}})
}

func TestGeneratedRepeatedDatabaseGuards(t *testing.T) {
	runGeneratedTransactionFixture(t, `
model User @noTime { id: Int @id }
fn requireTwice(id: Int): Int {
 User.where { it.id == id }.exists() ?: throw error.NotFound
 User.where { it.id == id }.exists() ?: throw error.NotFound
 return 7
}
fn rejectTwice(id: Int): Int {
 User.where { it.id == id }.exists() !: throw error.NotFound
 User.where { it.id == id }.exists() !: throw error.NotFound
 return 8
}
fn queryAnd(enabled: Boolean, id: Int): Boolean { return enabled && User.where { it.id == id }.exists() }
fn queryOr(enabled: Boolean, id: Int): Boolean { return enabled || User.where { it.id == id }.exists() }
fn queryLeft(id: Int): Boolean { return User.where { it.id == id }.exists() && true }
fn queryEqual(id: Int): Boolean { return User.where { it.id == id }.exists() == true }
fn queryNot(id: Int): Boolean { return !User.where { it.id == id }.exists() }
fn queryIf(): Int { if User.exists() { return 1 } return 0 }
fn queryWhen(enabled: Boolean): Int {
 return when { enabled -> 1 User.exists() -> 2 else -> 3 }
}
fn queryWhenSubject(): Int { return when User.exists() { true -> 2 else -> 3 } }
fn queryWhenYield(enabled: Boolean): Int? {
 val found = for item in [7] { when { enabled -> yield item User.exists() -> yield 8 else -> null } }
 return found
}
fn rejectNegated(id: Int): Int {
 !User.where { it.id == id }.exists() ?: throw error.NotFound
 return 9
}
fn requireNegated(id: Int): Int {
 !User.where { it.id == id }.exists() !: throw error.NotFound
 return 10
}
fn requireDoubleNegated(id: Int): Int {
 !!User.where { it.id == id }.exists() ?: throw error.NotFound
 return 11
}
`, `package generated
import("errors";"testing";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres"
 "github.com/light-speak/luxo/internal/testutil")
func TestGuards(t *testing.T){
 ctx:=t.Context();container,err:=postgres.Run(ctx,testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies());testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(ctx,"sslmode=disable");if err!=nil{t.Fatal(err)};db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE users(id bigint PRIMARY KEY); INSERT INTO users VALUES (1)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db)
 for _,enabled:=range []bool{false,true}{
  want:=int64(2);if enabled{want=1}
  got,err:=app.queryWhen(ctx,enabled);if err!=nil||got!=want{t.Fatal(got,err)}
  found,err:=app.queryWhenYield(ctx,enabled);want=8;if enabled{want=7};if err!=nil||found==nil||*found!=want{t.Fatal(found,err)}
 }
 subject,err:=app.queryWhenSubject(ctx);if err!=nil||subject!=2{t.Fatal(subject,err)}
 for _,id:=range []int64{1,2}{
  want:=id==1
  got,err:=app.queryAnd(ctx,true,id);if err!=nil||got!=want{t.Fatal(got,err)}
  got,err=app.queryOr(ctx,false,id);if err!=nil||got!=want{t.Fatal(got,err)}
  got,err=app.queryLeft(ctx,id);if err!=nil||got!=want{t.Fatal(got,err)}
  got,err=app.queryEqual(ctx,id);if err!=nil||got!=want{t.Fatal(got,err)}
  got,err=app.queryNot(ctx,id);if err!=nil||got==want{t.Fatal(got,err)}
  n,err:=app.queryIf(ctx);if err!=nil||n!=1{t.Fatal(n,err)}
 }
 got,err:=app.requireNegated(ctx,1);if err!=nil||got!=10{t.Fatal(got,err)}
 got,err=app.requireDoubleNegated(ctx,1);if err!=nil||got!=11{t.Fatal(got,err)}
 if _,err:=app.requireNegated(ctx,2);!errors.Is(err,luxerrors.NotFound){t.Fatal(err)}
 if _,err:=app.requireDoubleNegated(ctx,2);!errors.Is(err,luxerrors.NotFound){t.Fatal(err)}
 got,err=app.requireTwice(ctx,1);if err!=nil||got!=7{t.Fatal(got,err)}
 got,err=app.rejectTwice(ctx,2);if err!=nil||got!=8{t.Fatal(got,err)}
 got,err=app.rejectNegated(ctx,2);if err!=nil||got!=9{t.Fatal(got,err)}
 if _,err:=app.rejectNegated(ctx,1);!errors.Is(err,luxerrors.NotFound){t.Fatal(err)}
 if _,err:=app.requireTwice(ctx,2);!errors.Is(err,luxerrors.NotFound){t.Fatal(err)}
 if _,err:=app.rejectTwice(ctx,1);!errors.Is(err,luxerrors.NotFound){t.Fatal(err)}
 if _,err:=pg.Exec(ctx,db,"DROP TABLE users");err!=nil{t.Fatal(err)}
 found,e:=app.queryWhenYield(ctx,true);if e!=nil||found==nil||*found!=7{t.Fatal("yield WHEN did not short circuit",found,e)}
 if _,err:=app.queryWhenYield(ctx,false);err==nil{t.Fatal("yield WHEN lost query error")}
 if _,err:=app.queryWhenSubject(ctx);err==nil{t.Fatal("subject WHEN lost query error")}
 n,e:=app.queryWhen(ctx,true);if e!=nil||n!=1{t.Fatal("WHEN did not short circuit",n,e)}
 if _,err:=app.queryWhen(ctx,false);err==nil{t.Fatal("WHEN lost query error")}
 b,e:=app.queryAnd(ctx,false,1);if e!=nil||b{t.Fatal("AND did not short circuit",b,e)}
 b,e=app.queryOr(ctx,true,1);if e!=nil||!b{t.Fatal("OR did not short circuit",b,e)}
 if _,err:=app.queryAnd(ctx,true,1);err==nil{t.Fatal("AND lost query error")}
 if _,err:=app.queryOr(ctx,false,1);err==nil{t.Fatal("OR lost query error")}
 if _,err:=app.queryLeft(ctx,1);err==nil{t.Fatal("left query lost error")}
 if _,err:=app.queryEqual(ctx,1);err==nil{t.Fatal("comparison lost query error")}
 if _,err:=app.queryNot(ctx,1);err==nil{t.Fatal("NOT lost query error")}
 if _,err:=app.queryIf(ctx);err==nil{t.Fatal("IF lost query error")}
 if _,err:=app.requireTwice(ctx,1);err==nil||errors.Is(err,luxerrors.NotFound){t.Fatal("query error lost",err)}
 if _,err:=app.rejectTwice(ctx,1);err==nil||errors.Is(err,luxerrors.NotFound){t.Fatal("query error lost",err)}
 if _,err:=app.rejectNegated(ctx,1);err==nil||errors.Is(err,luxerrors.NotFound){t.Fatal("query error lost",err)}
 if _,err:=app.requireNegated(ctx,1);err==nil||errors.Is(err,luxerrors.NotFound){t.Fatal("query error lost",err)}
 if _,err:=app.requireDoubleNegated(ctx,1);err==nil||errors.Is(err,luxerrors.NotFound){t.Fatal("query error lost",err)}
}
`)
}

func TestGeneratedIfPreservesStringLiterals(t *testing.T) {
	runGeneratedFixture(t, `
fn exactText(value: String): Int {
 if value == " null " { return 1 }
 if value == "null value" { return 2 }
 if value == "value null" { return 3 }
 return 0
}
fn isMissing(value: String?): Boolean {
 if value == null { return true }
 return false
}
`, `package generated
import "testing"

func TestConditions(t *testing.T){
 app:=&App{}
 for _,tc:=range []struct{text string;want int64}{{" null ",1},{"null value",2},{"value null",3},{" nil ",0},{"nil value",0},{"value nil",0}}{
  got,err:=app.exactText(t.Context(),tc.text);if err!=nil||got!=tc.want{t.Fatal(tc,got,err)}
 }
 got,err:=app.isMissing(t.Context(),nil);if err!=nil||!got{t.Fatal(got,err)}
 value:="null";got,err=app.isMissing(t.Context(),&value);if err!=nil||got{t.Fatal(got,err)}
}
`, StableIDs{})
}

func TestGeneratedExpressionEvaluationOrder(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id value: Int }
type Pair { first: Int second: Int }
fn mutate(counter: Counter): Int { counter.value = 9 return 2 }
fn binaryOrder(counter: Counter): Int { return counter.value + mutate(counter) }
fn listOrder(counter: Counter): [Int] { return [counter.value, mutate(counter)] }
fn objectOrder(counter: Counter): Pair { return Pair { first: counter.value, second: mutate(counter) } }
fn nestedOrder(counter: Counter): [Pair] {
 return [Pair { first: counter.value, second: 0 }, Pair { first: mutate(counter), second: 0 }]
}
fn literalObjects(): [Pair] { return [Pair { first: 1, second: 2 }, Pair { first: 3, second: 4 }] }
`, `package generated
import "testing"
func TestExpressionOrder(t *testing.T){
 app:=&App{};ctx:=t.Context()
 t.Run("binary",func(t *testing.T){value,err:=app.binaryOrder(ctx,&Counter{Value:1});if err!=nil||value!=3{t.Fatal(value,err)}})
 t.Run("list",func(t *testing.T){value,err:=app.listOrder(ctx,&Counter{Value:1});if err!=nil||len(value)!=2||value[0]!=1||value[1]!=2{t.Fatal(value,err)}})
 t.Run("object",func(t *testing.T){value,err:=app.objectOrder(ctx,&Counter{Value:1});if err!=nil||value.First!=1||value.Second!=2{t.Fatal(value,err)}})
 t.Run("nested",func(t *testing.T){value,err:=app.nestedOrder(ctx,&Counter{Value:1});if err!=nil||len(value)!=2||value[0].First!=1||value[1].First!=2{t.Fatal(value,err)}})
 t.Run("literals",func(t *testing.T){value,err:=app.literalObjects(ctx);if err!=nil||len(value)!=2||value[0].First!=1||value[1].Second!=4{t.Fatal(value,err)}})
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}, "Pair": {"first": 1, "second": 2}}})
}

func TestGeneratedHashArgumentEvaluatedOnce(t *testing.T) {
	runGeneratedTransactionFixture(t, `
model User @noTime { id: Int @id name: String @hash }
fn password(): Result<String> @native
fn addUser(id: Int): User { return User.create(id: id, name: password()?) }
`, `package generated
import("context";"errors";"fmt";"testing";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/pg";luxocrypto "github.com/light-speak/luxo/pkg/lux/crypto";"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres"
 "github.com/light-speak/luxo/internal/testutil")
type passwordResolver struct{calls int;fail bool}
func(r *passwordResolver)Password(context.Context)(string,error){r.calls++;if r.fail{return "",fmt.Errorf("password unavailable")};return "secret",nil}
func TestHashArgumentOnce(t *testing.T){
 ctx:=t.Context();container,err:=postgres.Run(ctx,testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies());testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(ctx,"sslmode=disable");if err!=nil{t.Fatal(err)};db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE users(id bigint PRIMARY KEY,name text NOT NULL)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);r:=&passwordResolver{};app.Resolver=r
 value,err:=app.addUser(ctx,1);if err!=nil{t.Fatal(err)}
 if r.calls!=1||!luxocrypto.VerifyPassword("secret",value.Name){t.Fatal("incorrect hash evaluation",r.calls)}
 stored,err:=app.User.Find(ctx,1);if err!=nil||stored.Name!=value.Name{t.Fatal("hash not stored",err)}
 r.calls=0;r.fail=true
 if _,err:=app.addUser(ctx,2);err==nil||r.calls!=1{t.Fatal("failure not propagated",err,r.calls)}
 stored,err=app.User.Find(ctx,2);if !errors.Is(err,luxerrors.NotFound)||stored!=nil{t.Fatal("failed input persisted",stored,err)}
}
`)
}

func TestGeneratedRangeEvaluationOrder(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id value: Int }
fn endValue(counter: Counter): Int { counter.value = 9 return 2 }
fn rangeValue(counter: Counter): [Int] { return counter.value..endValue(counter) }
fn rangeLoop(counter: Counter): Int {
 var total = 0
 for i in (counter.value..endValue(counter)) { total += i }
 return total
}
fn rangeMap(counter: Counter): [Int] { return for i in (counter.value..endValue(counter)) { i } }
fn stableBound(counter: Counter): Int {
 var total = 0
 for i in 1..counter.value { counter.value = 0 total += i }
 return total
}

`, `package generated
import "testing"
func TestRangeOrder(t *testing.T){
 app:=&App{};ctx:=t.Context()
 for _,call:=range []func()([]int64,error){func()([]int64,error){return app.rangeValue(ctx,&Counter{Value:1})},func()([]int64,error){return app.rangeMap(ctx,&Counter{Value:1})}}{value,err:=call();if err!=nil||len(value)!=2||value[0]!=1||value[1]!=2{t.Fatal(value,err)}}
 value,err:=app.rangeLoop(ctx,&Counter{Value:1});if err!=nil||value!=3{t.Fatal(value,err)}
 value,err=app.stableBound(ctx,&Counter{Value:2});if err!=nil||value!=3{t.Fatal("range bound changed during loop",value,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}})
}

func TestGeneratedRangeMaximumTerminates(t *testing.T) {
	runGeneratedFixture(t, `
fn maxRange(start: Int, end: Int): Int {
 var count = 0
 for i in start..end { if i < 0 { return -1 } count += 1 }
 return count
}
fn checkedRangeValue(value: Int): Int { if value < 0 { throw error.NotFound } return value }
fn maxRangeMap(start: Int, end: Int): [Int] { return for i in start..end { checkedRangeValue(i) } }
fn maxRangeContinue(start: Int, end: Int): Int {
 var count = 0
 for i in start..end { if i < 0 { return -1 } count += 1 continue }
 return count
}
`, `package generated
import("context";"math";"strconv";"testing")

func TestRangeMaximum(t *testing.T){
 app:=&App{}
 value,err:=app.maxRangeMap(t.Context(),math.MaxInt64,math.MaxInt64)
 if err!=nil||len(value)!=1||value[0]!=math.MaxInt64{t.Fatal(value,err)}
 for _,call:=range []func(context.Context,int64,int64)(int64,error){app.maxRange,app.maxRangeContinue}{
 for _,sample:=range []struct{start,end,want int64}{{math.MaxInt64,math.MaxInt64,1},{math.MaxInt64-1,math.MaxInt64,2},{2,1,0}}{
 value,err:=call(t.Context(),sample.start,sample.end);if err!=nil||value!=sample.want{t.Errorf("%+v: %v %v",sample,value,err)}
 }}
}

// Reference reproduces the pre-fix loop, used only with non-overflowing inputs.
func previousRange(_ context.Context,start,end int64)(int64,error){
 var count int64
 for i:=int64(start);i<=end;i++{if i<0{return -1,nil};count+=1}
 return count,nil
}
var rangeBenchmarkSink int64
func BenchmarkGeneratedRangeLoop(b *testing.B){
 app:=&App{};ctx:=context.Background()
 for _,size:=range []int64{1,16,128,4096}{
  for _,candidate:=range []struct{name string;call func(context.Context,int64,int64)(int64,error)}{{"previous",previousRange},{"generated",app.maxRange}}{
   b.Run(strconv.FormatInt(size,10)+"/"+candidate.name,func(b *testing.B){
    b.ReportAllocs()
    for b.Loop(){value,err:=candidate.call(ctx,1,size);if err!=nil||value!=size{b.Fatal(value,err)};rangeBenchmarkSink=value}
   })
  }
 }
}

`, StableIDs{})
}

func TestGeneratedRangeShadowedNullableBinding(t *testing.T) {
	runGeneratedFixture(t, `
fn shadowedRangeMap(i: Int?): [Int] {
 if i != null { return for i in i..2 { i + 1 } }
 return []
}
fn shadowedListMap(i: Int?): [Int] {
 if i != null { return for i in [i, 2] { i + 1 } }
 return []
}
fn shadowedRange(i: Int?): Int {
 var total = 0
 if i != null {
  for i in i..2 { total += i }
  total += i
 }
 return total
}
`, `package generated
import "testing"

func TestShadowedRange(t *testing.T){
 app:=&App{};input:=int64(1)
 for _,call:=range []func()([]int64,error){func()([]int64,error){return app.shadowedRangeMap(t.Context(),&input)},func()([]int64,error){return app.shadowedListMap(t.Context(),&input)}}{values,err:=call();if err!=nil||len(values)!=2||values[0]!=2||values[1]!=3{t.Fatal(values,err)}}
 value,err:=app.shadowedRange(t.Context(),&input);if err!=nil||value!=4{t.Fatal(value,err)}
 value,err=app.shadowedRange(t.Context(),nil);if err!=nil||value!=0{t.Fatal(value,err)}
}
`, StableIDs{})
}

func TestGeneratedEventArgumentEvaluationOrder(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id value: Int }
event Changed(first: Int, second: Int)
fn mutateForEvent(counter: Counter): Int { counter.value = 9 return 2 }
fn publishOrder(counter: Counter): Int {
 emit Changed(first: counter.value, second: mutateForEvent(counter))
 return 0
}
`, `package generated
import("context";"testing";"github.com/light-speak/luxo/pkg/lux/event")
func TestEventOrder(t *testing.T){
 bus:=event.NewChanBus(1);defer bus.Close()
 var got ChangedEvent
 if err:=event.OnDecode(bus,"Changed",func([]byte,any)error{t.Error("unexpected wire decoding");return nil},func(_ context.Context,value ChangedEvent)error{got=value;return nil});err!=nil{t.Fatal(err)}
 app:=&App{EventBus:bus}
 if _,err:=app.publishOrder(t.Context(),&Counter{Value:1});err!=nil{t.Fatal(err)}
 bus.Close()
 if got.First!=1||got.Second!=2{t.Fatal(got)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}, EventFields: map[string]map[string]int{"Changed": {"first": 1, "second": 2}}})
}

func TestGeneratedNullableObjectFields(t *testing.T) {
	runGeneratedFixture(t, `
type OptionalValue { value: Int? }
fn buildOptional(value: Int): OptionalValue { return OptionalValue { value: value } }
fn passOptional(value: Int?): OptionalValue { return OptionalValue { value: value } }
fn nullOptional(): OptionalValue { return OptionalValue { value: null } }
`, `package generated
import "testing"

func TestNullableObject(t *testing.T){
 app:=&App{};value,err:=app.buildOptional(t.Context(),42);if err!=nil||value.Value==nil||*value.Value!=42{t.Fatal(value,err)}
 for _,input:=range []*int64{nil,value.Value}{got,err:=app.passOptional(t.Context(),input);if err!=nil||got.Value!=input{t.Fatal(got,err)}}
 got,err:=app.nullOptional(t.Context());if err!=nil||got.Value!=nil{t.Fatal(got,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"OptionalValue": {"value": 1}}})
}

func TestGeneratedNullableEventFields(t *testing.T) {
	runGeneratedFixture(t, `
event Changed(value: Int?)
fn publishNullable(value: Int): Int { emit Changed(value: value) return 0 }
`, `package generated
import("context";"testing";"github.com/light-speak/luxo/pkg/lux/event")
func TestNullableEvent(t *testing.T){bus:=event.NewChanBus(1);defer bus.Close();var got ChangedEvent
 if err:=event.OnDecode(bus,"Changed",func([]byte,any)error{return nil},func(_ context.Context,value ChangedEvent)error{got=value;return nil});err!=nil{t.Fatal(err)}
 app:=&App{EventBus:bus};if _,err:=app.publishNullable(t.Context(),42);err!=nil{t.Fatal(err)};bus.Close()
 if got.Value==nil||*got.Value!=42{t.Fatal(got)}
}
`, StableIDs{EventFields: map[string]map[string]int{"Changed": {"value": 1}}})
}

func TestGeneratedEventDefaultArguments(t *testing.T) {
	runGeneratedFixture(t, `
event Changed(value: Int = 7, optional: Int? = 9)
fn publishDefaults(): Int { emit Changed() return 0 }
`, `package generated
import("context";"testing";"github.com/light-speak/luxo/pkg/lux/event")
func TestEventDefaults(t *testing.T){bus:=event.NewChanBus(1);defer bus.Close();var got ChangedEvent
 if err:=event.OnDecode(bus,"Changed",func([]byte,any)error{return nil},func(_ context.Context,value ChangedEvent)error{got=value;return nil});err!=nil{t.Fatal(err)}
 app:=&App{EventBus:bus};if _,err:=app.publishDefaults(t.Context());err!=nil{t.Fatal(err)};bus.Close()
 if got.Value!=7||got.Optional==nil||*got.Optional!=9{t.Fatal(got)}
}
`, StableIDs{EventFields: map[string]map[string]int{"Changed": {"value": 1, "optional": 2}}})
}

func TestGeneratedListenerPayloadBindings(t *testing.T) {
	runGeneratedFixture(t, `
event Changed(value: Int)
event Named(value: Int)
event Captured(value: Int)
on Changed { emit Captured(value: it.value) }
on Named { ctx -> emit Captured(value: ctx.value) }
`, `package generated
import("context";"testing";"github.com/light-speak/luxo/pkg/lux/event")
func TestListenerBindings(t *testing.T){
 bus:=event.NewChanBus(4);defer bus.Close();received:=make(chan int64,2)
 if err:=event.OnDecode(bus,"Captured",func([]byte,any)error{return nil},func(_ context.Context,value CapturedEvent)error{received<-value.Value;return nil});err!=nil{t.Fatal(err)}
 app:=&App{EventBus:bus};if err:=RegisterEvents(bus,app);err!=nil{t.Fatal(err)}
 if err:=EmitChanged(t.Context(),bus,ChangedEvent{Value:1});err!=nil{t.Fatal(err)}
 if err:=EmitNamed(t.Context(),bus,NamedEvent{Value:2});err!=nil{t.Fatal(err)}
 first,second:=<-received,<-received;if first+second!=3||first==second{t.Fatal(first,second)}
}
`, StableIDs{EventFields: map[string]map[string]int{"Changed": {"value": 1}, "Named": {"value": 1}, "Captured": {"value": 1}}})
}

func TestGeneratedNativeListenerRequiresImplementation(t *testing.T) {
	runGeneratedFixture(t, `
event Changed(value: Int)
on Changed @native
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/event")
func TestNativeListenerRequiresImplementation(t *testing.T){
 bus:=event.NewChanBus(1);defer bus.Close()
 if err:=RegisterEvents(bus,&App{});err==nil{t.Fatal("unimplemented native listener registered as a successful no-op")}
}
`, StableIDs{EventFields: map[string]map[string]int{"Changed": {"value": 1}}})
}

func TestGeneratedNativeListenerDispatch(t *testing.T) {
	runGeneratedFixture(t, `
event Plain(value: Int)
event Changed(value: Int)
event Named(value: String)
on Plain { }
on Changed @native
on Named @native @broadcast
`, `package generated
import("context";"errors";"testing";"github.com/light-speak/luxo/pkg/lux/event")
type nativeEventBus struct {event.Bus; handlers map[string]event.Handler; queue, broadcast int}
func(b *nativeEventBus)On(name string,h event.Handler)error{b.broadcast++;b.handlers[name]=h;return nil}
func(b *nativeEventBus)OnQueue(name,group string,h event.Handler)error{b.queue++;b.handlers[name]=h;return nil}
func TestNativeEvents(t *testing.T){
 bus:=&nativeEventBus{handlers:map[string]event.Handler{}}
 if err:=RegisterEvents(bus,nil);err==nil{t.Fatal("nil app accepted")}
 sentinel:=errors.New("listener failed");var got int64;var text string
 app:=&App{EventHandlers:NativeEventHandlers{OnChanged:func(ctx context.Context,p ChangedEvent)error{got=p.Value;return sentinel}}}
 if err:=RegisterEvents(bus,app);err==nil||len(bus.handlers)!=0{t.Fatal("partial registration before validation",err,bus.handlers)}
 app.EventHandlers.OnNamed=func(ctx context.Context,p NamedEvent)error{text=p.Value;return nil}
 if err:=RegisterEvents(bus,app);err!=nil{t.Fatal(err)}
 if bus.queue!=2||bus.broadcast!=1{t.Fatal(bus.queue,bus.broadcast)}
 app.EventHandlers.OnChanged=nil
 if err:=bus.handlers["Changed"](t.Context(),ChangedEvent{Value:42});!errors.Is(err,sentinel)||got!=42{t.Fatal(err,got)}
 raw,err:=(&ChangedEvent{Value:84}).MarshalLuxo();if err!=nil{t.Fatal(err)}
 if err:=bus.handlers["Changed"](t.Context(),raw);!errors.Is(err,sentinel)||got!=84{t.Fatal(err,got)}
 if err:=bus.handlers["Named"](t.Context(),NamedEvent{Value:"received"});err!=nil||text!="received"{t.Fatal(err,text)}
 if err:=bus.handlers["Changed"](t.Context(),[]byte{255});err==nil{t.Fatal("malformed event accepted")}
}
`, StableIDs{EventFields: map[string]map[string]int{"Plain": {"value": 1}, "Changed": {"value": 1}, "Named": {"value": 1}}})
}

func TestGeneratedListenerFunctionCall(t *testing.T) {
	runGeneratedFixture(t, `
event Changed(value: Int)
event Captured(value: Int)
fn add(value: Int, extra: Int = 2): Int { return value + extra }
on Changed { emit Captured(value: add(it.value)) }
`, `package generated
import("context";"testing";"time";"github.com/light-speak/luxo/pkg/lux/event")
func TestListenerFunction(t *testing.T){
 bus:=event.NewChanBus(4);defer bus.Close();received:=make(chan int64,1)
 if err:=event.OnDecode(bus,"Captured",func([]byte,any)error{return nil},func(_ context.Context,value CapturedEvent)error{received<-value.Value;return nil});err!=nil{t.Fatal(err)}
 app:=&App{EventBus:bus};if err:=RegisterEvents(bus,app);err!=nil{t.Fatal(err)}
 if err:=EmitChanged(t.Context(),bus,ChangedEvent{Value:3});err!=nil{t.Fatal(err)}
 select {case got:=<-received:if got!=5{t.Fatal(got)};case <-time.After(5*time.Second):t.Fatal("listener failed to publish")}
}
`, StableIDs{EventFields: map[string]map[string]int{"Changed": {"value": 1}, "Captured": {"value": 1}}})
}

func TestGeneratedListenerFunctionErrors(t *testing.T) {
	runGeneratedFixture(t, `
event Changed(value: Int)
event Captured(value: Int)
fn add(value: Int, extra: Int = 2): Int { return value + extra }
fn checkNative(value: Int): Result<Int> @native
on Changed { emit Captured(value: checkNative(add(it.value))?) }
`, `package generated
import("context";"errors";"testing";"github.com/light-speak/luxo/pkg/lux/event")
type listenerBus struct{event.Bus;handler event.Handler;got int64;emitted int}
func(b *listenerBus)OnQueue(_ string,_ string,h event.Handler)error{b.handler=h;return nil}
func(b *listenerBus)Emit(_ context.Context,_ string,payload any)error{b.got=payload.(*CapturedEvent).Value;b.emitted++;return nil}
type listenerResolver struct{failure error;calls int}
func(r *listenerResolver)CheckNative(_ context.Context,value int64)(int64,error){r.calls++;return value,r.failure}
func TestListenerErrors(t *testing.T){
 bus:=&listenerBus{};resolver:=&listenerResolver{};app:=&App{EventBus:bus,Resolver:resolver}
 if err:=RegisterEvents(bus,app);err!=nil{t.Fatal(err)}
 raw,err:=(&ChangedEvent{Value:3}).MarshalLuxo();if err!=nil{t.Fatal(err)}
 for _,payload:=range []any{ChangedEvent{Value:3},raw}{
  resolver.failure=nil;before:=bus.emitted
  if err:=bus.handler(t.Context(),payload);err!=nil{t.Fatal(err)}
  if bus.got!=5||bus.emitted!=before+1{t.Fatal(bus.got,bus.emitted)}
  failure:=errors.New("native failure");resolver.failure=failure
  if err:=bus.handler(t.Context(),payload);!errors.Is(err,failure){t.Fatal(err)}
  if bus.emitted!=before+1{t.Fatal("emitted after failure")}
 }
 if resolver.calls!=4{t.Fatal(resolver.calls)}
}
`, StableIDs{EventFields: map[string]map[string]int{"Changed": {"value": 1}, "Captured": {"value": 1}}})
}

func TestGeneratedListenerControlFlow(t *testing.T) {
	runGeneratedFixture(t, `
event Changed(value: Int)
on Changed {
 if it.value != 0 { throw error.NotFound }
}
`, `package generated
import("errors";"testing";"github.com/light-speak/luxo/pkg/lux/event";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")
type controlBus struct{event.Bus;handler event.Handler}
func(b *controlBus)OnQueue(_ string,_ string,h event.Handler)error{b.handler=h;return nil}
func TestControlFlow(t *testing.T){
 bus:=&controlBus{};if err:=RegisterEvents(bus,&App{});err!=nil{t.Fatal(err)}
 if err:=bus.handler(t.Context(),ChangedEvent{Value:0});err!=nil{t.Fatal(err)}
 if err:=bus.handler(t.Context(),ChangedEvent{Value:1});!errors.Is(err,luxerrors.NotFound){t.Fatal(err)}
}
`, StableIDs{EventFields: map[string]map[string]int{"Changed": {"value": 1}}})
}

func TestGeneratedListenerStaticExpressions(t *testing.T) {
	runGeneratedFixture(t, `
event Changed(value: Int, text: String)
event Captured(text: String)
on Changed {
 val clean = it.text.trim()
 emit Captured(text: "${it.value}|${clean}|${clean.contains("yes")}|${clean.reversed()}|${"123".toInt()?}")
}
`, `package generated
import("context";"testing";"github.com/light-speak/luxo/pkg/lux/event")
type expressionBus struct{event.Bus;handler event.Handler;got string}
func(b *expressionBus)OnQueue(_ string,_ string,h event.Handler)error{b.handler=h;return nil}
func(b *expressionBus)Emit(_ context.Context,_ string,payload any)error{b.got=payload.(*CapturedEvent).Text;return nil}
func TestExpressions(t *testing.T){
 bus:=&expressionBus{};if err:=RegisterEvents(bus,&App{EventBus:bus});err!=nil{t.Fatal(err)}
 if err:=bus.handler(t.Context(),ChangedEvent{Value:42,Text:" yes "});err!=nil{t.Fatal(err)}
 if bus.got!="42|yes|true|sey|123"{t.Fatal(bus.got)}
}
`, StableIDs{EventFields: map[string]map[string]int{"Changed": {"value": 1, "text": 2}, "Captured": {"text": 1}}})
}

func TestGeneratedEmptyObjectAndEventLists(t *testing.T) {
	runGeneratedFixture(t, `
type Snapshot { values: [Int] marker: Int }
event Captured(values: [Int], marker: Int)
fn marker(): Int { return 7 }
fn emptySnapshot(): Snapshot { return Snapshot { values: [], marker: marker() } }
fn publishEmpty(): Int { emit Captured(values: [], marker: marker()) return 0 }
`, `package generated
import("context";"testing";"github.com/light-speak/luxo/pkg/lux/event")
type emptyListBus struct{event.Bus;called bool;values []int64;marker int64}
func(b *emptyListBus)Emit(_ context.Context,_ string,payload any)error{b.called=true;b.values=payload.(*CapturedEvent).Values;b.marker=payload.(*CapturedEvent).Marker;return nil}
func TestEmptyLists(t *testing.T){
 bus:=&emptyListBus{};app:=&App{EventBus:bus}
 snapshot,err:=app.emptySnapshot(t.Context());if err!=nil||snapshot.Values==nil||len(snapshot.Values)!=0||snapshot.Marker!=7{t.Fatal(snapshot,err)}
 if _,err:=app.publishEmpty(t.Context());err!=nil||!bus.called||bus.values==nil||len(bus.values)!=0||bus.marker!=7{t.Fatal(err,bus.called,bus.values)}
}
`, StableIDs{EventFields: map[string]map[string]int{"Captured": {"values": 1, "marker": 2}}, ModelFields: map[string]map[string]int{"Snapshot": {"values": 1, "marker": 2}}})
}

func TestGeneratedModelListObjectFields(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime { id: Int @id }
type Group { users: [User] marker: Int }
fn marker(): Int { return 7 }
fn groupOf(user: User): Group { return Group { users: [user], marker: marker() } }
`, `package generated
import "testing"
func TestGroup(t *testing.T){
 app:=&App{};user:=&User{Id:42};group,err:=app.groupOf(t.Context(),user)
 if err!=nil||len(group.Users)!=1||group.Users[0].Id!=42||group.Marker!=7{t.Fatal(group,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1}, "Group": {"users": 1, "marker": 2}}})
}

func TestGeneratedModelListFieldWithoutEffects(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime { id: Int @id }
type Group { users: [User] marker: Int }
fn direct(user: User): Group { return Group { users: [user], marker: 7 } }
fn empty(): Group { return Group { users: [], marker: 7 } }
`, `package generated
import "testing"
func TestDirectGroup(t *testing.T){
 app:=&App{};group,err:=app.direct(t.Context(),&User{Id:42})
 if err!=nil||len(group.Users)!=1||group.Users[0].Id!=42||group.Marker!=7{t.Fatal(group,err)}
 empty,err:=app.empty(t.Context());if err!=nil||empty.Users==nil||len(empty.Users)!=0{t.Fatal(empty,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1}, "Group": {"users": 1, "marker": 2}}})
}

func TestGeneratedModelListFieldFunctionBoundary(t *testing.T) {
	for _, body := range []string{
		"fn identity(users: [User]): [User] { return users }\nfn fieldToFunction(group: Group): [User] { return identity(group.users) }",
		"fn singleton(user: User): [User] { return [user] }\nfn functionToField(user: User): Group { return Group { users: singleton(user) } }",
	} {
		name := "fieldToFunction"
		assertions := `func TestBoundary(t *testing.T) { app:=&App{}; group:=&Group{}; if err:=group.ReadJSONInput([]byte("{\"users\":[{\"id\":42}]}"));err!=nil{t.Fatal(err)}; value,err:=app.fieldToFunction(t.Context(),*group);if err!=nil||len(value)!=1||value[0].Id!=42{t.Fatal(value,err)} }`
		if strings.Contains(body, "functionToField") {
			name = "functionToField"
			assertions = `func TestBoundary(t *testing.T) { app:=&App{}; group,err:=app.functionToField(t.Context(),&User{Id:42});if err!=nil||len(group.Users)!=1||group.Users[0].Id!=42{t.Fatal(group,err)} }`
		}
		t.Run(name, func(t *testing.T) {
			runGeneratedFixture(t, "model User @noTime { id: Int @id }\ntype Group { users: [User] }\n"+body,
				"package generated\nimport \"testing\"\n"+assertions,
				StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1}, "Group": {"users": 1}}})
		})
	}
}

func TestGeneratedModelListRelationLayout(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime { id: Int @id posts: [Post] }
model Post @noTime { id: Int @id userId: Int comments: [Comment] val commentCount: Int get @count(comments) }
model Comment @noTime { id: Int @id postId: Int }
`, `package generated
import "testing"
func TestRelationLayout(t *testing.T){
 post:=&Post{Id:42,UserId:1};user:=User{Id:1,Posts:[]*Post{post}}
 if len(user.Posts)!=1||user.Posts[0].Id!=42{t.Fatal(user)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "posts": 2}, "Post": {"id": 1, "userId": 2, "comments": 3, "commentCount": 4}, "Comment": {"id": 1, "postId": 2}}})
}

func TestGeneratedModelListNestedWire(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime { id: Int @id }
type Group { users: [User] optional: [User]? }
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/schema")
func TestNestedModelLists(t *testing.T){
 metadata:=schema.New();if err:=RegisterSchema(metadata);err!=nil{t.Fatal(err)}
 bitmap,err:=codec.FieldMaskSet(nil,1);if err!=nil{t.Fatal(err)}
 child,err:=codec.AppendSelectionMask(nil,bitmap,nil);if err!=nil{t.Fatal(err)}
 bitmap,err=codec.FieldMaskSet(bitmap,2);if err!=nil{t.Fatal(err)}
 mask,err:=codec.AppendSelectionMask(nil,bitmap,[]codec.SelectionMaskChild{{FieldID:1,Mask:child},{FieldID:2,Mask:child}});if err!=nil{t.Fatal(err)}
 for _,raw:=range []string{`+"`"+`{"users":[],"optional":null}`+"`"+`,`+"`"+`{"users":[{"id":9223372036854775807},{"id":-9223372036854775808}],"optional":[]}`+"`"+`}{
  var expected Group;if err:=expected.ReadJSONInput([]byte(raw));err!=nil{t.Fatal(err)}
  input,err:=metadata.JSONToBinary("Group",[]byte(raw));if err!=nil{t.Fatal(err)}
  var decoded Group;dec:=codec.NewDecoder(input);decoded.ReadLuxoInput(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)}
  verifyModelLists(t,decoded,expected)
  buffer:=api.GetBuf();if err:=decoded.WriteLuxo(buffer,mask);err!=nil{api.PutBuf(buffer);t.Fatal(err)}
  var row Group;dec=codec.NewDecoder(buffer.B);row.ReadLuxo(dec);if err:=dec.Finish();err!=nil{api.PutBuf(buffer);t.Fatal(err)};api.PutBuf(buffer)
  verifyModelLists(t,row,expected)
  buffer=api.GetBuf();if err:=WriteColumnarGroup(buffer,[]Group{decoded},mask);err!=nil{api.PutBuf(buffer);t.Fatal(err)}
  rows,err:=ReadColumnarGroup(buffer.B);api.PutBuf(buffer);if err!=nil||len(rows)!=1{t.Fatal(rows,err)};verifyModelLists(t,rows[0],expected)
 }
}
func TestRejectNullModelElement(t *testing.T){
 buffer:=api.GetBuf();defer api.PutBuf(buffer)
 if err:=WriteColumnarUser(buffer,[]*User{nil},nil);err==nil{t.Fatal("null model element accepted")}
}
func verifyModelLists(t *testing.T,got,want Group){
 t.Helper();for i,values:=range [][]*User{got.Users,got.Optional}{
  expected:=want.Users;if i==1{expected=want.Optional}
  if (values==nil)!=(expected==nil)||len(values)!=len(expected){t.Fatal("list presence changed")}
  for j:=range values{if values[j]==nil||values[j].Id!=expected[j].Id{t.Fatal("model value changed")}}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1}, "Group": {"users": 1, "optional": 2}}})
}

func TestGeneratedNestedSnapshotListFields(t *testing.T) {
	runGeneratedFixture(t, `
type Item { values: [Int] }
type Snapshot { items: [Item] optional: [Int]? marker: Int }
event Captured(items: [Item], optional: [Int])
fn marker(): Int { return 7 }
fn snapshotOf(): Snapshot { return Snapshot { items: [Item { values: [] }], optional: [], marker: marker() } }
fn publishSnapshot(): Int { emit Captured(items: [Item { values: [] }], optional: []) return 0 }
`, `package generated
import("context";"testing";"github.com/light-speak/luxo/pkg/lux/event")
type snapshotListBus struct{event.Bus;got CapturedEvent;called bool}
func(b *snapshotListBus)Emit(_ context.Context,_ string,payload any)error{b.got=*payload.(*CapturedEvent);b.called=true;return nil}
func TestSnapshotLists(t *testing.T){
 bus:=&snapshotListBus{};app:=&App{EventBus:bus}
 snapshot,err:=app.snapshotOf(t.Context());if err!=nil{t.Fatal(err)}
 if len(snapshot.Items)!=1||snapshot.Items[0].Values==nil||snapshot.Optional==nil||snapshot.Marker!=7{t.Fatal(snapshot)}
 if _,err:=app.publishSnapshot(t.Context());err!=nil{t.Fatal(err)}
 if !bus.called||len(bus.got.Items)!=1||bus.got.Items[0].Values==nil||bus.got.Optional==nil{t.Fatal(bus.got)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Item": {"values": 1}, "Snapshot": {"items": 1, "optional": 2, "marker": 3}}, EventFields: map[string]map[string]int{"Captured": {"items": 1, "optional": 2}}})
}

func TestGeneratedStringMethodEvaluationOrder(t *testing.T) {
	runGeneratedFixture(t, `
model TextState @noTime { id: Int @id text: String pattern: String calls: Int }
fn replaceArgument(state: TextState): String { state.text = "changed" state.pattern = "c" state.calls += 1 return "z" }
fn replaceOrdered(state: TextState): String { return state.text.replace(state.pattern, replaceArgument(state)) }
`, `package generated
import("context";"strings";"testing")
func TestStringOrder(t *testing.T){
 state:=&TextState{Text:"abc",Pattern:"a"};value,err:=(&App{}).replaceOrdered(t.Context(),state)
 if err!=nil||value!="zbc"||state.Calls!=1||state.Text!="changed"{t.Fatal(value,err,state)}
}
func staticReplaceReference(app *App,ctx context.Context,state *TextState)(string,error){
 receiver,pattern:=state.Text,state.Pattern
 replacement,err:=app.replaceArgument(ctx,state);if err!=nil{return "",err}
 return strings.ReplaceAll(receiver,pattern,replacement),nil
}
var replacementSink string
func BenchmarkStringEvaluationOrder(b *testing.B){
 for _,sample:=range []struct{name,input string}{{"short","abc"},{"1KiB",strings.Repeat("a",1024)}}{
  for _,candidate:=range []struct{name string;call func(*App,context.Context,*TextState)(string,error)}{{"reference",staticReplaceReference},{"generated",(*App).replaceOrdered}}{
   b.Run(sample.name+"/"+candidate.name,func(b *testing.B){
    app,state:=&App{},&TextState{};ctx:=b.Context();b.ReportAllocs()
    for b.Loop(){
     state.Text,state.Pattern,state.Calls=sample.input,"a",0
     value,err:=candidate.call(app,ctx,state);if err!=nil||state.Calls!=1||len(value)!=len(sample.input){b.Fatal(value,err,state)}
     replacementSink=value
    }
   })
  }
 }
}

`, StableIDs{ModelFields: map[string]map[string]int{"TextState": {"id": 1, "text": 2, "pattern": 3, "calls": 4}}})
}

func TestGeneratedNativeListenerRealNATS(t *testing.T) {
	if os.Getenv("NATS_URL") == "" {
		t.Skip("NATS_URL is required")
	}
	for _, broadcast := range []bool{false, true} {
		t.Run(strconv.FormatBool(broadcast), func(t *testing.T) {
			name := "AuditNative" + strconv.FormatInt(time.Now().UnixNano(), 10)
			source := "event EventName(value: Int)\non EventName @native"
			if broadcast {
				source += " @broadcast"
			}
			assertions := `package generated
import("context";"errors";"os";"runtime";"testing";"time";natsevent "github.com/light-speak/luxo/pkg/lux/nats/event")
func TestNativeNATS(t *testing.T){
 bus,err:=natsevent.NewNATSBus(os.Getenv("NATS_URL"));if err!=nil{t.Fatal(err)}
 defer func(){if err:=bus.Close();err!=nil{t.Error(err)}}()
 received:=make(chan int64,8)
 app:=&App{EventHandlers:NativeEventHandlers{OnEventName:func(_ context.Context,value EventNameEvent)error{
  received<-value.Value
  if value.Value == -1{return errors.New("expected native failure")}
  if value.Value == -2{runtime.Goexit()}
  return nil
 }}}
 if err:=RegisterEvents(bus,app);err!=nil{t.Fatal(err)}
 app.EventHandlers.OnEventName=nil
 if err:=bus.Emit(t.Context(),"EventName",[]byte{255});err!=nil{t.Fatal(err)}
 for _,value:=range []int64{-1,-2,42}{if err:=EmitEventName(t.Context(),bus,EventNameEvent{Value:value});err!=nil{t.Fatal(err)}}
 for _,want:=range []int64{-1,-2,42}{select{case got:=<-received:if got!=want{t.Fatal(got,want)};case <-time.After(5*time.Second):t.Fatal("native delivery did not resume")}}
 if err:=bus.Close();err!=nil{t.Fatal(err)}
 select{case extra:=<-received:t.Fatal("malformed event reached native callback",extra);default:}
}
`
			runGeneratedFixture(t, strings.ReplaceAll(source, "EventName", name), strings.ReplaceAll(assertions, "EventName", name), StableIDs{EventFields: map[string]map[string]int{name: {"value": 1}}})
		})
	}
}

func TestGeneratedModelListNativeBoundary(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime { id: Int @id payload: Bytes }
type Group { users: [User] }
fn nativeUsers(users: [User]): Result<[User]> @native
fn throughNative(group: Group): Group { return Group { users: nativeUsers(group.users)? } }
api echoUsers(users: [User]): [User] @native
api compiledUsers(users: [User]): [User] { return nativeUsers(users)? }
api watchUsers(group: Group): Int @stream @native
api watchList(users: [User]): [User] @stream @native
api watchOptionalList(users: [User]?): [User]? @stream @native
`, `package generated
import("testing";"context";"bytes";"net/http/httptest";"time";"nhooyr.io/websocket";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
type modelListResolver struct{}
func(modelListResolver)HandleWatchOptionalList(_ context.Context,params *WatchOptionalListStreamParams,_ any,stream *api.TypedStream[[]*User])error{return stream.Send(params.Users)}
func(modelListResolver)HandleWatchList(_ context.Context,params *WatchListStreamParams,_ any,stream *api.TypedStream[[]*User])error{return stream.Send(params.Users)}
func(modelListResolver)HandleWatchUsers(_ context.Context,params *WatchUsersStreamParams,_ any,stream *api.TypedStream[int64])error{return stream.Send(int64(len(params.Group.Users)))}
func(modelListResolver)NativeUsers(_ context.Context,users []*User)([]*User,error){return users,nil}
func(modelListResolver)EchoUsers(_ context.Context,users []*User)([]*User,error){return users,nil}
func TestNativeBoundary(t *testing.T){
 app:=&App{Resolver:modelListResolver{}}
 for _,users:=range [][]*User{{},{&User{Id:42}}}{
  result,err:=app.throughNative(t.Context(),Group{Users:users})
  if err!=nil||len(result.Users)!=len(users)||(len(users)>0&&result.Users[0]!=users[0]){t.Fatal(result,err)}
 }
}
func TestNativeListRequests(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{Resolver:modelListResolver{}});err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 bitmap,err:=codec.FieldMaskSet(nil,1);if err!=nil{t.Fatal(err)}
 mask,err:=codec.AppendSelectionMask(nil,bitmap,nil);if err!=nil{t.Fatal(err)}
 for _,name:=range []string{"echoUsers","compiledUsers"}{for _,empty:=range []bool{false,true}{for _,binary:=range []bool{false,true}{
  input:="[{\"id\":9223372036854775807}]";if empty{input="[]"}
  body:=[]byte("{\"$api\":\""+name+"\",\"$select\":\"id\",\"users\":"+input+"}")
  if binary{
   messages:=api.BinaryMessages{}
   if !empty{value,err:=router.Schema.JSONToBinary("User",[]byte("{\"id\":9223372036854775807}"));if err!=nil{t.Fatal(err)};messages=append(messages,api.BinaryMessage(value))}
   body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,mask,map[string]any{"users":messages},router.Registry.ParamOrder(name));if err!=nil{t.Fatal(err)}
  }
  req:=httptest.NewRequest("POST","/",bytes.NewReader(body));req.Header.Set("Content-Type","application/json")
  if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,req)
  if response.Code!=200{t.Fatalf("%s binary=%v empty=%v: %d %s",name,binary,empty,response.Code,response.Body.String())}
  if binary{
   values,err:=ReadColumnarUser(response.Body.Bytes());if err!=nil{t.Fatal(err)}
   if empty{if len(values)!=0{t.Fatal(values)}}else if len(values)!=1||values[0].Id!=9223372036854775807{t.Fatal(values)}
  }else if response.Body.String()!="{\"data\":"+input+"}"{t.Fatal(response.Body.String())}
 }}}
}

func TestNativeStreamModelOwnership(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterStreams(router,nil,modelListResolver{});err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 input,err:=router.Schema.JSONToBinary("Group",[]byte("{\"users\":[{\"id\":42,\"payload\":\"AQID\"}]}"));if err!=nil{t.Fatal(err)}
 body,err:=api.EncodeBinaryRequest(router.Schema.APIs["watchUsers"].ID,nil,map[string]any{"group":api.BinaryMessage(input)},router.Registry.ParamOrder("watchUsers"));if err!=nil{t.Fatal(err)}
 req,err:=router.Registry.ParseBinaryRequest(body);if err!=nil{t.Fatal(err)}
 var snapshot WatchUsersStreamParams;if err:=snapshot.readRequest(req);err!=nil{t.Fatal(err)}
 clear(body);clear(input)
 if len(snapshot.Group.Users)!=1||snapshot.Group.Users[0].Id!=42||!bytes.Equal(snapshot.Group.Users[0].Payload,[]byte{1,2,3}){t.Fatal("subscription retained borrowed input",snapshot.Group)}
}

func TestNativeModelListStream(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterStreams(router,nil,modelListResolver{});err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 server:=httptest.NewServer(router);defer server.Close()
 bitmap,err:=codec.FieldMaskSet(nil,1);if err!=nil{t.Fatal(err)}
 mask,err:=codec.AppendSelectionMask(nil,bitmap,nil);if err!=nil{t.Fatal(err)}
 for _,name:=range []string{"watchList","watchOptionalList"}{for _,binary:=range []bool{false,true}{for _,shape:=range []string{"value","empty","null"}{if name=="watchList"&&shape=="null"{continue};func(){
  empty:=shape=="empty";isNull:=shape=="null"
  ctx,cancel:=context.WithTimeout(t.Context(),5*time.Second);defer cancel()
  conn,_,err:=websocket.Dial(ctx,"ws"+server.URL[4:],nil);if err!=nil{t.Fatal(err)};defer conn.CloseNow()
  input:="[{\"id\":9223372036854775807}]";if empty{input="[]"};if isNull{input="null"}
  kind:=websocket.MessageText;frame:=[]byte("{\"$sub\":\""+name+"\",\"$id\":1,\"$select\":\"id\",\"users\":"+input+"}")
  if binary{
   messages:=api.BinaryMessages{}
   if !empty&&!isNull{value,err:=router.Schema.JSONToBinary("User",[]byte("{\"id\":9223372036854775807}"));if err!=nil{t.Fatal(err)};messages=append(messages,api.BinaryMessage(value))}
   params:=map[string]any{"users":messages};if isNull{params["users"]=nil}
   body,err:=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,mask,params,router.Registry.ParamOrder(name));if err!=nil{t.Fatal(err)}
   kind=websocket.MessageBinary;frame=append([]byte{api.BinaryFrameSubscribe,1},body...)
  }
  if err:=conn.Write(ctx,kind,frame);err!=nil{t.Fatal(err)}
  _,ack,err:=conn.Read(ctx);if err!=nil{t.Fatal(err)}
  if binary{if !bytes.Equal(ack,[]byte{api.BinaryFrameSubscribeSuccess,1}){t.Fatalf("ack %x",ack)}}else if !bytes.Contains(ack,[]byte("\"ok\":true")){t.Fatalf("ack %s",ack)}
  _,data,err:=conn.Read(ctx);if err!=nil{t.Fatal(err)}
  if binary{
   if len(data)<2||data[0]!=api.BinaryFrameStream||data[1]!=1{t.Fatalf("frame %x",data)}
   payload:=data[2:]
   if name=="watchOptionalList"{if len(payload)==0{t.Fatal("missing nullable marker")};if isNull{if !bytes.Equal(payload,[]byte{0}){t.Fatalf("null %x",payload)}}else if payload[0]!=1{t.Fatalf("present %x",payload)};payload=payload[1:]}
   if !isNull{values,err:=ReadColumnarUser(payload);if err!=nil{t.Fatal(err)};if empty{if len(values)!=0{t.Fatal(values)}}else if len(values)!=1||values[0].Id!=9223372036854775807{t.Fatal(values)}}
  }else if !bytes.Contains(data,[]byte("\"data\":"+input)){t.Fatalf("data %s",data)}
  _,end,err:=conn.Read(ctx);if err!=nil{t.Fatal(err)}
  want:=[]byte("{\"$end\":1,\"ok\":true}");if binary{want=[]byte{api.BinaryFrameStreamComplete,1}}
  if !bytes.Equal(end,want){t.Fatalf("end %q",end)}
 }()}}}
}

`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "payload": 2}, "Group": {"users": 1}}})
}

func TestGeneratedModelListDefaults(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime { id: Int @id }
fn defaultUsers(users: [User]): [User] { return users }
fn defaultOptionalUsers(users: [User]? = null): [User]? { return users }
fn emptyUsers(): [User] { return defaultUsers([]) }
fn missingOptionalUsers(): [User]? { return defaultOptionalUsers() }
`, `package generated
import "testing"
func TestDefaults(t *testing.T){
 app:=&App{}
 values,err:=app.emptyUsers(t.Context());if err!=nil||values==nil||len(values)!=0{t.Fatal(values,err)}
 optional,err:=app.missingOptionalUsers(t.Context());if err!=nil||optional!=nil{t.Fatal(optional,err)}
 for _,input:=range [][]*User{{},{&User{Id:42}}}{out,err:=app.defaultOptionalUsers(t.Context(),input);if err!=nil||out==nil||len(out)!=len(input){t.Fatal(out,err)};if len(input)>0&&out[0]!=input[0]{t.Fatal("model reference changed")}}
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1}}})
}

func TestGeneratedNativeModelScalarBoundary(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime { id: Int @id }
fn nativeUser(): Result<User> @native
fn forwardUser(): User { return nativeUser()? }
fn assignedUser(): User { val user = nativeUser()? return user }
fn nativeUserID(): Int { return (nativeUser()?).id }
`, `package generated
import("testing";"context";"errors")
type nativeUserResolver struct{fail bool}
var nativeFailure=errors.New("expected native failure")
func(r nativeUserResolver)NativeUser(context.Context)(User,error){if r.fail{return User{Id:42},nativeFailure};return User{Id:42},nil}
func TestNativeScalar(t *testing.T){
 for _,fail:=range []bool{false,true}{
  app:=&App{Resolver:nativeUserResolver{fail:fail}}
  for _,call:=range []func(context.Context)(*User,error){app.forwardUser,app.assignedUser}{
   value,err:=call(t.Context());if fail{if !errors.Is(err,nativeFailure)||value!=nil{t.Fatal(value,err)}}else if err!=nil||value==nil||value.Id!=42{t.Fatal(value,err)}
  }
  id,err:=app.nativeUserID(t.Context());if fail{if !errors.Is(err,nativeFailure)||id!=0{t.Fatal(id,err)}}else if err!=nil||id!=42{t.Fatal(id,err)}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1}}})
}

func TestGeneratedNativeModelArgumentBoundary(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime { id: Int @id }
fn inspectUser(user: User): Result<Int> @native
fn inspectOptional(user: User? = null): Result<Int> @native
fn mutateUser(user: User?): Result<Int> @native
fn inspectAfter(user: User, after: Int): Result<Int> @native
fn ordered(user: User): Int { return inspectAfter(user, mutateUser(user)?)? }
fn reordered(user: User): Int { return inspectAfter(after: mutateUser(user)?, user: user)? }
fn passUser(user: User): Int { return inspectUser(user)? }
fn passConstructed(): Int { return inspectUser(User { id: 42 })? }
fn passOptional(user: User?): Int { return inspectOptional(user)? }
fn passMissing(): Int { return inspectOptional()? }
api apiNative(user: User): Int { return inspectUser(user)? }
api apiCompiled(user: User): Int { return passUser(user) }
api apiNullable(user: User?): Int { return passOptional(user) }
`, `package generated
import("testing";"context";"bytes";"strconv";"net/http/httptest";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
type argumentResolver struct{}
func(argumentResolver)MutateUser(_ context.Context,user *User)(int64,error){user.Id=99;return 1,nil}
func(argumentResolver)InspectAfter(_ context.Context,user User,after int64)(int64,error){return user.Id,nil}
func(argumentResolver)InspectUser(_ context.Context,user User)(int64,error){id:=user.Id;user.Id=99;return id,nil}
func(argumentResolver)InspectOptional(_ context.Context,user *User)(int64,error){if user==nil{return -1,nil};return user.Id,nil}
func TestModelArguments(t *testing.T){
 app:=&App{Resolver:argumentResolver{}};user:=&User{Id:42}
 value,err:=app.passUser(t.Context(),user);if err!=nil||value!=42||user.Id!=42{t.Fatal(value,user,err)}
 value,err=app.passConstructed(t.Context());if err!=nil||value!=42{t.Fatal(value,err)}
 for _,input:=range []*User{nil,user}{want:=int64(-1);if input!=nil{want=input.Id};value,err=app.passOptional(t.Context(),input);if err!=nil||value!=want{t.Fatal(value,err)}}
 value,err=app.passMissing(t.Context());if err!=nil||value!=-1{t.Fatal(value,err)}
 user.Id=42;value,err=app.ordered(t.Context(),user);if err!=nil||value!=42||user.Id!=99{t.Fatal("positional order",value,user,err)}
 user.Id=42;value,err=app.reordered(t.Context(),user);if err!=nil||value!=99||user.Id!=99{t.Fatal("named order",value,user,err)}
}
func TestModelAPIArguments(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{Resolver:argumentResolver{}});err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,name:=range []string{"apiNative","apiCompiled","apiNullable"}{for _,binary:=range []bool{false,true}{for _,null:=range []bool{false,true}{
  input:="{\"id\":9223372036854775807}";want:=int64(9223372036854775807);if null{input="null";want=-1}
  body:=[]byte("{\"$api\":\""+name+"\",\"user\":"+input+"}")
  if binary{
   var value any
   if !null{raw,err:=router.Schema.JSONToBinary("User",[]byte(input));if err!=nil{t.Fatal(err)};value=api.BinaryMessage(raw)}
   meta:=append([]api.ParamMeta(nil),router.Registry.ParamOrder(name)...);if null{meta[0].Nullable=true}
   var err error;body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,map[string]any{"user":value},meta)
   if err!=nil{t.Fatal(err)}
  }
  request:=httptest.NewRequest("POST","/",bytes.NewReader(body));request.Header.Set("Content-Type","application/json")
  if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if null&&name!="apiNullable"{if response.Code!=400{t.Fatal(name,response.Code,response.Body.String())};continue}
  if response.Code!=200{t.Fatal(name,binary,response.Code,response.Body.String())}
  if binary{value,n:=codec.ReadSvarint(response.Body.Bytes(),0);if n!=response.Body.Len()||value!=want{t.Fatal(value,want)}}else if response.Body.String()!="{\"data\":"+strconv.FormatInt(want,10)+"}"{t.Fatal(response.Body.String())}
 }}}
}

`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1}}})
}

func TestGeneratedRangeMaterializationErrors(t *testing.T) {
	runGeneratedFixture(t, `
fn makeRange(start: Int, end: Int): [Int] { return start..end }
fn wrappedRange(start: Int, end: Int): [Int] { val values = start..end return values }
`, `package generated
import("testing";"context";"math";"slices";"errors";"github.com/light-speak/luxo/pkg/lux")

func TestRanges(t *testing.T){
 app:=&App{}
 for _,call:=range []func(context.Context,int64,int64)([]int64,error){app.makeRange,app.wrappedRange}{
  for _,sample:=range []struct{start,end int64;want []int64}{{math.MaxInt64,math.MaxInt64,[]int64{math.MaxInt64}},{-1,1,[]int64{-1,0,1}},{2,1,nil}}{
   values,err:=call(t.Context(),sample.start,sample.end);if err!=nil||!slices.Equal(values,sample.want){t.Fatal(values,err)}
  }
  values,err:=call(t.Context(),math.MinInt64,math.MaxInt64);if values!=nil||!errors.Is(err,lux.ErrRangeTooLarge){t.Fatal(values,err)}
 }
}
`, StableIDs{})
}

func TestGeneratedNullableTransformFunction(t *testing.T) {
	runGeneratedTransactionFixture(t, `
fn checked(value: String): String { if value == "" { throw error.NotFound } return value.trim() }
fn normalize(value: String, suffix: String = "!"): String { return checked(value) + suffix }
model User @noTime { id: Int @id name: String? @transform { normalize(it, suffix: "?") } }
`, `package generated
import("testing";"errors";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestNullableTransform(t *testing.T){
 mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 text:=" value "; value:=&User{Id:1,Name:&text}
 for _,write:=range []func(*api.ResponseBuf)error{
  func(out *api.ResponseBuf)error{return value.WriteLuxo(out,mask)},
  func(out *api.ResponseBuf)error{return WriteColumnarUser(out,[]*User{value},mask)},
  func(out *api.ResponseBuf)error{return WriteColumnarUserValues(out,[]User{*value},mask)},
 }{
  out:=&api.ResponseBuf{};if err:=write(out);err!=nil{t.Fatal(err)}
  if text!=" value "{t.Fatal("changed source")}
  text="";if err:=write(out);!errors.Is(err,luxerrors.NotFound){t.Fatal("lost error",err)}
  value.Name=nil;if err:=write(out);err!=nil{t.Fatal("null invoked helper",err)}
  text=" value ";value.Name=&text
 }
 out:=&api.ResponseBuf{};if err:=value.WriteLuxo(out,mask);err!=nil{t.Fatal(err)}
 dec:=codec.NewDecoder(out.B);var got User;got.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)}
 if got.Name==nil||*got.Name!="value?"{t.Fatal(got.Name)}
}
`)
}

func TestGeneratedNullableListTransformFunction(t *testing.T) {
	runGeneratedTransactionFixture(t, `
fn normalize(values: [String]): [String] { return values.map { value -> value.trim() } }
model User @noTime { id: Int @id name: [String]? @transform { normalize(it) } }
`, `package generated
import("testing";"slices";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestNullableListTransform(t *testing.T){
 mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 value:=&User{Id:1,Name:[]string{" a "," b "}}
 for index,write:=range []func(*api.ResponseBuf)error{
  func(out *api.ResponseBuf)error{return value.WriteLuxo(out,mask)},
  func(out *api.ResponseBuf)error{return WriteColumnarUser(out,[]*User{value},mask)},
  func(out *api.ResponseBuf)error{return WriteColumnarUserValues(out,[]User{*value},mask)},
 }{
  for _,null:=range []bool{false,true}{
   if null {value.Name=nil}else{value.Name=[]string{" a "," b "}}
   out:=&api.ResponseBuf{};if err:=write(out);err!=nil{t.Fatal(err)}
   var got *User
   if index==0 {got=&User{};dec:=codec.NewDecoder(out.B);got.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)}}else{
    rows,err:=ReadColumnarUser(out.B);if err!=nil||len(rows)!=1{t.Fatal(rows,err)};got=&rows[0]
   }
   if null {if got.Name!=nil{t.Fatal("null became a list",got.Name)}}else{
    if !slices.Equal(got.Name,[]string{"a","b"})||!slices.Equal(value.Name,[]string{" a "," b "}){t.Fatal(got.Name,value.Name)}
   }
  }
 }
}
`)
}

func TestGeneratedTransformHelperNameCollision(t *testing.T) {
	runGeneratedTransactionFixture(t, `
fn _luxoTransform0(value: String): String { return value.trim() }
model User @noTime { id: Int @id name: String @transform { _luxoTransform0(it) } }
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestHelperNameCollision(t *testing.T){
 value:=&User{Id:1,Name:" value "}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 out:=&api.ResponseBuf{};if err:=value.WriteLuxo(out,mask);err!=nil{t.Fatal(err)}
 dec:=codec.NewDecoder(out.B);var got User;got.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)}
 if got.Name!="value"{t.Fatal(got.Name)}
}
`)
}

func TestGeneratedRecursiveTransformFunctions(t *testing.T) {
	runGeneratedTransactionFixture(t, `
fn first(value: Int): Int {
 if value <= 0 { return 0 }
 return second(value - 1) + 1
}
fn second(value: Int): Int {
 if value <= 0 { return 0 }
 return first(value - 1) + 1
}
model User @noTime { id: Int @id name: Int @transform { first(it) + first(1) } }
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestRecursiveTransform(t *testing.T){
 value:=&User{Id:1,Name:4}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 out:=&api.ResponseBuf{};if err:=value.WriteLuxo(out,mask);err!=nil{t.Fatal(err)}
 dec:=codec.NewDecoder(out.B);var got User;got.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)}
 if got.Name!=5||value.Name!=4{t.Fatal(got.Name,value.Name)}
 for _,write:=range []func(*api.ResponseBuf)error{
  func(out *api.ResponseBuf)error{return WriteColumnarUser(out,[]*User{value},mask)},
  func(out *api.ResponseBuf)error{return WriteColumnarUserValues(out,[]User{*value},mask)},
 }{
  out:=&api.ResponseBuf{};if err:=write(out);err!=nil{t.Fatal(err)}
  rows,err:=ReadColumnarUser(out.B);if err!=nil||len(rows)!=1||rows[0].Name!=5{t.Fatal(rows,err)}
 }
}
`)
}

func TestGeneratedHiddenTransformDoesNotReachNative(t *testing.T) {
	for _, visibility := range []string{"hidden", "internal"} {
		t.Run(visibility, func(t *testing.T) {
			runGeneratedTransactionFixture(t, strings.ReplaceAll(`
fn nativeText(value: String): Result<String> @native
fn normalize(value: String): String { return nativeText(value)? }
model User @noTime { id: Int @id name: String @VISIBILITY @transform { normalize(it) } }
`, "VISIBILITY", visibility), `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestHiddenTransform(t *testing.T){
 value:=&User{Id:1,Name:"secret"}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 for index,write:=range []func(*api.ResponseBuf)error{
  func(out *api.ResponseBuf)error{return value.WriteLuxo(out,mask)},
  func(out *api.ResponseBuf)error{return WriteColumnarUser(out,[]*User{value},mask)},
  func(out *api.ResponseBuf)error{return WriteColumnarUserValues(out,[]User{*value},mask)},
 }{
  out:=&api.ResponseBuf{};if err:=write(out);err!=nil{t.Fatal(err)}
  if index==0{dec:=codec.NewDecoder(out.B);var got User;got.ReadLuxo(dec);if err:=dec.Finish();err!=nil||got.Id!=1||got.Name!=""{t.Fatal(got,err)}}else{
   rows,err:=ReadColumnarUser(out.B);if err!=nil||len(rows)!=1||rows[0].Id!=1||rows[0].Name!=""{t.Fatal(rows,err)}
  }
 }
 if value.Name!="secret"{t.Fatal("changed hidden source")}
}
`)
		})
	}
}

func TestGeneratedBeforeSaveFailureStopsBuilders(t *testing.T) {
	runGeneratedTransactionFixture(t, `
fn normalize(value: String): String { if value == "bad" { throw error.NotFound } return value.trim() }
model User @noTime @crud { id: Int @id name: String @beforeSave { normalize(it) } }
api store(value: String): Int { val user = User.create(name: value) return user.id }
`, `package generated
import("testing";"errors";"encoding/json";"github.com/light-speak/luxo/pkg/lux/api";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")
func TestPreparationFailure(t *testing.T){
 app:=&App{User:&UserClient{}}
 createRequest:=&api.Request{Params:map[string]json.RawMessage{"id":[]byte("1"),"name":[]byte("\"bad\"")},Buf:&api.ResponseBuf{}}
 if err:=handleCreateUser(app)(t.Context(),createRequest);!errors.Is(err,luxerrors.NotFound){t.Fatal("CRUD error",err)}
 storeRequest:=&api.Request{Params:map[string]json.RawMessage{"value":[]byte("\"bad\"")},Buf:&api.ResponseBuf{}}
 if err:=handleStore(app)(t.Context(),storeRequest);!errors.Is(err,luxerrors.NotFound){t.Fatal("DSL error",err)}

 if got,err:=prepareSave4UserName(" good ");err!=nil||got!="good"{t.Fatal(got,err)}
 if _,err:=prepareSave4UserName("bad");!errors.Is(err,luxerrors.NotFound){t.Fatal(err)}
 create:=&UserCreateBuilder{}
 create.SetName("bad").SetName("good")
 if _,err:=create.Exec(t.Context());!errors.Is(err,luxerrors.NotFound)||len(create.Cols)!=0{t.Fatal(create.Cols,err)}
 update:=&UserUpdateBuilder{}
 update.SetName("bad").SetName("good")
 if _,err:=update.Exec(t.Context());!errors.Is(err,luxerrors.NotFound)||len(update.Sets)!=0{t.Fatal(update.Sets,err)}
 if _,err:=update.ExecWhere(t.Context());!errors.Is(err,luxerrors.NotFound){t.Fatal(err)}
 many:=&UserCreateManyBuilder{}
 many.Add(UserCreateInput{Id:1,Name:" good "}).Add(UserCreateInput{Id:2,Name:"bad"}).Add(UserCreateInput{Id:3,Name:"later"})
 if _,err:=many.Exec(t.Context());!errors.Is(err,luxerrors.NotFound){t.Fatal(err)}
 if len(many.rows)!=1||many.rows[0].Name!="good"{t.Fatal(many.rows)}
}
`)
}

func TestGeneratedNullableSaveAndTransformFunctions(t *testing.T) {
	runGeneratedTransactionFixture(t, `
fn normalize(value: String, suffix: String = "!"): String { if value == "bad" { throw error.NotFound } return value.trim() + suffix }
model User @noTime { id: Int @id name: String? @beforeSave { normalize(it) } @transform { normalize(it, suffix: "?") } }
`, `package generated
import("testing";"errors";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestNullableSaveAndTransform(t *testing.T){
 if got,err:=prepareSave4UserName(nil);err!=nil||got!=nil{t.Fatal(got,err)}
 text:=" value ";prepared,err:=prepareSave4UserName(&text)
 if err!=nil||prepared==nil||*prepared!="value!"||text!=" value "{t.Fatal(prepared,text,err)}
 value:=&User{Id:1,Name:prepared}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 out:=&api.ResponseBuf{};if err:=value.WriteLuxo(out,mask);err!=nil{t.Fatal(err)}
 dec:=codec.NewDecoder(out.B);var got User;got.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)}
 if got.Name==nil||*got.Name!="value!?"||*value.Name!="value!"{t.Fatal(got.Name,value.Name)}
 bad:="bad";create:=&UserCreateBuilder{}
 create.SetName(&bad)
 if _,err:=create.Exec(t.Context());!errors.Is(err,luxerrors.NotFound)||bad!="bad"{t.Fatal(bad,err)}
 nullCreate:=&UserCreateBuilder{};nullCreate.SetName(nil)
 if len(nullCreate.Vals)!=1||nullCreate.Vals[0].(*string)!=nil{t.Fatal(nullCreate.Vals)}
}
`)
}

func TestGeneratedFallibleSaveDatabaseBoundaries(t *testing.T) {
	runGeneratedTransactionFixture(t, `
fn normalize(value: String): String { if value == "bad" { throw error.NotFound } return value.trim() + "!" }
model User @noTime @crud { id: Int @id name: String @beforeSave { normalize(it) } }
fn createDirect(id: Int, value: String): User { return User.create(id: id, name: value) }
fn updateDirect(user: User, value: String): Int { return user.update(name: value) }
`, `package generated
import("testing";"os";"fmt";"strings";"net/http/httptest";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/pg")
func TestFallibleDatabaseBoundaries(t *testing.T){
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE users(id bigint PRIMARY KEY,name text NOT NULL)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);router:=api.NewRouter()
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 crud:=func(name string,id int64,value string)error{
  body:=fmt.Sprintf("{\"$api\":%q,\"$select\":\"id,name\",\"id\":%d,\"name\":%q}",name,id,value)
  request:=httptest.NewRequest("POST","/luvia",strings.NewReader(body));request.Header.Set("Content-Type","application/json")
  response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if response.Code!=200{return fmt.Errorf("CRUD %s: %d %s",name,response.Code,response.Body.String())};return nil
 }
 cases:=[]struct{name string;existing bool;write func(int64,string)error}{
  {"crudCreate",false,func(id int64,v string)error{return crud("createUser",id,v)}},
  {"crudUpdate",true,func(id int64,v string)error{return crud("updateUser",id,v)}},
  {"dslCreate",false,func(id int64,v string)error{_,err:=app.createDirect(ctx,id,v);return err}},
  {"dslUpdate",true,func(id int64,v string)error{_,err:=app.updateDirect(ctx,&User{Id:id},v);return err}},
  {"builderCreate",false,func(id int64,v string)error{_,err:=app.User.Create().SetId(id).SetName(v).Exec(ctx);return err}},
  {"builderUpdate",true,func(id int64,v string)error{_,err:=newUserUpdateBuilder(db,"users",id).SetName(v).Exec(ctx);return err}},
  {"batchCreate",false,func(id int64,v string)error{_,err:=app.User.CreateMany().Add(UserCreateInput{Id:id,Name:v}).Exec(ctx);return err}},
  {"queryUpdate",true,func(id int64,v string)error{_,err:=app.User.Where(UserWhere.Id.Eq(id)).Update().SetName(v).Exec(ctx);return err}},
 }
 for index,tc:=range cases {t.Run(tc.name,func(t *testing.T){
  id:=int64(index+1);if tc.existing{if _,err:=pg.Exec(ctx,db,"INSERT INTO users VALUES($1,'seed')",id);err!=nil{t.Fatal(err)}}
  if err:=tc.write(id,"bad");err==nil{t.Fatal("invalid conversion succeeded")}
  count,err:=db.QueryScalar[int64](ctx,"SELECT count(*) FROM users WHERE id=$1",id);if err!=nil{t.Fatal(err)}
  if tc.existing {got,err:=db.QueryScalar[string](ctx,"SELECT name FROM users WHERE id=$1",id);if err!=nil||got!="seed"{t.Fatal(got,err)}}else if count!=0{t.Fatal("failed create stored a row",count)}
  if err:=tc.write(id," good ");err!=nil{t.Fatal(err)}
  got,err:=db.QueryScalar[string](ctx,"SELECT name FROM users WHERE id=$1",id);if err!=nil||got!="good!"{t.Fatal(got,err)}
 })}
 _,err=app.User.CreateMany().Add(UserCreateInput{Id:100,Name:"good"}).Add(UserCreateInput{Id:101,Name:"bad"}).Exec(ctx)
 if err==nil{t.Fatal("mixed batch succeeded")}
 count,err:=db.QueryScalar[int64](ctx,"SELECT count(*) FROM users WHERE id>=100");if err!=nil||count!=0{t.Fatal("partial batch written",count,err)}
}
`)
}

func TestGeneratedStringNumericConversions(t *testing.T) {
	runGeneratedTransactionFixture(t, `
fn integer(value: String): Int { return value.toInt()? }
fn floating(value: String): Float { return value.toFloat()? }
model User @noTime { id: Int @id name: String }
`, `package generated
import("testing";"math")
func TestNumericConversions(t *testing.T) {
 app:=&App{}
 if v,err:=app.integer(t.Context(),"42");err!=nil||v!=42{t.Fatal(v,err)}
 if v,err:=app.floating(t.Context(),"-0");err!=nil||!math.Signbit(v){t.Fatal(v,err)}
 for _,s:=range []string{"bad","9223372036854775808"}{if _,err:=app.integer(t.Context(),s);err==nil{t.Fatal(s)}}
 for _,s:=range []string{"bad","NaN","+Inf","1e999"}{if _,err:=app.floating(t.Context(),s);err==nil{t.Fatal(s)}}
}
`)
}

func TestGeneratedNumericFieldConversionErrors(t *testing.T) {
	runGeneratedTransactionFixture(t, `
model User @noTime {
 id: Int @id
 name: String @beforeSave { "${it.toInt()?}" } @transform { "${it.toInt()?}" }
}
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestNumericFields(t *testing.T){
 if got,err:=prepareSave4UserName("0042");err!=nil||got!="42"{t.Fatal(got,err)}
 if _,err:=prepareSave4UserName("bad");err==nil{t.Fatal("save swallowed error")}
 value:=&User{Id:1,Name:"bad"}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 for _,write:=range []func(*api.ResponseBuf)error{
 func(out *api.ResponseBuf)error{return value.WriteLuxo(out,mask)},
 func(out *api.ResponseBuf)error{return WriteColumnarUser(out,[]*User{value},mask)},
 func(out *api.ResponseBuf)error{return WriteColumnarUserValues(out,[]User{*value},mask)},
 }{
 value.Name="bad"
 if err:=write(&api.ResponseBuf{});err==nil{t.Fatal("output swallowed error")}
 value.Name="0042";out:=&api.ResponseBuf{}
 if err:=write(out);err!=nil{t.Fatal(err)}
 if value.Name!="0042"{t.Fatal("output modified source")}
 }
 value.Name="0042";out:=&api.ResponseBuf{}
 if err:=value.WriteLuxo(out,mask);err!=nil{t.Fatal(err)}
 decoder:=codec.NewDecoder(out.B);var decoded User;decoded.ReadLuxo(decoder)
 if err:=decoder.Finish();err!=nil||decoded.Name!="42"{t.Fatal(decoded,err)}
 out=&api.ResponseBuf{}
 if err:=WriteColumnarUser(out,[]*User{value},mask);err!=nil{t.Fatal(err)}
 rows,err:=ReadColumnarUser(out.B)
 if err!=nil||len(rows)!=1||rows[0].Name!="42"{t.Fatal(rows,err)}
}
`)
}

func TestGeneratedSoftInstanceMutationCounts(t *testing.T) {
	runGeneratedFixture(t, `
model User @soft @noTime { id: Int @id name: String }
fn removeUser(user: User): Int { return user.delete() }
fn renameUser(user: User, name: String): Int { return user.update(name: name) }
fn safeRemoveUser(user: User?): Int? { return user?.delete() }
fn safeRenameUser(user: User?, name: String): Int? { return user?.update(name: name) }
`, `package generated
import("testing";"os";"time";"github.com/light-speak/luxo/pkg/lux/pg")
func TestSoftMutationCounts(t *testing.T){
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(ctx,db,"CREATE TABLE users(id bigint PRIMARY KEY,name text NOT NULL,deleted_at timestamptz); INSERT INTO users VALUES(1,'old',NULL)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);user:=&User{Id:1}
 if count,err:=app.renameUser(ctx,user,"renamed");err!=nil||count!=1{t.Fatal(count,err)}
 if name,err:=db.QueryScalar[string](ctx,"SELECT name FROM users WHERE id=1");err!=nil||name!="renamed"{t.Fatal(name,err)}
 if count,err:=app.safeRemoveUser(ctx,user);err!=nil||count==nil||*count!=1{t.Fatal(count,err)}
 stamp,err:=db.QueryScalar[time.Time](ctx,"SELECT deleted_at FROM users WHERE id=1");if err!=nil||stamp.IsZero(){t.Fatal(stamp,err)}
 if count,err:=app.removeUser(ctx,user);err!=nil||count!=0{t.Fatal("repeated delete",count,err)}
 if count,err:=app.safeRemoveUser(ctx,user);err!=nil||count==nil||*count!=0{t.Fatal("safe repeated delete",count,err)}
 if count,err:=app.renameUser(ctx,user,"bad");err!=nil||count!=0{t.Fatal("updated deleted row",count,err)}
 if count,err:=app.safeRenameUser(ctx,user,"bad");err!=nil||count==nil||*count!=0{t.Fatal("safe updated deleted row",count,err)}
 if count,err:=app.removeUser(ctx,&User{Id:2});err!=nil||count!=0{t.Fatal(count,err)}
 if count,err:=app.safeRemoveUser(ctx,nil);err!=nil||count!=nil{t.Fatal(count,err)}
 if count,err:=app.safeRenameUser(ctx,nil,"bad");err!=nil||count!=nil{t.Fatal(count,err)}
 if intact,err:=db.QueryScalar[bool](ctx,"SELECT count(*)=1 AND bool_and(name='renamed' AND deleted_at=$1) FROM users",stamp);err!=nil||!intact{t.Fatal("soft record changed or removed",intact,err)}
 if _,err:=pg.Exec(ctx,db,"DROP TABLE users");err!=nil{t.Fatal(err)}
 if count,err:=app.safeRemoveUser(ctx,user);err==nil||count!=nil{t.Fatal("database error hidden",count,err)}
 if count,err:=app.safeRenameUser(ctx,user,"bad");err==nil||count!=nil{t.Fatal("update error hidden",count,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2}}})
}

func TestGeneratedEffectfulStringFloatCompoundAssignments(t *testing.T) {
	runGeneratedFixture(t, compoundEffectSource, compoundEffectAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"Cell": {"id": 1, "text": 2, "value": 3}, "Holder": {"id": 1, "cellId": 2, "cell": 3},
	}})
}

const compoundEffectSource = `
model Cell @noTime { id: Int @id text: String value: Float }
model Holder @noTime { id: Int @id cellId: Int cell: Cell }
fn replaceText(cell: Cell, text: String): String { cell.id += 1 cell.text = text return text }
fn appendText(cell: Cell, text: String): String { cell.text += replaceText(cell, text) return cell.text }
fn appendTwice(cell: Cell): String { cell.text += replaceText(cell, "x") + replaceText(cell, "y") return cell.text }
fn localText(cell: Cell): String { var result = "old" result += replaceText(cell, "+") return result }
fn locate(holder: Holder): Cell { holder.id += 1 return holder.cell }
fn swapText(holder: Holder, other: Cell, text: String): String { holder.cell.text = "side" holder.cell = other return text }
fn appendRebound(holder: Holder, other: Cell): String { locate(holder).text += swapText(holder, other, "+") return holder.cell.text }
fn appendDirectRebound(holder: Holder, other: Cell): String { holder.cell.text += swapText(holder, other, "+") return holder.cell.text }
fn failText(holder: Holder, other: Cell): String { holder.cell.text = "side" holder.cell = other throw error.Conflict }
fn appendRejected(holder: Holder, other: Cell): String { locate(holder).text += failText(holder, other) return holder.cell.text }
fn replaceFloat(cell: Cell, value: Float): Float { cell.value = 99.0 return value }
fn addFloat(cell: Cell, value: Float): Float { cell.value += replaceFloat(cell, value) return cell.value }
fn subFloat(cell: Cell, value: Float): Float { cell.value -= replaceFloat(cell, value) return cell.value }
fn mulFloat(cell: Cell, value: Float): Float { cell.value *= replaceFloat(cell, value) return cell.value }
fn divFloat(cell: Cell, value: Float): Float { cell.value /= replaceFloat(cell, value) return cell.value }
fn localFloat(cell: Cell): Float { var result = 10.0 result += replaceFloat(cell, 3.0) return result }
fn swapFloat(holder: Holder, other: Cell): Float { holder.cell.value = 99.0 holder.cell = other return 3.0 }
fn floatRebound(holder: Holder, other: Cell): Float { locate(holder).value *= swapFloat(holder, other) return holder.cell.value }
fn floatDirectRebound(holder: Holder, other: Cell): Float { holder.cell.value *= swapFloat(holder, other) return holder.cell.value }
fn failFloat(holder: Holder, other: Cell): Float { holder.cell.value = 99.0 holder.cell = other throw error.Conflict }
fn floatRejected(holder: Holder, other: Cell): Float { locate(holder).value *= failFloat(holder, other) return holder.cell.value }
api protocolText(): String { val cell = Cell { id: 0, text: "old", value: 10.0 } return appendText(cell, "+") }
api protocolFloat(): Float { val cell = Cell { id: 0, text: "old", value: 10.0 } return addFloat(cell, 3.0) }
api protocolRebound(): String {
 val before = Cell { id: 0, text: "old", value: 10.0 }
 val other = Cell { id: 0, text: "new", value: 50.0 }
 val holder = Holder { id: 0, cellId: 0, cell: before }
 val result = appendRebound(holder, other)
 return "${before.text}|${result}|${holder.id}"
}
api protocolRejected(): String {
 val before = Cell { id: 0, text: "old", value: 10.0 }
 val other = Cell { id: 0, text: "new", value: 50.0 }
 val holder = Holder { id: 0, cellId: 0, cell: before }
 return appendRejected(holder, other)
}
`

const compoundEffectAssertions = `package generated
import("bytes";"context";"encoding/json";"fmt";"net";"net/http/httptest";"testing";
 "github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/rpc";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")
func TestStringEffects(t *testing.T){
 app:=&App{};ctx:=t.Context()
 for _,sample:=range []struct{name string;call func(*Cell)(string,error);want string;calls int64}{
  {"member",func(c *Cell)(string,error){return app.appendText(ctx,c,"+")},"old+",1},
  {"two calls",func(c *Cell)(string,error){return app.appendTwice(ctx,c)},"oldxy",2},
  {"local",func(c *Cell)(string,error){return app.localText(ctx,c)},"old+",1},
 } {t.Run(sample.name,func(t *testing.T){cell:=&Cell{Text:"old"};got,err:=sample.call(cell);if err!=nil||got!=sample.want||cell.Id!=sample.calls{t.Fatal("old string or evaluation order changed",got,cell.Id,err)}})}
}
func TestFloatEffects(t *testing.T){
 app:=&App{};ctx:=t.Context()
 for _,sample:=range []struct{name string;call func(context.Context,*Cell,float64)(float64,error);want float64}{
  {"add",app.addFloat,13},{"sub",app.subFloat,7},{"mul",app.mulFloat,30},{"div",app.divFloat,10.0/3.0},
 }{t.Run(sample.name,func(t *testing.T){cell:=&Cell{Value:10};got,err:=sample.call(ctx,cell,3);if err!=nil||got!=sample.want||cell.Value!=sample.want{t.Fatal("old float changed",got,cell.Value,err)}})}
 cell:=&Cell{Value:10};if got,err:=app.localFloat(ctx,cell);err!=nil||got!=13||cell.Value!=99{t.Fatal("local old value lost",got,cell.Value,err)}
}
func TestCompoundReboundAndFailure(t *testing.T){
 app:=&App{};ctx:=t.Context()
 old,other:=&Cell{Text:"old",Value:10},&Cell{Text:"new",Value:50};holder:=&Holder{Cell:old}
 got,err:=app.appendRebound(ctx,holder,other);if err!=nil||got!="new"||old.Text!="old+"||other.Text!="new"||holder.Cell!=other||holder.Id!=1{t.Fatal("string target rebound",got,old.Text,other.Text,holder.Id,err)}
 old.Text="old";holder.Cell=old;holder.Id=0
 got,err=app.appendDirectRebound(ctx,holder,other);if err!=nil||got!="new"||old.Text!="old+"||other.Text!="new"||holder.Cell!=other||holder.Id!=0{t.Fatal("direct string target rebound",got,old.Text,other.Text,holder.Id,err)}
 old.Text="old";holder.Cell=old;holder.Id=0
 _,err=app.appendRejected(ctx,holder,other);requireCompoundConflict(t,err)
 if old.Text!="side"||other.Text!="new"||holder.Cell!=other||holder.Id!=1{t.Fatal("failed string assignment wrote or lost effects",old.Text,other.Text,holder.Id,err)}
 holder.Cell=old;holder.Id=0
 number,err:=app.floatRebound(ctx,holder,other);if err!=nil||number!=50||old.Value!=30||other.Value!=50||holder.Cell!=other||holder.Id!=1{t.Fatal("float target rebound",number,old.Value,other.Value,holder.Id,err)}
 old.Value=10;holder.Cell=old;holder.Id=0
 number,err=app.floatDirectRebound(ctx,holder,other);if err!=nil||number!=50||old.Value!=30||other.Value!=50||holder.Cell!=other||holder.Id!=0{t.Fatal("direct float target rebound",number,old.Value,other.Value,holder.Id,err)}
 old.Value=10;holder.Cell=old;holder.Id=0
 _,err=app.floatRejected(ctx,holder,other);requireCompoundConflict(t,err)
 if old.Value!=99||other.Value!=50||holder.Cell!=other||holder.Id!=1{t.Fatal("failed float assignment wrote or lost effects",old.Value,other.Value,holder.Id,err)}
}
func requireCompoundConflict(t *testing.T,err error){
 t.Helper();failure,ok:=err.(*luxerrors.AppError)
 if !ok||failure.Code!=409||failure.Name!="Conflict"{t.Fatal("lost Conflict identity",err)}
}
func TestCompoundProtocols(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}();defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}();client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,name:=range []string{"protocolText","protocolFloat","protocolRebound","protocolRejected"}{for mode:=0;mode<3;mode++{t.Run(fmt.Sprintf("%s/mode%d",name,mode),func(t *testing.T){
  definition:=router.Schema.APIs[name];var body []byte;var err error
  if mode==2{body,err=client.CallWithMaskContext(t.Context(),"",definition.ID,nil,[]byte{0})}else{
   if mode==0{body=[]byte("{\"$api\":\""+name+"\"}")}else{body,err=api.EncodeBinaryRequest(definition.ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
   request:=httptest.NewRequest("POST","/",bytes.NewReader(body));if mode==1{request.Header.Set("X-Luxo-Mode","binary")};response:=httptest.NewRecorder();router.ServeHTTP(response,request);want:=200;if name=="protocolRejected"{want=409};if response.Code!=want{t.Fatal("status",response.Code,response.Body.String())};body=response.Body.Bytes()
  }
  if name=="protocolRejected"{if mode==2{requireCompoundConflict(t,err);return};if mode==1{failure,e:=api.DecodeBinaryError(body,409);if e!=nil||failure.Code!=409{t.Fatal("Binary lost error",e)}}else{var failure struct{Code int};if e:=json.Unmarshal(body,&failure);e!=nil||failure.Code!=409{t.Fatal("JSON lost error",e)}};return}
  if err!=nil{t.Fatal(err)}
  if mode==0{if name=="protocolFloat"{var got struct{Data float64};if e:=json.Unmarshal(body,&got);e!=nil||got.Data!=13{t.Fatal("JSON float",string(body),e)}}else{var got struct{Data string};want:="old+";if name=="protocolRebound"{want="old+|new|1"};if e:=json.Unmarshal(body,&got);e!=nil||got.Data!=want{t.Fatal("JSON string",string(body),e)}}}else{d:=codec.NewDecoder(body);if name=="protocolFloat"{if d.ReadFloat()!=13{t.Fatal("Binary float")}}else{want:="old+";if name=="protocolRebound"{want="old+|new|1"};if d.ReadString()!=want{t.Fatal("Binary string",body)}};if e:=d.Finish();e!=nil{t.Fatal(e)}}
 })}}
}
`
