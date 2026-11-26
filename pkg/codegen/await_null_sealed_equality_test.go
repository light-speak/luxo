package codegen

import "testing"

func TestGeneratedAwaitNullSealedEquality(t *testing.T) {
	runGeneratedFixture(t, `
sealed Phase { Ready(value: Int) Pending }
fn equal(value: Phase?): Boolean { return value == await { null } }
fn unequal(value: Phase?): Boolean { return await { null } != value }
`, `package generated
import "testing"
func TestNullEquality(t *testing.T) {
 app:=&App{}
 ready:=(Phase{}).NewVariant1(1)
 for _,tc:=range []struct{ value *Phase; absent bool }{{&ready,false},{nil,true}} {
  if got,err:=app.equal(t.Context(),tc.value); err!=nil||got!=tc.absent {t.Fatal(got,err)}
  if got,err:=app.unequal(t.Context(),tc.value); err!=nil||got==tc.absent {t.Fatal(got,err)}
 }
 invalid := Phase{}
 if _,err:=app.equal(t.Context(),&invalid);err==nil{t.Fatal("invalid sealed value was accepted")}
 if _,err:=app.unequal(t.Context(),&invalid);err==nil{t.Fatal("invalid sealed value was accepted")}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Phase": {"Ready": 1, "Pending": 2}, "Phase.Ready": {"value": 1}}})
}
