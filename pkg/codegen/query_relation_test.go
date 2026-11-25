package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGeneratedOverlappingRelationAndOpaqueDemand(t *testing.T) {
	runGeneratedFixture(t, overlappingRelationSource, overlappingRelationAssertions, StableIDs{
		ModelFields: map[string]map[string]int{
			"Profile": {"id": 1, "name": 2, "secret": 3},
			"Record":  {"id": 1, "profileId": 2, "profile": 3},
		},
		APIs:      map[string]int{"overlap": 1, "whole": 2, "nestedOnly": 3},
		APIParams: map[string]map[string]int{"overlap": {"id": 1}, "whole": {"id": 1}, "nestedOnly": {"id": 1}},
	})
}

const overlappingRelationSource = `
model Profile @noTime { id: Int @id name: String secret: String }
model Record @noTime { id: Int @id profileId: Int profile: Profile? }
fn opaque(record: Record): Result<String> @native
api overlap(id: Int): String {
 val record = Record.select().where(id: id).first() ?: throw error.NotFound
 val name = (record.profile ?: throw error.NotFound).name
 return name + "|" + opaque(record)?
}
api whole(id: Int): String {
 val record = Record.select().where(id: id).first() ?: throw error.NotFound
 return opaque(record)?
}
api nestedOnly(id: Int): String {
 val record = Record.select().where(id: id).first() ?: throw error.NotFound
 return (record.profile ?: throw error.NotFound).name
}
`

const overlappingRelationAssertions = `package generated
import("bytes";"context";"encoding/json";"fmt";"io";"net/http";"net/http/httptest";"os";"sync/atomic";"testing";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/dataloader";"github.com/light-speak/luxo/pkg/lux/pg")
type overlapResolver struct{calls atomic.Int64}
func(r *overlapResolver)Opaque(_ context.Context,record Record)(string,error){
 r.calls.Add(1)
 if record.Id!=10||record.ProfileId!=1||record.Profile==nil||record.Profile.Id!=1||record.Profile.Name!="Ada"||record.Profile.Secret!="token"{return "",fmt.Errorf("opaque consumer lost fields: %+v profile=%+v",record,record.Profile)}
 return record.Profile.Name+":"+record.Profile.Secret,nil
}
func TestOverlappingRelationAndOpaqueDemand(t *testing.T){
 connection:=os.Getenv("LUXO_TEST_DATABASE_URL");if connection==""{t.Skip("requires isolated LUXO_TEST_DATABASE_URL")}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE profiles(id bigint PRIMARY KEY,name text NOT NULL,secret text NOT NULL); CREATE TABLE records(id bigint PRIMARY KEY,profile_id bigint NOT NULL); INSERT INTO profiles VALUES(1,'Ada','token'); INSERT INTO records VALUES(10,1)");err!=nil{t.Fatal(err)}
 resolver:=&overlapResolver{};app:=NewFromDB(db);app.Resolver=resolver;app.SetLoaders(NewDefaultLoaders(app,dataloader.DefaultConfig()))
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 server:=httptest.NewServer(router);t.Cleanup(server.Close);client:=server.Client();client.Timeout=5*time.Second
 call:=func(t *testing.T,name string,id int64,binary bool)(int,[]byte){
  t.Helper();body,err:=json.Marshal(map[string]any{"$api":name,"id":id});if err!=nil{t.Fatal(err)}
  if binary{body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,nil,map[string]any{"id":id},router.Registry.ParamOrder(name));if err!=nil{t.Fatal(err)}}
  request,err:=http.NewRequestWithContext(t.Context(),"POST",server.URL,bytes.NewReader(body));if err!=nil{t.Fatal(err)}
  request.Header.Set("Content-Type","application/json");if binary{request.Header.Set("Content-Type","application/x-luxo");request.Header.Set("X-Luxo-Mode","binary")}
  response,err:=client.Do(request);if err!=nil{t.Fatal(err)}
  raw,readErr:=io.ReadAll(response.Body);closeErr:=response.Body.Close();if readErr!=nil||closeErr!=nil{t.Fatal(readErr,closeErr)}
  return response.StatusCode,raw
 }
 for _,sample:=range []struct{name,want string}{{"overlap","Ada|Ada:token"},{"whole","Ada:token"},{"nestedOnly","Ada"}}{for _,binary:=range []bool{false,true}{
  t.Run(fmt.Sprintf("%s/%t",sample.name,binary),func(t *testing.T){
   status,raw:=call(t,sample.name,10,binary);if status!=200{t.Fatalf("status=%d body=%s",status,raw)}
   var value string
   if binary{decoder:=codec.NewDecoder(raw);value=decoder.ReadString();if err:=decoder.Finish();err!=nil{t.Fatal(err)}}else{var response struct{Data string};if err:=json.Unmarshal(raw,&response);err!=nil{t.Fatal(err)};value=response.Data}
   if value!=sample.want{t.Fatal(value,sample.want)}
  })
 }}
 if resolver.calls.Load()!=4{t.Fatal("native consumption count",resolver.calls.Load())}
 for _,binary:=range []bool{false,true}{t.Run(fmt.Sprintf("missing/%t",binary),func(t *testing.T){status,raw:=call(t,"overlap",999,binary);if status!=404{t.Fatalf("missing record status=%d body=%s",status,raw)}})}
 if resolver.calls.Load()!=4{t.Fatal("missing record reached native consumer",resolver.calls.Load())}
}
`

func TestQueryRelationDemandBoundaries(t *testing.T) {
	source := `model Node @noTime { id: Int @id parentId: Int? parent: Node? @by(id, parentId) }
fn read(id: Int): Node { return Node.find(id: id) }
api selected(): Node { val row = Node.find(id: 2) val alias = row return alias }
api scalar(): Int { return Node.find(id: 2).id }
api discarded(): Node { val row = Node.find(id: 2) return Node { id: 3, parentId: null, parent: null } }
api called(): Node { return read(2) }
api unknown(): Node @native
api pure(): Int { return 7 }
`
	input := analyzePermissionFixture(t, source)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	models := map[string]*ast.ModelDecl{"Node": input.Files[0].Models[0]}
	functions := collectFunctionDeclarations(input)
	for _, api := range input.Files[0].APIs {
		selected := analyzeQueryRelations(api.Body, api.ReturnType, models, functions)
		want := 0
		if api.Name == "selected" || api.Name == "called" {
			want = 1
		}
		if len(selected) != want {
			t.Fatalf("%s: query provenance escaped its result: %d", api.Name, len(selected))
		}
	}
	if got := analyzeQueryRelations(nil, nil, models, nil); got != nil {
		t.Fatal("void body acquired response provenance")
	}
}

