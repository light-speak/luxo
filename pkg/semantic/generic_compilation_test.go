package semantic

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGenericCompilationViewPreservesSourceAndMetadata(t *testing.T) {
	source := analyzeCached(t, `
type Box<T> { value: T }
fn read(box: Box<Int>): Int { return box.value }
api cached(): Int @cache(policy: "preview") { return 7 }
`)
	expectNoErrors(t, source)
	view, err := CompilationView(source)
	if err != nil {
		t.Fatal(err)
	}
	if view == source || view.Files[0] == source.Files[0] {
		t.Fatal("generic compilation reused mutable source containers")
	}
	parameter := view.Files[0].Functions[0].Params[0].Type
	if parameter.Name == "Box" || len(parameter.TypeArgs) != 0 {
		t.Fatalf("generic parameter was not concretized: %#v", parameter)
	}
	if len(view.Files[0].Types) != 1 || view.Files[0].Types[0].Name != parameter.Name || view.Files[0].Types[0].Fields[0].Type.Name != "Int" {
		t.Fatalf("missing concrete Box<Int> declaration: %#v", view.Files[0].Types)
	}
	if ref := source.Files[0].Functions[0].Params[0].Type; ref.Name != "Box" || len(ref.TypeArgs) != 1 {
		t.Fatal("compilation modified source type references")
	}
	if source.Files[0].Types[0].Fields[0].Type.Name != "T" || source.Scope.Lookup("read").Type.Name != "Int" {
		t.Fatal("compilation modified source declarations or scope")
	}
	for expression := range source.GenericExprTypes {
		if expression.GetTypeTag() != "Box" {
			t.Fatal("compilation modified source expression annotations")
		}
		if _, exists := view.GenericExprTypes[expression]; exists {
			t.Fatal("compilation retained a source expression key")
		}
	}
	for expression, typ := range view.GenericExprTypes {
		if expression.GetTypeTag() != parameter.Name || typ.Name != parameter.Name || len(typ.TypeArgs) != 0 {
			t.Fatal("concrete expression annotations or generic metadata disagree")
		}
	}
	assertGenericCompilationCacheMetadata(t, source, view)
	view.Files[0].Types[0].Fields[0].Type.Name = "String"
	second, err := CompilationView(source)
	if err != nil || second.Files[0].Types[0].Fields[0].Type.Name != "Int" {
		t.Fatal("independent compilation views shared mutable fields", err)
	}
}

func assertGenericCompilationCacheMetadata(t *testing.T, source, view *Result) {
	t.Helper()
	oldAPI, newAPI := source.Files[0].APIs[0], view.Files[0].APIs[0]
	if _, exists := view.CachePolicies[oldAPI.Directives[0]]; exists {
		t.Fatal("cache policy retained a source directive key")
	}
	if view.CachePolicies[newAPI.Directives[0]] != source.CachePolicies[oldAPI.Directives[0]] {
		t.Fatal("cache policy changed while rebinding its directive")
	}
	if _, exists := view.ExecutionCacheDependencies[newAPI]; !exists {
		t.Fatal("cache dependencies lost their cloned API key")
	}
	if _, exists := view.ExecutionCacheDependencies[oldAPI]; exists {
		t.Fatal("cache dependencies retained a source API key")
	}
}

func TestGenericCompilationViewNestedRecursiveAndBuiltinTypes(t *testing.T) {
	for _, sample := range []struct{ name, source string }{
		{"nested", `type Box<T> { value: T } type Wrap<T> { value: Box<T> } fn read(value: Wrap<Int>): Int { return value.value.value }`},
		{"recursive", `type Node<T> { value: T next: Node<T>? } fn read(value: Node<Int>): Int { return value.value }`},
		{"page", `type Item { id: Int } fn read(value: Page<Item>): [Item] { return value.items }`},
		{"cursor", `type Item { id: Int } fn read(value: Cursor<Item>): [Item] { return value.items }`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			source := analyze(t, sample.source)
			expectNoErrors(t, source)
			view, err := CompilationView(source)
			if err != nil {
				t.Fatal(err)
			}
			declared := make(map[string]bool)
			for _, file := range view.Files {
				for _, declaration := range file.Types {
					if len(declaration.TypeParams) != 0 || declared[declaration.Name] {
						t.Fatal("unexpanded or duplicate concrete declaration", declaration.Name)
					}
					declared[declaration.Name] = true
				}
			}
			if !declared[view.Files[0].Functions[0].Params[0].Type.Name] {
				t.Fatal("parameter does not reference an emitted concrete type")
			}
		})
	}
}

