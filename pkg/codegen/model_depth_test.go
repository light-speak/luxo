package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestFlatModelDepthRequiresNoRecursiveHelpers(t *testing.T) {
	input := analyzePermissionFixture(t, `type Flat { value: JSON values: [JSON] }
api echo(input: Flat): Flat { return input }`)
	ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Flat": {"value": 1, "values": 2}}})
	generated, err := mustNewGenerator(t, GeneratorConfig{IDs: ids}).Generate(input, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	source := string(generated.Files["writejson.gen.go"])
	for _, helper := range []string{"writeLuxoDepth", "readLuxoDepth", "readLuxoInputDepth", "readJSONInputDepth", "readColumnarFlatDepth", "writeColumnarFlatDepth"} {
		if strings.Contains(source, helper) {
			t.Fatalf("flat declarations cannot contain a second model object: unnecessary %s", helper)
		}
	}
	runGeneratedCompilation(t, generated, `package generated
import("encoding/json";"strings";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestFlatJSONBudget(t *testing.T){
 for _,depth:=range []int{128,129}{raw:=json.RawMessage(strings.Repeat("[",depth)+"0"+strings.Repeat("]",depth));value:=Flat{Value:raw,Values:[]json.RawMessage{raw}};var buf api.ResponseBuf
  err:=value.WriteLuxo(&buf,nil);if (err==nil)!=(depth==128){t.Fatal("flat writer JSON budget",depth,err)}
  if err==nil{var restored Flat;dec:=codec.NewDecoder(buf.B);restored.ReadLuxo(dec);if err:=dec.Finish();err!=nil||len(restored.Values)!=1{t.Fatal("flat binary result",err)}}
  var restored Flat;err=restored.ReadJSONInput([]byte("{\"value\":"+string(raw)+",\"values\":["+string(raw)+"]}"));if (err==nil)!=(depth==128){t.Fatal("flat input JSON budget",depth,err)}
 }
}
`)
}

func TestGeneratedModelDepth(t *testing.T) {
	runGeneratedFixture(t, modelDepthSource, modelDepthAssertions+modelDepthBenchmarks, modelDepthFixtureIDs())
}

func modelDepthFixtureIDs() StableIDs {
	return StableIDs{ModelFields: map[string]map[string]int{
		"DepthNode":                   {"value": 1, "child": 2, "children": 3, "payload": 4},
		"DepthBranch":                 {"value": 1, "child": 2, "children": 3, "payload": 4},
		"DepthEnvelope":               {"id": 1},
		"ModelChain":                  {"next": 1, "leaf": 2, "children": 3},
		"DepthLeaf":                   {"value": 1},
		"LeafChain":                   {"next": 1, "leaf": 2, "empty": 3},
		"readColumnarDepthNodeDepth":  {"value": 1},
		"writeColumnarDepthNodeDepth": {"value": 1},
		"DepthEmpty":                  {},
		"RequiredRoot":                {"node": 1, "children": 2},
	}}
}

func TestCodecDepthNamesRemainCompilationLocal(t *testing.T) {
	base := defaultGenerator()
	declarations := []*ast.ModelDecl{{Name: "Target"}, {Name: "writeColumnarTargetDepth"}}
	source := &semantic.Result{Files: []*ast.File{{Enums: []*ast.EnumDecl{
		{Name: "Role", Values: []string{"USER"}},
		{Name: "read", Values: []string{"ColumnarTargetDepth"}},
	}}}}
	owned := base.withCodecDepthNames(source, declarations)
	if owned == base || base.codecDepthNames != nil {
		t.Fatal("mutated original generator")
	}
	if owned.depthColumnarFunction("ReadColumnarTarget") != "readColumnarTargetDepth_" || owned.depthColumnarFunction("WriteColumnarTarget") != "writeColumnarTargetDepth_" {
		t.Fatal("codec names collided with source symbols")
	}
	plain := &semantic.Result{Files: []*ast.File{{Enums: []*ast.EnumDecl{{Name: "Role", Values: []string{"USER"}}}}}}
	if base.withCodecDepthNames(plain, declarations[:1]) != base || base.codecDepthNames != nil {
		t.Fatal("ordinary compilation retained alias state")
	}
}

const modelDepthSource = `
type DepthNode { value: Int child: DepthBranch? children: [DepthBranch]? payload: JSON? }
type DepthBranch { value: Int child: DepthNode? children: [DepthNode]? payload: JSON? }
model DepthEnvelope @noTime { id: Int @id }
type ModelChain { next: ModelChain? leaf: DepthEnvelope? children: [DepthEnvelope]? }
type DepthLeaf { value: Int }
type LeafChain { next: LeafChain? leaf: DepthLeaf? empty: DepthEmpty? }
type DepthEmpty {}
type RequiredRoot { node: DepthNode children: [DepthNode] }
enum readColumnarDepthNode { Depth_ }
type readColumnarDepthNodeDepth { value: Int }
type writeColumnarDepthNodeDepth { value: Int }
`

const modelDepthAssertions = `package generated
import("bytes";"errors";"encoding/json";"fmt";"strings";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")

var _ func(DepthNode,*api.ResponseBuf,[]byte)error = DepthNode.WriteLuxo
var _ func(*DepthNode,*codec.Decoder) = (*DepthNode).ReadLuxo
var _ func(*DepthNode,*codec.Decoder) = (*DepthNode).ReadLuxoInput
var _ func(*DepthNode,[]byte)error = (*DepthNode).ReadJSONInput
var _ func([]byte)([]DepthNode,error) = ReadColumnarDepthNode
var _ func(*api.ResponseBuf,[]DepthNode,[]byte)error = WriteColumnarDepthNode

func depthJSON(depth int) *json.RawMessage {
 raw:=json.RawMessage(strings.Repeat("[",depth)+"0"+strings.Repeat("]",depth));return &raw
}
func depthNode(depth int,lists bool,generic int)DepthNode {
 node:=DepthNode{Value:1,Children:[]DepthBranch{},Payload:depthJSON(0)}
 if depth==1{node.Payload=depthJSON(generic)}else{
  child:=depthBranch(depth-1,lists,generic);if lists{node.Children=[]DepthBranch{child}}else{node.Child=&child}
 };return node
}
func depthBranch(depth int,lists bool,generic int)DepthBranch {
 node:=DepthBranch{Value:1,Children:[]DepthNode{},Payload:depthJSON(0)}
 if depth==1{node.Payload=depthJSON(generic)}else{
  child:=depthNode(depth-1,lists,generic);if lists{node.Children=[]DepthNode{child}}else{node.Child=&child}
 };return node
}

// Hostile input/response bytes are constructed without the generated writer.
func depthWire(depth int,lists bool,generic int,input bool)[]byte {
 enc:=codec.NewEncoder(nil);enc.WriteVarint(0);enc.WriteFieldInt(1,1)
 var child []byte;if depth>1{child=depthWire(depth-1,lists,generic,input)}
 enc.WriteFieldHeader(2)
 if depth>1&&!lists{
  enc.WritePresent();if input{enc=codec.NewEncoder(append(enc.Bytes(),child...))}else{enc.WriteBytes(child)}
 }else{enc.WriteNull()}
 enc.WriteFieldHeader(3);enc.WritePresent()
 if input{
  count:=0;if depth>1&&lists{count=1};if err:=enc.WriteArrayHeader(count);err!=nil{panic(err)}
  if count==1{enc=codec.NewEncoder(append(enc.Bytes(),child...))}
 }else{column:=[]byte{0,0,0};if depth>1&&lists{column=append([]byte{1},child...)};enc.WriteBytes(column)}
 enc.WriteFieldHeader(4);enc.WritePresent();level:=0;if depth==1{level=generic};enc.WriteString(string(*depthJSON(level)));enc.WriteEnd();return enc.Bytes()
}
func verifyDepth(t *testing.T,node DepthNode,depth int,lists bool,generic int) {
 t.Helper();raw,err:=json.Marshal(node);if err!=nil{t.Fatal(err)};want,err:=json.Marshal(depthNode(depth,lists,generic));if err!=nil{t.Fatal(err)}
 if !bytes.Equal(raw,want){t.Fatalf("decoded value differs: got=%s want=%s",raw,want)}
}
func expectDepth(t *testing.T,depth int,err error) {
 t.Helper();if depth<=64{if err!=nil{t.Fatal("legal depth rejected",err)}}else if !errors.Is(err,codec.ErrModelDepth)||!strings.Contains(err.Error(),"64"){t.Fatalf("object depth %d requires limit64 error, got %v",depth,err)}
}
func TestModelDepthSixEntries(t *testing.T) {
 for _,depth:=range []int{1,63,64,65,128}{for _,lists:=range []bool{false,true}{
  name:=fmt.Sprintf("%d/lists=%t",depth,lists);node:=depthNode(depth,lists,128)
  row:=depthWire(depth,lists,128,false);input:=depthWire(depth,lists,128,true)
  raw,err:=json.Marshal(node);if err!=nil{t.Fatal(err)}
  cases:=map[string]func(*testing.T)error{
   "write-row":func(t *testing.T)error{buf:=&api.ResponseBuf{};err:=node.WriteLuxo(buf,nil);if err==nil&&!bytes.Equal(buf.B,row){t.Fatal("row bytes changed")};return err},
   "write-column":func(t *testing.T)error{buf:=&api.ResponseBuf{};err:=WriteColumnarDepthNode(buf,[]DepthNode{node},nil);if err==nil&&!bytes.Equal(buf.B,append([]byte{1},row...)){t.Fatal("column bytes changed")};return err},
   "read-row":func(t *testing.T)error{var value DepthNode;dec:=codec.NewDecoder(row);value.ReadLuxo(dec);err:=dec.Finish();if err==nil{verifyDepth(t,value,depth,lists,128)};return err},
   "read-column":func(t *testing.T)error{values,err:=ReadColumnarDepthNode(append([]byte{1},row...));if err==nil{if len(values)!=1{t.Fatal("wrong row count")};verifyDepth(t,values[0],depth,lists,128)}else if values!=nil{t.Fatal("partial column result")};return err},
   "read-input":func(t *testing.T)error{var value DepthNode;dec:=codec.NewDecoder(input);value.ReadLuxoInput(dec);err:=dec.Finish();if err==nil{verifyDepth(t,value,depth,lists,128)};return err},
   "read-json":func(t *testing.T)error{value:=DepthNode{Value:99};err:=value.ReadJSONInput(raw);if err==nil{verifyDepth(t,value,depth,lists,128)}else if value.Value!=99{t.Fatal("partial JSON publication")};return err},
  }
  for kind,call:=range cases{t.Run(name+"/"+kind,func(t *testing.T){expectDepth(t,depth,call(t))})}
 }}
}
func TestModelDepthJSONBudget(t *testing.T){
 for _,lists:=range []bool{false,true}{node:=depthNode(64,lists,129)
  buf:=&api.ResponseBuf{};if err:=node.WriteLuxo(buf,nil);err==nil{t.Fatal("JSON129 writer accepted")}
  for _,input:=range []bool{false,true}{dec:=codec.NewDecoder(depthWire(64,lists,129,input));var value DepthNode;if input{value.ReadLuxoInput(dec)}else{value.ReadLuxo(dec)};if err:=dec.Finish();err==nil{t.Fatal("JSON129 reader accepted")}}
  raw,err:=json.Marshal(node);if err!=nil{t.Fatal(err)};var value DepthNode;if err:=value.ReadJSONInput(raw);err==nil{t.Fatal("JSON129 input accepted")}
 }
}
func TestModelDepthSiblingsAndRecovery(t *testing.T){
 node:=depthNode(64,false,128);node.Children=[]DepthBranch{depthBranch(63,true,128),depthBranch(63,false,128)}
 var buf api.ResponseBuf
 for _,depth:=range []int{65,64,65,64}{value:=depthNode(depth,false,128);if depth==64{value=node}
  buf.Reset();err:=value.WriteLuxo(&buf,nil);expectDepth(t,depth,err)
  if err==nil{var restored DepthNode;dec:=codec.NewDecoder(buf.B);restored.ReadLuxo(dec);if err:=dec.Finish();err!=nil||len(restored.Children)!=2{t.Fatal("sibling/recovery",err)}}
 }
 buf.Reset();if err:=WriteColumnarDepthNode(&buf,[]DepthNode{node,node},nil);err!=nil{t.Fatal(err)}
 values,err:=ReadColumnarDepthNode(buf.B);if err!=nil||len(values)!=2{t.Fatal("independent rows",err)}
 raw,err:=json.Marshal([]DepthNode{node,node});if err!=nil{t.Fatal(err)};values,err=ReadJSONInputDepthNodeList(raw);if err!=nil||len(values)!=2{t.Fatal("independent JSON list",err)}
}
func TestModelDepthNativeCycles(t *testing.T){
 probe:=depthNode(65,false,0);if err:=probe.WriteLuxo(&api.ResponseBuf{},nil);err==nil{t.Fatal("guard absent; do not send unchecked native cycle to baseline")}
 for _,lists:=range []bool{false,true}{node:=DepthNode{Value:1};branch:=DepthBranch{Value:1,Child:&node};if lists{node.Children=[]DepthBranch{branch}}else{node.Child=&branch}
  var buf api.ResponseBuf;expectDepth(t,65,node.WriteLuxo(&buf,nil));buf.Reset();expectDepth(t,65,WriteColumnarDepthNode(&buf,[]DepthNode{node},nil))
 }
}
func TestModelDepthMixedRootAndLeaf(t *testing.T){
 for _,depth:=range []int{63,64}{
  var buf api.ResponseBuf
  model:=&DepthEnvelope{Id:1};chainModel:=ModelChain{Leaf:model,Children:[]*DepthEnvelope{model}}
  for index:=1;index<depth;index++{previous:=chainModel;chainModel=ModelChain{Next:&previous}}
  expectDepth(t,depth+1,chainModel.WriteLuxo(&buf,nil));buf.Reset();expectDepth(t,depth+1,WriteColumnarModelChain(&buf,[]ModelChain{chainModel},nil))
  buf.Reset();if err:=WriteColumnarDepthEnvelope(&buf,[]*DepthEnvelope{model},nil);err!=nil{t.Fatal(err)};buf.Reset();if err:=WriteColumnarDepthEnvelopeValues(&buf,[]DepthEnvelope{*model},nil);err!=nil{t.Fatal(err)}
  chain:=LeafChain{Leaf:&DepthLeaf{Value:1}};for index:=1;index<depth;index++{previous:=chain;chain=LeafChain{Next:&previous}}
  buf.Reset();expectDepth(t,depth+1,chain.WriteLuxo(&buf,nil))
 }
}
func appendNested(enc codec.Encoder,child []byte,input bool)codec.Encoder {
 if input{return codec.NewEncoder(append(enc.Bytes(),child...))};enc.WriteBytes(child);return enc
}
func chainWire(depth int,leaf []byte,field int,input bool,list bool)[]byte {
 enc:=codec.NewEncoder(nil);enc.WriteVarint(0);enc.WriteFieldHeader(field);enc.WritePresent();enc=appendNested(enc,leaf,input)
 if list{enc.WriteFieldHeader(3);enc.WritePresent();if input{_ = enc.WriteArrayHeader(1);enc=appendNested(enc,leaf,true)}else{enc.WriteBytes(append([]byte{1},leaf...))}}
 enc.WriteEnd();raw:=enc.Bytes()
 for level:=1;level<depth;level++{enc=codec.NewEncoder(nil);enc.WriteVarint(0);enc.WriteFieldHeader(1);enc.WritePresent();enc=appendNested(enc,raw,input);enc.WriteEnd();raw=enc.Bytes()}
 return raw
}
func TestModelDepthMixedReaders(t *testing.T){
 for _,depth:=range []int{63,64}{
  for _,input:=range []bool{false,true}{
   raw:=chainWire(depth,[]byte{0,1,2,0},2,input,true);var model ModelChain;dec:=codec.NewDecoder(raw)
   if input{model.ReadLuxoInput(dec)}else{model.ReadLuxo(dec)};expectDepth(t,depth+1,dec.Finish())
   if depth==63{cursor:=&model;for cursor.Next!=nil{cursor=cursor.Next};if cursor.Leaf.Id!=1||len(cursor.Children)!=1||cursor.Children[0].Id!=1{t.Fatal("cross-model result")}}
   for _,empty:=range []bool{false,true}{leaf:=[]byte{0,1,2,0};field:=2;if empty{leaf=[]byte{0,0};field=3}
    raw:=chainWire(depth,leaf,field,input,false);var value LeafChain;dec:=codec.NewDecoder(raw);if input{value.ReadLuxoInput(dec)}else{value.ReadLuxo(dec)};expectDepth(t,depth+1,dec.Finish())
   }
  }
  model:=ModelChain{Leaf:&DepthEnvelope{Id:1},Children:[]*DepthEnvelope{{Id:1}}};empty:=LeafChain{Empty:&DepthEmpty{}}
  for level:=1;level<depth;level++{oldModel,oldEmpty:=model,empty;model=ModelChain{Next:&oldModel};empty=LeafChain{Next:&oldEmpty}}
  raw,err:=json.Marshal(model);if err!=nil{t.Fatal(err)};var restored ModelChain;expectDepth(t,depth+1,restored.ReadJSONInput(raw))
  raw,err=json.Marshal(empty);if err!=nil{t.Fatal(err)};var restoredEmpty LeafChain;expectDepth(t,depth+1,restoredEmpty.ReadJSONInput(raw));var buf api.ResponseBuf;expectDepth(t,depth+1,empty.WriteLuxo(&buf,nil))
  values,err:=ReadColumnarModelChain(append([]byte{1},chainWire(depth,[]byte{0,1,2,0},2,false,true)...));expectDepth(t,depth+1,err);if err==nil&&len(values)!=1{t.Fatal("model column result")}
 }
}
func TestModelDepthRequiredEdges(t *testing.T){
 for _,depth:=range []int{63,64}{for _,lists:=range []bool{false,true}{
  node:=depthNode(depth,lists,128);value:=RequiredRoot{Node:&node,Children:[]DepthNode{node,node}};var buf api.ResponseBuf
  expectDepth(t,depth+1,value.WriteLuxo(&buf,nil));buf.Reset();expectDepth(t,depth+1,WriteColumnarRequiredRoot(&buf,[]RequiredRoot{value},nil))
  raw,err:=json.Marshal(value);if err!=nil{t.Fatal(err)};var restored RequiredRoot;expectDepth(t,depth+1,restored.ReadJSONInput(raw))
  for _,input:=range []bool{false,true}{child:=depthWire(depth,lists,128,input);enc:=codec.NewEncoder(nil);enc.WriteVarint(0);enc.WriteFieldHeader(1);enc=appendNested(enc,child,input);enc.WriteFieldHeader(2)
   if input{_ = enc.WriteArrayHeader(1);enc=appendNested(enc,child,true)}else{enc.WriteBytes(append([]byte{1},child...))};enc.WriteEnd();wire:=enc.Bytes()
   dec:=codec.NewDecoder(wire);if input{restored.ReadLuxoInput(dec)}else{restored.ReadLuxo(dec)};expectDepth(t,depth+1,dec.Finish())
   if !input{_,err:=ReadColumnarRequiredRoot(append([]byte{1},wire...));expectDepth(t,depth+1,err)}
  }
 }}
}
func TestModelDepthPreservesEarlierError(t *testing.T){
 previous:=fmt.Errorf("earlier error");dec:=codec.NewDecoder(nil);dec.Fail(previous);var value DepthLeaf;value.readLuxoDepth(dec,65);if dec.Err()!=previous{t.Fatal("depth replaced first failure")}
}

`

const modelDepthBenchmarks = `
func BenchmarkGeneratedModelDepth(b *testing.B) {
 for _,depth:=range []int{1,64}{for _,lists:=range []bool{false,true}{
  node:=depthNode(depth,lists,128);row:=depthWire(depth,lists,128,false);input:=depthWire(depth,lists,128,true);column:=append([]byte{1},row...)
  raw,err:=json.Marshal(node);if err!=nil{b.Fatal(err)}
  for _,kind:=range []string{"write-row","write-column","read-row","read-column","read-input","read-json"}{
   b.Run(fmt.Sprintf("%s/depth%d/list%t",kind,depth,lists),func(b *testing.B){
    var buf api.ResponseBuf;buf.B=make([]byte,0,len(row)*2+1024);var value DepthNode;var values []DepthNode;nodes:=[]DepthNode{node}
    b.ReportAllocs()
    for b.Loop(){
     switch kind{
     case "write-row":buf.Reset();if err:=node.WriteLuxo(&buf,nil);err!=nil{b.Fatal(err)}
     case "write-column":buf.Reset();if err:=WriteColumnarDepthNode(&buf,nodes,nil);err!=nil{b.Fatal(err)}
     case "read-row":value=DepthNode{};dec:=codec.NewDecoder(row);value.ReadLuxo(dec);if err:=dec.Finish();err!=nil{b.Fatal(err)}
     case "read-column":values,err=ReadColumnarDepthNode(column);if err!=nil{b.Fatal(err)}
     case "read-input":value=DepthNode{};dec:=codec.NewDecoder(input);value.ReadLuxoInput(dec);if err:=dec.Finish();err!=nil{b.Fatal(err)}
     case "read-json":value=DepthNode{};if err:=value.ReadJSONInput(raw);err!=nil{b.Fatal(err)}
     }
    }
    if (kind=="write-row"||kind=="write-column")&&len(buf.B)==0{b.Fatal("missing output")}
    if (kind=="read-row"||kind=="read-input"||kind=="read-json")&&value.Value!=1{b.Fatal("missing decoded root")}
    if kind=="read-column"&&(len(values)!=1||values[0].Value!=1){b.Fatal("missing column root")}
   })
  }
 }}
}
func BenchmarkGeneratedJSONDiscardedDepth(b *testing.B) {
 node:=func(depth int,lists bool)string{raw,err:=json.Marshal(depthNode(depth,lists,128));if err!=nil{b.Fatal(err)};return string(raw)}
 samples:=map[string]string{
  "unique1":node(1,false),"unique64-child":node(64,false),"unique64-list":node(64,true),
  "scalar":"{\"value\":\"bad\",\"value\":1}",
  "json128":"{\"value\":1,\"payload\":"+string(*depthJSON(128))+",\"payload\":0}",
  "model64":"{\"value\":1,\"child\":"+node(63,false)+",\"child\":null}",
  "model-list64":"{\"value\":1,\"children\":["+node(63,true)+"],\"children\":[]}",
  "missing-members":"{\"value\":1,\"child\":{},\"child\":null}",
 }
 for _,name:=range []string{"unique1","unique64-child","unique64-list","scalar","json128","model64","model-list64","missing-members"}{b.Run(name,func(b *testing.B){raw:=[]byte(samples[name]);var value DepthNode;b.ReportAllocs()
  for b.Loop(){value=DepthNode{};if err:=value.ReadJSONInput(raw);err!=nil{b.Fatal(err)}}
  if value.Value!=1{b.Fatal("missing final value")}
 })}
}
func BenchmarkGeneratedJSONRejectedDepth(b *testing.B) {
 node,err:=json.Marshal(depthNode(64,false,0));if err!=nil{b.Fatal(err)}
 for name,body:=range map[string]string{
  "model65":"{\"child\":"+string(node)+",\"child\":null}",
  "json129":"{\"payload\":"+string(*depthJSON(129))+",\"payload\":0}",
 }{b.Run(name,func(b *testing.B){raw:=[]byte(body);b.ReportAllocs();for b.Loop(){value:=DepthNode{Value:99};if err:=value.ReadJSONInput(raw);err==nil||value.Value!=99{b.Fatal("missing atomic resource rejection",err)}}})}
}
`

func TestGeneratedJSONDiscardedDepth(t *testing.T) {
	runGeneratedFixture(t, modelDepthSource, modelDepthAssertions+discardedDepthAssertions, modelDepthFixtureIDs())
}

func TestGeneratedJSONDiscardedFlat(t *testing.T) {
	runGeneratedFixture(t, `type FlatPacket { data: JSON values: [JSON] scalar: Int scalars: [Int] }`, discardedFlatAssertions,
		StableIDs{ModelFields: map[string]map[string]int{"FlatPacket": {"data": 1, "values": 2, "scalar": 3, "scalars": 4}}})
}

const discardedDepthAssertions = `
func TestDiscardedDepthResources(t *testing.T){
 node:=func(depth,generic int)string{raw,err:=json.Marshal(depthNode(depth,false,generic));if err!=nil{t.Fatal(err)};return string(raw)}
 for _,sample:=range []struct{name,field,old,last string;valid bool}{
  {"model64-json128","child",node(63,128),"null",true},
  {"model65","child",node(64,0),"null",false},
  {"model-list64-json128","children","["+node(63,128)+"]","[]",true},
  {"model-list65","children","["+node(64,0)+"]","[]",false},
  {"json128","payload",string(*depthJSON(128)),"0",true},
  {"json129","payload",string(*depthJSON(129)),"0",false},
  {"ignored-scalar-type","value","\"bad\"","1",true},
  {"ignored-missing-members","child","{}","null",true},
  {"ignored-unknown-members","child","{\"unknown\":0}","null",true},
  {"ignored-model-shape","child","42","null",true},
  {"ignored-list-shape","children","{}","[]",true},
  {"ignored-model-array","child",string(*depthJSON(128)),"null",true},
  {"ignored-unknown-json129","child","{\"unknown\":"+string(*depthJSON(129))+"}","null",false},
  {"ignored-business-error-before-depth","child","{\"value\":\"bad\",\"child\":"+node(63,0)+"}","null",false},
  {"nested-overwritten-model","child","{\"child\":"+node(63,0)+",\"child\":null}","null",false},
  {"nested-overwritten-json","child","{\"payload\":"+string(*depthJSON(129))+",\"payload\":0}","null",false},
  {"unknown-json128","child","{\"unknown\":"+string(*depthJSON(128))+"}","null",true},
 }{t.Run(sample.name,func(t *testing.T){
  raw:=[]byte("{\""+sample.field+"\":"+sample.old+",\""+sample.field+"\":"+sample.last+"}")
  value:=DepthNode{Value:99};err:=value.ReadJSONInput(raw)
  if (err==nil)!=sample.valid{t.Fatalf("expected valid=%t, got %v",sample.valid,err)}
  if err!=nil&&value.Value!=99{t.Fatal("partial publication")}
  if err==nil{want:=DepthNode{};if err:=want.ReadJSONInput([]byte("{\""+sample.field+"\":"+sample.last+"}"));err!=nil{t.Fatal(err)};a,_:=json.Marshal(value);b,_:=json.Marshal(want);if !bytes.Equal(a,b){t.Fatal("last value changed")}}
 })}
 raw:=[]byte("{\"child\":"+node(64,0)+",\"ch\\u0069ld\":null}");var value DepthNode;if err:=value.ReadJSONInput(raw);err==nil{t.Fatal("escaped duplicate hides old model65")}
 for _,last:=range []string{"{\"unknown\":0}","42"}{if err:=value.ReadJSONInput([]byte("{\"child\":{},\"child\":"+last+"}"));err==nil{t.Fatal("invalid final model accepted")}}
}
`

const discardedFlatAssertions = `package generated
import("encoding/json";"strings";"testing")
func TestFlatDiscardedDepthResources(t *testing.T){
 for _,field:=range []string{"data","values","scalar","scalars"}{for _,depth:=range []int{128,129}{
  raw:=strings.Repeat("[",depth)+"0"+strings.Repeat("]",depth);last:="0"
  if field=="values"||field=="scalars"{raw="["+raw+"]";last="[]"}
  body:=[]byte("{\""+field+"\":"+raw+",\""+field+"\":"+last+"}")
  var value FlatPacket;err:=value.ReadJSONInput(body);if (err==nil)!=(depth==128){t.Fatalf("field=%s depth=%d error=%v",field,depth,err)}
  if err==nil{got,_:=json.Marshal(value);var want FlatPacket;if err:=want.ReadJSONInput([]byte("{\""+field+"\":"+last+"}"));err!=nil{t.Fatal(err)};expected,_:=json.Marshal(want);if string(got)!=string(expected){t.Fatal("last value changed")}}
 }}
 for _,raw:=range []string{"{\"scalar\":\"bad\",\"scalar\":42}","{\"values\":{},\"values\":[]}","{\"scalars\":[null,{},\"bad\"],\"scalars\":[]}"}{var value FlatPacket;if err:=value.ReadJSONInput([]byte(raw));err!=nil{t.Fatal("ignored business error leaked",err)}}
}
`
