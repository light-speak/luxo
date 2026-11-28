package codegen

import (
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
)

func loadDemandFixture(t *testing.T, body, resultType string) *GenerateResult {
	t.Helper()
	return loadDemandModuleFixture(t, `api lookup(id: Int): `+resultType+` { `+body+` }
fn identity(value: Product): Product { return value }`)
}

func loadDemandModuleFixture(t *testing.T, declarations string) *GenerateResult {
	t.Helper()
	var files []*ast.File
	for _, source := range []struct{ name, text string }{
		{"origin/product/model.luxo", `model Product @noTime { id: Int @id name: String price: Int }`},
		{"origin/order/api.luxo", `extend Product { name: String price: Int }
` + declarations},
	} {
		tokens, lexErrors := lexer.New(source.text, source.name).Tokenize()
		if len(lexErrors) != 0 {
			t.Fatal(lexErrors)
		}
		file, parseErrors := parser.New(tokens).Parse(source.name)
		if len(parseErrors) != 0 {
			t.Fatal(parseErrors)
		}
		files = append(files, file)
	}
	input := semantic.AnalyzeModules(files)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	input.Files = input.Files[1:]
	ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Product": {"id": 1, "name": 2, "price": 3}}, APIs: map[string]int{"svc:batchLoad:Product": 100}, APIParams: map[string]map[string]int{"svc:batchLoad:Product": {"keys": 1}}})
	generator := mustNewGenerator(t, GeneratorConfig{Events: BuildEventContext(files, "example.com/app"), IDs: ids})
	generated, err := generator.Generate(input, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	return generated
}

func TestGeneratedRemoteLoadDemand(t *testing.T) {
	for name, body := range map[string]string{
		"chain_filter_map":    `val product = Product.load(id) ?: throw error.NotFound val rows = [product] return rows.filter { it.price > 0 }.map { it.name }`,
		"chain_filter_alias":  `val product = Product.load(id) ?: throw error.NotFound val rows = [product] val selected = rows.filter { it.price > 0 } return selected.map { it.name }`,
		"named_it_capture":    `val it = Product.load(id) ?: throw error.NotFound return [1].map { number -> it.name }`,
		"direct_field":        `val product = (Product.load(id) ?: throw error.NotFound) return product.name`,
		"statement_guard":     `val product = Product.load(id) product ?: throw error.NotFound return product.name`,
		"binding_collision":   `val _loadFields0 = id val product = Product.load(_loadFields0) ?: throw error.NotFound return product.name`,
		"direct_member":       `return (Product.load(id) ?: throw error.NotFound).name`,
		"alias_field":         `val product = (Product.load(id) ?: throw error.NotFound) val alias = product return alias.name`,
		"returned_model":      `val product = (Product.load(id) ?: throw error.NotFound) return product`,
		"returned_alias":      `val product = (Product.load(id) ?: throw error.NotFound) val alias = product return alias`,
		"returned_direct":     `return (Product.load(id) ?: throw error.NotFound)`,
		"returned_implicit":   `val product = (Product.load(id) ?: throw error.NotFound) product`,
		"returned_nullable":   `val product = Product.load(id) return product`,
		"returned_loop_model": `val product = Product.load(id) ?: throw error.NotFound return for value in [1] { product }`,
		"returned_loop_yield": `val product = Product.load(id) ?: throw error.NotFound return for value in [1] { if value == 1 { yield product } }`,
		"returned_list_model": `val product = Product.load(id) ?: throw error.NotFound return [product]`,
		"returned_with_logic": `val product = (Product.load(id) ?: throw error.NotFound) if product.price < 0 { throw error.NotFound } return product`,
		"returned_many_logic": `val product = (Product.load(id) ?: throw error.NotFound) if product.price < 0 || product.name == "" { throw error.NotFound } return product`,
		"nested_alias":        `val product = (Product.load(id) ?: throw error.NotFound) if id > 0 { val alias = product return alias.name } return ""`,
		"shadow":              `val product = (Product.load(id) ?: throw error.NotFound) if id > 0 { val product = (Product.load(id + 1) ?: throw error.NotFound) if product.price < 0 { throw error.NotFound } } return product.name`,
		"opaque_function":     `val product = (Product.load(id) ?: throw error.NotFound) return identity(product).name`,
		"mutation":            `var product = (Product.load(id) ?: throw error.NotFound) product = (Product.load(id + 1) ?: throw error.NotFound) return product.name`,
		"when_capture":        `val it = (Product.load(id) ?: throw error.NotFound) return when { true -> { it.name } else -> "" }`,
	} {
		t.Run(name, func(t *testing.T) {
			resultType := "String"
			if strings.HasPrefix(name, "chain_") || name == "named_it_capture" {
				resultType = "[String]"
			}
			if strings.HasPrefix(name, "returned_") {
				resultType = "Product"
			}
			if name == "returned_nullable" {
				resultType = "Product?"
			}
			if name == "returned_loop_model" || name == "returned_list_model" {
				resultType = "[Product]"
			} else if name == "returned_loop_yield" {
				resultType = "Product?"
			}
			expected := "name"
			if name == "returned_with_logic" || name == "returned_many_logic" {
				expected = "name,price"
			}
			if name == "opaque_function" || name == "mutation" || strings.HasPrefix(name, "chain_") {
				expected = "name,price"
			}
			assertions := strings.ReplaceAll(loadDemandAssertions, "FIELDS_REQUIRED", strconv.Quote(expected))
			assertions = strings.ReplaceAll(assertions, "RETURN_MODEL", strconv.FormatBool(strings.HasPrefix(resultType, "Product") || resultType == "[Product]"))
			wanted := "wanted"
			assertions = strings.ReplaceAll(assertions, "VALUE_REQUIRED", strconv.Quote(wanted))
			runGeneratedCompilation(t, loadDemandFixture(t, body, resultType), assertions, loadDemandRPCAssertions)
		})
	}
}

const loadDemandAssertions = `package generated
import("bytes";"context";"encoding/json";"errors";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/dataloader";"github.com/light-speak/luxo/pkg/lux/selection")
func TestSelectedLoadData(t *testing.T){
 app:=&App{};cfg:=dataloader.DefaultConfig();cfg.MaxBatch=1
 app.SetLoaders(NewLoaders(cfg,func(_ context.Context,keys []int64,fields []*selection.Field)(map[int64]*Product,error){
  expected:=FIELDS_REQUIRED;if keys[0]==43&&expected=="name"{expected="price"}
  if fields==nil||selection.Format(fields)!=expected{t.Fatalf("wrong projection: key=%d fields=%s want=%s",keys[0],selection.Format(fields),expected)}
  row:=&Product{Id:keys[0]}
  if fields==nil{row.Name="wanted";row.Price=99}
  for _,field:=range fields{if field.Name=="name"{row.Name="wanted"};if field.Name=="price"{row.Price=99}}
  return map[int64]*Product{keys[0]:row},nil
 }))
 assertDemandResult(t,app)
}
func TestLoadFailureHasNoPartialResult(t *testing.T){
 failure:=errors.New("load failed");app:=&App{};cfg:=dataloader.DefaultConfig();cfg.MaxBatch=1
 app.SetLoaders(NewLoaders(cfg,func(context.Context,[]int64,[]*selection.Field)(map[int64]*Product,error){return nil,failure}))
 req:=&api.Request{Params:map[string]json.RawMessage{"id":json.RawMessage("42")},Buf:&api.ResponseBuf{}}
 if err:=handleLookup(app)(t.Context(),req);!errors.Is(err,failure)||len(req.Buf.B)!=0{t.Fatal("load failure produced success or partial data",err,req.Buf.B)}
}
func assertDemandResult(t *testing.T,app *App){
 t.Helper()
 req:=&api.Request{Params:map[string]json.RawMessage{"id":json.RawMessage("42")},Buf:&api.ResponseBuf{}}
 if RETURN_MODEL {req.Select=[]*selection.Field{{Name:"name"}};mask,err:=codec.FieldMaskSet(nil,2);if err!=nil{t.Fatal(err)};mask,err=codec.AppendSelectionMask(nil,mask,nil);if err!=nil{t.Fatal(err)};req.FieldMask=mask}
 if err:=handleLookup(app)(t.Context(),req);err!=nil{t.Fatal(err)}
 if !bytes.Contains(req.Buf.B,[]byte(VALUE_REQUIRED)){t.Fatalf("requested/business field was silently lost: %x",req.Buf.B)}
}
`

func TestLoadProjectionScopesAndExpressions(t *testing.T) {
	for _, sample := range []struct {
		name, body, result, fields string
		client                     bool
	}{
		{"no_load", `return "x"`, "String", "", false},
		{"list", `val p = (Product.load(id) ?: throw error.NotFound) return [p.name]`, "[String]", "name", false},
		{"list_returned_model", `val p = Product.load(id) ?: throw error.NotFound return [p]`, "[Product]", "", true},
		{"template", `val p = (Product.load(id) ?: throw error.NotFound) return "${p.name}"`, "String", "name", false},
		{"negation", `val p = (Product.load(id) ?: throw error.NotFound) return !p.flag`, "Boolean", "flag", false},
		{"elvis", `val p: Product? = Product.load(id) return p?.name ?: "none"`, "String", "name", false},
		{"statement_guard", `val p = Product.load(id) p ?: throw error.NotFound return p.name`, "String", "name", false},
		{"null_guard", `val p = Product.load(id) if p != null { return p.name } return ""`, "String", "name", false},
		{"discarded", `Product.load(id) return 0`, "Int", "", false},
		{"when", `val p = (Product.load(id) ?: throw error.NotFound) return when(p.price) { 0 -> p.name else -> "x" }`, "String", "name,price", false},
		{"when_model", `val p = (Product.load(id) ?: throw error.NotFound) return when { p.flag -> p else -> p }`, "Product", "flag", true},
		{"loop", `val rows = Product.load(id: id) var name = "" for p in rows { name = p.name } return name`, "String", "name", false},
		{"loop_value", `val rows = Product.load(id: id) return for p in rows { p.name }`, "[String]", "name", false},
		{"loop_value_return", `val rows = Product.load(id: id) val names = for p in rows { return p.name } return "done"`, "String", "name", false},
		{"loop_returned_model", `val rows = Product.load(id: id) return for p in rows { p }`, "[Product]", "", true},
		{"loop_yielded_model", `val rows = Product.load(id: id) return for p in rows { if p.flag { yield p } }`, "Product?", "flag", true},
		{"range", `val p = (Product.load(id) ?: throw error.NotFound) var total = 0 for number in p.id..p.price { total += number } return total`, "Int", "id,price", false},
		{"closure", `val p = (Product.load(id) ?: throw error.NotFound) [1].forEach { number -> if p.flag { p.name.d } } return 0`, "Int", "name,flag", false},
		{"method_field_collision", `val rows = Product.load(id: id) return rows.map { it.name }.joinToString(",")`, "String", "id,name,price,flag", false},
		{"closure_shadow", `val p = (Product.load(id) ?: throw error.NotFound) [0].forEach { p -> "inner".d } return p.price`, "Int", "price", false},
		{"transaction", `return transaction { val p = (Product.load(id) ?: throw error.NotFound) p.name }`, "String", "name", false},
		{"async", `val p = (Product.load(id) ?: throw error.NotFound) async { p.name.d } return 0`, "Int", "name", false},
		{"throw", `val p = (Product.load(id) ?: throw error.NotFound) if p.flag { throw error.NotFound } return p.name`, "String", "name,flag", false},
		{"emit", `val p = (Product.load(id) ?: throw error.NotFound) emit Read(name: p.name) return 0`, "Int", "name", false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			input := analyzePermissionFixture(t, `model Product @noTime { id: Int @id name: String price: Int flag: Boolean map: String }
event Read(name: String)
api lookup(id: Int): `+sample.result+` { `+sample.body+` }`)
			if len(input.Errors) != 0 {
				t.Fatal(input.Errors)
			}
			models, _ := collectInferredAPIs(input)
			api := input.Files[0].APIs[0]
			projections := analyzeLoadProjections(api.Body, models, api.ReturnType)
			if sample.name == "no_load" {
				if projections != nil {
					t.Fatal(projections)
				}
				return
			}
			if len(projections) != 1 {
				t.Fatalf("projection count: %d", len(projections))
			}
			for _, projection := range projections {
				var fields []string
				for _, name := range []string{"id", "name", "price", "flag"} {
					if strings.Contains(projection.logic, `Name: "`+name+`"`) {
						fields = append(fields, name)
					}
				}
				if strings.Join(fields, ",") != sample.fields || projection.client != sample.client {
					t.Fatalf("projection=%+v fields=%v want=%s client=%v", projection, fields, sample.fields, sample.client)
				}
			}
		})
	}
}

