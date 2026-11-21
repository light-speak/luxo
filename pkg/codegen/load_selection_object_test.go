package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestSelectedQueryReturnedGenericObjectsKeepClientPaths(t *testing.T) {
	for _, sample := range []struct{ name, declarations, result, body, selection string }{
		{"direct", `type Box<T> { value: T }`, "Box<Node?>", `return Box<Node?>(value: Node.select().first())`, `selection.At(req.Select, "value")`},
		{"alias", `type Box<T> { value: T }`, "Box<Node?>", `val box = Box<Node?>(value: Node.select().first()) return box`, `selection.At(req.Select, "value")`},
		{"nested", `type Box<T> { value: T }`, "Box<Box<Node?>>", `return Box<Box<Node?>>(value: Box<Node?>(value: Node.select().first()))`, `selection.At(req.Select, "value", "value")`},
		{"ordinary", `type Box { value: Node? }`, "Box", `return Box { value: Node.select().first() }`, `selection.At(req.Select, "value")`},
		{"shared", `type Pair<T> { left: T right: T }`, "Pair<Node?>", `val row = Node.select().first() return Pair<Node?>(left: row, right: row)`, `selection.Merge(selection.At(req.Select, "left"), selection.At(req.Select, "right"))`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			source := analyzePermissionFixture(t, `model Node @noTime { id: Int @id name: String secret: String } `+sample.declarations+` api page(): `+sample.result+` { `+sample.body+` }`)
			if len(source.Errors) != 0 {
				t.Fatal(source.Errors)
			}
			view, err := semantic.CompilationView(source)
			if err != nil {
				t.Fatal(err)
			}
			models, _ := collectInferredAPIs(view)
			api := view.Files[0].APIs[0]
			projections := analyzeSelectedQueryProjections(api.Body, models, api.ReturnType)
			if len(projections) != 1 {
				t.Fatal("lost the selected model query", len(projections))
			}
			for _, projection := range projections {
				if !projection.client || len(projection.columns) != 0 {
					t.Fatal("wrapping a model made its complete contents a logic requirement", projection)
				}
				compiler := newCompiler(models)
				if selection := compiler.compiledProjectionSelection(projection); selection != sample.selection {
					t.Fatal("client selection was not routed through the returned object", selection)
				}
				if strings.Contains(compiler.compiledQueryColumns("Node", projection, "req.Select"), "secret") {
					t.Fatal("unread secret became a required SQL column")
				}
			}
		})
	}
}

func TestSelectedQueryFunctionObjectsKeepSourceAndResponsePaths(t *testing.T) {
	for _, sample := range []struct{ name, result, body, selection string }{
		{"direct", "Box<Node?>", `return fetchBox()`, `req.Select`},
		{"nested", "Box<Box<Node?>>", `return Box<Box<Node?>>(value: fetchBox())`, `selection.At(req.Select, "value")`},
		{"extracted", "Node?", `return fetchBox().value`, `[]*selection.Field{{Name: "value", Children: req.Select}}`},
		{"alias", "Node?", `val box = fetchBox() return box.value`, `[]*selection.Field{{Name: "value", Children: req.Select}}`},
		{"branch-repacked", "Box<Node?>", `val box = fetchBox() if flag { return box } return Box<Node?>(value: box.value)`, `req.Select`},
		{"branch-same-field", "Node?", `val box = fetchBox() if flag { return box.value } return box.value`, `[]*selection.Field{{Name: "value", Children: req.Select}}`},
		{"business", "Box<Node?>", `val box = fetchBox() val name = (box.value ?: throw error.NotFound).name return box`, `selection.Merge(req.Select, []*selection.Field{{Name: "value", Children: []*selection.Field{{Name: "name"}}}})`},
		{"opaque", "Box<Node?>", `val box = fetchBox() val ignored = inspect(box)? return box`, `selection.Merge(req.Select, nil)`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			input := analyzePermissionFixture(t, `model Node @noTime { id: Int @id name: String secret: String }
type Box<T> { value: T }
fn fetchBox(): Box<Node?> { return Box<Node?>(value: Node.select().first()) }
fn inspect(box: Box<Node?>): Result<Int> @native
api page(flag: Boolean): `+sample.result+` { `+sample.body+` }`)
			view, err := semantic.CompilationView(input)
			if err != nil {
				t.Fatal(err)
			}
			models, _ := collectInferredAPIs(view)
			functions := collectFunctionDeclarations(view)
			for _, file := range view.Files {
				for _, api := range file.APIs {
					projections := analyzeSelectedQueryProjections(api.Body, models, api.ReturnType, functions)
					var call *ast.CallExpr
					ast.WalkExprs(api.Body, func(expr ast.Expr) {
						if current, ok := expr.(*ast.CallExpr); ok {
							if name, ok := current.Func.(*ast.Ident); ok && name.Name == "fetchBox" {
								call = current
							}
						}
					})
					projection, ok := projections[call]
					if !ok {
						t.Fatal("compiled function lost its structured result origin")
					}
					c := newCompiler(models)
					if got := c.compiledProjectionSelection(projection); got != sample.selection {
						t.Fatalf("selection=%s want=%s", got, sample.selection)
					}
				}
			}
		})
	}
}