func TestQueryRelationLoopOrigins(t *testing.T) {
	for _, sample := range []struct {
		name, result, body string
		queries            int
	}{
		{"collected", "[Node]", `val rows = Node.where(id: outerId).all() return for row in rows { row }`, 1},
		{"yielded", "Node?", `val rows = Node.where(id: outerId).all() return for row in rows { if row.id == outerId { yield row } }`, 1},
		{"nested_statement", "Node?", `val rows = Node.where(id: outerId).all() return for row in rows { for value in [1] { if value == 1 { yield row } } }`, 1},
		{"ignored_inner", "[Node]", `val rows = Node.where(id: outerId).all() val candidates = Node.where(id: innerId).all() return for row in rows { val ignored = for candidate in candidates { yield candidate } row }`, 1},
		{"returned_inner", "[Node]", `val candidates = Node.where(id: outerId).all() return for value in [1] { val match = for candidate in candidates { yield candidate } match ?: throw error.NotFound }`, 1},
		{"discarded", "Node", `val rows = Node.where(id: innerId).all() val ignored = for row in rows { yield row } return Node { id: outerId, parentId: null, parent: null }`, 0},
		{"manual", "[Node]", `return for value in [1] { Node { id: value, parentId: null, parent: null } }`, 0},
	} {
		t.Run(sample.name, func(t *testing.T) {
			input := analyzePermissionFixture(t, `model Node @noTime { id: Int @id parentId: Int? parent: Node? @by(id, parentId) }
api lookup(outerId: Int, innerId: Int): `+sample.result+` { `+sample.body+` }`)
			if len(input.Errors) != 0 {
				t.Fatal(input.Errors)
			}
			declaration := input.Files[0].APIs[0]
			selected := analyzeQueryRelations(declaration.Body, declaration.ReturnType, map[string]*ast.ModelDecl{"Node": input.Files[0].Models[0]}, nil)
			if len(selected) != sample.queries {
				t.Fatalf("selected queries=%d, want %d", len(selected), sample.queries)
			}
			for query := range selected {
				ast.WalkExpr(query, func(expr ast.Expr) {
					if name, ok := expr.(*ast.Ident); ok && name.Name == "innerId" {
						t.Fatal("discarded nested loop inherited outer response selection")
					}
				})
			}
		})
	}
}

func TestQueryRelationCollectionOrigins(t *testing.T) {
	for _, body := range []string{
		`return Node.select().all().filter { it.id > 0 }`,
		`val rows = Node.select().all() return rows.filter { it.id > 0 }`,
		`return Node.select().all().map { row -> row }`,
	} {
		input := analyzePermissionFixture(t, `model Node @noTime { id: Int @id parentId: Int? parent: Node? @by(id, parentId) }
api lookup(): [Node] { `+body+` }`)
		if len(input.Errors) != 0 {
			t.Fatal(input.Errors)
		}
		api := input.Files[0].APIs[0]
		selected := analyzeQueryRelations(api.Body, api.ReturnType, map[string]*ast.ModelDecl{"Node": input.Files[0].Models[0]}, nil)
		if len(selected) != 1 {
			t.Fatalf("returned collection lost query relation origin: %s; origins=%d", body, len(selected))
		}
	}
}

