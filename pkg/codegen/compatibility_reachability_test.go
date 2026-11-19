package codegen

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestGeneratedPrivateSharedEnumAliases(t *testing.T) {
	for _, sample := range []struct{ name, declaration, source, value string }{
		{"private", "state", "enum state { ready idle }", "ready"},
		{"reserved exports", "state", "enum state { ready idle } enum LuxoPrivateEnum_7374617465 { ready } enum LuxoPrivateEnum_73746174657265616479 { ready }", "ready"},
		{"unicode", "状态", "enum 状态 { 好 坏 }", "好"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			file := parseStreamEnumSource(t, "origin/common/types.luxo", sample.source+" fn echo(value: "+sample.declaration+"): "+sample.declaration+" { return value }")
			input := semantic.Analyze([]*ast.File{file})
			if len(input.Errors) != 0 {
				t.Fatal(input.Errors)
			}
			runPrivateSharedEnum(t, input, sample.value)
		})
	}
}

func runPrivateSharedEnum(t *testing.T, input *semantic.Result, value string) {
	t.Helper()
	assertions := strings.ReplaceAll(privateSharedEnumAssertions, "WIRE_VALUE", value)
	dir := writeProtocolFixture(t, "package generated\n", assertions)
	context := BuildEventContext(input.Files, "github.com/light-speak/luxo/.tmp/"+filepath.Base(dir))
	context.SharedEnumPackage = context.ModulePath + "/internal/luxotypes"
	generator := mustNewGenerator(t, GeneratorConfig{Events: context})
	generated, err := generator.Generate(input, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	writeStreamEnumPackage(t, dir, generated.Files)
	canonical, err := generator.GenerateSharedEnums()
	if err != nil {
		t.Fatal(err)
	}
	writeStreamEnumPackage(t, filepath.Join(dir, "internal", "luxotypes"), map[string][]byte{"enum.gen.go": canonical})
	command := exec.Command("go", "test", "-race", "-count=1", "-timeout=60s", "./...")
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("private shared enum execution: %v\n%s", err, output)
	}
}

const privateSharedEnumAssertions = `package generated
import("bytes";"errors";"testing";"github.com/light-speak/luxo/pkg/lux/codec")
func TestPrivateSharedEnum(t *testing.T){
 app:=&App{};value,err:=app.echo(t.Context(),"WIRE_VALUE")
 if err!=nil||string(value)!="WIRE_VALUE"||!value.IsValid(){t.Fatal(value,err)}
 got,err:=value.AppendLuxo(nil);want,encodeErr:=codec.AppendString(nil,"WIRE_VALUE")
 if err!=nil||encodeErr!=nil||!bytes.Equal(got,want){t.Fatal(got,want,err,encodeErr)}
 invalid,err:=app.echo(t.Context(),"missing");if err!=nil{t.Fatal(err)}
 prefix:=[]byte{7,8};got,err=invalid.AppendLuxo(prefix)
 if !errors.Is(err,codec.ErrInvalidEnum)||!bytes.Equal(got,prefix){t.Fatal(got,err)}
}
`

func TestFormatGeneratedUnicodeRejectsInvalidOutput(t *testing.T) {
	for _, source := range []string{
		"package !",
		"package generated\ntype 状态 int\ntype 状态 string\n",
	} {
		if output, err := formatGeneratedWithIdentifiers([]byte(source), true); err == nil || output != nil {
			t.Fatal("invalid Go output was accepted", source, string(output), err)
		}
	}
	input := []byte("package generated\ntype 状态 struct { 名 string `json:\"名\"` }\nconst wire = \"状态.名\"\n")
	output, err := formatGeneratedWithIdentifiers(input, true)
	if err != nil || !bytes.Contains(output, []byte("`json:\"名\"`")) || !bytes.Contains(output, []byte("\"状态.名\"")) {
		t.Fatal("identifier normalization changed wire names", string(output), err)
	}
}

