package codegen

import (
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/internal/inferred"
	"github.com/light-speak/luxo/pkg/ast"
	pgdialect "github.com/light-speak/luxo/pkg/lux/dialect/pg"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestSQLIdentifierNameValidationGuards(t *testing.T) {
	for _, name := range []string{strings.Repeat("A", 63), "Probe"} {
		ext := &ast.ExtendDecl{Name: name}
		if name == "Probe" {
			ext.Fields = []*ast.FieldDecl{{Name: strings.Repeat("a", 64), Type: &ast.TypeRef{Name: "Int"}}}
		}
		input := &semantic.Result{Files: []*ast.File{{Extends: []*ast.ExtendDecl{ext}}}}
		if err := validateDatabaseIdentifiers(input, nil, pgdialect.Dialect{}); err == nil {
			t.Fatal("unrepresentable external database name accepted", name)
		}
	}
	model := &ast.ModelDecl{Name: "Probe", Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}},
		Directives: []*ast.Directive{{Name: "noTime"}, {Name: "index", Args: []*ast.NamedArg{{Name: "fields",
			Value: &ast.ListExpr{Items: []ast.Expr{&ast.Ident{Name: "missing"}}}}}}}}
	input := &semantic.Result{Files: []*ast.File{{Models: []*ast.ModelDecl{model}}}}
	if err := validateDatabaseIdentifiers(input, nil, pgdialect.Dialect{}); err == nil || !strings.Contains(err.Error(), "non-stored column") {
		t.Fatal("malformed metadata index did not fail clearly", err)
	}
	if err := validateDatabaseIdentifiers(input, nil, nil); err == nil || !strings.Contains(err.Error(), "explicit dialect") {
		t.Fatal("missing migration dialect accepted", err)
	}
	if err := (&sqlNameValidation{}).validateColumns("Probe", model.Fields, nil); err == nil {
		t.Fatal("missing column-validation dialect accepted")
	}
}

func TestSQLIdentifierNamesRejectLossyMapping(t *testing.T) {
	for _, sample := range []struct{ name, source string }{
		{"columns", `model Probe @noTime { id: Int @id userId: Int userID: Int }`},
		{"tables", `model APIUser @noTime { id: Int @id } model ApiUser @noTime { id: Int @id }`},
		{"managed-column", `model Probe { id: Int @id created_at: Int }`},
		{"long-column", `model Probe @noTime { id: Int @id ` + strings.Repeat("a", 64) + `: Int }`},
		{"long-unicode-column", `model Probe @noTime { id: Int @id ` + strings.Repeat("界", 22) + `: Int }`},
		{"long-table", `model ` + strings.Repeat("A", 63) + ` @noTime { id: Int @id }`},
		{"index-table", `model Probe @noTime { id: Int @id ids: Int @index } model IdxProbesId @noTime { id: Int @id }`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			input := analyzePermissionFixture(t, sample.source)
			if len(input.Errors) != 0 {
				t.Fatal("fixture failed before database-name validation", input.Errors)
			}
			ids := StableIDs{ModelFields: map[string]map[string]int{}}
			for _, file := range inferred.ExpandedFiles(input.Files) {
				for _, model := range file.Models {
					ids.ModelFields[model.Name] = sqlNameFixtureIDs(model.Fields)
				}
			}
			generated, err := Generate(input, "generated", GeneratorConfig{IDs: ids})
			if err == nil || generated != nil {
				t.Error("formal generation accepted lossy database names", err)
			}
			if schema, err := BuildSchemaJSON(input, nil, GeneratorConfig{IDs: ids}); err == nil || schema != nil {
				t.Error("SDK schema export accepted lossy database names", err)
			}
			if state, err := ComputeState(input, nil, pgdialect.Dialect{}); err == nil || state != nil {
				t.Error("migration accepted lossy database names", err)
			}
		})
	}
}

func TestSQLUniqueConstraintAndIndexShareNamespace(t *testing.T) {
	for _, sample := range []struct {
		name, source string
		collision    bool
	}{
		{"index-first", `model Probe @noTime { id: Int @id valueKey: Int @index }
model IdxProbe @noTime { id: Int @id value: Int @unique }`, true},
		{"unique-first", `model IdxProbe @noTime { id: Int @id value: Int @unique }
model Probe @noTime { id: Int @id valueKey: Int @index }`, true},
		{"separate-names", `model Probe @noTime { id: Int @id valueKey: Int @index }
model IdxProbe @noTime { id: Int @id other: Int @unique }`, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			input := analyzePermissionFixture(t, sample.source)
			if len(input.Errors) != 0 {
				t.Fatal(input.Errors)
			}
			ids := StableIDs{ModelFields: map[string]map[string]int{}}
			for _, model := range input.Files[0].Models {
				ids.ModelFields[model.Name] = sqlNameFixtureIDs(model.Fields)
			}
			config := GeneratorConfig{IDs: ids}
			generated, generateErr := Generate(input, "generated", config)
			schema, schemaErr := BuildSchemaJSON(input, nil, config)
			state, stateErr := ComputeState(input, nil, pgdialect.Dialect{})
			for _, err := range []error{generateErr, schemaErr, stateErr} {
				if sample.collision {
					if err == nil || !strings.Contains(err.Error(), `"idx_probes_value_key"`) || !strings.Contains(err.Error(), "conflicts with") {
						t.Fatalf("missing database namespace conflict: %v", err)
					}
				} else if err != nil {
					t.Fatal("distinct names rejected", err)
				}
			}
			if sample.collision && (generated != nil || schema != nil || state != nil) {
				t.Fatal("namespace conflict returned partial output")
			}
		})
	}
}

