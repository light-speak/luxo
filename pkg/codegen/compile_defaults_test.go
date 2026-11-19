package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestModelIdentityOperandRepresentation(t *testing.T) {
	for _, sample := range []struct {
		name, want string
		list       bool
		value      bool
	}{
		{"reference", "item", false, false},
		{"stored-value", "&item", false, true},
		{"list", "item", true, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			c := newCompiler(map[string]*ast.ModelDecl{"Item": {Name: "Item"}})
			c.vars["item"] = valType{name: "Item", isModel: true, isValue: sample.value, isList: sample.list}
			expr := &ast.Ident{Name: "item"}
			expr.SetTypeTag("Item")
			expr.SetListType(sample.list)
			if got := c.compileBooleanValue(expr); got != sample.want {
				t.Fatalf("operand = %q, want %q", got, sample.want)
			}
		})
	}
}

func TestGeneratedModelIdentityEquality(t *testing.T) {
	runGeneratedFixture(t, `
model Item @noTime { id: Int @id value: Int }
fn literalEqual(): Boolean { return Item { id: 1, value: 1 } == Item { id: 1, value: 1 } }
fn literalUnequal(): Boolean { return Item { id: 1, value: 1 } != Item { id: 1, value: 1 } }
fn localEqual(): Boolean { val left = Item { id: 1, value: 1 } val right = Item { id: 1, value: 1 } return left == right }
fn sameEqual(): Boolean { val item = Item { id: 1, value: 1 } val alias = item return item == alias }
`, `package generated
import "testing"
func TestModelIdentity(t *testing.T) {
 app:=&App{};ctx:=t.Context()
 for _,sample:=range []struct{name string;call func()(bool,error);want bool}{
  {"literal",func()(bool,error){return app.literalEqual(ctx)},false},
  {"literal-not-equal",func()(bool,error){return app.literalUnequal(ctx)},true},
  {"distinct-locals",func()(bool,error){return app.localEqual(ctx)},false},
  {"alias",func()(bool,error){return app.sameEqual(ctx)},true},
 }{t.Run(sample.name,func(t *testing.T){got,err:=sample.call();if err!=nil||got!=sample.want{t.Fatal(got,err,"want",sample.want)}})}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Item": {"id": 1, "value": 2}}})
}

func TestGeneratedModelIdentityBoundaries(t *testing.T) {
	runGeneratedFixture(t, `
model Item @noTime { id: Int @id values: [Int] }
type Snapshot { value: Int }
fn equal(left: Item?, right: Item?): Boolean { return left == right }
fn different(left: Item?, right: Item?): Boolean { return left != right }
fn literalLeft(item: Item): Boolean { return Item { id: 1, values: [1] } == item }
fn literalRight(item: Item): Boolean { return item == Item { id: 1, values: [1] } }
fn literalNull(): Boolean { return Item { id: 1, values: [1] } == null }
fn forward(item: Item): Item { return item }
fn literalThenCall(item: Item): Boolean { return Item { id: 1, values: [1] } == forward(item) }
fn matchLiteral(): Boolean { return when Item { id: 1, values: [1] } { Item { id: 1, values: [1] } -> true else -> false } }
fn matchInput(item: Item): Boolean { return when item { Item { id: 1, values: [1] } -> true else -> false } }
fn matchSame(item: Item): Boolean { return when item { forward(item) -> true else -> false } }
fn typeValues(): Boolean { return Snapshot { value: 1 } == Snapshot { value: 1 } }
`, `package generated
import "testing"
func TestModelIdentityBoundaries(t *testing.T) {
 app:=&App{};ctx:=t.Context();item:=&Item{Id:1,Values:[]int64{1}};other:=&Item{Id:1,Values:[]int64{1}}
 for _,pair:=range []struct{left,right *Item;equal bool}{{item,item,true},{item,other,false},{nil,nil,true},{item,nil,false},{nil,item,false}}{
  got,err:=app.equal(ctx,pair.left,pair.right);if err!=nil||got!=pair.equal{t.Fatal("equal",got,err)}
  got,err=app.different(ctx,pair.left,pair.right);if err!=nil||got==pair.equal{t.Fatal("different",got,err)}
 }
 for _,call:=range []func()(bool,error){func()(bool,error){return app.literalLeft(ctx,item)},func()(bool,error){return app.literalRight(ctx,item)},func()(bool,error){return app.literalNull(ctx)},func()(bool,error){return app.literalThenCall(ctx,item)},func()(bool,error){return app.matchLiteral(ctx)},func()(bool,error){return app.matchInput(ctx,item)}}{
  if got,err:=call();err!=nil||got{t.Fatal("distinct identity",got,err)}
 }
 if got,err:=app.matchSame(ctx,item);err!=nil||!got{t.Fatal("same match",got,err)}
 if got,err:=app.typeValues(ctx);err!=nil||!got{t.Fatal("type value semantics",got,err)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Item": {"id": 1, "values": 2}, "Snapshot": {"value": 1}}})
}

func TestGeneratedModelIdentityEvaluation(t *testing.T) {
	runGeneratedFixture(t, `
model Item @noTime { id: Int @id }
fn next(value: Int): Result<Int> @native
fn ordered(): Boolean { return Item { id: next(1)? } == Item { id: next(2)? } }
fn statement(item: Item): Int { var result = 0 when item { Item { id: 1 } -> { result = 1 } item -> { result = 2 } else -> { result = 3 } } return result }
fn yielded(item: Item): Int? { return for value in [7] { when item { Item { id: 1 } -> yield 1 item -> yield value else -> null } } }
api identity(left: Item, right: Item): Boolean { return left == right }
api alias(item: Item): Boolean { val same = item return same == item }
api literal(): Boolean { return Item { id: 1 } == Item { id: 1 } }
`, `package generated
import("bytes";"context";"encoding/json";"errors";"fmt";"net";"net/http/httptest";"slices";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/rpc")
type identityNative struct{calls []int64;fail int64;failure error}
func(r *identityNative)Next(_ context.Context,value int64)(int64,error){r.calls=append(r.calls,value);if value==r.fail{return 0,r.failure};return 1,nil}
func TestIdentityEvaluation(t *testing.T){
 failure:=errors.New("operand failed")
 for _,fail:=range []int64{0,1,2}{resolver:=&identityNative{fail:fail,failure:failure};app:=&App{Resolver:resolver};got,err:=app.ordered(t.Context());want:=[]int64{1,2};if fail==1{want=[]int64{1}};if got||!slices.Equal(resolver.calls,want)||(fail==0&&err!=nil)||(fail!=0&&!errors.Is(err,failure)){t.Fatal(got,err,resolver.calls)}}
 app:=&App{};item:=&Item{Id:1};if got,err:=app.statement(t.Context(),item);err!=nil||got!=2{t.Fatal("statement",got,err)}
 if got,err:=app.yielded(t.Context(),item);err!=nil||got==nil||*got!=7{t.Fatal("yield",got,err)}
}
func TestIdentityProtocols(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}();defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}();client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,sample:=range []struct{name string;want bool}{{"identity",false},{"alias",true},{"literal",false}}{for mode:=0;mode<3;mode++{t.Run(fmt.Sprintf("%s/mode=%d",sample.name,mode),func(t *testing.T){
  params:=map[string]any{};for _,param:=range router.Registry.ParamOrder(sample.name){params[param.Name]=api.BinaryMessage([]byte{0,1,2,0})};definition:=router.Schema.APIs[sample.name];body,err:=api.EncodeBinaryRequest(definition.ID,nil,params,router.Registry.ParamOrder(sample.name));if err!=nil{t.Fatal(err)}
  if mode==2{_,off:=codec.ReadVarint(body,0);size,n:=codec.ReadVarint(body,off);off+=n+int(size);body,err=client.CallWithMaskContext(t.Context(),"",definition.ID,nil,body[off:]);if err!=nil{t.Fatal(err)}}else{
   if mode==0{input:=map[string]any{"$api":sample.name};for _,param:=range router.Registry.ParamOrder(sample.name){input[param.Name]=map[string]int64{"id":1}};body,err=json.Marshal(input);if err!=nil{t.Fatal(err)}}
   request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));if mode==1{request.Header.Set("X-Luxo-Mode","binary")};response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatalf("status=%d body=%s",response.Code,response.Body)};body=response.Body.Bytes()
  }
  if mode==0{var result struct{Data bool};if err:=json.Unmarshal(body,&result);err!=nil||result.Data!=sample.want{t.Fatalf("JSON identity mismatch: %s %v",body,err)}}else{decoder:=codec.NewDecoder(body);if got:=decoder.ReadBool();got!=sample.want||decoder.Finish()!=nil{t.Fatalf("Binary identity mismatch: %x",body)}}
 })}}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Item": {"id": 1}}})
}

func TestCollectObjectDefaultsKeepsSourceAndModelProjections(t *testing.T) {
	field := &ast.FieldDecl{Name: "value", Type: &ast.TypeRef{Name: "Int"}, Default: &ast.Literal{Kind: token.Int, Value: "42"}}
	files := []*ast.File{{
		Models: []*ast.ModelDecl{{Name: "Partial", Fields: []*ast.FieldDecl{field}}},
		Types: []*ast.TypeDecl{{Name: "Snapshot", Fields: []*ast.FieldDecl{
			field, {Name: "absent", Type: field.Type}, {Name: "computed", Type: field.Type, Default: field.Default, Computed: &ast.ComputedField{}}, {Name: "untyped", Default: field.Default},
		}}},
	}}
	got := collectObjectDefaults(files)
	if len(got) != 1 || len(got["Snapshot"]) != 1 || got["Snapshot"][0] != field || len(files[0].Types[0].Fields) != 4 {
		t.Fatal("default preparation changed declarations or model projections", got)
	}
	if collectObjectDefaults([]*ast.File{{Models: files[0].Models}}) != nil {
		t.Fatal("partial model data acquired constructor defaults")
	}
	for _, c := range []*compiler{newCompiler(nil), {generator: defaultGenerator()}} {
		if got := c.compileObjectDefaults("Missing", nil, []string{"Value: 0"}); len(got) != 1 || got[0] != "Value: 0" {
			t.Fatal("fragment compilation invented a default", got)
		}
	}
}

func TestGeneratedRichTypeDefaults(t *testing.T) {
	runGeneratedFixture(t, richTypeDefaultSource, richTypeDefaultAssertions, richTypeDefaultIDs())
}

func TestGeneratedModelAliasSemantics(t *testing.T) {
	runGeneratedFixture(t, modelAliasSource, modelAliasAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"Item": {"id": 1, "value": 2}, "Value": {"value": 1}, "Holder": {"item": 1},
	}})
}

func TestGeneratedModelBindingPerformance(t *testing.T) {
	runGeneratedFixture(t, modelBindingPerformanceSource, modelBindingPerformanceAssertions, StableIDs{ModelFields: map[string]map[string]int{"Entry": {"id": 1, "value": 2}}})
}

const modelBindingPerformanceSource = `
model Entry @noTime { id: Int @id value: Int }
fn scalarControl(): Int { return 1 }
fn readLocal(): Int { val item = Entry { id: 1, value: 1 } return item.value }
fn readAlias(): Int { val item = Entry { id: 1, value: 1 } val alias = item return alias.value }
fn returnLocal(): Entry { val item = Entry { id: 1, value: 1 } return item }
fn readAwait(): Int { val item = await { Entry { id: 1, value: 1 } } return item.value }
api plainScalar(): Int { return 1 }
api plainInput(item: Entry): Int { return item.value }
api aliasInput(item: Entry): Int { val alias = item return alias.value }
`

const modelBindingPerformanceAssertions = `package generated
import("bytes";"context";"fmt";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api")
var modelBindingSink int64
func BenchmarkModelBindingFunctions(b *testing.B){
 app:=&App{};ctx:=context.Background()
 for _,sample:=range []struct{name string;call func()(int64,error)}{
  {"scalar",func()(int64,error){return app.scalarControl(ctx)}},
  {"local",func()(int64,error){return app.readLocal(ctx)}},
  {"alias",func()(int64,error){return app.readAlias(ctx)}},
  {"return",func()(int64,error){value,err:=app.returnLocal(ctx);if err!=nil{return 0,err};return value.Value,nil}},
  {"await",func()(int64,error){return app.readAwait(ctx)}},
 }{b.Run(sample.name,func(b *testing.B){b.ReportAllocs();for b.Loop(){value,err:=sample.call();if err!=nil||value!=1{b.Fatal(value,err)};modelBindingSink=value}})}
}
func BenchmarkModelBindingDispatch(b *testing.B){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{b.Fatal(err)};if err:=RegisterHandlers(router,&App{});err!=nil{b.Fatal(err)};if err:=router.Prepare();err!=nil{b.Fatal(err)}
 for _,name:=range []string{"plainScalar","plainInput","aliasInput"}{for _,binary:=range []bool{false,true}{b.Run(fmt.Sprintf("%s/binary=%v",name,binary),func(b *testing.B){
  params:=map[string]any{};if name!="plainScalar"{params["item"]=api.BinaryMessage([]byte{0,1,2,2,2,0})}
  definition:=router.Schema.APIs[name];body,err:=api.EncodeBinaryRequest(definition.ID,nil,params,router.Registry.ParamOrder(name));if err!=nil{b.Fatal(err)}
  if !binary{body=[]byte(fmt.Sprintf("{\"$api\":%q}",name));if name!="plainScalar"{body=[]byte(fmt.Sprintf("{\"$api\":%q,\"item\":{\"id\":1,\"value\":1}}",name))}}
  b.ReportAllocs();b.ResetTimer();for b.Loop(){request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));if binary{request.Header.Set("X-Luxo-Mode","binary")};response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{b.Fatal(response.Code,response.Body.String())};if binary{if !bytes.Equal(response.Body.Bytes(),[]byte{2}){b.Fatal(response.Body.String())}}else if response.Body.String()!="{\"data\":1}"{b.Fatal(response.Body.String())}}
 })}}
}
`

const modelAliasSource = `
model Item @noTime { id: Int @id value: Int }
type Value { value: Int }
type Holder { item: Item }
fn localInferred(): Int { val item = Item { id: 1, value: 1 } val alias = item item.value = 7 return alias.value }
fn localDeclared(): Int { val item: Item = Item { id: 1, value: 1 } val alias = item item.value = 7 return alias.value }
fn aliasInferred(item: Item): Int { val alias = item item.value = 7 return alias.value }
fn aliasDeclared(item: Item): Int { val alias: Item = item item.value = 7 return alias.value }
fn localNullable(): Int { val item: Item? = Item { id: 1, value: 1 } val alias = item val required = item ?: throw error.NotFound required.value = 7 return (alias ?: throw error.NotFound).value }
fn literalStored(): Int { val item = Item { id: 1, value: 1 } val holder = Holder { item: item } item.value = 7 return holder.item.value }
fn literalListed(): Int { val item = Item { id: 1, value: 1 } val items = [item] val alias = item for chosen in items { chosen.value = 7 } return alias.value }
fn awaitedInferred(): Int { val item = await { Item { id: 1, value: 1 } } val alias = item item.value = 7 return alias.value }
fn awaitedDeclared(): Int { val item: Item = await { Item { id: 1, value: 1 } } val alias = item item.value = 7 return alias.value }
fn awaitedTuple(): Int { val (item, other) = await { Item { id: 1, value: 1 } 42 } val alias = item item.value = 7 return alias.value }
fn typeValue(): Int { val value = Value { value: 1 } val alias = value value.value = 7 return alias.value }
fn localRebind(): Int { var item = Item { id: 1, value: 1 } val alias = item item = Item { id: 2, value: 9 } return alias.value }
fn declaredRebind(): Int { var item: Item = Item { id: 1, value: 1 } val alias = item item = Item { id: 2, value: 9 } return alias.value }
fn nullableRebind(): Int { var item: Item? = Item { id: 1, value: 1 } val alias = item item = Item { id: 2, value: 9 } return (alias ?: throw error.NotFound).value }
fn captureLocal(gate: Channel<Int>, output: Channel<Item>): Int { val item = Item { id: 1, value: 1 } async { val _ = <-gate output <- item } item.value = 7 return item.value }
fn readValue(): Result<Int> @native
fn awaitModelValue(): Int { val item = await { Item { id: 1, value: readValue()? } } return item.value }
api localModel(): Int { return localInferred() }
api inputModel(item: Item): Int { val alias = item item.value = 7 return alias.value }
api inputModelDeclared(item: Item): Int { val alias: Item = item item.value = 7 return alias.value }
api inputModelAwait(item: Item): Int { val alias = await { item } item.value = 7 return alias.value }
api inputModelRebind(item: Item): Int { val alias = item item = Item { id: 2, value: 9 } return alias.value }
api inputModelName(_luxoModelInput0: Item): Int { val alias = _luxoModelInput0 _luxoModelInput0.value = 7 return alias.value }
`

const modelAliasAssertions = `package generated
import("bytes";"context";"encoding/json";"errors";"fmt";"net";"net/http/httptest";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/rpc")
type aliasNative struct{calls int;failure error}
func(r *aliasNative)ReadValue(context.Context)(int64,error){r.calls++;return 7,r.failure}
func TestModelAliasOwnershipAndFailure(t *testing.T){
 app:=&App{};gate,output:=make(chan int64,1),make(chan *Item,1)
 if value,err:=app.captureLocal(t.Context(),gate,output);err!=nil||value!=7{t.Fatal(value,err)};gate<-1;captured:=<-output;if captured.Value!=1{t.Fatalf("async snapshot changed: %+v",captured)}
 original:=errors.New("native field failed")
 for _,failure:=range []error{nil,original}{resolver:=&aliasNative{failure:failure};app.Resolver=resolver;value,err:=app.awaitModelValue(t.Context());if resolver.calls!=1||!errors.Is(err,failure)||failure==nil&&value!=7{t.Fatal(value,err,resolver.calls)}}
}
func TestModelAliases(t *testing.T){
 app:=&App{}
 for _,sample:=range []struct{name string;call func()(int64,error);want int64}{
  {"inferred",func()(int64,error){return app.localInferred(t.Context())},7},
  {"declared",func()(int64,error){return app.localDeclared(t.Context())},7},
  {"param-inferred",func()(int64,error){return app.aliasInferred(t.Context(),&Item{Id:1,Value:1})},7},
  {"param-declared",func()(int64,error){return app.aliasDeclared(t.Context(),&Item{Id:1,Value:1})},7},
  {"nullable",func()(int64,error){return app.localNullable(t.Context())},7},
  {"stored",func()(int64,error){return app.literalStored(t.Context())},7},
  {"listed",func()(int64,error){return app.literalListed(t.Context())},7},
  {"await-inferred",func()(int64,error){return app.awaitedInferred(t.Context())},7},
  {"await-declared",func()(int64,error){return app.awaitedDeclared(t.Context())},7},
  {"await-tuple",func()(int64,error){return app.awaitedTuple(t.Context())},7},
  {"type-value",func()(int64,error){return app.typeValue(t.Context())},1},
  {"rebind",func()(int64,error){return app.localRebind(t.Context())},1},
  {"rebind-declared",func()(int64,error){return app.declaredRebind(t.Context())},1},
  {"rebind-nullable",func()(int64,error){return app.nullableRebind(t.Context())},1},
 }{t.Run(sample.name,func(t *testing.T){if got,err:=sample.call();err!=nil||got!=sample.want{t.Fatalf("alias mismatch: got=%d want=%d err=%v",got,sample.want,err)}})}
}
func TestModelAliasesProtocols(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}();defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}();client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,sample:=range []struct{name string;want int64}{{"localModel",7},{"inputModel",7},{"inputModelDeclared",7},{"inputModelAwait",7},{"inputModelRebind",1},{"inputModelName",7}}{for mode:=0;mode<3;mode++{t.Run(fmt.Sprintf("%s/mode=%d",sample.name,mode),func(t *testing.T){
  name:=sample.name;params:=map[string]any{};for _,param:=range router.Registry.ParamOrder(name){params[param.Name]=api.BinaryMessage([]byte{0,1,2,2,2,0})}
  definition:=router.Schema.APIs[name];body,err:=api.EncodeBinaryRequest(definition.ID,nil,params,router.Registry.ParamOrder(name));if err!=nil{t.Fatal(err)}
  if mode==2{_,off:=codec.ReadVarint(body,0);size,n:=codec.ReadVarint(body,off);off+=n+int(size);body,err=client.CallWithMaskContext(t.Context(),"",definition.ID,nil,body[off:]);if err!=nil{t.Fatal(err)}}else{
   if mode==0{input:=map[string]any{"$api":name};for _,param:=range router.Registry.ParamOrder(name){input[param.Name]=map[string]int64{"id":1,"value":1}};body,err=json.Marshal(input);if err!=nil{t.Fatal(err)}}
   request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));if mode==1{request.Header.Set("X-Luxo-Mode","binary")};response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatalf("status=%d body=%s",response.Code,response.Body)};body=response.Body.Bytes()
  }
  if mode==0{var result struct{Data int64};if err:=json.Unmarshal(body,&result);err!=nil||result.Data!=sample.want{t.Fatalf("JSON alias mismatch: %s %v",body,err)}}else{decoder:=codec.NewDecoder(body);if value:=decoder.ReadInt();value!=sample.want||decoder.Finish()!=nil{t.Fatalf("Binary alias mismatch: %x",body)}}
 })}}
}
`

const structuredMemberFlowSource = `
model Item @noTime { id: Int @id }
type Leaf { value: Int }
type Branch { leaf: Leaf optional: Leaf? items: [Leaf] }
type Root { branch: Branch? }
fn safeLeaf(value: Branch?): Leaf? { return value?.leaf }
fn safeOptional(value: Branch?): Leaf? { return value?.optional }
fn safeNested(value: Root?): Int? { return value?.branch?.leaf?.value }
fn safeItems(value: Branch?): [Leaf]? { return value?.items }
fn fallbackLeaf(value: Branch?): Leaf { return value?.leaf ?: Leaf { value: 7 } }
fn whenLeaf(flag: Boolean, value: Branch): Leaf? { return when { flag -> value.leaf else -> null } }
fn whenLeafLiteral(flag: Boolean): Leaf? { return when { flag -> Leaf { value: 42 } else -> null } }
fn whenLeafBlock(flag: Boolean): Leaf? { return when { flag -> { val result = Leaf { value: 42 } result } else -> null } }
fn whenInt(flag: Boolean): Int? { return when { flag -> 42 else -> null } }
fn nestedWhen(flag: Boolean): Int? { return when { flag -> when { true -> 42 else -> 7 } else -> null } }
fn whenModel(flag: Boolean): Item? { return when { flag -> Item { id: 42 } else -> null } }
fn whenItems(flag: Boolean): [Leaf]? { return when { flag -> [Leaf { value: 42 }] else -> null } }
fn selectInt(flag: Boolean): Int? {
 val ch: Channel<Int> = Channel(1)
 if flag { ch <- 42 }
 return when { <-ch -> { value -> value } else -> null }
}
fn selectLeaf(flag: Boolean): Leaf? {
 val ch: Channel<Int> = Channel(1)
 if flag { ch <- 42 }
 return when { <-ch -> { value -> Leaf { value: value } } else -> null }
}
fn selectModel(flag: Boolean): Item? {
 val ch: Channel<Int> = Channel(1)
 if flag { ch <- 42 }
 return when { <-ch -> { value -> Item { id: value } } else -> null }
}
fn selectRequiredModel(flag: Boolean): Item {
 val ch: Channel<Int> = Channel(1)
 if flag { ch <- 42 }
 return when { <-ch -> { value -> Item { id: value } } else -> Item { id: 7 } }
}
fn fetchLeaf(): Result<Leaf> @native
fn condition(flag: Boolean): Boolean { return flag }
fn whenFetched(flag: Boolean): Leaf? { return when { flag -> fetchLeaf()? else -> null } }
fn whenCalled(flag: Boolean): Leaf? { return when { condition(flag) -> fetchLeaf()? else -> null } }
fn replaceLeaf(value: Branch, other: Leaf): Branch { value.leaf = other return value }
fn replaceOptional(value: Branch, other: Leaf?): Branch { value.optional = other return value }
api sanity(): Int { return 1 }
api chooseLeaf(flag: Boolean): Leaf? { return when { flag -> Leaf { value: 42 } else -> null } }
api chooseInt(flag: Boolean): Int? { return when { flag -> 42 else -> null } }
api chooseItem(flag: Boolean): Item? { return when { flag -> Item { id: 42 } else -> null } }
api chooseFetched(flag: Boolean): Leaf? { return when { flag -> fetchLeaf()? else -> null } }
fn serviceChoice(flag: Boolean): Leaf? @service { return when { flag -> Leaf { value: 42 } else -> null } }
`

func TestGeneratedStructuredMemberFlows(t *testing.T) {
	runGeneratedFixture(t, structuredMemberFlowSource+nullableWhenScalarSource, structuredMemberFlowAssertions+nullableWhenScalarAssertions, StableIDs{
		APIs:      map[string]int{"sanity": 1, "chooseLeaf": 2, "chooseInt": 3, "chooseItem": 4, "svc:serviceChoice": 5, "chooseFetched": 6},
		APIParams: map[string]map[string]int{"chooseLeaf": {"flag": 1}, "chooseInt": {"flag": 1}, "chooseItem": {"flag": 1}, "svc:serviceChoice": {"flag": 1}, "chooseFetched": {"flag": 1}},
		ModelFields: map[string]map[string]int{
			"Item": {"id": 1}, "Leaf": {"value": 1},
			"Branch": {"leaf": 1, "optional": 2, "items": 3}, "Root": {"branch": 1},
		},
	})
}

const structuredMemberFlowAssertions = `package generated

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/rpc"
	"github.com/shopspring/decimal"
)

