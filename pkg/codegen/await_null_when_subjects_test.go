package codegen

import "testing"

func TestGeneratedSingleAwaitNullWhenSubjects(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id value: Int }
fn nonNullPattern(value: Int): Result<Int> @native
fn nullablePattern(value: Int): Result<Int?> @native
fn valueSubject(value: Int?): Int {
 return when (await { null }) { value -> 1 await { null } -> 2 else -> 3 }
}
fn statementSubject(value: Int?): Int {
 var result = 0
 when (await { null }) {
  value -> { result = 5 }
  await { null } -> { result = 7 }
  else -> { result = 9 }
 }
 return result
}
fn yieldSubject(value: Int?): Int? {
 return for item in [7] {
  when (await { null }) {
   value -> yield item
   await { null } -> yield item + 1
   else -> null
  }
 }
}
fn lazyPatterns(value: Int): Int {
 return when (await { null }) {
  nonNullPattern(4)? -> 1
  nullablePattern(value)? -> 2
  nonNullPattern(-5)? -> 3
  else -> 4
 }
}
`, `package generated
import ("context"; "errors"; "slices"; "testing")
var errAwaitPattern = errors.New("pattern failed")
type awaitSubjectResolver struct { calls []int64 }
func (r *awaitSubjectResolver) NonNullPattern(_ context.Context, value int64) (int64, error) {
 r.calls = append(r.calls, value)
 if value < 0 { return 0, errAwaitPattern }
 return value, nil
}
func (r *awaitSubjectResolver) NullablePattern(_ context.Context, value int64) (*int64, error) {
 r.calls = append(r.calls, value)
 if value == 0 { return nil, nil }
 return &value, nil
}
func TestAwaitNullSubjects(t *testing.T) {
 ctx := t.Context()
 resolver := &awaitSubjectResolver{}
 app := &App{Resolver: resolver}
 one := int64(1)
 for _, tc := range []struct { input *int64; value, statement, yielded int64 }{
  {nil, 1, 5, 7}, {&one, 2, 7, 8},
 } {
  if got, err := app.valueSubject(ctx, tc.input); got != tc.value || err != nil { t.Fatal("when value", got, err) }
  if got, err := app.statementSubject(ctx, tc.input); got != tc.statement || err != nil { t.Fatal("when statement", got, err) }
  if got, err := app.yieldSubject(ctx, tc.input); got == nil || *got != tc.yielded || err != nil { t.Fatal("when yield", got, err) }
 }
 if got,err := app.lazyPatterns(ctx,0); got != 2 || err != nil { t.Fatal("later pattern was not skipped", got, err) }
 if !slices.Equal(resolver.calls,[]int64{4,0}) { t.Fatal("lazy pattern order", resolver.calls) }
 resolver.calls = nil
 if _,err := app.lazyPatterns(ctx,1); !errors.Is(err,errAwaitPattern) { t.Fatal("lost pattern error", err) }
 if !slices.Equal(resolver.calls,[]int64{4,1,-5}) { t.Fatal("eager pattern order", resolver.calls) }
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}})
}

func TestGeneratedOrdinaryNullableWhenControl(t *testing.T) {
	runGeneratedFixture(t, `
fn ordinary(value: Int?): Int {
  return when (value) { null -> 3 else -> 4 }
}
`, `package generated
import "testing"
func TestPlainWhen(t *testing.T) {
 app:=&App{}
 input:=int64(1)
 if value, err := app.ordinary(t.Context(),nil); value!=3||err!=nil { t.Fatal(value,err) }
 if value, err := app.ordinary(t.Context(),&input); value!=4||err!=nil { t.Fatal(value,err) }
}
`, StableIDs{})
}

func TestGeneratedOrdinaryLiteralNullWhen(t *testing.T) {
	runGeneratedFixture(t, `fn ordinary(): Int { return when (null) { null -> 3 else -> 4 } }`,
		`package generated
import "testing"
func TestPlainWhen(t *testing.T) {
 if value, err := (&App{}).ordinary(t.Context()); value!=3||err!=nil { t.Fatal(value,err) }
}
`, StableIDs{})
}
