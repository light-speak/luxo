package codegen

import (
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

var bindingGenerationControls = []struct{ name, source string }{
	{"scalar-locals", "api read(input: Int): Int { val first = input + 1 val second = first + 1 return second }"},
	{"paged-query", "model Entry @noTime { id: Int @id value: Int } api read(): [Entry] @paginate { val rows = Entry.all() return rows }"},
	{"typed-channel", "api read(input: Int): Int { val channel: Channel<Int> = Channel(1) channel <- input val result = <-channel return result }"},
}

func BenchmarkGenerateVariableBindings(b *testing.B) {
	benchmarkBindingGeneration(b, bindingGenerationControls)
}

func BenchmarkGenerateModelReferences(b *testing.B) {
	benchmarkBindingGeneration(b, []struct{ name, source string }{
		bindingGenerationControls[0],
		{"model-local", `model Entry @noTime { id: Int @id value: Int } fn compute(): Int { val item = Entry { id: 1, value: 1 } val alias = item item.value = 7 return alias.value }`},
		{"model-input", `model Entry @noTime { id: Int @id value: Int } api compute(item: Entry): Int { val alias = item item.value = 7 return alias.value }`},
		{"model-await", `model Entry @noTime { id: Int @id value: Int } fn compute(): Int { val item = await { Entry { id: 1, value: 1 } } val alias = item item.value = 7 return alias.value }`},
		{"model-page", `model Entry @noTime { id: Int @id value: Int } api compute(item: Entry): [Entry] @paginate { val alias = item return [alias] }`},
	})
}

var standaloneAwaitGenerationControls = []struct{ name, source string }{
	{"independent", "fn compute(value: Int): Int { var result = 0 await { val first = value + 1 val second = value + 2 result = first + second } return result }"},
	{"shadow", "fn compute(value: Int): Int { await { val value = value + 1 } return value }"},
	{"nullable", "fn compute(): Int { var result = 0 await { val value: Int? = 42 result = value ?: 0 } return result }"},
	{"nullable-query", "model Entry @noTime { id: Int @id value: Int } fn compute(): Int { val count: Int? = Entry.count() return count ?: 0 }"},
}

func BenchmarkGenerateStandaloneAwaitBindings(b *testing.B) {
	benchmarkBindingGeneration(b, standaloneAwaitGenerationControls)
}

var valuedAwaitGenerationControls = []struct{ name, source string }{
	{"control-scalar", bindingGenerationControls[0].source},
	{"control-standalone", standaloneAwaitGenerationControls[0].source},
	{"ordinary", "fn compute(value: Int): Int { val result = await { value + 1 } return result }"},
	{"nullable", "fn compute(value: Int): Int { val result: Int? = await { value } return result ?: 0 }"},
	{"empty-list", "fn compute(): [Int] { val result: [Int] = await { [] } return result }"},
	{"null", "fn compute(): Int? { val result: Int? = await { null } return result }"},
	{"shadow", "fn compute(value: Int): Int { var result = 0 if true { val value = await { value + 1 } result = value } return result }"},
	{"native-nullable", "fn load(value: Int): Result<Int> @native fn compute(value: Int): Int { val result: Int? = await { load(value)? } return result ?: 0 }"},
}

func BenchmarkGenerateValuedAwaitBindings(b *testing.B) {
	benchmarkBindingGeneration(b, valuedAwaitGenerationControls)
}

func benchmarkBindingGeneration(b *testing.B, samples []struct{ name, source string }) {
	for _, sample := range samples {
		b.Run(sample.name, func(b *testing.B) {
			input := analyzePermissionFixture(b, sample.source)
			if len(input.Errors) != 0 {
				b.Fatal(input.Errors)
			}
			ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Entry": {"id": 1, "value": 2}}})
			config := GeneratorConfig{Driver: DriverPG, IDs: ids}
			b.ReportAllocs()
			for b.Loop() {
				output, err := Generate(input, "generated", config)
				if err != nil || output == nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestGenerateRejectsDuplicateVariableBindings(t *testing.T) {
	for _, source := range []string{
		`fn read(): Int { val value = 1 val value = 2 return value }`,
		`fn read(value: Int): Int { val value = 2 return value }`,
		"fn read(): Int { val (value, value) = await {\n1\n2\n} return value }",
		`val value = 1 val value = 2`,
	} {
		input := analyzePermissionFixture(t, source)
		output, err := Generate(input, "generated", GeneratorConfig{})
		if err == nil || output != nil || !strings.Contains(err.Error(), "duplicate variable binding") {
			t.Fatalf("duplicate declaration emitted files: %v", err)
		}
	}
}

func TestGeneratedNestedBindingsAndDiscards(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id value: Int }
fn step(counter: Counter): Int { counter.value += 1 return counter.value }
fn discard(counter: Counter): Int { val _ = step(counter) val _ = step(counter) return counter.value }
fn discardTuple(): Int { val (_, _) = await {
  1
  2
} return 42 }
fn discardTyped(): Int { val _: Float = 0.1 val _: Channel<Int> = Channel(1) return 42 }
fn collision(_luxoDiscard1: Int): Int { val _ = 1 val _luxoDiscard2 = _luxoDiscard1 + 1 val _ = 2 return _luxoDiscard2 }
fn missing(): Int { throw error.NotFound }
fn discardFailure(): Int { val _ = missing() return 42 }
fn discardAwaitFailure(): Int { val (_, _) = await {
  missing()
  2
} return 42 }
fn guarded(value: Int): Int { if true { val value = "inner" val copy = value } return value }
fn branches(value: Int): Int {
  when { true -> { val value = "left" } else -> { val value = false } }
  return value
}

fn nested(counter: Counter): Int {
  [1, 2].forEach { value -> val copy = value counter.value += copy }
  return counter.value
}
`, `package generated
import("errors";"testing";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")
func TestNestedBindings(t *testing.T) {
 app := &App{}
 for _,call := range []func()(int64,error){func()(int64,error){return app.guarded(t.Context(),42)},func()(int64,error){return app.branches(t.Context(),42)}} {
  if got,err:=call();err!=nil||got!=42{t.Fatal("inner declaration changed its outer binding",got,err)}
 }
 counter:=&Counter{}
 if got,err:=app.discard(t.Context(),counter);err!=nil||got!=2{t.Fatal("discard lost initialization effects",got,err)}
 if got,err:=app.nested(t.Context(),counter);err!=nil||got!=5{t.Fatal("lambda binding changed evaluation",got,err)}
 for _,call:=range []func()(int64,error){func()(int64,error){return app.discardTuple(t.Context())},func()(int64,error){return app.discardTyped(t.Context())},func()(int64,error){return app.collision(t.Context(),41)}} {
  if got,err:=call();err!=nil||got!=42{t.Fatal("typed/tuple discard or generated binding collision",got,err)}
 }
 for _,call:=range []func()(int64,error){func()(int64,error){return app.discardFailure(t.Context())},func()(int64,error){return app.discardAwaitFailure(t.Context())}} {
  _,err:=call();var failure *luxerrors.AppError
  if !errors.As(err,&failure)||failure.Code!=404{t.Fatal("discard swallowed initialization failure",err)}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}})
}

func TestGenerateDiscardsPreserveSourceBindings(t *testing.T) {
	input := analyzePermissionFixture(t, "fn ignored() { val _ = 1 val (_, _) = await {\n1\n2\n} }")
	var statements []*ast.ValStmt
	ast.Walk(input.Files[0].Functions[0].Body, ast.Visitor{Stmt: func(stmt ast.Stmt) {
		if declaration, ok := stmt.(*ast.ValStmt); ok {
			statements = append(statements, declaration)
		}
	}})
	if len(statements) != 2 {
		t.Fatal("fixture lost discard declarations")
	}
	output, err := Generate(input, "generated", GeneratorConfig{})
	if err != nil || output == nil {
		t.Fatal(err)
	}
	if statements[0].Name != "_" || !slices.Equal(statements[1].Names, []string{"_", "_"}) {
		t.Fatal("lowering rewrote the source AST")
	}
}

func TestCompileDiscardAvoidsExistingFragmentBindings(t *testing.T) {
	c := newCompiler(nil)
	initial := &ast.Literal{Kind: token.Int, Value: "1"}
	initial.SetTypeTag("Int")
	c.compileStmt(&ast.ValStmt{Name: "_luxoDiscard1", Value: initial})
	c.compileStmt(&ast.ValStmt{Name: "_", Value: &ast.Literal{Kind: token.Int, Value: "2"}})
	runGeneratedProtocolPackage(t, "package generated\nfunc retained() int64 {\n"+compilerOut(c)+"\nreturn _luxoDiscard1\n}\n", `package generated
import "testing"
func TestRetained(t *testing.T) { if got:=retained();got!=1 {t.Fatal("discard changed an existing binding",got)} }
`)
}

func TestCompileQueryLocalPreservesNullableListAndTotal(t *testing.T) {
	for _, paged := range []bool{false, true} {
		c := newCompiler(makeModels("Entry"))
		c.paginate, c.paginationTotals = paged, nil
		c.compileStmt(&ast.ValStmt{
			Name: "rows", Type: &ast.TypeRef{Name: "Entry", IsList: true, Nullable: true},
			Value: &ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Entry"}, Field: "all"}},
		})
		value := c.vars["rows"]
		if !value.isList || !value.nullable || !strings.Contains(compilerOut(c), "if rows == nil") {
			t.Fatal("query binding lost nullable-list presence", compilerOut(c))
		}
		if (c.paginationTotals["rows"] != "") != paged {
			t.Fatal("query binding lost its independent pagination total")
		}
	}
}

func TestGeneratedStandaloneAwaitBindings(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id value: Int }
fn step(counter: Counter): Int { counter.value += 1 return counter.value }
fn ignored(first: Counter, second: Counter): Int {
  await { val _ = step(first) val _ = step(second) }
  return first.value + second.value
}
fn isolated(): Int { await { val value = 1 } val value = 42 return value }
fn shadow(value: Int, counter: Counter): Int {
  await { val value = value + 1 counter.value = value }
  return value
}
fn awaitCollision(_luxoDiscard1: Int, counter: Counter): Int {
  await { val _luxoDiscard2 = _luxoDiscard1 + 1 if true { val _luxoDiscard2 = "inner" } counter.value = _luxoDiscard2 }
  val _luxoDiscard3 = counter.value
  return _luxoDiscard3
}
fn emptyAwait(): Int { await {} return 42 }
fn sequentialAwait(counter: Counter): Int { await { counter.value = 42 } return counter.value }
fn nullable(counter: Counter): Int {
  await { val value: Int? = 1 counter.value = value ?: 0 }
  return counter.value
}
fn missing(): Int { throw error.NotFound }
fn ignoredFailure(): Int { await { val _ = missing() } return 42 }
fn closureFailure(): Int {
 val result = when { true -> { await { val _ = missing() } 1 } else -> 2 }
 return result
}
`, `package generated
import("errors";"testing";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")
func TestStandaloneAwait(t *testing.T) {
 app:=&App{}
 if got,err:=app.ignored(t.Context(),&Counter{},&Counter{});err!=nil||got!=2{t.Fatal("discard lost side effects",got,err)}
 if got,err:=app.isolated(t.Context());err!=nil||got!=42{t.Fatal("await binding escaped its scope",got,err)}
 counter:=&Counter{}
 if got,err:=app.shadow(t.Context(),42,counter);err!=nil||got!=42||counter.Value!=43{t.Fatal("shadow initializer lost outer value",got,counter.Value,err)}
 if got,err:=app.nullable(t.Context(),counter);err!=nil||got!=1{t.Fatal("nullable declaration lost its type",got,err)}
 if got,err:=app.awaitCollision(t.Context(),41,counter);err!=nil||got!=42{t.Fatal("internal name collision",got,err)}
 if got,err:=app.emptyAwait(t.Context());err!=nil||got!=42{t.Fatal("empty await failed",got,err)}
 if got,err:=app.sequentialAwait(t.Context(),counter);err!=nil||got!=42{t.Fatal("sequential await failed",got,err)}
 for _,call:=range []func()(int64,error){func()(int64,error){return app.ignoredFailure(t.Context())},func()(int64,error){return app.closureFailure(t.Context())}}{
  _,err:=call();var failure *luxerrors.AppError
  if !errors.As(err,&failure)||failure.Code!=404{t.Fatal("await discard swallowed failure",err)}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}})
}

func TestGeneratedStandaloneAwaitChannel(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id value: Int }
fn channel(counter: Counter): Int {
 await { val ch: Channel<Int> = Channel(1) ch <- 42 counter.value = <-ch }
 return counter.value
}
`, `package generated
import "testing"
func TestStandaloneAwaitChannel(t *testing.T) {
 if got,err:=(&App{}).channel(t.Context(),&Counter{});err!=nil||got!=42{t.Fatal(got,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}})
}

func TestGeneratedStandaloneAwaitParallelism(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id value: Int }
fn rendezvous(index: Int): Result<Int> @native
fn parallel(counter: Counter): Int {
 await { val first = rendezvous(1)? val second = rendezvous(2)? counter.value = first + second }
 return counter.value
}
fn failed(): Int {
 await { val first = rendezvous(1)? val _ = rendezvous(-1)? }
 return 42
}
`, `package generated
import("context";"errors";"testing";"time")
var awaitFailure=errors.New("await failed")
type standaloneAwaitResolver struct{ entered chan struct{};release chan struct{};stopped chan struct{} }
func(r *standaloneAwaitResolver) Rendezvous(ctx context.Context,index int64)(int64,error){
 if index<0{select{case <-r.entered:return 0,awaitFailure;case <-ctx.Done():return 0,ctx.Err()}}
 r.entered<-struct{}{}
 select{case <-r.release:return index,nil;case <-ctx.Done():close(r.stopped);return 0,ctx.Err()}
}
func TestStandaloneParallelism(t *testing.T){
 ctx,cancel:=context.WithTimeout(t.Context(),2*time.Second);defer cancel()
 resolver:=&standaloneAwaitResolver{make(chan struct{},2),make(chan struct{}),make(chan struct{})}
 app:=&App{Resolver:resolver};done:=make(chan error,1)
 go func(){value,err:=app.parallel(ctx,&Counter{});if err==nil&&value!=3{err=errors.New("incorrect result")};done<-err}()
 for range 2{select{case <-resolver.entered:case <-ctx.Done():t.Fatal("tasks were serialized",ctx.Err())}}
 close(resolver.release);if err:=<-done;err!=nil{t.Fatal(err)}
}
func TestStandaloneFailureCancellation(t *testing.T){
 ctx,cancel:=context.WithTimeout(t.Context(),2*time.Second);defer cancel()
 resolver:=&standaloneAwaitResolver{make(chan struct{},1),make(chan struct{}),make(chan struct{})}
 _,err:=(&App{Resolver:resolver}).failed(ctx)
 if !errors.Is(err,awaitFailure){t.Fatal("native error lost",err)}
 select{case <-resolver.stopped:default:t.Fatal("sibling task was not canceled before await returned")}
 if ctx.Err()!=nil{t.Fatal("canceled parent instead of sibling",ctx.Err())}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}})
}

