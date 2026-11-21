package codegen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestJoinGenerationRejectsListSeparator(t *testing.T) {
	input := &ast.Ident{Name: "values"}
	input.SetTypeTag("Int")
	input.SetListType(true)
	separator := &ast.Ident{Name: "separator"}
	separator.SetTypeTag("String")
	separator.SetListType(true)
	call := &ast.CallExpr{Func: &ast.MemberExpr{Object: input, Field: "joinToString"}, Args: []*ast.NamedArg{{Value: separator}}}
	call.SetTypeTag("String")
	file := &ast.File{Functions: []*ast.FnDecl{{Name: "join", ReturnType: &ast.TypeRef{Name: "String"}, Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: call}}}}}}
	generated, err := Generate(result(file), "generated", GeneratorConfig{})
	if err == nil || generated != nil || !strings.Contains(err.Error(), "separator") {
		t.Fatalf("list separator generated invalid source: %v", err)
	}
}

func TestCollectionCompilerRequiresLambda(t *testing.T) {
	input := &ast.Ident{Name: "values"}
	input.SetTypeTag("Int")
	input.SetListType(true)
	call := &ast.CallExpr{Func: &ast.MemberExpr{Object: input, Field: "map"}}
	compiler := newCompiler(nil)
	if _, handled := compiler.compileCollectionCall(call); handled {
		t.Fatal("missing lambda accepted")
	}
	call.Args = []*ast.NamedArg{{Value: &ast.Ident{Name: "value"}}}
	if _, handled := compiler.compileCollectionCall(call); handled {
		t.Fatal("non-lambda accepted")
	}
}

func TestGeneratedCollectionLengthProperties(t *testing.T) {
	runGeneratedFixture(t, collectionLengthSource, collectionLengthAssertions, StableIDs{
		ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}, "LengthBox": {"values": 1, "size": 2, "length": 3}},
		APIs:        map[string]int{"propertySize": 71, "propertyEmpty": 72},
		APIParams:   map[string]map[string]int{"propertySize": {"values": 1}, "propertyEmpty": {"values": 1}},
	})
}

func TestCollectionLengthPropertyMatchesMethod(t *testing.T) {
	for _, member := range []struct{ name, result string }{{"size", "Int"}, {"isEmpty", "Boolean"}} {
		t.Run(member.name, func(t *testing.T) {
			generate := func(suffix string) *GenerateResult {
				input := analyzePermissionFixture(t, "fn run(values: [Int]): "+member.result+" { return values."+member.name+suffix+" }")
				generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG})
				if err != nil {
					t.Fatal(err)
				}
				return generated
			}
			property, method := generate(""), generate("()")
			if len(property.Files) != len(method.Files) {
				t.Fatal("different generated file counts")
			}
			for name, source := range property.Files {
				if !bytes.Equal(source, method.Files[name]) {
					t.Fatalf("property and method generated different %s", name)
				}
			}
		})
	}
}

func TestFormalGenerateRejectsLengthArguments(t *testing.T) {
	for _, sample := range []struct{ receiver, member, result string }{
		{"[Int]", "size", "Int"}, {"[Int]", "isEmpty", "Boolean"},
		{"String", "size", "Int"}, {"String", "length", "Int"}, {"String", "isEmpty", "Boolean"},
	} {
		t.Run(sample.receiver+"/"+sample.member, func(t *testing.T) {
			input := analyzePermissionFixture(t, "fn run(value: "+sample.receiver+"): "+sample.result+" { return value."+sample.member+"(1) }")
			output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG})
			if err == nil || output != nil || !strings.Contains(err.Error(), "accepts no arguments") {
				t.Fatalf("invalid length call produced output: %v", err)
			}
		})
	}
}

func TestFormalGenerateRejectsListElementFields(t *testing.T) {
	for _, source := range []string{
		`fn run(values: [String]): Int { return values.length }`,
		`fn run(values: [String]): Int { return values.length() }`,
		`fn run(values: [String]): String { return values.lowercase() }`,
		`type Box { length: Int } fn run(values: [Box]): Int { return values.length }`,
	} {
		input := analyzePermissionFixture(t, source)
		output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: StableIDs{ModelFields: map[string]map[string]int{"Box": {"length": 1}}}})
		if err == nil || output != nil || !strings.Contains(err.Error(), "has no field") {
			t.Fatalf("list inherited an element field: %v", err)
		}
	}
}

func TestFormalGenerateRejectsUnsupportedListMembers(t *testing.T) {
	for _, method := range []string{"first", "contains", "count", "take", "flatMap", "groupBy", "chunked", "days"} {
		for _, receiver := range []string{"[Int]", "[Node]"} {
			input := analyzePermissionFixture(t, `model Node @noTime { id: Int @id } fn probe(values: `+receiver+`): Int { values.`+method+`() return 1 }`)
			output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: StableIDs{ModelFields: map[string]map[string]int{"Node": {"id": 1}}}})
			if err == nil || output != nil || !strings.Contains(err.Error(), "has no field") {
				t.Fatalf("unsupported list member generated output: %s %s / %v", receiver, method, err)
			}
		}
	}
}

func TestGenerateRejectsUncheckedListMemberAST(t *testing.T) {
	for _, call := range []bool{false, true} {
		for _, method := range []string{"first", "contains", "count", "take", "chunked", "days"} {
			input := analyzePermissionFixture(t, `fn probe(values: [Int]): Int { return 1 }`)
			list := &ast.Ident{Name: "values"}
			list.SetTypeTag("Int")
			list.SetListType(true)
			var expression ast.Expr = &ast.MemberExpr{Object: list, Field: method}
			if call {
				expression = &ast.CallExpr{Func: expression}
			}
			body := input.Files[0].Functions[0].Body
			body.Stmts = append([]ast.Stmt{&ast.ExprStmt{Expr: expression}}, body.Stmts...)
			output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG})
			if err == nil || output != nil || !strings.Contains(err.Error(), "list") {
				t.Fatalf("unchecked list AST escaped: %s call=%v / %v", method, call, err)
			}
		}
	}
}

func TestGenerateRejectsUncheckedCollectionPropertyArguments(t *testing.T) {
	for _, method := range []string{"size", "isEmpty"} {
		input := analyzePermissionFixture(t, `fn probe(values: [Int]): Int { return 1 }`)
		list := &ast.Ident{Name: "values"}
		list.SetTypeTag("Int")
		list.SetListType(true)
		call := &ast.CallExpr{
			Func: &ast.MemberExpr{Object: list, Field: method},
			Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "unexpected"}}},
		}
		body := input.Files[0].Functions[0].Body
		body.Stmts = append([]ast.Stmt{&ast.ExprStmt{Expr: call}}, body.Stmts...)
		output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG})
		if err == nil || output != nil || !strings.Contains(err.Error(), "list call") {
			t.Fatalf("unchecked list arguments escaped: %s / %v", method, err)
		}
	}
}

func TestGeneratedQueryCollectionLength(t *testing.T) {
	runGeneratedFixture(t, collectionLengthSource, collectionLengthQueryAssertions, StableIDs{
		ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}, "LengthBox": {"values": 1, "size": 2, "length": 3}},
		APIs:        map[string]int{"propertySize": 71, "propertyEmpty": 72},
		APIParams:   map[string]map[string]int{"propertySize": {"values": 1}, "propertyEmpty": {"values": 1}},
	})
}

const collectionLengthSource = `
model Counter @noTime { id: Int @id value: Int }
type LengthBox { values: [Int] size: Int length: Int }
fn readValues(): Result<[Int]> @native
fn listSize(values: [Int]): Int { return values.size }
fn listEmpty(values: [Int]): Boolean { return values.isEmpty }
fn sizeMethod(values: [Int]): Int { return values.size() }
fn emptyMethod(values: [Int]): Boolean { return values.isEmpty() }
fn literalSize(): Int { return [1, 2, 3].size }
fn literalEmpty(): Boolean { val values: [Int] = [] return values.isEmpty }
fn mappedSize(values: [Int]): Int { return values.map { it + 1 }.size }
fn nestedSize(value: LengthBox): Int { return value.values.size }
fn ordinarySize(value: LengthBox): Int { return value.size }
fn ordinaryLength(value: LengthBox): Int { return value.length }
fn stringLength(value: String): Int { return value.length }
fn stringSize(value: String): Int { return value.size }
fn stringEmpty(value: String): Boolean { return value.isEmpty }
fn nullableSize(values: [Int]?): Int? { return values?.size }
fn nullableEmpty(values: [Int]?): Boolean? { return values?.isEmpty }
fn readSize(): Int { return (readValues()?).size }
fn readSizeMethod(): Int { return (readValues()?).size() }
fn querySize(): Int { return Counter.all().size }
fn querySizeMethod(): Int { return Counter.all().size() }
fn queryEmpty(): Boolean { return Counter.all().isEmpty }
fn queryEmptyMethod(): Boolean { return Counter.all().isEmpty() }
api propertySize(values: [Int]): Int { return values.size }
api propertyEmpty(values: [Int]): Boolean { return values.isEmpty }
`

