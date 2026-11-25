package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestFormalGenerateRejectsUnsafePagination(t *testing.T) {
	for _, body := range []string{
		`return Node.all().filter { it.id > 1 }`,
		`return Node.all().sortBy { it.id }`,
		`return for row in Node.all() { if row.id > 1 { break } row }`,
		`return Node.limit(1).all()`,
		`var rows = [Node { id: 1 }] val ids: [Int] = [] for id in ids { rows = Node.all() } return rows`,
		`var rows = Node.all() for id in [1] { rows = [Node { id: id }] } return rows`,
		`var rows = [Node { id: 1 }] val ignored = [1].map { rows = Node.all() it } return rows`,
		`val rows = Node.all() rows.size() > 1 !: throw error.Conflict return rows`,
		`val rows = Node.all() rows.size > 1 !: throw error.Conflict return rows`,
		`val rows = Node.all() rows.isEmpty !: throw error.Conflict return rows`,
		`val rows = Node.all() val label = "rows=${rows.size()}" return rows`,
		`val rows = Node.all() for index in 1..rows.size() { val value = index } return rows`,
		`val rows = Node.all() async { val count = rows.size() } return rows`,
		`val rows = Node.all() emit Counted(count: rows.size()) return rows`,
		`val rows = Node.all() val ignored = Node.where(id > rows.size()).all() return rows`,
		`val rows = Node.all() val ignored = Node.where(id > rows.size).all() return rows`,
		`val rows = Node.all() val ignored = Node.where(id > rows.size()).select().orderBy(id.asc).all() return rows`,
		`val rows = Node.all() val ignored = Node.findMany(id: rows.size()) return rows`,
		`val rows = Node.all() val ignored = Node.limit(rows.size()).all() return rows`,
		`return Box { rows: Node.all() }.rows.filter { it.id > 1 }`,
		`val box = Box { rows: Node.all() } val size = box.rows.size() return box.rows`,
		`return Outer { snapshot: Snapshot { ids: Node.all().map { it.id } } }.snapshot.ids.map { Node { id: it } }.filter { it.id > 1 }`,
		`val rows = Node.all() val box = Box { rows: [] } if true { box.rows = rows } return rows`,
		`val rows = Node.all() val snapshot = Snapshot { ids: [] } snapshot.ids = rows.map { it.id } snapshot.ids.size() > 1 !: throw error.Conflict return rows`,
	} {
		input := analyzePermissionFixture(t, `model Node @noTime { id: Int @id } type Box { rows: [Node] } type Snapshot { ids: [Int] } type Outer { snapshot: Snapshot } event Counted(count: Int) api page(): [Node] { `+body+` }`)
		if len(input.Errors) != 0 {
			t.Fatal(input.Errors)
		}
		input.Files[0].APIs[0].Directives = append(input.Files[0].APIs[0].Directives, &ast.Directive{Name: "paginate"})
		ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Node": {"id": 1}, "Box": {"rows": 1}, "Snapshot": {"ids": 1}, "Outer": {"snapshot": 1}}, EventFields: map[string]map[string]int{"Counted": {"count": 1}}})
		generator := mustNewGenerator(t, GeneratorConfig{Driver: DriverPG, IDs: ids})
		output, err := generator.Generate(input, "generated", nil)
		if err == nil || output != nil || !strings.Contains(err.Error(), "cannot safely push down pagination") {
			t.Fatalf("formal generation accepted unsafe plan: %v", err)
		}
	}
}