func TestSelectedQueryBusinessDemand(t *testing.T) {
	for _, body := range []string{
		`val rows = Product.select().all() return rows.filter { it.price > 0 }`,
		`return Product.select().all().filter { row -> row.price > 0 }`,
		`val query = Product.select() val rows = query.all() return rows.filter { it.price > 0 }`,
	} {
		input := analyzePermissionFixture(t, `model Product @noTime { id: Int @id name: String price: Int description: String }
api priced(): [Product] { `+body+` }`)
		if len(input.Errors) != 0 {
			t.Fatal(input.Errors)
		}
		var out strings.Builder
		compileAPIBody(&out, input.Files[0].APIs[0], map[string]*ast.ModelDecl{"Product": input.Files[0].Models[0]}, nil)
		if !strings.Contains(out.String(), `selectProductSQLColumns(req.Select, "price")`) {
			t.Fatalf("business predicate field omitted from selected SQL: %s", out.String())
		}
	}
}

func TestSelectedQueryProjectionBoundaries(t *testing.T) {
	for _, sample := range []struct {
		name, body, result string
		fields             []string
		client             bool
	}{
		{"public", `return Product.select().all()`, "[Product]", nil, true},
		{"scalar_map", `return Product.select().all().filter { it.price > 0 }.map { it.name }`, "[String]", []string{"price", "name"}, false},
		{"map_alias", `val rows = Product.select().all() val alias = rows.map { row -> row } return alias.filter { it.price > 0 }`, "[Product]", []string{"price"}, true},
		{"sort", `return Product.select().all().sortBy { it.price }`, "[Product]", []string{"price"}, true},
		{"any", `return Product.select().all().any { it.price > 0 }`, "Boolean", []string{"price"}, false},
		{"sum", `return Product.select().all().sumOf { it.price }`, "Int", []string{"price"}, false},
		{"query_count", `return Product.select().count()`, "Int", nil, false},
		{"selected_builder", `val query = Product.where(id: 1) return query.select().all()`, "[Product]", nil, true},
		{"first", `return Product.select().first() ?: throw error.NotFound`, "Product", nil, true},
		{"captured", `val rows = Product.select().all() return [1].map { rows.sumOf { row -> row.price } }`, "[Int]", []string{"price"}, false},
		{"named_capture_it", `val it = Product.select().first() ?: throw error.NotFound return [1].map { number -> it.name }`, "[String]", []string{"name"}, false},
		{"nested_named_capture_it", `return Product.select().all().filter { [0].any { number -> it.price > number } }`, "[Product]", []string{"price"}, true},
		{"explicit_it_shadow", `val it = Product.select().first() ?: throw error.NotFound if it.id < 0 { throw error.NotFound } return [1].any { it -> it > 0 }`, "Boolean", []string{"id"}, false},
		{"implicit_it_shadow", `val it = Product.select().first() ?: throw error.NotFound if it.id < 0 { throw error.NotFound } return [1].any { it > 0 }`, "Boolean", []string{"id"}, false},
		{"builder_chain", `val query = Product.select() return query.limit(3).all().filter { it.price > 0 }`, "[Product]", []string{"price"}, true},
		{"opaque", `return identity(Product.select().first() ?: throw error.NotFound)`, "Product", []string{"id", "name", "price", "secret"}, false},
		{"loop", `val rows = Product.select().all() return for row in rows { if row.price > 0 { yield row } }`, "Product?", []string{"price"}, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			input := analyzePermissionFixture(t, `model Product @noTime { id: Int @id name: String price: Int secret: String @hidden }
fn identity(value: Product): Product { return value }
api lookup(): `+sample.result+` { `+sample.body+` }`)
			if len(input.Errors) != 0 {
				t.Fatal(input.Errors)
			}
			declaration := input.Files[0].APIs[0]
			projections := analyzeSelectedQueryProjections(declaration.Body, map[string]*ast.ModelDecl{"Product": input.Files[0].Models[0]}, declaration.ReturnType)
			if len(projections) != 1 {
				t.Fatalf("projections=%d", len(projections))
			}
			var paths [][]string
			for _, field := range sample.fields {
				paths = append(paths, []string{field})
			}
			for _, projection := range projections {
				if projection.client != sample.client || projection.logic != compileSelectionLiteral(paths) {
					t.Fatalf("projection=%+v, want client=%v fields=%v", projection, sample.client, sample.fields)
				}
			}
		})
	}
}