const collectionLengthQueryAssertions = `package generated
import (
 "context"
 "strings"
 "testing"
 "github.com/light-speak/luxo/internal/testutil"
 "github.com/light-speak/luxo/pkg/lux/pg"
 "github.com/testcontainers/testcontainers-go"
 "github.com/testcontainers/testcontainers-go/modules/postgres"
)
type lengthResolver struct{}
func (*lengthResolver) ReadValues(context.Context)([]int64,error){return nil,nil}
func TestQueryCollectionLength(t *testing.T) {
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("length_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE counters(id bigint PRIMARY KEY,value bigint NOT NULL)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db)
 sizes:=[]func(context.Context)(int64,error){app.querySize,app.querySizeMethod}
 empty:=[]func(context.Context)(bool,error){app.queryEmpty,app.queryEmptyMethod}
 for _,count:=range []int64{0,3} {
  if count>0 {if _,err:=pg.Exec(t.Context(),db,"INSERT INTO counters VALUES(1,1),(2,2),(3,3)");err!=nil{t.Fatal(err)}}
  for _,call:=range sizes {if value,err:=call(t.Context());value!=count||err!=nil{t.Fatal(value,count,err)}}
  for _,call:=range empty {if value,err:=call(t.Context());value!=(count==0)||err!=nil{t.Fatal(value,count,err)}}
 }
 if _,err:=pg.Exec(t.Context(),db,"DROP TABLE counters");err!=nil{t.Fatal(err)}
 for _,call:=range sizes {if _,err:=call(t.Context());err==nil||!strings.Contains(err.Error(),"42P01"){t.Fatal("query failure lost",err)}}
 for _,call:=range empty {if _,err:=call(t.Context());err==nil||!strings.Contains(err.Error(),"42P01"){t.Fatal("query failure lost",err)}}
}
`

const collectionLengthAssertions = `package generated
import (
 "bytes"
 "context"
 stderrors "errors"
 "encoding/json"
 "net/http/httptest"
 "strconv"
 "testing"
 "github.com/light-speak/luxo/pkg/lux/codec"
 "github.com/light-speak/luxo/pkg/lux/luvia"
)
type lengthResolver struct { calls int; failure error }
func (r *lengthResolver) ReadValues(context.Context) ([]int64,error) {
 r.calls++
 if r.failure!=nil{return nil,r.failure}
 return []int64{1,2,3},nil
}
func TestCollectionLengthProperties(t *testing.T) {
 resolver:=&lengthResolver{}
 app:=&App{Resolver:resolver}
 for _,values:=range [][]int64{{},{1,2,3}} {
  want:=int64(len(values))
  for _,call:=range []func(context.Context,[]int64)(int64,error){app.listSize,app.sizeMethod,app.mappedSize} {
   if got,err:=call(t.Context(),values);got!=want||err!=nil{t.Fatal(got,want,err)}
  }
  for _,call:=range []func(context.Context,[]int64)(bool,error){app.listEmpty,app.emptyMethod} {
   if got,err:=call(t.Context(),values);got!=(want==0)||err!=nil{t.Fatal(got,want,err)}
  }
 }
 if value,err:=app.literalSize(t.Context());value!=3||err!=nil{t.Fatal(value,err)}
 if value,err:=app.literalEmpty(t.Context());!value||err!=nil{t.Fatal(value,err)}
 box:=LengthBox{Values:[]int64{1,2},Size:99,Length:88}
 if value,err:=app.nestedSize(t.Context(),box);value!=2||err!=nil{t.Fatal(value,err)}
 if value,err:=app.ordinarySize(t.Context(),box);value!=99||err!=nil{t.Fatal(value,err)}
 if value,err:=app.ordinaryLength(t.Context(),box);value!=88||err!=nil{t.Fatal(value,err)}
 for _,text:=range []string{"","abc"} {
  for _,call:=range []func(context.Context,string)(int64,error){app.stringLength,app.stringSize} {
   if value,err:=call(t.Context(),text);value!=int64(len(text))||err!=nil{t.Fatal(value,err)}
  }
  if value,err:=app.stringEmpty(t.Context(),text);value!=(len(text)==0)||err!=nil{t.Fatal(value,err)}
 }
 for _,values:=range [][]int64{nil,{}, {1,2}} {
  size,err:=app.nullableSize(t.Context(),values);if err!=nil{t.Fatal(err)}
  empty,err:=app.nullableEmpty(t.Context(),values);if err!=nil{t.Fatal(err)}
  if values==nil {if size!=nil||empty!=nil{t.Fatal("null became empty")};continue}
  if size==nil||empty==nil||*size!=int64(len(values))||*empty!=(len(values)==0){t.Fatal("nullable length mismatch")}
 }
 for _,call:=range []func(context.Context)(int64,error){app.readSize,app.readSizeMethod} {
  before:=resolver.calls
  if value,err:=call(t.Context());value!=3||err!=nil||resolver.calls!=before+1{t.Fatal(value,err,resolver.calls,before)}
  expected:=stderrors.New("read failed");resolver.failure=expected;before=resolver.calls
  if _,err:=call(t.Context());err!=expected||resolver.calls!=before+1{t.Fatal(err,resolver.calls,before)}
  resolver.failure=nil
 }
}
func TestCollectionLengthHTTP(t *testing.T) {
 router:=luvia.New().Router
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{Resolver:&lengthResolver{}});err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,binary:=range []bool{false,true} {for _,values:=range [][]int64{{},{1,2,3}} {
  for _,name:=range []string{"propertySize","propertyEmpty"} {
   var body []byte
   if binary {
    body=codec.AppendVarint(nil,uint64(router.Schema.APIs[name].ID));body=codec.AppendVarint(body,0)
    encoder:=codec.NewEncoder(body);encoder.WriteFieldIntArray(1,values);encoder.WriteEnd();body=encoder.Bytes()
   } else {
    body=[]byte("{\"$api\":\""+name+"\",\"values\":[")
    for i,value:=range values {if i>0{body=append(body,',')};body=strconv.AppendInt(body,value,10)}
    body=append(body,']','}')
   }
   request:=httptest.NewRequest("POST","/",bytes.NewReader(body));request.Header.Set("Content-Type","application/json")
   if binary{request.Header.Set("X-Luxo-Mode","binary")}
   response:=httptest.NewRecorder();router.ServeHTTP(response,request)
   if response.Code!=200{t.Fatal(name,binary,response.Code,response.Body.String())}
   expected:=[]byte(strconv.Itoa(len(values)))
   if name=="propertyEmpty"{expected=[]byte(strconv.FormatBool(len(values)==0))}
   if binary {
    expected=codec.AppendSvarint(nil,int64(len(values)))
    if name=="propertyEmpty"{expected=codec.AppendBool(nil,len(values)==0)}
    if !bytes.Equal(response.Body.Bytes(),expected){t.Fatal(name,response.Body.Bytes(),expected)}
   } else {
    var envelope struct{Data json.RawMessage}
    if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil||!bytes.Equal(envelope.Data,expected){t.Fatal(name,response.Body.String(),err)}
   }
  }
 }}
}
`

func TestGeneratedTypedCollections(t *testing.T) {
	runGeneratedTransactionFixture(t, `
fn number(value: Int): Result<Int> @native
api mapped(values: [Int]): [Int] { return values.map { it + 1 } }
api filtered(values: [Int]): [Int] { return values.filter { it > 2 } }
api chained(values: [Int]): [Int] { return values.map { value -> value + 1 }.filter { it > 2 } }
api blockMapped(values: [Int]): [Int] {
 return values.map { value ->
  val doubled = value * 2
  if doubled > 10 { return 10 }
  doubled
 }
}
api blockFiltered(values: [Int]): [Int] {
 return values.filter { value ->
  if value < 0 { return false }
  value > 2
 }
}
api fallibleMapped(values: [Int]): [Int] { return values.map { number(it)? } }
api fallibleFiltered(values: [Int]): [Int] { return values.filter { number(it)? > 2 } }
api constantMapped(values: [Int]): [Int] { return values.map { 9 } }
api nestedMapped(values: [Int]): [Int] { return values.map { it + values.filter { it > 0 }.size() } }
api shadowedMapped(values: [Int]): [Int] { return values.map { _index -> _index + 1 } }
api shadowedFiltered(values: [Int]): [Int] { return values.filter { _result -> _result > 2 } }
api capturedMapped(values: [Int]): [Int] {
 val _result = 5
 return values.map { it + _result }
}
api generatedNameMapped(values: [Int]): [Int] {
 val _collection1Result = 5
 return values.map { it + _collection1Result }
}
fn checkedValues(values: [Int]): [Int] {
 number(99)?
 return values
}
api sourceMapped(values: [Int]): [Int] { return checkedValues(values).map { number(it)? } }
api visited(values: [Int]): [Int] { values.forEach { ctx -> number(ctx)? } return values }
api sourceVisited(values: [Int]): [Int] { checkedValues(values).forEach { req -> number(req)? } return values }
`, typedCollectionAssertions)
}

