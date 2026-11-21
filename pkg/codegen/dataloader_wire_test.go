package codegen

import (
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGeneratedNamedLoaderPreservesResponseIdentity(t *testing.T) {
	model := &ast.ModelDecl{Name: "Post", Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}}
	var source strings.Builder
	source.WriteString(`package generated
import("context";"fmt";"github.com/light-speak/luxo/pkg/lux";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/selection")
type App struct{DB fixtureDB}
type fixtureDB struct{rows []*Post}
type Post struct{Id int64;Expected any;Writes int}
var scanPost int
func(f fixtureDB)QueryRows(context.Context,int,string,...any)([]*Post,error){return f.rows,nil}
func selectPostSQLColumns([]*selection.Field)[]string{return []string{"id"}}
func(p *Post)WriteLuxo(buf *api.ResponseBuf,_ []byte)error{
 if buf.Identity!=p.Expected{return fmt.Errorf("identity lost for row %d",p.Id)}
 p.Writes++;buf.AppendByte(byte(p.Id));return nil
}
`)
	generateRemoteNamedLoadHandler(&source, model, loadCallInfo{modelName: "Post", argNames: []string{"id"}, argTypes: []string{"int64"}, argTypeNames: []string{"Int"}})
	runGeneratedProtocolPackage(t, source.String(), `package generated
import("testing";"strings";"net/http/httptest";"github.com/light-speak/luxo/pkg/lux/api")
func TestNamedLoaderIdentity(t *testing.T){
 for _,identity:=range []any{int64(42),nil,int64(99)}{
  rows:=[]*Post{{Id:1,Expected:identity},{Id:1,Expected:identity},{Id:2,Expected:identity}}
  app:=&App{DB:fixtureDB{rows:rows}}
  req,err:=api.ParseRequest(httptest.NewRequest("POST","/",strings.NewReader("{\"$api\":\"load\",\"id\":[1,2]}")));if err!=nil{t.Fatal(err)}
  req.Buf=&api.ResponseBuf{Identity:identity}
  if err:=handleLoadPostById(app)(t.Context(),req);err!=nil{t.Fatal(err)}
  for _,row:=range rows{if row.Writes!=1{t.Fatal("missing or repeated row",row.Id,row.Writes)}}
  if req.Buf.Identity!=identity{t.Fatal("parent identity changed")}
 }
}
`)
}

func TestGeneratedRemoteLoaderRejectsMalformedRows(t *testing.T) {
	runGeneratedProtocolPackage(t, remoteLoaderWireSource(t), remoteLoaderWireAssertions)
}

func remoteLoaderWireSource(t *testing.T) string {
	return remoteLoaderWireSourceWithKeyID(t, 1)
}

func remoteLoaderWireSourceWithKeyID(t *testing.T, fieldID int) string {
	t.Helper()
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"svc:batchLoad:Post": 42, "svc:load:Post:id": 43}, APIParams: map[string]map[string]int{"svc:batchLoad:Post": {"keys": fieldID}, "svc:load:Post:id": {"id": 1}}, ModelFields: map[string]map[string]int{"Post": {"id": 1}}}})
	model := &ast.ModelDecl{Name: "Post", Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}}
	var source strings.Builder
	source.WriteString(`package generated
import("context";"fmt";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/luvia";"github.com/light-speak/luxo/pkg/lux/rpc";"github.com/light-speak/luxo/pkg/lux/schema";"github.com/light-speak/luxo/pkg/lux/selection")
`)
	generateModel(&source, model, nil)
	g.generateReadLuxo(&source, model, nil)
	source.WriteString(`type loader struct{load func(context.Context,[]int64,[]*selection.Field)(map[int64]*Post,error)}
func newLoader(rpcClients map[string]*rpc.Client,registry *schema.Schema)loader{return loader{
`)
	g.generateRemoteByPKBatchFunc(&source, "Post", []string{"id"}, "")
	source.WriteString("}}\n")
	source.WriteString(`type namedLoader struct{load func(context.Context,[]int64,[]*selection.Field)(map[int64][]*Post,error)}
func newNamedLoader(rpcClients map[string]*rpc.Client,registry *schema.Schema)namedLoader{return namedLoader{
`)
	g.generateRemoteNamedLoadBatchFunc(&source, loadCallInfo{modelName: "Post", argNames: []string{"id"}, argTypes: []string{"int64"}, argTypeNames: []string{"Int"}}, []string{"id"})
	source.WriteString("}}\n")

	return source.String()
}

