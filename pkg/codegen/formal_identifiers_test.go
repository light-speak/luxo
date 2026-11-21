package codegen

import (
	"encoding/json"
	"fmt"
	"go/types"
	"os"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lockfile"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestBindingsProtectEveryGoPredeclaredName(t *testing.T) {
	for _, name := range append(types.Universe.Names(), "api", "sync") {
		if !runtimeLocalName(name) {
			t.Errorf("Go predeclared identifier %q is not protected from source bindings", name)
		}
	}
}

func TestFormalGenerationRejectsDuplicateDeclarationFieldsAtSemanticBoundary(t *testing.T) {
	for _, source := range []string{
		`type Packet { value: Int value: String }`,
		`interface Packet { value: Int value: String }`,
		`type Packet<T> { value: T value: Int }`,
	} {
		t.Run(source, func(t *testing.T) {
			input := analyzePermissionFixture(t, source)
			if len(input.Errors) == 0 {
				t.Error("duplicate field passed semantic analysis", source)
			}
			generated, err := Generate(input, "generated", GeneratorConfig{
				Driver: DriverPG,
				IDs:    StableIDs{ModelFields: map[string]map[string]int{"Packet": {"value": 1}}},
			})
			if err == nil || generated != nil || !strings.Contains(err.Error(), "semantic") {
				t.Fatalf("duplicate field bypassed the semantic boundary: %v", err)
			}
		})
	}
}

func TestGeneratedInheritedFieldOverrideKeepsTypedAccess(t *testing.T) {
	runGeneratedFixture(t, `
interface Base { value: Int }
model Packet : Base @noTime { id: Int @id value: Int }
fn inheritedValue(value: Packet): Int { return value.value }
`, `package generated
import "testing"
func TestInheritedValue(t *testing.T) {
 app := &App{}
 value := &Packet{Id: 1, Value: 42}
 got, err := app.inheritedValue(t.Context(), value)
 if err != nil || got != 42 { t.Fatal(got, err) }
}
`, StableIDs{ModelFields: map[string]map[string]int{"Packet": {"id": 1, "value": 2}}})
}

func TestPredeclaredBindingsShareRepeatedSourceName(t *testing.T) {
	params := []*ast.ParamDecl{{Name: "nil"}}
	loop := &ast.ForStmt{VarName: "nil"}
	body := &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: loop}}}
	names := parameterBindings(params, body)
	if len(names) != 1 || names.name("nil") == "nil" {
		t.Fatalf("repeated source binding must use the same internal name: %v", names)
	}
	if params[0].Name != "nil" || loop.VarName != "nil" {
		t.Fatal("internal binding changed the source AST")
	}
}

func TestGeneratedGoPredeclaredBindings(t *testing.T) {
	var source, assertions strings.Builder
	assertions.WriteString("package generated\nimport(\"testing\";\"bytes\";\"net/http/httptest\";\"encoding/json\";\"github.com/light-speak/luxo/pkg/lux/api\";\"github.com/light-speak/luxo/pkg/lux/codec\")\n\nfunc TestPredeclaredChannels(t *testing.T){app:=&App{}\n")
	for index, name := range types.Universe.Names() {
		if token.LookupKeyword(name) != token.Ident {
			continue
		}
		fmt.Fprintf(&source, "fn protected%d(%s: Int): Int { val ch: Channel<Int> = Channel(%s) ch <- (%s + 1) ch.close() return <-ch }\n", index, name, name, name)
		fmt.Fprintf(&assertions, "if value,err:=app.protected%d(t.Context(),1);err!=nil||value!=2{t.Fatalf(\"%s: %%d %%v\",value,err)}\n", index, name)
	}
	source.WriteString("fn protectedLocal(): Int { val nil = 1 val ch: Channel<Int> = Channel(nil) ch <- 42 ch.close() return <-ch }\napi protectedAPI(nil: Int): Int { return nil + 1 }\n")
	assertions.WriteString("if value,err:=app.protectedLocal(t.Context());err!=nil||value!=42{t.Fatal(value,err)}\n}\n")
	assertions.WriteString(predeclaredAPIAssertions)
	runGeneratedFixture(t, source.String(), assertions.String(), StableIDs{
		APIs: map[string]int{"protectedAPI": 1}, APIParams: map[string]map[string]int{"protectedAPI": {"nil": 1}},
	})
}