type memberFlowResolver struct {
	calls   int
	failure error
}

func (r *memberFlowResolver) FetchLeaf(context.Context) (Leaf, error) {
	r.calls++
	return Leaf{Value: 42}, r.failure
}

func TestStructuredSafeAccess(t *testing.T) {
	app := &App{}
	leaf := Leaf{Value: 42}
	branch := Branch{Leaf: &leaf}
	for _, input := range []*Branch{nil, &branch} {
		got, err := app.safeLeaf(t.Context(), input)
		checkNullableWhen(t, input != nil, got, leaf, err)
		optional, err := app.safeOptional(t.Context(), input)
		checkNullableWhen(t, false, optional, Leaf{}, err)
		fallback, err := app.fallbackLeaf(t.Context(), input)
		want := int64(7)
		if input != nil {
			want = 42
		}
		if err != nil || fallback.Value != want {
			t.Fatal("field fallback", fallback, err)
		}
		items, err := app.safeItems(t.Context(), input)
		checkEmptyLeafList(t, input != nil, items, err)
	}
	branch.Optional = &leaf
	got, err := app.safeOptional(t.Context(), &branch)
	checkNullableWhen(t, true, got, leaf, err)
	for _, input := range []*Root{nil, {}, {Branch: &branch}} {
		value, err := app.safeNested(t.Context(), input)
		checkNullableWhen(t, input != nil && input.Branch != nil, value, int64(42), err)
	}
	replaced, err := app.replaceLeaf(t.Context(), branch, Leaf{Value: 9})
	if err != nil || replaced.Leaf == nil || replaced.Leaf.Value != 9 {
		t.Fatal("required assignment", replaced, err)
	}
	replaced, err = app.replaceOptional(t.Context(), branch, nil)
	if err != nil || replaced.Optional != nil {
		t.Fatal("nullable assignment", replaced, err)
	}
}