func TestGenerateStandaloneAwaitPreservesBindingsAndFusion(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		declaration := "val projectId ="
		if nullable {
			declaration = "val projectId: Int? ="
		}
		source := `model Entry @noTime { id: Int @id projectId: Int }
fn read(projectId: Int): Int {
 await { val first = Entry.where(projectId: projectId).count() ` + declaration + ` Entry.where(projectId: 42).count() }
 return projectId
}`
		input := analyzePermissionFixture(t, source)
		var originals []ast.ValStmt
		ast.Walk(input.Files[0].Functions[0].Body, ast.Visitor{Stmt: func(statement ast.Stmt) {
			if value, ok := statement.(*ast.ValStmt); ok {
				originals = append(originals, *value)
			}
		}})
		output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: StableIDs{ModelFields: map[string]map[string]int{"Entry": {"id": 1, "projectId": 2}}}})
		if err != nil {
			t.Fatal(err)
		}
		body := string(output.Files["handler.gen.go"])
		if !strings.Contains(body, "EntryWhere.ProjectId.Eq(projectId)") {
			t.Fatal("aggregate lost the initializer's outer binding", body)
		}
		fused := strings.Contains(body, ".AggregateBatch(gctx,")
		if fused == nullable {
			t.Fatal("aggregate fusion ignored its declared result ABI", body)
		}
		index := 0
		ast.Walk(input.Files[0].Functions[0].Body, ast.Visitor{Stmt: func(statement ast.Stmt) {
			if value, ok := statement.(*ast.ValStmt); ok {
				previous := originals[index]
				if value.Name != previous.Name || value.Type != previous.Type || value.Value != previous.Value {
					t.Fatal("await rewrote source declaration")
				}
				index++
			}
		}})
		runGeneratedCompilation(t, output, "package generated\nimport \"testing\"\nfunc TestCompile(t *testing.T){}\n")
	}
}