func TestSelectedQueryPrivateBusinessField(t *testing.T) {
	input := analyzePermissionFixture(t, `model Product @noTime { id: Int @id name: String secret: Int @hidden }
api lookup(): [Product] { return Product.select().all().filter { it.secret > 0 } }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	var out strings.Builder
	compileAPIBody(&out, input.Files[0].APIs[0], map[string]*ast.ModelDecl{"Product": input.Files[0].Models[0]}, nil)
	if !strings.Contains(out.String(), `(*Product)(nil).loadSQLColumns(req.Select, "secret")`) {
		t.Fatalf("private business field was discarded by public SQL selector: %s", out.String())
	}
}

func TestSelectedQueriesKeepSeparateDemand(t *testing.T) {
	input := analyzePermissionFixture(t, `model Product @noTime { id: Int @id name: String price: Int }
api lookup(): [Product] {
 val rows = Product.select().all()
 val prices = rows.map { it.price }
 val other = Product.select().all()
 return other.filter { it.name != "" }
}`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	api := input.Files[0].APIs[0]
	projections := analyzeSelectedQueryProjections(api.Body, map[string]*ast.ModelDecl{"Product": input.Files[0].Models[0]}, api.ReturnType)
	var ordered []loadProjection
	ast.WalkExprs(api.Body, func(expr ast.Expr) {
		if projection, ok := projections[expr]; ok {
			ordered = append(ordered, projection)
		}
	})
	if len(ordered) != 2 || ordered[0].client || ordered[0].singleColumn() != "price" || !ordered[1].client || ordered[1].singleColumn() != "name" {
		t.Fatalf("separate query projections contaminated each other: %+v", ordered)
	}
}

func TestSelectedQueryCallBusinessDemand(t *testing.T) {
	input := analyzePermissionFixture(t, `model Product @noTime { id: Int @id name: String price: Int }
fn stored(): [Product] { return Product.select().all() }
api lookup(): [Product] { return stored().filter { it.price > 0 } }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Product": {"id": 1, "name": 2, "price": 3}}})})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated.Files["handler.gen.go"]), `app.stored(ctx, selection.EnsureField(req.Select, "price"))`) {
		t.Fatalf("local function lost caller business field demand: %s", generated.Files["handler.gen.go"])
	}
}

