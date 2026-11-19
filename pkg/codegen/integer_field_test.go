package codegen

import "testing"

func TestGeneratedDurationPropertyField(t *testing.T) {
	runGeneratedFixture(t, `type Timed { hours: Duration }
fn field(): Duration { val value = Timed { hours: 2h } return value.hours }
fn shadow(_integer1: Int, _integer1Err: Int): Int { return _integer1 + _integer1Err }`, `package generated
import("testing";"time")
func TestDurationField(t *testing.T){
 if got,err:=(&App{}).field(t.Context());err!=nil||got!=2*time.Hour{t.Fatal(got,err)}
 if got,err:=(&App{}).shadow(t.Context(),40,2);err!=nil||got!=42{t.Fatal("reserved temporary",got,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Timed": {"hours": 1}}})
}

func TestGeneratedCheckedIntegerTransform(t *testing.T) {
	const source = `type CheckedView { value: Int @transform { it + 1 } delay: Duration @transform { it + 1ms } }`
	runGeneratedFixture(t, source, `package generated
import("errors";"math";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")
func TestCheckedTransform(t *testing.T){
 value:=CheckedView{Value:math.MaxInt64,Delay:time.Duration(math.MaxInt64)}
 for _,field:=range []byte{1,2}{
  mask,err:=codec.AppendSelectionMask(nil,[]byte{field},nil);if err!=nil{t.Fatal(err)}
  for _,call:=range []func()error{func()error{return value.WriteLuxo(&api.ResponseBuf{},mask)},func()error{return WriteColumnarCheckedView(&api.ResponseBuf{},[]CheckedView{value},mask)}}{
   err:=call();var failure *luxerrors.AppError;if !errors.As(err,&failure)||failure.Code!=400{t.Fatal("transform must return error",err)}
  }
 }
}`, StableIDs{ModelFields: map[string]map[string]int{"CheckedView": {"value": 1, "delay": 2}}})
}

func TestGeneratedCheckedSavePreparation(t *testing.T) {
	const source = `model Store @noTime { id: Int @id value: Int @beforeSave { it += 1 } delay: Duration @beforeSave { it += 1ms } }`
	runGeneratedFixture(t, source, `package generated
import("errors";"math";"testing";"time";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")
func TestCheckedSave(t *testing.T){
 _,err:=prepareSave5StoreValue(math.MaxInt64);var failure *luxerrors.AppError;if !errors.As(err,&failure)||failure.Code!=400{t.Fatal(err)}
 if got,err:=prepareSave5StoreValue(41);err!=nil||got!=42{t.Fatal(got,err)}
 _,err=prepareSave5StoreDelay(time.Duration(math.MaxInt64));if !errors.As(err,&failure)||failure.Code!=400{t.Fatal(err)}
 if got,err:=prepareSave5StoreDelay(time.Second);err!=nil||got!=time.Second+time.Millisecond{t.Fatal(got,err)}
}`, StableIDs{ModelFields: map[string]map[string]int{"Store": {"id": 1, "value": 2, "delay": 3}}})
}