func TestGeneratedCollectionElementTypes(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime { id: Int @id name: String }
type Snapshot { value: Int }
enum State { OPEN CLOSED }
fn mapBoolean(values: [Int]): [Boolean] { return values.map { it > 0 } }
fn mapInt(values: [Int]): [Int] { return values.map { it + 1 } }
fn filterInt(values: [Int]): [Int] { return values.filter { it > 0 } }
fn mapText(values: [Int]): [String] { return values.map { "text" } }
fn mapEnum(values: [Int]): [State] { return values.map { when { it > 0 -> State.OPEN else -> State.CLOSED } } }
fn mapSnapshot(values: [Int]): [Snapshot] { return values.map { Snapshot { value: it } } }
fn mapModel(values: [Int]): [User] { return values.map { User { id: it, name: "user" } } }
fn mapModelLocal(values: [Int]): [User] {
 return values.map { val user = User { id: it, name: "user" }
 user }
}
fn mapModelWhen(values: [Int]): [User] {
 return values.map { when { it > 0 -> User { id: it, name: "positive" } else -> User { id: it, name: "other" } } }
}
fn mapIdentity(values: [User]): [User] { return values.map { it } }
fn filterModel(values: [User]): [User] { return values.filter { it.id > 0 } }
fn visitModels(values: [User]): Int { var total = 0 values.forEach { ctx -> total += ctx.id } return total }
fn visitSnapshots(values: [Snapshot]): Int { var total = 0 values.forEach { req -> total += req.value } return total }
fn anyModel(values: [User]): Boolean { return values.any { ctx -> ctx.id > 0 } }
fn anyEnum(values: [State]): Boolean { return values.any { req -> req == State.OPEN } }

fn loopMap(values: [Int]): [Int] { return values.map { value ->
 for other in values {
  if other < 0 { continue }
  if other > 2 { return other }
  break
 }
 value
} }
`, collectionElementTypeAssertions+collectionBenchmarks, StableIDs{ModelFields: map[string]map[string]int{
		"User": {"id": 1, "name": 2}, "Snapshot": {"value": 1},
	}})
}

const collectionElementTypeAssertions = `package generated
import("context";"fmt";"slices";"testing")
func TestCollectionElementTypes(t *testing.T){
 app:=&App{};ctx:=t.Context();input:=[]int64{-1,2,7}
 booleans,err:=app.mapBoolean(ctx,input);if err!=nil||!slices.Equal(booleans,[]bool{false,true,true}){t.Fatal(booleans,err)}
 texts,err:=app.mapText(ctx,input);if err!=nil||!slices.Equal(texts,[]string{"text","text","text"}){t.Fatal(texts,err)}
 states,err:=app.mapEnum(ctx,input);if err!=nil||!slices.Equal(states,[]State{StateCLOSED,StateOPEN,StateOPEN}){t.Fatal(states,err)}
 snapshots,err:=app.mapSnapshot(ctx,input);if err!=nil||len(snapshots)!=len(input){t.Fatal(snapshots,err)}
 for i,value:=range snapshots{if value.Value!=input[i]{t.Fatal(snapshots)}}
 if total,err:=app.visitSnapshots(ctx,snapshots);err!=nil||total!=8{t.Fatal(total,err)}
 if any,err:=app.anyEnum(ctx,states);err!=nil||!any{t.Fatal(any,err)}
 for _,fn:=range []func([]int64)([]*User,error){
  func(values []int64)([]*User,error){return app.mapModel(ctx,values)},
  func(values []int64)([]*User,error){return app.mapModelLocal(ctx,values)},
  func(values []int64)([]*User,error){return app.mapModelWhen(ctx,values)},
 }{
  users,err:=fn(input);if err!=nil||len(users)!=len(input){t.Fatal(users,err)}
  for i,user:=range users{if user==nil||user.Id!=input[i]{t.Fatal(users)}}
  if total,err:=app.visitModels(ctx,users);err!=nil||total!=8{t.Fatal(total,err)}
  if any,err:=app.anyModel(ctx,users);err!=nil||!any{t.Fatal(any,err)}
  same,err:=app.mapIdentity(ctx,users);if err!=nil||!slices.Equal(same,users){t.Fatal(same,err)}
  filtered,err:=app.filterModel(ctx,users);if err!=nil||!slices.Equal(filtered,users[1:]){t.Fatal(filtered,err)}
 }
 looped,err:=app.loopMap(ctx,input);if err!=nil||!slices.Equal(looped,input){t.Fatal(looped,err)}
}
`

const collectionBenchmarks = `
func (*App) referenceMap(_ context.Context,values []int64)([]int64,error){
 result:=make([]int64,len(values));for i,value:=range values{result[i]=value+1};return result,nil
}
func (*App) referenceFilter(_ context.Context,values []int64)([]int64,error){
 var result []int64;for _,value:=range values{if value>0{result=append(result,value)}};return result,nil
}
var collectionBenchmarkResult []int64
func BenchmarkTypedCollections(b *testing.B){
 app:=&App{};ctx:=context.Background()
 for _,size:=range []int{0,1,128}{
  for _,selected:=range []int{0,50,100}{
   values:=make([]int64,size);for i:=range values{values[i]=-1;if i*100<size*selected{values[i]=1}}
   for _,sample:=range []struct{name string;run func(context.Context,[]int64)([]int64,error)}{
    {"MapGenerated",app.mapInt},{"MapReference",app.referenceMap},
    {"FilterGenerated",app.filterInt},{"FilterReference",app.referenceFilter},
   }{b.Run(fmt.Sprintf("%s/rows=%d/selected=%d",sample.name,size,selected),func(b *testing.B){
    b.ReportAllocs();for b.Loop(){values,err:=sample.run(ctx,values);if err!=nil{b.Fatal(err)};collectionBenchmarkResult=values}
   })}
  }
 }
}
`

const typedCollectionAssertions = `package generated
import("bytes";"context";"encoding/json";"errors";"fmt";"net/http/httptest";"slices";"testing"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
type collectionResolver struct{ calls []int64; failAt int64 }
func(r *collectionResolver)Number(_ context.Context,value int64)(int64,error){
 r.calls=append(r.calls,value)
 if value==r.failAt{return 0,errors.New("number failed")};return value,nil
}
func TestTypedCollectionsHTTP(t *testing.T){
 for _,binary:=range []bool{false,true}{for _,sample:=range []struct{name string;want []int64}{
  {"mapped",[]int64{0,3,8}}, {"filtered",[]int64{7}}, {"chained",[]int64{3,8}},
  {"blockMapped",[]int64{-2,4,10}}, {"blockFiltered",[]int64{7}},
  {"fallibleMapped",[]int64{-1,2,7}}, {"fallibleFiltered",[]int64{7}},
  {"constantMapped",[]int64{9,9,9}}, {"nestedMapped",[]int64{1,4,9}},
  {"shadowedMapped",[]int64{0,3,8}}, {"shadowedFiltered",[]int64{7}},
  {"capturedMapped",[]int64{4,7,12}},
  {"generatedNameMapped",[]int64{4,7,12}},
  {"sourceMapped",[]int64{-1,2,7}},
  {"visited",[]int64{-1,2,7}}, {"sourceVisited",[]int64{-1,2,7}},
 }{t.Run(fmt.Sprintf("%s/%v",sample.name,binary),func(t *testing.T){
  testCollectionRequest(t,sample.name,binary,[]int64{-1,2,7},sample.want,0)
  testCollectionRequest(t,sample.name,binary,[]int64{},[]int64{},0)
  if sample.name=="fallibleMapped"||sample.name=="fallibleFiltered"||sample.name=="visited"{testCollectionRequest(t,sample.name,binary,[]int64{-1,2,7},nil,2)}
  if sample.name=="sourceMapped"||sample.name=="sourceVisited"{for _,failAt:=range []int64{2,99}{testCollectionRequest(t,sample.name,binary,[]int64{-1,2,7},nil,failAt)}}
 })}}
}
func testCollectionRequest(t *testing.T,name string,binary bool,values,want []int64,failAt int64){
 t.Helper();router:=api.NewRouter();resolver:=&collectionResolver{failAt:failAt}
 if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{Resolver:resolver});err!=nil{t.Fatal(err)}
 body,err:=json.Marshal(map[string]any{"$api":name,"values":values});if err!=nil{t.Fatal(err)}
 if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,map[string]any{"values":values},[]api.ParamMeta{{Name:"values",Type:"Int",IsList:true,FieldID:1}});if err!=nil{t.Fatal(err)}}
 request:=httptest.NewRequest("POST","/",bytes.NewReader(body))
 if binary{request.Header.Set("X-Luxo-Mode","binary")}else{request.Header.Set("Content-Type","application/json")}
 response:=httptest.NewRecorder();router.ServeHTTP(response,request)
 status:=200;if failAt!=0{status=500}
 if response.Code!=status{t.Fatalf("status=%d body=%s",response.Code,response.Body.String())}
 if name=="fallibleMapped"||name=="fallibleFiltered"||name=="sourceMapped"||name=="visited"||name=="sourceVisited"{
  calls:=values;if failAt!=0{calls=[]int64{-1,2}}
  if name=="sourceMapped"||name=="sourceVisited"{calls=append([]int64{99},calls...);if failAt==99{calls=[]int64{99}}}
  if !slices.Equal(resolver.calls,calls){t.Fatalf("calls=%v want=%v",resolver.calls,calls)}
 }
 if status!=200{return}
 var got []int64
 if binary{decoder:=codec.NewDecoder(response.Body.Bytes());got=decoder.ReadIntArray();if err:=decoder.Finish();err!=nil{t.Fatal(err)}}else{
  var envelope struct{Data []int64};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil{t.Fatal(err)};got=envelope.Data
 }
 if !slices.Equal(got,want){t.Fatalf("values=%v want=%v",got,want)}
}
`

func TestGeneratedCollectionForEach(t *testing.T) {
	runGeneratedFixture(t, `
