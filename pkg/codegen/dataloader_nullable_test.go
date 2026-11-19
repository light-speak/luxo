package codegen

import (
	"strings"
	"testing"
)

func TestExplicitByCustomKeyGeneratedCompilation(t *testing.T) {
	for _, sample := range []struct{ name, source string }{
		{"nullable_string", `model Target @noTime { key: String @id } model Source @noTime { id: Int @id ownerId: String? target: Target? @by(key, ownerId) }`},
		{"uuid_list", `model Target @noTime { id: Int @id sourceId: UUID? } model Source @noTime { key: UUID @id targets: [Target] @by(sourceId) }`},
		{"inherited_string", `model Base @noTime { key: String @id } model Source : Base { targets: [Target] @by(sourceKey) } model Target @noTime { id: Int @id sourceKey: String }`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			runGeneratedFixture(t, sample.source, "package generated\n", StableIDs{ModelFields: map[string]map[string]int{
				"Base":   {"key": 1},
				"Source": {"id": 1, "ownerId": 2, "target": 3, "key": 4, "targets": 5, "createdAt": 6, "updatedAt": 7},
				"Target": {"id": 1, "key": 2, "sourceId": 3, "sourceKey": 4},
			}})
		})
	}
}

func TestGeneratedNullableRemoteRelationKeys(t *testing.T) {
	for _, keyType := range []string{"Int", "String", "UUID"} {
		for _, explicit := range []bool{false, true} {
			name := keyType + "/implicit"
			directive := ""
			if explicit {
				name, directive = keyType+"/explicit", " @by(sourceId)"
			}
			t.Run(name, func(t *testing.T) {
				source := "model Source @noTime { id: " + keyType + " @id targets: [Target]" + directive + " target: Target?" + directive + " }\n" +
					"model Target @noTime { id: Int @id sourceId: " + keyType + "? }"
				runGeneratedFixture(t, source, "package generated\n", StableIDs{ModelFields: map[string]map[string]int{
					"Source": {"id": 1, "targets": 2, "target": 3}, "Target": {"id": 1, "sourceId": 2},
				}})
			})
		}
	}
}

func TestGeneratedNullableLocalListRecursion(t *testing.T) {
	for _, sample := range []struct{ luxoType, goType string }{{"Int", "int64"}, {"String", "string"}, {"UUID", "uuid.UUID"}} {
		t.Run(sample.luxoType, func(t *testing.T) {
			source, ids := nullableLocalListFixture(sample.luxoType)
			runGeneratedFixture(t, source, strings.ReplaceAll(nullableLocalListRecursionAssertions, "KEY_TYPE", sample.goType), ids)
		})
	}
}

func nullableLocalListFixture(keyType string) (string, StableIDs) {
	source := strings.ReplaceAll(`
model Source @noTime { id: Int @id groupKey: KEY_TYPE? targets: [Target] @by(groupKey, groupKey) }
model Target @noTime { id: Int @id groupKey: KEY_TYPE leafId: Int? leaf: Leaf? @by(id, leafId) }
model Leaf @noTime { id: Int @id name: String }
`, "KEY_TYPE", keyType)
	return source, StableIDs{ModelFields: map[string]map[string]int{
		"Source": {"id": 1, "groupKey": 2, "targets": 3},
		"Target": {"id": 1, "groupKey": 2, "leafId": 3, "leaf": 4},
		"Leaf":   {"id": 1, "name": 2},
	}}
}