func TestQueryRelationBusinessDemand(t *testing.T) {
	input := analyzePermissionFixture(t, `model Node @noTime { id: Int @id parentId: Int? parent: Node? @by(id, parentId) }
api lookup(): [Int] { return Node.select().all().map { (it.parent ?: throw error.NotFound).id } }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	var out strings.Builder
	compileAPIBody(&out, input.Files[0].APIs[0], map[string]*ast.ModelDecl{"Node": input.Files[0].Models[0]}, nil)
	if !strings.Contains(out.String(), "resolveNodeListRelations") || !strings.Contains(out.String(), `Name: "parent"`) {
		t.Fatalf("business relation was not prepared before scalar map: %s", out.String())
	}
}

func TestRelationQueryModelBoundaries(t *testing.T) {
	models := map[string]*ast.ModelDecl{
		"Node":   {Name: "Node", Fields: []*ast.FieldDecl{{}, {Type: &ast.TypeRef{Name: "String"}}, {Type: &ast.TypeRef{Name: "Node"}}}},
		"Scalar": {Name: "Scalar", Fields: []*ast.FieldDecl{{Type: &ast.TypeRef{Name: "Node"}, Computed: &ast.ComputedField{}}}},
	}
	for _, sample := range []struct {
		expr ast.Expr
		want bool
	}{
		{&ast.Ident{Name: "Node"}, false},
		{&ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Literal{}, Field: "all"}}, false},
		{&ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "missing"}, Field: "all"}}, false},
		{&ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Scalar"}, Field: "all"}}, false},
		{&ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Node"}, Field: "count"}}, false},
		{&ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Node"}, Field: "find"}}, true},
		{&ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "query", TypeTagged: ast.TypeTagged{TypeTag: "NodeQueryBuilder"}}, Field: "all"}}, true},
	} {
		if name, got := relationQueryModel(sample.expr, models); got != sample.want || got && name != "Node" {
			t.Fatal("non-query expression acquired database provenance", name, got)
		}
	}
}

func TestGeneratedPureAPIHasNoScope(t *testing.T) {
	var out strings.Builder
	compileAPIBody(&out, &ast.ApiDecl{Name: "pure", Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ReturnStmt{Value: &ast.Literal{Value: "7"}},
	}}}, nil, nil)
	if strings.Contains(out.String(), "NewScope") || strings.Contains(out.String(), "SelectedRelations") {
		t.Fatal("pure handler acquired DataLoader setup", out.String())
	}
}

func TestQueryRelationEmission(t *testing.T) {
	model := &ast.ModelDecl{Name: "Node", Fields: []*ast.FieldDecl{{Name: "parent", Type: &ast.TypeRef{Name: "Node"}}}}
	query := &ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Node"}, Field: "all"}}
	c := &compiler{models: map[string]*ast.ModelDecl{"Node": model}, queryRelations: map[ast.Expr]loadProjection{query: {}}}
	for _, paginate := range []bool{false, true} {
		c.paginate = paginate
		code := c.compileExpr(query)
		if !strings.Contains(code, "len(result) > 0 && hasNodeSelectedRelations(nil)") ||
			!strings.Contains(code, "resolveNodeListRelations(ctx, app, result, nil)") {
			t.Fatal("list query omitted typed, lazy relation preparation", code)
		}
		if paginate && !strings.Contains(code, "return result, total, nil") {
			t.Fatal("relation loading discarded the exact pagination total", code)
		}
	}
	c.clientSelection = "req.Select"
	query.Func.(*ast.MemberExpr).Field = "find"
	if code := c.compileExpr(query); !strings.Contains(code, "result != nil && hasNodeSelectedRelations(req.Select)") {
		t.Fatal("nullable query omitted the presence guard", code)
	}
	call := &ast.CallExpr{Func: &ast.Ident{Name: "forwarded"}}
	c.queryRelations[call] = loadProjection{}
	if code := c.compileExpr(call); code != "forwarded()" {
		t.Fatal("local function result was hydrated twice", code)
	}
	if c.functionSelection(call, "forwarded") != "req.Select" || c.functionSelection(query, "other") != "req.Select" {
		t.Fatal("returned query lost call-local selection")
	}
	if c.functionSelection(&ast.CallExpr{}, "other") != "nil" {
		t.Fatal("unconsumed result inherited the response selection")
	}
	logic := compileSelectionLiteral([][]string{{"parent", "id"}})
	c.queryRelations[call] = loadProjection{logic: logic}
	if c.functionSelection(call, "forwarded") != logic {
		t.Fatal("business-only function demand was replaced by response fields")
	}
	c.expressionBindings = map[ast.Expr]string{query: "capturedQuery"}
	if c.compileExpr(query) != "capturedQuery" {
		t.Fatal("captured query was evaluated or hydrated twice")
	}
	if c.compileExpr(call) != "forwarded()" {
		t.Fatal("an unrelated capture replaced a function call")
	}
}

func TestRelationBusinessPathsTolerateIncompleteMetadata(t *testing.T) {
	models := map[string]*ast.ModelDecl{
		"Node":   {Name: "Node", Fields: []*ast.FieldDecl{{Name: "parent", Type: &ast.TypeRef{Name: "Parent"}}}},
		"Parent": {Name: "Parent", Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}, {Name: "untyped"}}},
	}
	paths := relationBusinessPaths(&loadDemand{model: "Node", paths: [][]string{{"parent", "id"}, {"parent", "missing"}, {"parent", "untyped"}}}, models)
	for _, path := range paths {
		if len(path) == 2 && path[0] == "parent" && path[1] == "id" {
			return
		}
	}
	t.Fatal("incomplete sibling metadata discarded a valid stored relation path")
}

func TestRelationScopeHelpers(t *testing.T) {
	var out strings.Builder
	generateRelationSelectionCheck(&out, "Node", []Relation{{FieldName: "parent"}, {FieldName: "children"}})
	writeTraceScope(&out, "\t")
	for _, want := range []string{`case "parent", "children":`, "if api.DebugTrace(traceCtx) != nil", "dataloader.NewScope(traceCtx)"} {
		if !strings.Contains(out.String(), want) {
			t.Fatal("static relation scope plan omitted", want)
		}
	}
	for name, want := range map[string]generatedImports{"str": 1 << 19, "convert": 1 << 20, "dataloader": 1 << 24, "unknown": 0} {
		if got := generatedUtilityPackageBit(name); got != want {
			t.Fatal("runtime import collided with another package", name, got)
		}
	}
	call := &ast.CallExpr{}
	analysis := loadDemandAnalysis{source: func(*ast.CallExpr) (string, bool) { return "Node", true }, origins: make(map[ast.Expr]*ast.NamedArg)}
	_, first := analysis.callSource(call)
	if _, again := analysis.callSource(call); again != first {
		t.Fatal("a repeated query AST changed its provenance identity")
	}
}

func TestGeneratedQueryRelationOrigins(t *testing.T) {
	runGeneratedFixture(t, `
model Node @noTime { id: Int @id parentId: Int? parent: Node? @by(id, parentId) }
fn queried(id: Int): Node { return Node.find(id: id) }
fn forwarded(id: Int): Node { return queried(id) }
fn nativeNode(id: Int): Result<Node> @native
api direct(id: Int): Node { return Node.find(id: id) }
api loaded(id: Int): Node { return Node.load(id) ?: throw error.NotFound }
api alias(id: Int): Node { val row = Node.find(id: id) val other = row return other }
api guarded(id: Int): Node { return Node.where(id: id).first() ?: throw error.NotFound }
api projected(id: Int): Node { return Node.where(id: id).select().first() ?: throw error.NotFound }
api cropped(id: Int): Node { val row = Node.find(id: id) row.parent = null return row }
api optional(id: Int): Node? { return Node.where(id: id).first() }
api many(): [Node] { return Node.where(parentId: 1).all() }
api filtered(): [Node] { return Node.where(id: 2).select().all().filter { it.id > 0 } }
api mapped(): [Node] { return Node.where(id: 2).select().all().map { row -> row } }
api sorted(): [Node] { return Node.where(id: 2).select().all().sortBy { it.id } }
api logicFiltered(): [Node] { return Node.where(id: 2).select().all().filter { (it.parent ?: throw error.NotFound).id == 1 } }
api scalarParent(): [Int] { return Node.where(id: 2).select().all().map { (it.parent ?: throw error.NotFound).id } }
fn relatedRows(): [Node] { return Node.where(id: 2).select().all() }
api scalarCalledParent(): [Int] { return relatedRows().map { (it.parent ?: throw error.NotFound).id } }
api logicCalledFiltered(): [Node] { return relatedRows().filter { (it.parent ?: throw error.NotFound).id == 1 } }
fn filteredRelatedRows(): [Node] { return Node.where(id: 2).select().all().filter { (it.parent ?: throw error.NotFound).id == 1 } }
fn forwardedRelatedRows(): [Node] { return filteredRelatedRows() }
fn optionalRelated(id: Int): Node? {
  val row = Node.where(id: id).select().first()
  if row != null { val parent = row.parent if parent != null && parent.id > 0 { return row } }
  return null
}
api ownLogicFiltered(): [Node] { return filteredRelatedRows() }
api forwardedOwnFiltered(): [Node] { return forwardedRelatedRows() }
api scalarOwnFiltered(): [Int] { return filteredRelatedRows().map { it.id } }
api optionalOwnRelated(id: Int): Node? { return optionalRelated(id) }
api page(): [Node] @paginate { return Node.where(id: 2).all() }
api builder(): [Node] { val query = Node.where(id: 2) return query.all() }
api collected(id: Int): [Node] { val rows = Node.where(id: id).all() return for row in rows { row } }
api yielded(id: Int): Node? { val rows = Node.where(id: id).all() return for row in rows { if row.id == id { yield row } } }
api collectedCall(id: Int): [Node] { return for row in Node.where(id: id).all() { row } }
api yieldedCall(id: Int): Node? { return for row in Node.where(id: id).all() { if row.id == id { yield row } } }
api countCall(id: Int): Int { var total = 0 for row in Node.where(id: id).all() { total += row.id } return total }
api queryElement(id: Int): [Node] { return for value in [id] { Node.find(id: value) } }
api nativeElement(id: Int): [Node] { return for value in [id] { nativeNode(value)? } }
api queryLiteral(id: Int): [Node] { return [Node.find(id: id)] }
api nativeLiteral(id: Int): [Node] { return [nativeNode(id)?] }
api mixedLiteral(id: Int): [Node] { return [Node.find(id: id), nativeNode(id)?] }
api nestedYield(id: Int): Node? { val rows = Node.where(id: id).all() return for row in rows { for value in [1] { if value == 1 { yield row } } } }
api ignoredInner(id: Int): [Node] { val rows = Node.where(id: id).all() val candidates = Node.where(id: 3).all() return for row in rows { val ignored = for candidate in candidates { yield candidate } row } }
api manualLoop(id: Int): [Node] { return for value in [id] { Node { id: value, parentId: 1, parent: Node { id: 99, parentId: null, parent: null } } } }
api loopCropped(id: Int): [Node] { val rows = Node.where(id: id).all() return for row in rows { row.parent = null row } }
api transactional(id: Int, newParentId: Int): Node {
  var row: Node? = null
  transaction {
    Node.create(id: newParentId, parentId: null)
    Node.where(id: id).update(parentId: newParentId)
    row = Node.find(id: id)
  }
  return row ?: throw error.NotFound
}
api through(id: Int): Node { return forwarded(id) }
api mixed(id: Int, database: Boolean): Node { return when { database -> queried(id) else -> nativeNode(id)? } }
api manual(id: Int): Node { return Node { id: id, parentId: 1, parent: Node { id: 99, parentId: null, parent: null } } }
api nativeValue(id: Int): Node { return nativeNode(id)? }
api scalar(id: Int): Int { return queried(id).id }
`, queryRelationAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"Node": {"id": 1, "parentId": 2, "parent": 3},
	}})
}

const queryRelationAssertions = `package generated
import("bytes";"context";"encoding/json";"net";"net/http/httptest";"testing";"sync/atomic";"errors";"github.com/light-speak/luxo/internal/testutil";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/dataloader";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/light-speak/luxo/pkg/lux/rpc";"github.com/light-speak/luxo/pkg/lux/schema";"github.com/light-speak/luxo/pkg/lux/selection";"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres")
type queryRelationNative struct{calls atomic.Int32}
func(r *queryRelationNative)NativeNode(_ context.Context,id int64)(Node,error){r.calls.Add(1);parent:=int64(1);return Node{Id:id,ParentId:&parent,Parent:&Node{Id:99}},nil}
func TestQueryRelationOrigins(t *testing.T){
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies());testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE nodes(id bigint PRIMARY KEY,parent_id bigint);INSERT INTO nodes VALUES(1,NULL),(2,1),(3,1)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);resolver:=&queryRelationNative{};app.Resolver=resolver
 var loads atomic.Int32;var fail atomic.Bool;cfg:=dataloader.DefaultConfig();loaders:=NewDefaultLoaders(app,cfg);original:=loaders.NodeParent
 loaders.NodeParent=dataloader.New(func(ctx context.Context,keys []int64,fields []*selection.Field)(map[int64]*Node,error){loads.Add(1);if fail.Load(){return nil,errors.New("private database failure")};return original.LoadAll(ctx,keys,fields)},cfg);app.SetLoaders(loaders)
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}();defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,sample:=range []struct{name string;database,wantRelation bool}{
  {"filtered",true,true},{"mapped",true,true},{"sorted",true,true},{"logicFiltered",true,true},{"ownLogicFiltered",true,true},{"forwardedOwnFiltered",true,true},{"optionalOwnRelated",true,true},{"direct",true,true},{"loaded",true,true},{"page",true,true},{"alias",true,true},{"guarded",true,true},{"projected",true,true},{"optional",true,true},{"many",true,true},{"builder",true,true},{"collected",true,true},{"yielded",true,true},{"collectedCall",true,true},{"yieldedCall",true,true},{"queryElement",true,true},{"nativeElement",false,false},{"queryLiteral",true,true},{"nativeLiteral",false,false},{"mixedLiteral",true,true},{"nestedYield",true,true},{"ignoredInner",true,true},{"through",true,true},{"mixed",true,true},{"mixed",false,false},{"manual",false,false},{"manualLoop",false,false},{"nativeValue",false,false},
 }{t.Run(sample.name+map[bool]string{true:"/database",false:"/manual"}[sample.database],func(t *testing.T){
  for mode:=0;mode<3;mode++{
   before:=loads.Load();fields:=[]*selection.Field{{Name:"id"},{Name:"parent",Children:[]*selection.Field{{Name:"id"}}}}
   mask,err:=schema.SelectToFieldMask(fields,router.Schema.Models["Node"],router.Schema);if err!=nil{t.Fatal(err)}
   params:=map[string]any{};metadata:=router.Registry.ParamOrder(sample.name);for _,param:=range metadata{switch param.Name{case "id":params["id"]=int64(2);case "database":params["database"]=sample.database}}
   wire,err:=api.EncodeBinaryRequest(router.Schema.APIs[sample.name].ID,mask,params,metadata);if err!=nil{t.Fatal(err)}
   var raw []byte
   if mode==2{
    _,offset:=codec.ReadVarint(wire,0);size,n:=codec.ReadVarint(wire,offset);offset+=n+int(size);raw,err=client.CallWithMaskContext(t.Context(),"",router.Schema.APIs[sample.name].ID,mask,wire[offset:]);if err!=nil{t.Fatal(err)}
   }else{
    body:=wire;if mode==0{document:=map[string]any{"$api":sample.name,"$select":"id,parent{id}"};for name,value:=range params{document[name]=value};body,err=json.Marshal(document);if err!=nil{t.Fatal(err)}}
    request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));if mode==1{request.Header.Set("X-Luxo-Mode","binary")};response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatalf("mode=%d status=%d body=%s",mode,response.Code,response.Body)};raw=response.Body.Bytes()
   }
   if mode!=0{raw,err=router.StreamPayloadJSON(sample.name,raw);if err!=nil{t.Fatal(err)}}
   var output struct{Data json.RawMessage};if mode==0{if err:=json.Unmarshal(raw,&output);err!=nil{t.Fatal(err)};raw=output.Data}
   want:=[]byte("\"parent\":{\"id\":99}");if sample.wantRelation{want=[]byte("\"parent\":{\"id\":1}")}
   if !bytes.Contains(raw,want)||loads.Load()-before!=map[bool]int32{true:1,false:0}[sample.wantRelation]{t.Fatalf("mode=%d lost relation or overwrote manual value: %s, loads=%d",mode,raw,loads.Load()-before)}
   if sample.name=="mixedLiteral"&&!bytes.Contains(raw,[]byte("\"parent\":{\"id\":99}")){t.Fatalf("query selection overwrote a native list element: mode=%d data=%s",mode,raw)}
  }
 })}
 for _,sample:=range []struct{body string;want string}{
  {"{\"$api\":\"direct\",\"id\":2,\"$select\":\"id\"}","{\"data\":{\"id\":2}}"},
  {"{\"$api\":\"scalar\",\"id\":2}","{\"data\":2}"},
  {"{\"$api\":\"countCall\",\"id\":2}","{\"data\":2}"},
  {"{\"$api\":\"optional\",\"id\":99,\"$select\":\"id,parent{id}\"}","{\"data\":null}"},
  {"{\"$api\":\"optionalOwnRelated\",\"id\":99,\"$select\":\"id,parent{id}\"}","{\"data\":null}"},
  {"{\"$api\":\"optionalOwnRelated\",\"id\":1,\"$select\":\"id,parent{id}\"}","{\"data\":null}"},
 }{
  before:=loads.Load();response:=httptest.NewRecorder();router.ServeHTTP(response,httptest.NewRequest("POST","/luvia",bytes.NewBufferString(sample.body)))
  if response.Code!=200||response.Body.String()!=sample.want||loads.Load()!=before{t.Fatalf("unselected, scalar or missing result unexpectedly loaded relations: %d %s",response.Code,response.Body)}
 }
 for _,name:=range []string{"queryElement","queryLiteral","mixedLiteral"}{testQueryValueNotFound(t,router,client,&loads,resolver,name)}
 for _,name:=range []string{"logicFiltered","logicCalledFiltered","ownLogicFiltered","forwardedOwnFiltered"}{t.Run("defaultSelection/"+name,func(t *testing.T){
  before:=loads.Load();response:=httptest.NewRecorder();router.ServeHTTP(response,httptest.NewRequest("POST","/luvia",bytes.NewBufferString("{\"$api\":\""+name+"\"}")))
  if response.Code!=400||loads.Load()!=before||!bytes.Contains(response.Body.Bytes(),[]byte("$select is required")){t.Fatalf("structured response accepted missing selection: api=%s status=%d data=%s loads=%d",name,response.Code,response.Body,loads.Load()-before)}
 })}
 for _,sample:=range []struct{name,selection,want string}{
  {"logicFiltered","id","{\"data\":[{\"id\":2}]}"},
  {"scalarParent","","{\"data\":[1]}"},
  {"scalarCalledParent","","{\"data\":[1]}"},
  {"ownLogicFiltered","id","{\"data\":[{\"id\":2}]}"},
  {"forwardedOwnFiltered","id","{\"data\":[{\"id\":2}]}"},
  {"scalarOwnFiltered","","{\"data\":[2]}"},
 }{
  before:=loads.Load();body:=map[string]string{"$api":sample.name};if sample.selection!=""{body["$select"]=sample.selection};wire,err:=json.Marshal(body);if err!=nil{t.Fatal(err)}
  response:=httptest.NewRecorder();router.ServeHTTP(response,httptest.NewRequest("POST","/luvia",bytes.NewReader(wire)))
  if response.Code!=200||response.Body.String()!=sample.want||loads.Load()!=before+1{t.Fatalf("business relation demand lost or leaked: %s %d %s loads=%d",sample.name,response.Code,response.Body,loads.Load()-before)}
 }
 for _,name:=range []string{"cropped","loopCropped"}{
  beforeCrop:=loads.Load();crop:=httptest.NewRecorder();router.ServeHTTP(crop,httptest.NewRequest("POST","/luvia",bytes.NewBufferString("{\"$api\":\""+name+"\",\"id\":2,\"$select\":\"id,parent{id}\"}")))
  if crop.Code!=200||!bytes.Contains(crop.Body.Bytes(),[]byte("\"parent\":null"))||loads.Load()!=beforeCrop+1{t.Fatalf("relation preparation overwrote later business trimming: %s %d %s",name,crop.Code,crop.Body)}
 }
 testQueryRelationTransactions(t,db)
 fail.Store(true)
 for _,name:=range []string{"direct","collectedCall","yieldedCall","queryElement","ownLogicFiltered","forwardedOwnFiltered","optionalOwnRelated"}{
  params:="";for _,param:=range router.Registry.ParamOrder(name){if param.Name=="id"{params=",\"id\":2"}}
  response:=httptest.NewRecorder();router.ServeHTTP(response,httptest.NewRequest("POST","/luvia",bytes.NewBufferString("{\"$api\":\""+name+"\""+params+",\"$select\":\"id,parent{id}\"}")))
  if response.Code!=500||bytes.Contains(response.Body.Bytes(),[]byte("private database failure"))||bytes.Contains(response.Body.Bytes(),[]byte("\"data\"")){t.Fatalf("failed relation published partial success or details: %s %d %s",name,response.Code,response.Body)}
 }
 fail.Store(false)
 for _,name:=range []string{"ownLogicFiltered","forwardedOwnFiltered","optionalOwnRelated"}{
  before:=loads.Load();params:="";for _,param:=range router.Registry.ParamOrder(name){if param.Name=="id"{params=",\"id\":2"}}
  response:=httptest.NewRecorder();router.ServeHTTP(response,httptest.NewRequest("POST","/luvia",bytes.NewBufferString("{\"$api\":\""+name+"\""+params+",\"$select\":\"id,parent{id}\"}")))
  want:="{\"data\":[]}";if name=="optionalOwnRelated"{want="{\"data\":{\"id\":2,\"parent\":{\"id\":100}}}"}
  if response.Code!=200||response.Body.String()!=want||loads.Load()!=before+1{t.Fatalf("function relation did not recover with current database state: %s %d %s loads=%d",name,response.Code,response.Body,loads.Load()-before)}
 }
}

