package codegen

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux"
	pgdialect "github.com/light-speak/luxo/pkg/lux/dialect/pg"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestMigrationTypedDefaults(t *testing.T) {
	for _, sample := range []struct{ field, want string }{
		{"value: Int = -9223372036854775808", "-9223372036854775808"},
		{"value: Int = 010", "10"},
		{"value: Float = -0.0", "'-0'::DOUBLE PRECISION"},
		{"value: Float = 3.14", "'3.14'::DOUBLE PRECISION"},
		{"value: Duration = -01d", "INTERVAL '-86400000000 microseconds'"},
		{"value: Duration = 01ms", "INTERVAL '1000 microseconds'"},
		{"value: String? = null", "NULL"},
		{"value: String = \"null\"", "'null'"},
		{"value: Boolean? = null", "NULL"},
		{"value: JSON = null", "'null'::JSONB"},
		{"value: JSON? = null", "NULL"},
		{"value: [Int]? = null", "NULL"},
		{"value: State = State.CLOSED", "'CLOSED'"},
		{"value: State = OPEN", "'OPEN'"},
	} {
		t.Run(sample.field, func(t *testing.T) {
			input := analyzePermissionFixture(t, "enum State { OPEN CLOSED }\nmodel Probe @noTime { "+sample.field+" }")
			state, err := ComputeState(input, map[string]bool{"State": true}, pgDialect())
			if err != nil {
				t.Fatal(err)
			}
			if got := state.Models["Probe"].Columns["value"].Default; got != sample.want {
				t.Fatalf("typed default = %q, want %q", got, sample.want)
			}
		})
	}
}

type defaultProbeDialect struct {
	pgdialect.Dialect
	values *[]lux.ColumnDefault
	err    error
}

func (d defaultProbeDialect) DefaultSQL(value lux.ColumnDefault) (string, error) {
	*d.values = append(*d.values, value)
	return "foreign-default", d.err
}

func TestMigrationDefaultDelegatesWithoutPublishingPartialState(t *testing.T) {
	input := analyzePermissionFixture(t, "model First @noTime { value: Int = 42 }\nmodel Second @noTime { value: Float = -0.0 }")
	var values []lux.ColumnDefault
	state, err := ComputeState(input, nil, defaultProbeDialect{values: &values})
	if err != nil || len(values) != 2 || values[0].Kind != lux.DefaultInt || values[0].Int != 42 ||
		values[1].Kind != lux.DefaultFloat || !math.Signbit(values[1].Float) || state.Models["Second"].Columns["value"].Default != "foreign-default" {
		t.Fatal("compiler bypassed the selected dialect", state, values, err)
	}
	want := errors.New("backend cannot store this default")
	state, err = ComputeState(input, nil, defaultProbeDialect{values: &values, err: want})
	if state != nil || !errors.Is(err, want) || !strings.Contains(err.Error(), "First.value") {
		t.Fatal("partial migration state or lost backend error", state, err)
	}
	if input.Files[0].Models[0].Fields[0].Default.(*ast.Literal).Value != "42" {
		t.Fatal("migration preparation modified the source AST")
	}
}

func TestMigrationRejectsInvalidDefaults(t *testing.T) {
	for _, expression := range []ast.Expr{
		&ast.Literal{Kind: token.Null, Value: "null"},
		&ast.Literal{Kind: token.Int, Value: "9223372036854775808"},
		&ast.Literal{Kind: token.Float, Value: "1e9999"},
		&ast.Literal{Kind: token.Duration, Value: "bad"},
		&ast.Literal{Kind: token.Duration, Value: "oopsd"},
		&ast.Literal{Kind: token.Duration, Value: "106752d"},
		&ast.Literal{Kind: token.Duration, Value: "-106752d"},
		&ast.Literal{Kind: token.Duration, Value: "9223372036854775808s"},
		&ast.Literal{Kind: token.LParen},
		&ast.UnaryExpr{Op: "!", Value: &ast.Literal{Kind: token.True}},
		&ast.UnaryExpr{Op: "-", Value: &ast.Ident{Name: "other"}},
		&ast.UnaryExpr{Op: "-", Value: &ast.Literal{Kind: token.String, Value: "42"}},
		&ast.CallExpr{Func: &ast.Ident{Name: "now"}},
	} {
		input := &semantic.Result{Files: []*ast.File{{Models: []*ast.ModelDecl{{Name: "Probe", Fields: []*ast.FieldDecl{{Name: "value", Type: &ast.TypeRef{Name: "Int"}, Default: expression}}}}}}}
		if state, err := ComputeState(input, nil, pgDialect()); err == nil || state != nil || !strings.Contains(err.Error(), "Probe.value") {
			t.Fatalf("invalid default became a partial migration: %T, %+v, %v", expression, state, err)
		}
	}
	for _, input := range []*semantic.Result{nil, analyzePermissionFixture(t, "model Probe { value: Int = null }")} {
		if state, err := ComputeState(input, nil, pgDialect()); state != nil || err == nil {
			t.Fatal("invalid analysis published migration state", state, err)
		}
	}
}