const predeclaredAPIAssertions = `
func TestPredeclaredAPI(t *testing.T){
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,&App{});err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\"protectedAPI\",\"nil\":5}")
  if binary{var encoded codec.Encoder;encoded.WriteVarint(1);encoded.WriteVarint(0);encoded.WriteFieldInt(1,5);encoded.WriteEnd();body=encoded.Bytes()}
  request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));request.Header.Set("Content-Type","application/json")
  if binary{request.Header.Set("X-Luxo-Mode","binary");request.Header.Set("Content-Type","application/x-luxo")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
  if binary{if !bytes.Equal(response.Body.Bytes(),codec.AppendSvarint(nil,6)){t.Fatal(response.Body.Bytes())}}else{
   var value struct{Data int64};if err:=json.Unmarshal(response.Body.Bytes(),&value);err!=nil||value.Data!=6{t.Fatal(value,err)}
  }
 }
}
`

func TestGeneratedInferredUnicodeFields(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime { id: Int @id école: String 用户: String 𐐨value: String name: String orderId: Int }
api getUserByÉcole
api getUserBy用户
api getUserBy𐐀value
api getUserByNameAndÉcole
api getUserByÉcoleOrName
api getUserByNameOr𐐀value
api getUserByOrderIdOrName
`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/schema")
func TestUnicodeInferredSchema(t *testing.T){
 catalog:=schema.New()
 if err:=RegisterSchema(catalog);err!=nil{t.Fatal(err)}
 for api,fields:=range map[string][]string{
  "getUserByÉcole":{"école"},"getUserBy用户":{"用户"},"getUserBy𐐀value":{"𐐨value"},
  "getUserByNameAndÉcole":{"name","école"},"getUserByÉcoleOrName":{"école","name"},
  "getUserByNameOr𐐀value":{"name","𐐨value"},"getUserByOrderIdOrName":{"orderId","name"},
 }{
  entry:=catalog.APIs[api]
  if entry==nil||len(entry.Params)!=len(fields){t.Fatalf("%s: %+v",api,entry)}
  for index,field:=range fields{
   wantType:=schema.FieldString;if field=="orderId"{wantType=schema.FieldInt}
   if entry.Params[index].Name!=field||entry.Params[index].Type!=wantType{t.Fatalf("%s: %+v",api,entry.Params[index])}
  }
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "école": 2, "用户": 3, "𐐨value": 4, "name": 5, "orderId": 6}}})
}

func TestGeneratedCompoundQueriesPostgres(t *testing.T) {
	if os.Getenv("LUXO_AUDIT_POSTGRES_URL") == "" {
		t.Skip("requires an isolated LUXO_AUDIT_POSTGRES_URL")
	}
	runGeneratedFixture(t, `
model Entry @noTime { id: Int @id orderId: Int name: String école: String 𐐨value: String }
api countEntriesByOrderIdOrName
api countEntriesByNameAndÉcole
api countEntriesByNameAndÉcoleOr𐐀value
`, `package generated
import("bytes";"encoding/json";"net/http/httptest";"os";"testing"
"github.com/light-speak/luxo/pkg/lux/api"
"github.com/light-speak/luxo/pkg/lux/codec"
"github.com/light-speak/luxo/pkg/lux/pg")
func TestCompoundQueries(t *testing.T){
 db,err:=pg.NewDB(t.Context(),os.Getenv("LUXO_AUDIT_POSTGRES_URL"));if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE entrys(id bigint PRIMARY KEY,order_id bigint,name text,école text,𐐨value text); INSERT INTO entrys VALUES(1,7,'Alice','Paris','A'),(2,8,'Bob','Paris','B'),(3,9,'Alice','Rome','C'),(4,10,'Zed','Lyon','D')");err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,NewFromDB(db));err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,sample:=range []struct{name string;params map[string]any;want int64}{
  {"countEntriesByOrderIdOrName",map[string]any{"orderId":int64(7),"name":"Bob"},2},
  {"countEntriesByNameAndÉcole",map[string]any{"name":"Alice","école":"Paris"},1},
  {"countEntriesByNameAndÉcoleOr𐐀value",map[string]any{"name":"Alice","école":"Paris","𐐨value":"D"},2},
 }{for _,binary:=range []bool{false,true}{
  payload:=map[string]any{"$api":sample.name};for key,value:=range sample.params{payload[key]=value}
  body,err:=json.Marshal(payload);if err!=nil{t.Fatal(err)}
  if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs[sample.name].ID,nil,sample.params,router.Registry.ParamOrder(sample.name));if err!=nil{t.Fatal(err)}}
  req:=httptest.NewRequest("POST","/",bytes.NewReader(body));req.Header.Set("Content-Type","application/json")
  if binary{req.Header.Set("Content-Type","application/x-luxo");req.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,req)
  if response.Code!=200{t.Fatalf("%s binary=%t status=%d: %s",sample.name,binary,response.Code,response.Body)}
  var count int64
  if binary{decoder:=codec.NewDecoder(response.Body.Bytes());count=decoder.ReadInt();if err:=decoder.Finish();err!=nil{t.Fatal(err)}}else{var envelope struct{Data int64};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil{t.Fatal(err)};count=envelope.Data}
  if count!=sample.want{t.Fatalf("%s binary=%t got=%d want=%d",sample.name,binary,count,sample.want)}
 }}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Entry": {"id": 1, "orderId": 2, "name": 3, "école": 4, "𐐨value": 5}}})
}