func TestGeneratedRemoteKeyParameterTypes(t *testing.T) {
	var source strings.Builder
	source.WriteString(`package generated
import("github.com/google/uuid";"github.com/light-speak/luxo/pkg/lux/codec")
`)
	for _, kind := range []struct{ name, goType string }{{"Int", "int64"}, {"String", "string"}, {"UUID", "uuid.UUID"}} {
		for _, fieldID := range []int{7, 2147483645} {
			source.WriteString("func encode" + kind.name + strconv.Itoa(fieldID) + "(keys []" + kind.goType + ")([]byte,error){enc:=codec.NewEncoder(nil)\n")
			generateRemoteKeyEncoding(&source, kind.name, fieldID)
			source.WriteString("enc.WriteEnd();return enc.Bytes(),nil}\n")
		}
	}
	runGeneratedProtocolPackage(t, source.String(), `package generated
import("testing";"github.com/google/uuid";"github.com/light-speak/luxo/pkg/lux/codec")
func TestKeyParameterTypes(t *testing.T){
 for _,encode:=range []func()([]byte,error){
  func()([]byte,error){return encodeInt7(make([]int64,codec.MaxArrayElements+1))},
  func()([]byte,error){return encodeString7(make([]string,codec.MaxArrayElements+1))},
  func()([]byte,error){return encodeUUID7(make([]uuid.UUID,codec.MaxArrayElements+1))},
 }{data,err:=encode();if data!=nil||err!=codec.ErrInvalidArrayCount{t.Fatal("oversized remote keys",len(data),err)}}
 id:=uuid.MustParse("531f2d00-20e5-47f2-b874-22b6fc76328b")
 for _,sample:=range []struct{id int;encode func()([]byte,error);read func(*codec.Decoder)bool}{
  {7,func()([]byte,error){return encodeInt7([]int64{-9223372036854775808,9223372036854775807})},func(d *codec.Decoder)bool{v:=d.ReadIntArray();return len(v)==2&&v[0]==-9223372036854775808&&v[1]==9223372036854775807}},
  {2147483645,func()([]byte,error){return encodeInt2147483645([]int64{42})},func(d *codec.Decoder)bool{v:=d.ReadIntArray();return len(v)==1&&v[0]==42}},
  {7,func()([]byte,error){return encodeString7([]string{"租户/42"})},func(d *codec.Decoder)bool{v:=d.ReadStringArray();return len(v)==1&&v[0]=="租户/42"}},
  {2147483645,func()([]byte,error){return encodeString2147483645([]string{""})},func(d *codec.Decoder)bool{v:=d.ReadStringArray();return len(v)==1&&v[0]==""}},
  {7,func()([]byte,error){return encodeUUID7([]uuid.UUID{id})},func(d *codec.Decoder)bool{v:=d.ReadUUIDArray();return len(v)==1&&v[0]==[16]byte(id)}},
  {2147483645,func()([]byte,error){return encodeUUID2147483645([]uuid.UUID{id,uuid.Nil})},func(d *codec.Decoder)bool{v:=d.ReadUUIDArray();return len(v)==2&&v[0]==[16]byte(id)&&v[1]==[16]byte{}}},
 }{
  data,err:=sample.encode();if err!=nil{t.Fatal(err)}
  d:=codec.NewDecoder(data);if !d.NextField()||d.FieldID()!=sample.id||!sample.read(d){t.Fatalf("incorrect key encoding: %d/%x",sample.id,data)}
  if d.NextField()||d.Finish()!=nil{t.Fatal("invalid parameter boundary")}
 }
}
`)
}

func TestGeneratedRemoteLoaderUsesStableKeyParameter(t *testing.T) {
	for _, fieldID := range []int{7, 2147483645} {
		t.Run(strconv.Itoa(fieldID), func(t *testing.T) {
			assertions := strings.ReplaceAll(remoteLoaderStableKeyAssertions, "KEY_FIELD_ID", strconv.Itoa(fieldID))
			runGeneratedProtocolPackage(t, remoteLoaderWireSourceWithKeyID(t, fieldID), assertions)
		})
	}
}

const remoteLoaderStableKeyAssertions = `package generated
import("context";"errors";"net";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/rpc";"github.com/light-speak/luxo/pkg/lux/schema")
func TestStableKeyParameter(t *testing.T){
 router:=api.NewRouter()
 router.HandleInternal("load",func(_ context.Context,req *api.Request)error{
  keys,err:=req.ParamIntArray("keys");if err!=nil{return err};if len(keys)!=1||keys[0]!=42{return errors.New("incorrect key values")}
  var enc codec.Encoder;enc.WriteFieldInt(1,keys[0]);enc.WriteEnd();req.Buf.B=append(req.Buf.B,1,0);req.Buf.B=append(req.Buf.B,enc.Bytes()...);return nil
 })
 if err:=router.Registry.Register("load",42);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterParams("load",[]api.ParamMeta{{Name:"keys",Type:"Int",IsList:true,FieldID:KEY_FIELD_ID}});err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 done:=make(chan error,1);go func(){done<-server.Serve(listener)}();defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 registry:=schema.New();if err:=registry.RegisterModel(&schema.Model{Name:"Post",Fields:[]schema.Field{{ID:1,Name:"id",Type:schema.FieldInt}}});err!=nil{t.Fatal(err)}
 rows,err:=newLoader(map[string]*rpc.Client{"post":client},registry).load(t.Context(),[]int64{42},nil)
 if err!=nil||len(rows)!=1||rows[42]==nil||rows[42].Id!=42{t.Fatalf("stable parameter lost: %v/%v",rows,err)}
}
`

