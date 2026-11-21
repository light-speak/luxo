package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGeneratedOwnershipCollectionsAndConcurrentChange(t *testing.T) {
	for _, soft := range []bool{false, true} {
		name, directive, enabled := "hard", "", "false"
		if soft {
			name, directive, enabled = "soft", "@soft", "true"
		}
		t.Run(name, func(t *testing.T) {
			source := `model User @noTime @crud @auth(Admin, own: "name") SOFT {
 id: Int @id
 name: Int
}
api countUsersByName
api deleteUsersByName
`
			runGeneratedFixture(t, strings.ReplaceAll(source, "SOFT", directive), strings.ReplaceAll(ownershipCollectionsFixture, "SOFT_ENABLED", enabled), StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2, "deletedAt": 3}}}, ownershipInterleaveFixture)
		})
	}
}

const ownershipCollectionsFixture = `package generated
import (
 "bytes"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "os"
 "testing"
 "github.com/light-speak/luxo/pkg/lux/api"
 "github.com/light-speak/luxo/pkg/lux/auth"
 "github.com/light-speak/luxo/pkg/lux/codec"
 "github.com/light-speak/luxo/pkg/lux/luvia"
 "github.com/light-speak/luxo/pkg/lux/pg"
 "github.com/light-speak/luxo/pkg/lux/schema"
 "github.com/testcontainers/testcontainers-go"
 "github.com/testcontainers/testcontainers-go/modules/postgres"
)
const softEnabled = SOFT_ENABLED

func ownershipDB(t *testing.T)*pg.DB {
 t.Helper()
 image:=os.Getenv("LUXO_TEST_POSTGRES_IMAGE");if image==""{image="postgres:17-alpine"}
 container,err:=postgres.Run(t.Context(),image,postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 extra:="";if softEnabled{extra=",deleted_at timestamptz"}
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE users(id bigint PRIMARY KEY,name bigint NOT NULL"+extra+")");err!=nil{t.Fatal(err)}
 return db
}

func resetOwnershipRows(t *testing.T,db *pg.DB){
 t.Helper()
 if _,err:=pg.Exec(t.Context(),db,"TRUNCATE users; INSERT INTO users(id,name) VALUES(1,99),(2,42),(3,42),(4,99)");err!=nil{t.Fatal(err)}
}

func ownershipRequest(t *testing.T,router *api.Router,endpoint http.Handler,token,name string,params map[string]any,binary bool)*httptest.ResponseRecorder {
 t.Helper()
 var body []byte;var err error
 if binary{
  mask,e:=codec.AppendSelectionMask(nil,[]byte{3},nil);if e!=nil{t.Fatal(e)}
  body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,mask,params,router.Registry.ParamOrder(name))
 }else{
  payload:=map[string]any{"$api":name,"$select":"id,name"}
  for key,value:=range params{payload[key]=value};body,err=json.Marshal(payload)
 }
 if err!=nil{t.Fatal(err)}
 request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json")
 if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
 request.Header.Set("Authorization","Bearer "+token)
 response:=httptest.NewRecorder();endpoint.ServeHTTP(response,request);return response
}

func ownershipInteger(t *testing.T,response *httptest.ResponseRecorder,binary bool)int64{
 t.Helper();if response.Code!=200{t.Fatalf("status=%d body=%s",response.Code,response.Body)}
 if binary{dec:=codec.NewDecoder(response.Body.Bytes());value:=dec.ReadInt();if err:=dec.Finish();err!=nil{t.Fatal(err)};return value}
 var envelope struct{Data int64};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil{t.Fatal(err)};return envelope.Data
}

func ownershipPage(t *testing.T,router *api.Router,response *httptest.ResponseRecorder,binary bool,want int,counts ...int){
 t.Helper();if response.Code!=200{t.Fatalf("status=%d body=%s",response.Code,response.Body)}
 raw:=response.Body.Bytes();var err error
 if binary{raw,err=schema.BinaryPaginatedListToJSON(nil,raw,router.Schema.Models["User"],router.Schema)}else{var envelope struct{Data json.RawMessage};err=json.Unmarshal(raw,&envelope);raw=envelope.Data};if err!=nil{t.Fatal(err)}
 var page struct{Items []User;Total int};if err:=json.Unmarshal(raw,&page);err!=nil{t.Fatal(err)}
 wantItems:=want;if len(counts)>0{wantItems=counts[0]}
 if page.Total!=want||len(page.Items)!=wantItems{t.Fatalf("page=%+v want total=%d items=%d",page,want,wantItems)}
 if want==2{for _,row:=range page.Items{if row.Name!=42{t.Fatalf("foreign row leaked: %+v",row)}}}
}

func TestOwnershipCollections(t *testing.T){
 db:=ownershipDB(t);app:=NewFromDB(db)
 t.Setenv("JWT_SECRET","ownership-collections");t.Setenv("JWT_EXPIRES","1h");auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,admin:=range []bool{false,true}{
  claims:="{\"id\":42}";if admin{claims="{\"role\":\"Admin\"}"}
  token,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,claims...),nil});if err!=nil{t.Fatal(err)}
  for _,binary:=range []bool{false,true}{
   resetOwnershipRows(t,db);want:=2;if admin{want=4}
   ownershipPage(t,router,ownershipRequest(t,router,endpoint,token,"listUsers",nil,binary),binary,want)
   ownershipPage(t,router,ownershipRequest(t,router,endpoint,token,"listUsers",map[string]any{"page":int64(1),"pageSize":int64(1)},binary),binary,want,1)
   ownershipPage(t,router,ownershipRequest(t,router,endpoint,token,"listUsers",map[string]any{"page":int64(10),"pageSize":int64(1)},binary),binary,want,0)
   count:=ownershipInteger(t,ownershipRequest(t,router,endpoint,token,"countUsersByName",map[string]any{"name":int64(99)},binary),binary)
   expected:=int64(0);if admin{expected=2};if count!=expected{t.Fatalf("inferred count=%d want=%d",count,expected)}
   testOwnershipDeletes(t,db,router,endpoint,token,binary,admin)
   testOwnershipCreates(t,db,router,endpoint,token,binary,admin)
  }
 }
 testOwnershipChangedAfterRead(t,db,app)
 testOwnershipHandlerChangedAfterRead(t,db)
}

func testOwnershipDeletes(t *testing.T,db *pg.DB,router *api.Router,endpoint http.Handler,token string,binary,admin bool){
 t.Helper()
 for _,inferred:=range []bool{false,true}{
  resetOwnershipRows(t,db);name:="deleteUsers";params:=map[string]any{"ids":[]int64{1,2,3,4}}
  want:=int64(2);if admin{want=4}
  if inferred{name="deleteUsersByName";params=map[string]any{"name":int64(99)};want=0;if admin{want=2}}
  response:=ownershipRequest(t,router,endpoint,token,name,params,binary)
  if inferred&&want==0{if response.Code!=404{t.Fatalf("inferred missing status=%d",response.Code)}}else{
   got:=ownershipInteger(t,response,binary);if got!=want{t.Fatalf("%s changed=%d want=%d",name,got,want)}
  }
  remaining,err:=appCountActive(t,db);if err!=nil||remaining!=4-want{t.Fatalf("active=%d err=%v",remaining,err)}
  physical,err:=pg.QueryScalar[int64](t.Context(),db,"SELECT COUNT(*) FROM users");if err!=nil{t.Fatal(err)}
  expected:=4-want;if softEnabled{expected=4};if physical!=expected{t.Fatalf("physical rows=%d want=%d",physical,expected)}
 }
}

func appCountActive(t *testing.T,db *pg.DB)(int64,error){
 t.Helper();query:="SELECT COUNT(*) FROM users";if softEnabled{query+=" WHERE deleted_at IS NULL"};return pg.QueryScalar[int64](t.Context(),db,query)
}

func testOwnershipCreates(t *testing.T,db *pg.DB,router *api.Router,endpoint http.Handler,token string,binary,admin bool){
 t.Helper()
 for _,owner:=range []int64{42,99}{
  resetOwnershipRows(t,db)
  response:=ownershipRequest(t,router,endpoint,token,"createUser",map[string]any{"id":int64(5),"name":owner},binary)
  expected:=200;if !admin&&owner!=42{expected=403};if response.Code!=expected{t.Fatalf("create owner=%d status=%d want=%d body=%s",owner,response.Code,expected,response.Body)}
  count,err:=pg.QueryScalar[int64](t.Context(),db,"SELECT COUNT(*) FROM users WHERE id=5");if err!=nil{t.Fatal(err)}
  want:=int64(1);if expected==403{want=0};if count!=want{t.Fatal("rejected create changed database",count)}
 }
}

func testOwnershipChangedAfterRead(t *testing.T,db *pg.DB,app *App){
 t.Helper();resetOwnershipRows(t,db)
 existing,err:=app.User.Where(UserWhere.Id.Eq(2),UserWhere.Name.Eq(42)).First(t.Context());if err!=nil||existing==nil{t.Fatal(existing,err)}
 // Commit a different writer's ownership change after the successful pre-read.
 if _,err:=pg.Exec(t.Context(),db,"UPDATE users SET name=99 WHERE id=2");err!=nil{t.Fatal(err)}
 builder:=newUserUpdateBuilder(app.User.db,"users",2);builder.SetName(42)
 result,err:=builder.ExecWhere(t.Context(),UserWhere.Name.Eq(42));if err!=nil||result!=nil{t.Fatal("stale authorization mutated row",result,err)}
 owner,err:=pg.QueryScalar[int64](t.Context(),db,"SELECT name FROM users WHERE id=2");if err!=nil||owner!=99{t.Fatal(owner,err)}
}
`

