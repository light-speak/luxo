package codegen

import (
	"strings"
	"testing"
)

func TestGeneratedNullablePrimaryKeyLoad(t *testing.T) {
	generated := loadDemandModuleFixture(t, nullableLoadDeclarations("Int", "id"))
	runGeneratedCompilation(t, generated, nullablePrimaryKeyAssertions, nullablePrimaryKeyHTTPAssertions, nullablePrimaryKeyRPCAssertions)
}

func nullableLoadDeclarations(keyType, parameter string) string {
	return strings.NewReplacer("KEY_TYPE", keyType, "KEY_PARAM", parameter).Replace(`
api nullable(KEY_PARAM: KEY_TYPE): Product? { return Product.load(KEY_PARAM) }
api guarded(KEY_PARAM: KEY_TYPE): String { val row = Product.load(KEY_PARAM) ?: throw error.NotFound return row.name }
api safe(KEY_PARAM: KEY_TYPE): String? { return Product.load(KEY_PARAM)?.name }
api fallback(KEY_PARAM: KEY_TYPE): String { return Product.load(KEY_PARAM)?.name ?: "none" }
api present(KEY_PARAM: KEY_TYPE): Boolean { val row = Product.load(KEY_PARAM) return row != null }
api absent(KEY_PARAM: KEY_TYPE): Boolean { return Product.load(KEY_PARAM) == null }
fn loadMaybe(KEY_PARAM: KEY_TYPE): Product? { return Product.load(KEY_PARAM) }
api nullableThroughFunction(KEY_PARAM: KEY_TYPE): Product? { return loadMaybe(KEY_PARAM) }
api safeThroughFunction(KEY_PARAM: KEY_TYPE): String? { return loadMaybe(KEY_PARAM)?.name }
`)
}

const nullablePrimaryKeyAssertions = `package generated
import("context";"encoding/json";"errors";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/dataloader";"github.com/light-speak/luxo/pkg/lux/selection")
func TestNullablePrimaryKeyLocal(t *testing.T){
 app:=&App{};cfg:=dataloader.DefaultConfig();cfg.MaxBatch=1
 app.SetLoaders(NewLoaders(cfg,func(_ context.Context,keys []int64,fields []*selection.Field)(map[int64]*Product,error){
  if fields==nil{t.Fatal("implicit full projection")}
  if got:=selection.Format(fields);got!=""&&got!="name"&&got!="name,price"{t.Fatal("unexpected projection",got)}
  rows:=map[int64]*Product{};if keys[0]==42{rows[42]=&Product{Id:42,Name:"wanted"}};return rows,nil
 }))
 assertNullablePrimaryKeyHTTP(t,app,"id",int64(42),int64(0),"wanted")
}
func TestNullablePrimaryKeyFailures(t *testing.T){
 failure:=errors.New("load failed");app:=&App{};cfg:=dataloader.DefaultConfig();cfg.MaxBatch=1
 app.SetLoaders(NewLoaders(cfg,func(context.Context,[]int64,[]*selection.Field)(map[int64]*Product,error){return nil,failure}))
 for _,handler:=range []api.HandlerFunc{handleNullable(app),handleGuarded(app),handleSafe(app),handleFallback(app),handlePresent(app),handleAbsent(app)}{
  req:=&api.Request{Params:map[string]json.RawMessage{"id":json.RawMessage("42")},Select:[]*selection.Field{{Name:"name"}},Buf:&api.ResponseBuf{}}
  if err:=handler(t.Context(),req);!errors.Is(err,failure)||len(req.Buf.B)!=0{t.Fatal("load error became missing record or partial success",err,req.Buf.B)}
 }
}
`

