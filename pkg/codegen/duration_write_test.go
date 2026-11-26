package codegen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedDurationWriteUsesPostgresIsolation(t *testing.T) {
	dir := writeProtocolFixture(t, "package generated\n", durationWriteAssertions)
	fixture, err := os.ReadFile(filepath.Join(dir, "postgres_fixture_test.go"))
	if err != nil {
		t.Fatalf("duration write fixture does not install PostgreSQL isolation: %v", err)
	}
	if string(fixture) != protocolPostgresFixture {
		t.Fatal("duration write fixture must use the shared PostgreSQL isolation")
	}
}

func TestGeneratedDurationWritePrecision(t *testing.T) {
	runGeneratedFixture(t, `
model Reading @noTime { id: Int @id elapsed: Duration optionalElapsed: Duration? laps: [Duration] optionalLaps: [Duration]? }
api writeReading(id: Int, elapsed: Duration, optional: Duration?, laps: [Duration], optionalLaps: [Duration]?): Reading {
 return Reading.create(id: id, elapsed: elapsed, optionalElapsed: optional, laps: laps, optionalLaps: optionalLaps)
}
api changeReading(id: Int, elapsed: Duration, optional: Duration?, laps: [Duration], optionalLaps: [Duration]?): Int {
 Reading.where(id: id).update(elapsed: elapsed, optionalElapsed: optional, laps: laps, optionalLaps: optionalLaps)
 return id
}
`, durationWriteAssertions, StableIDs{ModelFields: map[string]map[string]int{"Reading": {
		"id": 1, "elapsed": 2, "optionalElapsed": 3, "laps": 4, "optionalLaps": 5,
	}}})
}