func TestGenericCompilationViewSeparatesArgumentShapes(t *testing.T) {
	source := analyze(t, `type Box<T> { value: T }
fn number(value: Box<Int>): Int { return value.value }
fn text(value: Box<String>): String { return value.value }
fn optional(value: Box<String?>): String? { return value.value }
fn numbers(value: Box<[Int]>): [Int] { return value.value }
`)
	expectNoErrors(t, source)
	view, err := CompilationView(source)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for _, declaration := range view.Files[0].Functions {
		name := declaration.Params[0].Type.Name
		if seen[name] {
			t.Fatalf("distinct generic arguments shared concrete type %s", name)
		}
		seen[name] = true
	}
	if len(view.Files[0].Types) != 4 {
		t.Fatalf("expected four concrete argument shapes, got %d", len(view.Files[0].Types))
	}
}

func TestGenericCompilationViewCoversConstructorsAndLocalChannels(t *testing.T) {
	for _, source := range []string{
		`type Box<T> { value: T } fn make(): Box<Int> { return Box<Int>(value: 1) }`,
		`type Box<T> { value: T } fn make(): Box<Box<Int>> { return Box<Box<Int>>(value: Box<Int>(value: 1)) }`,
		`type Box<T> { value: T } fn make(): Box<[Int]> { return Box<[Int]>(value: [1, 2]) }`,
		`type Box<T> { value: T } fn read(value: Channel<Box<Int>>): Int { return 1 }`,
		`type Box<T> { value: T } fn read(value: Channel<Box<Int>>): Box<Int> { return <-value }`,
		`type Box<T> { value: T } fn number(): Box<Int> { return Box<Int>(value: 1) } fn text(): Box<String> { return Box<String>(value: "ok") } fn read(): Int { val (numberValue, textValue) = await { number() text() } return numberValue.value }`,
		`model Record @noTime { id: Int @id } enum Role { READY } sealed Outcome { Done } type Box<T> { value: T } fn read(value: Box<Record>, role: Box<Role>, outcome: Box<Outcome>): Int { return value.value.id }`,
		`fn read(value: Page<Int>, cursor: Cursor<Int>): Int { return value.total }`,
	} {
		t.Run(source, func(t *testing.T) {
			analysis := analyze(t, source)
			expectNoErrors(t, analysis)
			view, err := CompilationView(analysis)
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range view.Files {
				for _, function := range file.Functions {
					ast.WalkExprs(function.Body, func(expression ast.Expr) {
						if call, ok := expression.(*ast.CallExpr); ok && call.ConstructorType != nil {
							if len(call.TypeArgs) != 0 || len(call.ConstructorType.TypeArgs) != 0 || call.Func.(*ast.Ident).Name != call.ConstructorType.Name {
								t.Fatal("constructor or destination retained open type arguments")
							}
						}
					})
				}
			}
		})
	}
}

func TestGenericCompilationViewOwnsRepeatedEventArguments(t *testing.T) {
	source := analyze(t, `type Box<T> { value: T }
event Ready(value: Box<Int>)
fn publish() { emit Ready(value: Box<Int>(value: 1)) emit Ready(value: Box<Int>(value: 2)) }`)
	expectNoErrors(t, source)
	view, err := CompilationView(source)
	if err != nil {
		t.Fatal(err)
	}
	name := view.Files[0].Events[0].Params[0].Type.Name
	for _, statement := range view.Files[0].Functions[0].Body.Stmts {
		argument := statement.(*ast.EmitStmt).Args[0]
		if argument.ExpectedType.Name != name || len(argument.ExpectedType.TypeArgs) != 0 {
			t.Fatal("repeated event arguments retained a generic template")
		}
	}
	if source.Files[0].Events[0].Params[0].Type.Name != "Box" {
		t.Fatal("event argument compilation modified the source declaration")
	}
}