fn visit(values: [Int]): Int {
 var total = 0
 values.forEach { ctx ->
  if ctx >= 0 { total += ctx
   ctx
  }
  ctx
 }
 return total
}
fn nestedVisits(values: [Int]): Int {
 var total = 0
 values.forEach { req -> values.forEach { ctx -> total += req * ctx } }
 return total
}
fn failVisit(values: [Int]): Int {
 values.forEach { ctx -> if ctx < 0 { throw error.NotFound } }
 return 42
}
`, `package generated
import("testing";"context";"fmt")

func TestForEach(t *testing.T) {
 app:=&App{}
 for _,sample:=range []struct{values []int64;want int64}{{nil,0},{[]int64{1,2,3},6},{[]int64{-5,2},2}} {
  value,err:=app.visit(t.Context(),sample.values)
  if err!=nil||value!=sample.want{t.Fatal(value,err)}
 }
 if value,err:=app.nestedVisits(t.Context(),[]int64{2,3});err!=nil||value!=25{t.Fatal(value,err)}
 if _,err:=app.failVisit(t.Context(),[]int64{-1});err==nil{t.Fatal("callback error lost")}
 if value,err:=app.failVisit(t.Context(),[]int64{1});err!=nil||value!=42{t.Fatal(value,err)}
}
func (*App) referenceVisit(_ context.Context, values []int64)(int64,error){
 var total int64
 for _,value:=range values {if value>=0{total+=value}}
 return total,nil
}
var forEachBenchmarkResult int64
func BenchmarkForEach(b *testing.B){
 app:=&App{};ctx:=context.Background()
 for _,size:=range []int{0,1,128}{
  values:=make([]int64,size);for index:=range values{values[index]=int64(index)-64}
  for _,sample:=range []struct{name string;call func(context.Context,[]int64)(int64,error)}{
   {"Reference",app.referenceVisit},{"Generated",app.visit},
  }{b.Run(fmt.Sprintf("%s/%d",sample.name,size),func(b *testing.B){b.ReportAllocs();for b.Loop(){value,err:=sample.call(ctx,values);if err!=nil{b.Fatal(err)};forEachBenchmarkResult=value}})}
 }
}

`, StableIDs{})
}

func TestGeneratedDocumentedCollectionMethods(t *testing.T) {
	for _, sample := range []struct{ name, output, expression, check string }{
		{"any", "Boolean", `values.any { it > 0 }`, `!value`},
		{"sumOf", "Int", `values.sumOf { it }`, `value!=3`},
		{"sortBy", "[Int]", `values.sortBy { it }`, `!slices.Equal(value,[]int64{-1,2,2})`},
		{"joinToString", "String", `values.joinToString(",")`, `value!="2,-1,2"`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			imports := `"testing"`
			if sample.name == "sortBy" {
				imports += `;"slices"`
			}
			source := `fn probe(values: [Int]): ` + sample.output + ` { return ` + sample.expression + ` }`
			assertions := `package generated
import (` + imports + `)

func TestDocumentedOperation(t *testing.T){value,err:=(&App{}).probe(t.Context(),[]int64{2,-1,2});if err!=nil||` + sample.check + `{t.Fatal(value,err)}}`
			runGeneratedFixture(t, source, assertions, StableIDs{})
		})
	}
}

func TestGeneratedAnyShortCircuit(t *testing.T) {
	runGeneratedFixture(t, `
fn check(value: Int): Result<Boolean> @native
fn probe(values: [Int]): Boolean { return values.any { ctx -> check(ctx)? } }
`, `package generated
import("context";"errors";"slices";"testing")
type resolver struct{calls []int64}
func(r *resolver)Check(_ context.Context,value int64)(bool,error){r.calls=append(r.calls,value);if value==99{return false,errors.New("bad")};return value>0,nil}
func TestAny(t *testing.T){
 for _,sample:=range []struct{values,calls []int64;want,fail bool}{
  {nil,nil,false,false},{[]int64{-1,0},[]int64{-1,0},false,false},
  {[]int64{-1,2,99},[]int64{-1,2},true,false},
  {[]int64{-1,99,2},[]int64{-1,99},false,true},
 }{r:=&resolver{};value,err:=(&App{Resolver:r}).probe(t.Context(),sample.values);if (err!=nil)!=sample.fail||value!=sample.want||!slices.Equal(r.calls,sample.calls){t.Fatal(value,err,r.calls,sample)}}
}
`, StableIDs{})
}

func TestGeneratedSafeCollections(t *testing.T) {
	runGeneratedFixture(t, `
fn liftList(values: [Int]): [Int]? { return values }
fn liftLocal(values: [Int]): [Int]? { val lifted: [Int]? = values return lifted }
fn liftScalar(): Int? { return 42 }
fn safeMap(values: [Int]?): [Int]? { return values?.map { it + 1 } }
fn safeFilter(values: [Int]?): [Int]? { return values?.filter { it > 0 } }
fn safeAny(values: [Int]?): Boolean? { return values?.any { it > 0 } }
fn failedValue(): Int { throw error.NotFound }
fn safeMapFailure(values: [Int]?): [Int]? { return values?.map { failedValue() } }
fn safeForEach(values: [Int]?): Int { values?.forEach { failedValue() } return 42 }
`, `package generated
import "testing"

func TestSafeCollections(t *testing.T){
 app:=&App{};ctx:=t.Context()
 for _,input:=range [][]int64{nil,{}}{
 if value,err:=app.liftList(ctx,input);err!=nil||value==nil||len(value)!=0{t.Fatal(value,err)}
 if value,err:=app.liftLocal(ctx,input);err!=nil||value==nil||len(value)!=0{t.Fatal(value,err)}
 }
 if value,err:=app.liftScalar(ctx);err!=nil||value==nil||*value!=42{t.Fatal(value,err)}
 if value,err:=app.safeMap(ctx,nil);err!=nil||value!=nil{t.Fatal(value,err)}
 if value,err:=app.safeMap(ctx,[]int64{});err!=nil||value==nil||len(value)!=0{t.Fatal(value,err)}
 if value,err:=app.safeMap(ctx,[]int64{1,2});err!=nil||len(value)!=2||value[0]!=2||value[1]!=3{t.Fatal(value,err)}
 if value,err:=app.safeFilter(ctx,nil);err!=nil||value!=nil{t.Fatal(value,err)}
 for _,input:=range [][]int64{{},{-1}}{if value,err:=app.safeFilter(ctx,input);err!=nil||value==nil||len(value)!=0{t.Fatal(value,err)}}
 if value,err:=app.safeFilter(ctx,[]int64{-1,2});err!=nil||len(value)!=1||value[0]!=2{t.Fatal(value,err)}
 if value,err:=app.safeAny(ctx,nil);err!=nil||value!=nil{t.Fatal(value,err)}
 if value,err:=app.safeAny(ctx,[]int64{});err!=nil||value==nil||*value{t.Fatal(value,err)}
 if value,err:=app.safeAny(ctx,[]int64{1});err!=nil||value==nil||!*value{t.Fatal(value,err)}
 if value,err:=app.safeMapFailure(ctx,nil);err!=nil||value!=nil{t.Fatal(value,err)}
 if _,err:=app.safeMapFailure(ctx,[]int64{1});err==nil{t.Fatal("map error lost")}
 for _,input:=range [][]int64{nil,{}}{if value,err:=app.safeForEach(ctx,input);err!=nil||value!=42{t.Fatal(value,err)}}
 if _,err:=app.safeForEach(ctx,[]int64{1});err==nil{t.Fatal("forEach error lost")}
}
`, StableIDs{})
}

func TestGeneratedJoinScalarValues(t *testing.T) {
	runGeneratedFixture(t, `
fn joinInts(values: [Int], separator: String): String { return values.joinToString(separator) }
fn joinStrings(values: [String]): String { return values.joinToString("|") }
fn joinBools(values: [Boolean]): String { return values.joinToString(",") }
fn joinFloats(values: [Float]): String { return values.joinToString("/") }
fn failedSeparator(): String { throw error.NotFound }
fn joinFailure(values: [Int]): String { return values.joinToString(failedSeparator()) }
fn joinSafe(values: [Int]?): String? { return values?.joinToString(failedSeparator()) }
`, `package generated
import("testing";"math";"strings";"strconv";"context")