const nullableLocalListRecursionAssertions = `package generated
import("context";"errors";"testing";"github.com/google/uuid";"github.com/light-speak/luxo/pkg/lux/dataloader";"github.com/light-speak/luxo/pkg/lux/selection")
var _ = uuid.Nil
func TestNullableLocalListRecursion(t *testing.T){
 for _,batch:=range []bool{false,true}{for _,missing:=range []bool{false,true}{for _,fail:=range []string{"","targets","leaf"}{
  var key KEY_TYPE;leafID,otherLeafID:=int64(7),int64(8);targetCalls,leafCalls:=0,0;failure:=errors.New("failed relation")
  app:=&App{};cfg:=dataloader.DefaultConfig();cfg.MaxBatch=1
  app.SetLoaders(NewLoaders(cfg,
   func(_ context.Context,keys []KEY_TYPE,fields []*selection.Field)(map[KEY_TYPE][]*Target,error){
    targetCalls++;if len(keys)!=1||keys[0]!=key||selection.Format(fields)!="id,leaf{id,name}"{t.Fatal(keys,selection.Format(fields))}
    if fail=="targets"{return nil,failure};return map[KEY_TYPE][]*Target{key:{{Id:1,GroupKey:key,LeafId:&leafID},{Id:2,GroupKey:key,LeafId:&otherLeafID},{Id:3,GroupKey:key}}},nil
   },
   func(_ context.Context,keys []int64,fields []*selection.Field)(map[int64]*Leaf,error){
    leafCalls++;if len(keys)!=2||keys[0]!=leafID||keys[1]!=otherLeafID||selection.Format(fields)!="id,name"{t.Fatal("nested keys were not batched",keys,selection.Format(fields))}
    if fail=="leaf"{return nil,failure};return map[int64]*Leaf{leafID:{Id:leafID,Name:"nested"},otherLeafID:{Id:otherLeafID,Name:"other"}},nil
   },
  ))
  row:=&Source{Id:1};if !missing{row.GroupKey=&key}
  fields:=[]*selection.Field{{Name:"targets",Children:[]*selection.Field{{Name:"id"},{Name:"leaf",Children:[]*selection.Field{{Name:"id"},{Name:"name"}}}}}}
  var err error;if batch{err=resolveSourceListRelations(t.Context(),app,[]*Source{row},fields)}else{err=resolveSourceRelations(t.Context(),app,row,fields)}
  if missing{if err!=nil||targetCalls!=0||leafCalls!=0||len(row.Targets)!=0{t.Fatal("nil key was queried",err,targetCalls,leafCalls,row)};continue}
  if fail!=""{if !errors.Is(err,failure){t.Fatal("nested failure was swallowed",err)};continue}
  if err!=nil||targetCalls!=1||leafCalls!=1||len(row.Targets)!=3||row.Targets[0].Leaf==nil||row.Targets[0].Leaf.Name!="nested"||row.Targets[1].Leaf==nil||row.Targets[1].Leaf.Name!="other"||row.Targets[2].Leaf!=nil{t.Fatal("selected nested relation missing",err,targetCalls,leafCalls,row)}
 }}}
}
`

func TestGeneratedNullableLocalListRecursionPostgres(t *testing.T) {
	source, ids := nullableLocalListFixture("String")
	source = strings.Replace(source, "model Source @noTime", "model Source @noTime @crud", 1)
	runGeneratedFixture(t, source, nullableLocalListPostgresAssertions, ids)
}