func TestSelectedQueryFunctionKeepsPrivateDemand(t *testing.T) {
	input := analyzePermissionFixture(t, `model Product @noTime { id: Int @id name: String secret: Int @hidden }
fn stored(): [Product] { return Product.select().all() }
api lookup(): [Product] { return stored().filter { it.secret > 0 } }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Product": {"id": 1, "name": 2, "secret": 3}}})})
	if err != nil {
		t.Fatal(err)
	}
	var code strings.Builder
	for _, data := range generated.Files {
		code.Write(data)
	}
	if !strings.Contains(code.String(), `(*Product)(nil).loadSQLColumns(_select)`) {
		t.Fatalf("function SQL selector discarded caller private demand: %s", generated.Files["app.gen.go"])
	}
}

func TestSelectedQueryClosureKeepsPrivateDemand(t *testing.T) {
	input := analyzePermissionFixture(t, `model Product @noTime { id: Int @id name: String secret: Int @hidden }
fn nestedStored(): [Product] { return [1].map { number -> Product.select().first() ?: throw error.NotFound } }
api lookup(): [Product] { return nestedStored().filter { it.secret > 0 } }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Product": {"id": 1, "name": 2, "secret": 3}}})
	generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	code := string(generated.Files["handler.gen.go"])
	if !strings.Contains(code, `loadSQLColumns(_select)`) {
		t.Fatalf("value closure discarded caller private demand: %s", code)
	}
}

func TestGeneratedQueryClosurePrivateDemand(t *testing.T) {
	runGeneratedFixture(t, `model Product @noTime { id: Int @id name: String secret: Int @hidden }
fn nestedStored(): [Product] { return [1].map { number -> Product.select().first() ?: throw error.NotFound } }
api lookup(): [Product] { return nestedStored().filter { it.secret > 0 } }`, queryClosurePrivateAssertions,
		StableIDs{ModelFields: map[string]map[string]int{"Product": {"id": 1, "name": 2, "secret": 3}}})
}

const queryClosurePrivateAssertions = `package generated
import("bytes";"encoding/json";"errors";"net";"net/http/httptest";"testing";"github.com/light-speak/luxo/internal/testutil";"github.com/light-speak/luxo/pkg/lux/api";luxerrors "github.com/light-speak/luxo/pkg/lux/errors";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/light-speak/luxo/pkg/lux/rpc";"github.com/light-speak/luxo/pkg/lux/schema";"github.com/light-speak/luxo/pkg/lux/selection";"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres")
func TestClosurePrivate(t *testing.T){
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies());testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)};db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE products(id bigint PRIMARY KEY,name text,secret bigint);INSERT INTO products VALUES(1,'wanted',7)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 fields:=[]*selection.Field{{Name:"name"}};mask,err:=schema.SelectToFieldMask(fields,router.Schema.Models["Product"],router.Schema);if err!=nil{t.Fatal(err)}
 definition:=router.Schema.APIs["lookup"];wire,err:=api.EncodeBinaryRequest(definition.ID,mask,nil,router.Registry.ParamOrder("lookup"));if err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}();defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}();client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 for mode:=0;mode<3;mode++{
  var raw []byte
  if mode==2{raw,err=client.CallWithMaskContext(t.Context(),"",definition.ID,mask,nil);if err!=nil{t.Fatal(err)}}else{
   body:=[]byte("{\"$api\":\"lookup\",\"$select\":\"name\"}");if mode==1{body=wire};request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));if mode==1{request.Header.Set("X-Luxo-Mode","binary")};response:=httptest.NewRecorder();router.ServeHTTP(response,request);if response.Code!=200{t.Fatalf("mode=%d status=%d %s",mode,response.Code,response.Body)};raw=response.Body.Bytes()
  }
  if mode==0{var document struct{Data json.RawMessage};if err:=json.Unmarshal(raw,&document);err!=nil{t.Fatal(err)};raw=document.Data}else{raw,err=router.StreamPayloadJSON("lookup",raw);if err!=nil{t.Fatal(err)}}
  if string(raw)!="[{\"name\":\"wanted\"}]"{t.Fatalf("mode=%d caller private field lost or leaked: %s",mode,raw)}
 }
 if _,err:=pg.Exec(t.Context(),db,"DELETE FROM products");err!=nil{t.Fatal(err)}
 for _,binary:=range []bool{false,true}{
  body:=[]byte("{\"$api\":\"lookup\",\"$select\":\"name\"}");if binary{body=wire};request:=httptest.NewRequest("POST","/luvia",bytes.NewReader(body));if binary{request.Header.Set("X-Luxo-Mode","binary")};response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if response.Code!=404||bytes.Contains(response.Body.Bytes(),[]byte("\"data\"")){t.Fatalf("closure lost error return ABI: binary=%v status=%d %s",binary,response.Code,response.Body)}
 }
 raw,err:=client.CallWithMaskContext(t.Context(),"",definition.ID,mask,nil);var failure *luxerrors.AppError;if !errors.As(err,&failure)||failure.Code!=404||len(raw)!=0{t.Fatalf("LRPC closure lost NotFound: data=%x err=%v",raw,err)}
}
`

func TestUnrelatedQueryAnalysisDoesNotAllocate(t *testing.T) {
	input := analyzePermissionFixture(t, `model Product @noTime { id: Int @id name: String }
api plain(): Int { return 1 }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	api := input.Files[0].APIs[0]
	models := map[string]*ast.ModelDecl{"Product": input.Files[0].Models[0]}
	allocations := testing.AllocsPerRun(100, func() {
		analyzeSelectedQueryProjections(api.Body, models, api.ReturnType)
		analyzeQueryRelations(api.Body, api.ReturnType, models, nil)
	})
	if allocations != 0 {
		t.Fatalf("unrelated scalar body allocated selection metadata: %g", allocations)
	}
}

func TestQueryProjectionUnionsColumnsWithoutTreeCopies(t *testing.T) {
	input := analyzePermissionFixture(t, `model Product @noTime { id: Int @id name: String price: Int secret: Int @hidden }
api lookup(): [Product] { return Product.select().all().filter { it.price > 0 && it.secret > 0 } }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Product": {"id": 1, "name": 2, "price": 3, "secret": 4}}})
	generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	code := string(generated.Files["handler.gen.go"])
	if !strings.Contains(code, `loadSQLColumns(req.Select, "price", "secret")`) || strings.Contains(code, `selection.Merge(req.Select`) {
		t.Fatalf("SQL-only demand still copies selection trees: %s", code)
	}
}

func TestQueryColumnUnionReusesModelMapping(t *testing.T) {
	for _, hidden := range []string{"", "@hidden"} {
		input := analyzePermissionFixture(t, `model Product @noTime { id: Int @id name: String price: Int secret: Int `+hidden+` }
api lookup(): [Product] { return Product.select().all().filter { it.price > 0 && it.secret > 0 } }`)
		if len(input.Errors) != 0 {
			t.Fatal(input.Errors)
		}
		ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Product": {"id": 1, "name": 2, "price": 3, "secret": 4}}})
		generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: ids})
		if err != nil {
			t.Fatal(err)
		}
		code := string(generated.Files["handler.gen.go"])
		want := 1
		if hidden != "" {
			want = 2
		}
		if got := strings.Count(code, `case "price":`); got != want {
			t.Fatalf("generated %d field mappings, want %d", got, want)
		}
	}
}