func TestInferredSchemaUsesFormalLockParameterIDs(t *testing.T) {
	for _, name := range []string{"User", "Status", "News", "Series"} {
		t.Run(name, func(t *testing.T) { testInferredSchemaLockParameters(t, name) })
	}
}

func testInferredSchemaLockParameters(t *testing.T, name string) {
	t.Helper()
	plural := ast.PluralModelName(name)
	list, top, get := "list"+plural+"ByName", "listTop5"+plural+"ByName", "get"+name+"ByName"
	model := testModel(name, []*ast.Directive{{Name: "noTime"}}, []*ast.FieldDecl{
		testField("id", "Int", &ast.Directive{Name: "id"}), testField("name", "String"),
	})
	file := &ast.File{Models: []*ast.ModelDecl{model}, APIs: []*ast.ApiDecl{
		{Name: list}, {Name: top}, {Name: get},
	}}
	input := result(file)
	lf := lockfile.New()
	if err := lf.Update(input.Files); err != nil {
		t.Fatal(err)
	}
	ids := StableIDs{ModelFields: map[string]map[string]int{name: lf.Models[name].Fields}, APIs: map[string]int{}, APIParams: map[string]map[string]int{}}
	for name, api := range lf.APIs {
		ids.APIs[name], ids.APIParams[name] = api.ID, api.Params
	}
	g := mustNewGenerator(t, GeneratorConfig{Driver: DriverPG, IDs: ids})
	if _, err := g.Generate(input, "generated", nil); err != nil {
		t.Fatal(err)
	}
	raw, err := g.BuildSchemaJSON(input, nil)
	if err != nil {
		t.Fatal(err)
	}
	var exported schema.Schema
	if err := json.Unmarshal(raw, &exported); err != nil {
		t.Fatal(err)
	}
	for apiName, wantParams := range map[string]int{list: 3, top: 1, get: 1} {
		api := exported.APIs[apiName]
		if api == nil || api.ReturnType != name || api.ReturnList != (apiName != get) || len(api.Params) != wantParams || api.Paginated != (wantParams == 3) {
			t.Fatalf("incorrect exported contract: %+v", api)
		}
		for _, param := range api.Params {
			if param.ID <= 0 || param.ID != lf.APIParamID(apiName, param.Name) {
				t.Fatalf("exported parameter lost stable ID: %+v", param)
			}
		}
	}
}