const nullableLocalListPostgresAssertions = `package generated
import("bytes";"encoding/json";"net/http/httptest";"os";"strconv";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/dataloader";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/light-speak/luxo/pkg/lux/schema";"github.com/light-speak/luxo/pkg/lux/selection")
func TestNullableLocalListDatabase(t *testing.T){
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 ctx:=t.Context();db,err:=pg.NewDB(ctx,connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 sql:="CREATE TABLE sources(id bigint PRIMARY KEY,group_key text);CREATE TABLE targets(id bigint PRIMARY KEY,group_key text,leaf_id bigint);CREATE TABLE leafs(id bigint PRIMARY KEY,name text);INSERT INTO sources VALUES(1,'group'),(2,NULL);INSERT INTO targets VALUES(1,'group',7),(2,'group',NULL),(3,'other',7);INSERT INTO leafs VALUES(7,'nested')"
 if _,err:=pg.Exec(ctx,db,sql);err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);cfg:=dataloader.DefaultConfig();cfg.MaxBatch=1;app.SetLoaders(NewDefaultLoaders(app,cfg))
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,broken:=range []bool{false,true}{
  if broken{if _,err:=pg.Exec(ctx,db,"DROP TABLE leafs");err!=nil{t.Fatal(err)}}
  for _,id:=range []int64{1,2}{for _,binary:=range []bool{false,true}{
   response:=nullableListHTTP(t,router,id,binary)
   if broken&&id==1{if response.Code!=500||bytes.Contains(response.Body.Bytes(),[]byte("relation"))||bytes.Contains(response.Body.Bytes(),[]byte("leafs")){t.Fatal("nested database failure became success or leaked SQL",response.Code,response.Body.String())};continue}
   if response.Code!=200{t.Fatal(response.Code,response.Body.String())};var row Source
   if binary{dec:=codec.NewDecoder(response.Body.Bytes());row.ReadLuxo(dec);if err:=dec.Finish();err!=nil{t.Fatal(err)}}else{var envelope struct{Data Source};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil{t.Fatal(err)};row=envelope.Data}
   if row.Id!=id{t.Fatal(row)}
   if id==2{if len(row.Targets)!=0{t.Fatal("NULL key acquired rows",row)};continue}
   if len(row.Targets)!=2{t.Fatal("selected list missing or acquired foreign rows",row)}
   for _,target:=range row.Targets{switch target.Id{case 1:if target.Leaf==nil||target.Leaf.Id!=7||target.Leaf.Name!="nested"{t.Fatal("selected nested relation missing",target)};case 2:if target.Leaf!=nil{t.Fatal("NULL child key acquired a row",target)};default:t.Fatal("foreign relation row",target)}}
  }}
 }
}
func nullableListHTTP(t *testing.T,router *api.Router,id int64,binary bool)*httptest.ResponseRecorder{
 t.Helper();fields:=[]*selection.Field{{Name:"id"},{Name:"targets",Children:[]*selection.Field{{Name:"id"},{Name:"leaf",Children:[]*selection.Field{{Name:"id"},{Name:"name"}}}}}}
 body:=[]byte("{\"$api\":\"getSource\",\"id\":"+strconv.FormatInt(id,10)+",\"$select\":\""+selection.Format(fields)+"\"}")
 if binary{mask,err:=schema.SelectToFieldMask(fields,router.Schema.Models["Source"],router.Schema);if err!=nil{t.Fatal(err)};body,err=api.EncodeBinaryRequest(router.Schema.APIs["getSource"].ID,mask,map[string]any{"id":id},router.Registry.ParamOrder("getSource"));if err!=nil{t.Fatal(err)}}
 request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
 response:=httptest.NewRecorder();router.ServeHTTP(response,request);return response
}
`

func TestNamedLoadRejectsNullableKeysBeforeGeneration(t *testing.T) {
	input := analyzePermissionFixture(t, `model Target @noTime { id: Int @id sourceId: Int? label: String? }
api named(key: Int): [Target] { return Target.load(sourceId: key) }
api composite(key: Int, label: String): [Target] { return Target.load(sourceId: key, label: label) }
`)
	if len(input.Errors) != 3 {
		t.Fatal("nullable named keys unexpectedly accepted", input.Errors)
	}
	files, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG})
	if err == nil || files != nil {
		t.Fatal("invalid named keys produced output", err)
	}
}

func TestGeneratedNullableBatchGrouping(t *testing.T) {
	for _, keyType := range []string{"int64", "string", "uuid.UUID"} {
		t.Run(keyType, func(t *testing.T) {
			var source strings.Builder
			source.WriteString(strings.ReplaceAll(nullableBatchDeclarations, "KEY_TYPE", keyType))
			for _, list := range []bool{true, false} {
				name, valueType := "single", "*Target"
				if list {
					name, valueType = "many", "[]*Target"
				}
				source.WriteString("func " + name + "(app *App) []func(context.Context, []" + keyType + ", []*selection.Field) (map[" + keyType + "]" + valueType + ", error) { return []func(context.Context, []" + keyType + ", []*selection.Field) (map[" + keyType + "]" + valueType + ", error) {\n")
				defaultGenerator().generateBatchFunc(&source, "Source", Relation{TargetName: "Target", RemoteKey: "sourceId", KeyGoType: keyType, IsList: list, RemoteKeyNullable: true}, false)
				source.WriteString("}}\n")
			}
			runGeneratedProtocolPackage(t, source.String(), strings.ReplaceAll(nullableBatchAssertions, "KEY_TYPE", keyType))
		})
	}
}

const nullableBatchDeclarations = `package generated
import("context"; "github.com/google/uuid"; "github.com/light-speak/luxo/pkg/lux"; "github.com/light-speak/luxo/pkg/lux/selection"; "github.com/light-speak/luxo/pkg/lux/dataloader")
var _ = uuid.Nil
var _ = dataloader.DefaultConfig
var scanTarget int
type Target struct{Id int; SourceId *KEY_TYPE}
type fixtureDB struct{rows []*Target}
type App struct{DB fixtureDB}
func(f fixtureDB) QueryRows(context.Context,int,string,...any)([]*Target,error){return f.rows,nil}
func selectTargetSQLColumns([]*selection.Field)[]string{return []string{"id"}}
func ensureField(fields []string,field string)[]string{return append(fields,field)}
`

