package codegen

import (
	"strconv"
	"testing"
)

const automaticSaveSource = `
fn rejectDate(value: DateTime): DateTime { throw error.NotFound }
fn rejectUUID(value: UUID): UUID { throw error.NotFound }
fn keepUUID(value: UUID): UUID { return value }
fn shiftDate(value: DateTime): DateTime { return value + 1000h }
model Shifted @crud { id: Int @id value: Int createdAt: DateTime @beforeSave { it + 1000h } updatedAt: DateTime @beforeSave { it + 1000h } }
model RejectCreate { id: Int @id createdAt: DateTime @beforeSave { rejectDate(it) } }
model RejectUpdate { id: Int @id value: Int updatedAt: DateTime @beforeSave { rejectDate(it) } }
model RejectUUID @noTime { id: UUID @id @auto @beforeSave { rejectUUID(it) } }
model UUIDAuto @noTime { id: UUID @id @auto @beforeSave { keepUUID(it) } }
model NullableAuto { id: Int @id createdAt: DateTime? @beforeSave { it + 1000h } updatedAt: DateTime? @beforeSave { shiftDate(it) } }
fn saveShifted(item: Shifted): Int { return item.update(value: 3) }
api autoCreateShifted(id: Int): Shifted { return Shifted.create(id: id, value: 1) }
api autoUpdateShifted(id: Int): Int { val item = Shifted.where(id: id).first() ?: throw error.NotFound return item.update(value: 2) }
`

func TestGeneratedAutomaticSavePreparation(t *testing.T) {
	input := analyzePermissionFixture(t, automaticSaveSource)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	state, err := ComputeState(input, nil, pgDialect())
	if err != nil {
		t.Fatal(err)
	}
	sql, _, migrationSQLFailure1 := GenerateMigrationSQL(mustMigrationDiff(t, &SchemaState{Models: map[string]*ModelState{}}, state, nil), state, pgDialect())
	if migrationSQLFailure1 != nil {
		t.Fatal(migrationSQLFailure1)
	}
	ids := StableIDs{ModelFields: map[string]map[string]int{}}
	for _, model := range input.Files[0].Models {
		fields := map[string]int{}
		for index, field := range model.Fields {
			fields[field.Name] = index + 1
		}
		ids.ModelFields[model.Name] = fields
	}
	runGeneratedFixture(t, automaticSaveSource, automaticSaveAssertions+"\nconst automaticSaveSQL = "+strconv.Quote(sql), ids)
}

