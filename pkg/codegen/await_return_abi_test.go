package codegen

import "testing"

func TestAwaitReturnDoesNotChangeAPIABI(t *testing.T) {
	for _, sample := range []struct{ name, body string }{
		{"elvis", `val ignored = value ?: await { if early { return Node.select().all() } 42 }`},
		{"when arm", `val ignored = when { early -> await { if early { return Node.select().all() } 42 } else -> 13 }`},
		{"loop source", `val ignored = for item in (await { if early { return Node.select().all() } [42, 13] }) { item }`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			runGeneratedFixture(t, `model Node @noTime { id: Int @id name: String }
api page(early: Boolean, value: Int?): [Node] {
`+sample.body+`
return [Node { id: 1, name: "normal" }]
}
`, "package generated\n", StableIDs{ModelFields: map[string]map[string]int{"Node": {"id": 1, "name": 2}}})
		})
	}
}

func TestGeneratedAwaitRetainsReturnTarget(t *testing.T) {
	runGeneratedFixture(t, `
fn mark(value: Int): Result<Int> @native
fn fallback(early: Boolean, input: Int?, seed: Int): String {
 val number = input ?: await {
  val first = mark(seed)?
  if early { return "outer" }
  mark(first + 1)?
 }
 return "fallback ${number}"
}
fn choose(selected: Boolean, early: Boolean): String {
 val number = when {
  selected -> await {
   if early { return "outer" }
   mark(11)?
  }
  else -> mark(12)?
 }
 return "chosen ${number}"
}
fn collect(early: Boolean): String {
 val results = for number in (await {
  if early { return "outer" }
  [42, 13]
 }) { mark(number)? }
 return "collected ${results.size}"
}
fn whenBlock(early: Boolean): String {
 val number = when {
  early -> {
   val result = await {
    if early { return 7 }
    9
   }
   result
  }
  else -> 13
 }
 return "after ${number}"
}
`, `package generated
import ("context"; "errors"; "slices"; "testing")

var failedReturnWork = errors.New("work failed before the early return")
type returnResolver struct { calls []int64 }

func (r *returnResolver) Mark(_ context.Context, value int64) (int64, error) {
 r.calls = append(r.calls, value)
 if value < 0 { return 0, failedReturnWork }
 return value, nil
}

func TestReturnTarget(t *testing.T) {
 ctx := t.Context()
 resolver := &returnResolver{}
 app := &App{Resolver: resolver}
 check := func(got, want string, err error, calls ...int64) {
  t.Helper()
  if got != want || err != nil || !slices.Equal(resolver.calls, calls) {
   t.Fatalf("result=%q err=%v calls=%v, want %q %v", got, err, resolver.calls, want, calls)
  }
  resolver.calls = nil
 }
 input := int64(8)
 got,err := app.fallback(ctx, true, &input, -1)
 check(got, "fallback 8", err)
 got,err = app.fallback(ctx, true, nil, 1)
 check(got, "outer", err, 1)
 got,err = app.fallback(ctx, false, nil, 1)
 check(got, "fallback 2", err, 1, 2)
 if _,err = app.fallback(ctx, true, nil, -1); !errors.Is(err, failedReturnWork) || !slices.Equal(resolver.calls, []int64{-1}) { t.Fatal(err, resolver.calls) }
 resolver.calls = nil

 got,err = app.choose(ctx, false, true)
 check(got, "chosen 12", err, 12)
 got,err = app.choose(ctx, true, true)
 check(got, "outer", err)
 got,err = app.choose(ctx, true, false)
 check(got, "chosen 11", err, 11)

 got,err = app.collect(ctx, true)
 check(got, "outer", err)
 got,err = app.collect(ctx, false)
 check(got, "collected 2", err, 42, 13)

 got,err = app.whenBlock(ctx, true)
 check(got, "after 7", err)
 got,err = app.whenBlock(ctx, false)
 check(got, "after 13", err)
}
`, StableIDs{})
}