func testQueryValueNotFound(t *testing.T,router *api.Router,client *rpc.Client,loads *atomic.Int32,resolver *queryRelationNative,name string){
 t.Helper();before:=loads.Load();nativeBefore:=resolver.calls.Load()
 fields:=[]*selection.Field{{Name:"id"},{Name:"parent",Children:[]*selection.Field{{Name:"id"}}}}
 mask,err:=schema.SelectToFieldMask(fields,router.Schema.Models["Node"],router.Schema);if err!=nil{t.Fatal(err)}
 definition:=router.Schema.APIs[name];metadata:=router.Registry.ParamOrder(name)
 for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\""+name+"\",\"id\":99,\"$select\":\"id,parent{id}\"}")
  if binary{body,err=api.EncodeBinaryRequest(definition.ID,mask,map[string]any{"id":int64(99)},metadata);if err!=nil{t.Fatal(err)}}
  request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));if binary{request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if response.Code!=404||loads.Load()!=before||resolver.calls.Load()!=nativeBefore{t.Fatalf("missing query value did not stop before relations/native: api=%s binary=%v status=%d body=%s",name,binary,response.Code,response.Body)}
 }
 for _,id:=range []int64{99,2}{
  params,err:=rpc.EncodeParams(rpc.ParamField{FieldID:metadata[0].FieldID,Value:id});if err!=nil{t.Fatal(err)}
  body,err:=client.CallWithMaskContext(t.Context(),"",definition.ID,mask,params)
  if id==99{var failure *luxerrors.AppError;if !errors.As(err,&failure)||failure.Code!=404||len(body)!=0||loads.Load()!=before||resolver.calls.Load()!=nativeBefore{t.Fatalf("LRPC missing value continued or became success: api=%s data=%x err=%v",name,body,err)};continue}
  if err!=nil{t.Fatal(err)}
  data,err:=router.StreamPayloadJSON(name,body);if err!=nil||!bytes.Contains(data,[]byte("\"parent\":{\"id\":1}"))||loads.Load()!=before+1{t.Fatalf("request after value failure lost data: api=%s %s err=%v",name,data,err)}
 }
}