func checkEmptyLeafList(t *testing.T, present bool, items []Leaf, err error) {
	t.Helper()
	if err != nil || (!present && items != nil) || (present && (items == nil || len(items) != 0)) {
		t.Fatal("null/empty list", items, err)
	}
}

func TestNullableWhenRepresentations(t *testing.T) {
	app := &App{}
	branch := Branch{Leaf: &Leaf{Value: 42}}
	for _, enabled := range []bool{false, true} {
		checkWhenObjects(t, app, enabled, branch)
		checkWhenScalars(t, app, enabled)
		checkWhenModels(t, app, enabled)
		items, err := app.whenItems(t.Context(), enabled)
		if err != nil || (!enabled && items != nil) || (enabled && (len(items) != 1 || items[0].Value != 42)) {
			t.Fatal("when list", items, err)
		}
	}
}

func checkWhenObjects(t *testing.T, app *App, enabled bool, branch Branch) {
	t.Helper()
	for _, call := range []func() (*Leaf, error){
		func() (*Leaf, error) { return app.whenLeaf(t.Context(), enabled, branch) },
		func() (*Leaf, error) { return app.whenLeafLiteral(t.Context(), enabled) },
		func() (*Leaf, error) { return app.whenLeafBlock(t.Context(), enabled) },
		func() (*Leaf, error) { return app.selectLeaf(t.Context(), enabled) },
	} {
		value, err := call()
		checkNullableWhen(t, enabled, value, Leaf{Value: 42}, err)
	}
}