func TestScalarJoin(t *testing.T) {
 app:=&App{};ctx:=t.Context()
 for _, sample:=range []struct{input []int64;want string}{
  {nil,""},{[]int64{},""},{[]int64{0},"0"},{[]int64{math.MinInt64,math.MaxInt64},"-9223372036854775808|9223372036854775807"},
 }{got,err:=app.joinInts(ctx,sample.input,"|");if err!=nil||got!=sample.want{t.Fatal(got,err,sample.want)}}
 if got,err:=app.joinStrings(ctx,[]string{"","東京",""});err!=nil||got!="|東京|"{t.Fatal(got,err)}
 if got,err:=app.joinBools(ctx,[]bool{true,false});err!=nil||got!="true,false"{t.Fatal(got,err)}
 if got,err:=app.joinFloats(ctx,[]float64{math.Copysign(0,-1),1.25});err!=nil||got!="-0/1.25"{t.Fatal(got,err)}
 for _,input:=range [][]int64{nil,{1}}{if _,err:=app.joinFailure(ctx,input);err==nil{t.Fatal("separator error lost")}}
 if got,err:=app.joinSafe(ctx,nil);err!=nil||got!=nil{t.Fatal("safe call evaluated separator",got,err)}
 if _,err:=app.joinSafe(ctx,[]int64{1});err==nil{t.Fatal("safe call lost separator error")}
}
var joinBenchmarkResult string
func BenchmarkStaticJoin(b *testing.B) {
 app:=&App{};ctx:=context.Background()
 for _,n:=range []int{0,1,128}{
  values:=make([]int64,n);for i:=range values{values[i]=int64(i)-64}
  for _,sample:=range []struct{name string;call func(context.Context,[]int64,string)(string,error)}{
   {"generated",app.joinInts},
   {"reference",func(_ context.Context,values []int64,separator string)(string,error){
    var out strings.Builder
    for i,value:=range values{if i!=0{out.WriteString(separator)};var buffer [32]byte;out.Write(strconv.AppendInt(buffer[:0],value,10))}
    return out.String(),nil
   }},
  }{b.Run(sample.name+"/"+strconv.Itoa(n),func(b *testing.B){b.ReportAllocs();for b.Loop(){var err error;joinBenchmarkResult,err=sample.call(ctx,values,",");if err!=nil{b.Fatal(err)}}})}
 }
}

`, StableIDs{})
}

func TestGeneratedJoinEvaluationOrder(t *testing.T) {
	runGeneratedFixture(t, `
fn visit(value: Int): Result<Int> @native
fn joinSource(values: [Int]): [Int] { visit(1)? return values }
fn joinSeparator(): String { visit(2)? return "|" }
fn joinOrdered(values: [Int]): String { return joinSource(values).joinToString(joinSeparator()) }
fn joinCaptured(_collection1Items: [Int], _collection1Separator: String): String {
 return _collection1Items.joinToString(_collection1Separator)
}
fn joinNested(values: [Int]): String { return values.joinToString(values.joinToString("|")) }
`, `package generated
import("context";"errors";"slices";"testing")
type joinResolver struct{calls []int64;failAt int64}
var joinFailure=errors.New("join evaluation failed")
func(r *joinResolver)Visit(_ context.Context,value int64)(int64,error){
 r.calls=append(r.calls,value);if value==r.failAt{return 0,joinFailure};return value,nil
}
func TestEvaluationOrder(t *testing.T){
 for _,values:=range [][]int64{nil,{3,4}}{for _,failAt:=range []int64{0,1,2}{
  r:=&joinResolver{failAt:failAt};app:=&App{Resolver:r}
  value,err:=app.joinOrdered(t.Context(),values)
  calls:=[]int64{1,2};if failAt==1{calls=[]int64{1}}
  if !slices.Equal(r.calls,calls){t.Fatal("evaluation count/order",r.calls,calls)}
  if failAt!=0{if !errors.Is(err,joinFailure){t.Fatal("error lost",err)};continue}
  want:="";if len(values)!=0{want="3|4"};if err!=nil||value!=want{t.Fatal(value,err)}
 }}
 app:=&App{}
 if value,err:=app.joinCaptured(t.Context(),[]int64{3,4},"/");err!=nil||value!="3/4"{t.Fatal(value,err)}
 if value,err:=app.joinNested(t.Context(),[]int64{3,4});err!=nil||value!="33|44"{t.Fatal(value,err)}
}
`, StableIDs{})
}

func TestGeneratedJoinSpecialScalars(t *testing.T) {
	runGeneratedFixture(t, `
enum JoinState { READY DONE }
fn joinStates(values: [JoinState]): String { return values.joinToString("|") }
fn joinDates(values: [DateTime]): String { return values.joinToString("|") }
fn joinDurations(values: [Duration]): String { return values.joinToString("|") }
fn joinUUIDs(values: [UUID]): String { return values.joinToString("|") }
fn joinDecimals(values: [Decimal]): String { return values.joinToString("|") }
`, `package generated
import("testing";"time";"github.com/google/uuid";"github.com/shopspring/decimal")

func TestSpecialJoin(t *testing.T){
 app:=&App{};ctx:=t.Context()
 if got,err:=app.joinStates(ctx,[]JoinState{JoinStateREADY,JoinStateDONE});err!=nil||got!="READY|DONE"{t.Fatal(got,err)}
 date:=time.Unix(0,123456789).UTC()
 if got,err:=app.joinDates(ctx,[]time.Time{date,date});err!=nil||got!=date.String()+"|"+date.String(){t.Fatal(got,err)}
 if got,err:=app.joinDurations(ctx,[]time.Duration{time.Nanosecond,-time.Minute});err!=nil||got!="1ns|-1m0s"{t.Fatal(got,err)}
 id:=uuid.MustParse("531f2d00-20e5-47f2-b874-22b6fc76328b")
 if got,err:=app.joinUUIDs(ctx,[]uuid.UUID{id,id});err!=nil||got!=id.String()+"|"+id.String(){t.Fatal(got,err)}
 amount:=decimal.RequireFromString("12345678901234567890.123456789")
 if got,err:=app.joinDecimals(ctx,[]decimal.Decimal{amount,amount});err!=nil||got!=amount.String()+"|"+amount.String(){t.Fatal(got,err)}
}
`, StableIDs{})
}

func TestGeneratedAnyRuntimeBenchmark(t *testing.T) {
	runGeneratedFixture(t, `fn probe(values: [Int]): Boolean { return values.any { it > 0 } }`, `package generated
import("context";"os";"slices";"testing")

var anyResult bool
func referenceAny(_ context.Context, values []int64)(bool,error){
 for _,value:=range values{if value>0{return true,nil}}
 return false,nil
}
func anyInputs() map[string][]int64 {
 first,last,miss:=make([]int64,128),make([]int64,128),make([]int64,128)
 first[0]=1;last[127]=1
 return map[string][]int64{"empty":nil,"single":{1},"first128":first,"last128":last,"miss128":miss}
}
func TestAnyReferenceAgreement(t *testing.T){
 app:=&App{}
 for name,values:=range anyInputs(){got,err:=app.probe(t.Context(),values);want,_:=referenceAny(t.Context(),values);if err!=nil||got!=want{t.Fatal(name,got,want,err)}}
}
func BenchmarkGeneratedAny(b *testing.B){
 app:=&App{};ctx:=context.Background();inputs:=anyInputs()
 variants:=[]struct{name string;call func(context.Context,[]int64)(bool,error)}{{"generated",app.probe},{"reference",referenceAny}}
 if os.Getenv("LUXO_ANY_BENCH_REVERSE")=="1"{slices.Reverse(variants)}
 for _,name:=range []string{"empty","single","first128","last128","miss128"}{
  values:=inputs[name];want,_:=referenceAny(ctx,values)
  for _,variant:=range variants{b.Run(name+"/"+variant.name,func(b *testing.B){
   b.ReportAllocs();for b.Loop(){var err error;anyResult,err=variant.call(ctx,values);if err!=nil{b.Fatal(err)}}
   if anyResult!=want{b.Fatal("incorrect benchmark result")}
  })}
 }
}
`, StableIDs{})
}

func TestGeneratedDiscardedCollectionParameter(t *testing.T) {
	runGeneratedFixture(t, `
fn mapped(values: [Int]): [Int] { return values.map { _ -> 42 } }
fn filtered(values: [Int]): [Int] { return values.filter { _ -> true } }
fn matched(values: [Int]): Boolean { return values.any { _ -> true } }
fn counted(values: [Int]): Int { var count = 0 values.forEach { _ -> count += 1 } return count }
`, `package generated
import("slices";"testing")

func TestDiscardedParameter(t *testing.T){
 app:=&App{};ctx:=t.Context()
 for _,values:=range [][]int64{nil,{3,5,7}}{
  mapped,err:=app.mapped(ctx,values);want:=make([]int64,len(values));for i:=range want{want[i]=42};if err!=nil||!slices.Equal(mapped,want){t.Fatal(mapped,err)}
  filtered,err:=app.filtered(ctx,values);if err!=nil||!slices.Equal(filtered,values){t.Fatal(filtered,err)}
  matched,err:=app.matched(ctx,values);if err!=nil||matched!=(len(values)>0){t.Fatal(matched,err)}
  count,err:=app.counted(ctx,values);if err!=nil||count!=int64(len(values)){t.Fatal(count,err)}
 }
}
`, StableIDs{})
}

func TestGeneratedStableSortCopy(t *testing.T) {
	runGeneratedFixture(t, `
type Item { id: Int key: Int }
fn ordered(values: [Item]): [Item] { return values.sortBy { it.key } }
`, `package generated
import("testing";"slices")

