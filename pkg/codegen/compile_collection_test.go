package codegen

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

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
 for _,fn:=range []func([]int64)([]*User,error){
  func(values []int64)([]*User,error){return app.mapModel(ctx,values)},
  func(values []int64)([]*User,error){return app.mapModelLocal(ctx,values)},
  func(values []int64)([]*User,error){return app.mapModelWhen(ctx,values)},
 }{
  users,err:=fn(input);if err!=nil||len(users)!=len(input){t.Fatal(users,err)}
  for i,user:=range users{if user==nil||user.Id!=input[i]{t.Fatal(users)}}
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
type App struct{ Resolver NativeResolver }
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
 }{t.Run(fmt.Sprintf("%s/%v",sample.name,binary),func(t *testing.T){
  testCollectionRequest(t,sample.name,binary,[]int64{-1,2,7},sample.want,0)
  testCollectionRequest(t,sample.name,binary,[]int64{},[]int64{},0)
  if sample.name=="fallibleMapped"||sample.name=="fallibleFiltered"{testCollectionRequest(t,sample.name,binary,[]int64{-1,2,7},nil,2)}
  if sample.name=="sourceMapped"{for _,failAt:=range []int64{2,99}{testCollectionRequest(t,sample.name,binary,[]int64{-1,2,7},nil,failAt)}}
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
 if name=="fallibleMapped"||name=="fallibleFiltered"||name=="sourceMapped"{
  calls:=values;if failAt!=0{calls=[]int64{-1,2}}
  if name=="sourceMapped"{calls=append([]int64{99},calls...);if failAt==99{calls=[]int64{99}}}
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