const durationWriteAssertions = `package generated
import (
 "bytes"
 "encoding/json"
 "errors"
 "net"
 "net/http/httptest"
 "os"
 "testing"
 "time"
 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgxpool"
 "github.com/light-speak/luxo/internal/testutil"
 "github.com/light-speak/luxo/pkg/lux/api"
 "github.com/light-speak/luxo/pkg/lux/codec"
 luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
 "github.com/light-speak/luxo/pkg/lux/pg"
 "github.com/light-speak/luxo/pkg/lux/rpc"
 "github.com/light-speak/luxo/pkg/lux/schema"
 "github.com/light-speak/luxo/pkg/lux/selection"
 "github.com/testcontainers/testcontainers-go"
 "github.com/testcontainers/testcontainers-go/modules/postgres"
)
func TestDurationWritePrecision(t *testing.T) {
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL")
 if connection==""{
  container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
  testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
  connection,err=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 }
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE readings(id bigint PRIMARY KEY,elapsed interval NOT NULL,optional_elapsed interval,laps interval[] NOT NULL,optional_laps interval[])");err!=nil{t.Fatal(err)}
 for _,mode:=range []pgx.QueryExecMode{pgx.QueryExecModeCacheStatement,pgx.QueryExecModeCacheDescribe,pgx.QueryExecModeDescribeExec,pgx.QueryExecModeExec,pgx.QueryExecModeSimpleProtocol}{
  t.Run(mode.String(),func(t *testing.T){testDurationWriteMode(t,connection,mode)})
 }
}
func testDurationWriteMode(t *testing.T,connection string,mode pgx.QueryExecMode) {
 cfg,err:=pgxpool.ParseConfig(connection);if err!=nil{t.Fatal(err)};cfg.ConnConfig.DefaultQueryExecMode=mode
 pool,err:=pgxpool.NewWithConfig(t.Context(),cfg);if err!=nil{t.Fatal(err)};db:=pg.NewDBFromPool(pool);t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"TRUNCATE readings");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);good:=time.Microsecond;bad:=time.Duration(1001)
 saved,err:=app.Reading.Create().SetId(1).SetElapsed(good).SetLaps([]time.Duration{good}).Exec(t.Context())
 if err!=nil||saved==nil||saved.Elapsed!=good||len(saved.Laps)!=1||saved.Laps[0]!=good{t.Fatal("exact generated write changed",saved,err)}
 for _,field:=range []string{"elapsed","optional","laps","optionalLaps"}{
  create:=app.Reading.Create().SetId(2).SetElapsed(good).SetLaps([]time.Duration{good})
  update:=app.Reading.Where(ReadingWhere.Id.Eq(1)).Update()
  switch field {
  case "elapsed":create.SetElapsed(bad);update.SetElapsed(bad)
  case "optional":create.SetOptionalElapsed(&bad);update.SetOptionalElapsed(&bad)
  case "laps":create.SetLaps([]time.Duration{good,good,good,bad,good});update.SetLaps([]time.Duration{good,good,good,bad,good})
  case "optionalLaps":create.SetOptionalLaps([]time.Duration{bad});update.SetOptionalLaps([]time.Duration{bad})
  }
  if value,err:=create.Exec(t.Context());value!=nil||!errors.Is(err,pg.ErrDurationPrecision){t.Fatal("generated create accepted inexact duration",field,value,err)}
  if count,err:=update.Exec(t.Context());count!=0||!errors.Is(err,pg.ErrDurationPrecision){t.Fatal("generated update accepted inexact duration",field,count,err)}
 }
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 testDurationWriteRequests(t,router,client,db)
}
func testDurationWriteRequests(t *testing.T,router *api.Router,client *rpc.Client,db *pg.DB) {
 for _,name:=range []string{"writeReading","changeReading"}{for protocol:=0;protocol<3;protocol++{
  for _,field:=range []string{"elapsed","optional","laps","optionalLaps"}{
   params:=map[string]any{"id":int64(2),"elapsed":time.Microsecond,"optional":nil,"laps":[]time.Duration{},"optionalLaps":nil}
   if name=="changeReading"{params["id"]=int64(1)}
   if field=="laps"||field=="optionalLaps"{params[field]=[]time.Duration{time.Microsecond,1001}}else{params[field]=time.Duration(1001)}
   callDurationWrite(t,router,client,name,params,protocol,true)
  }
 }}
 existing,err:=pg.QueryScalar[time.Duration](t.Context(),db,"SELECT elapsed FROM readings WHERE id=1")
 count,countErr:=pg.QueryScalar[int64](t.Context(),db,"SELECT count(*) FROM readings")
 if err!=nil||countErr!=nil||existing!=time.Microsecond||count!=1{t.Fatal("rejected request changed stored values",existing,count,err,countErr)}
 for protocol:=0;protocol<3;protocol++{
  if _,err:=pg.Exec(t.Context(),db,"DELETE FROM readings WHERE id=2");err!=nil{t.Fatal(err)}
  params:=map[string]any{"id":int64(2),"elapsed":time.Duration(-1000),"optional":nil,"laps":[]time.Duration{0,1000,-1000,2000,0},"optionalLaps":nil}
  callDurationWrite(t,router,client,"writeReading",params,protocol,false)
  value,err:=pg.QueryScalar[time.Duration](t.Context(),db,"SELECT elapsed FROM readings WHERE id=2")
  if err!=nil||value!=-time.Microsecond{t.Fatal("write did not recover after rejection",value,err)}
 }
}
func callDurationWrite(t *testing.T,router *api.Router,client *rpc.Client,name string,params map[string]any,protocol int,reject bool) {
 t.Helper();definition:=router.Schema.APIs[name];var mask []byte;var err error
 if name=="writeReading"{mask,err=schema.SelectToFieldMask([]*selection.Field{{Name:"id"}},router.Schema.Models["Reading"],router.Schema);if err!=nil{t.Fatal(err)}}
 body,err:=api.EncodeBinaryRequest(definition.ID,mask,params,router.Registry.ParamOrder(name));if err!=nil{t.Fatal(err)}
 if protocol==2{
  _,off:=codec.ReadVarint(body,0);size,n:=codec.ReadVarint(body,off);off+=n+int(size)
  data,err:=client.CallWithMaskContext(t.Context(),"",definition.ID,mask,body[off:])
  if reject{var failure *luxerrors.AppError;if !errors.As(err,&failure)||failure.Code!=500||len(data)!=0{t.Fatal("invalid duration escaped LRPC",name,data,err)}}else if err!=nil{t.Fatal(err)}
  return
 }
 if protocol==0{
  params["$api"]=name;if name=="writeReading"{params["$select"]="id"}
  body,err=json.Marshal(params);if err!=nil{t.Fatal(err)}
 }
 request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));if protocol==1{request.Header.Set("X-Luxo-Mode","binary")}
 response:=httptest.NewRecorder();router.ServeHTTP(response,request);want:=200;if reject{want=500}
 if response.Code!=want{t.Fatalf("duration write response mismatch: api=%s protocol=%d status=%d body=%s",name,protocol,response.Code,response.Body.String())}
}
`