func TestQueryColumnUnionGeneratorReuse(t *testing.T) {
	const model = `model Product @noTime { id: Int @id name: String price: Int }`
	selected := analyzePermissionFixture(t, model+` api lookup(): [Product] { return Product.select().all().filter { it.price > 0 } }`)
	plain := analyzePermissionFixture(t, model+` api lookup(): [Product] { return Product.select().all() }`)
	if len(selected.Errors) != 0 || len(plain.Errors) != 0 {
		t.Fatal(selected.Errors, plain.Errors)
	}
	ids := completeFixtureAPIIDs(selected, StableIDs{ModelFields: map[string]map[string]int{"Product": {"id": 1, "name": 2, "price": 3}}})
	generator := mustNewGenerator(t, GeneratorConfig{Driver: DriverPG, IDs: ids})
	first, err := generator.Generate(selected, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := generator.Generate(plain, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(unrelated.Files["handler.gen.go"]), "required ...string") {
		t.Fatal("a previous query changed unrelated generated code")
	}
	again, err := generator.Generate(selected, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range first.Files {
		if string(data) != string(again.Files[name]) {
			t.Fatalf("generator reuse changed %s", name)
		}
	}
}

func TestQueryColumnUnionKeepsIndependentQueries(t *testing.T) {
	input := analyzePermissionFixture(t, `model Product @noTime { id: Int @id name: String price: Int }
api lookup(): Int {
 val names = Product.select().all().map { it.name }
 val prices = Product.select().all().map { it.price }
 return names.size + prices.size
}`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Product": {"id": 1, "name": 2, "price": 3}}})
	generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	code := string(generated.Files["handler.gen.go"])
	for _, field := range []string{"name", "price"} {
		if !strings.Contains(code, `selectProductSQLColumns([]*selection.Field{}, "`+field+`")`) {
			t.Fatalf("independent query lost %s: %s", field, code)
		}
	}
	if strings.Count(code, "func selectProductSQLColumns(") != 1 {
		t.Fatal("duplicate SQL column union helper", code)
	}
}

func TestQueryColumnUnionStandaloneCompiler(t *testing.T) {
	var compiler compiler
	projection := loadProjection{client: true, columns: [][]string{{"price"}}}
	if got := compiler.compiledQueryColumns("Product", projection, "req.Select"); got != `selectProductSQLColumns(req.Select, "price")` {
		t.Fatalf("standalone compiler lost query demand: %s", got)
	}
}

func TestGeneratedQueryColumnUnion(t *testing.T) {
	for _, visibility := range []string{"", "@hidden", "@internal"} {
		t.Run("visibility"+visibility, func(t *testing.T) {
			assertions := queryColumnUnionAssertions
			if visibility == "" {
				assertions = strings.ReplaceAll(assertions, "rows.loadSQLColumns(", "selectProductSQLColumns(")
				assertions = strings.ReplaceAll(assertions, " rows:=(*Product)(nil)\n", "")
			}
			runGeneratedFixture(t, `model User @noTime { id: Int @id name: String }
model Product @noTime { id: Int @id name: String price: Int secret: Int `+visibility+` ownerId: Int owner: User @by(id, ownerId) }
api lookup(): [Product] { return Product.select().all().filter { it.price > 0 && it.secret > 0 } }`, assertions,
				StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2}, "Product": {"id": 1, "name": 2, "price": 3, "secret": 4, "ownerId": 5, "owner": 6}}})
		})
	}
}

