package codegen

// Exercise generated HTTP handlers without any response-cache infrastructure.
const generatedCRUDAssertions = `package generated
import (
"github.com/light-speak/luxo/internal/testutil"

  "bytes"
  "encoding/json"
  "net/http/httptest"
  "testing"
  "github.com/light-speak/luxo/pkg/lux/api"
  "github.com/light-speak/luxo/pkg/lux/codec"
  "github.com/light-speak/luxo/pkg/lux/pg"
)

func TestGeneratedCRUDWithoutCacheInfrastructure(t *testing.T) {
  for _,mode:=range []string{"json","binary"} {
    t.Run(mode,func(t *testing.T){
      db:=transactionFixtureDB(t)
      app:=NewFromDB(db)
      router:=api.NewRouterWithOptions(api.RouterOptions{})
      RegisterSchema(router.Schema)
      if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
      if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)}
      if err:=router.Prepare();err!=nil {t.Fatal(err)}
      testCRUDWrites(t,db,router,mode)
      testCRUDStorageFailure(t,db,router,mode)
      assertNoCacheInfrastructure(t,db)
    })
  }
}

func testCRUDWrites(t *testing.T,db *pg.DB,router *api.Router,mode string) {
  t.Helper()
  cases:=[]struct{name,api string; params map[string]any; success bool; rows int64; value string}{
    {"create","createUser",map[string]any{"id":int64(2),"name":"created"},true,2,"created"},
    {"duplicate","createUser",map[string]any{"id":int64(2),"name":"duplicate"},false,2,"created"},
    {"update","updateUser",map[string]any{"id":int64(2),"name":"updated"},true,2,"updated"},
    {"missing-update","updateUser",map[string]any{"id":int64(99),"name":"missing"},false,2,"updated"},
    {"missing-delete","deleteUser",map[string]any{"id":int64(99)},false,2,"updated"},
    {"empty-delete","deleteUsers",map[string]any{"ids":[]int64{98,99}},true,2,"updated"},
    {"delete","deleteUser",map[string]any{"id":int64(2)},true,1,""},
    {"delete-many","deleteUsers",map[string]any{"ids":[]int64{1,99}},true,0,""},
  }
  for _,tc:=range cases {
    t.Run(tc.name,func(t *testing.T){
      callCRUD(t,router,mode,tc.api,tc.params,tc.success)
      assertCRUDCount(t,db,"SELECT count(*) FROM users",tc.rows)
      if tc.value!="" {
        value,err:=db.QueryScalar[string](t.Context(),"SELECT name FROM users WHERE id=2")
        if err!=nil || value!=tc.value {t.Fatalf("stored name=%q error=%v; want %q",value,err,tc.value)}
      }
    })
  }
}

func testCRUDStorageFailure(t *testing.T,db *pg.DB,router *api.Router,mode string) {
  t.Helper()
  if _,err:=pg.Exec(t.Context(),db,"INSERT INTO users VALUES(10,'ten'),(11,'eleven'); ALTER TABLE users RENAME TO unavailable_users");err!=nil {t.Fatal(err)}
  for _,tc:=range []struct{api string;params map[string]any}{
    {"createUser",map[string]any{"id":int64(12),"name":"must-rollback"}},
    {"updateUser",map[string]any{"id":int64(10),"name":"must-rollback"}},
    {"deleteUser",map[string]any{"id":int64(10)}},
    {"deleteUsers",map[string]any{"ids":[]int64{10,11}}},
  } {
    t.Run("storage-unavailable/"+tc.api,func(t *testing.T){
      response:=callCRUD(t,router,mode,tc.api,tc.params,false)
      if response.Code!=500 {t.Fatalf("storage failure became success: HTTP %d body=%q",response.Code,response.Body.Bytes())}
      assertCRUDCount(t,db,"SELECT count(*) FROM unavailable_users",2)
      assertCRUDCount(t,db,"SELECT count(*) FROM unavailable_users WHERE (id=10 AND name='ten') OR (id=11 AND name='eleven')",2)
    })
  }
}

func assertCRUDCount(t *testing.T,db *pg.DB,query string,want int64) {
  t.Helper()
  got,err:=db.QueryScalar[int64](t.Context(),query)
  if err!=nil || got!=want {t.Fatalf("%s: got=%d want=%d error=%v",query,got,want,err)}
}

func assertNoCacheInfrastructure(t *testing.T,db *pg.DB) {
  t.Helper()
  assertCRUDCount(t,db,"SELECT count(*) FROM pg_catalog.pg_tables WHERE schemaname=current_schema() AND left(tablename,12)='_luxo_cache_'",0)
}

func callCRUD(t *testing.T,router *api.Router,mode,name string,params map[string]any,success bool) *httptest.ResponseRecorder {
  t.Helper()
  var body []byte
  var err error
  contentType:="application/json"
  if mode=="binary" {
    definition:=router.Schema.APIs[name]
    var mask []byte
    if definition.ReturnType=="User" {mask=testutil.Bytes(t)(codec.AppendSelectionMask(nil,testutil.Bytes(t)(codec.FieldMaskAll(2)),nil))}
    body,err=api.EncodeBinaryRequest(definition.ID,mask,params,router.Registry.ParamOrder(name))
    contentType="application/x-luxo"
  } else {
    payload:=map[string]any{"$api":name,"$select":"id, name"}
    for key,value:=range params {payload[key]=value}
    body,err=json.Marshal(payload)
  }
  if err!=nil {t.Fatal(err)}
  request:=httptest.NewRequest("POST","/",bytes.NewReader(body))
  request.Header.Set("Content-Type",contentType)
  request.Header.Set("X-Luxo-Mode",mode)
  response:=httptest.NewRecorder()
  router.ServeHTTP(response,request)
  if (response.Code==200)!=success {t.Fatalf("%s: HTTP %d body=%q; want success=%t",name,response.Code,response.Body.Bytes(),success)}
  return response
}
`
