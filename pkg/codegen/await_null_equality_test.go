package codegen

import "testing"

func TestAwaitCompositionEdgeScalarEquality(t *testing.T) {
	runGeneratedFixture(t, `
fn observe(value: Int): Result<Int> @native
fn equalScalar(value: Int): Boolean { return await { null } == observe(value)? }
fn unequalScalar(value: Int): Boolean { return observe(value)? != await { null } }
fn equalOptional(value: Int?): Boolean { return await { null } == value }
fn unequalOptional(value: Int?): Boolean { return value != await { null } }
fn newerRight(): Boolean { var seen = 0 return await { seen = 1 null } == observe(seen)? }
fn olderLeft(): Boolean { var seen = 0 return observe(seen)? != await { seen = 1 null } }
fn failedLeft(): Boolean { return observe(-2)? == await { val later = observe(99)? null } }
`, `package generated
import ("context"; "errors"; "slices"; "testing")
var observeError = errors.New("observation failed")
type comparisonResolver struct { calls []int64 }
func (r *comparisonResolver) Observe(_ context.Context, value int64) (int64, error) {
 r.calls = append(r.calls,value)
 if value < 0 { return 0, observeError }
 return value, nil
}
func TestScalarEquality(t *testing.T) {
 r := &comparisonResolver{}
 app := &App{Resolver:r}
 if got,err := app.equalScalar(t.Context(),1); err != nil || got { t.Fatal(got,err) }
 if got,err := app.unequalScalar(t.Context(),1); err != nil || !got { t.Fatal(got,err) }
 if _,err := app.equalScalar(t.Context(),-1); !errors.Is(err,observeError) { t.Fatal(err) }
 if _,err := app.unequalScalar(t.Context(),-1); !errors.Is(err,observeError) { t.Fatal(err) }
 value := int64(7)
 for _,tc:= range []struct{value *int64; absent bool}{{nil,true},{&value,false}} {
  if got,err:=app.equalOptional(t.Context(),tc.value); err!=nil||got!=tc.absent { t.Fatal(got,err) }
  if got,err:=app.unequalOptional(t.Context(),tc.value); err!=nil||got==tc.absent { t.Fatal(got,err) }
 }
 if got,err:=app.newerRight(t.Context()); err!=nil||got {t.Fatal(got,err)}
 if got,err:=app.olderLeft(t.Context()); err!=nil||!got {t.Fatal(got,err)}
 if _,err:=app.failedLeft(t.Context()); !errors.Is(err,observeError) {t.Fatal(err)}
 if !slices.Equal(r.calls,[]int64{1,1,-1,-1,1,0,-2}) {t.Fatal("operand order or skipped evaluation",r.calls)}
}
`, StableIDs{})
}