func TestGeneratedQuerySelectorWithoutModels(t *testing.T) {
	runGeneratedFixture(t, `api plain(): Int { return 7 }`, "package generated\n", StableIDs{})
}

const queryColumnUnionAssertions = `package generated
import("slices";"testing";"github.com/light-speak/luxo/pkg/lux/selection")
func TestUnion(t *testing.T){
 rows:=(*Product)(nil)
 if rows.loadSQLColumns(nil,"price")!=nil{t.Fatal("nil no longer means all columns")}
 fields:=make([]*selection.Field,1,4);fields[0]=&selection.Field{Name:"name"};sentinel:=&selection.Field{Name:"untouched"};fields[:cap(fields)][1]=sentinel
 before:=selection.Format(fields)
 cases:=[]struct{required,want []string}{
  {[]string{"price","secret"},[]string{"\"name\"","\"price\"","\"secret\"","\"id\""}},
  {[]string{"name","price","price","id"},[]string{"\"name\"","\"price\"","\"id\""}},
  {[]string{"owner"},[]string{"\"name\"","\"owner_id\"","\"id\""}},
  {[]string{"missing"},[]string{"\"name\"","\"id\""}},
 }
 for _,sample:=range cases{if got:=rows.loadSQLColumns(fields,sample.required...);!slices.Equal(got,sample.want){t.Fatalf("required=%v got=%v want=%v",sample.required,got,sample.want)}}
 if selection.Format(fields)!=before||fields[:cap(fields)][1]!=sentinel{t.Fatal("caller selection mutated")}
 if got:=rows.loadSQLColumns([]*selection.Field{});!slices.Equal(got,[]string{"\"id\""}){t.Fatal(got)}
}
`