func testQueryRelationTransactions(t *testing.T,db *pg.DB){
 t.Helper()
 app:=NewFromDB(db);app.SetLoaders(NewDefaultLoaders(app,dataloader.DefaultConfig()))
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)}
 fields:=[]*selection.Field{{Name:"id"},{Name:"parent",Children:[]*selection.Field{{Name:"id"}}}}
 mask,err:=schema.SelectToFieldMask(fields,router.Schema.Models["Node"],router.Schema);if err!=nil{t.Fatal(err)}
 sentinel:=errors.New("outer transaction rollback")
 for _,rollback:=range []bool{false,true}{
  parentID:=int64(100);if rollback{parentID++}
  encodedID,err:=json.Marshal(parentID);if err!=nil{t.Fatal(err)}
  err=app.Tx(t.Context(),func(tx *App)error{
   request:=&api.Request{Params:map[string]json.RawMessage{"id":json.RawMessage("2"),"newParentId":encodedID},Select:fields,FieldMask:mask,Buf:&api.ResponseBuf{}}
   if err:=handleTransactional(tx)(t.Context(),request);err!=nil{return err}
   data,err:=router.StreamPayloadJSON("transactional",request.Buf.B);if err!=nil{return err}
   var result struct{Id int64;Parent *struct{Id int64}}
   if err:=json.Unmarshal(data,&result);err!=nil{return err}
   if result.Id!=2||result.Parent==nil||result.Parent.Id!=parentID{t.Fatalf("relation escaped uncommitted transaction: rollback=%v data=%s",rollback,data)}
   if rollback{return sentinel};return nil
  })
  if rollback&&!errors.Is(err,sentinel)||!rollback&&err!=nil{t.Fatalf("transaction result: rollback=%v err=%v",rollback,err)}
  parent,err:=db.QueryScalar[int64](t.Context(),"SELECT parent_id FROM nodes WHERE id=2");if err!=nil||parent!=100{t.Fatalf("nested transaction escaped commit/rollback: parent=%d err=%v",parent,err)}
  count,err:=db.QueryScalar[int64](t.Context(),"SELECT count(*) FROM nodes WHERE id=$1",parentID);want:=int64(1);if rollback{want=0};if err!=nil||count!=want{t.Fatalf("parent write escaped rollback: rollback=%v count=%d err=%v",rollback,count,err)}
 }
}
`

func TestGeneratedPaginatedLoopCount(t *testing.T) {
	runGeneratedFixture(t, `model Node @noTime { id: Int @id }