func TestMigrationSignedDefaultLiterals(t *testing.T) {
	for _, sample := range []struct {
		literal *ast.Literal
		want    lux.ColumnDefault
	}{
		{&ast.Literal{Kind: token.Int, Value: "010"}, lux.ColumnDefault{Kind: lux.DefaultInt, Int: 10}},
		{&ast.Literal{Kind: token.Float, Value: "1.5"}, lux.ColumnDefault{Kind: lux.DefaultFloat, Float: 1.5}},
		{&ast.Literal{Kind: token.Duration, Value: "1d"}, lux.ColumnDefault{Kind: lux.DefaultDuration, Int: int64(24 * time.Hour)}},
	} {
		value, err := literalColumnDefault(sample.literal, "+")
		if err != nil || value != sample.want {
			t.Fatal("signed default changed", value, sample.want, err)
		}
	}
}

var databaseDefaultSource = `enum State { OPEN CLOSED }
model NegativeInt @noTime @crud { id: Int @id value: Int = -42 }
model NegativeFloat @noTime { id: Int @id value: Float = -1.5 }
model OptionalDefaults @noTime { id: Int @id text: String? = null flag: Boolean? = null }
model DurationDefaults @noTime { id: Int @id value: Duration = 1s }
model FloatZero @noTime { id: Int @id value: Float = -0.0 }
model FloatTiny @noTime { id: Int @id value: Float = 0.` + strings.Repeat("0", 324) + `1 }
model JSONDefault @noTime { id: Int @id value: JSON = null }
model DurationBounds @noTime { id: Int @id negative: Duration = -01d minimum: Duration = -9223372036854ms maximum: Duration = 9223372036854ms tiny: Duration = 01ms }
model NumericLimits @noTime { id: Int @id minimum: Int = -9223372036854775808 maximum: Float = ` + strconv.FormatFloat(math.MaxFloat64, 'f', -1, 64) + `.0 subnormal: Float = 0.` + strings.Repeat("0", 323) + `5 }
model NullableDefaults @noTime { id: Int @id number: Int? = null real: Float? = null elapsed: Duration? = null at: DateTime? = null key: UUID? = null amount: Decimal? = null payload: JSON? = null blob: Bytes? = null items: [Int]? = null state: State? = null }
model PositiveDefaults @noTime { id: Int @id count: Int = 010 real: Float = 0.1 text: String = "null" quoted: String = "O'Reilly" flag: Boolean = true state: State = OPEN }
`

func TestGeneratedDatabaseDefaults(t *testing.T) {
	input := analyzePermissionFixture(t, databaseDefaultSource)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	state, err := ComputeState(input, map[string]bool{"State": true}, pgDialect())
	if err != nil {
		t.Fatal(err)
	}
	var migrations strings.Builder
	migrations.WriteString("\nvar defaultMigrations = map[string]string{\n")
	ids := StableIDs{ModelFields: map[string]map[string]int{}}
	for _, model := range input.Files[0].Models {
		part := &SchemaState{Models: map[string]*ModelState{model.Name: state.Models[model.Name]}}
		up, _, migrationSQLFailure1 := GenerateMigrationSQL(mustMigrationDiff(t, &SchemaState{Models: map[string]*ModelState{}}, part, nil), part, pgDialect())
		if migrationSQLFailure1 != nil {
			t.Fatal(migrationSQLFailure1)
		}
		migrations.WriteString(strconv.Quote(model.Name) + ": " + strconv.Quote(up) + ",\n")
		fields := map[string]int{}
		for i, field := range model.Fields {
			fields[field.Name] = i + 1
		}
		ids.ModelFields[model.Name] = fields
	}
	migrations.WriteString("}\n")
	next, err := ComputeState(analyzePermissionFixture(t, strings.Replace(databaseDefaultSource, "value: Int = -42", "value: Int = -84", 1)), map[string]bool{"State": true}, pgDialect())
	if err != nil {
		t.Fatal(err)
	}
	up, down, migrationSQLFailure2 := GenerateMigrationSQL(mustMigrationDiff(t, state, next, nil), next, pgDialect())
	if migrationSQLFailure2 != nil {
		t.Fatal(migrationSQLFailure2)
	}
	migrations.WriteString("\nconst defaultUp = " + strconv.Quote(up) + "\nconst defaultDown = " + strconv.Quote(down))
	runGeneratedFixture(t, databaseDefaultSource, databaseDefaultAssertions+migrations.String(), ids)
}