const nullableBatchAssertions = `package generated
import ("testing"; "errors"; "github.com/google/uuid"; "github.com/light-speak/luxo/pkg/lux/dataloader")
var _ = uuid.Nil
func TestGrouping(t *testing.T){
 var zero KEY_TYPE
 otherZero:=zero
 first,second:=&Target{Id:1,SourceId:&zero},&Target{Id:2,SourceId:&otherZero}
 app:=&App{DB:fixtureDB{rows:[]*Target{{Id:99},first,second}}}
 grouped,err:=many(app)[0](t.Context(),[]KEY_TYPE{zero},nil)
 if err!=nil || len(grouped)!=1 || len(grouped[zero])!=2 || grouped[zero][0]!=first || grouped[zero][1]!=second {t.Fatalf("NULL or pointer identity changed list grouping: %v %v",grouped,err)}
 app.DB.rows=[]*Target{first,second}
 if got,err:=single(app)[0](t.Context(),[]KEY_TYPE{zero},nil);!errors.Is(err,dataloader.ErrNonUniqueRelation)||got!=nil{t.Fatal("ambiguous single relation returned success or partial data",got,err)}
 app.DB.rows=[]*Target{first,{Id:99}}
 single,err:=single(app)[0](t.Context(),[]KEY_TYPE{zero},nil)
 if err!=nil || len(single)!=1 || single[zero]!=first {t.Fatalf("NULL replaced a valid zero key: %v %v",single,err)}
 app.DB.rows=[]*Target{{Id:99}}
 grouped,err=many(app)[0](t.Context(),[]KEY_TYPE{zero},nil)
 if err!=nil || len(grouped)!=0 {t.Fatalf("NULL created an empty-value group: %v %v",grouped,err)}
}
`

func TestGeneratedNullableRelationPostgres(t *testing.T) {
	runGeneratedFixture(t, `model Source @noTime { key: UUID @id targets: [Target] @by(sourceId) target: Target? @by(sourceId) }
model Target @noTime { id: Int @id sourceId: UUID? }
api lookupSource(key: UUID): Source { return Source.find(key: key) }
api getSourceByKey
`, nullableRelationPostgresAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"Source": {"key": 1, "targets": 2, "target": 3}, "Target": {"id": 1, "sourceId": 2},
	}})
}