type Snapshot { ids: [Int] }
type Counter { count: Int }
event Counted(count: Int)
sealed PageSource { FromDatabase FromMemory }
api plain(): [Node] @paginate { return Node.orderBy(id.asc).all() }
api selectedPage(): [Node] @paginate { return Node.select().orderBy(id.asc).all() }
api selectedFiltered(): [Node] @paginate { return Node.where(id > 1).select().orderBy(id.asc).all() }
api selectedBuilder(): [Node] @paginate { val query = Node.select() return query.orderBy(id.asc).all() }
api aliased(): [Node] @paginate { val rows = Node.orderBy(id.asc).all() return rows }
api mapped(): [Node] @paginate { val rows = Node.orderBy(id.asc).all() return for row in rows { row } }
api directMapped(): [Node] @paginate { return for row in Node.orderBy(id.asc).all() { row } }
api methodMapped(): [Node] @paginate { return Node.orderBy(id.asc).all().map { it } }
api projectedMap(): [Node] @paginate { return for row in Node.orderBy(id.asc).all() { Node { id: row.id + 10 } } }
api selected(left: Boolean): [Node] @paginate { return when { left -> Node.where(id == 1).orderBy(id.asc).all() else -> Node.orderBy(id.asc).all() } }
api mixed(database: Boolean): [Node] @paginate { return when { database -> Node.orderBy(id.asc).all() else -> [Node { id: 8 }, Node { id: 9 }, Node { id: 10 }, Node { id: 11 }] } }
api builderAlias(): [Node] @paginate { val query = Node.where(id > 1).orderBy(id.asc) return query.all() }
api nestedBranch(left: Boolean): [Node] @paginate { val rows = when { left -> Node.where(id == 1).orderBy(id.asc).all() else -> Node.orderBy(id.asc).all() } return for row in rows { row } }
api earlyBranch(left: Boolean): [Node] @paginate { if left { return Node.where(id == 1).orderBy(id.asc).all() } return Node.orderBy(id.asc).all() }
api nullableFallback(): [Node] @paginate { val rows: [Node]? = Node.orderBy(id.asc).all() return rows ?: [Node { id: 8 }, Node { id: 9 }] }
api nullAwaitDefault(): [Node] @paginate { val rows: [Node] = (await { null }) ?: Node.orderBy(id.asc).all() return rows }
api nullLiteralDefault(): [Node] @paginate { val rows: [Node] = null ?: Node.orderBy(id.asc).all() return rows }
api defaultFromBranch(database: Boolean): [Node] @paginate {
  val queryRows = Node.orderBy(id.asc).all()
  val rows: [Node] = null ?: (when { database -> queryRows else -> [Node { id: 8 }, Node { id: 9 }, Node { id: 10 }, Node { id: 11 }] })
  return rows
}
api sealedPage(database: Boolean): [Node] @paginate {
  val source: PageSource = when { database -> PageSource.FromDatabase() else -> PageSource.FromMemory() }
  return when (source) { is FromDatabase -> Node.orderBy(id.asc).all() is FromMemory -> [Node { id: 8 }, Node { id: 9 }] }
}
api channelPage(database: Boolean): [Node] @paginate {
  val ready: Channel<Int> = Channel(1)
  if database { ready <- 1 }
  return when { <-ready -> Node.orderBy(id.asc).all() else -> [Node { id: 8 }, Node { id: 9 }] }
}
api nullableBranch(database: Boolean): [Node] @paginate { val rows: [Node]? = when { database -> Node.orderBy(id.asc).all() else -> null } return rows ?: [Node { id: 8 }, Node { id: 9 }] }
api assigned(): [Node] @paginate { var rows: [Node] = [] rows = Node.orderBy(id.asc).all() return rows }
api transactionAssigned(): [Node] @paginate { var rows: [Node] = [] transaction { rows = Node.orderBy(id.asc).all() } return rows }
api unrelated(): [Node] @paginate { val ignored = Node.where(id == 2).all() return Node.orderBy(id.asc).all() }
api manualFilter(): [Node] @paginate { val rows = [Node { id: 1 }, Node { id: 2 }, Node { id: 3 }] return rows.filter { it.id > 1 } }
api manualSort(): [Node] @paginate { val rows = [Node { id: 3 }, Node { id: 1 }, Node { id: 2 }] return rows.sortBy { it.id } }
api manualLoop(ids: [Int]): [Node] @paginate { var rows = [Node { id: 8 }, Node { id: 9 }] for id in ids { rows = [Node { id: id }, Node { id: id + 1 }] } return rows }
api manualLoopValue(ids: [Int]): [Node] @paginate { var rows = [Node { id: 8 }, Node { id: 9 }] val ignored = for id in ids { rows = [Node { id: id }, Node { id: id + 1 }] id } return rows }
api manualLoopMethod(ids: [Int]): [Node] @paginate { var rows = [Node { id: 8 }, Node { id: 9 }] val ignored = ids.map { rows = [Node { id: it }, Node { id: it + 1 }] it } return rows }
api manualInputs(): [Node] @paginate { val rows = [Node { id: 8 }, Node { id: 9 }] val label = "rows=${rows.size()}" rows.size() > 4 !: throw error.Conflict for index in 1..rows.size() { val value = index } emit Counted(count: rows.size()) return rows }
api unrelatedInputs(): [Node] @paginate { val rows = Node.all() val label = "rows=${rows.size()}" rows.size() > 4 !: throw error.Conflict for index in 1..rows.size() { val value = index } emit Counted(count: rows.size()) return [Node { id: 8 }, Node { id: 9 }] }
api aggregateGuard(): [Node] @paginate { val count = Node.count() count > 4 !: throw error.Conflict return Node.orderBy(id.asc).all() }
api queryInput(): [Node] @paginate { val rows = Node.all() val ignored = Node.where(id > rows.size()).all() return Node.orderBy(id.asc).all() }
api terminalInput(): [Node] @paginate { val rows = Node.all() return Node.findMany(id: rows.size()) }
api assignedInput(): [Node] @paginate { var rows = Node.all() rows = Node.findMany(id: rows.size()) return rows }
api builderInput(): [Node] @paginate { val rows = Node.all() val query = Node.where(id > rows.size()) val ignored = query.all() return Node.orderBy(id.asc).all() }
api manualField(): [Node] @paginate { val rows = [Node { id: 8 }, Node { id: 9 }] val snapshot = Snapshot { ids: [] } snapshot.ids = rows.map { it.id } snapshot.ids.size() > 2 !: throw error.Conflict return rows }
api independentField(): [Node] @paginate { val rows = Node.all() val snapshot = Snapshot { ids: [] } snapshot.ids = rows.map { it.id } snapshot.ids.size() != 3 !: throw error.Conflict return Node.where(id > 1).orderBy(id.asc).all() }
api scalarField(): [Node] @paginate { val counter = Counter { count: 0 } counter.count = 1 return Node.orderBy(id.asc).all() }
`, paginatedLoopAssertions, StableIDs{ModelFields: map[string]map[string]int{"Node": {"id": 1}, "Snapshot": {"ids": 1}, "Counter": {"count": 1}, "PageSource": {"FromDatabase": 1, "FromMemory": 2}}, EventFields: map[string]map[string]int{"Counted": {"count": 1}}})
}

const paginatedLoopAssertions = `package generated
import("os";"bytes";"context";"encoding/json";"fmt";"net";"net/http/httptest";"strings";"sync";"testing";"github.com/jackc/pgx/v5";"github.com/jackc/pgx/v5/pgxpool";"github.com/light-speak/luxo/internal/testutil";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/event";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/light-speak/luxo/pkg/lux/rpc";"github.com/light-speak/luxo/pkg/lux/schema";"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres")
type pageSQLLog struct{mu sync.Mutex;queries []string}
func(s *pageSQLLog)TraceQueryStart(ctx context.Context,_ *pgx.Conn,data pgx.TraceQueryStartData)context.Context{s.mu.Lock();if strings.HasPrefix(strings.ToUpper(data.SQL),"SELECT"){s.queries=append(s.queries,data.SQL)};s.mu.Unlock();return ctx}
func(*pageSQLLog)TraceQueryEnd(context.Context,*pgx.Conn,pgx.TraceQueryEndData){}
func(s *pageSQLLog)take()[]string{s.mu.Lock();defer s.mu.Unlock();result:=s.queries;s.queries=nil;return result}
type pageCase struct{name string;params map[string]any;ids []int64;queries int}
func pageFixtureConnection(t *testing.T)string{
 t.Helper()
 if connection:=os.Getenv("LUXO_TEST_DATABASE_URL");connection!=""{return connection}
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies());testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 return connection
}
func TestPaginatedLoopCount(t *testing.T){
 connection:=pageFixtureConnection(t)
 cfg,err:=pgxpool.ParseConfig(connection);if err!=nil{t.Fatal(err)};log:=&pageSQLLog{};cfg.ConnConfig.Tracer=log
 pool,err:=pgxpool.NewWithConfig(t.Context(),cfg);if err!=nil{t.Fatal(err)};db:=pg.NewDBFromPool(pool);t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE nodes(id bigint PRIMARY KEY);INSERT INTO nodes VALUES(1),(2),(3)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);bus:=event.NewChanBus(8);app.EventBus=bus;t.Cleanup(func(){if err:=bus.Close();err!=nil{t.Error(err)}})
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}();defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}();client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,sample:=range []pageCase{
  {"plain",nil,[]int64{1,2,3},2},{"aliased",nil,[]int64{1,2,3},2},{"mapped",nil,[]int64{1,2,3},2},{"directMapped",nil,[]int64{1,2,3},2},{"methodMapped",nil,[]int64{1,2,3},2},{"projectedMap",nil,[]int64{11,12,13},2},
  {"selectedPage",nil,[]int64{1,2,3},2},{"selectedFiltered",nil,[]int64{2,3},2},{"selectedBuilder",nil,[]int64{1,2,3},2},
  {"selected",map[string]any{"left":true},[]int64{1},2},{"selected",map[string]any{"left":false},[]int64{1,2,3},2},
  {"mixed",map[string]any{"database":true},[]int64{1,2,3},2},{"mixed",map[string]any{"database":false},[]int64{8,9,10,11},0},
  {"assigned",nil,[]int64{1,2,3},2},{"transactionAssigned",nil,[]int64{1,2,3},2},{"unrelated",nil,[]int64{1,2,3},3},{"builderAlias",nil,[]int64{2,3},2},{"nestedBranch",map[string]any{"left":true},[]int64{1},2},{"nestedBranch",map[string]any{"left":false},[]int64{1,2,3},2},
  {"earlyBranch",map[string]any{"left":true},[]int64{1},2},{"earlyBranch",map[string]any{"left":false},[]int64{1,2,3},2},
  {"nullAwaitDefault",nil,[]int64{1,2,3},2},{"nullLiteralDefault",nil,[]int64{1,2,3},2},
  {"defaultFromBranch",map[string]any{"database":true},[]int64{1,2,3},2},{"defaultFromBranch",map[string]any{"database":false},[]int64{8,9,10,11},2},
  {"sealedPage",map[string]any{"database":true},[]int64{1,2,3},2},{"sealedPage",map[string]any{"database":false},[]int64{8,9},0},
  {"channelPage",map[string]any{"database":true},[]int64{1,2,3},2},{"channelPage",map[string]any{"database":false},[]int64{8,9},0},
  {"nullableFallback",nil,[]int64{1,2,3},2},{"nullableBranch",map[string]any{"database":true},[]int64{1,2,3},2},{"nullableBranch",map[string]any{"database":false},[]int64{8,9},0},
  {"manualFilter",nil,[]int64{2,3},0},{"manualSort",nil,[]int64{1,2,3},0},
  {"manualLoop",map[string]any{"ids":[]int64{}},[]int64{8,9},0},{"manualLoop",map[string]any{"ids":[]int64{4}},[]int64{4,5},0},{"manualLoop",map[string]any{"ids":[]int64{4,6}},[]int64{6,7},0},
  {"manualLoopValue",map[string]any{"ids":[]int64{}},[]int64{8,9},0},{"manualLoopValue",map[string]any{"ids":[]int64{4}},[]int64{4,5},0},{"manualLoopValue",map[string]any{"ids":[]int64{4,6}},[]int64{6,7},0},
  {"manualLoopMethod",map[string]any{"ids":[]int64{}},[]int64{8,9},0},{"manualLoopMethod",map[string]any{"ids":[]int64{4}},[]int64{4,5},0},{"manualLoopMethod",map[string]any{"ids":[]int64{4,6}},[]int64{6,7},0},
  {"manualInputs",nil,[]int64{8,9},0},{"unrelatedInputs",nil,[]int64{8,9},1},{"aggregateGuard",nil,[]int64{1,2,3},3},
  {"queryInput",nil,[]int64{1,2,3},4},{"terminalInput",nil,[]int64{3},3},{"assignedInput",nil,[]int64{3},3},{"builderInput",nil,[]int64{1,2,3},4},
  {"manualField",nil,[]int64{8,9},0},{"independentField",nil,[]int64{2,3},3},{"scalarField",nil,[]int64{1,2,3},2},
 }{for _,page:=range []int{1,2,5}{for mode:=0;mode<3;mode++{t.Run(fmt.Sprintf("%s/%v/page=%d/mode=%d",sample.name,sample.params,page,mode),func(t *testing.T){
  log.take();data:=callPage(t,router,client,sample,page,mode);assertPage(t,data,sample.ids,page)
  queries:=log.take();if len(queries)!=sample.queries{t.Fatalf("wrong SQL count: %v",queries)}
  unpaged:=0;for _,query:=range queries{upper:=strings.ToUpper(query);if !strings.Contains(upper,"COUNT(")&&!strings.Contains(upper," LIMIT $"){unpaged++}}
  wantUnpaged:=0;switch sample.name{case "unrelated","unrelatedInputs","terminalInput","assignedInput","independentField":wantUnpaged=1;case "queryInput","builderInput":wantUnpaged=2};if unpaged!=wantUnpaged{t.Fatalf("wrong SQL pushdown: %v",queries)}
 })}}}
}
func callPage(t *testing.T,router *api.Router,client *rpc.Client,sample pageCase,page,mode int)[]byte{
 t.Helper();params:=map[string]any{"page":int64(page),"pageSize":int64(1)};for name,value:=range sample.params{params[name]=value}
 mask,err:=codec.AppendSelectionMask(nil,[]byte{1},nil);if err!=nil{t.Fatal(err)}
 definition:=router.Schema.APIs[sample.name]
 body,err:=api.EncodeBinaryRequest(definition.ID,mask,params,router.Registry.ParamOrder(sample.name));if err!=nil{t.Fatal(err)}
 if mode==2{_,off:=codec.ReadVarint(body,0);size,n:=codec.ReadVarint(body,off);off+=n+int(size);body,err=client.CallWithMaskContext(t.Context(),"",definition.ID,mask,body[off:]);if err!=nil{t.Fatal(err)}}else{
  if mode==0{params["$api"]=sample.name;params["$select"]="id";body,err=json.Marshal(params);if err!=nil{t.Fatal(err)}}
  request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));if mode==1{request.Header.Set("X-Luxo-Mode","binary")}
  response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatalf("status=%d body=%s",response.Code,response.Body)};body=response.Body.Bytes()
 }
 if mode==0{var envelope struct{Data json.RawMessage};if err:=json.Unmarshal(body,&envelope);err!=nil{t.Fatal(err)};return envelope.Data}
 body,err=schema.BinaryPaginatedListToJSON(nil,body,router.Schema.Models["Node"],router.Schema);if err!=nil{t.Fatal(err)};return body
}
func assertPage(t *testing.T,data []byte,ids []int64,page int){
 t.Helper();var output struct{Items []struct{Id int64};Total int64;Page,PageSize int};if err:=json.Unmarshal(data,&output);err!=nil{t.Fatal(err)}
 want:=0;if page<=len(ids){want=1}
 if output.Total!=int64(len(ids))||output.Page!=page||output.PageSize!=1||len(output.Items)!=want||want==1&&output.Items[0].Id!=ids[page-1]{t.Fatalf("page/total lost or applied twice: %s",data)}
}
`