const ownershipInterleaveFixture = `package generated
import (
 "context"
 "net/http"
 "strings"
 "testing"
 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgxpool"
 "github.com/light-speak/luxo/pkg/lux/api"
 "github.com/light-speak/luxo/pkg/lux/auth"
 "github.com/light-speak/luxo/pkg/lux/luvia"
 "github.com/light-speak/luxo/pkg/lux/pg"
)

type ownershipChangeKey struct{}
type ownershipReadKey struct{}
type ownershipInterleave struct{db *pg.DB;changed bool;err error}
type ownershipInterleaveTracer struct{}

func (ownershipInterleaveTracer) TraceQueryStart(ctx context.Context,_ *pgx.Conn,data pgx.TraceQueryStartData)context.Context{
 if gate,ok:=ctx.Value(ownershipChangeKey{}).(*ownershipInterleave);ok&&!gate.changed&&strings.HasPrefix(data.SQL,"SELECT "){
  return context.WithValue(ctx,ownershipReadKey{},gate)
 }
 return ctx
}

func (ownershipInterleaveTracer) TraceQueryEnd(ctx context.Context,_ *pgx.Conn,_ pgx.TraceQueryEndData){
 if gate,ok:=ctx.Value(ownershipReadKey{}).(*ownershipInterleave);ok&&!gate.changed{
  gate.changed=true
  // The other connection commits after the handler's successful pre-read.
  _,gate.err=pg.Exec(ctx,gate.db,"UPDATE users SET name=99 WHERE id=2")
 }
}

func testOwnershipHandlerChangedAfterRead(t *testing.T,db *pg.DB){
 t.Helper()
 config:=db.Pool().Config();config.ConnConfig.Tracer=ownershipInterleaveTracer{}
 pool,err:=pgxpool.NewWithConfig(t.Context(),config);if err!=nil{t.Fatal(err)}
 traced:=pg.NewDBFromPool(pool);t.Cleanup(traced.Close)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,NewFromDB(traced));err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,binary:=range []bool{false,true}{for _,admin:=range []bool{false,true}{
  label:="json/owner";if binary{label="binary/owner"};if admin{label=strings.ReplaceAll(label,"owner","admin")}
  t.Run("interleaved/"+label,func(t *testing.T){
   resetOwnershipRows(t,db)
   claims:="{\"id\":42}";if admin{claims="{\"role\":\"Admin\"}"}
   token,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,claims...),nil});if err!=nil{t.Fatal(err)}
   gate:=&ownershipInterleave{db:db}
   ctx:=context.WithValue(t.Context(),ownershipChangeKey{},gate)
   scoped:=http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){endpoint.ServeHTTP(w,r.WithContext(ctx))})
   response:=ownershipRequest(t,router,scoped,token,"updateUser",map[string]any{"id":int64(2),"name":int64(42)},binary)
   if !gate.changed||gate.err!=nil{t.Fatal("the competing write did not commit after pre-read",gate.changed,gate.err)}
   wantStatus,wantOwner:=403,int64(99);if admin{wantStatus,wantOwner=200,42}
   if response.Code!=wantStatus{t.Fatalf("stale handler status=%d want=%d body=%s",response.Code,wantStatus,response.Body)}
   owner,err:=pg.QueryScalar[int64](t.Context(),db,"SELECT name FROM users WHERE id=2");if err!=nil||owner!=wantOwner{t.Fatal("stale handler bypassed atomic ownership",owner,wantOwner,err)}
  })
 }}
}
`