func TestSelectedQueryModelIdentityAvoidsDataFields(t *testing.T) {
	for _, sample := range []struct{ name, body, columns string }{
		{"direct", `return row == row`, ""},
		{"sealed", `val left = Holder.Model(value: row) val right = Holder.Model(value: row) return left == right`, ""},
		{"labeled", `val left = Holder.Labeled(value: row, label: row.name) val right = Holder.Labeled(value: row, label: row.name) return left == right`, `, "name"`},
		{"relation", `return row.owner == row.owner`, `, "owner"`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			input := analyzePermissionFixture(t, `model Owner @noTime { id: Int @id name: String secret: String }
model Node @noTime { id: Int @id name: String secret: String ownerId: Int owner: Owner @by(id, ownerId) }
sealed Holder { Model(value: Node) Labeled(value: Node,label: String) }
api same(): Boolean { val row = Node.select().first() ?: throw error.NotFound `+sample.body+` }`)
			view, err := semantic.CompilationView(input)
			if err != nil {
				t.Fatal(err)
			}
			models, _ := collectInferredAPIs(view)
			for _, file := range view.Files {
				for _, api := range file.APIs {
					projections := analyzeSelectedQueryProjections(api.Body, models, api.ReturnType)
					if len(projections) != 1 {
						t.Fatal("missing selected model query")
					}
					for _, projection := range projections {
						if got := queryColumnArguments(projection.columns); got != sample.columns {
							t.Fatal("identity comparison requested model data", got, sample.columns)
						}
						if sample.name == "relation" && projection.logic != compileSelectionLiteral([][]string{{"owner", "id"}}) {
							t.Fatal("relation identity comparison lost the owner's primary key", projection.logic)
						}
					}
				}
			}
		})
	}
}

func TestGeneratedGenericFunctionSelectionSQL(t *testing.T) {
	runGeneratedFixture(t, `model Node @noTime { id: Int @id name: String secret: String }
type Box<T> { value: T }
sealed Holder { Model(value: Node) Labeled(value: Node,label: String) }
fn fetchBox(): Box<Node?> { return Box<Node?>(value: Node.select().first()) }
api direct(): Box<Node?> { return fetchBox() }
api nested(): Box<Box<Node?>> { return Box<Box<Node?>>(value: fetchBox()) }
api extracted(): Node? { val box = fetchBox() return box.value }
api same(): Boolean { val row = Node.select().first() ?: throw error.NotFound val left = Holder.Model(value: row) val right = Holder.Model(value: row) return left == right }
api sameLabeled(): Boolean { val row = Node.select().first() ?: throw error.NotFound val left = Holder.Labeled(value: row,label: row.name) val right = Holder.Labeled(value: row,label: row.name) return left == right }
`, genericFunctionSelectionAssertions, StableIDs{ModelFields: map[string]map[string]int{"Node": {"id": 1, "name": 2, "secret": 3}, "Box": {"value": 1}, "Holder": {"Model": 1, "Labeled": 2}, "Holder.Model": {"value": 1}, "Holder.Labeled": {"value": 1, "label": 2}}})
}