func TestGenericCompilationViewProjectsServiceDependencies(t *testing.T) {
	files := []*ast.File{
		parseFileWithName(t, `type Box<T> { value: T } type Counter { value: Int }`, "origin/common/box.luxo"),
		parseFileWithName(t, `fn alpha(value: Box<Counter>): Int { return value.value.value }`, "origin/alpha/main.luxo"),
		parseFileWithName(t, `fn beta(value: Box<Counter>): Int { return value.value.value }`, "origin/beta/main.luxo"),
	}
	source := AnalyzeModules(files)
	expectNoErrors(t, source)
	full, err := CompilationView(source)
	if err != nil {
		t.Fatal(err)
	}
	concrete := full.Files[1].Functions[0].Params[0].Type.Name
	if full.Files[0].Types[1].Name != concrete {
		t.Fatal("concrete declaration lost its template's canonical owner")
	}
	for _, index := range []int{1, 2} {
		selected := *full
		selected.Files = full.Files[index : index+1]
		view, err := CompilationView(&selected)
		if err != nil {
			t.Fatal(err)
		}
		found := make(map[string]bool)
		for _, file := range view.Files {
			for _, declaration := range file.Types {
				if file.Name != files[0].Name || found[declaration.Name] {
					t.Fatal("dependency ownership or uniqueness changed", file.Name, declaration.Name)
				}
				found[declaration.Name] = true
			}
		}
		if !found[concrete] || !found["Counter"] || len(found) != 2 || len(selected.Files) != 1 {
			t.Fatal("service lost its concrete/ordinary dependencies or modified the source", found)
		}
		if again, err := CompilationView(view); err != nil || again != view {
			t.Fatal("complete service projection should be reused", err)
		}
	}
	common := *full
	common.Files = full.Files[:1]
	projected, err := CompilationView(&common)
	if err != nil || len(projected.Files[0].Types) != 2 {
		t.Fatal("canonical common definitions were removed", err)
	}
}

func TestSealedCompilationViewProjectsCanonicalPayloads(t *testing.T) {
	source := AnalyzeModules([]*ast.File{
		parseFileWithName(t, `type Box<T> { value: T } sealed Outcome { Done(value: Box<Int>) Next(value: Outcome?) }`, "origin/common/result.luxo"),
		parseFileWithName(t, `fn score(value: Outcome): Int { return when(value) { is Done -> value.value.value is Next -> 0 } }`, "origin/alpha/main.luxo"),
	})
	expectNoErrors(t, source)
	full, err := CompilationView(source)
	if err != nil {
		t.Fatal(err)
	}
	selected := *full
	selected.Files = full.Files[1:2]
	view, err := CompilationView(&selected)
	if err != nil {
		t.Fatal(err)
	}
	var sealed *ast.SealedDecl
	var payload *ast.TypeDecl
	for _, file := range view.Files {
		if len(file.Sealeds) > 0 {
			sealed = file.Sealeds[0]
		}
		if len(file.Types) > 0 {
			payload = file.Types[0]
		}
	}
	if sealed == nil || payload == nil || sealed.Variants[0].Fields[0].Type.Name != payload.Name || len(payload.TypeParams) != 0 {
		t.Fatal("service projection lost the canonical sealed/generic payload declarations")
	}
	if source.Files[0].Sealeds[0].Variants[0].Fields[0].Type.Name != "Box" || len(selected.Files) != 1 {
		t.Fatal("service compilation changed source declarations")
	}
	if again, err := CompilationView(full); err != nil || again != full {
		t.Fatal("complete sealed compilation should retain canonical declarations", err)
	}
}