func TestFormalGenerationRequiresStableIdentifiers(t *testing.T) {
	field := &ast.FieldDecl{Name: "value", Type: &ast.TypeRef{Name: "Int"}}
	for name, file := range map[string]*ast.File{
		"model":  {Models: []*ast.ModelDecl{{Name: "M", Fields: []*ast.FieldDecl{field}}}},
		"type":   {Types: []*ast.TypeDecl{{Name: "T", Fields: []*ast.FieldDecl{field}}}},
		"extend": {Extends: []*ast.ExtendDecl{{Name: "M", Fields: []*ast.FieldDecl{field}}}},
		"event":  {Events: []*ast.EventDecl{{Name: "E", Params: []*ast.ParamDecl{{Name: "value", Type: field.Type}}}}},
		"API":    {APIs: []*ast.ApiDecl{{Name: "ping", ReturnType: field.Type, Directives: []*ast.Directive{{Name: "native"}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			input := result(file)
			if generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG}); err == nil || generated != nil || !strings.Contains(err.Error(), "stable ID") {
				t.Fatalf("public generation accepted unbound declarations: %v", err)
			}
			if data, err := BuildSchemaJSON(input, nil, GeneratorConfig{}); err == nil || data != nil || !strings.Contains(err.Error(), "stable ID") {
				t.Fatalf("public export accepted unbound declarations: %v", err)
			}
			g := mustNewGenerator(t, GeneratorConfig{})
			if generated, err := g.Generate(input, "generated", nil); err == nil || generated != nil {
				t.Fatal("context generation accepted unbound declarations")
			}
			if data, err := g.BuildSchemaJSON(input, nil); err == nil || data != nil {
				t.Fatal("context export accepted unbound declarations")
			}
		})
	}
}

func TestFormalEntrypointsPreserveExplicitIdentifiers(t *testing.T) {
	input := result(&ast.File{
		Models: []*ast.ModelDecl{{Name: "Record", Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}}, {Name: "name", Type: &ast.TypeRef{Name: "String"}},
		}}},
		APIs: []*ast.ApiDecl{{Name: "read", ReturnType: &ast.TypeRef{Name: "Record"}, Directives: []*ast.Directive{{Name: "native"}}, Params: []*ast.ParamDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}}},
	})
	config := GeneratorConfig{IDs: StableIDs{
		ModelFields: map[string]map[string]int{"Record": {"id": 17, "name": 42}},
		APIs:        map[string]int{"read": 73}, APIParams: map[string]map[string]int{"read": {"id": 9}},
	}}
	generated, err := Generate(input, "generated", config, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated.Files["writejson.gen.go"]), "AppendByte(42)") {
		t.Fatal("explicit sparse field ID disappeared")
	}
	data, err := BuildSchemaJSON(input, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	var exported schema.Schema
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatal(err)
	}
	if exported.APIs["read"].ID != 73 || exported.APIs["read"].Params[0].ID != 9 || exported.Models["Record"].Fields[1].ID != 42 {
		t.Fatal("SDK export renumbered explicit identifiers")
	}
	if data, err := BuildSchemaJSON(input, nil, GeneratorConfig{Driver: "invalid"}); err == nil || data != nil {
		t.Fatal("export ignored invalid configuration")
	}
}

func TestFormalGenerationRequiresSynthesizedAPIIdentifiers(t *testing.T) {
	input := result(&ast.File{Name: "origin/user.luxo", Models: []*ast.ModelDecl{{
		Name: "User", Directives: []*ast.Directive{{Name: "crud"}}, Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}}}},
	}}, Functions: []*ast.FnDecl{{Name: "score", ReturnType: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "service"}, {Name: "native"}}}}})
	for _, missing := range []string{"getUser", "listUsers", "svc:batchLoad:User", "svc:score"} {
		ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1}}})
		delete(ids.APIs, missing)
		g := mustNewGenerator(t, GeneratorConfig{IDs: ids})
		if generated, err := g.Generate(input, "generated", nil); err == nil || generated != nil || !strings.Contains(err.Error(), missing) {
			t.Fatalf("missing synthesized ID accepted: %s %v", missing, err)
		}
		if data, err := g.BuildSchemaJSON(input, nil); err == nil || data != nil || !strings.Contains(err.Error(), missing) {
			t.Fatalf("missing synthesized ID exported: %s %v", missing, err)
		}
	}
}