func TestStableSortCopy(t *testing.T){
 input:=[]Item{{Id:1,Key:2},{Id:2,Key:1},{Id:3,Key:2},{Id:4,Key:1}}
 before:=slices.Clone(input);result,err:=(&App{}).ordered(t.Context(),input)
 if err!=nil{t.Fatal(err)}
 if len(result)!=4||result[0].Id!=2||result[1].Id!=4||result[2].Id!=1||result[3].Id!=3{t.Fatal("unstable or unsorted",result)}
 if !slices.Equal(input,before){t.Fatal("input reordered")}
 result[0].Id=99;if !slices.Equal(input,before){t.Fatal("output aliases input")}
 for _,empty:=range [][]Item{nil,{}}{result,err:=(&App{}).ordered(t.Context(),empty);if err!=nil||len(result)!=0{t.Fatal(result,err)}}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Item": {"id": 1, "key": 2}}})
}

func TestGeneratedSortKeyEvaluation(t *testing.T) {
	runGeneratedFixture(t, `
fn key(value: Int): Result<Int> @native
fn ordered(values: [Int]): [Int] { return values.sortBy { ctx -> key(ctx)? } }
`, `package generated
import("context";"errors";"slices";"testing")
var keyFailure=errors.New("key failed")
type resolver struct{calls []int64}
func(r *resolver)Key(_ context.Context,value int64)(int64,error){r.calls=append(r.calls,value);if value==99{return 0,keyFailure};return value,nil}
func TestKeys(t *testing.T){
 for _,input:=range [][]int64{nil,{}, {3,1,2,1},{3,99,1}}{
  r:=&resolver{};before:=slices.Clone(input);result,err:=(&App{Resolver:r}).ordered(t.Context(),input)
  if !slices.Equal(input,before){t.Fatal("input mutated")}
  if slices.Contains(input,int64(99)){if !errors.Is(err,keyFailure)||len(result)!=0||!slices.Equal(r.calls,[]int64{3,99}){t.Fatal(result,err,r.calls)};continue}
  expected:=slices.Clone(input);slices.Sort(expected)
  if err!=nil||!slices.Equal(result,expected)||!slices.Equal(r.calls,input){t.Fatal(result,err,r.calls)}
 }
}

`, StableIDs{})
}

func TestGeneratedSortScalarKeys(t *testing.T) {
	runGeneratedFixture(t, `
fn floats(values: [Float]): [Float] { return values.sortBy { it } }
fn texts(values: [String]): [String] { return values.sortBy { it } }
fn dates(values: [DateTime]): [DateTime] { return values.sortBy { it } }
fn durations(values: [Duration]): [Duration] { return values.sortBy { it } }
fn decimals(values: [Decimal]): [Decimal] { return values.sortBy { it } }
fn nested(values: [Int]): [Int] { return values.sortBy { values.sortBy { it }.size() + it } }
`, `package generated
import("testing";"math";"slices";"time";"github.com/shopspring/decimal")

func TestSortScalars(t *testing.T){
 app:=&App{};ctx:=t.Context()
 floats,err:=app.floats(ctx,[]float64{1,-1,math.SmallestNonzeroFloat64,math.MaxFloat64});if err!=nil||!slices.IsSorted(floats){t.Fatal(floats,err)}
 texts,err:=app.texts(ctx,[]string{"z","a","中文",""});if err!=nil||!slices.IsSorted(texts){t.Fatal(texts,err)}
 durations,err:=app.durations(ctx,[]time.Duration{time.Hour,-time.Hour,1});if err!=nil||!slices.IsSorted(durations){t.Fatal(durations,err)}
 first:=time.Unix(1,1);second:=time.Unix(1,2)
 dates,err:=app.dates(ctx,[]time.Time{second,first});if err!=nil||len(dates)!=2||!dates[0].Equal(first)||!dates[1].Equal(second){t.Fatal(dates,err)}
 small:=decimal.RequireFromString("9007199254740992.0000000001");large:=decimal.RequireFromString("9007199254740992.0000000002")
 decimals,err:=app.decimals(ctx,[]decimal.Decimal{large,small});if err!=nil||len(decimals)!=2||!decimals[0].Equal(small)||!decimals[1].Equal(large){t.Fatal(decimals,err)}
 nested,err:=app.nested(ctx,[]int64{3,1,2});if err!=nil||!slices.Equal(nested,[]int64{1,2,3}){t.Fatal(nested,err)}
}
func TestSortRejectsNonFinite(t *testing.T){
 for _,value:=range []float64{math.NaN(),math.Inf(1),math.Inf(-1)}{
  input:=[]float64{2,value,1};result,err:=(&App{}).floats(t.Context(),input)
  if err==nil||len(result)!=0{t.Fatalf("nonfinite key accepted: %v %v",result,err)}
  if input[0]!=2||input[2]!=1{t.Fatal("failed sort mutated input")}
 }
}
`, StableIDs{})
}

func TestGeneratedSortFieldHelpers(t *testing.T) {
	runGeneratedTransactionFixture(t, `
fn normalize(values: [Float]): [Float] { return values.sortBy { it } }
model User @noTime { id: Int @id name: [Float] @beforeSave { normalize(it) } @transform { normalize(it) } }
`, `package generated
import("testing";"math";"slices";"errors";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestSortHelpers(t *testing.T){
 input:=[]float64{3,1,2};got,err:=prepareSave4UserName(input)
 if err!=nil||!slices.Equal(got,[]float64{1,2,3})||!slices.Equal(input,[]float64{3,1,2}){t.Fatal(got,err,input)}
 if _,err:=prepareSave4UserName([]float64{math.NaN()});!errors.Is(err,codec.ErrNonFiniteFloat){t.Fatal(err)}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)}
 user:=&User{Id:1,Name:input};out:=&api.ResponseBuf{}
 if err:=user.WriteLuxo(out,mask);err!=nil{t.Fatal(err)}
 dec:=codec.NewDecoder(out.B);var row User;row.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)}
 if !slices.Equal(row.Name,[]float64{1,2,3})||!slices.Equal(user.Name,input){t.Fatal(row.Name,user.Name)}
 user.Name=[]float64{math.Inf(1)};out.B=out.B[:0]
 if err:=user.WriteLuxo(out,mask);!errors.Is(err,codec.ErrNonFiniteFloat){t.Fatal(err)}
}
`)
}

func TestGeneratedSortComposition(t *testing.T) {
	runGeneratedTransactionFixture(t, `
model User @noTime { id: Int @id name: String }
fn source(values: [Int]): Result<[Int]> @native
fn key(value: Int): Result<Int> @native
fn sourced(values: [Int]): [Int] { return (source(values)?).sortBy { key(it)? } }
fn nullable(values: [Int]?): [Int]? { return values?.sortBy { key(it)? } }
fn discarded(values: [Int]): [Int] { return values.sortBy { _ -> 1 } }
fn captured(values: [Int]): [Int] {
 val _collection1Items = 8
 return values.sortBy { ctx -> _collection1Items - ctx }
}
fn models(values: [User]): [User] { return values.sortBy { it.name } }
`, `package generated
import("context";"errors";"slices";"testing")
var sortFailure=errors.New("sort failure")
type sortResolver struct{sourceCalls int;keys []int64;failSource bool}
func(r *sortResolver)Source(_ context.Context,values []int64)([]int64,error){r.sourceCalls++;if r.failSource{return nil,sortFailure};return values,nil}
func(r *sortResolver)Key(_ context.Context,value int64)(int64,error){r.keys=append(r.keys,value);if value==99{return 0,sortFailure};return value,nil}
func TestSortComposition(t *testing.T){
 ctx:=t.Context()
 for _,failSource:=range []bool{false,true}{
  r:=&sortResolver{failSource:failSource};app:=&App{Resolver:r}
  got,err:=app.sourced(ctx,[]int64{3,1,2})
  if r.sourceCalls!=1{t.Fatal(r.sourceCalls)}
  if failSource{if !errors.Is(err,sortFailure)||len(got)!=0||len(r.keys)!=0{t.Fatal(got,err,r.keys)}}else if err!=nil||!slices.Equal(got,[]int64{1,2,3})||!slices.Equal(r.keys,[]int64{3,1,2}){t.Fatal(got,err,r.keys)}
 }
 r:=&sortResolver{};app:=&App{Resolver:r}
 if got,err:=app.nullable(ctx,nil);got!=nil||err!=nil||len(r.keys)!=0{t.Fatal(got,err,r.keys)}
 if got,err:=app.nullable(ctx,[]int64{});got==nil||len(got)!=0||err!=nil{t.Fatal(got,err)}
 if got,err:=app.nullable(ctx,[]int64{3,1});err!=nil||!slices.Equal(got,[]int64{1,3}){t.Fatal(got,err)}
 if got,err:=app.nullable(ctx,[]int64{3,99,1});!errors.Is(err,sortFailure)||got!=nil{t.Fatal(got,err)}
 input:=[]int64{3,1,2};got,err:=app.discarded(ctx,input);if err!=nil||!slices.Equal(got,input){t.Fatal(got,err)}
 got[0]=42;if input[0]!=3{t.Fatal("slice alias")}
 if got,err:=app.captured(ctx,input);err!=nil||!slices.Equal(got,[]int64{3,2,1}){t.Fatal(got,err)}
 first,second,third:=&User{Id:1,Name:"b"},&User{Id:2,Name:"a"},&User{Id:3,Name:"b"}
 users:=[]*User{first,second,third};ordered,err:=app.models(ctx,users)
 if err!=nil||!slices.Equal(ordered,[]*User{second,first,third})||!slices.Equal(users,[]*User{first,second,third}){t.Fatal(ordered,err)}
 ordered[0]=third;if users[0]!=first{t.Fatal("model slice alias")}
}
`)
}