func TestPrepareSealedRejectsMissingStableWireIDs(t *testing.T) {
	input := analyzePermissionFixture(t, "sealed Outcome { Ready(value: Int) }")
	for _, sample := range []struct {
		name string
		ids  StableIDs
		want string
	}{
		{"variant", StableIDs{}, "has no stable tag"},
		{"payload", StableIDs{ModelFields: map[string]map[string]int{"Outcome": {"Ready": 1}}}, "has no stable ID"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			generator := mustNewGenerator(t, GeneratorConfig{IDs: sample.ids})
			generated, err := generator.Generate(input, "generated", nil)
			if generated != nil || err == nil || !strings.Contains(err.Error(), sample.want) {
				t.Fatalf("missing stable wire ID emitted a partial package: output=%v err=%v", generated != nil, err)
			}
		})
	}
}

func TestGeneratedSealedPayloadNamespaceCollision(t *testing.T) {
	runGeneratedFixture(t, `type _luxoSealed7_Outcome_1 {}
sealed Outcome { Ready(value: Int) }
fn ready(): Outcome { return Outcome.Ready(42) }`, `package generated
import("bytes";"testing";"github.com/light-speak/luxo/pkg/lux/api")
func TestSealedPayloadNamespace(t *testing.T){
 value,err:=(&App{}).ready(t.Context());if err!=nil{t.Fatal(err)}
 var output api.ResponseBuf;if err:=value.WriteLuxo(&output,nil);err!=nil{t.Fatal(err)}
 if !bytes.Equal(output.B,[]byte{1,4,0,1,84,0}){t.Fatal(output.B)}
}
`, StableIDs{ModelFields: map[string]map[string]int{"Outcome": {"Ready": 1}, "Outcome.Ready": {"value": 1}, "_luxoSealed7_Outcome_1": {}}})
}

func TestUnicodeExportDetectionIncludesEveryDeclarationKind(t *testing.T) {
	field := &ast.FieldDecl{Name: "名", Type: &ast.TypeRef{Name: "String"}}
	parameter := &ast.ParamDecl{Name: "值", Type: &ast.TypeRef{Name: "Int"}}
	for _, sample := range []struct {
		name  string
		input *semantic.Result
	}{
		{"resolved field", &semantic.Result{Types: map[string]*semantic.ResolvedType{"Packet": {Fields: map[string]*semantic.FieldInfo{"名": {}}}}}},
		{"raw type", &semantic.Result{Files: []*ast.File{{Types: []*ast.TypeDecl{{Name: "值"}}}}}},
		{"raw model field", &semantic.Result{Files: []*ast.File{{Models: []*ast.ModelDecl{{Name: "Packet", Fields: []*ast.FieldDecl{field}}}}}}},
		{"raw enum", &semantic.Result{Files: []*ast.File{{Enums: []*ast.EnumDecl{{Name: "状态"}}}}}},
		{"raw sealed", &semantic.Result{Files: []*ast.File{{Sealeds: []*ast.SealedDecl{{Name: "结果"}}}}}},
		{"event parameter", &semantic.Result{Files: []*ast.File{{Events: []*ast.EventDecl{{Name: "Changed", Params: []*ast.ParamDecl{parameter}}}}}}},
		{"function", &semantic.Result{Files: []*ast.File{{Functions: []*ast.FnDecl{{Name: "读取"}}}}}},
		{"API parameter", &semantic.Result{Files: []*ast.File{{APIs: []*ast.ApiDecl{{Name: "read", Params: []*ast.ParamDecl{parameter}}}}}}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			if !needsUnicodeGoExports(sample.input) {
				t.Fatal("export rewrite omitted a valid declaration", sample.name)
			}
		})
	}
	context := &GeneratorContext{events: &EventContext{Events: map[string]*ast.EventDecl{"Changed": {Name: "Changed", Params: []*ast.ParamDecl{parameter}}}}}
	if !context.needsUnicodeGoExports(&semantic.Result{}) {
		t.Fatal("remote event parameter was omitted from export rewriting")
	}
	if (&GeneratorContext{}).needsUnicodeGoExports(&semantic.Result{Files: []*ast.File{{Functions: []*ast.FnDecl{{Name: "read"}}}}}) {
		t.Fatal("ASCII-only generation acquired an unnecessary export rewrite")
	}
}