const automaticSaveAssertions = `package generated
import (
 "bytes"
 "errors"
 "net/http/httptest"
 "strconv"
 "testing"
 "time"
 "github.com/google/uuid"
 "github.com/light-speak/luxo/internal/testutil"
 "github.com/light-speak/luxo/pkg/lux/api"
 "github.com/light-speak/luxo/pkg/lux/codec"
 luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
 "github.com/light-speak/luxo/pkg/lux/pg"
 "github.com/testcontainers/testcontainers-go"
 "github.com/testcontainers/testcontainers-go/modules/postgres"
)
func TestAutomaticSave(t *testing.T) {
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies());testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,automaticSaveSQL);err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 t.Run("failures",func(t *testing.T){testAutomaticSaveFailures(t,app,db)})
 t.Run("values",func(t *testing.T){testAutomaticSaveValues(t,app,db,router)})
 t.Run("nullableAndUUID",func(t *testing.T){testAutomaticSaveEdges(t,app,db)})
}
func testAutomaticSaveFailures(t *testing.T,app *App,db *pg.DB) {
 if _,err:=pg.Exec(t.Context(),db,"INSERT INTO reject_updates(id,value,updated_at) VALUES(1,7,'2000-01-01T00:00:00Z')");err!=nil{t.Fatal(err)}
 cases:=[]struct{name string;run func()error}{
  {"create",func()error{_,err:=app.RejectCreate.Create().SetId(1).Exec(t.Context());return err}},
  {"createMany",func()error{_,err:=app.RejectCreate.CreateMany().Add(RejectCreateCreateInput{Id:2}).Exec(t.Context());return err}},
  {"uuid",func()error{_,err:=app.RejectUUID.Create().Exec(t.Context());return err}},
  {"uuidMany",func()error{_,err:=app.RejectUUID.CreateMany().Add(RejectUUIDCreateInput{}).Exec(t.Context());return err}},
  {"update",func()error{_,err:=newRejectUpdateUpdateBuilder(db,"reject_updates",1).SetValue(8).Exec(t.Context());return err}},
  {"updateWhere",func()error{_,err:=newRejectUpdateUpdateBuilder(db,"reject_updates",1).SetValue(9).ExecWhere(t.Context(),RejectUpdateWhere.Id.Eq(1));return err}},
  {"queryUpdate",func()error{_,err:=app.RejectUpdate.Where(RejectUpdateWhere.Id.Eq(1)).Update().SetValue(10).Exec(t.Context());return err}},
 }
 for _,sample:=range cases{t.Run(sample.name,func(t *testing.T){if err:=sample.run();!errors.Is(err,luxerrors.NotFound){t.Error("automatic field bypassed save failure",err)}})}
 for _,table:=range []string{"reject_creates","reject_uuids"}{count,err:=pg.QueryScalar[int64](t.Context(),db,"SELECT count(*) FROM "+table);if err!=nil||count!=0{t.Error("failed preparation wrote rows",table,count,err)}}
 value,err:=pg.QueryScalar[int64](t.Context(),db,"SELECT value FROM reject_updates WHERE id=1");if err!=nil||value!=7{t.Error("failed preparation updated a row",value,err)}
}
func testAutomaticSaveValues(t *testing.T,app *App,db *pg.DB,router *api.Router) {
 nextID:=int64(100)
 calls:=[]struct{name string;create bool;run func(int64)error}{
  {"create",true,func(id int64)error{_,err:=app.Shifted.Create().SetId(id).SetValue(1).Exec(t.Context());return err}},
  {"createMany",true,func(id int64)error{_,err:=app.Shifted.CreateMany().Add(ShiftedCreateInput{Id:id,Value:1}).Exec(t.Context());return err}},
  {"update",false,func(id int64)error{_,err:=newShiftedUpdateBuilder(db,"shifteds",id).SetValue(2).Exec(t.Context());return err}},
  {"updateWhere",false,func(id int64)error{_,err:=newShiftedUpdateBuilder(db,"shifteds",id).SetValue(2).ExecWhere(t.Context(),ShiftedWhere.Id.Eq(id));return err}},
  {"queryUpdate",false,func(id int64)error{_,err:=app.Shifted.Where(ShiftedWhere.Id.Eq(id)).Update().SetValue(2).Exec(t.Context());return err}},
  {"instanceUpdate",false,func(id int64)error{_,err:=app.saveShifted(t.Context(),&Shifted{Id:id});return err}},
 }
 for _,binary:=range []bool{false,true}{for _,name:=range []string{"createShifted","updateShifted","autoCreateShifted","autoUpdateShifted"}{
  create:=name=="createShifted"||name=="autoCreateShifted"
  calls=append(calls,struct{name string;create bool;run func(int64)error}{name+"/binary="+strconv.FormatBool(binary),create,func(id int64)error{return automaticSaveRequest(t,router,name,id,binary)}})
 }}
 for _,sample:=range calls{t.Run(sample.name,func(t *testing.T){
  nextID++;id:=nextID
  if !sample.create{if _,err:=pg.Exec(t.Context(),db,"INSERT INTO shifteds(id,value,created_at,updated_at) VALUES($1,1,'2000-01-01T00:00:00Z','2000-01-01T00:00:00Z')",id);err!=nil{t.Fatal(err)}}
  before:=time.Now().Add(1000*time.Hour-time.Second)
  if err:=sample.run(id);err!=nil{t.Fatal(err)}
  after:=time.Now().Add(1000*time.Hour+time.Second)
  row,err:=app.Shifted.Find(t.Context(),id);if err!=nil||row==nil{t.Fatal(row,err)}
  if row.UpdatedAt.Before(before)||row.UpdatedAt.After(after){t.Error("updatedAt bypassed or repeated conversion",row.UpdatedAt,before,after)}
  if sample.create&&(row.CreatedAt.Before(before)||row.CreatedAt.After(after)){t.Error("createdAt bypassed or repeated conversion",row.CreatedAt)}
 })}
}
func testAutomaticSaveEdges(t *testing.T,app *App,db *pg.DB) {
 rows,err:=app.UUIDAuto.CreateMany().Add(UUIDAutoCreateInput{}).Add(UUIDAutoCreateInput{}).Exec(t.Context());if err!=nil||len(rows)!=2{t.Fatal(rows,err)}
 if rows[0].Id==uuid.Nil||rows[0].Id==rows[1].Id||rows[0].Id.Version()!=7{t.Fatal("invalid prepared UUID",rows)}
 row,err:=app.UUIDAuto.Create().Exec(t.Context());if err!=nil||row==nil||row.Id==uuid.Nil||row.Id==rows[0].Id{t.Fatal(row,err)}
 before:=time.Now().Add(1000*time.Hour-time.Second)
 nullable,err:=app.NullableAuto.CreateMany().Add(NullableAutoCreateInput{Id:1}).Add(NullableAutoCreateInput{Id:2}).Exec(t.Context());if err!=nil||len(nullable)!=2{t.Fatal(nullable,err)}
 for _,row:=range nullable{if row.CreatedAt==nil||row.UpdatedAt==nil||row.CreatedAt.Before(before)||!row.CreatedAt.Equal(*row.UpdatedAt){t.Fatal("invalid nullable automatic value",row)}}
 single,err:=app.NullableAuto.Create().SetId(3).Exec(t.Context());if err!=nil||single==nil||single.CreatedAt==nil||single.UpdatedAt==nil||single.CreatedAt.Before(before){t.Fatal(single,err)}
 if _,err:=newNullableAutoUpdateBuilder(db,"nullable_autos",3).Exec(t.Context());err!=nil{t.Fatal(err)}
 if _,err:=app.NullableAuto.Where(NullableAutoWhere.Id.Eq(3)).Update().Exec(t.Context());err!=nil{t.Fatal(err)}
 empty,err:=app.RejectUUID.CreateMany().Exec(t.Context());if err!=nil||len(empty)!=0{t.Fatal("empty batch ran row preparation",empty,err)}
}
func automaticSaveRequest(t *testing.T,router *api.Router,name string,id int64,binary bool)error {
 t.Helper();body:=[]byte("{\"$api\":\""+name+"\",\"id\":"+strconv.FormatInt(id,10))
 if name!="autoUpdateShifted"{body=append(body,",\"$select\":\"id,value,createdAt,updatedAt\""...)}
 if name=="createShifted"||name=="updateShifted"{body=append(body,",\"value\":1"...)};body=append(body,'}')
 if binary{mask,err:=codec.AppendSelectionMask(nil,[]byte{15},nil);if err!=nil{return err};if name=="autoUpdateShifted"{mask=nil};params:=map[string]any{"id":id};if name=="createShifted"||name=="updateShifted"{params["value"]=int64(1)};body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,mask,params,router.Registry.ParamOrder(name));if err!=nil{return err}}
 request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
 response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{return errors.New(strconv.Itoa(response.Code)+": "+response.Body.String())};return nil
}
`