const nullablePrimaryKeyHTTPAssertions = `package generated
import("bytes";"encoding/json";"net/http/httptest";"strings";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/selection";"github.com/light-speak/luxo/pkg/lux/schema")
func assertNullablePrimaryKeyHTTP(t *testing.T,app *App,param string,existingKey,missingKey any,value string){
 t.Helper();router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,name:=range []string{"nullable","guarded","safe","fallback","present","absent","nullableThroughFunction","safeThroughFunction"}{for _,missing:=range []bool{false,true}{for _,binary:=range []bool{false,true}{
  t.Run(name+"/"+boolLabel(missing)+"/"+boolLabel(binary),func(t *testing.T){
   kind:=strings.TrimSuffix(name,"ThroughFunction")
   key:=existingKey;if missing{key=missingKey};encoded,err:=json.Marshal(value);if err!=nil{t.Fatal(err)};want:=string(encoded)
   switch kind{case "nullable":want=` + "`" + `{"name":` + "`" + `+want+"}";if missing{want="null"};case "safe":if missing{want="null"};case "fallback":if missing{want=` + "`" + `"none"` + "`" + `};case "present":want=boolLabel(!missing);case "absent":want=boolLabel(missing)}
   params:=map[string]any{"$api":name,param:key};if kind=="nullable"{params["$select"]="name"}
   body,err:=json.Marshal(params);if err!=nil{t.Fatal(err)}
   if binary{var mask []byte;if kind=="nullable"{mask,err=schema.SelectToFieldMask([]*selection.Field{{Name:"name"}},router.Schema.Models["Product"],router.Schema);if err!=nil{t.Fatal(err)}};body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,mask,map[string]any{param:key},router.Registry.ParamOrder(name));if err!=nil{t.Fatal(err)}}
   request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
   response:=httptest.NewRecorder();router.ServeHTTP(response,request)
   if name=="guarded"&&missing{if response.Code!=404||bytes.Contains(response.Body.Bytes(),[]byte("runtime")){t.Fatal("missing row did not return NotFound",response.Code,response.Body.String())};return}
   if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
   if !binary{var envelope struct{Data json.RawMessage};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil||string(envelope.Data)!=want{t.Fatal("JSON null contract",response.Body.String(),want,err)};return}
   assertNullablePrimaryBinary(t,kind,missing,response.Body.Bytes(),value)
  })
 }}}
}
func boolLabel(value bool)string{if value{return "true"};return "false"}
func assertNullablePrimaryBinary(t *testing.T,name string,missing bool,data []byte,value string){
 t.Helper();dec:=codec.NewDecoder(data)
 switch name{
 case "nullable":body,null,err:=codec.UnwrapNullableRoot(data);if err!=nil||null!=missing{t.Fatal("nullable root",data,err)};if null{return};dec=codec.NewDecoder(body);var row Product;row.ReadLuxo(dec);if row.Name!=value{t.Fatal(row)}
 case "safe":got:=dec.ReadStringPtr();if missing{if got!=nil{t.Fatal("missing safe value",*got)}}else if got==nil||*got!=value{t.Fatal(got)}
 case "guarded","fallback":want:=value;if missing{want="none"};if got:=dec.ReadString();got!=want{t.Fatal(got,want)}
 case "present":if got:=dec.ReadBool();got==missing{t.Fatal(got,missing)}
 case "absent":if got:=dec.ReadBool();got!=missing{t.Fatal(got,missing)}
 }
 if err:=dec.Finish();err!=nil{t.Fatal(err)}
}
`

const nullablePrimaryKeyRPCAssertions = `package generated
import("context";"net";"strconv";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/dataloader";"github.com/light-speak/luxo/pkg/lux/rpc";"github.com/light-speak/luxo/pkg/lux/schema")
func TestNullablePrimaryKeyRPC(t *testing.T){
 registry:=schema.New();if err:=registry.RegisterModel(&schema.Model{Name:"Product",Fields:[]schema.Field{{ID:1,Name:"id",Type:schema.FieldInt},{ID:2,Name:"name",Type:schema.FieldString},{ID:3,Name:"price",Type:schema.FieldInt}}});err!=nil{t.Fatal(err)}
 router:=api.NewRouter();router.Schema=registry
 router.HandleInternal("svc:batchLoad:Product",func(_ context.Context,req *api.Request)error{
  keys,err:=req.ParamIntArray("keys");if err!=nil{return err}
  if keys[0]!=42{req.Buf.B=codec.AppendVarint(req.Buf.B,0);return nil}
  req.Buf.B=codec.AppendVarint(req.Buf.B,1);return (&Product{Id:42,Name:"wanted",Price:99}).WriteLuxo(req.Buf,req.FieldMask)
 })
 if err:=router.Registry.Register("svc:batchLoad:Product",100);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterParams("svc:batchLoad:Product",[]api.ParamMeta{{Name:"keys",Type:"Int",IsList:true,FieldID:1}});err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,maxBatch:=range []int{1,100}{t.Run(strconv.Itoa(maxBatch),func(t *testing.T){
  app:=&App{};cfg:=dataloader.DefaultConfig();cfg.MaxBatch=maxBatch
  app.SetLoaders(NewRemoteLoaders(app,map[string]*rpc.Client{"product":client},registry,cfg))
  assertNullablePrimaryKeyHTTP(t,app,"id",int64(42),int64(0),"wanted")
 })}
}
`