func TestFormalGenerationRejectsUnregisteredAPI(t *testing.T) {
	input := result(&ast.File{APIs: []*ast.ApiDecl{
		{Name: "known", ReturnType: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "native"}}},
		{Name: "missing", ReturnType: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "native"}}},
	}})
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"known": 1}}})
	if generated, err := g.Generate(input, "generated", nil); err == nil || generated != nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unregistered API generated: %v", err)
	}
	if data, err := g.BuildSchemaJSON(input, nil); err == nil || data != nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unregistered API exported: %v", err)
	}
}

func TestFormalCatalogDoesNotInventLocalResolvers(t *testing.T) {
	field := &ast.FieldDecl{Name: "related", Type: &ast.TypeRef{Name: "Target"}}
	for _, module := range []string{"origin/owner.luxo", "origin/consumer.luxo"} {
		input := result(
			&ast.File{Name: "origin/owner.luxo", Models: []*ast.ModelDecl{{Name: "M", Fields: []*ast.FieldDecl{field}}}},
			&ast.File{Name: module, Extends: []*ast.ExtendDecl{{Name: "M", Fields: []*ast.FieldDecl{field}}}},
		)
		g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"M": {"related": 1}}}})
		if err := g.validateRegisteredParameters(input, nil); err != nil {
			t.Fatalf("invented a resolver for owner-declared field (%s): %v", module, err)
		}
		if err := g.validateFieldIdentifiers(input, nil); err != nil {
			t.Fatalf("local extension incorrectly required an external key (%s): %v", module, err)
		}
	}
}

func TestFormalCatalogUsesExternalOwnershipAndEnums(t *testing.T) {
	input := result(&ast.File{Name: "origin/consumer.luxo", Extends: []*ast.ExtendDecl{{Name: "M", Fields: []*ast.FieldDecl{
		{Name: "related", Type: &ast.TypeRef{Name: "Target"}}, {Name: "state", Type: &ast.TypeRef{Name: "RemoteState"}},
	}}}})
	g := mustNewGenerator(t, GeneratorConfig{
		IDs: StableIDs{ModelFields: map[string]map[string]int{"M": {"related": 1, "state": 2, "id": 3}}},
		Events: &EventContext{
			ModelModule: map[string]string{"M": "owner"}, ModelFields: map[string]map[string]bool{"M": {"related": true}},
			EnumModule: map[string]string{"RemoteState": "owner"},
		},
	})
	if _, err := g.BuildSchemaJSON(input, nil); err != nil {
		t.Fatalf("invented internal API from external owner or enum: %v", err)
	}
	if len(g.events.ModelFields["M"]) != 1 || g.events.ModelFields["M"]["state"] {
		t.Fatal("catalog construction mutated external ownership")
	}
}

func TestFormalCatalogUsesLocalEnumDeclarations(t *testing.T) {
	input := result(&ast.File{Name: "origin/consumer.luxo", Enums: []*ast.EnumDecl{{Name: "LocalState", Values: []string{"OPEN"}}}, Extends: []*ast.ExtendDecl{{Name: "M", Fields: []*ast.FieldDecl{
		{Name: "state", Type: &ast.TypeRef{Name: "LocalState"}},
	}}}})
	g := generatorWithModelFieldIDs(map[string]map[string]int{"M": {"state": 1, "id": 2}})
	for _, hints := range []map[string]bool{nil, {"LocalState": false}, {"LocalState": true}} {
		if _, err := g.BuildSchemaJSON(input, hints); err != nil {
			t.Fatalf("local enum treated as relation: %v", err)
		}
	}
}

