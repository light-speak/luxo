package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestGeneratedLoaderEntryScopes(t *testing.T) {
	runGeneratedFixture(t, `model Product @noTime { id: Int @id name: String }
fn lookupName(id: Int): String { return (Product.load(id) ?: throw error.NotFound).name }
fn forwardName(id: Int): String { return lookupName(id) }
fn serviceName(id: Int): String @service { return forwardName(id) }
api direct(id: Int): String { return (Product.load(id) ?: throw error.NotFound).name }
api indirect(id: Int): String { return forwardName(id) }
api names(dataloader: Int, scopeCtx: Int, scopeErr: Int): String {
 val row = Product.load(dataloader) ?: throw error.NotFound
 if scopeCtx + scopeErr == 3 { return row.name }
 return "wrong bindings"
}
api pure(): Int { return 7 }
event Requested(key: Int)
on Requested { val name = forwardName(it.key) if name == "" { throw error.NotFound } }
`, generatedLoaderEntryScopesAssertions, StableIDs{
		ModelFields: map[string]map[string]int{"Product": {"id": 1, "name": 2}},
		EventFields: map[string]map[string]int{"Requested": {"key": 1}},
	})
}

const generatedLoaderEntryScopesAssertions = `package generated
import("context";"encoding/json";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/dataloader";"github.com/light-speak/luxo/pkg/lux/event";"github.com/light-speak/luxo/pkg/lux/selection")
type scopeTenantKey struct{}
type scopeListenerBus struct{event.Bus;handler event.Handler}
func(b *scopeListenerBus)OnQueue(_ string,_ string,handler event.Handler)error{b.handler=handler;return nil}
func TestLoaderEntryScopes(t *testing.T){
 app:=&App{};calls:=0;cfg:=dataloader.DefaultConfig()
 app.SetLoaders(NewLoaders(cfg,func(ctx context.Context,keys []int64,_ []*selection.Field)(map[int64]*Product,error){
  calls++;if ctx.Value(scopeTenantKey{})!="tenant-a"{t.Fatal("request values lost")}
  return map[int64]*Product{42:{Id:42,Name:"scoped"}},nil
 }))
 ctx:=context.WithValue(t.Context(),scopeTenantKey{},"tenant-a")
 for name,handler:=range map[string]api.HandlerFunc{"direct":handleDirect(app),"indirect":handleIndirect(app),"service":_handleService_serviceName(app),"names":handleNames(app)}{
  t.Run(name,func(t *testing.T){req:=&api.Request{Params:map[string]json.RawMessage{"id":json.RawMessage("42"),"dataloader":json.RawMessage("42"),"scopeCtx":json.RawMessage("1"),"scopeErr":json.RawMessage("2")},Buf:&api.ResponseBuf{}}
   if err:=handler(ctx,req);err!=nil{t.Fatal("generated entry omitted request scope",err)}
   decoder:=codec.NewDecoder(req.Buf.B);if decoder.ReadString()!="scoped"||decoder.Finish()!=nil{t.Fatal("scope initialization changed business bindings")}
  })
 }
 bus:=&scopeListenerBus{};if err:=RegisterEvents(bus,app);err!=nil{t.Fatal(err)}
 if err:=bus.handler(ctx,RequestedEvent{Key:42});err!=nil{t.Fatal("listener omitted transitive request scope",err)}
 if calls!=5{t.Fatal("generated body skipped loading",calls)}
}
`

func TestGeneratedLocalPrimaryKeyLoads(t *testing.T) {
	for _, keyType := range []string{"Int", "String", "UUID"} {
		t.Run(keyType, func(t *testing.T) {
			source := "model Product @noTime { key: " + keyType + " @id name: String }\n" +
				"api lookup(key: " + keyType + "): Product { return (Product.load(key) ?: throw error.NotFound) }\n" +
				"fn again(key: " + keyType + "): Product { return (Product.load(key) ?: throw error.NotFound) }"
			goType := map[string]string{"Int": "int64", "String": "string", "UUID": "uuid.UUID"}[keyType]
			assertions := strings.ReplaceAll(localPrimaryKeyAssertions, "KEY_TYPE", goType)
			runGeneratedFixture(t, source, assertions, StableIDs{ModelFields: map[string]map[string]int{"Product": {"key": 1, "name": 2}}})
		})
	}
}