func checkWhenScalars(t *testing.T, app *App, enabled bool) {
	t.Helper()
	for _, call := range []func() (*int64, error){
		func() (*int64, error) { return app.whenInt(t.Context(), enabled) },
		func() (*int64, error) { return app.nestedWhen(t.Context(), enabled) },
		func() (*int64, error) { return app.selectInt(t.Context(), enabled) },
	} {
		value, err := call()
		checkNullableWhen(t, enabled, value, int64(42), err)
	}
}

func checkWhenModels(t *testing.T, app *App, enabled bool) {
	t.Helper()
	for _, call := range []func() (*Item, error){
		func() (*Item, error) { return app.whenModel(t.Context(), enabled) },
		func() (*Item, error) { return app.selectModel(t.Context(), enabled) },
	} {
		value, err := call()
		if err != nil || (!enabled && value != nil) || (enabled && (value == nil || value.Id != 42)) {
			t.Fatal("when model", value, err)
		}
	}
	required, err := app.selectRequiredModel(t.Context(), enabled)
	want := int64(7)
	if enabled {
		want = 42
	}
	if err != nil || required == nil || required.Id != want {
		t.Fatal("required select model", required, err)
	}
}

func TestNullableWhenNativeFailure(t *testing.T) {
	for _, withCall := range []bool{false, true} {
		resolver := &memberFlowResolver{}
		app := &App{Resolver: resolver}
		call := app.whenFetched
		if withCall {
			call = app.whenCalled
		}
		if value, err := call(t.Context(), false); err != nil || value != nil || resolver.calls != 0 {
			t.Fatal("inactive branch evaluated", value, err, resolver.calls)
		}
		if value, err := call(t.Context(), true); err != nil || value == nil || value.Value != 42 || resolver.calls != 1 {
			t.Fatal("native success", value, err, resolver.calls)
		}
		failure := errors.New("native failed")
		resolver.failure = failure
		if value, err := call(t.Context(), true); err != failure || value != nil || resolver.calls != 2 {
			t.Fatal("native error lost", value, err, resolver.calls)
		}
	}
}