const nullableRelationPostgresAssertions = `package generated
import (
 "testing"
 "bytes"
 "net/http/httptest"
 "strings"
 "github.com/light-speak/luxo/pkg/lux/api"
 "github.com/light-speak/luxo/pkg/lux/schema"
 "github.com/light-speak/luxo/pkg/lux/codec"
 "errors"
 "github.com/google/uuid"
 "github.com/light-speak/luxo/internal/testutil"
 "github.com/light-speak/luxo/pkg/lux/dataloader"
 "github.com/light-speak/luxo/pkg/lux/pg"
 "github.com/light-speak/luxo/pkg/lux/selection"
 "github.com/testcontainers/testcontainers-go"
 "github.com/testcontainers/testcontainers-go/modules/postgres"
)
func TestNullableRelationDatabase(t *testing.T){
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE sources(key uuid PRIMARY KEY);INSERT INTO sources VALUES('00000000-0000-0000-0000-000000000000');CREATE TABLE targets(id bigint PRIMARY KEY,source_id uuid);INSERT INTO targets VALUES(99,NULL),(1,'00000000-0000-0000-0000-000000000000'),(2,'00000000-0000-0000-0000-000000000009')");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);cfg:=dataloader.DefaultConfig();cfg.MaxBatch=1
 loaders:=NewDefaultLoaders(app,cfg)
 zero,other,missing:=uuid.Nil,uuid.MustParse("00000000-0000-0000-0000-000000000009"),uuid.MustParse("00000000-0000-0000-0000-000000000042")
 fields:=[]*selection.Field{{Name:"id"}}
 many,err:=loaders.SourceTargets.LoadAll(t.Context(),[]uuid.UUID{zero,other,missing},fields)
 if err!=nil{t.Fatal(err)}
 for i,key:=range []uuid.UUID{zero,other}{if len(many[key])!=1||many[key][0].Id!=int64(i+1)||many[key][0].SourceId==nil||*many[key][0].SourceId!=key{t.Fatalf("invalid nullable relation group: key=%s rows=%+v",key,many[key])}}
 if len(many[missing])!=0{t.Fatal("missing key acquired a NULL row")}
 single,err:=loaders.SourceTarget.LoadAll(t.Context(),[]uuid.UUID{zero,other,missing},fields)
 if err!=nil{t.Fatal(err)}
 if single[zero]==nil||single[zero].Id!=1||single[other]==nil||single[other].Id!=2||single[missing]!=nil{t.Fatalf("invalid single grouping: %+v",single)}
 testDuplicateRelationHTTP(t,app,db,zero)
}
func testDuplicateRelationHTTP(t *testing.T,app *App,db *pg.DB,key uuid.UUID){
 t.Helper();app.SetLoaders(NewDefaultLoaders(app,dataloader.DefaultConfig()))
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,duplicate:=range []bool{false,true}{
  if duplicate{
   if _,err:=pg.Exec(t.Context(),db,"INSERT INTO targets VALUES(3,$1)",key);err!=nil{t.Fatal(err)}
   for _,maxBatch:=range []int{1,100}{
    cfg:=dataloader.DefaultConfig();cfg.MaxBatch=maxBatch;app.SetLoaders(NewDefaultLoaders(app,cfg))
    scope,err:=dataloader.NewScope(t.Context());if err!=nil{t.Fatal(err)}
    value,err:=app.loaders.SourceTarget.Load(scope,key,[]*selection.Field{{Name:"id"}})
    if value!=nil||!errors.Is(err,dataloader.ErrNonUniqueRelation){t.Fatalf("batch=%d hid duplicate error: %v %v",maxBatch,value,err)}
    values,err:=app.loaders.SourceTargets.LoadAll(t.Context(),[]uuid.UUID{key},[]*selection.Field{{Name:"id"}})
    if err!=nil||len(values[key])!=2{t.Fatal("single cardinality check changed list relation",values,err)}
   }
  }
  for _,apiName:=range []string{"getSourceByKey","lookupSource"}{for _,binary:=range []bool{false,true}{
   response:=relationHTTP(t,router,apiName,key,binary)
   want:=200;if duplicate{want=500}
   if response.Code!=want{t.Fatalf("binary=%v duplicate=%v status=%d body=%s",binary,duplicate,response.Code,response.Body)}
   if !duplicate{
    if binary{var value Source;dec:=codec.NewDecoder(response.Body.Bytes());value.ReadLuxo(dec);if err:=dec.Finish();err!=nil||value.Target==nil||value.Target.Id!=1{t.Fatal("selected binary relation was not loaded",value,err)}}else if !bytes.Contains(response.Body.Bytes(),[]byte("\"id\":1")){t.Fatal("selected JSON relation was not loaded",response.Body)}
   }
   if duplicate&&(bytes.Contains(response.Body.Bytes(),[]byte("multiple rows"))||bytes.Contains(response.Body.Bytes(),[]byte("target"))){t.Fatal("internal error published partial relation or database details",response.Body)}
  }}
 }
}
func relationHTTP(t *testing.T,router *api.Router,apiName string,key uuid.UUID,binary bool)*httptest.ResponseRecorder{
 t.Helper();fields:=[]*selection.Field{{Name:"key"},{Name:"target",Children:[]*selection.Field{{Name:"id"}}}}
 body:=[]byte("{\"$api\":\""+apiName+"\",\"key\":\""+key.String()+"\",\"$select\":\""+selection.Format(fields)+"\"}")
 if binary{
  mask,err:=schema.SelectToFieldMask(fields,router.Schema.Models["Source"],router.Schema);if err!=nil{t.Fatal(err)}
  body,err=api.EncodeBinaryRequest(router.Schema.APIs[apiName].ID,mask,map[string]any{"key":key},router.Registry.ParamOrder(apiName));if err!=nil{t.Fatal(err)}
 }
 request:=httptest.NewRequest("POST","/luvia",strings.NewReader(string(body)))
 request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
 response:=httptest.NewRecorder();router.ServeHTTP(response,request);return response
}
`