func TestGeneratedSortBenchmark(t *testing.T) {
	runGeneratedFixture(t, `
fn sortedInts(values: [Int]): [Int] { return values.sortBy { it } }
fn sortedFloats(values: [Float]): [Float] { return values.sortBy { it } }
`, `package generated
import("context";"fmt";"slices";"testing";"github.com/light-speak/luxo/pkg/lux/codec")

type intSortEntry struct{value,key int64}
type floatSortEntry struct{value,key float64}
func referenceInts(_ context.Context,values []int64)([]int64,error){
 entries:=make([]intSortEntry,len(values));for i,value:=range values{entries[i]=intSortEntry{value,value}}
 slices.SortStableFunc(entries,func(a,b intSortEntry)int{if a.key<b.key{return -1};if a.key>b.key{return 1};return 0})
 result:=make([]int64,len(entries));for i:=range entries{result[i]=entries[i].value};return result,nil
}
func referenceFloats(_ context.Context,values []float64)([]float64,error){
 entries:=make([]floatSortEntry,len(values));for i,value:=range values{if !codec.IsFiniteFloat(value){return nil,codec.ErrNonFiniteFloat};entries[i]=floatSortEntry{value,value}}
 slices.SortStableFunc(entries,func(a,b floatSortEntry)int{if a.key<b.key{return -1};if a.key>b.key{return 1};return 0})
 result:=make([]float64,len(entries));for i:=range entries{result[i]=entries[i].value};return result,nil
}
var sortIntsResult []int64
var sortFloatsResult []float64
func BenchmarkGeneratedSort(b *testing.B){
 app:=&App{};ctx:=context.Background()
 for _,size:=range []int{0,1,128}{
  ints:=make([]int64,size);floats:=make([]float64,size)
  for i:=range ints{ints[i]=int64((i*37)%17);floats[i]=float64(ints[i])/3}
  for _,variant:=range []struct{name string;call func(context.Context,[]int64)([]int64,error)}{{"generated",app.sortedInts},{"reference",referenceInts}}{
   got,err:=variant.call(ctx,ints);if err!=nil||len(got)!=size||!slices.IsSorted(got){b.Fatal(got,err)}
   b.Run(fmt.Sprintf("int/%d/%s",size,variant.name),func(b *testing.B){b.ReportAllocs();for b.Loop(){var err error;sortIntsResult,err=variant.call(ctx,ints);if err!=nil{b.Fatal(err)}}})
  }
  for _,variant:=range []struct{name string;call func(context.Context,[]float64)([]float64,error)}{{"generated",app.sortedFloats},{"reference",referenceFloats}}{
   got,err:=variant.call(ctx,floats);if err!=nil||len(got)!=size||!slices.IsSorted(got){b.Fatal(got,err)}
   b.Run(fmt.Sprintf("float/%d/%s",size,variant.name),func(b *testing.B){b.ReportAllocs();for b.Loop(){var err error;sortFloatsResult,err=variant.call(ctx,floats);if err!=nil{b.Fatal(err)}}})
  }
 }
}
`, StableIDs{})
}

func TestGeneratedNumericSum(t *testing.T) {
	runGeneratedFixture(t, `
fn ints(values: [Int]): Int { return values.sumOf { it } }
fn floats(values: [Float]): Float { return values.sumOf { it } }
fn decimals(values: [Decimal]): Decimal { return values.sumOf { it } }
fn nullable(values: [Int]?): Int? { return values?.sumOf { it } }
fn mapped(values: [Int]): Int { return values.sumOf { value -> if value < 0 { return 0 } value } }
`, `package generated
import("math";"testing";"github.com/shopspring/decimal")

func TestNumericSum(t *testing.T){
 app:=&App{};ctx:=t.Context()
 for _,sample:=range []struct{values []int64;want int64;fail bool}{
  {nil,0,false},{[]int64{},0,false},{[]int64{9007199254740993,2},9007199254740995,false},
  {[]int64{math.MaxInt64,0},math.MaxInt64,false},{[]int64{math.MinInt64,0},math.MinInt64,false},
  {[]int64{math.MaxInt64,1},0,true},{[]int64{math.MinInt64,-1},0,true},
  {[]int64{math.MaxInt64,-math.MaxInt64},0,false},{[]int64{math.MaxInt64,1,-1},0,true},
 }{got,err:=app.ints(ctx,sample.values);if (err!=nil)!=sample.fail||got!=sample.want{t.Fatal(sample,got,err)}}
 for _,sample:=range []struct{values []float64;want float64;fail bool}{
  {nil,0,false},{[]float64{1.5,-0.25},1.25,false},{[]float64{math.SmallestNonzeroFloat64},math.SmallestNonzeroFloat64,false},
  {[]float64{math.NaN()},0,true},{[]float64{math.Inf(1)},0,true},{[]float64{math.Inf(-1)},0,true},
  {[]float64{math.MaxFloat64,math.MaxFloat64},0,true},
 }{got,err:=app.floats(ctx,sample.values);if (err!=nil)!=sample.fail||got!=sample.want{t.Fatal(sample,got,err)}}
 for _,values:=range [][]decimal.Decimal{nil,{decimal.RequireFromString("9007199254740993.0000000001"),decimal.RequireFromString("0.0000000002")}}{
  want:=decimal.Zero;for _,value:=range values{want=want.Add(value)}
  got,err:=app.decimals(ctx,values);if err!=nil||!got.Equal(want){t.Fatal(got,err,want)}
 }
 if got,err:=app.nullable(ctx,nil);got!=nil||err!=nil{t.Fatal(got,err)}
 if got,err:=app.nullable(ctx,[]int64{});got==nil||*got!=0||err!=nil{t.Fatal(got,err)}
 if got,err:=app.mapped(ctx,[]int64{-1,2,3});err!=nil||got!=5{t.Fatal(got,err)}
}
`, StableIDs{})
}

func TestGeneratedSumFailureAndWire(t *testing.T) {
	runGeneratedTransactionFixture(t, `
model User @noTime { id: Int @id name: Int @beforeSave { total([it, 1]) } }
fn total(values: [Int]): Int { return values.sumOf { it } }
fn select(value: Int): Result<Int> @native
api sum(values: [Int]): Int { return values.sumOf { select(it)? } }
`, `package generated
import("testing";"context";"errors";"math";"slices";"bytes";"encoding/json";"net/http/httptest";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
var selectorFailure=errors.New("selector failed")
type sumResolver struct{calls []int64}
func(r *sumResolver)Select(_ context.Context,value int64)(int64,error){r.calls=append(r.calls,value);if value==99{return 0,selectorFailure};return value,nil}
func TestSumWire(t *testing.T){
 for _,binary:=range []bool{false,true}{for _,sample:=range []struct{values,calls []int64;want int64;status int}{
  {[]int64{},nil,0,200},{[]int64{9007199254740993,2},[]int64{9007199254740993,2},9007199254740995,200},
  {[]int64{1,99,2},[]int64{1,99},0,500},{[]int64{math.MaxInt64,1,2},[]int64{math.MaxInt64,1},0,400},
 }{
  resolver:=&sumResolver{};router:=api.NewRouter()
  if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
  if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
  if err:=RegisterHandlers(router,&App{Resolver:resolver});err!=nil{t.Fatal(err)}
  if err:=router.Prepare();err!=nil{t.Fatal(err)}
  params:=map[string]any{"values":sample.values};var body []byte;var err error
  if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs["sum"].ID,nil,params,router.Registry.ParamOrder("sum"))}else{params["$api"]="sum";body,err=json.Marshal(params)}
  if err!=nil{t.Fatal(err)}
  request:=httptest.NewRequest("POST","/",bytes.NewReader(body));request.Header.Set("Content-Type","application/json")
  if binary{request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if response.Code!=sample.status||!slices.Equal(resolver.calls,sample.calls){t.Fatalf("binary=%v code=%d body=%s calls=%v",binary,response.Code,response.Body.String(),resolver.calls)}
  if sample.status!=200{continue}
  var got int64
  if binary{decoder:=codec.NewDecoder(response.Body.Bytes());got=decoder.ReadInt();if err:=decoder.Finish();err!=nil{t.Fatal(err)}}else{var envelope struct{Data int64};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil{t.Fatal(err)};got=envelope.Data}
  if got!=sample.want{t.Fatal(got,sample.want)}
 }}
}
func TestSumSaveHelper(t *testing.T){
 if got,err:=prepareSave4UserName(9007199254740993);err!=nil||got!=9007199254740994{t.Fatal(got,err)}
 if got,err:=prepareSave4UserName(math.MaxInt64);err==nil||got!=0{t.Fatal(got,err)}
}
`)
}