func TestSealedCompilationViewRetainsListenerOnlyDependencies(t *testing.T) {
	source := AnalyzeModules([]*ast.File{
		parseFileWithName(t, `type Box<T> { value: T } sealed Outcome { Done(value: Box<Int>) Pending }`, "origin/common/result.luxo"),
		parseFileWithName(t, `event Ready(value: Outcome)`, "origin/alpha/events.luxo"),
		parseFileWithName(t, `on Ready {}`, "origin/beta/events.luxo"),
	})
	expectNoErrors(t, source)
	full, err := CompilationView(source)
	if err != nil {
		t.Fatal(err)
	}
	selected := *full
	selected.Files = full.Files[2:3]
	view, err := CompilationView(&selected)
	if err != nil {
		t.Fatal(err)
	}
	var sum *ast.SealedDecl
	var payload *ast.TypeDecl
	for _, file := range view.Files {
		if len(file.Sealeds) > 0 {
			sum = file.Sealeds[0]
		}
		if len(file.Types) > 0 {
			payload = file.Types[0]
		}
	}
	if sum == nil || sum.Name != "Outcome" || payload == nil || sum.Variants[0].Fields[0].Type.Name != payload.Name {
		t.Fatal("empty listener lost the event's static sealed payload dependencies")
	}
	if again, err := CompilationView(view); err != nil || again != view {
		t.Fatal("listener projection should reuse its complete dependencies", err)
	}
}

func TestGenericCompilationViewExtendsProjectedFiles(t *testing.T) {
	source := AnalyzeModules([]*ast.File{
		parseFileWithName(t, `type Box<T> { value: T }`, "origin/common/box.luxo"),
		parseFileWithName(t, `fn alpha(value: Box<Int>): Int { return value.value }`, "origin/alpha/main.luxo"),
		parseFileWithName(t, `fn beta(value: Box<String>): String { return value.value }`, "origin/beta/main.luxo"),
	})
	expectNoErrors(t, source)
	full, err := CompilationView(source)
	if err != nil {
		t.Fatal(err)
	}
	selected := *full
	selected.Files = full.Files[1:2]
	first, err := CompilationView(&selected)
	if err != nil {
		t.Fatal(err)
	}
	extended := *first
	extended.Files = append(append([]*ast.File{}, first.Files...), full.Files[2])
	view, err := CompilationView(&extended)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.valueTypes.injected) != 2 || !view.valueTypes.injected[first.Files[1]] || len(first.valueTypes.injected) != 1 {
		t.Fatal("extended projection lost existing dependency ownership or changed its source")
	}
	if again, err := CompilationView(view); err != nil || again != view {
		t.Fatal("extended projection imported unrelated declarations", err)
	}
}

func TestGenericServiceProjectionIsIdempotentWithOtherInstantiations(t *testing.T) {
	source := AnalyzeModules([]*ast.File{
		parseFileWithName(t, `type Box<T> { value: T } sealed Unused { Value(value: Box<Boolean>) }`, "origin/common/box.luxo"),
		parseFileWithName(t, `fn alpha(value: Box<Int>): Int { return value.value }`, "origin/alpha/main.luxo"),
		parseFileWithName(t, `fn beta(value: Box<String>): String { return value.value }`, "origin/beta/main.luxo"),
	})
	expectNoErrors(t, source)
	full, err := CompilationView(source)
	if err != nil {
		t.Fatal(err)
	}
	selected := *full
	selected.Files = full.Files[1:2]
	first, err := CompilationView(&selected)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CompilationView(first)
	if err != nil || second != first {
		t.Fatal("repeated service compilation imported unrelated concrete types", err)
	}
	for _, file := range second.Files {
		for _, declaration := range file.Types {
			if declaration.Name != first.Files[0].Functions[0].Params[0].Type.Name {
				t.Fatal("unrelated concrete type leaked into the service", declaration.Name)
			}
		}
	}
	current := first
	for range 4 {
		subset := *current
		subset.Files = current.Files[:1]
		current, err = CompilationView(&subset)
		if err != nil {
			t.Fatal(err)
		}
		if len(current.valueTypes.injected) != len(current.Files)-1 {
			t.Fatal("projection metadata retained discarded dependency files")
		}
	}
}

