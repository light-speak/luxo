package codegen

import (
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

const preparedDefaultSource = `
fn checked(value: String): String { if value == "bad" { throw error.NotFound } return value + "!" }
model Prepared @noTime @crud { id: Int @id value: String = "raw" @beforeSave { it + "!" } }
model Hashed @noTime @crud { id: Int @id password: String = "secret" @hash @hidden }
model Rejected @noTime @crud { id: Int @id value: String = "bad" @beforeSave { checked(it) } }
enum Stage { OPEN CLOSED }
model InternalDefault @noTime { id: Int @id secret: String = "raw" @internal @beforeSave { it + "!" } }
model NullableDefault @noTime { id: Int @id value: String? = "raw" @beforeSave { it + "!" } empty: String? = null @beforeSave { checked(it) } phase: Stage = OPEN @beforeSave { it } }
api defaultPrepared(id: Int): Prepared { return Prepared.create(id: id) }
api defaultHashed(id: Int): Hashed { return Hashed.create(id: id) }
`

func TestGeneratedPreparedCreateDefaults(t *testing.T) {
	source := preparedDefaultSource + preparedDefaultEdgeSource()
	input := analyzePermissionFixture(t, source)
	state, err := ComputeState(input, map[string]bool{"Stage": true}, pgDialect())
	if err != nil {
		t.Fatal(err)
	}
	up, _, migrationSQLFailure1 := GenerateMigrationSQL(mustMigrationDiff(t, &SchemaState{Models: map[string]*ModelState{}}, state, nil), state, pgDialect())
	if migrationSQLFailure1 != nil {
		t.Fatal(migrationSQLFailure1)
	}
	ids := StableIDs{ModelFields: map[string]map[string]int{}}
	for _, model := range input.Files[0].Models {
		fields := map[string]int{}
		for index, field := range model.Fields {
			fields[field.Name] = index + 1
			if model.Name == "WideDefault" && index > 0 {
				fields[field.Name] = index*900 + 1
			}
		}
		ids.ModelFields[model.Name] = fields
	}
	runGeneratedFixture(t, source, preparedDefaultAssertions+"\nconst preparedMigrationSQL = "+strconv.Quote(up), ids)
}

func preparedDefaultEdgeSource() string {
	var source strings.Builder
	source.WriteString("\nmodel WideDefault @noTime { id: Int @id\n")
	for index := range 65 {
		source.WriteString("field" + strconv.Itoa(index) + ": Int = 0 @beforeSave { it + 1 }\n")
	}
	source.WriteString("}\nmodel LongHashDefault @noTime { id: Int @id password: String = \"" + strings.Repeat("x", 73) + "\" @hash }\n")
	source.WriteString(`model NullableHashDefault @noTime { id: Int @id password: String? = " raw " @hash @beforeSave { it.trim() } absent: String? = null @hash }`)
	return source.String()
}

func TestPreparedDefaultsDropRawDatabaseFallback(t *testing.T) {
	input := analyzePermissionFixture(t, preparedDefaultSource)
	state, err := ComputeState(input, map[string]bool{"Stage": true}, pgDialect())
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range state.Models {
		for field, column := range model.Columns {
			if column.Default != "" {
				t.Fatal("unprocessed default retained in database", model.Table, field, column.Default)
			}
		}
	}
	previous := &SchemaState{Models: map[string]*ModelState{"Hashed": {Table: "hasheds", Columns: map[string]*ColumnState{
		"id": {Type: "BIGINT", PK: true}, "password": {Type: "TEXT", Default: "'secret'"},
	}}}}
	next := &SchemaState{Models: map[string]*ModelState{"Hashed": state.Models["Hashed"]}}
	up, _, migrationSQLFailure2 := GenerateMigrationSQL(mustMigrationDiff(t, previous, next, nil), next, pgDialect())
	if migrationSQLFailure2 != nil {
		t.Fatal(migrationSQLFailure2)
	}
	if !strings.Contains(up, "DROP DEFAULT") {
		t.Fatal("legacy plaintext default was not removed by migration", up)
	}
}

func TestPreparedDefaultMetadataFiltersWithoutChangingFields(t *testing.T) {
	literal := &ast.Literal{Kind: token.String, Value: "raw"}
	hook := &ast.Directive{Name: "beforeSave"}
	plain := &ast.FieldDecl{Name: "plain", Type: &ast.TypeRef{Name: "String"}, Default: literal}
	prepared := &ast.FieldDecl{Name: "prepared", Type: plain.Type, Default: literal, Directives: []*ast.Directive{hook}}
	internal := &ast.FieldDecl{Name: "secret", Type: plain.Type, Default: literal, Directives: []*ast.Directive{hook, {Name: "internal"}}}
	auto := &ast.FieldDecl{Name: "auto", Type: plain.Type, Default: literal, Directives: []*ast.Directive{hook, {Name: "auto"}}}
	fields := []*ast.FieldDecl{plain, prepared, internal, auto, {Name: "absent", Type: plain.Type, Directives: prepared.Directives}, {Name: "computed", Type: plain.Type, Default: literal, Computed: &ast.ComputedField{}}}
	got := preparedCreateDefaults(fields)
	if len(got) != 2 || got[0] != prepared || got[1] != internal || len(fields) != 6 || prepared.Default != literal {
		t.Fatal("default metadata changed declarations or lost internal fields", got)
	}
	if preparedCreateDefaults([]*ast.FieldDecl{plain}) != nil {
		t.Fatal("ordinary defaults acquired runtime preparation")
	}
}

func TestPreparedDefaultsKeepInvalidASTRejection(t *testing.T) {
	field := &ast.FieldDecl{Name: "value", Type: &ast.TypeRef{Name: "String"},
		Default: &ast.CallExpr{Func: &ast.Ident{Name: "runtimeValue"}}, Directives: []*ast.Directive{{Name: "beforeSave"}}}
	input := &semantic.Result{Files: []*ast.File{{Models: []*ast.ModelDecl{{Name: "Probe", Fields: []*ast.FieldDecl{field}}}}}}
	if state, err := ComputeState(input, nil, pgDialect()); state != nil || err == nil {
		t.Fatal("prepared field bypassed constant validation", state, err)
	}
}

func TestGeneratedPreparedDefaultsWithSortingHelper(t *testing.T) {
	runGeneratedFixture(t, `
fn order(value: Int): Int {
 val values = [value, 0].sortBy { it }
 if values.size() == 2 { throw error.NotFound }
 return value
}
model SortedDefaults @noTime { id: Int @id a: Int = 2 @beforeSave { order(it) } b: Int = 3 @beforeSave { order(it) } }
`, `package generated
import("errors";"testing";luxerrors "github.com/light-speak/luxo/pkg/lux/errors")
func TestSortingDefaultImports(t *testing.T){if _,err:=(&SortedDefaultsCreateBuilder{}).Exec(t.Context());!errors.Is(err,luxerrors.NotFound){t.Fatal("default sort helper reached database",err)}}
`, StableIDs{ModelFields: map[string]map[string]int{"SortedDefaults": {"id": 1, "a": 2, "b": 3}}})
}

const preparedDefaultAssertions = `package generated
import(
 "bytes"
 "errors"
 "net/http/httptest"
 "strconv"
 "strings"
 "testing"
 "github.com/light-speak/luxo/internal/testutil"
 "github.com/light-speak/luxo/pkg/lux/api"
 "github.com/light-speak/luxo/pkg/lux/codec"
 luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
 "github.com/light-speak/luxo/pkg/lux/pg"
 "github.com/testcontainers/testcontainers-go"
 "github.com/testcontainers/testcontainers-go/modules/postgres"
)
func TestPreparedDefaults(t *testing.T){
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies());testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,preparedMigrationSQL);err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 t.Run("GoBuilder",func(t *testing.T){value,err:=app.Prepared.Create().SetId(1).Exec(t.Context());if err!=nil||value==nil||value.Value!="raw!"{t.Error("default bypassed beforeSave",value,err)};hashed,err:=app.Hashed.Create().SetId(1).Exec(t.Context());if err!=nil||hashed==nil||hashed.Password=="secret"||!hashed.VerifyPassword("secret"){t.Error("hash default stored plaintext",hashed,err)}})
 t.Run("Failure",func(t *testing.T){got,err:=app.Rejected.Create().SetId(1).Exec(t.Context());if got!=nil||!errors.Is(err,luxerrors.NotFound){t.Error("default bypassed failure",got,err)};count,err:=pg.QueryScalar[int64](t.Context(),db,"SELECT count(*) FROM rejecteds");if err!=nil||count!=0{t.Error("failed default wrote a row",count,err)}})
 t.Run("Internal",func(t *testing.T){got,err:=app.InternalDefault.Create().SetId(1).Exec(t.Context());if err!=nil||got==nil||got.Secret!="raw!"{t.Fatal("internal default bypassed preparation",got,err)}})
 t.Run("Nullable",func(t *testing.T){got,err:=app.NullableDefault.Create().SetId(1).Exec(t.Context());if err!=nil||got==nil||got.Value==nil||*got.Value!="raw!"||got.Empty!=nil||got.Phase!=StageOPEN{t.Fatal("nullable/enum default changed",got,err)};got,err=app.NullableDefault.Create().SetId(2).SetValue(nil).Exec(t.Context());if err!=nil||got==nil||got.Value!=nil{t.Fatal("explicit null was replaced",got,err)}})
 t.Run("Reuse",func(t *testing.T){builder:=app.Prepared.Create().SetId(100);first,err:=builder.Exec(t.Context());if err!=nil||first.Value!="raw!"||len(builder.Cols)!=1{t.Fatal("default mutated builder",first,builder.Cols,err)};second,err:=builder.SetId(101).Exec(t.Context());if err!=nil||second.Value!="raw!"||len(builder.Cols)!=1{t.Fatal("default repeated preparation",second,builder.Cols,err)};third,err:=builder.SetId(102).SetValue("provided").Exec(t.Context());if err!=nil||third.Value!="provided!"||len(builder.Cols)!=2{t.Fatal("provided value was prepared twice",third,builder.Cols,err)}})
 t.Run("RecoverDefaultFailure",func(t *testing.T){builder:=app.Rejected.Create().SetId(7);if _,err:=builder.Exec(t.Context());!errors.Is(err,luxerrors.NotFound)||len(builder.Cols)!=1{t.Fatal("failed default changed builder",builder.Cols,err)};got,err:=builder.SetValue("good").Exec(t.Context());if err!=nil||got==nil||got.Value!="good!"{t.Fatal("explicit replacement failed",got,err)}})
 t.Run("TrustedRawSet",func(t *testing.T){builder:=app.Prepared.Create().SetId(103);builder.Set("value","trusted");got,err:=builder.Exec(t.Context());if err!=nil||got==nil||got.Value!="trusted"{t.Fatal("raw bound column was duplicated or transformed",got,err)}})
 t.Run("Wide",func(t *testing.T){got,err:=app.WideDefault.Create().SetId(1).SetField64(10).Exec(t.Context());if err!=nil||got==nil||got.Field0!=1||got.Field63!=1||got.Field64!=11{t.Fatal("ordinal bitmap crossed a wire-ID/64-bit boundary",got,err)}})
 t.Run("LongHashFailure",func(t *testing.T){if _,err:=(&LongHashDefaultCreateBuilder{}).Exec(t.Context());err==nil{t.Fatal("oversized default reached missing database")}})
 t.Run("NullableHash",func(t *testing.T){got,err:=app.NullableHashDefault.Create().SetId(1).Exec(t.Context());if err!=nil||got==nil||got.Password==nil||!got.VerifyPassword("raw")||got.Absent!=nil{t.Fatal("nullable default preparation/hash order changed",got,err)}})
 t.Run("NoRawFallback",func(t *testing.T){if _,err:=pg.Exec(t.Context(),db,"INSERT INTO hasheds(id) VALUES(100)");err==nil||!strings.Contains(err.Error(),"23502"){t.Fatal("raw SQL retained an unhashed default",err)}})
 t.Run("FreshHash",func(t *testing.T){first,err:=app.Hashed.Find(t.Context(),1);if err!=nil||first==nil{t.Fatal(first,err)};second,err:=app.Hashed.Create().SetId(50).Exec(t.Context());if err!=nil||second==nil||first.Password==second.Password{t.Fatal("default hash reused its salt",err)}})
 for _,binary:=range []bool{false,true}{
  id:=int64(2);if binary{id=20}
  for _,name:=range []string{"createPrepared","createHashed","defaultPrepared","defaultHashed"}{
   id++
   t.Run(name+"/"+strconv.FormatBool(binary),func(t *testing.T){
    body:=[]byte("{\"$api\":\""+name+"\",\"$select\":\"id\",\"id\":"+strconv.FormatInt(id,10)+"}")
    if binary{mask,err:=codec.AppendSelectionMask(nil,[]byte{1},nil);if err!=nil{t.Fatal(err)};body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,mask,map[string]any{"id":id},router.Registry.ParamOrder(name));if err!=nil{t.Fatal(err)}}
    request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")};response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatal(name,binary,response.Code,response.Body.String())}
    if name=="createPrepared"||name=="defaultPrepared"{got,err:=app.Prepared.Find(t.Context(),id);if err!=nil||got==nil||got.Value!="raw!"{t.Error("request default skipped preparation",name,binary,got,err)}}else{got,err:=app.Hashed.Find(t.Context(),id);if err!=nil||got==nil||!got.VerifyPassword("secret"){t.Error("request default stored plaintext",name,binary,got,err)}}
   })
  }
 }
}
`