func TestFormalGenerationRequiresExternalPrimaryKeyIdentifier(t *testing.T) {
	input := result(&ast.File{Name: "origin/consumer.luxo", Extends: []*ast.ExtendDecl{{Name: "Remote", Fields: []*ast.FieldDecl{
		{Name: "state", Type: &ast.TypeRef{Name: "String"}},
	}}}})
	config := GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Remote": {"state": 1}}}, Events: &EventContext{
		ModelIDField: map[string]string{"Remote": "key"}, ModelIDType: map[string]string{"Remote": "UUID"},
	}}
	g := mustNewGenerator(t, config)
	if generated, err := g.Generate(input, "generated", nil); err == nil || generated != nil || !strings.Contains(err.Error(), "Remote.key") {
		t.Fatalf("synthetic external key has no wire ID: %v", err)
	}
	if data, err := g.BuildSchemaJSON(input, nil); err == nil || data != nil || !strings.Contains(err.Error(), "Remote.key") {
		t.Fatalf("export accepted incomplete external catalog: %v", err)
	}
	config.IDs.ModelFields["Remote"]["key"] = 2
	if _, err := Generate(input, "generated", config); err != nil {
		t.Fatalf("complete external key catalog rejected: %v", err)
	}
}

func TestSchemaExportRejectsInvalidSemanticResult(t *testing.T) {
	g := mustNewGenerator(t, GeneratorConfig{})
	for _, input := range []struct {
		name  string
		value *semantic.Result
		want  string
	}{
		{"nil", nil, "semantic result"},
		{"failed analysis", &semantic.Result{Errors: []semantic.Error{{Message: "invalid declared field"}}}, "invalid declared field"},
	} {
		t.Run(input.name, func(t *testing.T) {
			for _, entry := range []struct {
				name  string
				build func(*semantic.Result) ([]byte, error)
			}{
				{"public", func(r *semantic.Result) ([]byte, error) { return BuildSchemaJSON(r, nil, GeneratorConfig{}) }},
				{"context", func(r *semantic.Result) ([]byte, error) { return g.BuildSchemaJSON(r, nil) }},
			} {
				t.Run(entry.name, func(t *testing.T) {
					defer func() {
						if failure := recover(); failure != nil {
							t.Errorf("invalid input panicked: %v", failure)
						}
					}()
					data, err := entry.build(input.value)
					if err == nil || data != nil || !strings.Contains(err.Error(), input.want) {
						t.Fatalf("invalid analysis exported: data=%s error=%v", data, err)
					}
				})
			}
		})
	}
}