func TestRelationRemoteKeyNullableMetadata(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		input := analyzePermissionFixture(t, "model Target @noTime { id: Int @id sourceId: Int"+map[bool]string{false: "", true: "?"}[nullable]+" }")
		for _, name := range []string{"sourceId", "missing"} {
			if got := relationRemoteKeyNullable(input, Relation{TargetName: "Target", RemoteKey: name}); got != (nullable && name == "sourceId") {
				t.Fatalf("key=%s nullable=%v got=%v", name, nullable, got)
			}
		}
		input.Types["Target"].Fields["sourceId"].Type = nil
		if relationRemoteKeyNullable(input, Relation{TargetName: "Target", RemoteKey: "sourceId"}) {
			t.Fatal("missing type is nullable")
		}
	}
}

func TestGeneratedRequiredKeyRelationCardinality(t *testing.T) {
	for _, keyType := range []string{"int64", "string", "uuid.UUID"} {
		t.Run(keyType, func(t *testing.T) {
			var source strings.Builder
			declarations := strings.ReplaceAll(nullableBatchDeclarations, "KEY_TYPE", keyType)
			source.WriteString(strings.Replace(declarations, "SourceId *"+keyType, "SourceId "+keyType, 1))
			source.WriteString("func single(app *App) []func(context.Context, []" + keyType + ", []*selection.Field) (map[" + keyType + "]*Target, error) { return []func(context.Context, []" + keyType + ", []*selection.Field) (map[" + keyType + "]*Target, error) {\n")
			defaultGenerator().generateBatchFunc(&source, "Source", Relation{TargetName: "Target", RemoteKey: "sourceId", KeyGoType: keyType}, false)
			source.WriteString("}}\n")
			runGeneratedProtocolPackage(t, source.String(), strings.ReplaceAll(requiredKeyCardinalityAssertions, "KEY_TYPE", keyType))
		})
	}
}

const requiredKeyCardinalityAssertions = `package generated
import("testing";"errors";"github.com/google/uuid";"github.com/light-speak/luxo/pkg/lux/dataloader")
var _ = uuid.Nil
func TestCardinality(t *testing.T){
 var key KEY_TYPE
 for count:=0;count<=2;count++{
  app:=&App{}
  for i:=0;i<count;i++{app.DB.rows=append(app.DB.rows,&Target{Id:i+1,SourceId:key})}
  got,err:=single(app)[0](t.Context(),[]KEY_TYPE{key},nil)
  if count==2{if !errors.Is(err,dataloader.ErrNonUniqueRelation)||got!=nil{t.Fatalf("duplicate relation returned success: %v %v",got,err)};continue}
  if err!=nil||len(got)!=count||(count==1&&got[key]!=app.DB.rows[0]){t.Fatalf("valid relation changed: count=%d got=%v err=%v",count,got,err)}
 }
}
`

func TestGeneratedImplicitCustomTargetKey(t *testing.T) {
	runGeneratedFixture(t, `model Target @noTime { key: String @id }
model Source @noTime { id: Int @id targetKey: String? target: Target? }
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/schema")
func TestImplicitCustomKeySchema(t *testing.T){
 registry:=schema.New();if err:=RegisterSchema(registry);err!=nil{t.Fatal(err)}
 for _,field:=range registry.Models["Source"].Fields{if field.Name=="target"{if field.LocalKey!="targetKey"||field.RemoteKey!="key"||field.RelationKind!=schema.RelationBelongsTo{t.Fatal(field)};return}}
 t.Fatal("target relation missing")
}
`, StableIDs{ModelFields: map[string]map[string]int{
		"Source": {"id": 1, "targetKey": 2, "target": 3}, "Target": {"key": 1},
	}})
}

func TestGeneratedImplicitInheritedTargetKey(t *testing.T) {
	runGeneratedFixture(t, `model Base @noTime { key: UUID @id }
model Target : Base @noTime { name: String }
model Source @noTime { id: Int @id targetKey: UUID? target: Target? }
`, "package generated\n", StableIDs{ModelFields: map[string]map[string]int{
		"Base": {"key": 1}, "Target": {"key": 1, "name": 2}, "Source": {"id": 1, "targetKey": 2, "target": 3},
	}})
}
