package codegen

import "testing"

func TestGeneratedSealedMutationCopiesValueSlots(t *testing.T) {
	runGeneratedFixture(t, `type Box<T> { value: T }
sealed Outcome { Value(value: Int, box: Box<Int>) Pending }
fn change(value: Outcome): Int {
 val before = value
 return when(value) {
  is Value -> {
   value.box.value = 9
   when(before) { is Value -> before.box.value is Pending -> 0 }
  }
  is Pending -> 0
 }
}
fn compound(value: Outcome): Int {
 val before = value
 return when(value) {
  is Value -> {
   value.box.value += 1
   when(before) { is Value -> before.box.value is Pending -> 0 }
  }
  is Pending -> 0
 }
}
fn nested(value: Outcome): Int {
 val before = value
 return when(value) {
  is Value -> {
   value.box.value = 9
   when(before) { is Value -> before.box.value is Pending -> 0 }
  }
  is Pending -> 0
 }
}
fn nullable(value: Outcome?): Int {
 return when(value) { null -> 0 is Value -> { value.box.value += 1 value.box.value } is Pending -> 0 }
}
`, sealedMutationAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"Box": {"value": 1}, "Outcome": {"Value": 1, "Pending": 2}, "Outcome.Value": {"value": 1, "box": 2},
	}})
}

const sealedMutationAssertions = `package generated
import("context";"testing")
func TestSealedMutationValueSlots(t *testing.T){
 app:=&App{};var source Outcome;if err:=source.ReadJSONInput([]byte("{\"Value\":{\"value\":7,\"box\":{\"value\":7}}}"));err!=nil{t.Fatal(err)}
 for _,call:=range []struct{name string;run func(context.Context,Outcome)(int64,error)}{{"assign",app.change},{"compound",app.compound},{"nested",app.nested}}{
  result,err:=call.run(t.Context(),source);if err!=nil||result!=7||source.AsVariant1().F1!=7||source.AsVariant1().F2.Value!=7{t.Fatal(call.name,result,source,err)}
 }
 result,err:=app.nullable(t.Context(),&source);if err!=nil||result!=8||source.AsVariant1().F2.Value!=8{t.Fatal("nullable storage alias",result,source,err)}
}
`