func TestGeneratedValuedAwaitExplicitTypes(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id value: Int }
fn nullableValue(): Int { val value: Int? = await { 42 } return value ?: 0 }
fn nullableNull(): Int? { val value: Int? = await { null } return value }
fn emptyList(): [Int] { val values: [Int] = await { [] } return values }
fn nullableEmptyList(): [Int]? { val values: [Int]? = await { [] } return values }
fn ignored(): Int { val _: Int? = await { 1 } return 42 }
fn shadow(value: Int, counter: Counter): Int {
 if true { val value = await { value + 1 } counter.value = value }
 return value
}
`, `package generated
import "testing"
func TestValuedAwait(t *testing.T){
 app:=&App{}
 if value,err:=app.nullableValue(t.Context());err!=nil||value!=42{t.Fatal("nullable value lost its ABI",value,err)}
 if value,err:=app.nullableNull(t.Context());err!=nil||value!=nil{t.Fatal("typed null was lost",value,err)}
 if value,err:=app.emptyList(t.Context());err!=nil||value==nil||len(value)!=0{t.Fatal("typed empty list was lost",value,err)}
 if value,err:=app.nullableEmptyList(t.Context());err!=nil||value==nil||len(value)!=0{t.Fatal("empty list became null",value,err)}
 if value,err:=app.ignored(t.Context());err!=nil||value!=42{t.Fatal("typed discard failed",value,err)}
 counter:=&Counter{}
 if value,err:=app.shadow(t.Context(),42,counter);err!=nil||value!=42||counter.Value!=43{t.Fatal("shadow task read its destination instead of outer value",value,counter.Value,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}})
}