func TestPaginationPlansDoNotLeakAcrossGeneration(t *testing.T) {
	input := analyzePermissionFixture(t, `model Node @noTime { id: Int @id }
api paged(): [Node] @paginate { return Node.all() }
api ordinary(): [Node] { return Node.all() }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Node": {"id": 1}}})
	generator := mustNewGenerator(t, GeneratorConfig{Driver: DriverPG, IDs: ids})
	first, err := generator.Generate(input, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(first.Files["handler.gen.go"]), ".AllWithCount(ctx)"); count != 1 {
		t.Fatalf("unpaged API inherited SQL pagination: %d", count)
	}
	input.Files[0].APIs[0].Directives = nil
	second, err := generator.Generate(input, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(second.Files["handler.gen.go"]), ".AllWithCount(ctx)") {
		t.Fatal("old pagination plan survived generator reuse")
	}
}

func TestCompileTerminalResultRejectsUnknownMethod(t *testing.T) {
	compiler := newCompiler(makeModels("Node"))
	var output strings.Builder
	output.WriteString("existing")
	if compiler.compileTerminalResult(&output, "Node", chainLink{method: "unknown"}) || output.String() != "existing" {
		t.Fatal("unknown terminal mutated generated query")
	}
}

func TestPaginationObjectFieldsPreserveSQLWindow(t *testing.T) {
	source := `model Node @noTime { id: Int @id name: String }
type Box { rows: [Node] }
type Outer { box: Box }
model Holder @noTime { id: Int @id rows: [Node] @by(id, id) parent: Holder? @by(id, id) }
api plain(): [Node] @paginate { return Node.select().orderBy(id.asc).all() }
api directBox(): [Node] @paginate { return Box { rows: Node.select().orderBy(id.asc).all() }.rows }
api aliasBox(): [Node] @paginate { val box = Box { rows: Node.select().orderBy(id.asc).all() } return box.rows }
api nestedBox(): [Node] @paginate { val box = Outer { box: Box { rows: Node.select().orderBy(id.asc).all() } } return box.box.rows }
api assignedBox(): [Node] @paginate { val box = Box { rows: [] } box.rows = Node.select().orderBy(id.asc).all() return box.rows }
api copiedBox(): [Node] @paginate { val box = Box { rows: Node.select().orderBy(id.asc).all() } val alias = box box.rows = [] return alias.rows }
api nestedCopy(): [Node] @paginate { val outer = Outer { box: Box { rows: Node.select().orderBy(id.asc).all() } } val alias = outer.box outer.box.rows = [] return alias.rows }
api modelImplicitReference(): [Node] @paginate { val holder = Holder { id: 1, rows: Node.select().orderBy(id.asc).all() } val alias = holder holder.rows = [] return alias.rows }
api modelReference(): [Node] @paginate { val holder: Holder = Holder { id: 1, rows: [] } val alias = holder holder.rows = Node.select().orderBy(id.asc).all() return alias.rows }
api modelCycle(): [Node] @paginate { val holder: Holder = Holder { id: 1, rows: Node.select().orderBy(id.asc).all() } holder.parent = holder return holder.parent?.rows ?: [] }
fn buildHolder(): Holder { return Holder { id: 1, rows: [] } }
api helperReference(): [Node] @paginate { val holder = buildHolder() val alias = holder holder.rows = Node.select().orderBy(id.asc).all() return alias.rows }
api nullableCopy(): [Node] @paginate { val box: Box? = Box { rows: Node.select().orderBy(id.asc).all() } val alias = box ?: throw error.NotFound alias.rows = [] return (box ?: throw error.NotFound).rows }
api nullableAssigned(): [Node] @paginate { var box: Box? = Box { rows: [] } box = Box { rows: Node.select().orderBy(id.asc).all() } val alias = box val present = box ?: throw error.NotFound present.rows = [] return (alias ?: throw error.NotFound).rows }
api nestedStorage(): [Node] @paginate { val outer = Outer { box: Box { rows: Node.select().orderBy(id.asc).all() } } val alias = outer outer.box.rows = [] return alias.box.rows }
api modelReferenceEmpty(): [Node] @paginate { val holder: Holder = Holder { id: 1, rows: Node.select().orderBy(id.asc).all() } val alias = holder holder.rows = [] return alias.rows }
api inputAlias(input: Outer): [Node] @paginate { val alias = input input.box.rows = Node.select().orderBy(id.asc).all() return alias.box.rows }
api inputCopy(input: Outer): [Node] @paginate { val alias = input.box input.box.rows = Node.select().orderBy(id.asc).all() return alias.rows }
api inputModelAlias(holder: Holder): [Node] @paginate { val alias = holder holder.rows = Node.select().orderBy(id.asc).all() return alias.rows }
api inputModelRebind(holder: Holder): [Node] @paginate { val alias = holder holder = Holder { id: 2, rows: Node.select().orderBy(id.asc).all() } return alias.rows }
api storedCopy(): [Node] @paginate { val rows = Node.select().orderBy(id.asc).all() val box = Box { rows: [] } box.rows = rows return rows }
api manualBox(): [Node] @paginate { val box = Box { rows: [Node { id: 8, name: "eight" }, Node { id: 9, name: "nine" }] } return box.rows }
`
	prefix, _, ok := strings.Cut(paginatedLoopAssertions, "func TestPaginatedLoopCount")
	if !ok {
		t.Fatal("missing unique shared assertion header")
	}
	prefix = strings.Replace(prefix, `;"github.com/light-speak/luxo/pkg/lux/event"`, "", 1)
	_, suffix, ok := strings.Cut(paginatedLoopAssertions, "func callPage")
	if !ok {
		t.Fatal("missing shared protocol assertions")
	}
	suffix = strings.Replace(suffix, "body,err:=api.EncodeBinaryRequest(definition.ID,mask,params,", "body,err:=api.EncodeBinaryRequest(definition.ID,mask,objectPageWireParams(t,router,sample.name,params),", 1)
	suffix += objectPageInputAssertions
	test := `func TestObjectSourceSQL(t *testing.T){
 connection:=pageFixtureConnection(t);cfg,err:=pgxpool.ParseConfig(connection);if err!=nil{t.Fatal(err)};log:=&pageSQLLog{};cfg.ConnConfig.Tracer=log
 pool,err:=pgxpool.NewWithConfig(t.Context(),cfg);if err!=nil{t.Fatal(err)};db:=pg.NewDBFromPool(pool);t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE nodes(id bigint PRIMARY KEY,name text NOT NULL);INSERT INTO nodes VALUES(1,'one'),(2,'two'),(3,'three')");err!=nil{t.Fatal(err)}
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,NewFromDB(db));err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}();defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}();client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for _,sample:=range []pageCase{{"plain",nil,[]int64{1,2,3},2},{"directBox",nil,[]int64{1,2,3},2},{"aliasBox",nil,[]int64{1,2,3},2},{"nestedBox",nil,[]int64{1,2,3},2},{"assignedBox",nil,[]int64{1,2,3},2},{"copiedBox",nil,[]int64{1,2,3},2},{"nestedCopy",nil,[]int64{1,2,3},2},{"modelImplicitReference",nil,nil,1},{"modelReference",nil,[]int64{1,2,3},2},{"modelCycle",nil,[]int64{1,2,3},2},{"helperReference",nil,[]int64{1,2,3},2},{"nullableCopy",nil,[]int64{1,2,3},2},{"nullableAssigned",nil,[]int64{1,2,3},2},{"nestedStorage",nil,nil,1},{"modelReferenceEmpty",nil,nil,1},{"inputAlias",map[string]any{"input":map[string]any{"box":map[string]any{"rows":[]any{}}}},[]int64{1,2,3},2},{"inputCopy",map[string]any{"input":map[string]any{"box":map[string]any{"rows":[]any{}}}},nil,1},{"inputModelAlias",map[string]any{"holder":map[string]any{"id":int64(1),"rows":[]any{},"parent":nil}},[]int64{1,2,3},2},{"inputModelRebind",map[string]any{"holder":map[string]any{"id":int64(1),"rows":[]any{},"parent":nil}},nil,1},{"storedCopy",nil,[]int64{1,2,3},2},{"manualBox",nil,[]int64{8,9},0}}{for _,page:=range []int{1,2,5}{for mode:=0;mode<3;mode++{t.Run(fmt.Sprintf("%s/page=%d/mode=%d",sample.name,page,mode),func(t *testing.T){
  log.take();body:=callPage(t,router,client,sample,page,mode);assertPage(t,body,sample.ids,page);queries:=log.take();t.Logf("exact SQL: %q",queries)
  if len(queries)!=sample.queries{t.Errorf("wrapper lost SQL pagination: expected %d statements, got %d",sample.queries,len(queries))}
  for _,query:=range queries{upper:=strings.ToUpper(query);if !strings.Contains(upper,"COUNT(")&&!strings.Contains(upper," LIMIT $")&&sample.queries!=1{t.Error("wrapper performed a full list read",query)};if !strings.Contains(upper,"COUNT(")&&(strings.Contains(query,"*")||strings.Contains(query,"name")){t.Error("explicit selection lost in SQL",query)}}
 })}}}
}

`
	runGeneratedFixture(t, source, prefix+test+"func callPage"+suffix, StableIDs{ModelFields: map[string]map[string]int{"Node": {"id": 1, "name": 2}, "Box": {"rows": 1}, "Outer": {"box": 1}, "Holder": {"id": 1, "rows": 2, "parent": 3}}})
}

const objectPageInputAssertions = `
// The generic test caller accepts pre-encoded structured Binary parameters.
// Production handlers still use the generated input reader directly.
func objectPageWireParams(t *testing.T,router *api.Router,name string,params map[string]any)map[string]any{
 t.Helper();wire:=make(map[string]any,len(params));for key,value:=range params{wire[key]=value}
 for _,param:=range router.Registry.ParamOrder(name){if param.Type!="Model"{continue};raw,err:=json.Marshal(params[param.Name]);if err!=nil{t.Fatal(err)};body,err:=router.Schema.JSONToBinary(param.TypeName,raw);if err!=nil{t.Fatal(err)};wire[param.Name]=api.BinaryMessage(body)}
 return wire
}
`