func TestNullableWhenHTTP(t *testing.T) {
	router := api.NewRouter()
	resolver := &memberFlowResolver{}
	if err := RegisterSchema(router.Schema); err != nil {
		t.Fatal(err)
	}
	if err := RegisterHandlers(router, &App{Resolver: resolver}); err != nil {
		t.Fatal(err)
	}
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, field, json string
		id                int
		body              []byte
		failure           bool
	}{
		{"chooseLeaf", "value", "{\"value\":42}", 2, []byte{1, 0, 1, 84, 0}, false},
		{"chooseInt", "", "42", 3, []byte{1, 84}, false},
		{"chooseItem", "id", "{\"id\":42}", 4, []byte{1, 0, 1, 84, 0}, false},
		{"chooseFetched", "value", "{\"value\":42}", 6, []byte{1, 0, 1, 84, 0}, false},
		{"chooseFetched", "value", "{\"value\":42}", 6, []byte{1, 0, 1, 84, 0}, true},
	} {
		resolver.failure = nil
		if tc.failure {
			resolver.failure = errors.New("native failed")
		}
		for _, enabled := range []bool{false, true} {
			flag := "false"
			if enabled {
				flag = "true"
			}
			for _, binary := range []bool{false, true} {
				body := []byte("{\"$api\":\"" + tc.name + "\",\"$select\":\"" + tc.field + "\",\"flag\":" + flag + "}")
				if binary {
					var mask []byte
					if tc.field != "" {
						var err error
						mask, err = codec.AppendSelectionMask(nil, []byte{1}, nil)
						if err != nil {
							t.Fatal(err)
						}
					}
					var err error
					body, err = api.EncodeBinaryRequest(tc.id, mask, map[string]any{"flag": enabled}, router.Registry.ParamOrder(tc.name))
					if err != nil {
						t.Fatal(err)
					}
				}
				request := httptest.NewRequest("POST", "/", bytes.NewReader(body))
				if binary {
					request.Header.Set("X-Luxo-Mode", "binary")
				}
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				wantJSON := tc.json
				wantBinary := tc.body
				if !enabled {
					wantJSON = "null"
					wantBinary = []byte{0}
				}
				valid := bytes.Contains(response.Body.Bytes(), []byte("\"data\":"+wantJSON))
				if binary {
					valid = bytes.Equal(response.Body.Bytes(), wantBinary)
				}
				status := 200
				if enabled && tc.failure {
					status = 500
					valid = true
				}
				if response.Code != status || !valid {
					t.Fatalf("%s flag=%v binary=%v status=%d body=%x", tc.name, enabled, binary, response.Code, response.Body.Bytes())
				}
			}
		}
	}
}