func TestGeneratedAwaitReturnScopeComposition(t *testing.T) {
	runGeneratedFixture(t, `
type Box { label: String }
fn safe(early: Boolean, box: Box?): Int {
 val value = (await {
  if early { return 41 }
  box
 })?.label
 if value == null { return 13 }
 return 17
}
fn formatted(early: Boolean): Int {
 val value = "text ${await {
  if early { return 41 }
  7
 }}"
 if value == "text 7" { return 17 }
 return 0
}
fn branchCondition(first: Boolean, early: Boolean): String {
 val value = when {
  first -> 9
  await {
   if early { return "outer" }
   false
  } -> 7
  else -> 13
 }
 return "condition ${value}"
}
fn nestedBranch(outerReturn: Boolean): String {
 val empty: Int? = null
 val value = when {
  outerReturn -> empty ?: await {
   if outerReturn { return "outer" }
   11
  }
  else -> {
   val local = empty ?: await {
    if !outerReturn { return 7 }
    11
   }
   local
  }
 }
 return "after ${value}"
}
fn mapScope(early: Boolean): [Int] {
 val empty: Int? = null
 return [1, 2].map {
  val local = empty ?: await {
   if early { return it + 10 }
   it + 20
  }
  local
 }
}
fn yielded(early: Boolean): String {
 val value = for number in (await {
  if early { return "outer" }
  [1, 2, 3]
 }) {
  if number > 1 { yield number }
 }
 return "yielded ${value ?: 0}"
}
fn nullableWhen(early: Boolean, present: Boolean): String {
 val absent: Int? = null
 val value = when {
  present -> await {
   if early { return "outer" }
   absent
  }
  else -> 13
 }
 return "nullable ${value ?: 17}"
}
fn selectReady(early: Boolean): String {
 val channel: Channel<Int> = Channel(1)
 channel <- 9
 val value = when {
  <-channel -> await {
   if early { return "outer" }
   7
  }
  else -> 13
 }
 return "channel ${value}"
}
`, `package generated
import ("slices"; "testing")
func TestReturnComposition(t *testing.T) {
 ctx := t.Context()
 app := &App{}
 if value,err := app.safe(ctx,true,nil); value!=41||err!=nil {t.Fatal(value,err)}
 if value,err := app.safe(ctx,false,nil); value!=13||err!=nil {t.Fatal(value,err)}
 if value,err := app.safe(ctx,false,&Box{Label:"box"}); value!=17||err!=nil {t.Fatal(value,err)}
 if value,err := app.formatted(ctx,true); value!=41||err!=nil {t.Fatal(value,err)}
 if value,err := app.formatted(ctx,false); value!=17||err!=nil {t.Fatal(value,err)}
 if value,err := app.branchCondition(ctx,true,true); value!="condition 9"||err!=nil {t.Fatal(value,err)}
 if value,err := app.branchCondition(ctx,false,true); value!="outer"||err!=nil {t.Fatal(value,err)}
 if value,err := app.branchCondition(ctx,false,false); value!="condition 13"||err!=nil {t.Fatal(value,err)}
 if value,err := app.nestedBranch(ctx,true); value!="outer"||err!=nil {t.Fatal(value,err)}
 if value,err := app.nestedBranch(ctx,false); value!="after 7"||err!=nil {t.Fatal(value,err)}
 if values,err := app.mapScope(ctx,true); !slices.Equal(values,[]int64{11,12})||err!=nil {t.Fatal(values,err)}
 if values,err := app.mapScope(ctx,false); !slices.Equal(values,[]int64{21,22})||err!=nil {t.Fatal(values,err)}
 if value,err := app.yielded(ctx,true); value!="outer"||err!=nil {t.Fatal(value,err)}
 if value,err := app.yielded(ctx,false); value!="yielded 2"||err!=nil {t.Fatal(value,err)}
 if value,err := app.nullableWhen(ctx,true,true); value!="outer"||err!=nil {t.Fatal(value,err)}
 if value,err := app.nullableWhen(ctx,false,true); value!="nullable 17"||err!=nil {t.Fatal(value,err)}
 if value,err := app.nullableWhen(ctx,true,false); value!="nullable 13"||err!=nil {t.Fatal(value,err)}
 if value,err := app.selectReady(ctx,true); value!="outer"||err!=nil {t.Fatal(value,err)}
 if value,err := app.selectReady(ctx,false); value!="channel 7"||err!=nil {t.Fatal(value,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Box": {"label": 1}}})
}

func TestGeneratedAwaitReturnInCollectionInputs(t *testing.T) {
	runGeneratedFixture(t, `
fn collections(exitAt: Int): String {
 val mapped = (await { if exitAt == 1 { return "mapped" } [3, 1, 2] }).map { it + 1 }
 val filtered = (await { if exitAt == 2 { return "filtered" } mapped }).filter { it >= 3 }
 val matched = (await { if exitAt == 3 { return "matched" } filtered }).any { it == 4 }
 val ordered = (await { if exitAt == 4 { return "ordered" } filtered }).sortBy { it }
 val total = (await { if exitAt == 5 { return "total" } ordered }).sumOf { it }
 val joined = ordered.joinToString(await { if exitAt == 6 { return "joined" } "," })
 return "${joined} ${total} ${matched}"
}
`, `package generated
import "testing"
func TestCollectionInputs(t *testing.T) {
 app := &App{}
 for code, want := range []string{"3,4 7 true", "mapped", "filtered", "matched", "ordered", "total", "joined"} {
  if got,err:=app.collections(t.Context(),int64(code)); got!=want||err!=nil {
   t.Fatalf("exit=%d result=%q error=%v, want %q",code,got,err,want)
  }
 }
}
`, StableIDs{})
}