const databaseDefaultAssertions = `package generated
import (
 "bytes"
 "math"
 "net/http/httptest"
 "testing"
 "time"
 "github.com/light-speak/luxo/internal/testutil"
 "github.com/light-speak/luxo/pkg/lux"
 "github.com/light-speak/luxo/pkg/lux/api"
 "github.com/light-speak/luxo/pkg/lux/codec"
 "github.com/light-speak/luxo/pkg/lux/pg"
 "github.com/testcontainers/testcontainers-go"
 "github.com/testcontainers/testcontainers-go/modules/postgres"
)
func TestDatabaseDefaults(t *testing.T) {
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies());testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 app:=NewFromDB(db)
 migrate:=func(t *testing.T,name string){t.Helper();if _,err:=pg.Exec(t.Context(),db,defaultMigrations[name]);err!=nil{t.Fatal("generated default SQL failed",err,defaultMigrations[name])}}
 t.Run("NegativeInt",func(t *testing.T){migrate(t,"NegativeInt");got,err:=app.NegativeInt.Create().SetId(1).Exec(t.Context());if err!=nil||got==nil||got.Value!=-42{t.Fatal("negative Int default lost",got,err)}})
 t.Run("NegativeFloat",func(t *testing.T){migrate(t,"NegativeFloat");got,err:=app.NegativeFloat.Create().SetId(1).Exec(t.Context());if err!=nil||got==nil||got.Value!=-1.5{t.Fatal("negative Float default lost",got,err)}})
 t.Run("OptionalDefaults",func(t *testing.T){migrate(t,"OptionalDefaults");got,err:=app.OptionalDefaults.Create().SetId(1).Exec(t.Context());if err!=nil||got==nil||got.Text!=nil||got.Flag!=nil{t.Fatal("nullable default changed",got,err)}})
 t.Run("DurationDefaults",func(t *testing.T){migrate(t,"DurationDefaults");got,err:=app.DurationDefaults.Create().SetId(1).Exec(t.Context());if err!=nil||got==nil||got.Value!=time.Second{t.Fatal("Duration default lost",got,err)}})
 t.Run("FloatZero",func(t *testing.T){migrate(t,"FloatZero");got,err:=app.FloatZero.Create().SetId(1).Exec(t.Context());if err!=nil||got==nil||got.Value!=0||!math.Signbit(got.Value){t.Fatal("negative Float zero lost",got,err)}})
 t.Run("FloatTiny",func(t *testing.T){migrate(t,"FloatTiny");got,err:=app.FloatTiny.Create().SetId(1).Exec(t.Context());if err!=nil||got==nil||got.Value!=0{t.Fatal("Float64 underflow default diverged",got,err)}})
 t.Run("JSONDefault",func(t *testing.T){migrate(t,"JSONDefault");got,err:=app.JSONDefault.Create().SetId(1).Exec(t.Context());if err!=nil||got==nil||string(got.Value)!="null"{t.Fatal("JSON null became SQL NULL",got,err)}})
 t.Run("DurationBounds",func(t *testing.T){migrate(t,"DurationBounds");got,err:=app.DurationBounds.Create().SetId(1).Exec(t.Context());if err!=nil||got==nil||got.Negative!=-24*time.Hour||got.Minimum!=-9223372036854000000||got.Maximum!=9223372036854000000||got.Tiny!=time.Millisecond{t.Fatal("Duration default boundary changed",got,err)}})
 t.Run("NumericLimits",func(t *testing.T){migrate(t,"NumericLimits");got,err:=app.NumericLimits.Create().SetId(1).Exec(t.Context());if err!=nil||got==nil||got.Minimum!=math.MinInt64||got.Maximum!=math.MaxFloat64||got.Subnormal!=math.SmallestNonzeroFloat64{t.Fatal("numeric default boundary changed",got,err)}})
 t.Run("NullableDefaults",func(t *testing.T){migrate(t,"NullableDefaults");got,err:=app.NullableDefaults.Create().SetId(1).Exec(t.Context());if err!=nil||got==nil||got.Number!=nil||got.Real!=nil||got.Elapsed!=nil||got.At!=nil||got.Key!=nil||got.Amount!=nil||got.Payload!=nil||got.Blob!=nil||got.Items!=nil||got.State!=nil{t.Fatal("nullable scalar/list default changed",got,err)}})
 t.Run("PositiveDefaults",func(t *testing.T){migrate(t,"PositiveDefaults");got,err:=app.PositiveDefaults.Create().SetId(1).Exec(t.Context());if err!=nil||got==nil||got.Count!=10||got.Real!=0.1||got.Text!="null"||got.Quoted!="O'Reilly"||!got.Flag||got.State!=StateOPEN{t.Fatal("valid default control changed",got,err)}})
 t.Run("ExplicitValues",func(t *testing.T){text,flag:="null",false;got,err:=app.OptionalDefaults.Create().SetId(2).SetText(&text).SetFlag(&flag).Exec(t.Context());if err!=nil||got==nil||got.Text==nil||*got.Text!="null"||got.Flag==nil||*got.Flag{t.Fatal("explicit values were defaulted",got,err)};number,err:=app.NegativeInt.Create().SetId(2).SetValue(0).Exec(t.Context());if err!=nil||number==nil||number.Value!=0{t.Fatal("explicit zero was defaulted",number,err)}})
 t.Run("GeneratedHTTP",func(t *testing.T){testDefaultHTTP(t,app)})
 t.Run("AlterDefault",func(t *testing.T){if _,err:=pg.Exec(t.Context(),db,defaultUp);err!=nil{t.Fatal(err)};got,err:=app.NegativeInt.Create().SetId(9).Exec(t.Context());if err!=nil||got==nil||got.Value!=-84{t.Fatal("altered default lost",got,err)};if _,err:=pg.Exec(t.Context(),db,defaultDown);err!=nil{t.Fatal(err)};got,err=app.NegativeInt.Create().SetId(10).Exec(t.Context());if err!=nil||got==nil||got.Value!=-42{t.Fatal("restored default lost",got,err)}})
 t.Run("SessionStringEscapes",func(t *testing.T){testDefaultStringEscapes(t,connection)})
}
func testDefaultStringEscapes(t *testing.T,connection string){
 for _,setting:=range []string{"on","off"}{
  db,err:=pg.NewDB(t.Context(),connection+"&standard_conforming_strings="+setting);if err!=nil{t.Fatal(err)}
  sql,err:=(pg.Dialect{}).DefaultSQL(lux.ColumnDefault{Kind:lux.DefaultString,Text:"O'Reilly\\folder"});if err!=nil{db.Close();t.Fatal(err)}
  got,err:=pg.QueryScalar[string](t.Context(),db,"SELECT "+sql);db.Close();if err!=nil||got!="O'Reilly\\folder"{t.Fatal("session changed a typed string default",setting,got,err)}
 }
}
func testDefaultHTTP(t *testing.T,app *App){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,binary:=range []bool{false,true}{
  id:=int64(3);body:=[]byte("{\"$api\":\"createNegativeInt\",\"$select\":\"id,value\",\"id\":3}")
  if binary{id=4;mask,err:=codec.AppendSelectionMask(nil,[]byte{3},nil);if err!=nil{t.Fatal(err)};body,err=api.EncodeBinaryRequest(router.Schema.APIs["createNegativeInt"].ID,mask,map[string]any{"id":id},router.Registry.ParamOrder("createNegativeInt"));if err!=nil{t.Fatal(err)}}
  request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")};response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatal("generated CRUD omitted default failed",binary,response.Code,response.Body.String())}
  got,err:=app.NegativeInt.Find(t.Context(),id);if err!=nil||got==nil||got.Value!=-42{t.Fatal("HTTP default diverged from DB builder",binary,got,err)}
 }
}
`