func TestSQLIdentifierNamesPreserveValidBoundaries(t *testing.T) {
	for _, sample := range []struct{ name, model, field string }{
		{"column-63-bytes", "Probe", strings.Repeat("a", 63)},
		{"unicode-63-bytes", "Probe", strings.Repeat("界", 21)},
		{"table-63-bytes", strings.Repeat("A", 62), "value"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			input := analyzePermissionFixture(t, "model "+sample.model+" @noTime { id: Int @id "+sample.field+": Int }")
			if len(input.Errors) != 0 {
				t.Fatal(input.Errors)
			}
			config := GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{sample.model: {"id": 1, sample.field: 2}}}}
			if _, err := Generate(input, "generated", config); err != nil {
				t.Fatal("valid boundary rejected by generator", err)
			}
			if _, err := BuildSchemaJSON(input, nil, config); err != nil {
				t.Fatal("valid boundary rejected by schema export", err)
			}
			state, err := ComputeState(input, nil, pgdialect.Dialect{})
			if err != nil || len(state.Models[sample.model].Columns) != 2 {
				t.Fatal("valid boundary rejected by migration", state, err)
			}
		})
	}
}

func TestSQLIdentifierNamesDoNotRestrictTypeWireNames(t *testing.T) {
	field := strings.Repeat("a", 64)
	input := analyzePermissionFixture(t, "type Payload { "+field+": Int } api echo(value: Payload): Payload { return value }")
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	config := GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"echo": 1},
		APIParams: map[string]map[string]int{"echo": {"value": 1}}, ModelFields: map[string]map[string]int{"Payload": {field: 1}}}}
	if _, err := Generate(input, "generated", config); err != nil {
		t.Fatal("database limit leaked into type field", err)
	}
	if _, err := BuildSchemaJSON(input, nil, config); err != nil {
		t.Fatal("database limit leaked into SDK schema", err)
	}
}

func TestSQLGeneratedNamesAllowLongValidTableAndColumn(t *testing.T) {
	model, field := strings.Repeat("A", 40), strings.Repeat("b", 32)
	input := analyzePermissionFixture(t, "model "+model+" @noTime { id: Int @id "+field+": String @unique @index }")
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	config := GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{model: {"id": 1, field: 2}}}}
	if _, err := Generate(input, "generated", config); err != nil {
		t.Fatal("generated object name restricted valid model fields", err)
	}
	if _, err := BuildSchemaJSON(input, nil, config); err != nil {
		t.Fatal(err)
	}
	state, err := ComputeState(input, nil, pgDialect())
	if err != nil {
		t.Fatal(err)
	}
	up, _, err := GenerateMigrationSQL(mustMigrationDiff(t, &SchemaState{Models: map[string]*ModelState{}}, state, nil), state, pgDialect())
	if err != nil {
		t.Fatal(err)
	}
	stored := state.Models[model]
	names := []string{stored.Indexes[0].Name, pgDialect().UniqueConstraintName(stored.Table, field)}
	for _, name := range names {
		if err := pgDialect().ValidateIdentifier(name); err != nil || !strings.Contains(up, pgDialect().QuoteIdentifier(name)) {
			t.Fatal("migration did not use the validated generated name", name, up, err)
		}
	}
}

func sqlNameFixtureIDs(fields []*ast.FieldDecl) map[string]int {
	ids := make(map[string]int, len(fields))
	for i, field := range fields {
		ids[field.Name] = i + 1
	}
	return ids
}

func TestSQLIdentifierContext(t *testing.T) {
	g := defaultGenerator()
	for _, sample := range []struct{ got, want string }{
		{g.sqlIdentifier(`a"b`), `"a""b"`},
		{g.sqlColumn("createdAt"), `"created_at"`},
		{g.sqlTable("Value"), `"values"`},
		{g.sqlAggregateColumn(""), ""},
		{g.sqlAggregateColumn("order"), `"order"`},
	} {
		if sample.got != sample.want {
			t.Errorf("SQL context got %q, want %q", sample.got, sample.want)
		}
	}
}

