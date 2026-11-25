package codegen

import (
	"strings"
	"testing"
)

func TestGeneratedAwaitStaticNullTypePattern(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id }
sealed Phase { Ready(value: Int) Pending }
fn record(value: Int): Result<Int> @native
fn optional(value: Int): Result<Int?> @native
fn choose(subject: Int, condition: Int): Int {
  return when (await { val seed = record(subject)?
    null
  }) {
    is Counter -> record(-1)?
    record(7)? -> 92
    optional(condition)? -> 4
    else -> record(5)?
  }
}
fn statement(): Int {
  var result = 0
  when (await { null }) {
    is Counter -> { result = 90 }
    null -> { result = 6 }
    else -> { result = 91 }
  }
  return result
}
fn yielding(): Int? {
  return for value in [8] {
    when (await { null }) {
      is Counter -> yield 90
      null -> yield value
      else -> null
    }
  }
}
fn awaitedTyped(value: Phase?): Int {
  return when (await { value }) { is Ready -> 10 is Pending -> 11 null -> 12 }
}
fn literalTyped(): Int {
  return when (null) { is Counter -> record(-7)? null -> 18 else -> 19 }
}
fn ordinaryTyped(value: Phase?): Int {
  return when (value) { is Ready -> 10 is Pending -> 11 null -> 12 }
}
`, `package generated
import ("context"; "errors"; "slices"; "testing")
var errNullPattern = errors.New("pattern fixture failed")
type nullTypeResolver struct { calls []int64 }
func (r *nullTypeResolver) Record(_ context.Context, value int64) (int64,error) {
  r.calls=append(r.calls,value)
  if value<0 { return 0,errNullPattern }
  return value,nil
}
func (r *nullTypeResolver) Optional(_ context.Context,value int64)(*int64,error) {
  r.calls=append(r.calls,value)
  if value==0{return nil,nil}
  return &value,nil
}
func TestStaticNullTypePattern(t *testing.T) {
  ctx:=t.Context()
  r:=&nullTypeResolver{}
  app:=&App{Resolver:r}
  if _,err:=app.choose(ctx,-3,0);!errors.Is(err,errNullPattern) { t.Fatal(err) }
  if got,err:=app.choose(ctx,3,0);got!=4||err!=nil { t.Fatal(got,err) }
  if got,err:=app.choose(ctx,4,1);got!=5||err!=nil { t.Fatal(got,err) }
  if !slices.Equal(r.calls,[]int64{-3,3,7,0,4,7,1,5}) { t.Fatal("pattern order",r.calls) }
  if got,err:=app.statement(ctx);got!=6||err!=nil { t.Fatal(got,err) }
  if got,err:=app.yielding(ctx);got==nil||*got!=8||err!=nil { t.Fatal(got,err) }
  if got,err:=app.literalTyped(ctx);got!=18||err!=nil { t.Fatal(got,err) }
  if !slices.Equal(r.calls,[]int64{-3,3,7,0,4,7,1,5}) { t.Fatal("a skipped type arm ran",r.calls) }
  ready,pending:=(Phase{}).NewVariant1(1),(Phase{}).NewVariant2()
  for _,tt:=range []struct{input *Phase;value int64}{{&ready,10},{&pending,11},{nil,12}}{
    if got,err:=app.awaitedTyped(ctx,tt.input);got!=tt.value||err!=nil { t.Fatal(got,err) }
    if got,err:=app.ordinaryTyped(ctx,tt.input);got!=tt.value||err!=nil { t.Fatal(got,err) }
  }
}
`, StableIDs{ModelFields: map[string]map[string]int{
		"Counter": {"id": 1}, "Phase": {"Ready": 1, "Pending": 2}, "Phase.Ready": {"value": 1},
	}})
}

func TestStaticNullWhenRejectsUnknownPattern(t *testing.T) {
	for _, subject := range []string{"await { null }", "null"} {
		t.Run(subject, func(t *testing.T) {
			analysis := analyzePermissionFixture(t, "fn invalid(): Int { return when ("+subject+") { is Missing -> 1 else -> 2 } }")
			for _, diagnostic := range analysis.Errors {
				if strings.Contains(diagnostic.Message, "unknown type 'Missing'") {
					return
				}
			}
			t.Fatalf("missing diagnostic for unmatched invalid type: %v", analysis.Errors)
		})
	}
}