const genericFunctionSelectionAssertions = `package generated
import("bytes";"context";"encoding/json";"fmt";"net/http/httptest";"strings";"testing";
"github.com/light-speak/luxo/internal/testutil";"github.com/light-speak/luxo/pkg/lux";
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/pg";
"github.com/light-speak/luxo/pkg/lux/schema";"github.com/light-speak/luxo/pkg/lux/selection";
"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres")
type genericFunctionTrace struct{statements []string}
func(*genericFunctionTrace)DatabaseTraceDetails()bool{return true}
func(s *genericFunctionTrace)StartDatabaseQuery(ctx context.Context,meta lux.DatabaseTraceMeta)context.Context{
 if meta.Operation=="SELECT"{s.statements=append(s.statements,meta.Statement)};return ctx
}
func(*genericFunctionTrace)FinishDatabaseQuery(context.Context,lux.DatabaseTraceResult){}
func(*genericFunctionTrace)StartDatabaseAcquire(ctx context.Context,_ lux.DatabasePoolTraceMeta)context.Context{return ctx}
func(*genericFunctionTrace)FinishDatabaseAcquire(context.Context,error){}
func TestGenericFunctionSelectionSQL(t *testing.T){
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("generic_fn"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE nodes(id bigint PRIMARY KEY,name text NOT NULL,secret text NOT NULL); INSERT INTO nodes VALUES(1,'Ada','private')");err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,NewFromDB(db));err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 for _,sample:=range []struct{name,fields,want string}{{"direct","value{name}",` + "`" + `{"value":{"name":"Ada"}}` + "`" + `},{"nested","value{value{name}}",` + "`" + `{"value":{"value":{"name":"Ada"}}}` + "`" + `},{"extracted","name",` + "`" + `{"name":"Ada"}` + "`" + `}}{for _,binary:=range []bool{false,true}{
  t.Run(fmt.Sprintf("%s/%t",sample.name,binary),func(t *testing.T){
   declaration:=router.Schema.APIs[sample.name];model:=router.Schema.LookupModel(declaration.ReturnType)
   body,err:=json.Marshal(map[string]string{"$api":sample.name,"$select":sample.fields});if err!=nil{t.Fatal(err)}
   if binary{fields,err:=selection.Parse(sample.fields);if err!=nil{t.Fatal(err)};mask,err:=schema.SelectToFieldMask(fields,model,router.Schema);if err!=nil{t.Fatal(err)};body,err=api.EncodeBinaryRequest(declaration.ID,mask,nil,router.Registry.ParamOrder(sample.name));if err!=nil{t.Fatal(err)}}
   trace:=&genericFunctionTrace{};request:=httptest.NewRequest("POST","/",bytes.NewReader(body)).WithContext(lux.WithDatabaseTraceSink(t.Context(),trace))
   if binary{request.Header.Set("X-Luxo-Mode","binary")}else{request.Header.Set("Content-Type","application/json")}
   response:=httptest.NewRecorder();router.ServeHTTP(response,request)
   if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
   if len(trace.statements)!=1||strings.Contains(trace.statements[0],"secret")||!strings.Contains(trace.statements[0],` + "`" + `"name"` + "`" + `){t.Fatal("incorrect projected SQL",trace.statements)}
   data:=response.Body.Bytes()
   if binary{if declaration.ReturnNullable{var null bool;data,null,err=codec.UnwrapNullableRoot(data);if err!=nil||null{t.Fatal("expected present response",null,err)}};data,err=schema.BinaryToJSON(nil,data,model,router.Schema);if err!=nil{t.Fatal(err)}}else{var envelope struct{Data json.RawMessage};if err:=json.Unmarshal(data,&envelope);err!=nil{t.Fatal(err)};data=envelope.Data}
   if string(data)!=sample.want{t.Fatalf("got=%s want=%s",data,sample.want)}
  })
 }}
 for _,name:=range []string{"same","sameLabeled"}{for _,binary:=range []bool{false,true}{t.Run(fmt.Sprintf("%s/%t",name,binary),func(t *testing.T){
  body,err:=json.Marshal(map[string]string{"$api":name});if err!=nil{t.Fatal(err)}
  if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,nil,nil);if err!=nil{t.Fatal(err)}}
  trace:=&genericFunctionTrace{};request:=httptest.NewRequest("POST","/",bytes.NewReader(body)).WithContext(lux.WithDatabaseTraceSink(t.Context(),trace))
  if binary{request.Header.Set("X-Luxo-Mode","binary")}else{request.Header.Set("Content-Type","application/json")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
  if len(trace.statements)!=1||strings.Contains(trace.statements[0],"secret")||strings.Contains(trace.statements[0],` + "`" + `"name"` + "`" + `)!=(name=="sameLabeled"){t.Fatal("identity requested unrelated data",trace.statements)}
  if binary{dec:=codec.NewDecoder(response.Body.Bytes());if !dec.ReadBool(){t.Fatal("identical model payloads differ")};if err:=dec.Finish();err!=nil{t.Fatal(err)}}else{var envelope struct{Data bool};if err:=json.Unmarshal(response.Body.Bytes(),&envelope);err!=nil||!envelope.Data{t.Fatal(response.Body.String(),err)}}
 })}}
}
`