func TestSQLIdentifierMigrationDefaults(t *testing.T) {
	for _, sample := range []struct{ before, after string }{
		{"", "42"}, {"42", ""}, {"42", "84"},
	} {
		t.Run(sample.before+"_to_"+sample.after, func(t *testing.T) {
			ops := []DiffOp{{Kind: AlterColumn, Table: `a"b`, Column: "order",
				OldColumn: &ColumnState{Type: "BIGINT", Default: sample.before},
				NewColumn: &ColumnState{Type: "BIGINT", Default: sample.after}}}
			up, down, migrationSQLFailure1 := GenerateMigrationSQL(ops, nil, pgdialect.Dialect{})
			if migrationSQLFailure1 != nil {
				t.Fatal(migrationSQLFailure1)
			}
			for _, sql := range []string{up, down} {
				if !strings.HasPrefix(sql, `ALTER TABLE "a""b" ALTER COLUMN "order" `) {
					t.Fatal("default migration lost identifier quoting", sql)
				}
			}
		})
	}
}

func TestGeneratedSQLReservedIdentifiers(t *testing.T) {
	const source = `model Probe @noTime { id: Int @id order: Int @filterable @sortable value: Value? @by(id, order) }
model Value @noTime { id: Int @id amount: Int probes: [Probe] @by(order, id) val probeCount: Int get @count(probes) val totalOrder: Int get @sum(probes.order) }
type OrderSummary { order: Int count: Int }
api byOrder(minimum: Int): [Probe] { return Probe.where(it.order >= minimum).orderBy(order.desc).all() }
api listProbesByOrderOrId(order: Int, id: Int): [Probe]
api listProbesByIdInOrOrder(ids: [Int], order: Int): [Probe]
api listProbesByIdNotInOrOrder(ids: [Int], order: Int): [Probe]
api orderGroups(): [OrderSummary] { return Probe.groupBy { it.order }.select { OrderSummary { order: it.key, count: it.count() } } }
api relatedValue(): Value { return Value.find(id: 42) }
api relatedProbe(): Probe { return Probe.find(id: 1) }
api loadProbes(order: Int): [Probe] { return Probe.load(order: order) }
`
	input := analyzePermissionFixture(t, source)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	state, err := ComputeState(input, nil, pgdialect.Dialect{})
	if err != nil {
		t.Fatal(err)
	}
	up, _, migrationSQLFailure2 := GenerateMigrationSQL(mustMigrationDiff(t, &SchemaState{Models: map[string]*ModelState{}}, state, nil), state, pgdialect.Dialect{})
	if migrationSQLFailure2 != nil {
		t.Fatal(migrationSQLFailure2)
	}
	defaultUp, defaultDown, migrationSQLFailure3 := GenerateMigrationSQL([]DiffOp{{Kind: AlterColumn, Table: "probes", Column: "order",
		OldColumn: &ColumnState{Type: "BIGINT"}, NewColumn: &ColumnState{Type: "BIGINT", Default: "11"}}}, nil, pgdialect.Dialect{})
	if migrationSQLFailure3 != nil {
		t.Fatal(migrationSQLFailure3)
	}
	assertions := reservedIdentifierAssertions + "\nconst reservedMigrationSQL = " + strconv.Quote(up) +
		"\nconst reservedDefaultUp = " + strconv.Quote(defaultUp) + "\nconst reservedDefaultDown = " + strconv.Quote(defaultDown)
	runGeneratedFixture(t, source, assertions, StableIDs{ModelFields: map[string]map[string]int{
		"Probe":        {"id": 1, "order": 2, "value": 3},
		"Value":        {"id": 1, "amount": 2, "probes": 3, "probeCount": 4, "totalOrder": 5},
		"OrderSummary": {"order": 1, "count": 2},
	}})
}