func TestGeneratedOwnershipCreateDefaultsAndTransforms(t *testing.T) {
	for _, sample := range []struct{ name, field string }{
		{"default", "name: Int = 42"}, {"nullable", "name: Int? = 42"},
		{"transform", "name: Int @beforeSave { it += 1 }"},
		{"default_transform", "name: Int = 41 @beforeSave { it += 1 }"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			source := "model User @noTime @crud @auth(Admin, own: \"name\") { id: Int @id " + sample.field + " }"
			helpers := strings.Split(ownershipCollectionsFixture, "\nfunc ownershipInteger")[0]
			helpers = strings.ReplaceAll(helpers, ` "github.com/light-speak/luxo/pkg/lux/schema"`, "")
			if sample.name == "default" {
				helpers = strings.ReplaceAll(helpers, "name bigint NOT NULL", "name bigint NOT NULL DEFAULT 42")
			}
			if sample.name == "nullable" {
				helpers = strings.ReplaceAll(helpers, "name bigint NOT NULL", "name bigint DEFAULT 42")
			}
			helpers = strings.ReplaceAll(helpers, "SOFT_ENABLED", "false")
			assertions := strings.ReplaceAll(ownershipCreateBoundaryFixture, "SCENARIO", sample.name)
			runGeneratedFixture(t, source, helpers+assertions, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2}}})
		})
	}
}