func TestQueryProjectionRejectsUnrelatedMemberCalls(t *testing.T) {
	models := map[string]*ast.ModelDecl{"Product": {Name: "Product"}}
	for _, receiver := range []ast.Expr{
		&ast.Ident{Name: "ordinary", TypeTagged: ast.TypeTagged{TypeTag: "Product"}},
		&ast.Ident{Name: "missing", TypeTagged: ast.TypeTagged{TypeTag: "MissingQueryBuilder"}},
	} {
		call := &ast.CallExpr{Func: &ast.MemberExpr{Object: receiver, Field: "select"}}
		if selectedQueryModel(call, models) != "" {
			t.Fatal("ordinary member became a database projection")
		}
	}
	analysis := loadDemandAnalysis{models: models, selectedQueries: true}
	receiver := &ast.Ident{Name: "rows", TypeTagged: ast.TypeTagged{TypeTag: "Product", ListTypeTag: true}}
	call := &ast.CallExpr{Func: &ast.MemberExpr{Object: receiver, Field: "filter"}, Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "callback"}}}}
	if origins, handled := analysis.selectedQueryCall(call); handled || origins != nil {
		t.Fatal("unknown callback bypassed conservative call analysis")
	}
}

func TestLoadFunctionRetainsUnconsumedObjectContract(t *testing.T) {
	input := analyzePermissionFixture(t, `model Product @noTime { id: Int @id name: String price: Int }
fn loaded(id: Int): Product? { return Product.load(id) }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	projections := analyzeLoadProjections(input.Files[0].Functions[0].Body, map[string]*ast.ModelDecl{"Product": input.Files[0].Models[0]}, nil)
	if len(projections) != 1 {
		t.Fatalf("function load projections=%d", len(projections))
	}
	want := compileSelectionLiteral([][]string{{"id"}, {"name"}, {"price"}})
	for _, projection := range projections {
		if projection.logic != want || projection.client {
			t.Fatalf("opaque function result lost its stored fields: %+v", projection)
		}
	}
}

func TestLoadProjectionDefensivePaths(t *testing.T) {
	argument := &ast.NamedArg{Value: &ast.Ident{Name: "id"}}
	call := &ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Product"}, Field: "load"}, Args: []*ast.NamedArg{argument}}
	analysis := loadDemandAnalysis{models: map[string]*ast.ModelDecl{"Product": {Name: "Product", Fields: []*ast.FieldDecl{{Name: "name", Type: &ast.TypeRef{Name: "String"}}}}}, demands: make(map[*ast.NamedArg]*loadDemand)}
	origins := analysis.call(call)
	analysis.scopes = []map[string][]loadOrigin{{"product": origins}}
	if result := analysis.expression(&ast.MemberExpr{Object: &ast.Ident{Name: "product"}, Field: "unresolved"}); len(result) != 0 {
		t.Fatal("unresolved member acquired a false model path")
	}
	if len(analysis.demands[argument].paths) == 0 {
		t.Fatal("unresolved member discarded the declared object contract")
	}
	if analysis.block(nil, nil, true, false) != nil {
		t.Fatal("nil block produced values")
	}
	analysis.statement(&ast.ValStmt{Names: []string{"first", "second"}, Value: &ast.Ident{Name: "product"}}, false)
	if origins, exists := analysis.lookup("second"); !exists || len(origins) != 1 {
		t.Fatal("destructured binding dropped provenance")
	}
	analysis.assignment(&ast.AssignStmt{Target: &ast.MemberExpr{Object: &ast.Ident{Name: "product"}, Field: "name"}, Value: &ast.Literal{}})
	analysis.assignment(&ast.AssignStmt{Target: &ast.Ident{Name: "missing"}, Value: &ast.Ident{Name: "product"}})
	analysis.expression(&ast.ObjectExpr{Fields: []*ast.NamedArg{{Name: "name", Value: &ast.MemberExpr{Object: &ast.Ident{Name: "product"}, Field: "name"}}}})
	analysis.expression(&ast.UnaryExpr{Op: "?", Value: &ast.Ident{Name: "product"}})
	analysis.expression(&ast.BangElvisExpr{Left: &ast.Ident{Name: "product"}, Right: &ast.Literal{}})
	analysis.expression(&ast.AwaitExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.Ident{Name: "product"}}}}})
	analysis.expression(&ast.TransactionExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.Ident{Name: "product"}}}}})
	analysis.expression(&ast.YieldExpr{Value: &ast.Ident{Name: "product"}})
	analysis.loop(&ast.ForStmt{VarName: "letter", Collection: &ast.MemberExpr{Object: &ast.Ident{Name: "product"}, Field: "name"}}, false, false)
	for _, expression := range []ast.Expr{
		&ast.ForStmt{},
		&ast.ForStmt{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}}},
		&ast.AwaitExpr{Body: &ast.Block{}},
	} {
		if origins := analysis.expression(expression); len(origins) != 0 {
			t.Fatalf("partial value expression %T acquired provenance: %v", expression, origins)
		}
		if analysis.controlDepth != 0 || len(analysis.scopes) != 1 {
			t.Fatalf("partial value expression %T leaked its scope", expression)
		}
	}
	if analysis.originModel(loadOrigin{demand: analysis.demands[argument], path: []string{"absent"}}) != nil {
		t.Fatal("unknown path acquired a model")
	}
	analysis.models["Product"].Fields = append(analysis.models["Product"].Fields, &ast.FieldDecl{Name: "untyped"})
	if analysis.originModel(loadOrigin{demand: analysis.demands[argument], path: []string{"untyped"}}) != nil {
		t.Fatal("missing type acquired a model")
	}
	projection := loadProjection{logic: `[]*selection.Field{}`, client: true}
	compiler := newCompiler(nil)
	compiler.loadSelections = map[*ast.NamedArg]loadProjection{argument: projection}
	if compiler.compiledLoadSelection(nil) != "nil" || compiler.compiledLoadSelection(call.Args) != "req.Select" {
		t.Fatal("selection defaults changed")
	}
}

const loadDemandRPCAssertions = `package generated
import("context";"net";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/dataloader";"github.com/light-speak/luxo/pkg/lux/rpc";"github.com/light-speak/luxo/pkg/lux/schema")
func TestSelectedLoadDataRPC(t *testing.T){
 registry:=schema.New();if err:=registry.RegisterModel(&schema.Model{Name:"Product",Fields:[]schema.Field{{ID:1,Name:"id",Type:schema.FieldInt},{ID:2,Name:"name",Type:schema.FieldString},{ID:3,Name:"price",Type:schema.FieldInt}}});err!=nil{t.Fatal(err)}
 router:=api.NewRouter();router.Schema=registry
 router.HandleInternal("svc:batchLoad:Product",func(_ context.Context,req *api.Request)error{
  keys,err:=req.ParamIntArray("keys");if err!=nil{return err}
  req.Buf.B=codec.AppendVarint(req.Buf.B,uint64(len(keys)))
  for _,key:=range keys {if err:=(&Product{Id:key,Name:"wanted",Price:99}).WriteLuxo(req.Buf,req.FieldMask);err!=nil{return err}}
  return nil
 })
 if err:=router.Registry.Register("svc:batchLoad:Product",100);err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterParams("svc:batchLoad:Product",[]api.ParamMeta{{Name:"keys",Type:"Int",IsList:true,FieldID:1}});err!=nil{t.Fatal(err)}
 if err:=router.Prepare();err!=nil{t.Fatal(err)}
 server:=rpc.NewServer(router);listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 app:=&App{};cfg:=dataloader.DefaultConfig();cfg.MaxBatch=1
 app.SetLoaders(NewRemoteLoaders(app,map[string]*rpc.Client{"product":client},registry,cfg))
 assertDemandResult(t,app)
}
`

func TestPrivateSQLSelectorsDoNotCollideWithModels(t *testing.T) {
	runGeneratedFixture(t, `model Product @noTime { id: Int @id secret: String @hidden loadSQLColumns: String }
model ProductLoad @noTime { id: Int @id }
api readSecret(id: Int): String { val product = (Product.load(id) ?: throw error.NotFound) return product.secret }
`, "package generated\n", StableIDs{ModelFields: map[string]map[string]int{"Product": {"id": 1, "secret": 2, "loadSQLColumns": 3}, "ProductLoad": {"id": 1}}})
}