func TestSelectedQueryObjectStorage(t *testing.T) {
	for _, sample := range []struct {
		name, body, columns string
		client              bool
	}{
		{"direct", `return Box { rows: Node.select().all() }.rows`, "", true},
		{"alias", `val box = Box { rows: Node.select().all() } return box.rows`, "", true},
		{"nested", `val box = Outer { box: Box { rows: Node.select().all() } } return box.box.rows`, "", true},
		{"assignment", `val box = Box { rows: [] } box.rows = Node.select().all() return box.rows`, "", true},
		{"copy", `val box = Box { rows: Node.select().all() } val copy = box box.rows = [] return copy.rows`, "", true},
		{"nested_copy", `val outer = Outer { box: Box { rows: Node.select().all() } } val copy = outer.box outer.box.rows = [] return copy.rows`, "", true},
		{"nested_shared", `val outer = Outer { box: Box { rows: Node.select().all() } } val copy = outer copy.box.rows = [] return outer.box.rows`, "", false},
		{"reference", `val holder = Holder { id: 1, rows: [] } val alias = holder holder.rows = Node.select().all() return alias.rows`, "", true},
		{"reference_empty", `val holder = Holder { id: 1, rows: Node.select().all() } val alias = holder holder.rows = [] return alias.rows`, "", false},
		{"cycle", `val holder = Holder { id: 1, rows: Node.select().all() } holder.parent = holder return holder.parent?.rows ?: []`, "", true},
		{"function_reference", `val holder = makeHolder() val alias = holder holder.rows = Node.select().all() return alias.rows`, "", true},
		{"nullable_copy", `val box: Box? = Box { rows: Node.select().all() } val copy = box ?: throw error.NotFound copy.rows = [] return (box ?: throw error.NotFound).rows`, "", true},
		{"nullable_alias", `var box: Box? = Box { rows: [] } box = Box { rows: Node.select().all() } val alias = box val present = box ?: throw error.NotFound present.rows = [] return (alias ?: throw error.NotFound).rows`, "", true},
		{"input", `val alias = input input.box.rows = Node.select().all() return alias.box.rows`, "", true},
		{"input_copy", `val copy = input.box input.box.rows = Node.select().all() return copy.rows`, "", false},
		{"input_reference", `val alias = inputHolder inputHolder.rows = Node.select().all() return alias.rows`, "", true},
		{"input_rebind", `val alias = inputHolder inputHolder = Holder { id: 2, rows: Node.select().all() } return alias.rows`, "", false},
		{"stored", `val rows = Node.select().all() val box = Box { rows: [] } box.rows = rows return rows`, "", true},
		{"business_field", `val box = Box { rows: Node.select().all() } return box.rows.filter { it.name != "" }`, `, "name"`, true},
		{"opaque", `val box = Box { rows: Node.select().all() } val count = inspect(box)? return box.rows`, `, "id", "name"`, true},
		{"conditional", `val box = Box { rows: Node.select().all() } if flag { box.rows = [] } return box.rows`, "", true},
		{"loop", `val box = Box { rows: Node.select().all() } for id in ids { box.rows = [] } return box.rows`, "", true},
		{"manual_and_selected", `val box = Box { rows: [] } return [Node.select().first() ?: throw error.NotFound]`, "", true},
		{"nested_business", `val box = Outer { box: Box { rows: Node.select().all() } } return box.box.rows.filter { it.name != "" }`, `, "name"`, true},
		{"copy_business", `val box = Box { rows: Node.select().all() } val copy = box box.rows = [] return copy.rows.filter { it.name != "" }`, `, "name"`, true},
		{"opaque_cycle", `val holder = Holder { id: 1, rows: Node.select().all() } holder.parent = holder val count = inspectHolder(holder)? return holder.rows`, `, "id", "name"`, true},
		{"conditional_callback", `val box = Box { rows: Node.select().all() } ids.forEach { box.rows = [] } return box.rows`, "", true},
		{"conditional_when", `val box = Box { rows: Node.select().all() } val ignored = when { flag -> { box.rows = [] 1 } else -> 0 } return box.rows`, "", true},
		{"async", `val box = Box { rows: Node.select().all() } async { box.rows = [] } return box.rows`, "", true},
		{"value_loop", `val box = Box { rows: Node.select().all() } return for row in box.rows { row }`, "", true},
		{"opaque_input", `val old = input.box input.box.rows = Node.select().all() val count = inspectOuter(input)? return input.box.rows`, `, "id", "name"`, true},
		{"opaque_input_override", `val old = input.box input.box = Box { rows: Node.select().all() } val count = inspectOuter(input)? return input.box.rows`, `, "id", "name"`, true},
		{"conditional_binding", `var box = Box { rows: Node.select().all() } if flag { box = Box { rows: [] } } return box.rows`, "", true},
		{"conditional_null", `var holder: Holder? = Holder { id: 1, rows: Node.select().all() } if flag { holder = null } return (holder ?: throw error.NotFound).rows`, "", true},
		{"compound_field", `val row = Node.select().first() ?: throw error.NotFound val counter = Counter { total: row.id } counter.total += 1 return [row]`, `, "id"`, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			projections := selectedObjectProjections(t, sample.body, "[Node]")
			for _, projection := range projections {
				if columns := queryColumnArguments(projection.columns); columns != sample.columns || projection.client != sample.client {
					t.Errorf("columns=%q client=%t; want columns=%q client=%t", columns, projection.client, sample.columns, sample.client)
				}
			}
			if len(projections) != 1 {
				t.Fatalf("expected one selected Node source, got %d", len(projections))
			}
		})
	}
}