func TestNullableWhenLRPC(t *testing.T) {
	router := api.NewRouter()
	if err := RegisterSchema(router.Schema); err != nil {
		t.Fatal(err)
	}
	if err := RegisterServiceFns(router, &App{Resolver: &memberFlowResolver{}}); err != nil {
		t.Fatal(err)
	}
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer(router)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		server.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	client := rpc.NewClient(listener.Addr().String())
	defer client.Close()
	mask, err := codec.AppendSelectionMask(nil, []byte{1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	meta := router.Schema.APIs["svc:serviceChoice"]
	if meta == nil || !meta.Internal || !meta.ReturnNullable {
		t.Fatal("service metadata lost", meta)
	}
	for _, enabled := range []bool{false, true} {
		enc := codec.Encoder{}
		enc.WriteFieldBool(meta.Params[0].ID, enabled)
		enc.WriteEnd()
		value, err := client.CallWithMaskContext(t.Context(), "", meta.ID, mask, enc.Bytes())
		want := []byte{0}
		if enabled {
			want = []byte{1, 0, 1, 84, 0}
		}
		if err != nil || !bytes.Equal(value, want) {
			t.Fatal("nullable service result", enabled, value, err)
		}
	}
}

`

const nullableWhenScalarSource = `
enum State { READY WAITING }
fn nullableInt(flag: Boolean, value: Int): Int? { return when { flag -> value else -> null } }
fn nullableString(flag: Boolean, value: String): String? { return when { flag -> value else -> null } }
fn nullableBool(flag: Boolean, value: Boolean): Boolean? { return when { flag -> value else -> null } }
fn nullableFloat(flag: Boolean, value: Float): Float? { return when { flag -> value else -> null } }
fn nullableDate(flag: Boolean, value: DateTime): DateTime? { return when { flag -> value else -> null } }
fn nullableDuration(flag: Boolean, value: Duration): Duration? { return when { flag -> value else -> null } }
fn nullableUUID(flag: Boolean, value: UUID): UUID? { return when { flag -> value else -> null } }
fn nullableDecimal(flag: Boolean, value: Decimal): Decimal? { return when { flag -> value else -> null } }
fn nullableBytes(flag: Boolean, value: Bytes): Bytes? { return when { flag -> value else -> null } }
fn nullableJSON(flag: Boolean, value: JSON): JSON? { return when { flag -> value else -> null } }
fn nullableState(flag: Boolean, value: State): State? { return when { flag -> value else -> null } }
fn nullablePass(flag: Boolean, value: Int?): Int? { return when { flag -> value else -> null } }
fn capturedScalar(): Int? { var value = 42 val captured: Int? = value value = 99 return captured }
fn capturedObject(value: Branch): Leaf? { val captured: Leaf? = value.leaf value.leaf.value = 99 return captured }
fn shadowedNew(new: Int): Int? { return new }
fn yieldBytes(flag: Boolean, value: Bytes): Bytes? {
 val found = for item in [1] { if flag { yield value } }
 return found
}
`

const nullableWhenScalarAssertions = `func checkNullableWhen[T comparable](t *testing.T, enabled bool, got *T, want T, err error) {
	t.Helper()
	if err != nil || (!enabled && got != nil) || (enabled && (got == nil || *got != want)) {
		t.Fatalf("enabled=%v got=%v want=%v err=%v", enabled, got, want, err)
	}
}

func TestNullableWhenScalarTypes(t *testing.T) {
	app := &App{}
	text := "東京\x00"
	instant := time.Date(1969, 12, 31, 23, 59, 59, 123456789, time.UTC)
	elapsed := -time.Second - 42
	id := uuid.UUID{0: 1, 15: 42}
	for _, enabled := range []bool{false, true} {
		integer, err := app.nullableInt(t.Context(), enabled, math.MinInt64)
		checkNullableWhen(t, enabled, integer, int64(math.MinInt64), err)
		stringValue, err := app.nullableString(t.Context(), enabled, text)
		checkNullableWhen(t, enabled, stringValue, text, err)
		boolean, err := app.nullableBool(t.Context(), enabled, false)
		checkNullableWhen(t, enabled, boolean, false, err)
		date, err := app.nullableDate(t.Context(), enabled, instant)
		checkNullableWhen(t, enabled, date, instant, err)
		duration, err := app.nullableDuration(t.Context(), enabled, elapsed)
		checkNullableWhen(t, enabled, duration, elapsed, err)
		uuidValue, err := app.nullableUUID(t.Context(), enabled, id)
		checkNullableWhen(t, enabled, uuidValue, id, err)
		state, err := app.nullableState(t.Context(), enabled, StateREADY)
		checkNullableWhen(t, enabled, state, StateREADY, err)
		checkWhenNumbers(t, app, enabled)
		checkWhenBuffers(t, app, enabled)
		value := int64(42)
		passed, err := app.nullablePass(t.Context(), enabled, &value)
		if err != nil || (!enabled && passed != nil) || (enabled && passed != &value) {
			t.Fatal("nullable pass", passed, err)
		}
	}
}

func TestNullableValueSnapshots(t *testing.T) {
	app := &App{}
	value, err := app.capturedScalar(t.Context())
	checkNullableWhen(t, true, value, int64(42), err)
	leaf := Leaf{Value: 42}
	object, err := app.capturedObject(t.Context(), Branch{Leaf: &leaf})
	checkNullableWhen(t, true, object, Leaf{Value: 42}, err)
	if leaf.Value != 99 { t.Fatal("source mutation missing", leaf) }
	shadowed, err := app.shadowedNew(t.Context(), 42)
	checkNullableWhen(t, true, shadowed, int64(42), err)
}

func checkWhenNumbers(t *testing.T, app *App, enabled bool) {
	t.Helper()
	negativeZero := math.Copysign(0, -1)
	floatValue, err := app.nullableFloat(t.Context(), enabled, negativeZero)
	if err != nil || (!enabled && floatValue != nil) || (enabled && (floatValue == nil || math.Float64bits(*floatValue) != math.Float64bits(negativeZero))) {
		t.Fatal("float", floatValue, err)
	}
	exact := decimal.New(1234567890123456789, -9)
	decimalValue, err := app.nullableDecimal(t.Context(), enabled, exact)
	if err != nil || (!enabled && decimalValue != nil) || (enabled && (decimalValue == nil || !decimalValue.Equal(exact))) {
		t.Fatal("decimal", decimalValue, err)
	}
}

func checkWhenBuffers(t *testing.T, app *App, enabled bool) {
	t.Helper()
	for _, input := range [][]byte{nil, {}, {0, 255, 127}} {
		value, err := app.nullableBytes(t.Context(), enabled, input)
		if err != nil || (!enabled && value != nil) || (enabled && (value == nil || !bytes.Equal(*value, input))) {
			t.Fatal("bytes", value, err)
		}
		yielded, err := app.yieldBytes(t.Context(), enabled, input)
		if err != nil || (!enabled && yielded != nil) || (enabled && (yielded == nil || !bytes.Equal(*yielded, input))) {
			t.Fatal("yield bytes", yielded, err)
		}
	}
	for _, input := range []json.RawMessage{json.RawMessage("null"), json.RawMessage("{\"n\":9007199254740993}")} {
		value, err := app.nullableJSON(t.Context(), enabled, input)
		if err != nil || (!enabled && value != nil) || (enabled && (value == nil || !bytes.Equal(*value, input))) {
			t.Fatal("JSON", value, err)
		}
	}
}

var nullableWhenIntSink *int64
var nullableWhenBytesSink *[]byte

func (app *App) referenceWhenInt(_ context.Context, enabled bool, value int64) (*int64, error) {
	if !enabled {
		return nil, nil
	}
	copy := value
	return &copy, nil
}

func (app *App) referenceWhenBytes(_ context.Context, enabled bool, value []byte) (*[]byte, error) {
	if !enabled {
		return nil, nil
	}
	copy := value
	return &copy, nil
}

func BenchmarkNullableWhenRuntime(b *testing.B) {
	app := &App{}
	for _, enabled := range []bool{false, true} {
		name := "null"
		if enabled {
			name = "value"
		}
		integers := []struct {
			name string
			call func(context.Context, bool, int64) (*int64, error)
		}{{"generated", app.nullableInt}, {"reference", app.referenceWhenInt}}
		if os.Getenv("LUXO_BENCH_REFERENCE_FIRST") == "1" {
			integers[0], integers[1] = integers[1], integers[0]
		}
		for _, method := range integers {
			b.Run("int_"+name+"/"+method.name, func(b *testing.B) {
				ctx := b.Context()
				b.ReportAllocs()
				for b.Loop() {
					var err error
					nullableWhenIntSink, err = method.call(ctx, enabled, 42)
					if err != nil || (!enabled && nullableWhenIntSink != nil) || (enabled && (nullableWhenIntSink == nil || *nullableWhenIntSink != 42)) {
						b.Fatal(nullableWhenIntSink, err)
					}
				}
			})
		}
		bytesMethods := []struct {
			name string
			call func(context.Context, bool, []byte) (*[]byte, error)
		}{{"generated", app.nullableBytes}, {"reference", app.referenceWhenBytes}}
		if os.Getenv("LUXO_BENCH_REFERENCE_FIRST") == "1" {
			bytesMethods[0], bytesMethods[1] = bytesMethods[1], bytesMethods[0]
		}
		input := make([]byte, 128)
		input[127] = 42
		for _, method := range bytesMethods {
			b.Run("bytes_"+name+"/"+method.name, func(b *testing.B) {
				ctx := b.Context()
				b.ReportAllocs()
				for b.Loop() {
					var err error
					nullableWhenBytesSink, err = method.call(ctx, enabled, input)
					if err != nil || (!enabled && nullableWhenBytesSink != nil) || (enabled && (nullableWhenBytesSink == nil || len(*nullableWhenBytesSink) != len(input) || (*nullableWhenBytesSink)[127] != 42)) {
						b.Fatal(nullableWhenBytesSink, err)
					}
				}
			})
		}
	}
}
`

func TestRowReaderFragmentSkipsMissingMetadata(t *testing.T) {
	g := mustNewGenerator(t, GeneratorConfig{Driver: DriverPG, IDs: StableIDs{ModelFields: map[string]map[string]int{"Fragment": {"value": 1}}}})
	model := &ast.ModelDecl{Name: "Fragment", Fields: []*ast.FieldDecl{
		{Name: "untyped"}, {Name: "unregistered", Type: &ast.TypeRef{Name: "Int"}}, {Name: "value", Type: &ast.TypeRef{Name: "Int"}},
	}}
	var source strings.Builder
	g.generateRowReader(&source, model, nil, false)
	if text := source.String(); !strings.Contains(text, "case 1:") || strings.Contains(text, "unregistered") || strings.Contains(text, "untyped") {
		t.Fatal("fragment reader emitted a field without wire metadata", text)
	}
}

func TestObjectDefaultsUseAnonymousAPIReturnType(t *testing.T) {
	c := newCompiler(nil)
	c.api = &ast.ApiDecl{ReturnType: &ast.TypeRef{Name: "Snapshot"}}
	c.generator = defaultGenerator()
	c.generator.objectDefaults = map[string][]*ast.FieldDecl{"Snapshot": {
		{Name: "value", Type: &ast.TypeRef{Name: "Int"}, Default: &ast.Literal{Kind: token.Int, Value: "42"}},
	}}
	if value := c.compileObject(&ast.ObjectExpr{}); value != "Snapshot{Value: 42}" {
		t.Fatal("anonymous typed result lost its default", value)
	}
}

func TestObjectFieldStorageTypes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ref        *ast.TypeRef
		references bool
		want       string
	}{
		{"untyped", nil, true, ""},
		{"scalar", &ast.TypeRef{Name: "Int"}, true, ""},
		{"enum", &ast.TypeRef{Name: "State"}, true, ""},
		{"list", &ast.TypeRef{Name: "Snapshot", IsList: true}, true, "[]Snapshot"},
		{"nullable", &ast.TypeRef{Name: "Snapshot", Nullable: true}, true, "*Snapshot"},
		{"object", &ast.TypeRef{Name: "Snapshot"}, true, "*Snapshot"},
		{"event", &ast.TypeRef{Name: "Snapshot"}, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wasNullable := tc.ref != nil && tc.ref.Nullable
			got := objectFieldType(tc.ref, tc.references, map[string]bool{"State": true})
			value := ""
			if got != nil {
				value = resolveGoType(got)
			}
			if value != tc.want || (tc.ref != nil && tc.ref.Nullable != wasNullable) {
				t.Fatalf("field storage=%q want=%q, declaration=%+v", value, tc.want, tc.ref)
			}
		})
	}
}

func TestStructuredMemberRepresentations(t *testing.T) {
	c := newCompiler(makeModels("Counter"))
	c.models["Remote"] = nil
	c.types = map[string]bool{"Box": true, "Snapshot": true}
	for _, tc := range []struct {
		name, parent, child string
		nullable, list      bool
		want                string
	}{
		{"type", "Box", "Snapshot", false, false, "(*value.Child)"},
		{"model", "Counter", "Snapshot", false, false, "(*value.Child)"},
		{"model_metadata", "Remote", "Snapshot", false, false, "(*value.Child)"},
		{"event", "ChangedEvent", "Snapshot", false, false, "value.Child"},
		{"scalar", "Box", "Int", false, false, "value.Child"},
		{"model_reference", "Box", "Counter", false, false, "value.Child"},
		{"nullable", "Box", "Snapshot", true, false, "value.Child"},
		{"list", "Box", "Snapshot", false, true, "value.Child"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			member := &ast.MemberExpr{Object: &ast.Ident{TypeTagged: ast.TypeTagged{TypeTag: tc.parent}},
				TypeTagged: ast.TypeTagged{TypeTag: tc.child, NullableTag: tc.nullable, ListTypeTag: tc.list}}
			if got := c.structuredMemberValue(member, "value.Child"); got != tc.want {
				t.Fatalf("field value=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestTypeDefaultsDoNotSynthesizeIdentity(t *testing.T) {
	ids := StableIDs{ModelFields: map[string]map[string]int{"Principal": {"id": 1, "role": 2}}}
	g := mustNewGenerator(t, GeneratorConfig{Driver: DriverPG, IDs: ids})
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Default: &ast.Literal{Kind: token.Int, Value: "42"}},
		{Name: "role", Type: &ast.TypeRef{Name: "String"}, Default: &ast.Literal{Kind: token.String, Value: "ADMIN"}},
	}
	g.objectDefaults = map[string][]*ast.FieldDecl{"Principal": fields}
	var helper strings.Builder
	g.generateReadIdentityInput(&helper, &ast.ModelDecl{Name: "Principal", Fields: fields}, nil, false)
	runGeneratedFixture(t, `type Principal { id: Int = 42 role: String = "ADMIN" }`, identityDefaultAssertions+helper.String(), ids)
}

const identityDefaultAssertions = `package generated
import("fmt";"testing";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/jsonutil")
func TestIdentityDefaults(t *testing.T){
 for _,raw:=range []string{"{}","{\"id\":1}","{\"role\":\"USER\"}"}{value:=Principal{Id:7,Role:"sentinel"};if err:=value.readIdentityInput([]byte(raw),0);err==nil||value.Id!=7||value.Role!="sentinel"{t.Fatal("missing claims were defaulted",value,err)}}
 var value Principal;if err:=value.readIdentityInput([]byte("{\"id\":1,\"role\":\"USER\"}"),0);err!=nil||value.Id!=1||value.Role!="USER"{t.Fatal("explicit claims changed",value,err)}
}
`

func richTypeDefaultIDs() StableIDs {
	fields := map[string]int{}
	for i, name := range strings.Fields("count text real flag elapsed state legacyState optional optionalText optionalState optionalReal optionalDuration empty nullableJSON payload items secret") {
		fields[name] = i + 1
	}
	return StableIDs{ModelFields: map[string]map[string]int{
		"Defaults": fields, "Box": {"inner": 1, "items": 2, "models": 3}, "Partial": {"id": 1, "flag": 2},
	}, APIs: map[string]int{"echo": 1, "wrap": 2, "echoMany": 3}}
}

const richTypeDefaultSource = `enum State { OPEN CLOSED }
type Defaults {
 count: Int = -9223372036854775808
 text: String = "東京"
 real: Float = -0.0
 flag: Boolean = true
 secret: String = "server" @hidden
 elapsed: Duration = -1d
 state: State = State.OPEN
 legacyState: State = OPEN
 optional: Int? = 42
 optionalText: String? = "guest"
 optionalState: State? = OPEN
 optionalReal: Float? = -0.0
 optionalDuration: Duration? = -1s
 empty: Int? = null
 nullableJSON: JSON? = null
 payload: JSON = null
 items: [Int]? = null
}
type Box { inner: Defaults items: [Defaults] models: [Partial] }
model Partial @noTime { id: Int @id flag: Boolean = true }
fn built(): Defaults { return Defaults {} }
fn explicit(): Defaults { return Defaults { count: 0, text: "", real: 0.0, flag: false, optional: null, optionalText: null, optionalState: null, optionalReal: null, optionalDuration: null, items: [] } }
fn rows(): [Defaults] { return for i in 0..1 { Defaults {} } }
fn partial(): Partial { return Partial { id: 1 } }
api echo(value: Defaults): Defaults { return value }
api wrap(value: Box): Box { return value }
api echoMany(value: [Defaults]): [Defaults] { return value }
`

const richTypeDefaultAssertions = `package generated
import("bytes";"math";"net";"net/http/httptest";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/rpc")
func checkDefaults(t *testing.T,v Defaults){
 t.Helper()
 if v.Count!=math.MinInt64||v.Text!="東京"||math.Float64bits(v.Real)!=uint64(1)<<63||!v.Flag||v.Elapsed!=-24*time.Hour||v.State!=StateOPEN||v.LegacyState!=StateOPEN{t.Fatal("scalar defaults",v)}
 if v.Optional==nil||*v.Optional!=42||v.OptionalText==nil||*v.OptionalText!="guest"||v.OptionalState==nil||*v.OptionalState!=StateOPEN||v.OptionalReal==nil||math.Float64bits(*v.OptionalReal)!=uint64(1)<<63||v.OptionalDuration==nil||*v.OptionalDuration!=-time.Second{t.Fatal("nullable defaults",v)}
 if v.Empty!=nil||v.NullableJSON!=nil||v.Items!=nil||!bytes.Equal(v.Payload,[]byte("null"))||v.Secret!="server"{t.Fatal("null/private defaults",v)}
}
func TestRichConstructorDefaults(t *testing.T){
 app:=&App{};first,err:=app.built(t.Context());if err!=nil{t.Fatal(err)};checkDefaults(t,first)
 second,err:=app.built(t.Context());if err!=nil{t.Fatal(err)};first.Payload[0]='x';*first.Optional=7;checkDefaults(t,second)
 rows,err:=app.rows(t.Context());if err!=nil||len(rows)!=2{t.Fatal(rows,err)};rows[0].Payload[0]='x';*rows[0].Optional=7;checkDefaults(t,rows[1])
 explicit,err:=app.explicit(t.Context());if err!=nil||explicit.Count!=0||explicit.Text!=""||math.Float64bits(explicit.Real)!=0||explicit.Flag||explicit.Optional!=nil||explicit.OptionalText!=nil||explicit.OptionalState!=nil||explicit.OptionalReal!=nil||explicit.OptionalDuration!=nil||explicit.Items==nil||len(explicit.Items)!=0{t.Fatal("explicit values lost",explicit,err)}
 partial,err:=app.partial(t.Context());if err!=nil||partial==nil||partial.Id!=1||partial.Flag{t.Fatal("partial model invented data",partial,err)}
}
func TestRichInputDefaults(t *testing.T){
 var first,second Defaults;if err:=first.ReadJSONInput([]byte("{}"));err!=nil{t.Fatal(err)};checkDefaults(t,first);if err:=second.ReadJSONInput([]byte("{}"));err!=nil{t.Fatal(err)};first.Payload[0]='x';*first.Optional=7;checkDefaults(t,second)
 decoder:=codec.NewDecoder([]byte{0,0});var binary Defaults;binary.ReadLuxoInput(decoder);if err:=decoder.Finish();err!=nil{t.Fatal(err)};checkDefaults(t,binary)
 var explicit Defaults;if err:=explicit.ReadJSONInput([]byte("{\"count\":0,\"text\":\"\",\"real\":0.0,\"flag\":false,\"optional\":null,\"optionalText\":null,\"optionalState\":null,\"optionalReal\":null,\"optionalDuration\":null,\"items\":[]}"));err!=nil||explicit.Count!=0||explicit.Text!=""||math.Float64bits(explicit.Real)!=0||explicit.Flag||explicit.Optional!=nil||explicit.OptionalText!=nil||explicit.OptionalState!=nil||explicit.OptionalReal!=nil||explicit.OptionalDuration!=nil||explicit.Items==nil{t.Fatal("JSON explicit values lost",explicit,err)}
 encoder:=codec.NewEncoder(nil);encoder.WriteVarint(0);encoder.WriteFieldInt(1,0);if err:=encoder.WriteFieldString(2,"");err!=nil{t.Fatal(err)};if err:=encoder.WriteFieldFloat(3,0);err!=nil{t.Fatal(err)};encoder.WriteFieldBool(4,false);encoder.WriteFieldIntPtr(8,nil);encoder.WriteEnd();decoder=codec.NewDecoder(encoder.Bytes());var provided Defaults;provided.ReadLuxoInput(decoder);if err:=decoder.Finish();err!=nil||provided.Count!=0||provided.Text!=""||math.Float64bits(provided.Real)!=0||provided.Flag||provided.Optional!=nil{t.Fatal("Binary explicit values lost",provided,err)}
 for _,raw:=range []string{"{\"optional\":\"bad\"}","{\"secret\":\"client\"}"}{before:=Defaults{Count:7};if err:=before.ReadJSONInput([]byte(raw));err==nil||before.Count!=7{t.Fatal("bad JSON input published",before,err)}}
 decoder=codec.NewDecoder([]byte{0,1});failed:=Defaults{Text:"sentinel"};failed.ReadLuxoInput(decoder);if decoder.Err()==nil||failed.Optional!=nil||failed.Payload!=nil||failed.Secret!=""{t.Fatal("bad Binary input acquired defaults",failed,decoder.Err())}
}
func TestRichDefaultNetwork(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}();defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}();client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 mask,err:=codec.AppendSelectionMask(nil,[]byte{0xff,0xff},nil);if err!=nil{t.Fatal(err)}
 body,err:=api.EncodeBinaryRequest(1,mask,map[string]any{"value":api.BinaryMessage([]byte{0,0})},router.Registry.ParamOrder("echo"));if err!=nil{t.Fatal(err)}
 request:=httptest.NewRequest("POST","/",bytes.NewReader(body));request.Header.Set("X-Luxo-Mode","binary");response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatal(response.Code,response.Body.String())};var got Defaults;decoder:=codec.NewDecoder(response.Body.Bytes());got.ReadLuxo(decoder);if err:=decoder.Finish();err!=nil{t.Fatal(err)};if got.Secret!=""{t.Fatal("projection invented hidden default",got.Secret)};got.Secret="server";checkDefaults(t,got)
 frame,err:=api.EncodeBinaryRequest(1,nil,map[string]any{"value":api.BinaryMessage([]byte{0,0})},router.Registry.ParamOrder("echo"));if err!=nil||!bytes.Equal(frame[:2],[]byte{1,0}){t.Fatal(frame,err)};raw,err:=client.CallWithMaskContext(t.Context(),"",1,mask,frame[2:]);if err!=nil{t.Fatal(err)};got=Defaults{};decoder=codec.NewDecoder(raw);got.ReadLuxo(decoder);if err:=decoder.Finish();err!=nil{t.Fatal(err)};if got.Secret!=""{t.Fatal("projection invented hidden default",got.Secret)};got.Secret="server";checkDefaults(t,got)
 for _,body:=range []string{"{\"$api\":\"echo\",\"$select\":\"count,text,optional,payload\",\"value\":{}}","{\"$api\":\"wrap\",\"$select\":\"inner{count},items{count},models{id,flag}\",\"value\":{\"inner\":{},\"items\":[{},{}],\"models\":[{\"id\":1}]}}","{\"$api\":\"echoMany\",\"$select\":\"count\",\"value\":[{},{}]}"}{
  request:=httptest.NewRequest("POST","/",bytes.NewBufferString(body));response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200||!bytes.Contains(response.Body.Bytes(),[]byte("-9223372036854775808")){t.Fatal("JSON/nested defaults",response.Code,response.Body.String())}
 }
}
`

func TestGeneratedWideTypeDefaults(t *testing.T) {
	var source strings.Builder
	source.WriteString("type Wide {\n")
	ids := map[string]int{}
	for i := range 65 {
		fmt.Fprintf(&source, "f%d: Int = %d\n", i, i+1)
		ids[fmt.Sprintf("f%d", i)] = i*900 + 1
	}
	source.WriteString("}\nfn wide(): Wide { return Wide {} }\n")
	runGeneratedFixture(t, source.String(), wideTypeDefaultAssertions, StableIDs{ModelFields: map[string]map[string]int{"Wide": ids}})
}

const wideTypeDefaultAssertions = `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/codec")

func TestWideDefaults(t *testing.T){
 got,err:=(&App{}).wide(t.Context());if err!=nil||got.F0!=1||got.F63!=64||got.F64!=65{t.Fatal("constructor",got,err)}
 decoder:=codec.NewDecoder([]byte{0,0});got=Wide{};got.ReadLuxoInput(decoder);if err:=decoder.Finish();err!=nil||got.F0!=1||got.F63!=64||got.F64!=65{t.Fatal("missing",got,err)}
 encoder:=codec.NewEncoder(nil);encoder.WriteVarint(0);encoder.WriteFieldInt(57601,0);encoder.WriteEnd();decoder=codec.NewDecoder(encoder.Bytes());got=Wide{};got.ReadLuxoInput(decoder);if err:=decoder.Finish();err!=nil||got.F0!=1||got.F63!=64||got.F64!=0{t.Fatal("provided",got,err)}
}
`
