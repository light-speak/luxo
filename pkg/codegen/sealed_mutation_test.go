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

func TestGeneratedScalarDestructuringKeepsBindings(t *testing.T) {
	for _, sample := range []struct {
		name, definition string
	}{
		{"ordinary", "val (first, current) = observe(source, seen)\n" + inspectDestructuredOutcome},
		{"await scalar", "val (first, current) = await { observe(source, seen) }\n" + inspectDestructuredOutcome},
		{"await tuple", "val (first, current) = await {\n1\nobserve(source, seen)\n}\n" + inspectDestructuredOutcome},
		{"discard first", "val (_, current) = observe(source, seen)\n" + inspectDestructuredOutcome},
		{"discard await", "val (_, current) = await { observe(source, seen) }\n" + inspectDestructuredOutcome},
		{"discard all", "val (_, _) = observe(source, seen)\nreturn 7"},
		{"shadow a different owner", shadowDestructuredOutcome},
		{"mutate payload", "var (first, current) = observe(source, seen)\n" + mutateDestructuredOutcome},
		{"standalone await", scalarDestructuringInAwait},
		{"standalone await all discarded", "await { val (_, _) = observe(source, seen) }\nreturn 7"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			runGeneratedFixture(t, scalarDestructuringSource+
				"fn inspect(source: Outcome, seen: Channel<Int>): Int {\n"+sample.definition+"\n}\n", scalarDestructuringAssertions,
				StableIDs{ModelFields: map[string]map[string]int{
					"AuditBox": {"value": 1}, "Outcome": {"Value": 1, "Pending": 2}, "Outcome.Value": {"box": 1},
					"Other": {"Value": 1}, "Other.Value": {"number": 1},
				}})
		})
	}
}

const scalarDestructuringSource = `
type AuditBox { value: Int }
sealed Outcome { Value(box: AuditBox) Pending }
sealed Other { Value(number: Int) }
fn observe(source: Outcome, seen: Channel<Int>): Outcome {
 seen <- 1
 return source
}
`

const inspectDestructuredOutcome = "return when(current) { is Value -> current.box.value is Pending -> 0 }"

const shadowDestructuredOutcome = `
val current = Other.Value(number: 8)
if true {
 val (first, current) = await { observe(source, seen) }
 return when(current) { is Value -> current.box.value is Pending -> 0 }
}
return when(current) { is Value -> current.number }
`

const mutateDestructuredOutcome = `
return when(current) {
 is Value -> {
  current.box.value = 12
  when(first) { is Value -> first.box.value is Pending -> 0 }
 }
 is Pending -> 0
}
`

const scalarDestructuringInAwait = `
var result = 0
await {
 val (first, current) = observe(source, seen)
 val selected = when(current) { is Value -> current.box.value is Pending -> 0 }
 result = selected
}
return result
`

const scalarDestructuringAssertions = `package generated

import "testing"

func TestInspect(t *testing.T) {
	var value Outcome
	if err := value.ReadJSONInput([]byte("{\"Value\":{\"box\":{\"value\":7}}}")); err != nil {
		t.Fatal(err)
	}
	seen := make(chan int64, 2)
	got, err := (&App{}).inspect(t.Context(), value, seen)
	if err != nil || got != 7 || value.AsVariant1().F1.Value != 7 || len(seen) != 1 {
		t.Fatalf("result = %d, %v, original = %v, initializer calls = %d", got, err, value, len(seen))
	}
}
`