const localPrimaryKeyAssertions = `package generated
import("context";"testing";"github.com/google/uuid";"github.com/light-speak/luxo/pkg/lux/dataloader";"github.com/light-speak/luxo/pkg/lux/selection")
var _ = uuid.Nil
func TestTypedPrimaryKeyFunction(t *testing.T){
 var key KEY_TYPE
 calls:=0;app:=&App{};cfg:=dataloader.DefaultConfig();cfg.MaxBatch=1
 app.SetLoaders(NewLoaders(cfg,func(_ context.Context,keys []KEY_TYPE,_ []*selection.Field)(map[KEY_TYPE]*Product,error){
  calls++;if len(keys)!=1||keys[0]!=key{t.Fatal("typed primary key changed",keys)}
  return map[KEY_TYPE]*Product{key:{Key:key,Name:"loaded"}},nil
 }))
 row,err:=app.again(t.Context(),key)
 if err!=nil||row==nil||row.Key!=key||row.Name!="loaded"||calls!=1{t.Fatal("compiled function bypassed typed loader",row,err,calls)}
}
`

func TestLocalPrimaryKeyLoadersHaveStartupWiring(t *testing.T) {
	input := analyzePermissionFixture(t, `model Product @noTime { key: String @id name: String }
api lookup(key: String): Product { return (Product.load(key) ?: throw error.NotFound) }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	modules := collectModules(input)
	if len(modules) != 1 || !modules[0].hasLoaders || modules[0].hasExtend {
		t.Fatalf("local primary load has wrong startup flags: %+v", modules)
	}
	generator := mustNewGenerator(t, GeneratorConfig{IDs: completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Product": {"key": 1, "name": 2}}})})
	generated, err := generator.Generate(input, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := GenerateEntryFileChecked(input, "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entry), "permissionApp.SetLoaders(permission_luxo.NewDefaultLoaders(permissionApp, dlCfg))") {
		t.Fatal("embedded entry omitted local loader binding")
	}
	entries, err := GenerateModuleEntryFilesChecked(input, "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entries["permission"]), "app.SetLoaders(permission_luxo.NewDefaultLoaders(app, dlCfg))") {
		t.Fatal("module entry omitted local loader binding")
	}
	code := string(generated.Files["dataloader.gen.go"])
	if strings.Count(code, "type ExtendProductByIdLoader =") != 1 || strings.Contains(code, "func NewRemoteLoaders(") || strings.Contains(code, `"github.com/light-speak/luxo/pkg/lux/rpc"`) {
		t.Fatal("local primary loader duplicated or acquired RPC dependencies", code)
	}
}

func TestLocalLoadersAcrossExecutableBodies(t *testing.T) {
	for name, body := range map[string]string{
		"listener": `event Requested(key: String)
on Requested { val product = (Product.load(it.key) ?: throw error.NotFound) if product.name == "" { throw error.NotFound } }`,
	} {
		t.Run(name, func(t *testing.T) {
			input := analyzePermissionFixture(t, `model Product @noTime { key: String @id name: String }`+"\n"+body)
			if len(input.Errors) != 0 {
				t.Fatal(input.Errors)
			}
			calls := collectLoadCalls(input)
			if len(calls) != 1 || calls[0].modelName != "Product" {
				t.Fatalf("body loader omitted: %+v", calls)
			}
			if modules := collectModules(input); len(modules) != 1 || !modules[0].hasLoaders || modules[0].hasExtend {
				t.Fatalf("body loader not wired locally: %+v", modules)
			}
			runGeneratedFixture(t, `model Product @noTime { key: String @id name: String }`+"\n"+body, localPrimaryListenerAssertions, StableIDs{ModelFields: map[string]map[string]int{"Product": {"key": 1, "name": 2}}, EventFields: map[string]map[string]int{"Requested": {"key": 1}}})
		})
	}
}

const localPrimaryListenerAssertions = `package generated
import("context";"testing";"github.com/light-speak/luxo/pkg/lux/event";"github.com/light-speak/luxo/pkg/lux/dataloader";"github.com/light-speak/luxo/pkg/lux/selection")
type primaryListenerBus struct{event.Bus;handler event.Handler}
func(b *primaryListenerBus)OnQueue(_ string,_ string,handler event.Handler)error{b.handler=handler;return nil}
func TestPrimaryListener(t *testing.T){
 app:=&App{};cfg:=dataloader.DefaultConfig();cfg.MaxBatch=1;calls:=0
 app.SetLoaders(NewLoaders(cfg,func(_ context.Context,keys []string,fields []*selection.Field)(map[string]*Product,error){
  calls++;if len(keys)!=1||keys[0]!="x"||len(fields)!=1||fields[0].Name!="name"{t.Fatal("listener lost primary key or logic projection",keys,fields)}
  return map[string]*Product{"x":{Key:"x",Name:"loaded"}},nil
 }))
 bus:=&primaryListenerBus{};if err:=RegisterEvents(bus,app);err!=nil{t.Fatal(err)}
 if err:=bus.handler(t.Context(),RequestedEvent{Key:"x"});err!=nil||calls!=1{t.Fatal("listener missed local loader",err,calls)}
}
`

func TestSameModuleExtensionKeepsLoadersLocal(t *testing.T) {
	input := &semantic.Result{Files: []*ast.File{{Name: "origin/product/model.luxo", Models: []*ast.ModelDecl{{Name: "Product"}}},
		{Name: "origin/product/extra.luxo", Extends: []*ast.ExtendDecl{{Name: "Product"}}}}}
	modules := collectModules(input)
	if len(modules) != 1 || modules[0].hasExtend || modules[0].hasLoaders {
		t.Fatalf("same-module extension unexpectedly needs RPC loaders: %+v", modules)
	}
}

func TestMixedPrimaryKeyLoadsKeepLocalQueriesLocal(t *testing.T) {
	input := analyzePermissionFixture(t, `model Product @noTime { key: String @id name: String }
model User @noTime { id: Int @id name: String }
api lookup(key: String): Product { return (Product.load(key) ?: throw error.NotFound) }
api remote(key: Int): User { return (User.load(key) ?: throw error.NotFound) }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	input.Files[0].Extends = []*ast.ExtendDecl{{Name: "User", Fields: input.Files[0].Models[1].Fields}}
	input.Files[0].Models = input.Files[0].Models[:1]
	events := &EventContext{ModelModule: map[string]string{"Product": "permission", "User": "user"}}
	ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Product": {"key": 1, "name": 2}, "User": {"id": 1, "name": 2}}, APIs: map[string]int{"svc:batchLoad:User": 99}})
	generator := mustNewGenerator(t, GeneratorConfig{Events: events, IDs: ids})
	output, err := generator.Generate(input, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	runGeneratedCompilation(t, output, "package generated\n")
	_, remote, ok := strings.Cut(string(output.Files["dataloader.gen.go"]), "func NewRemoteLoaders")
	if !ok || !strings.Contains(remote, `lux.BuildSelectSQL("\"products\""`) || strings.Contains(remote, `rpcClients["permission"]`) || !strings.Contains(remote, `rpcClients["user"]`) || strings.Contains(remote, `lux.BuildSelectSQL("\"users\""`) {
		t.Fatal("mixed loaders changed local/remote routing", remote)
	}
}

func TestCrossModulePrimaryKeyLoadsRegisterEndpoints(t *testing.T) {
	input := analyzePermissionFixture(t, `model Product @noTime { key: String @id }
api lookup(key: String): Product { return (Product.load(key) ?: throw error.NotFound) }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	owner := &ast.File{Name: "origin/product/model.luxo", Models: input.Files[0].Models}
	caller := *input.Files[0]
	caller.Name = "origin/order/api.luxo"
	caller.Models = nil
	result := &semantic.Result{Files: []*ast.File{owner, &caller}}
	modules := collectModules(result)
	if len(modules) != 2 || !modules[0].hasBatchRPC || modules[0].hasLoaders || !modules[1].hasExtend || !modules[1].hasLoaders {
		t.Fatalf("cross-module loader endpoint flags: %+v", modules)
	}
	ctx := BuildEventContext(result.Files, "example.com/app")
	if !ctx.remotePKModels["Product"] {
		t.Fatal("owner lacks batch endpoint without an extension")
	}
	generator := mustNewGenerator(t, GeneratorConfig{Events: ctx})
	inputs := generator.collectDataLoaderInputs(&semantic.Result{Files: []*ast.File{&caller}}, nil, nil)
	if len(inputs.pkModels) != 1 || len(inputs.remotePKModels) != 1 || inputs.remotePKModels[0] != "Product" || !inputs.hasRemote {
		t.Fatalf("direct remote load lost routing metadata: %+v", inputs)
	}
}

func TestGeneratedLocalPrimaryKeyLoadsPostgres(t *testing.T) {
	runGeneratedFixture(t, `model Product @noTime { key: String @id name: String secret: String @hidden internalSecret: String @internal }
api readSecret(key: String): String { val row = (Product.load(key) ?: throw error.NotFound) return row.secret }
api readInternal(key: String): String { val row = (Product.load(key) ?: throw error.NotFound) return row.internalSecret }
api checked(key: String): Product { val row = (Product.load(key) ?: throw error.NotFound) if row.secret != "private" { throw error.NotFound } return row }
api lookup(key: String): Product { return (Product.load(key) ?: throw error.NotFound) }
api named(name: String): [Product] { return Product.load(name: name) }
api findName(key: String): String { return Product.find(key: key).name }
`+nullableLoadDeclarations("String", "key"), localPrimaryKeyPostgresAssertions, StableIDs{ModelFields: map[string]map[string]int{"Product": {"key": 1, "name": 2, "secret": 3, "internalSecret": 4}}}, nullablePrimaryKeyHTTPAssertions)
}

const localPrimaryKeyPostgresAssertions = `package generated
import (
 "bytes"
 "context"
 "errors"
 "encoding/json"
 "net/http/httptest"
 "testing"
 "github.com/light-speak/luxo/internal/testutil"
 "github.com/light-speak/luxo/pkg/lux/api"
 "github.com/light-speak/luxo/pkg/lux/codec"
 "github.com/light-speak/luxo/pkg/lux/dataloader"
 "github.com/light-speak/luxo/pkg/lux/pg"
 "github.com/light-speak/luxo/pkg/lux/schema"
 "github.com/light-speak/luxo/pkg/lux/selection"
 "github.com/testcontainers/testcontainers-go"
 "github.com/testcontainers/testcontainers-go/modules/postgres"
)
func TestLocalPrimaryKeyDatabase(t *testing.T){
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE products(key text PRIMARY KEY,name text,secret text,internal_secret text);INSERT INTO products VALUES('','zero','private','internal'),('x','original','private','internal')");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);cfg:=dataloader.DefaultConfig();cfg.MaxBatch=1;app.SetLoaders(NewDefaultLoaders(app,cfg))
 fields:=[]*selection.Field{{Name:"name"}}
 rows,err:=app.loaders.ExtendProduct.LoadAll(t.Context(),[]string{"","x","missing","x"},fields)
 if err!=nil||rows[""]==nil||rows[""].Name!="zero"||rows["x"]==nil||rows["x"].Name!="original"||rows["missing"]!=nil||rows["x"].Secret!="" {t.Fatalf("primary grouping/projection: %+v %v",rows,err)}
 for _,sample:=range []struct{handler api.HandlerFunc;want string}{{handleReadSecret(app),"private"},{handleReadInternal(app),"internal"}}{
  req:=&api.Request{Params:map[string]json.RawMessage{"key":json.RawMessage("\"x\"")},Buf:&api.ResponseBuf{}}
  if err:=sample.handler(t.Context(),req);err!=nil{t.Fatal(err)}
  dec:=codec.NewDecoder(req.Buf.B);got:=dec.ReadString();if err:=dec.Finish();err!=nil||got!=sample.want{t.Fatalf("private logic field lost: got=%q want=%q err=%v",got,sample.want,err)}
 }
 testLocalPrimaryTransaction(t,app,fields)
 testLocalPrimaryHTTP(t,app,fields)
 assertNullablePrimaryKeyHTTP(t,app,"key","x","missing","original")
 testLocalPrimaryMissingQueries(t,app,fields)
 testLocalPrimaryPresence(t,db,connection,cfg)
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
 for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\"checked\",\"key\":\"x\",\"$select\":\"name\"}")
  if binary{mask,err:=schema.SelectToFieldMask(fields,router.Schema.Models["Product"],router.Schema);if err!=nil{t.Fatal(err)};body,err=api.EncodeBinaryRequest(router.Schema.APIs["checked"].ID,mask,map[string]any{"key":"x"},router.Registry.ParamOrder("checked"));if err!=nil{t.Fatal(err)}}
  request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if response.Code!=200||bytes.Contains(response.Body.Bytes(),[]byte("private"))||bytes.Contains(response.Body.Bytes(),[]byte("internal")){t.Fatal("logical private selection changed public result",response.Code,response.Body.String())}
 }

 if _,err:=pg.Exec(t.Context(),db,"CREATE ROLE projection_reader;GRANT SELECT(key,name) ON products TO projection_reader");err!=nil{t.Fatal(err)}
 reader,err:=pg.NewDB(t.Context(),connection+"&options=-c%20role%3Dprojection_reader");if err!=nil{t.Fatal(err)};t.Cleanup(reader.Close)
 restricted:=NewFromDB(reader);restricted.SetLoaders(NewDefaultLoaders(restricted,cfg))
 testLocalPrimaryHTTP(t,restricted,fields)
 cancelled,cancel:=context.WithCancel(t.Context());cancel()
 if row,err:=app.loaders.ExtendProduct.Load(cancelled,"x",fields);err==nil||row!=nil{t.Fatal("cancelled query succeeded",row,err)}
 if _,err:=pg.Exec(t.Context(),db,"DROP TABLE products");err!=nil{t.Fatal(err)}
 if row,err:=app.loaders.ExtendProduct.Load(t.Context(),"x",fields);err==nil||row!=nil{t.Fatal("database failure returned success",row,err)}
}
func testLocalPrimaryPresence(t *testing.T,db *pg.DB,connection string,cfg dataloader.Config){
 t.Helper()
 if _,err:=pg.Exec(t.Context(),db,"CREATE ROLE projection_key_reader;GRANT SELECT(key) ON products TO projection_key_reader");err!=nil{t.Fatal(err)}
 reader,err:=pg.NewDB(t.Context(),connection+"&options=-c%20role%3Dprojection_key_reader");if err!=nil{t.Fatal(err)};t.Cleanup(reader.Close)
 app:=NewFromDB(reader);app.SetLoaders(NewDefaultLoaders(app,cfg))
 for _,key:=range []string{"x","missing"}{for _,sample:=range []struct{handler api.HandlerFunc;want bool}{{handlePresent(app),key=="x"},{handleAbsent(app),key!="x"}}{
  raw,err:=json.Marshal(key);if err!=nil{t.Fatal(err)}
  req:=&api.Request{Params:map[string]json.RawMessage{"key":raw},Buf:&api.ResponseBuf{}}
  if err:=sample.handler(t.Context(),req);err!=nil{t.Fatal("presence must read only the actual key",err)}
  dec:=codec.NewDecoder(req.Buf.B);got:=dec.ReadBool();if err:=dec.Finish();err!=nil||got!=sample.want{t.Fatal(got,sample.want,err)}
 }}
}
func testLocalPrimaryMissingQueries(t *testing.T,app *App,fields []*selection.Field){
 t.Helper();router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,binary:=range []bool{false,true}{for _,sample:=range []struct{name,param,value,want string;status int}{{"named","name","missing","[]",200},{"findName","key","missing","",404},{"findName","key","x",` + "`" + `"original"` + "`" + `,200}}{
  params:=map[string]any{"$api":sample.name,sample.param:sample.value};if sample.name=="named"{params["$select"]="name"}
  body,err:=json.Marshal(params);if err!=nil{t.Fatal(err)}
  if binary{var mask []byte;if sample.name=="named"{mask,err=schema.SelectToFieldMask(fields,router.Schema.Models["Product"],router.Schema);if err!=nil{t.Fatal(err)}};body,err=api.EncodeBinaryRequest(router.Schema.APIs[sample.name].ID,mask,map[string]any{sample.param:sample.value},router.Registry.ParamOrder(sample.name));if err!=nil{t.Fatal(err)}}
  request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=sample.status{t.Fatal(sample.name,binary,response.Code,response.Body.String())};if sample.status!=200{continue}
  if !binary{var envelope struct{Data json.RawMessage};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil||string(envelope.Data)!=sample.want{t.Fatal(response.Body.String(),err)};continue}
  if sample.name=="named"{value,err:=schema.BinaryListToJSON(nil,response.Body.Bytes(),router.Schema.Models["Product"],router.Schema);if err!=nil||string(value)!="[]"{t.Fatal(string(value),err)}}else{dec:=codec.NewDecoder(response.Body.Bytes());got:=dec.ReadString();if err:=dec.Finish();err!=nil||got!="original"{t.Fatal(got,err)}}
 }}
}
func testLocalPrimaryTransaction(t *testing.T,app *App,fields []*selection.Field){
 t.Helper();abort:=errors.New("rollback")
 err:=app.Tx(t.Context(),func(tx *App)error{
  if tx.loaders.ExtendProduct==app.loaders.ExtendProduct{t.Fatal("transaction reused outer primary loader")}
  if _,err:=pg.Exec(t.Context(),tx.DB,"UPDATE products SET name='uncommitted' WHERE key='x'");err!=nil{return err}
  row,err:=tx.loaders.ExtendProduct.Load(t.Context(),"x",fields);if err!=nil{return err}
  if row==nil||row.Name!="uncommitted"{t.Fatal("loader missed own transactional write",row)}
  return abort
 })
 if !errors.Is(err,abort){t.Fatal(err)}
 row,err:=app.loaders.ExtendProduct.Load(t.Context(),"x",fields)
 if err!=nil||row==nil||row.Name!="original"{t.Fatal("transaction escaped rollback",row,err)}
}
func testLocalPrimaryHTTP(t *testing.T,app *App,fields []*selection.Field){
 t.Helper();router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\"lookup\",\"key\":\"x\",\"$select\":\"name\"}")
  if binary{
   mask,err:=schema.SelectToFieldMask(fields,router.Schema.Models["Product"],router.Schema);if err!=nil{t.Fatal(err)}
   body,err=api.EncodeBinaryRequest(router.Schema.APIs["lookup"].ID,mask,map[string]any{"key":"x"},router.Registry.ParamOrder("lookup"));if err!=nil{t.Fatal(err)}
  }
  request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json")
  if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if response.Code!=200{t.Fatal("local load HTTP failed",response.Code,response.Body.String())}
  if binary{var value Product;dec:=codec.NewDecoder(response.Body.Bytes());value.ReadLuxo(dec);if err:=dec.Finish();err!=nil||value.Name!="original"||value.Key!=""||value.Secret!=""{t.Fatal(value,err)}}else if !bytes.Contains(response.Body.Bytes(),[]byte("\"data\":{\"name\":\"original\"}")){t.Fatal("JSON projection changed",response.Body.String())}
 }

 for _,field:=range []struct{name string;id int}{{"secret",3},{"internalSecret",4}}{for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\"lookup\",\"key\":\"x\",\"$select\":\""+field.name+"\"}")
  if binary{mask,err:=codec.FieldMaskSet(nil,field.id);if err!=nil{t.Fatal(err)};mask,err=codec.AppendSelectionMask(nil,mask,nil);if err!=nil{t.Fatal(err)};body,err=api.EncodeBinaryRequest(router.Schema.APIs["lookup"].ID,mask,map[string]any{"key":"x"},router.Registry.ParamOrder("lookup"));if err!=nil{t.Fatal(err)}}
  request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=400{t.Fatal("public private-field selection accepted",field.name,binary,response.Code,response.Body.String())}
 }}
}
`