func TestGenericArgumentsAreInvariant(t *testing.T) {
	for _, source := range []string{
		`type Box<T> { value: T } fn widen(value: Box<Int>): Box<Int?> { return value }`,
		`type Box<T> { value: T } type Wrap<T> { value: Box<T> } fn widen(value: Wrap<Int>): Wrap<Int?> { return value }`,
	} {
		expectError(t, analyze(t, source), "return expects")
	}
	expectNoErrors(t, analyze(t, `type Box<T> { value: T } fn nullable(value: Box<Int>): Box<Int>? { return value }`))
}

func TestGenericDiagnosticsIncludeArgumentShapes(t *testing.T) {
	result := analyze(t, `type Box<T> { value: T } fn widen(value: Box<Int>): Box<Int?> { return value }`)
	expectError(t, result, "Box<Int?>")
	expectError(t, result, "Box<Int>")
}

func TestBuiltinGenericOwnershipIsIndependentOfInputOrder(t *testing.T) {
	for _, order := range [][]string{{"beta", "alpha"}, {"alpha", "beta"}} {
		var files []*ast.File
		for _, name := range order {
			files = append(files, parseFileWithName(t, `fn `+name+`(value: Page<Int>): Int { return value.total }`, "origin/"+name+"/main.luxo"))
		}
		source := AnalyzeModules(files)
		expectNoErrors(t, source)
		view, err := CompilationView(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range view.Files {
			if len(file.Types) != 0 && file.Name != "origin/common/luxo_value_types.luxo" {
				t.Fatal("builtin ownership depends on source ordering", file.Name)
			}
		}
	}
}

func BenchmarkGenericCompilationView(b *testing.B) {
	for _, generic := range []bool{false, true} {
		for _, count := range []int{1, 64} {
			var source strings.Builder
			typeName := "Int"
			if generic {
				source.WriteString("type Box<T> { value: T }\n")
				typeName = "Box<Int>"
			}
			for index := range count {
				fmt.Fprintf(&source, "fn read%d(value: %s): %s { return value }\n", index, typeName, typeName)
			}
			analysis := Analyze([]*ast.File{parseFile(b, source.String())})
			if len(analysis.Errors) != 0 {
				b.Fatal(analysis.Errors)
			}
			b.Run(fmt.Sprintf("generic=%v/count=%d", generic, count), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if view, err := CompilationView(analysis); err != nil || view == nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func TestGenericCompilationViewOrdinaryAndInvalidSources(t *testing.T) {
	ordinary := analyze(t, `fn read(value: Int): Int { return value }`)
	view, err := CompilationView(ordinary)
	if err != nil || view != ordinary {
		t.Fatal("ordinary source should reuse its immutable analysis", err)
	}
	if view, err := CompilationView(nil); err == nil || view != nil {
		t.Fatal("nil source must return an error")
	}
	invalid := analyze(t, `type Box<T> { value: T } fn read(value: Box): Int { return 1 }`)
	if view, err := CompilationView(invalid); err == nil || view != nil {
		t.Fatal("invalid source must not produce a compilation view")
	}
	unused := analyze(t, `type Box<T> { value: T } fn read(value: Int): Int { return value }`)
	view, err = CompilationView(unused)
	if err != nil || len(view.Files[0].Types) != 0 {
		t.Fatal("unused templates must not emit free type parameters", err)
	}
	if again, err := CompilationView(view); err != nil || again != view {
		t.Fatal("unused templates should reuse their complete compilation", err)
	}
}

type unknownCompilationExpr struct{ ast.Expr }

func TestGenericCompilationViewRejectsUnknownASTWithoutPartialOutput(t *testing.T) {
	source := analyze(t, `type Box<T> { value: T } fn read(value: Box<Int>): Int { return value.value }`)
	source.Files[0].Functions[0].Body.Stmts = append(source.Files[0].Functions[0].Body.Stmts, &ast.ExprStmt{Expr: unknownCompilationExpr{}})
	if view, err := CompilationView(source); err == nil || view != nil {
		t.Fatal("unknown AST node exposed a partial compilation")
	}
}

func TestCompiledValueMetadataAccessorsOwnTheirMaps(t *testing.T) {
	var absent *Result
	if absent.RequiresTypeCompilation() || absent.ConcreteTypeOrigins() != nil || absent.CompiledValueDeclarations() != nil || absent.CompiledSealedDeclarations() != nil || absent.CompiledValueEvents() != nil {
		t.Fatal("nil result exposes compilation metadata")
	}
	ordinary := analyze(t, `fn read(value: Int): Int { return value }`)
	if ordinary.RequiresTypeCompilation() || ordinary.ConcreteTypeOrigins() != nil || ordinary.CompiledValueDeclarations() != nil || ordinary.CompiledSealedDeclarations() != nil || ordinary.CompiledValueEvents() != nil {
		t.Fatal("ordinary analysis allocated compilation metadata")
	}
	source := analyze(t, `type Box<T> { value: T } sealed Outcome { Done(value: Box<Int>) } event Ready(value: Box<Int>) fn read(value: Box<Int>): Int { return value.value }`)
	expectNoErrors(t, source)
	if !source.RequiresTypeCompilation() || source.ConcreteTypeOrigins() != nil || source.CompiledValueDeclarations() != nil || source.CompiledSealedDeclarations() != nil || source.CompiledValueEvents() != nil {
		t.Fatal("source analysis exposes premature concrete metadata")
	}
	view, err := CompilationView(source)
	if err != nil {
		t.Fatal(err)
	}
	name := view.Files[0].Functions[0].Params[0].Type.Name
	view.ConcreteTypeOrigins()[name] = "changed"
	delete(view.CompiledValueDeclarations(), name)
	delete(view.CompiledSealedDeclarations(), "Outcome")
	delete(view.CompiledValueEvents(), "Ready")
	if view.ConcreteTypeOrigins()[name] != "Box" || view.CompiledValueDeclarations()[name].Declaration == nil || view.CompiledSealedDeclarations()["Outcome"].Declaration == nil || view.CompiledValueEvents()["Ready"] == nil {
		t.Fatal("mutating an accessor map changed the compilation catalog")
	}
}

func TestGenericCompilationRejectsInvalidSourceGraphsWithoutPartialOutput(t *testing.T) {
	const basic = `type Box<T> { value: T } fn read(value: Box<Int>): Box<Int> { return value }`
	for _, sample := range []struct {
		name, source string
		mutate       func(*Result)
	}{
		{"argument count", basic, func(result *Result) {
			ref := result.Files[0].Functions[0].Params[0].Type
			ref.TypeArgs = append(ref.TypeArgs, &ast.TypeRef{Name: "String"})
		}},
		{"nil type argument", basic, func(result *Result) {
			result.Files[0].Functions[0].Params[0].Type.TypeArgs[0] = nil
		}},
		{"sealed constructor metadata", `sealed Outcome { Done(value: Int) } fn make(): Outcome { return Outcome.Done(1) }`, func(result *Result) {
			ast.WalkExprs(result.Files[0].Functions[0].Body, func(expression ast.Expr) {
				if call, ok := expression.(*ast.CallExpr); ok && call.ConstructorType != nil {
					call.ConstructorType.Name = "Missing"
				}
			})
		}},
		{"cyclic reference", basic, func(result *Result) {
			ref := result.Files[0].Functions[0].Params[0].Type.TypeArgs[0]
			ref.TypeArgs = []*ast.TypeRef{ref}
		}},
		{"template expression", basic, func(result *Result) {
			result.Files[0].Types[0].Fields[0].Default = unknownCompilationExpr{}
		}},
		{"expression metadata", basic, func(result *Result) {
			for _, typ := range result.GenericExprTypes {
				typ.TypeArgs = append(typ.TypeArgs, result.Types["String"])
			}
		}},
		{"nested channel argument", `type Box<T> { value: T } fn read(value: Channel<Box<Int>>): Int { return 1 }`, func(result *Result) {
			ref := result.Files[0].Functions[0].Params[0].Type.TypeArgs[0]
			ref.TypeArgs = append(ref.TypeArgs, &ast.TypeRef{Name: "String"})
		}},
		{"list composition", `type Box<T> { values: [T] } fn read(value: Box<Int>): Int { return 1 }`, func(result *Result) {
			result.Files[0].Functions[0].Params[0].Type.TypeArgs[0].Nullable = true
		}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			source := analyze(t, sample.source)
			expectNoErrors(t, source)
			sample.mutate(source)
			if view, err := CompilationView(source); err == nil || view != nil {
				t.Fatal("invalid source graph exposed a partial compilation", err)
			}
		})
	}
}

func TestGenericCompilationReportsCanonicalNameCollision(t *testing.T) {
	const basic = `type Box<T> { value: T } fn read(value: Box<Int>): Int { return value.value }`
	first, err := CompilationView(analyze(t, basic))
	if err != nil {
		t.Fatal(err)
	}
	name := first.Files[0].Functions[0].Params[0].Type.Name
	source := analyze(t, basic+" type "+name+" { value: String }")
	expectNoErrors(t, source)
	if view, err := CompilationView(source); err == nil || view != nil || !strings.Contains(err.Error(), "conflicts with a declaration") {
		t.Fatal("canonical collision lost its diagnostic", err)
	}
}

func TestGenericCompilationRebindsTemplateTransformTypes(t *testing.T) {
	source := analyze(t, `type Box<T> { value: T @transform { it } } fn read(value: Box<Int>): Int { return value.value }`)
	expectNoErrors(t, source)
	view, err := CompilationView(source)
	if err != nil {
		t.Fatal(err)
	}
	old := source.Files[0].Types[0].Fields[0].Directives[0].Body.Stmts[0].(*ast.ExprStmt).Expr
	concrete := view.Files[0].Types[0].Fields[0].Directives[0].Body.Stmts[0].(*ast.ExprStmt).Expr
	if old.GetTypeTag() != "T" || concrete.GetTypeTag() != "Int" || view.GenericExprTypes[concrete].Name != "Int" {
		t.Fatal("template transform retained free type parameters or mutated its source")
	}
}

func TestPublicCompilationViewKeepsLocalChannelTuple(t *testing.T) {
	result := AnalyzeModules([]*ast.File{parseFileWithName(t, `type Box<T> { value: T }
fn relay(value: Channel<(Int, String)>): Channel<(Int, String)> { return value }`, "bounded.luxo")})
	expectNoErrors(t, result)
	original := result.Files[0].Functions[0].Body.Stmts[0].(*ast.ReturnStmt).Value
	view, err := CompilationView(result)
	if err != nil || view == nil {
		t.Fatalf("compilation view rejected local channel metadata: %v", err)
	}
	returned := view.Files[0].Functions[0].Body.Stmts[0].(*ast.ReturnStmt).Value
	typ := view.GenericExprTypes[returned]
	if returned == original || typ == nil || typ.Name != "Channel" || len(typ.TypeArgs) != 1 || len(typ.TypeArgs[0].Tuple) != 2 || typ.TypeArgs[0].Tuple[0].Name != "Int" || typ.TypeArgs[0].Tuple[1].Name != "String" {
		t.Fatalf("local channel tuple metadata was lost: %+v", typ)
	}
	if len(result.GenericExprTypes[original].TypeArgs[0].Tuple) != 2 {
		t.Fatal("compilation changed the source tuple metadata")
	}
}

func TestPublicCompilationViewRejectsSubstitutedDepth(t *testing.T) {
	nested := strings.Repeat("Phantom<", 126) + "Int" + strings.Repeat(">", 126)
	result := AnalyzeModules([]*ast.File{parseFileWithName(t, `type Phantom<T> { marker: Int }
type Wrapper<T> { marker: Int }
type Holder<T> { value: Wrapper<T> }
fn read(value: Holder<`+nested+`>): Int { return 1 }`, "bounded.luxo")})
	expectNoErrors(t, result)
	view, err := CompilationView(result)
	if view != nil || err == nil || !strings.Contains(err.Error(), "exceeds 128 levels") {
		t.Fatalf("substitution depth must return its error without a partial view: %v", err)
	}
	if result.Files[0].Functions[0].Params[0].Type.Name != "Holder" || result.Files[0].Types[2].Fields[0].Type.TypeArgs[0].Name != "T" {
		t.Fatal("failed compilation mutated source references")
	}
}

func TestCompilationViewPreservesTupleFieldArgumentsAndProjection(t *testing.T) {
	files := []*ast.File{
		parseFileWithName(t, `type Holder { pair: (Int, String) } sealed Marker { Empty }`, "origin/common/types.luxo"),
		parseFileWithName(t, `fn build(pair: (Int, String)): Holder { return Holder { pair: pair } }`, "origin/alpha/main.luxo"),
	}
	source := AnalyzeModules(files)
	expectNoErrors(t, source)
	original := files[1].Functions[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.ObjectExpr).Fields[0]
	full, err := CompilationView(source)
	if err != nil {
		t.Fatal(err)
	}
	compiled := full.Files[1].Functions[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.ObjectExpr).Fields[0]
	if compiled.ExpectedType == nil || compiled.ExpectedType == original.ExpectedType || len(compiled.ExpectedType.Tuple) != 2 || compiled.ExpectedType.Tuple[0].Name != "Int" || compiled.ExpectedType.Tuple[1].Name != "String" {
		t.Fatal("compilation lost independently owned tuple argument metadata")
	}
	selected := *full
	selected.Files = full.Files[1:2]
	projected, err := CompilationView(&selected)
	if err != nil {
		t.Fatal(err)
	}
	holders := 0
	for _, file := range projected.Files {
		for _, declaration := range file.Types {
			if declaration.Name != "Holder" {
				continue
			}
			holders++
			if file.Name != files[0].Name || len(declaration.Fields[0].Type.Tuple) != 2 {
				t.Fatal("module projection lost the ordinary tuple field or its canonical owner")
			}
		}
	}
	if holders != 1 || len(selected.Files) != 1 || len(source.Files) != 2 || len(original.ExpectedType.Tuple) != 2 {
		t.Fatal("projection changed source metadata, selection, or dependency uniqueness")
	}
}

func TestCompilationViewBindsSealedArgumentsInVisibleGenericTemplate(t *testing.T) {
	source := AnalyzeModules([]*ast.File{parseFileWithName(t, `
sealed Choice { Value(value: Int) Empty }
type Box<T> { value: T? @visible(Choice.Value(1) == Choice.Value(1)) }
fn keep(value: Box<Int>): Int? { return value.value }
`, "visible-template.luxo")})
	expectNoErrors(t, source)
	before, err := json.Marshal(source.Files)
	if err != nil {
		t.Fatal(err)
	}
	view, err := CompilationView(source)
	if err != nil {
		t.Fatalf("legal visible generic template failed compilation: %v", err)
	}
	expected := view.Files[0].Sealeds[0].Variants[0].Fields[0].Type
	name := view.Files[0].Functions[0].Params[0].Type.Name
	declaration := view.CompiledValueDeclarations()[name].Declaration
	if declaration == nil || len(declaration.Fields) != 1 || !declaration.Fields[0].Type.Nullable {
		t.Fatal("concrete template lost its nullable field")
	}
	constructors := 0
	ast.WalkExpr(declaration.Fields[0].Directives[0].SingleExpression(), func(expression ast.Expr) {
		call, ok := expression.(*ast.CallExpr)
		if !ok || call.ConstructorType == nil {
			return
		}
		constructors++
		if len(call.Args) != 1 || call.Args[0].ExpectedType != expected || expected.Name != "Int" || expected.Nullable {
			t.Error("template constructor did not retain the root compilation's canonical payload type")
		}
	})
	if constructors != 2 {
		t.Fatalf("visible condition lost its sealed constructors: %d", constructors)
	}
	after, err := json.Marshal(source.Files)
	if err != nil || string(after) != string(before) {
		t.Fatal("compilation changed the original analyzed source", err)
	}
}