const ownershipCreateBoundaryFixture = `
func TestCreateOwnershipBoundary(t *testing.T){
 db:=ownershipDB(t);app:=NewFromDB(db)
 t.Setenv("JWT_SECRET","ownership-create-boundary");t.Setenv("JWT_EXPIRES","1h");auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,admin:=range []bool{false,true}{
  claims:="{\"id\":42}";if admin{claims="{\"role\":\"Admin\"}"}
  token,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,claims...),nil});if err!=nil{t.Fatal(err)}
  for _,binary:=range []bool{false,true}{
   for _,input:=range []string{"missing","own","foreign","null"}{
    params:=map[string]any{"id":int64(5)};wantStatus:=200;wantOwner:=int64(42);wantNull:=false
    switch input{
     case "missing":if "SCENARIO"=="transform"{continue};if !admin{wantStatus=403}
     case "own":params["name"]=int64(42);if "SCENARIO"=="transform"||"SCENARIO"=="default_transform"{params["name"]=int64(41)}
     case "foreign":params["name"]=int64(99);wantOwner=99;if "SCENARIO"=="transform"||"SCENARIO"=="default_transform"{params["name"]=int64(42);wantOwner=43};if !admin{wantStatus=403}
     case "null":if "SCENARIO"!="nullable"{continue};params["name"]=nil;wantNull=admin;if !admin{wantStatus=403}
    }
    if _,err:=pg.Exec(t.Context(),db,"TRUNCATE users");err!=nil{t.Fatal(err)}
    response:=ownershipRequest(t,router,endpoint,token,"createUser",params,binary)
    if response.Code!=wantStatus{t.Fatalf("SCENARIO input=%s admin=%v binary=%v status=%d want=%d body=%s",input,admin,binary,response.Code,wantStatus,response.Body)}
    count,err:=pg.QueryScalar[int64](t.Context(),db,"SELECT COUNT(*) FROM users");if err!=nil{t.Fatal(err)}
    if wantStatus!=200{if count!=0{t.Fatal("rejected creation wrote a row")};continue}
    if wantNull{isNull,err:=pg.QueryScalar[bool](t.Context(),db,"SELECT name IS NULL FROM users WHERE id=5");if err!=nil||count!=1||!isNull{t.Fatal("authorized nullable owner lost",err)};continue}
    owner,err:=pg.QueryScalar[int64](t.Context(),db,"SELECT name FROM users WHERE id=5");if err!=nil||count!=1||owner!=wantOwner{t.Fatalf("stored owner=%d count=%d error=%v",owner,count,err)}
   }
  }
 }
}
`