const reservedIdentifierAssertions = `package generated
import("testing";"bytes";"strings";"net/http/httptest";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/dataloader";"github.com/light-speak/luxo/internal/testutil";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres")
func TestReservedSQLNames(t *testing.T){
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies());testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 t.Run("migration",func(t *testing.T){if _,err:=pg.Exec(t.Context(),db,reservedMigrationSQL);err!=nil{t.Error("generated migration rejected legal schema names",err)}})
 if _,err:=pg.Exec(t.Context(),db,"DROP TABLE IF EXISTS probes;DROP TABLE IF EXISTS \"values\";CREATE TABLE probes(id bigint PRIMARY KEY,\"order\" bigint NOT NULL);CREATE TABLE \"values\"(id bigint PRIMARY KEY,amount bigint NOT NULL);INSERT INTO probes VALUES(1,42);INSERT INTO \"values\" VALUES(1,84),(42,100)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);app.SetLoaders(NewDefaultLoaders(app,dataloader.DefaultConfig()))
 t.Run("create_column",func(t *testing.T){got,err:=app.Probe.Create().SetId(2).SetOrder(7).Exec(t.Context());if err!=nil||got==nil||got.Order!=7{t.Error("generated column identifier failed",got,err)}})
 t.Run("create_table",func(t *testing.T){got,err:=app.Value.Create().SetId(2).SetAmount(8).Exec(t.Context());if err!=nil||got==nil||got.Amount!=8{t.Error("generated table identifier failed",got,err)}})
 t.Run("select_column",func(t *testing.T){got,err:=app.Probe.Where(ProbeWhere.Order.Eq(42)).Select(ProbeFields.Id,ProbeFields.Order).All(t.Context());if err!=nil||len(got)!=1||got[0].Order!=42{t.Error("generated filter/projection failed",got,err)}})
 t.Run("select_table",func(t *testing.T){got,err:=app.Value.Find(t.Context(),1);if err!=nil||got==nil||got.Amount!=84{t.Error("generated lookup table failed",got,err)}})

 t.Run("batch_create",func(t *testing.T){got,err:=app.Probe.CreateMany().Add(ProbeCreateInput{Id:3,Order:42}).Add(ProbeCreateInput{Id:4,Order:8}).Exec(t.Context());if err!=nil||len(got)!=2{t.Error(got,err)}})
 t.Run("aggregate",func(t *testing.T){got,err:=app.Probe.Where().Sum(t.Context(),ProbeFields.Order);if err!=nil||got!=99{t.Error(got,err)}})
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,sample:=range []struct{body,fragment string}{
  {` + "`" + `{"$api":"byOrder","minimum":40,"$select":"id,order"}` + "`" + `, ` + "`" + `"order":42` + "`" + `},
  {` + "`" + `{"$api":"listProbesByOrderOrId","order":42,"id":4,"$select":"id,order"}` + "`" + `, ` + "`" + `"id":4` + "`" + `},
  {` + "`" + `{"$api":"orderGroups","$select":"order,count"}` + "`" + `, ` + "`" + `"count":2` + "`" + `},
  {` + "`" + `{"$api":"listProbesByIdInOrOrder","ids":[],"order":42,"$select":"id,order"}` + "`" + `, ` + "`" + `"id":1` + "`" + `},
  {` + "`" + `{"$api":"listProbesByIdNotInOrOrder","ids":[],"order":999,"$select":"id,order"}` + "`" + `, ` + "`" + `"id":4` + "`" + `},
  {` + "`" + `{"$api":"relatedValue","$select":"id,probes{id,order},probeCount,totalOrder"}` + "`" + `, ` + "`" + `"probeCount":2,"totalOrder":84` + "`" + `},
  {` + "`" + `{"$api":"relatedProbe","$select":"id,value{id,amount}"}` + "`" + `, ` + "`" + `"value":{"id":42,"amount":100}` + "`" + `},
  {` + "`" + `{"$api":"loadProbes","order":42,"$select":"id,order"}` + "`" + `, ` + "`" + `"order":42` + "`" + `},
 }{t.Run(sample.body,func(t *testing.T){response:=httptest.NewRecorder();router.ServeHTTP(response,httptest.NewRequest("POST","/luvia",strings.NewReader(sample.body)));if response.Code!=200||!bytes.Contains(response.Body.Bytes(),[]byte(sample.fragment)){t.Fatal(response.Code,response.Body.String())}})}
 t.Run("typed_update",func(t *testing.T){n,err:=app.Probe.Where(ProbeWhere.Id.Eq(1)).Update().SetOrder(43).Exec(t.Context());if err!=nil||n!=1{t.Error(n,err)};row,err:=app.Probe.Find(t.Context(),1);if err!=nil||row.Order!=43{t.Error(row,err)}})
 t.Run("default_migration",func(t *testing.T){
  if _,err:=pg.Exec(t.Context(),db,reservedDefaultUp);err!=nil{t.Fatal("default upgrade",err)}
  if _,err:=pg.Exec(t.Context(),db,"INSERT INTO probes(id) VALUES(99)");err!=nil{t.Fatal(err)}
  row,err:=app.Probe.Find(t.Context(),99);if err!=nil||row==nil||row.Order!=11{t.Fatal("default changed value",row,err)}
  if _,err:=pg.Exec(t.Context(),db,reservedDefaultDown);err!=nil{t.Fatal("default rollback",err)}
  if _,err:=pg.Exec(t.Context(),db,"INSERT INTO probes(id) VALUES(100)");err==nil{t.Fatal("rollback retained default")}
 })
}
`
