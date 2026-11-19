package codegen

import (
	"strconv"
	"testing"
)

const uuidFailureSource = `
fn rejectUUID(value: UUID): UUID { throw error.NotFound }
model Entry @noTime @crud { id: UUID @id @auto value: Int }
model Twin @noTime { id: UUID @id @auto token: UUID @auto }
model Hooked @noTime { id: UUID @id @auto @beforeSave { rejectUUID(it) } }
model OptionalUUID @noTime { id: Int @id token: UUID? @auto }
model PreparedUUID @noTime { id: UUID @id @auto @beforeSave { it } }
model OptionalPreparedUUID @noTime { id: Int @id token: UUID? @auto @beforeSave { it } }
api autoEntry(value: Int): Entry { return Entry.create(value: value) }
`

func TestGeneratedUUIDFailureReturnsError(t *testing.T) {
	runGeneratedFixture(t, uuidFailureSource, uuidFailureAssertions, uuidFailureIDs())
}

func uuidFailureIDs() StableIDs {
	return StableIDs{ModelFields: map[string]map[string]int{
		"Entry": {"id": 1, "value": 2}, "Twin": {"id": 1, "token": 2}, "Hooked": {"id": 1}, "OptionalUUID": {"id": 1, "token": 2},
		"PreparedUUID":         {"id": 1},
		"OptionalPreparedUUID": {"id": 1, "token": 2},
	}}
}

func TestGeneratedUUIDSuccessPostgres(t *testing.T) {
	input := analyzePermissionFixture(t, uuidFailureSource)
	state, err := ComputeState(input, nil, pgDialect())
	if err != nil {
		t.Fatal(err)
	}
	sql, _, migrationSQLFailure1 := GenerateMigrationSQL(mustMigrationDiff(t, &SchemaState{Models: map[string]*ModelState{}}, state, nil), state, pgDialect())
	if migrationSQLFailure1 != nil {
		t.Fatal(migrationSQLFailure1)
	}
	runGeneratedFixture(t, uuidFailureSource, uuidSuccessAssertions+"\nconst uuidSuccessSQL = "+strconv.Quote(sql), uuidFailureIDs())
}

const uuidFailureAssertions = `package generated
import (
 "bytes"
 "errors"
 "io"
 "net/http/httptest"
 "strconv"
 "strings"
 "testing"
 "testing/iotest"
 "github.com/google/uuid"
 "github.com/light-speak/luxo/pkg/lux/api"
 "github.com/light-speak/luxo/pkg/lux/codec"
 luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)
func TestUUIDFailures(t *testing.T) {
 uuid.DisableRandPool()
 app:=NewFromDB(nil)
 samples:=[]struct{name string;prefix int;call func()error}{
  {"create",0,func()error{row,err:=app.Entry.Create().SetValue(1).Exec(t.Context());if row!=nil{t.Error("partial result",row)};return err}},
  {"batchFirst",0,func()error{rows,err:=app.Entry.CreateMany().Add(EntryCreateInput{Value:1}).Exec(t.Context());if len(rows)!=0{t.Error("partial result",rows)};return err}},
  {"batchSecond",16,func()error{rows,err:=app.Entry.CreateMany().Add(EntryCreateInput{Value:1}).Add(EntryCreateInput{Value:2}).Exec(t.Context());if len(rows)!=0{t.Error("partial result",rows)};return err}},
  {"secondField",16,func()error{_,err:=app.Twin.Create().Exec(t.Context());return err}},
  {"secondBatchField",16,func()error{_,err:=app.Twin.CreateMany().Add(TwinCreateInput{}).Exec(t.Context());return err}},
  {"beforeSaveOrder",0,func()error{_,err:=app.Hooked.Create().Exec(t.Context());return err}},
  {"nullable",0,func()error{_,err:=app.OptionalUUID.Create().SetId(1).Exec(t.Context());return err}},
  {"nullableBatch",0,func()error{_,err:=app.OptionalUUID.CreateMany().Add(OptionalUUIDCreateInput{Id:1}).Exec(t.Context());return err}},
  {"nullablePrepared",0,func()error{_,err:=app.OptionalPreparedUUID.Create().SetId(1).Exec(t.Context());return err}},
  {"nullablePreparedBatch",0,func()error{_,err:=app.OptionalPreparedUUID.CreateMany().Add(OptionalPreparedUUIDCreateInput{Id:1}).Exec(t.Context());return err}},
 }
 for _,sample:=range samples{t.Run(sample.name,func(t *testing.T){withUUIDFailure(t,sample.prefix,func(){if err:=sample.call();!errors.Is(err,io.ErrUnexpectedEOF){t.Error("UUID error was lost or database was reached",err)}})})}
 t.Run("emptyBatch",func(t *testing.T){withUUIDFailure(t,0,func(){rows,err:=app.Entry.CreateMany().Exec(t.Context());if err!=nil||len(rows)!=0{t.Fatal(rows,err)}})})
 t.Run("saveFailure",func(t *testing.T){uuid.SetRand(bytes.NewReader(make([]byte,16)));defer uuid.SetRand(nil);if _,err:=app.Hooked.Create().Exec(t.Context());!errors.Is(err,luxerrors.NotFound){t.Fatal(err)}})
 for _,binary:=range []bool{false,true}{for _,name:=range []string{"createEntry","autoEntry"}{t.Run(name+"/binary="+strconv.FormatBool(binary),func(t *testing.T){withUUIDFailure(t,0,func(){testUUIDFailureHTTP(t,app,name,binary)})})}}
}
func withUUIDFailure(t *testing.T,prefix int,run func()) {
 t.Helper();uuid.SetRand(io.MultiReader(bytes.NewReader(make([]byte,prefix)),iotest.ErrReader(io.ErrUnexpectedEOF)));defer uuid.SetRand(nil)
 defer func(){if value:=recover();value!=nil{t.Error("UUID generation panicked",value)}}()
 run()
}
func testUUIDFailureHTTP(t *testing.T,app *App,name string,binary bool){
 t.Helper();router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 body:=[]byte("{\"$api\":\""+name+"\",\"$select\":\"id,value\",\"value\":1}")
 if binary{mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)};body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,mask,map[string]any{"value":int64(1)},router.Registry.ParamOrder(name));if err!=nil{t.Fatal(err)}}
 request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
 response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=500||!strings.Contains(response.Body.String(),"Internal"){t.Error("UUID failure did not become explicit internal error",response.Code,response.Body.String())}
}
`