func TestUnusedGenericDeclarationDoesNotEmitUnboundType(t *testing.T) {
	input := analyzePermissionFixture(t, `type Unused<T> { value: T }`)
	generated, err := Generate(input, "generated", GeneratorConfig{})
	if err != nil || generated == nil {
		t.Fatal("unused generic declaration was rejected", err)
	}
	for name, body := range generated.Files {
		if strings.Contains(string(body), "type Unused struct") {
			t.Fatalf("unbound generic escaped into %s", name)
		}
	}
}

func TestGenericLoweringErrorsPropagateWithoutArtifacts(t *testing.T) {
	const source = `type Box<T> { value: T } api echo(value: Box<Int>): Box<Int> { return value }`
	view, err := semantic.CompilationView(analyzePermissionFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	name := view.Files[0].APIs[0].Params[0].Type.Name
	input := analyzePermissionFixture(t, source+" type "+name+" { value: String }")
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	generated, err := Generate(input, "generated", GeneratorConfig{})
	if err == nil || generated != nil {
		t.Fatal("generic lowering collision emitted a package", err)
	}
	data, err := BuildSchemaJSON(input, nil, GeneratorConfig{})
	if err == nil || data != nil {
		t.Fatal("generic lowering collision emitted a schema", err)
	}
}

func TestScalarComparisonDeclinesNonComparisonOperators(t *testing.T) {
	for _, sample := range []struct {
		name     string
		nullable bool
		op       string
	}{{"DateTime", false, "+"}, {"DateTime", true, "<"}, {"Decimal", true, "<"}} {
		t.Run(sample.name+sample.op, func(t *testing.T) {
			left, right := &ast.Ident{Name: "left"}, &ast.Ident{Name: "right"}
			left.SetTypeTag(sample.name)
			right.SetTypeTag(sample.name)
			left.SetNullable(sample.nullable)
			right.SetNullable(sample.nullable)
			compiled, handled := newCompiler(nil).compileScalarComparison(&ast.BinaryExpr{Op: sample.op, Left: left, Right: right}, "left", "right")
			if handled || compiled != "" {
				t.Fatalf("comparison emitter consumed an unsupported operator: %q", compiled)
			}
		})
	}
}

func TestGeneratedSealedEqualityAndNullableStatementPatterns(t *testing.T) {
	runGeneratedFixture(t, `type Plain { value: Int }
sealed Leaf { Items(values: [Int]) }
sealed Parent { Child(value: Leaf) }
sealed Optional { Data(value: Plain?) }
sealed Scalar { Values(amount: Decimal, moment: DateTime, note: String?) }
sealed Mark { Ready(value: Int) Waiting }
fn same(left: Scalar, right: Scalar): Boolean { return left == right }
fn parent(value: Parent): Parent { return value }
fn optional(value: Optional): Optional { return value }
fn classify(value: Mark?): Int {
 var score = 0
 when(value) {
  null -> { score = 1 }
  Mark.Waiting() -> { score = 2 }
  is Ready -> { score = 3 }
  else -> { score = 4 }
 }
 return score
}`, sealedEqualityReachabilityAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"Plain": {"value": 1}, "Leaf": {"Items": 1}, "Leaf.Items": {"values": 1},
		"Parent": {"Child": 1}, "Parent.Child": {"value": 1},
		"Optional": {"Data": 1}, "Optional.Data": {"value": 1},
		"Scalar": {"Values": 1}, "Scalar.Values": {"amount": 1, "moment": 2, "note": 3},
		"Mark": {"Ready": 1, "Waiting": 2}, "Mark.Ready": {"value": 1},
	}})
}