func TestGeneratedValuedAwaitShadow(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id value: Int }
fn scalar(value: Int, counter: Counter): Int {
 if true { val value = await { value + 1 } counter.value = value }
 return value
}
fn tuple(first: Int, second: Int, counter: Counter): Int {
 if true { val (first, second) = await {
 first + 1
 second + 2
 } counter.value = first + second }
 return first + second
}
fn reserved(req: Int, counter: Counter): Int {
 if true { val req = await { req + 1 } counter.value = req }
 return req
}
`, `package generated
import "testing"
func TestValuedAwaitShadow(t *testing.T){
 app:=&App{};counter:=&Counter{}
 if value,err:=app.scalar(t.Context(),42,counter);err!=nil||value!=42||counter.Value!=43{t.Fatal("scalar shadow changed its initializer",value,counter.Value,err)}
 if value,err:=app.tuple(t.Context(),42,40,counter);err!=nil||value!=82||counter.Value!=85{t.Fatal("tuple shadow changed its initializer",value,counter.Value,err)}
 if value,err:=app.reserved(t.Context(),42,counter);err!=nil||value!=42||counter.Value!=43{t.Fatal("reserved shadow changed its initializer",value,counter.Value,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}})
}

func TestGeneratedValuedAwaitNativeErrorsAndParallelism(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id value: Int }
fn wait(index: Int): Result<Int> @native
fn value(): Int? { val result: Int? = await { wait(0)? } return result }
fn failed(): Int { val _: Int? = await { wait(-1)? } return 42 }
fn parallel(first: Int, second: Int, counter: Counter): Int {
 if true { val (first, second) = await {
 wait(first)?
 wait(second)?
 } counter.value = first + second }
 return first + second
}
`, `package generated
import("context";"errors";"testing";"time")
var valuedAwaitFailure=errors.New("valued await failed")
type valuedAwaitResolver struct{entered chan struct{};release chan struct{}}
func(r *valuedAwaitResolver) Wait(ctx context.Context,index int64)(int64,error){
 if index<0{return 0,valuedAwaitFailure};if index==0{return 42,nil}
 r.entered<-struct{}{};select{case <-r.release:return index+10,nil;case <-ctx.Done():return 0,ctx.Err()}
}
func TestValuedAwaitNative(t *testing.T){
 resolver:=&valuedAwaitResolver{make(chan struct{},2),make(chan struct{})};app:=&App{Resolver:resolver}
 if value,err:=app.value(t.Context());err!=nil||value==nil||*value!=42{t.Fatal("native nullable result lost",value,err)}
 if _,err:=app.failed(t.Context());!errors.Is(err,valuedAwaitFailure){t.Fatal("native failure lost",err)}
 ctx,cancel:=context.WithTimeout(t.Context(),2*time.Second);defer cancel();counter:=&Counter{};done:=make(chan error,1)
 go func(){value,err:=app.parallel(ctx,1,2,counter);if err==nil&&(value!=3||counter.Value!=23){err=errors.New("incorrect shadow inputs")};done<-err}()
 for range 2{select{case <-resolver.entered:case <-ctx.Done():t.Fatal("shadowed tasks serialized",ctx.Err())}}
 close(resolver.release);if err:=<-done;err!=nil{t.Fatal(err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}})
}

func TestGeneratedValuedAwaitNullableDomainTypes(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id value: Int }
fn bytesValue(value: Bytes): Bytes? { val result: Bytes? = await { value } return result }
fn jsonValue(value: JSON): JSON? { val result: JSON? = await { value } return result }
fn channelValue(value: Channel<Int>): Channel<Int> { val result: Channel<Int> = await { value } return result }
`, `package generated
import("bytes";"encoding/json";"testing")
func TestValuedAwaitDomainTypes(t *testing.T){
 app:=&App{}
 if value,err:=app.bytesValue(t.Context(),[]byte{0,1,255});err!=nil||value==nil||!bytes.Equal(*value,[]byte{0,1,255}){t.Fatal("nullable Bytes lost",value,err)}
 raw:=json.RawMessage("{\"id\":9007199254740993}")
 if value,err:=app.jsonValue(t.Context(),raw);err!=nil||value==nil||!bytes.Equal(*value,raw){t.Fatal("raw JSON lost",value,err)}
 channel:=make(chan int64,1);channel<-42
 if value,err:=app.channelValue(t.Context(),channel);err!=nil||value!=channel||<-value!=42{t.Fatal("typed channel lost",value,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}})
}