func TestGeneratedOwnershipCreateWithoutWritableOwner(t *testing.T) {
	for _, sample := range []struct {
		name, source, columns, params string
	}{
		{"internal", `model User @noTime @crud @auth(Admin, own: name) {
 id: Int @id
 name: Int = 42 @internal
}`, "id bigint PRIMARY KEY,name bigint NOT NULL DEFAULT 42", `map[string]any{"id": int64(42)}`},
		{"serial", `model User @noTime @crud @auth(Admin, own: "id") {
 id: Int @id @serial
 name: Int = 42
}`, "id bigint GENERATED BY DEFAULT AS IDENTITY (START WITH 42) PRIMARY KEY,name bigint NOT NULL DEFAULT 42", `map[string]any{"name": int64(42)}`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			helpers := strings.Split(ownershipCollectionsFixture, "\nfunc ownershipInteger")[0]
			helpers = strings.ReplaceAll(helpers, "SOFT_ENABLED", "false")
			helpers = strings.ReplaceAll(helpers, "id bigint PRIMARY KEY,name bigint NOT NULL", sample.columns)
			helpers = strings.ReplaceAll(helpers, "[]byte{3}", "[]byte{1}")
			helpers = strings.ReplaceAll(helpers, `"$select":"id,name"`, `"$select":"id"`)
			assertions := strings.ReplaceAll(ownershipUnwritableCreateFixture, "PARAMS", sample.params)
			runGeneratedFixture(t, sample.source, helpers+assertions, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2}}})
		})
	}
}