func TestCompiledEventContextPreservesCallerMetadata(t *testing.T) {
	input := analyzePermissionFixture(t, `type Box<T> { value: T } event Changed(value: Box<Int>)`)
	view, err := semantic.CompilationView(input)
	if err != nil {
		t.Fatal(err)
	}
	source := &EventContext{ModulePath: "example.test/events"}
	compiled := compiledEventContext(source, view, view.CompiledValueDeclarations())
	if compiled == source || source.Events != nil || source.TypeModule != nil {
		t.Fatal("generic event preparation changed caller-owned metadata")
	}
	declaration := view.CompiledValueEvents()["Changed"]
	if !sameCompiledEventParameters(compiled.Events["Changed"], declaration) || compiled.TypeModule[declaration.Params[0].Type.Name] == "" {
		t.Fatal("compiled event lost its concrete parameter or owner")
	}
	if sameCompiledEventParameters(nil, declaration) || sameCompiledEventParameters(&ast.EventDecl{}, declaration) {
		t.Fatal("incomplete event metadata was accepted as equivalent")
	}
	if next := compiledEventContext(compiled, view, view.CompiledValueDeclarations()); next != compiled {
		t.Fatal("already compiled metadata was needlessly cloned")
	}
}

func TestGeneratedExternalSealedEventCodec(t *testing.T) {
	files := []*ast.File{
		parseStreamEnumSource(t, "origin/common/value.luxo", `sealed State { Ready }`),
		parseStreamEnumSource(t, "origin/producer/main.luxo", `event Remote(value: State)`),
	}
	input := semantic.AnalyzeModules(files)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	selected := *input
	selected.Files = files[:1]
	generator := mustNewGenerator(t, GeneratorConfig{
		Events: BuildEventContext(files, "example.test/events"),
		IDs:    StableIDs{ModelFields: map[string]map[string]int{"State": {"Ready": 1}}},
	})
	generated, err := generator.Generate(&selected, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	runGeneratedCompilation(t, generated, `package generated
import "testing"
func TestExternalSealedEventWire(t *testing.T){
 value:=(State{}).NewVariant1()
 raw,err:=value.MarshalEventSnapshot(nil,0);if err!=nil{t.Fatal(err)}
 var decoded State;if err:=decoded.UnmarshalEventSnapshot(raw,0);err!=nil{t.Fatal(err)}
 if decoded.LuxoVariantTag()!=1{t.Fatal(decoded)}
}
`)
}

func TestGeneratedConcreteWireIDsOverrideTemplate(t *testing.T) {
	const source = `type Box<T> { value: T } fn echo(value: Box<Int>): Box<Int> { return value }`
	view, err := semantic.CompilationView(analyzePermissionFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	name := view.Files[0].Functions[0].ReturnType.Name
	runGeneratedFixture(t, source, strings.ReplaceAll(`package generated
import("bytes";"testing";"github.com/light-speak/luxo/pkg/lux/api")
func TestConcreteWireID(t *testing.T){
 value,err:=(&App{}).echo(t.Context(),CONCRETE{Value:7});if err!=nil{t.Fatal(err)}
 var output api.ResponseBuf;if err:=value.WriteLuxo(&output,nil);err!=nil{t.Fatal(err)}
 if !bytes.Equal(output.B,[]byte{0,17,14,0}){t.Fatal(output.B)}
}
`, "CONCRETE", name), StableIDs{ModelFields: map[string]map[string]int{"Box": {"value": 1}, name: {"value": 17}}})
}

func TestGeneratedSecondSealedPayloadMutation(t *testing.T) {
	runGeneratedFixture(t, `type Plain { value: Int }
type Collection { values: [Int] }
sealed NonComparable { Data(value: Collection) }
sealed Choice { Empty Data(box: Plain) }
fn bump(value: Choice): Int {
 return when(value) { is Empty -> 0 is Data -> { value.box.value += 1 value.box.value } }
}`, `package generated
import "testing"
func TestSecondSealedPayloadMutation(t *testing.T){
 value:=(Choice{}).NewVariant2(Plain{Value:4})
 got,err:=(&App{}).bump(t.Context(),value);if err!=nil||got!=5{t.Fatal(got,err)}
 if value.AsVariant2().F1.Value!=4{t.Fatal("value slot was modified through a copied holder")}
}

`, StableIDs{ModelFields: map[string]map[string]int{
		"Plain": {"value": 1}, "Collection": {"values": 1}, "NonComparable": {"Data": 1}, "NonComparable.Data": {"value": 1},
		"Choice": {"Empty": 1, "Data": 2}, "Choice.Data": {"box": 1},
	}})
}

func TestReturnedCyclicObjectKeepsSelectedQueryContract(t *testing.T) {
	projections := selectedObjectProjections(t, `val holder = Holder { id: 1, rows: Node.select().all() } holder.parent = holder return holder`, "Holder")
	if len(projections) != 1 {
		t.Fatal("cyclic returned object lost its selected source", projections)
	}
	if columns := queryColumnArguments(projections[0].columns); columns != `, "id", "name"` {
		t.Fatal("cyclic path discarded the declared row contract", columns)
	}
}

func TestFunctionProjectionCoalescesParentAndChildDemands(t *testing.T) {
	input := analyzePermissionFixture(t, `model Node @noTime { id: Int @id name: String secret: String }
type Box<T> { value: T }
type Outer { box: Box<Node?> }
fn fetch(): Outer { return Outer { box: Box<Node?>(value: Node.select().first()) } }
fn inspect(value: Box<Node?>): Result<Int> @native
api page(): String {
 val outer = fetch()
 val name = (outer.box.value ?: throw error.NotFound).name
 val ignored = inspect(outer.box)?
 return (outer.box.value ?: throw error.NotFound).name
}`)
	view, err := semantic.CompilationView(input)
	if err != nil {
		t.Fatal(err)
	}
	models, _ := collectInferredAPIs(view)
	api := view.Files[0].APIs[0]
	projections := analyzeSelectedQueryProjections(api.Body, models, api.ReturnType, collectFunctionDeclarations(view))
	for _, projection := range projections {
		if len(projection.columns) != 1 || strings.Join(projection.columns[0], ".") != "box" {
			t.Fatal("parent contract did not subsume repeated child demands", projection.columns)
		}
	}
	if len(projections) != 1 {
		t.Fatal("function source projection was lost", projections)
	}
}

func TestUnusedMiddlewareDoesNotCreateInstanceSaveState(t *testing.T) {
	input := analyzePermissionFixture(t, `model Entry @noTime { id: Int @id name: String }
 middleware persist { val value = Entry { id: 1, name: "saved" } value.save() }`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	generated, err := Generate(input, "generated", GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Entry": {"id": 1, "name": 2}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(generated.Files["save.gen.go"]) != 0 {
		t.Fatal("unused middleware allocated persistence state without a typed model receiver")
	}
}

func TestSealedSchemaRejectsIncompleteOrRepeatedWireMetadata(t *testing.T) {
	variant := &ast.SealedVariant{Name: "Ready", Fields: []*ast.ParamDecl{{Name: "value", Type: &ast.TypeRef{Name: "Int"}}}}
	for _, sample := range []struct {
		name        string
		declaration *ast.SealedDecl
		ids         StableIDs
		want        string
	}{
		{"missing declaration", nil, StableIDs{}, "sealed declaration is missing"},
		{"missing variant", &ast.SealedDecl{Name: "Outcome", Variants: []*ast.SealedVariant{nil}}, StableIDs{}, "sealed Outcome variant is missing"},
		{"missing payload type", &ast.SealedDecl{Name: "Outcome", Variants: []*ast.SealedVariant{{Name: "Ready", Fields: []*ast.ParamDecl{{Name: "value"}}}}}, StableIDs{ModelFields: map[string]map[string]int{"Outcome": {"Ready": 1}}}, "sealed payload Outcome.Ready field type is missing"},
		{"missing tag", &ast.SealedDecl{Name: "Outcome", Variants: []*ast.SealedVariant{variant}}, StableIDs{}, "stable tag"},
		{"repeated variant", &ast.SealedDecl{Name: "Outcome", Variants: []*ast.SealedVariant{variant, variant}}, StableIDs{ModelFields: map[string]map[string]int{"Outcome": {"Ready": 1}, "Outcome.Ready": {"value": 1}}}, "duplicate"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			input := buildResult(&ast.File{Sealeds: []*ast.SealedDecl{sample.declaration}})
			data, err := BuildSchemaJSON(input, nil, GeneratorConfig{IDs: sample.ids})
			if data != nil || err == nil || !strings.Contains(err.Error(), sample.want) {
				t.Fatalf("invalid sealed metadata produced partial JSON: output=%s err=%v", data, err)
			}
		})
	}
}

const sealedEqualityReachabilityAssertions = `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec")
func TestScalarEqualityAndPatterns(t *testing.T){
 app:=&App{}
 for _,sample:=range []struct{right string;want bool}{
  {"{\"Values\":{\"amount\":\"1.0\",\"moment\":\"2026-01-01T00:00:00Z\",\"note\":null}}",true},
  {"{\"Values\":{\"amount\":\"2\",\"moment\":\"2026-01-01T00:00:00Z\",\"note\":null}}",false},
  {"{\"Values\":{\"amount\":\"1\",\"moment\":\"2026-01-02T00:00:00Z\",\"note\":null}}",false},
 }{
  var left,right Scalar
  if err:=left.ReadJSONInput([]byte("{\"Values\":{\"amount\":\"1\",\"moment\":\"2026-01-01T00:00:00Z\",\"note\":null}}"));err!=nil{t.Fatal(err)}
  if err:=right.ReadJSONInput([]byte(sample.right));err!=nil{t.Fatal(err)}
  got,err:=app.same(t.Context(),left,right);if err!=nil||got!=sample.want{t.Fatal(got,err)}
 }
 for _,sample:=range []struct{raw string;want int64}{{"null",1},{"{\"Waiting\":{}}",2},{"{\"Ready\":{\"value\":4}}",3}}{
  var value *Mark
  if sample.raw!="null"{value=&Mark{};if err:=value.ReadJSONInput([]byte(sample.raw));err!=nil{t.Fatal(err)}}
  got,err:=app.classify(t.Context(),value);if err!=nil||got!=sample.want{t.Fatal(sample.raw,got,err)}
 }
 var parent Parent;if err:=parent.ReadJSONInput([]byte("{\"Child\":{\"value\":{\"Items\":{\"values\":[1,2]}}}}"));err!=nil{t.Fatal(err)}
 got,err:=app.parent(t.Context(),parent);if err!=nil{t.Fatal(err)}
 var wire api.ResponseBuf;if err:=got.WriteLuxo(&wire,nil);err!=nil{t.Fatal(err)}
 var decoded Parent;reader:=codec.NewDecoder(wire.B);decoded.ReadLuxo(reader);if err:=reader.Finish();err!=nil||decoded.LuxoVariantTag()!=1{t.Fatal(decoded,err)}
 var optional Optional;if err:=optional.ReadJSONInput([]byte("{\"Data\":{\"value\":{\"value\":7}}}"));err!=nil{t.Fatal(err)}
 if _,err:=app.optional(t.Context(),optional);err!=nil{t.Fatal(err)}
}
`