const remoteLoaderWireAssertions = `package generated
import("context";"net";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/rpc";"github.com/light-speak/luxo/pkg/lux/schema")
func TestRemoteLoaderWire(t *testing.T){
 var enc codec.Encoder;enc.WriteFieldInt(1,42);enc.WriteEnd()
 row:=append([]byte{0},enc.Bytes()...)
 valid:=append(codec.AppendVarint(nil,1),row...)
 for _,tc:=range []struct{name string;data []byte;bad bool}{
  {"valid",valid,false},{"empty",[]byte{0},false},
  {"missing-row",[]byte{1},true},
  {"truncated-row",valid[:len(valid)-1],true},
  {"trailing-row",append(append([]byte{},valid...),0),true},
  {"trailing-empty",[]byte{0,0},true},
  {"excess-count",codec.AppendVarint(nil,1024),true},
  {"excess-rows",append(append(codec.AppendVarint(nil,2),row...),row...),true},
 }{t.Run(tc.name,func(t *testing.T){assertRemotePayload(t,tc.data,tc.bad)})}
}
func assertRemotePayload(t *testing.T,payload []byte,bad bool,named ...bool){
 t.Helper();router:=api.NewRouter()
 router.HandleInternal("load",func(_ context.Context,req *api.Request)error{req.Buf.B=append(req.Buf.B,payload...);return nil})
 if err:=router.Registry.Register("load",42);err!=nil{t.Fatal(err)}
 router.HandleInternal("named",router.ExportHandlers()["load"])
 if err:=router.Registry.Register("named",43);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterParams("named",[]api.ParamMeta{{Name:"keys",Type:"Int",IsList:true,FieldID:1}});err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterParams("load",[]api.ParamMeta{{Name:"keys",Type:"Int",IsList:true,FieldID:1}});err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 registry:=schema.New();if err:=registry.RegisterModel(&schema.Model{Name:"Post",Fields:[]schema.Field{{ID:1,Name:"id",Type:schema.FieldInt}}});err!=nil{t.Fatal(err)}
 if len(named)>0{
  result,err:=newNamedLoader(map[string]*rpc.Client{"post":client},registry).load(t.Context(),[]int64{42},nil)
  if bad{if err==nil||result!=nil{t.Fatalf("malformed named payload accepted: %v %v",result,err)};return}
  if err!=nil||len(result)!=1{t.Fatalf("valid named payload failed: %v %v",result,err)}
  return
 }
 loader:=newLoader(map[string]*rpc.Client{"post":client},registry)
 result,err:=loader.load(t.Context(),[]int64{42},nil)
 if bad{if err==nil||result!=nil{t.Fatalf("malformed payload accepted: result=%v error=%v",result,err)};return}
 if err!=nil{t.Fatal(err)}
 if len(payload)>1&&(len(result)!=1||result[42]==nil){t.Fatal("valid row changed")}
 if len(payload)==1&&len(result)!=0{t.Fatal("empty response changed")}
}
func TestNamedLoaderWire(t *testing.T){
 var enc codec.Encoder;enc.WriteFieldInt(1,42);enc.WriteEnd()
 row:=append([]byte{0},enc.Bytes()...)
 valid:=codec.AppendBytes([]byte{1,1},row)
 trailingRow:=codec.AppendBytes([]byte{1,1},append(append([]byte{},row...),0))
 for _,tc:=range []struct{name string;data []byte;bad bool}{
  {"valid",valid,false},{"empty-group",[]byte{1,0},false},
  {"missing-group",[]byte{1},true},{"missing-item",[]byte{1,1},true},
  {"trailing-item",trailingRow,true},{"truncated-item",valid[:len(valid)-1],true},
  {"count-without-data",append([]byte{1},codec.AppendVarint(nil,1024)...),true},
  {"trailing-group",append(append([]byte{},valid...),0),true},
 }{t.Run(tc.name,func(t *testing.T){assertRemotePayload(t,tc.data,tc.bad,true)})}
}

`