func TestGeneratedDiscardedCollectionResults(t *testing.T) {
	runGeneratedFixture(t, `
fn key(value: Int): Result<Int> @native
fn visit(values: [Int]): Int {
 values.map { key(it)? }
 values.filter { key(it)? > 0 }
 values.any { key(it)? > 0 }
 values.sortBy { key(it)? }
 values.sumOf { key(it)? }
 values.joinToString(",")
 return 42
}
fn safe(values: [Int]?): Int { values?.sumOf { key(it)? } return 42 }
`, `package generated
import("testing";"context";"errors";"slices")
var failure=errors.New("key failed")
type resolver struct{calls []int64;fail bool}
func(r *resolver)Key(_ context.Context,value int64)(int64,error){r.calls=append(r.calls,value);if r.fail{return 0,failure};return value,nil}
func TestDiscardedResults(t *testing.T){
 for _,fail:=range []bool{false,true}{
  r:=&resolver{fail:fail};app:=&App{Resolver:r};got,err:=app.visit(t.Context(),[]int64{1,2})
  if fail{if !errors.Is(err,failure)||got!=0||!slices.Equal(r.calls,[]int64{1}){t.Fatal(got,err,r.calls)}}else if err!=nil||got!=42||!slices.Equal(r.calls,[]int64{1,2,1,2,1,1,2,1,2}){t.Fatal(got,err,r.calls)}
 }
 r:=&resolver{fail:true};app:=&App{Resolver:r}
 if got,err:=app.safe(t.Context(),nil);err!=nil||got!=42||len(r.calls)!=0{t.Fatal(got,err,r.calls)}
 if got,err:=app.safe(t.Context(),[]int64{1});!errors.Is(err,failure)||got!=0{t.Fatal(got,err)}
}
`, StableIDs{})
}

func TestGeneratedNestedCollectionFailures(t *testing.T) {
	runGeneratedFixture(t, `
fn key(value: Int): Result<Int> @native
fn nestedSum(values: [Int]): Int { return values.sumOf { outer -> values.sumOf { inner -> key(outer + inner)? } } }
fn nestedSort(values: [Int]): [Int] { return values.sortBy { outer -> values.sumOf { inner -> key(outer + inner)? } } }
fn nestedMap(values: [Int]): [Int] { return values.map { outer -> values.sumOf { inner -> key(outer + inner)? } } }
fn nestedFilter(values: [Int]): [Int] { return values.filter { outer -> values.sumOf { inner -> key(outer + inner)? } > 5 } }
fn nestedAny(values: [Int]): Boolean { return values.any { outer -> values.sumOf { inner -> key(outer + inner)? } > 5 } }
`, `package generated
import("context";"errors";"slices";"testing")
var nestedFailure=errors.New("nested selector failed")
type nestedResolver struct{calls []int64;failAt int}
func(r *nestedResolver)Key(_ context.Context,value int64)(int64,error){r.calls=append(r.calls,value);if len(r.calls)-1==r.failAt{return 0,nestedFailure};return value,nil}
func TestNestedCollections(t *testing.T){
 r:=&nestedResolver{failAt:-1};app:=&App{Resolver:r}
 type call func(context.Context,[]int64)([]int64,error)
 cases:=[]struct{name string;run call;want,calls,empty []int64}{
  {"sum",func(ctx context.Context,v []int64)([]int64,error){n,e:=app.nestedSum(ctx,v);if e!=nil{return nil,e};return []int64{n},nil},[]int64{12},[]int64{4,3,3,2},[]int64{0}},
  {"sort",app.nestedSort,[]int64{1,2},[]int64{4,3,3,2},nil},
  {"map",app.nestedMap,[]int64{7,5},[]int64{4,3,3,2},nil},
  {"filter",app.nestedFilter,[]int64{2},[]int64{4,3,3,2},nil},
  {"any",func(ctx context.Context,v []int64)([]int64,error){b,e:=app.nestedAny(ctx,v);if e!=nil{return nil,e};var n int64;if b{n=1};return []int64{n},nil},[]int64{1},[]int64{4,3},[]int64{0}},
 }
 for _,sample:=range cases{t.Run(sample.name,func(t *testing.T){
  for failAt:=-1;failAt<len(sample.calls);failAt++{
   r.calls=nil;r.failAt=failAt;input:=[]int64{2,1};got,err:=sample.run(t.Context(),input)
   if !slices.Equal(input,[]int64{2,1}){t.Fatal("input mutated",input)}
   if failAt<0{if err!=nil||!slices.Equal(got,sample.want)||!slices.Equal(r.calls,sample.calls){t.Fatal(got,err,r.calls)}
   }else if !errors.Is(err,nestedFailure)||len(got)!=0||!slices.Equal(r.calls,sample.calls[:failAt+1]){t.Fatal(failAt,got,err,r.calls)}
  }
  r.calls=nil;r.failAt=0;got,err:=sample.run(t.Context(),nil)
  if err!=nil||!slices.Equal(got,sample.empty)||len(r.calls)!=0{t.Fatal("empty",got,err,r.calls)}
 })}
}
`, StableIDs{})
}

func TestGeneratedSumBenchmark(t *testing.T) {
	runGeneratedFixture(t, `
fn ints(values: [Int]): Int { return values.sumOf { it } }
fn floats(values: [Float]): Float { return values.sumOf { it } }
fn decimals(values: [Decimal]): Decimal { return values.sumOf { it } }
`, `package generated
import("context";"fmt";"math";"os";"slices";"testing";"github.com/shopspring/decimal";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/errors")

func referenceIntSum(_ context.Context,values []int64)(int64,error){
 var result int64
 for _,value:=range values{
  if (value>0 && result>math.MaxInt64-value)||(value<0 && result<math.MinInt64-value){return 0,errors.BadRequest.WithData(&errors.ParamError{Param:"sumOf",Error:"Int64 sum overflow"})}
  result+=value
 }
 return result,nil
}
func referenceFloatSum(_ context.Context,values []float64)(float64,error){
 var result float64
 for _,value:=range values{result+=value;if !codec.IsFiniteFloat(result){return 0,codec.ErrNonFiniteFloat}}
 return result,nil
}
func referenceDecimalSum(_ context.Context,values []decimal.Decimal)(decimal.Decimal,error){
 var result decimal.Decimal
 for _,value:=range values{result=result.Add(value)}
 return result,nil
}
func benchmarkSum[T any](b *testing.B,name string,values []T,generated,reference func(context.Context,[]T)(T,error),equal func(T,T)bool){
 ctx:=context.Background();want,err:=reference(ctx,values);if err!=nil{b.Fatal(err)}
 variants:=[]struct{name string;call func(context.Context,[]T)(T,error)}{{"generated",generated},{"reference",reference}}
 if os.Getenv("LUXO_SUM_BENCH_REVERSE")=="1"{slices.Reverse(variants)}
 for _,variant:=range variants{
  got,err:=variant.call(ctx,values);if err!=nil||!equal(got,want){b.Fatal("incorrect sum",got,want,err)}
  b.Run(name+"/"+variant.name,func(b *testing.B){
   var got T;b.ReportAllocs()
   for b.Loop(){var err error;got,err=variant.call(ctx,values);if err!=nil{b.Fatal(err)}}
   if !equal(got,want){b.Fatal("incorrect benchmark sum",got,want)}
  })
 }
}
func BenchmarkGeneratedSum(b *testing.B){
 app:=&App{}
 for _,size:=range []int{0,1,128}{
  ints:=make([]int64,size);floats:=make([]float64,size);decimals:=make([]decimal.Decimal,size)
  for i:=range ints{ints[i]=int64(i%17)-8;floats[i]=float64(ints[i])/4;decimals[i]=decimal.New(ints[i],-4)}
  benchmarkSum(b,fmt.Sprintf("int/%d",size),ints,app.ints,referenceIntSum,func(a,b int64)bool{return a==b})
  benchmarkSum(b,fmt.Sprintf("float/%d",size),floats,app.floats,referenceFloatSum,func(a,b float64)bool{return a==b})
  benchmarkSum(b,fmt.Sprintf("decimal/%d",size),decimals,app.decimals,referenceDecimalSum,func(a,b decimal.Decimal)bool{return a.Equal(b)})
 }
}
`, StableIDs{})
}

func TestGeneratedSortPackageNameBindings(t *testing.T) {
	runGeneratedFixture(t, `
fn ordered(slices: [Int]): [Int] { return slices.sortBy { it } }
fn localOrdered(values: [Int]): [Int] {
    val slices = values
    return slices.sortBy { it }
}
fn nestedOrdered(values: [Int]): [Int] {
    return values.map { slices -> values.sortBy { it }.sumOf { it } + slices }
}
`, `package generated
import("testing";"slices")

func TestSortBindings(t *testing.T) {
 app:=&App{}
 input:=[]int64{3,1,2}
 for _,call:=range []func()([]int64,error){
  func()([]int64,error){return app.ordered(t.Context(),input)},
  func()([]int64,error){return app.localOrdered(t.Context(),input)},
 } {
  got,err:=call();if err!=nil||!slices.Equal(got,[]int64{1,2,3}){t.Fatal(got,err)}
 }
 got,err:=app.nestedOrdered(t.Context(),input)
 if err!=nil||!slices.Equal(got,[]int64{9,7,8}){t.Fatal(got,err)}
 if !slices.Equal(input,[]int64{3,1,2}){t.Fatal("input mutated")}
}
`, StableIDs{})
}