func selectedObjectProjections(t *testing.T, body, result string) []loadProjection {
	t.Helper()
	input := analyzePermissionFixture(t, `model Node @noTime { id: Int @id name: String }
model Holder @noTime { id: Int @id rows: [Node] @by(id, id) parent: Holder? @by(id, id) }
type Box { rows: [Node] }
type Outer { box: Box }
type Counter { total: Int }
fn inspect(box: Box): Result<Int> @native
fn inspectHolder(holder: Holder): Result<Int> @native
fn inspectOuter(outer: Outer): Result<Int> @native
fn makeHolder(): Holder { return Holder { id: 1, rows: [] } }
api page(input: Outer, inputHolder: Holder, flag: Boolean, ids: [Int]): `+result+` { `+body+` }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	models, _ := collectInferredAPIs(input)
	declaration := input.Files[0].APIs[0]
	projections := analyzeSelectedQueryProjections(declaration.Body, models, declaration.ReturnType, collectFunctionDeclarations(input))
	var ordered []loadProjection
	ast.WalkExprs(declaration.Body, func(expression ast.Expr) {
		if call, ok := expression.(*ast.CallExpr); ok && selectedQueryModel(call, models) == "Node" {
			ordered = append(ordered, projections[call])
		}
	})
	return ordered
}

func TestSelectedQueryObjectSourcesStayIndependent(t *testing.T) {
	for _, sample := range []struct {
		name, body  string
		first, last loadProjection
	}{
		{"copy", `val box = Box { rows: Node.select().all() } val copy = box box.rows = Node.select().all() val names = copy.rows.map { it.name } return box.rows`, loadProjection{columns: [][]string{{"name"}}}, loadProjection{client: true}},
		{"reference", `val holder = Holder { id: 1, rows: Node.select().all() } val alias = holder holder.rows = Node.select().all() val names = alias.rows.map { it.name } return holder.rows`, loadProjection{}, loadProjection{client: true, columns: [][]string{{"name"}}}},
		{"nested_shared", `val outer = Outer { box: Box { rows: Node.select().all() } } val copy = outer copy.box.rows = Node.select().all() val names = outer.box.rows.map { it.name } return copy.box.rows`, loadProjection{}, loadProjection{client: true, columns: [][]string{{"name"}}}},
		{"nested_copy", `val outer = Outer { box: Box { rows: Node.select().all() } } val copy = outer.box outer.box.rows = Node.select().all() val names = copy.rows.map { it.name } return outer.box.rows`, loadProjection{columns: [][]string{{"name"}}}, loadProjection{client: true}},
		{"conditional", `val box = Box { rows: Node.select().all() } if flag { box.rows = Node.select().all() } return box.rows`, loadProjection{client: true}, loadProjection{client: true}},
		{"opaque_copy", `val box = Box { rows: Node.select().all() } val copy = box box.rows = Node.select().all() val size = inspect(copy)? return box.rows`, loadProjection{columns: [][]string{{"id"}, {"name"}}}, loadProjection{client: true}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			projections := selectedObjectProjections(t, sample.body, "[Node]")
			if len(projections) != 2 {
				t.Fatalf("sources=%d", len(projections))
			}
			for index, want := range []loadProjection{sample.first, sample.last} {
				got := projections[index]
				if got.client != want.client || queryColumnArguments(got.columns) != queryColumnArguments(want.columns) {
					t.Errorf("source%d=%+v want=%+v", index, got, want)
				}
			}
		})
	}
}

func TestSelectedQueryReturnedObjectContract(t *testing.T) {
	for _, sample := range []struct{ body, result, selection string }{
		{`return Box { rows: Node.select().all() }`, "Box", `selection.At(req.Select, "rows")`},
		{`return Outer { box: Box { rows: Node.select().all() } }`, "Outer", `selection.At(req.Select, "box", "rows")`},
	} {
		projections := selectedObjectProjections(t, sample.body, sample.result)
		if len(projections) != 1 || len(projections[0].columns) != 0 || !projections[0].client || (&compiler{}).compiledProjectionSelection(projections[0]) != sample.selection {
			t.Fatal("returned wrapper lost its client selection path", projections)
		}
	}
}