const uuidSuccessAssertions = `package generated
import(
 "context"
 "errors"
 "testing"
 "github.com/google/uuid"
 "github.com/light-speak/luxo/internal/testutil"
 "github.com/light-speak/luxo/pkg/lux/pg"
 "github.com/testcontainers/testcontainers-go"
 "github.com/testcontainers/testcontainers-go/modules/postgres"
)
func TestUUIDSuccess(t *testing.T){
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies());testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,uuidSuccessSQL);err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);builder:=app.Entry.Create().SetValue(7)
 canceled,cancel:=context.WithCancel(t.Context());cancel();if _,err:=builder.Exec(canceled);!errors.Is(err,context.Canceled){t.Fatal(err)}
 first,err:=builder.Exec(t.Context());if err!=nil||first==nil||first.Id.Version()!=7{t.Fatal(first,err)}
 second,err:=builder.Exec(t.Context());if err!=nil||second==nil||first.Id==second.Id||len(builder.Cols)!=1{t.Fatal(first,second,err)}
 batch,err:=app.Twin.CreateMany().Add(TwinCreateInput{}).Add(TwinCreateInput{}).Exec(t.Context());if err!=nil||len(batch)!=2{t.Fatal(batch,err)}
 for _,row:=range batch{if row.Id==uuid.Nil||row.Token==uuid.Nil||row.Id==row.Token||row.Id.Version()!=7||row.Token.Version()!=7{t.Fatal(row)}}
 if batch[0].Id==batch[1].Id||batch[0].Token==batch[1].Token{t.Fatal("reused batch UUID")}
 nullable,err:=app.OptionalUUID.Create().SetId(1).Exec(t.Context());if err!=nil||nullable==nil||nullable.Token==nil||nullable.Token.Version()!=7{t.Fatal(nullable,err)}
 optionalPrepared,err:=app.OptionalPreparedUUID.CreateMany().Add(OptionalPreparedUUIDCreateInput{Id:1}).Add(OptionalPreparedUUIDCreateInput{Id:2}).Exec(t.Context());if err!=nil||len(optionalPrepared)!=2||optionalPrepared[0].Token==nil||optionalPrepared[1].Token==nil||*optionalPrepared[0].Token==*optionalPrepared[1].Token{t.Fatal(optionalPrepared,err)}
 prepared,err:=app.PreparedUUID.Create().Exec(t.Context());if err!=nil||prepared==nil||prepared.Id.Version()!=7{t.Fatal(prepared,err)}
 preparedRows,err:=app.PreparedUUID.CreateMany().Add(PreparedUUIDCreateInput{}).Add(PreparedUUIDCreateInput{}).Exec(t.Context());if err!=nil||len(preparedRows)!=2||preparedRows[0].Id==preparedRows[1].Id{t.Fatal(preparedRows,err)}
}
`