const ownershipUnwritableCreateFixture = `
func TestCreateUnwritableOwner(t *testing.T) {
 db:=ownershipDB(t);app:=NewFromDB(db)
 t.Setenv("JWT_SECRET","unwritable-create-owner");t.Setenv("JWT_EXPIRES","1h");auth.ResetConfig();t.Cleanup(auth.ResetConfig)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 endpoint:=luvia.AuthMiddleware(cfg,router)
 for _,mode:=range []struct{name string;binary bool}{{"json",false},{"binary",true}} {
  for _,sample:=range []struct{name,raw string;want int}{
   {"anonymous","",401},
   {"owner","{\"id\":42}",403},
   {"zero","{\"id\":0}",403},
   {"missing","{}",403},
   {"null","{\"id\":null}",403},
   {"wrong_type","{\"id\":\"42\"}",403},
   {"admin","{\"role\":\"Admin\"}",200},
   {"admin_wrong_id","{\"role\":\"Admin\",\"id\":\"invalid\"}",200},
  } {
   t.Run(mode.name+"/"+sample.name,func(t *testing.T){
    if _,err:=pg.Exec(t.Context(),db,"TRUNCATE users RESTART IDENTITY");err!=nil{t.Fatal(err)}
    token:="";if sample.raw!="" {token,err=auth.Sign(cfg,func(dst []byte)([]byte,error){return append(dst,sample.raw...),nil});if err!=nil{t.Fatal(err)}}
    response:=ownershipRequest(t,router,endpoint,token,"createUser",PARAMS,mode.binary)
    count,err:=pg.QueryScalar[int64](t.Context(),db,"SELECT COUNT(*) FROM users");if err!=nil{t.Fatal(err)}
    if response.Code!=sample.want{t.Fatalf("status=%d want=%d rows=%d body=%s",response.Code,sample.want,count,response.Body)}
    if sample.want!=200{if count!=0{t.Fatal("rejected owner creation wrote a row")};return}
    id,err:=pg.QueryScalar[int64](t.Context(),db,"SELECT id FROM users");if err!=nil{t.Fatal(err)}
    owner,err:=pg.QueryScalar[int64](t.Context(),db,"SELECT name FROM users");if err!=nil{t.Fatal(err)}
    if count!=1||id!=42||owner!=42{t.Fatalf("stored id=%d name=%d count=%d",id,owner,count)}
    raw:=response.Body.Bytes()
    if mode.binary{raw,err=schema.BinaryToJSON(nil,raw,router.Schema.Models["User"],router.Schema)}else{var envelope struct{Data json.RawMessage};err=json.Unmarshal(raw,&envelope);raw=envelope.Data}
    if err!=nil{t.Fatal(err)}
    var fields map[string]json.RawMessage;if err:=json.Unmarshal(raw,&fields);err!=nil{t.Fatal(err)}
    if len(fields)!=1||string(fields["id"])!="42"{t.Fatalf("selected response=%s",raw)}
   })
  }
 }
}
`

func TestFormalGenerationRejectsInvalidOwnershipField(t *testing.T) {
	const source = `model User @noTime @crud @auth(own: "ownerId") {
 id: Int @id
 ownerId: Int
}`
	for _, sample := range []string{"unknown", "wrong_type", "list"} {
		t.Run(sample, func(t *testing.T) {
			input := analyzePermissionFixture(t, source)
			if len(input.Errors) != 0 {
				t.Fatal(input.Errors)
			}
			ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "ownerId": 2}}})
			generator, err := NewGenerator(GeneratorConfig{IDs: ids})
			if err != nil {
				t.Fatal(err)
			}
			before, err := generator.Generate(input, "generated", nil)
			if err != nil {
				t.Fatal(err)
			}
			model, fieldName := input.Files[0].Models[0], "ownerId"
			switch sample {
			case "unknown":
				fieldName = "absent"
				findDirective(model.Directives, "auth").Args[0].Value.(*ast.Literal).Value = fieldName
			case "wrong_type":
				model.Fields[1].Type.Name = "String"
			case "list":
				model.Fields[1].Type.IsList = true
			}
			generated, err := generator.Generate(input, "generated", nil)
			failure, internal := err.(*InternalCompilerError)
			if generated != nil || !internal || len(failure.Stack) == 0 || !strings.Contains(failure.Detail, "scalar Int field: User."+fieldName) {
				t.Fatalf("invalid ownership did not fail at the formal boundary: output=%v error=%v", generated != nil, err)
			}
			after, err := generator.Generate(analyzePermissionFixture(t, source), "generated", nil)
			if err != nil || after == nil || len(after.Files) != len(before.Files) {
				t.Fatalf("valid generation after ownership rejection failed: %v", err)
			}
			for name, content := range before.Files {
				if string(after.Files[name]) != string(content) {
					t.Errorf("rejected ownership contaminated %s", name)
				}
			}
		})
	}
}