func TestGenerateValuedAwaitSourceAndStaticTypes(t *testing.T) {
	input := analyzePermissionFixture(t, `
fn emptyValue(): [Int] { val value: [Int] = await { [] } return value }
fn nullValue(): Int? { val value: Int? = await { null } return value }
`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	type original struct {
		statement *ast.ValStmt
		name      string
		typ       *ast.TypeRef
		value     ast.Expr
	}
	var originals []original
	for _, function := range input.Files[0].Functions {
		ast.Walk(function.Body, ast.Visitor{Stmt: func(statement ast.Stmt) {
			if value, ok := statement.(*ast.ValStmt); ok {
				originals = append(originals, original{value, value.Name, value.Type, value.Value})
			}
		}})
	}
	output, err := Generate(input, "generated", GeneratorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, previous := range originals {
		if previous.statement.Name != previous.name || previous.statement.Type != previous.typ || previous.statement.Value != previous.value {
			t.Fatal("await mutated a source declaration")
		}
	}
	file, err := goparser.ParseFile(gotoken.NewFileSet(), "handler.gen.go", output.Files["handler.gen.go"], 0)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, declaration := range file.Decls {
		function, ok := declaration.(*goast.FuncDecl)
		if !ok || (function.Name.Name != "emptyValue" && function.Name.Name != "nullValue") {
			continue
		}
		checked++
		goast.Inspect(function.Body, func(node goast.Node) bool {
			switch value := node.(type) {
			case *goast.InterfaceType, *goast.MapType, *goast.TypeAssertExpr:
				t.Fatal("typed await emitted dynamic Go", function.Name.Name)
			case *goast.Ident:
				if value.Name == "any" {
					t.Fatal("typed await emitted any", function.Name.Name)
				}
			}
			return true
		})
	}
	if checked != 2 || len(originals) != 2 {
		t.Fatal("generated functions or source declarations missing", checked, len(originals))
	}
}

func TestAwaitTaskOmitsUnusedContextCapture(t *testing.T) {
	for _, sample := range []struct {
		name, body  string
		wantContext bool
	}{
		{"scalar", "\t\tvalue = 42\n", false},
		{"native", "\t\tvalue, err := app.load(ctx)\n\t\tif err != nil { return err }\n", true},
		{"literal", "\t\tvalue = \"ctx\"\n", true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			c := newCompiler(nil)
			c.emitAwaitTask(awaitTask{body: sample.body})
			if got := strings.Contains(compilerOut(c), "ctx := gctx"); got != sample.wantContext {
				t.Fatalf("unexpected task context capture: %t, want %t", got, sample.wantContext)
			}
		})
	}
}

func TestGeneratedBindingsPreserveBusinessTypeNames(t *testing.T) {
	runGeneratedFixture(t, `
model Item @noTime { id: Int @id value: Int }
type Packet { value: Int }
enum State { READY }
fn modelBinding(Item: Item): Int { val copy: Item = Item return copy.value }
fn packetBinding(Packet: Packet): Int { val copy: Packet = Packet return copy.value }
fn enumBinding(State: State): State { val copy: State = State return copy }
fn localModelBinding(row: Item): Int { val Item = row val copy: Item = Item return copy.value }
fn loopModelBinding(row: Item): Int { var total = 0 for Item in [row] { val copy: Item = Item total += copy.value } return total }
fn awaitModelBinding(row: Item): Int { val Item = await { row } val copy: Item = Item return copy.value }
fn nestedModelBinding(Item: Item): Int { var total = Item.value if true { val Item = Item { id: 1, value: 2 } total += Item.value } return total + Item.value }
api modelInput(Item: Item): Int { val copy: Item = Item return copy.value }
api packetInput(Packet: Packet): Int { val copy: Packet = Packet return copy.value }
api enumInput(State: State = State.READY): State { val copy: State = State return copy }
api nativeItem(Item: Item): Int @native
api nativePacket(Packet: Packet): Int @native
api nativeState(State: State): State @native
fn nativeService(Item: Item): Int @native @service
`, `package generated
import("bytes";"context";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api")
type businessBindingResolver struct{}
func(businessBindingResolver)NativeItem(_ context.Context, value Item)(int64,error){return value.Value,nil}
func(businessBindingResolver)NativePacket(_ context.Context, value Packet)(int64,error){return value.Value,nil}
func(businessBindingResolver)NativeState(_ context.Context, value State)(State,error){return value,nil}
func(businessBindingResolver)NativeService(_ context.Context, value Item)(int64,error){return value.Value,nil}
func TestBusinessBindingValues(t *testing.T) {
 app:=&App{Resolver:businessBindingResolver{}}
 row:=&Item{Id:1,Value:42}
 for _,call:=range []func()(int64,error){func()(int64,error){return app.modelBinding(t.Context(),row)},func()(int64,error){return app.packetBinding(t.Context(),Packet{Value:42})},func()(int64,error){return app.localModelBinding(t.Context(),row)},func()(int64,error){return app.loopModelBinding(t.Context(),row)},func()(int64,error){return app.awaitModelBinding(t.Context(),row)},func()(int64,error){return app.nestedModelBinding(t.Context(),&Item{Value:20})}} {
  if value,err:=call();err!=nil||value!=42{t.Fatal("business binding changed its value",value,err)}
 }
 if value,err:=app.enumBinding(t.Context(),StateREADY);err!=nil||value!=StateREADY{t.Fatal(value,err)}
 if _handleService_nativeService(app)==nil{t.Fatal("native service constructor missing")}
 router:=api.NewRouter()
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 for _,sample:=range []struct{body,want string}{
  {"{\"$api\":\"modelInput\",\"Item\":{\"id\":1,\"value\":42}}","\"data\":42"},
  {"{\"$api\":\"packetInput\",\"Packet\":{\"value\":42}}","\"data\":42"},
  {"{\"$api\":\"enumInput\"}","\"data\":\"READY\""},
  {"{\"$api\":\"enumInput\",\"State\":\"READY\"}","\"data\":\"READY\""},
  {"{\"$api\":\"nativeItem\",\"Item\":{\"id\":1,\"value\":42}}","\"data\":42"},
  {"{\"$api\":\"nativePacket\",\"Packet\":{\"value\":42}}","\"data\":42"},
  {"{\"$api\":\"nativeState\",\"State\":\"READY\"}","\"data\":\"READY\""},
 } {
  req:=httptest.NewRequest("POST","/",bytes.NewBufferString(sample.body));req.Header.Set("Content-Type","application/json")
  out:=httptest.NewRecorder();router.ServeHTTP(out,req)
  if out.Code!=200||!bytes.Contains(out.Body.Bytes(),[]byte(sample.want)){t.Fatal(sample.body,out.Code,out.Body.String())}
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"Item": {"id": 1, "value": 2}, "Packet": {"value": 1}}})
}

func TestParameterBindingsProtectTypeCatalog(t *testing.T) {
	generator := defaultGenerator()
	generator.modelDeclarations = map[string]*ast.ModelDecl{"Item": nil, "_luxoParam0": nil}
	generator.typeDeclarations = map[string]*ast.TypeDecl{"Packet": nil}
	params := []*ast.ParamDecl{{Name: "Item"}, {Name: "Packet"}, {Name: "State"}, {Name: "plain"}, {Name: "_luxoParam1"}}
	body := &ast.Block{Stmts: []ast.Stmt{&ast.ValStmt{Name: "Item"}}}
	names := generator.parameterBindings(params, body, nil, map[string]bool{"State": true})
	used := map[string]bool{}
	for _, source := range []string{"Item", "Packet", "State", "_luxoParam1"} {
		name := names.name(source)
		if name == source || name == "_luxoParam0" || name == "_luxoParam1" || used[name] {
			t.Fatalf("binding %s reused protected name %s", source, name)
		}
		used[name] = true
	}
	if len(names) != 4 || names.name("plain") != "plain" || params[0].Name != "Item" || body.Stmts[0].(*ast.ValStmt).Name != "Item" {
		t.Fatal("catalog binding changed source identifiers or ordinary names", names)
	}
	fallback := defaultGenerator().parameterBindings([]*ast.ParamDecl{{Name: "Fallback"}}, nil, map[string]*ast.ModelDecl{"Fallback": nil}, nil)
	if fallback.name("Fallback") == "Fallback" {
		t.Fatal("fragment model catalog was ignored")
	}
	if defaultGenerator().parameterBindings([]*ast.ParamDecl{{Name: "plain"}}, nil, nil, nil) != nil {
		t.Fatal("ordinary bindings allocated a name map")
	}
	if parameterBindings([]*ast.ParamDecl{{Name: "payload"}}, nil, "payload").name("payload") == "payload" {
		t.Fatal("explicit reserved names stopped binding")
	}
}

func BenchmarkGenerateNominalBindings(b *testing.B) {
	benchmarkBindingGeneration(b, []struct{ name, source string }{
		{"scalar", `fn compute(value: Int): Int { return value }`},
		{"model-ordinary", `model Entry @noTime { id: Int @id value: Int } fn compute(row: Entry): Int { return row.value }`},
		{"model-shadow", `model Entry @noTime { id: Int @id value: Int } fn compute(Entry: Entry): Int { val copy: Entry = Entry return copy.value }`},
		{"type-shadow", `type Entry { id: Int value: Int } fn compute(Entry: Entry): Int { val copy: Entry = Entry return copy.value }`},
		{"enum-shadow", `enum State { READY } fn compute(State: State): State { val copy: State = State return copy }`},
	})
}

func TestGeneratedEnumDefaultSymbolBindings(t *testing.T) {
	runGeneratedFixture(t, `enum Role { ADMIN USER }
fn choose(role: Role = Role.ADMIN): Role { return role }
fn parameterDefault(RoleADMIN: Int): Role { return choose() }
fn localDefault(): Role { val RoleADMIN = 7 return choose() }
api probe(RoleADMIN: Int, role: Role = Role.ADMIN): Role { return role }
api nativeRole(RoleADMIN: Int, role: Role = Role.ADMIN): Role @native
fn nativeService(RoleADMIN: Int, role: Role = Role.ADMIN): Role @native @service
`, `package generated
import("bytes";"context";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api")
type roleBindingResolver struct{}
func(roleBindingResolver)NativeRole(_ context.Context,_ int64,role Role)(Role,error){return role,nil}
func(roleBindingResolver)NativeService(_ context.Context,_ int64,role Role)(Role,error){return role,nil}
func TestEnumDefaultValues(t *testing.T){
 app:=&App{Resolver:roleBindingResolver{}}
 if value,err:=app.parameterDefault(t.Context(),7);err!=nil||value!=RoleADMIN{t.Fatal(value,err)}
 if value,err:=app.localDefault(t.Context());err!=nil||value!=RoleADMIN{t.Fatal(value,err)}
 if _handleService_nativeService(app)==nil{t.Fatal("missing native service constructor")}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 for _,sample:=range []struct{body,want string}{
  {"{\"$api\":\"probe\",\"RoleADMIN\":7}","\"data\":\"ADMIN\""},
  {"{\"$api\":\"probe\",\"RoleADMIN\":7,\"role\":\"USER\"}","\"data\":\"USER\""},
  {"{\"$api\":\"nativeRole\",\"RoleADMIN\":7}","\"data\":\"ADMIN\""},
  {"{\"$api\":\"nativeRole\",\"RoleADMIN\":7,\"role\":\"USER\"}","\"data\":\"USER\""},
 } {
  req:=httptest.NewRequest("POST","/",bytes.NewBufferString(sample.body));req.Header.Set("Content-Type","application/json")
  out:=httptest.NewRecorder();router.ServeHTTP(out,req)
  if out.Code!=200||!bytes.Contains(out.Body.Bytes(),[]byte(sample.want)){t.Fatal(sample.body,out.Code,out.Body.String())}
 }
}
`, StableIDs{})
}

func TestGenerateEnumBindingContextIsolation(t *testing.T) {
	enumInput := analyzePermissionFixture(t, `enum Role { ADMIN USER } api probe(RoleADMIN: Int, role: Role = Role.ADMIN): Role { return role }`)
	plainInput := analyzePermissionFixture(t, `api plain(RoleADMIN: Int): Int { return RoleADMIN }`)
	if len(enumInput.Errors) != 0 || len(plainInput.Errors) != 0 {
		t.Fatal(enumInput.Errors, plainInput.Errors)
	}
	ids := StableIDs{APIs: map[string]int{"probe": 1, "plain": 2}, APIParams: map[string]map[string]int{"probe": {"RoleADMIN": 1, "role": 2}, "plain": {"RoleADMIN": 1}}}
	generator := mustNewGenerator(t, GeneratorConfig{IDs: ids})
	var wg sync.WaitGroup
	failures := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			first, err := generator.Generate(enumInput, "generated", nil)
			if err != nil || first == nil {
				failures <- "enum generation failed"
				return
			}
			if strings.Contains(string(first.Files["handler.gen.go"]), `RoleADMIN, err :=`) {
				failures <- "enum constant was shadowed"
				return
			}
			next, err := generator.Generate(plainInput, "generated", nil)
			if err != nil || next == nil {
				failures <- "plain generation failed"
				return
			}
			if !strings.Contains(string(next.Files["handler.gen.go"]), `RoleADMIN, err :=`) {
				failures <- "enum directory leaked between compilations"
			}
		})
	}
	wg.Wait()
	close(failures)
	for message := range failures {
		t.Error(message)
	}
}

func TestGeneratedModelWhereSymbolBindings(t *testing.T) {
	runGeneratedFixture(t, `model Item @noTime { id: Int @id }
api probe(ItemWhere: Int): Int { return Item.count(where: it.id > 0) }
fn localWhere(ItemWhere: Int): Int { return Item.count(where: it.id > 0) }
`, `package generated
import "testing"
func TestWhereBindingConstructor(t *testing.T){if handleProbe(nil)==nil{t.Fatal("nil handler")}}
`, StableIDs{ModelFields: map[string]map[string]int{"Item": {"id": 1}}})
}

func TestBindingSymbolsUseExactOwnersAndConstants(t *testing.T) {
	generator := defaultGenerator()
	generator.enumSymbols = enumSymbols{"Role": "Role", "RoleADMIN": "Role", "_luxoParam0": "_luxo"}
	params := []*ast.ParamDecl{{Name: "RoleADMIN"}, {Name: "ctx"}, {Name: "_luxoParam1"}, {Name: "RoleADMINExtra"}}
	names := generator.parameterBindings(params, nil, nil, nil)
	for _, source := range []string{"RoleADMIN", "ctx", "_luxoParam1"} {
		name := names.name(source)
		if name == source || name == "_luxoParam0" || name == "_luxoParam1" {
			t.Fatal("constant directory or fresh alias collision was ignored", source, name)
		}
	}
	if names.name("RoleADMINExtra") != "RoleADMINExtra" || len(generator.enumSymbols) != 3 {
		t.Fatal("constant lookup was not exact or changed the borrowed directory")
	}
	catalog := bindingTypes{models: map[string]*ast.ModelDecl{"Item": {Name: "Item"}}}
	for name, want := range map[string]bool{"ItemWhere": true, "MissingWhere": false, "ItemWhereExtra": false, "Itemwhere": false, "Where": false} {
		if catalog.contains(name) != want {
			t.Fatal("Where helper acquired the wrong owner", name, want)
		}
	}
}

func TestGeneratedReaderWriterSymbolBindings(t *testing.T) {
	runGeneratedFixture(t, `model Item @noTime { id: Int @id children: [Other] @by(itemId, id) val total: Int get @count(children) }
model Other @noTime { id: Int @id itemId: Int }
api probe(WriteColumnarItem: Int, ReadJSONInputItemList: Int, referenceItemList: Int, resolveItemComputed: Int, items: [Item]): [Item] @native
api readComputed(resolveItemComputed: Int, row: Item): Item { return row }
`, `package generated
import("bytes";"context";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api")
type helperBindingResolver struct{}
func(helperBindingResolver)Probe(_ context.Context,a,b,c,d int64,items []*Item)([]*Item,error){if a!=1||b!=2||c!=3||d!=4{panic("wire parameter names changed")};return items,nil}
func TestReaderWriterBindings(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,&App{Resolver:helperBindingResolver{}});err!=nil{t.Fatal(err)}
 req:=httptest.NewRequest("POST","/",bytes.NewBufferString("{\"$api\":\"probe\",\"$select\":\"id\",\"WriteColumnarItem\":1,\"ReadJSONInputItemList\":2,\"referenceItemList\":3,\"resolveItemComputed\":4,\"items\":[]}"));req.Header.Set("Content-Type","application/json")
 out:=httptest.NewRecorder();router.ServeHTTP(out,req);if out.Code!=200||!bytes.Contains(out.Body.Bytes(),[]byte("\"data\":[]")){t.Fatal(out.Code,out.Body.String())}
 if handleReadComputed(nil)==nil{t.Fatal("missing computed handler")}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Item": {"id": 1, "children": 2, "total": 3}, "Other": {"id": 1, "itemId": 2}}})
}

func TestGeneratedEventErrorSymbolBindings(t *testing.T) {
	runGeneratedFixture(t, `event Changed(value: Int)
error Rejected(RejectedErrorData: Int) { code: 422 message: "rejected" }
api probe(EmitChanged: Int, ChangedEvent: Int): Int { emit Changed(value: 1) return 1 }
api eventTypeProbe(ChangedEvent: Int): Int { emit Changed(value: 1) return 1 }
api errorProbe(NewRejected: Int): Int { throw Rejected(RejectedErrorData: 1) }
on Changed { val NewRejected = 7 throw Rejected(RejectedErrorData: NewRejected) }
`, `package generated
import("testing")
func TestEventErrorBindings(t *testing.T){
 if handleProbe(nil)==nil||handleEventTypeProbe(nil)==nil||handleErrorProbe(nil)==nil{t.Fatal("missing handler")}
 failure:=NewRejected(7);data,ok:=failure.Data.(*RejectedErrorData);if !ok{t.Fatal("typed error data changed")};body,err:=data.AppendJSON(nil);if err!=nil||string(body)!="{\"RejectedErrorData\":7}"{t.Fatal(string(body),err)}
}
`, StableIDs{EventFields: map[string]map[string]int{"Changed": {"value": 1}}})
}

func TestGeneratedSelectorLoadAndInferredSymbolBindings(t *testing.T) {
	runGeneratedFixture(t, `model Item @noTime { id: Int @id a: Int b: Int selectItemSQLColumns: Int resolveItemListRelations: Int children: [Other] @by(itemId, id) }
model Other @noTime { id: Int @id itemId: Int }
api selected(selectItemSQLColumns: Int): [Item] { return Item.select().all() }
api keyed(ItemByAAndBKey: Int): [Item] { return Item.load(a: 1, b: 2) }
api listItemsBySelectItemSQLColumns
api listItemsByResolveItemListRelations
`, `package generated
import "testing"
func TestSelectorLoadConstructors(t *testing.T){if handleSelected(nil)==nil||handleKeyed(nil)==nil{t.Fatal("missing selected/load handler")}}
`, StableIDs{ModelFields: map[string]map[string]int{"Item": {"id": 1, "a": 2, "b": 3, "selectItemSQLColumns": 4, "resolveItemListRelations": 5, "children": 6}, "Other": {"id": 1, "itemId": 2}}})
}

func TestGeneratedStreamHelperSymbolBindings(t *testing.T) {
	runGeneratedFixture(t, `model Item @noTime { id: Int @id }
api watch(WatchStreamParams: Int, ownStreamInputItem: Int, ReadJSONInputItemList: Int, referenceItemList: Int, items: [Item]): Int @stream @native
`, `package generated
import("encoding/json";"testing";"github.com/light-speak/luxo/pkg/lux/api")
func TestStreamBindingSnapshot(t *testing.T){
 var snapshot WatchStreamParams
 req:=&api.Request{Params:map[string]json.RawMessage{"WatchStreamParams":[]byte("7"),"ownStreamInputItem":[]byte("8"),"ReadJSONInputItemList":[]byte("9"),"referenceItemList":[]byte("10"),"items":[]byte("[]")}}
 if err:=snapshot.readRequest(req);err!=nil||snapshot.WatchStreamParams!=7||snapshot.OwnStreamInputItem!=8||snapshot.ReadJSONInputItemList!=9||snapshot.ReferenceItemList!=10||len(snapshot.Items)!=0{t.Fatal(snapshot,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Item": {"id": 1}}})
}

func TestBindingPlanSymbolBoundaries(t *testing.T) {
	plan := &bindingSymbolPlan{models: map[string]*ast.ModelDecl{"Owned": {Name: "Owned"}, "OwnedList": {Name: "OwnedList"}, "Remote": nil}, types: map[string]*ast.TypeDecl{"Packet": {Name: "Packet"}}, enums: enumSymbols{"RoleADMIN": "Role"}, declarations: map[string]bindingDeclaration{"Changed": bindingEvent, "Rejected": bindingError | bindingErrorData, "Plain": bindingError}, exact: map[string]bool{"opaqueKey": true}, relations: map[string]bool{"Owned": true, "OwnedList": true}, computed: map[string]bool{"Owned": true}, selectors: true, owners: &streamInputOwnerPlan{seen: map[string]bool{"Owned": true}}}
	for _, name := range []string{"Owned", "Remote", "Packet", "RoleADMIN", "opaqueKey", "OwnedWhere", "WriteColumnarPacket", "ReadJSONInputRemoteList", "referenceRemoteList", "resolveOwnedRelations", "resolveOwnedListRelations", "resolveOwnedComputed", "selectRemoteSQLColumns", "hasOwnedSelectedRelations", "ownStreamInputOwned", "EmitChanged", "ChangedEvent", "NewRejected", "RejectedErrorData", "NewPlain"} {
		if !plan.contains(name) {
			t.Error("actual source symbol was not protected", name)
		}
	}
	for _, name := range []string{"", "RemoteWhere", "WriteColumnarMissing", "ReadJSONInputMissingList", "referencePacketList", "resolveMissingRelations", "selectMissingSQLColumns", "hasPacketSelectedRelations", "ownStreamInputMissing", "PlainErrorData", "EmitMissing", "ChangedEventExtra", "ordinary"} {
		if plan.contains(name) {
			t.Error("nonexistent helper was protected", name)
		}
	}
	plan.selectors = false
	plan.owners = nil
	delete(plan.relations, "Owned")
	if plan.contains("selectOwnedSQLColumns") || plan.contains("ownStreamInputOwned") || !plan.contains("resolveOwnedListRelations") {
		t.Fatal("conditional or ambiguous helper owner was not resolved exactly")
	}
	fallback := bindingTypes{models: plan.models}
	if fallback.contains("RemoteWhere") || !fallback.contains("OwnedWhere") {
		t.Fatal("legacy Where lookup accepted an external stub")
	}
	g := defaultGenerator()
	g.bindingPlan = plan
	names := g.parameterBindings([]*ast.ParamDecl{{Name: "RemoteWhere"}, {Name: "OwnedWhere"}}, nil, nil, nil)
	if names.name("RemoteWhere") != "RemoteWhere" || names.name("OwnedWhere") == "OwnedWhere" {
		t.Fatal("owned/external receiver names acquired the wrong aliases", names)
	}
}

func TestBindingPlanContextAndSparseDomains(t *testing.T) {
	plain := analyzePermissionFixture(t, `api plain(value: Int): Int { return value }`)
	base := defaultGenerator()
	bound := base.bindingContext(plain, nil)
	if base.bindingSource != nil || base.bindingPlan != nil || bound.bindingPlan != nil || bound.bindingContext(plain, nil) != bound {
		t.Fatal("empty directory or source ownership changed")
	}
	declarations := analyzePermissionFixture(t, `enum Role { ADMIN USER } event Changed() error Rejected { code: 422 message: "rejected" }`)
	next := bound.bindingContext(declarations, map[string]bool{"Role": true})
	if next.bindingPlan == nil || !next.bindingPlan.contains("RoleADMIN") || !next.bindingPlan.contains("EmitChanged") || !next.bindingPlan.contains("NewRejected") || next.bindingPlan.contains("RejectedErrorData") {
		t.Fatal("source declarations did not create the exact directory")
	}
	stream := analyzePermissionFixture(t, `api watch(value: Int): Int @stream @native`)
	streamed := base.bindingContext(stream, nil)
	if streamed.bindingPlan == nil || !streamed.bindingPlan.contains("WatchStreamParams") || streamed.bindingPlan.owners != nil {
		t.Fatal("scalar stream created object ownership state")
	}
	base.addComputedBindingSymbol("Ignored")
	base.addImportBindingSymbols(map[string]string{"module": "module_luxo"})
	next.addImportBindingSymbols(map[string]string{"module": "module_luxo"})
	if !next.bindingPlan.contains("module_luxo") {
		t.Fatal("actual module import alias was not protected")
	}
	configured := defaultGenerator()
	configured.events = &EventContext{}
	fork := configured.bindingContext(plain, nil)
	if configured.bindingPlan != nil || configured.bindingSource != nil || fork.parameterBindings([]*ast.ParamDecl{{Name: "value"}}, nil, nil, nil) != nil {
		t.Fatal("configured context retained a directory or aliased an ordinary name")
	}
}

func TestBindingSourceFragmentEntryCompatibility(t *testing.T) {
	var output strings.Builder
	writePlannedStreamInputOwners(&output, nil, nil)
	if output.Len() != 0 {
		t.Fatal("empty stream ownership domain emitted code")
	}
	declaration := &ast.ErrorDecl{Name: "Rejected", Fields: []*ast.ParamDecl{{Name: "RejectedErrorData", Type: &ast.TypeRef{Name: "Int"}}}}
	generateErrorConstructor(&output, declaration, nil)
	if strings.Contains(output.String(), "NewRejected(RejectedErrorData int64)") {
		t.Fatal("legacy constructor parameter shadowed its generated data type")
	}
	explicit := &ast.OnDecl{Params: []string{"payload"}, Body: &ast.Block{}}
	source, name, names := listenerPayloadBinding(explicit)
	if source != "payload" || name == "payload" || names.name(source) != name {
		t.Fatal("explicit listener payload binding changed", source, name)
	}
}