func TestGeneratedMultipleExtensionsPreserveFields(t *testing.T) {
	runGeneratedFixture(t, `extend Entry { left: Int }
extend Entry { right: Int }`, `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestMergedFields(t *testing.T){
 value:=Entry{Id:1,Left:42,Right:84};buf:=&api.ResponseBuf{}
 if err:=value.WriteLuxo(buf,nil);err!=nil{t.Fatal(err)}
 var got Entry;dec:=codec.NewDecoder(buf.B);got.ReadLuxo(dec)
 if err:=dec.Finish();err!=nil{t.Fatal(err)}
 if got.Id!=1||got.Left!=42||got.Right!=84{t.Fatalf("lost extension: %+v",got)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Entry": {"id": 1, "left": 2, "right": 3}}})
}

func TestInheritedSoftDeleteFieldContract(t *testing.T) {
	for _, sample := range []struct {
		field string
		valid bool
	}{
		{"deletedAt: DateTime?", true},
		{"deletedAt: String?", false},
		{"DeletedAt: DateTime?", false},
		{"deleted_at: DateTime?", false},
	} {
		t.Run(sample.field, func(t *testing.T) {
			input := analyzePermissionFixture(t, "model Base @noTime { id: Int @id "+sample.field+" }\nmodel Entry : Base @soft @noTime {}")
			if len(input.Errors) != 0 && (sample.valid || !strings.Contains(input.Errors[0].Message, "soft-delete field")) {
				t.Fatal(input.Errors)
			}
			name := strings.Split(sample.field, ":")[0]
			config := GeneratorConfig{Driver: DriverPG, IDs: StableIDs{ModelFields: map[string]map[string]int{
				"Base": {"id": 1, name: 2}, "Entry": {"id": 1, name: 2},
			}}}
			generated, err := Generate(input, "generated", config)
			if (err == nil) != sample.valid || (generated != nil) != sample.valid {
				t.Fatalf("inherited soft-delete generation: valid=%v error=%v", sample.valid, err)
			}
			schema, err := BuildSchemaJSON(input, nil, config)
			if (err == nil) != sample.valid || (schema != nil) != sample.valid {
				t.Fatalf("inherited soft-delete schema: valid=%v error=%v", sample.valid, err)
			}
		})
	}
}

func TestGeneratedSoftDeleteTimestamp(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name, field := "implicit", ""
		ids := map[string]int{"id": 1}
		if explicit {
			name, field = "explicit", "deletedAt: DateTime?"
			ids["deletedAt"] = 2
		}
		t.Run(name, func(t *testing.T) {
			runGeneratedFixture(t, "model Entry @soft @crud @noTime { id: Int @id "+field+" }", `package generated
import ("testing"; "time")
func TestSoftTimestamp(t *testing.T) {
 value := &Entry{Id: 42}
 if value.DeletedAt != nil { t.Fatal("new record marked deleted") }
 now := time.Unix(42, 123)
 value.DeletedAt = &now
 if !value.DeletedAt.Equal(now) { t.Fatal("timestamp changed") }
}
`, StableIDs{ModelFields: map[string]map[string]int{"Entry": ids}})
		})
	}
}

func TestGeneratedProtocolMethodFieldCollision(t *testing.T) {
	for _, declaration := range []string{"model", "type", "extend"} {
		for _, field := range []string{"writeLuxo", "WriteLuxo", "readLuxo", "ReadLuxo", "readLuxoInput", "ReadLuxoInput", "readJSONInput", "ReadJSONInput"} {
			t.Run(declaration+"/"+field, func(t *testing.T) {
				directives := ""
				if declaration == "model" {
					directives = " @noTime"
				}
				input := analyzePermissionFixture(t, declaration+" Entry"+directives+" { id: Int "+field+": Int }")
				if len(input.Errors) != 0 {
					t.Fatal(input.Errors)
				}
				config := GeneratorConfig{Driver: DriverPG, IDs: StableIDs{ModelFields: map[string]map[string]int{"Entry": {"id": 1, field: 2}}}}
				output, err := Generate(input, "generated", config)
				if err == nil || output != nil || !strings.Contains(err.Error(), "generated protocol method") {
					t.Fatalf("invalid field generated: %v", err)
				}
				schema, err := BuildSchemaJSON(input, nil, config)
				if err == nil || schema != nil || !strings.Contains(err.Error(), "generated protocol method") {
					t.Fatalf("invalid field exported: %v", err)
				}
			})
		}
	}
}

func TestGeneratedProtocolMethodFieldControls(t *testing.T) {
	runGeneratedFixture(t, "model Entry @noTime { id: Int writeColumnar: Int writeLuxoValue: Int readJsonInput: Int }", "package generated\n", StableIDs{ModelFields: map[string]map[string]int{"Entry": {"id": 1, "writeColumnar": 2, "writeLuxoValue": 3, "readJsonInput": 4}}})
}

func TestGeneratedConditionalMethodControls(t *testing.T) {
	runGeneratedFixture(t, `model Entry @noTime @withAuth(stores: [id]) { id: Int @id secret: String @hash verifySecrets: String createTokenValue: Int }`, `package generated
import "testing"
func TestConditionalMethods(t *testing.T) {
 value := &Entry{Id:42, Secret:"invalid", VerifySecrets:"ordinary field", CreateTokenValue:7}
 if value.VerifySecret("password") { t.Fatal("invalid hash accepted") }
 data,err:=value.AppendClaimsJSON(nil)
 if err!=nil || string(data)!="{\"id\":42}" { t.Fatalf("claims=%s err=%v",data,err) }
}
`, StableIDs{ModelFields: map[string]map[string]int{"Entry": {"id": 1, "secret": 2, "verifySecrets": 3, "createTokenValue": 4}}})
}

func TestEventGeneratedFieldNames(t *testing.T) {
	for _, fields := range [][]string{{"name", "Name"}, {"marshalLuxo"}, {"MarshalLuxo"}, {"unmarshalLuxo"}, {"UnmarshalLuxo"}, {"snapshotEvent"}, {"SnapshotEvent"}} {
		t.Run(strings.Join(fields, "/"), func(t *testing.T) {
			params := make([]string, len(fields))
			ids := make(map[string]int)
			for i, field := range fields {
				params[i] = field + ": Int"
				ids[field] = i + 1
			}
			input := analyzePermissionFixture(t, "event Changed("+strings.Join(params, ",")+")")
			if len(input.Errors) != 0 {
				t.Fatal(input.Errors)
			}
			config := GeneratorConfig{IDs: StableIDs{EventFields: map[string]map[string]int{"Changed": ids}}}
			output, err := Generate(input, "generated", config)
			if err == nil || output != nil {
				t.Fatalf("event name collision accepted: %v", err)
			}
			schema, err := BuildSchemaJSON(input, nil, config)
			if err == nil || schema != nil {
				t.Fatalf("event name collision exported: %v", err)
			}
		})
	}
}

func TestGeneratedErrorAndEventNameControls(t *testing.T) {
	runGeneratedFixture(t, `event Changed(writeLuxo: Int, snapshotEventValue: Int)
error Rejected(appendJSON: Int, appendText: Int, name: String, Name: String) { code: 422 message: "rejected" }
`, `package generated
import "testing"
func TestIndependentNames(t *testing.T) {
 data:=NewRejected(1,2,"lower","upper").Data
 raw,err:=data.AppendJSON(nil)
 if err!=nil || string(raw)!="{\"appendJSON\":1,\"appendText\":2,\"name\":\"lower\",\"Name\":\"upper\"}" {t.Fatalf("%s %v",raw,err)}
 source:=&ChangedEvent{WriteLuxo:42,SnapshotEventValue:7}
 wire,err:=source.MarshalLuxo();if err!=nil {t.Fatal(err)}
 var decoded ChangedEvent;if err:=decoded.UnmarshalLuxo(wire);err!=nil {t.Fatal(err)}
 if decoded!=*source {t.Fatal("event fields changed")}
 if _,err:=decoded.SnapshotEvent();err!=nil {t.Fatal(err)}
}
`, StableIDs{EventFields: map[string]map[string]int{"Changed": {"writeLuxo": 1, "snapshotEventValue": 2}}})
}

func TestEnumSymbolCatalogRemainsCompilationLocal(t *testing.T) {
	longName := strings.Repeat("Long", 20)
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{longName: {"id": 1}}}})
	valid := analyzePermissionFixture(t, `enum Role { ADMIN USER } fn choose(role: Role = Role.ADMIN): Role { return role }`)
	if len(valid.Errors) != 0 {
		t.Fatal(valid.Errors)
	}
	generated, err := generator.Generate(valid, "generated", nil)
	if err != nil || generated == nil || generator.enumSymbols != nil {
		t.Fatal("successful generation retained a compilation directory", err)
	}
	invalid := analyzePermissionFixture(t, `enum Role { ADMIN USER } model `+longName+` @noTime { id: Int @id }`)
	if len(invalid.Errors) != 0 {
		t.Fatal(invalid.Errors)
	}
	generated, err = generator.Generate(invalid, "generated", nil)
	if err == nil || generated != nil || generator.enumSymbols != nil {
		t.Fatal("database rejection retained a compilation directory", err)
	}
	plain := analyzePermissionFixture(t, `fn read(RoleADMIN: Int): Int { return RoleADMIN }`)
	generated, err = generator.Generate(plain, "generated", nil)
	if err != nil || generated == nil || generator.enumSymbols != nil || !strings.Contains(string(generated.Files["handler.gen.go"]), "RoleADMIN int64") {
		t.Fatal("a later compilation inherited enum symbols", err)
	}
}
