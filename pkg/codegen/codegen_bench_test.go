package codegen

import (
	"fmt"
	"go/types"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func BenchmarkGenerateQueryOrder(b *testing.B) {
	baseline := os.Getenv("LUXO_QUERY_ORDER_BASELINE") == "1"
	for _, sample := range []struct {
		name, query, fields, scope string
		beforeError                bool
	}{
		{name: "direct", query: `Item.orderBy(id.asc).all()`},
		{name: "named", query: `Item.findMany(order: id.asc)`},
		{name: "multi", query: `Item.orderBy(id.asc, name.desc).all()`},
		{name: "managed", query: `Item.orderBy(createdAt.desc).all()`},
		{name: "field-direction", query: `Item.orderBy(it.asc).all()`},
		{name: "field-it", fields: `it: Int`, query: `Item.orderBy(it.desc).all()`},
		{name: "scope", scope: `scope ordered = where(id > 0).orderBy(id.desc)`, query: `Item.all()`},
		{name: "scope-root", scope: `scope ordered = orderBy(id.desc)`, query: `Item.all()`, beforeError: true},
	} {
		b.Run(sample.name, func(b *testing.B) {
			directives := ""
			if sample.scope != "" {
				directives = "@scope(ordered)"
			}
			input := analyzePermissionFixture(b, `model Item @soft { id: Int @id name: String asc: Int desc: Int `+sample.fields+` `+sample.scope+` } api sorted(): [Item] `+directives+` { return `+sample.query+` }`)
			if len(input.Errors) != 0 {
				b.Fatal(input.Errors)
			}
			fields := map[string]int{"id": 1, "name": 2, "asc": 3, "desc": 4}
			if sample.fields != "" {
				fields["it"] = 5
			}
			ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Item": fields}})
			generator, err := NewGenerator(GeneratorConfig{Driver: DriverPG, IDs: ids})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				generated, err := generator.Generate(input, "generated", nil)
				if baseline && sample.beforeError {
					if err == nil || generated != nil {
						b.Fatal("baseline unexpectedly accepted a root order scope")
					}
				} else if err != nil || generated == nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkParameterBindings(b *testing.B) {
	for _, sample := range []struct {
		name   string
		prefix string
		count  int
	}{
		{"ordinary-1", "value", 1},
		{"ordinary-128", "value", 128},
		{"reserved-1", "_value", 1},
		{"reserved-128", "_value", 128},
		{"predeclared-1", "", 1},
		{"predeclared-all", "", 0},
	} {
		b.Run(sample.name, func(b *testing.B) {
			names := []string{"nil"}
			if sample.prefix != "" {
				names = make([]string, sample.count)
				for i := range names {
					names[i] = fmt.Sprintf("%s%d", sample.prefix, i)
				}
			} else if sample.count == 0 {
				names = slices.DeleteFunc(types.Universe.Names(), func(name string) bool {
					return token.LookupKeyword(name) != token.Ident
				})
			}
			params := make([]*ast.ParamDecl, len(names))
			for i, name := range names {
				params[i] = &ast.ParamDecl{Name: name, Type: &ast.TypeRef{Name: "Int"}}
			}
			b.ReportAllocs()
			for b.Loop() {
				_ = parameterBindings(params, nil)
			}
		})
	}
}

func BenchmarkFunctionMemberGeneration(b *testing.B) {
	for _, count := range []int{1, 128} {
		for _, collision := range []bool{false, true} {
			b.Run(fmt.Sprintf("functions%d/collision%t", count, collision), func(b *testing.B) {
				var source strings.Builder
				for index := range count {
					name := fmt.Sprintf("work%d", index)
					if collision && index == 0 {
						name = "Close"
					}
					fmt.Fprintf(&source, "fn %s(): Int { return 42 }\n", name)
				}
				input := analyzePermissionFixture(b, source.String())
				if len(input.Errors) != 0 {
					b.Fatal(input.Errors)
				}
				generator := defaultGenerator()
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if generated, err := generator.Generate(input, "generated", nil); err != nil || len(generated.Files) == 0 {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkGeneratedPackageSymbols(b *testing.B) {
	var models strings.Builder
	for index := range 128 {
		fmt.Fprintf(&models, "model Record%d @noTime { id: Int @id name: String }\n", index)
	}
	for _, sample := range []struct{ name, source string }{
		{"one-model", "model Record @noTime { id: Int @id name: String }"},
		{"models128", models.String()},
		{"native-stream", "api read(value: Int): Int @native\napi watch(value: Int): Int @stream @native"},
	} {
		b.Run(sample.name, func(b *testing.B) {
			input := analyzePermissionFixture(b, sample.source)
			if len(input.Errors) != 0 {
				b.Fatal(input.Errors)
			}
			ids := StableIDs{ModelFields: map[string]map[string]int{}}
			for _, file := range input.Files {
				for _, model := range file.Models {
					ids.ModelFields[model.Name] = map[string]int{"id": 1, "name": 2}
				}
			}
			config := GeneratorConfig{IDs: completeFixtureAPIIDs(input, ids)}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := Generate(input, "generated", config); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkGenerateLambdaBoundary(b *testing.B) {
	for _, sample := range []struct{ name, source string }{
		{"scalar-control", "api run(value: Int): Int { return value + 1 }"},
		{"static-collection", "api run(value: [Int]): [Int] { return value.map { uint64 -> uint64 + 1 }.filter { it > 0 } }"},
		{"untyped-ast", "api run(value: Int): Int { return value }"},
	} {
		b.Run(sample.name, func(b *testing.B) {
			input := analyzePermissionFixture(b, sample.source)
			if len(input.Errors) != 0 {
				b.Fatal(input.Errors)
			}
			invalid := sample.name == "untyped-ast"
			if invalid {
				input.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value = &ast.LambdaExpr{
					Params: []string{"value"}, Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: &ast.Ident{Name: "value"}}}},
				}
			}
			config := GeneratorConfig{IDs: completeFixtureAPIIDs(input, StableIDs{APIs: map[string]int{"run": 1}})}
			b.ReportAllocs()
			for b.Loop() {
				result, err := Generate(input, "generated", config)
				if (!invalid && err != nil) || (result == nil) != (err != nil) {
					b.Fatalf("inconsistent generation output=%t error=%v", result != nil, err)
				}
			}
		})
	}
}

func BenchmarkWhereCondition(b *testing.B) {
	for _, sample := range []struct {
		name  string
		value ast.Expr
	}{
		{"integer", &ast.Literal{Kind: token.Int, Value: "42"}},
		{"text", &ast.Literal{Kind: token.String, Value: "OPEN"}},
		{"enum-literal", &ast.MemberExpr{Object: &ast.Ident{Name: "State"}, Field: "OPEN"}},
		{"enum-parameter", &ast.Ident{Name: "state"}},
	} {
		b.Run(sample.name, func(b *testing.B) {
			c := newCompiler(nil)
			c.enums = map[string]bool{"State": true}
			c.vars["state"] = valType{name: "State"}
			condition := &ast.BinaryExpr{Left: &ast.Ident{Name: "value"}, Op: "==", Right: sample.value}
			b.ReportAllocs()
			for b.Loop() {
				if c.compileWhereArg("Order", condition) == "" {
					b.Fatal("missing condition")
				}
			}
		})
	}
}

func BenchmarkPureQueryGeneration(b *testing.B) {
	c := newCompiler(makeModels("Record"))
	links := []chainLink{
		{method: "where", args: []*ast.NamedArg{{Name: "id", Value: &ast.MemberExpr{Object: &ast.Ident{Name: "source"}, Field: "id"}}}},
		{method: "where", args: []*ast.NamedArg{{Name: "value", Value: &ast.MemberExpr{Object: &ast.Ident{Name: "source"}, Field: "value"}}}},
		{method: "all"},
	}
	b.ReportAllocs()
	for b.Loop() {
		c.b.Reset()
		c.resultTmp = 0
		if c.compileModelChain("Record", links) == "" {
			b.Fatal("missing query")
		}
	}
}

func BenchmarkQueryFacadeSource(b *testing.B) {
	for _, name := range []string{"User", "R"} {
		for _, count := range []int{1, 128} {
			b.Run(fmt.Sprintf("%s/%d", name, count), func(b *testing.B) {
				names := make([]string, count)
				for i := range names {
					names[i] = fmt.Sprintf("%s%d", name, i)
				}
				names[0] = name
				fields := []*ast.FieldDecl{testField("id", "Int", directive("id")), testField("value", "Int")}
				b.ReportAllocs()
				for b.Loop() {
					var output strings.Builder
					for _, model := range names {
						defaultGenerator().generateQueryFacade(&output, model, fields, true)
					}
					if output.Len() == 0 {
						b.Fatal("missing query facade")
					}
				}
			})
		}
	}
}

func BenchmarkFindRead(b *testing.B) {
	model := testModel("Probe", nil, []*ast.FieldDecl{testField("id", "Int", directive("id"))})
	compiler := newCompiler(map[string]*ast.ModelDecl{"Probe": model})
	call := &ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Probe"}, Field: "find"},
		Args: []*ast.NamedArg{{Name: "id", Value: &ast.Ident{Name: "key"}}}}
	b.ReportAllocs()
	for b.Loop() {
		if compiler.compileCall(call) != "app.Probe.Where(ProbeWhere.Id.Eq(key)).FirstRequired(ctx)" {
			b.Fatal("unexpected primary key query")
		}
	}
}

func buildBenchResult() *semantic.Result {
	file := &ast.File{
		Name: "bench.luxo",
		Enums: []*ast.EnumDecl{
			{Name: "Role", Values: []string{"USER", "ADMIN", "MODERATOR"}},
			{Name: "OrderStatus", Values: []string{"PENDING", "PAID", "SHIPPED", "COMPLETED"}},
		},
		Models: []*ast.ModelDecl{
			{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "UUID"}, Directives: []*ast.Directive{{Name: "auto"}}},
					{Name: "name", Type: &ast.TypeRef{Name: "String"}},
					{Name: "email", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "unique"}}},
					{Name: "password", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hidden"}}},
					{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
					{Name: "bio", Type: &ast.TypeRef{Name: "String", Nullable: true}},
					{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
					{Name: "updatedAt", Type: &ast.TypeRef{Name: "DateTime"}},
				},
			},
			{
				Name: "Post",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "serial"}}},
					{Name: "title", Type: &ast.TypeRef{Name: "String"}},
					{Name: "content", Type: &ast.TypeRef{Name: "String"}},
					{Name: "published", Type: &ast.TypeRef{Name: "Boolean"}},
					{Name: "authorId", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "immutable"}}},
					{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
					{Name: "updatedAt", Type: &ast.TypeRef{Name: "DateTime"}},
				},
			},
			{
				Name: "Order",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "serial"}}},
					{Name: "userId", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "immutable"}}},
					{Name: "status", Type: &ast.TypeRef{Name: "OrderStatus"}},
					{Name: "total", Type: &ast.TypeRef{Name: "Decimal"}},
					{Name: "note", Type: &ast.TypeRef{Name: "String", Nullable: true}},
					{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
				},
			},
		},
	}
	return &semantic.Result{Files: []*ast.File{file}}
}

func BenchmarkEnumSymbolValidation(b *testing.B) {
	for _, count := range []int{1, 16, 128} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			context, _ := sharedEnumBenchmarkFixture(count)
			declarations := make([]*ast.EnumDecl, 0, count)
			for _, declaration := range context.Enums {
				declarations = append(declarations, declaration)
			}
			slices.SortFunc(declarations, func(a, c *ast.EnumDecl) int { return strings.Compare(a.Name, c.Name) })
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				var names enumSymbols
				for _, declaration := range declarations {
					if err := names.add(declaration); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func BenchmarkSharedEnumGeneration(b *testing.B) {
	for _, count := range []int{1, 16, 128} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			context, input := sharedEnumBenchmarkFixture(count)
			generator := mustNewGenerator(b, GeneratorConfig{Events: context})
			b.Run("snapshot", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					_ = cloneEventContext(context)
				}
			})
			b.Run("dependencies", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					output := generator.withEnumDependencies(input)
					if len(output.Files) != count+1 {
						b.Fatal("missing enum dependencies")
					}
				}
			})
			b.Run("shared-source", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := generator.GenerateSharedEnums(); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

func sharedEnumBenchmarkFixture(count int) (*EventContext, *semantic.Result) {
	context := &EventContext{
		EnumModule: make(map[string]string, count), Enums: make(map[string]*ast.EnumDecl, count),
		SharedEnumPackage: "example.com/app/internal/luxotypes",
	}
	model := &ast.ModelDecl{Name: "Record"}
	for index := range count {
		name := fmt.Sprintf("State%d", index)
		context.EnumModule[name] = "common"
		context.Enums[name] = &ast.EnumDecl{Name: name, Values: []string{"OPEN", "CLOSED"}}
		model.Fields = append(model.Fields, &ast.FieldDecl{Name: fmt.Sprintf("field%d", index), Type: &ast.TypeRef{Name: name}})
	}
	return context, &semantic.Result{Files: []*ast.File{{Name: "origin/records/model.luxo", Models: []*ast.ModelDecl{model}}}}
}

func BenchmarkGenerate(b *testing.B) {
	result := buildBenchResult()
	fields := make(map[string]map[string]int)
	for _, model := range result.Files[0].Models {
		fields[model.Name] = make(map[string]int)
		for i, field := range model.Fields {
			fields[model.Name][field.Name] = i + 1
		}
	}
	config := GeneratorConfig{Driver: DriverPG, IDs: StableIDs{ModelFields: fields}}
	b.ResetTimer()
	for b.Loop() {
		if _, err := Generate(result, "gen", config); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGenerateJSONDepth(b *testing.B) {
	for _, sample := range []struct {
		name, fieldType string
		count           int
	}{{"json1", "JSON", 1}, {"json32", "JSON", 32}, {"int1-control", "Int", 1}} {
		b.Run(sample.name, func(b *testing.B) {
			var source strings.Builder
			source.WriteString("type Packet {\n")
			fields := make(map[string]int, sample.count)
			for index := range sample.count {
				name := fmt.Sprintf("value%d", index)
				fmt.Fprintf(&source, "%s: %s\n", name, sample.fieldType)
				fields[name] = index + 1
			}
			source.WriteString("}\napi echo(input: Packet): Packet { return input }")
			input := analyzePermissionFixture(b, source.String())
			if len(input.Errors) != 0 {
				b.Fatal(input.Errors)
			}
			config := GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"echo": 1}, ModelFields: map[string]map[string]int{"Packet": fields}}}
			config.IDs = completeFixtureAPIIDs(input, config.IDs)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Generate(input, "generated", config); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// The previous boundary formatted the same entry twice. This comparison keeps
// validation and emission identical and restores only that redundant pass.
func BenchmarkCheckedEntryFormattingComparison(b *testing.B) {
	result := &semantic.Result{Files: []*ast.File{{Name: "origin/user.luxo", Models: []*ast.ModelDecl{{Name: "User"}}}}}
	for name, generate := range map[string]func() ([][]byte, error){
		"embedded": func() ([][]byte, error) {
			source, err := GenerateEntryFileChecked(result, "example.com/app")
			return [][]byte{source}, err
		},
		"gateway": func() ([][]byte, error) {
			source, err := GenerateGatewayEntryChecked(result, "example.com/app")
			return [][]byte{source}, err
		},
		"module": func() ([][]byte, error) {
			sources, err := GenerateModuleEntryFilesChecked(result, "example.com/app")
			return [][]byte{sources["user"]}, err
		},
	} {
		b.Run(name, func(b *testing.B) {
			for _, mode := range []string{"previous", "current"} {
				b.Run(mode, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						sources, err := generate()
						if err != nil {
							b.Fatal(err)
						}
						if mode == "previous" {
							for _, source := range sources {
								if _, err := formatGeneratedChecked(source); err != nil {
									b.Fatal(err)
								}
							}
						}
					}
				})
			}
		})
	}
}

func BenchmarkGenerateModelFile(b *testing.B) {
	result := buildBenchResult()
	b.ResetTimer()
	for b.Loop() {
		generateModelFile(result, "gen", nil)
	}
}

func BenchmarkModelAuthImportGeneration(b *testing.B) {
	for _, kind := range []string{"ordinary", "auth", "auth-decimal"} {
		for _, count := range []int{1, 128} {
			b.Run(fmt.Sprintf("%s/%d", kind, count), func(b *testing.B) {
				inputKind := kind
				if kind == "auth-decimal" {
					inputKind = "auth"
				}
				input, generator := generatedMethodBenchmarkInput(b, inputKind, count)
				if kind == "auth-decimal" {
					for _, model := range input.Files[0].Models {
						model.Fields[0].Type.Name = "Decimal"
					}
				}
				b.ReportAllocs()
				for b.Loop() {
					generator.generateModelFile(input, "generated", nil)
				}
			})
		}
	}
}

// Exercise actual handler imports; the model-only fixture emits no handlers.
func BenchmarkGenerateHandlerFile(b *testing.B) {
	result := buildBenchResult()
	for _, model := range result.Files[0].Models {
		model.Directives = []*ast.Directive{{Name: "crud"}}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		generateHandlerFile(result, "gen", nil)
	}
}

func BenchmarkWhenBinding(b *testing.B) {
	for _, sample := range []struct {
		name, tag string
		list      bool
	}{
		{"scalar", "Int", false},
		{"list", "Int", true},
		{"modelList", "User", true},
		{"untyped", "", false},
	} {
		b.Run(sample.name, func(b *testing.B) {
			expression := &ast.WhenExpr{Else: &ast.Ident{Name: "fallback"}}
			expression.SetTypeTag(sample.tag)
			expression.SetListType(sample.list)
			binding := &ast.ValStmt{Name: "chosen", Value: expression}
			models := map[string]*ast.ModelDecl{"User": {Name: "User"}}
			declaration := &ast.ApiDecl{ReturnType: &ast.TypeRef{Name: "String"}}
			b.ReportAllocs()
			for b.Loop() {
				c := newCompiler(models)
				c.api = declaration
				c.compileVal(binding)
				if c.b.Len() == 0 || c.vars["chosen"].name == "" {
					b.Fatal("missing generated binding")
				}
			}
		})
	}
}

func BenchmarkLogicalExpression(b *testing.B) {
	for _, sample := range []struct {
		name, op string
		call     bool
		typed    bool
	}{
		{"pureAnd", "&&", false, false}, {"pureOr", "||", false, false},
		{"fallibleAnd", "&&", true, false}, {"fallibleOr", "||", true, false},
		{"typedAnd", "&&", false, true}, {"typedOr", "||", false, true},
	} {
		b.Run(sample.name, func(b *testing.B) {
			var right ast.Expr = &ast.Ident{Name: "fallback"}
			if sample.call {
				right = &ast.CallExpr{Func: &ast.Ident{Name: "check"}}
			}
			expression := &ast.BinaryExpr{Left: &ast.Ident{Name: "enabled"}, Op: sample.op, Right: right}
			if sample.typed {
				expression.Left.SetTypeTag("Boolean")
				expression.Right.SetTypeTag("Boolean")
			}
			functions := map[string]*ast.FnDecl{"check": {Name: "check", Body: &ast.Block{}, ReturnType: &ast.TypeRef{Name: "Boolean"}}}
			b.ReportAllocs()
			for b.Loop() {
				c := newCompiler(nil)
				c.functions = functions
				if sample.typed {
					c.vars["enabled"] = valType{name: "Boolean"}
					c.vars["fallback"] = valType{name: "Boolean"}
				}
				if c.compileExpr(expression) == "" {
					b.Fatal("missing expression")
				}
			}
		})
	}
}

func BenchmarkAPIParameterMetadata(b *testing.B) {
	for _, typeName := range []string{"Int", "State"} {
		b.Run(typeName, func(b *testing.B) {
			generator := mustNewGenerator(b, GeneratorConfig{IDs: StableIDs{
				APIs:      map[string]int{"echo": 1},
				APIParams: map[string]map[string]int{"echo": {"value": 1, "values": 2}},
			}})
			generator.requestParams = requestParamDeclarations(map[string]map[string]string{"echo": {"value": typeName, "values": "[" + typeName + "]?"}})
			enums := map[string]bool{"State": true}
			var sample strings.Builder
			generator.writeAPIRegistration(&sample, "echo", enums)
			want := `Type: "Int"`
			if typeName == "State" {
				want = `Type: "Enum", TypeName: "State"`
			}
			if !strings.Contains(sample.String(), want) {
				b.Fatal("incorrect parameter metadata")
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				var output strings.Builder
				generator.writeAPIRegistration(&output, "echo", enums)
			}
		})
	}
}

func BenchmarkGeneratorIdentifierValidation(b *testing.B) {
	for _, count := range []int{1, 128} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			ids := StableIDs{APIs: make(map[string]int), APIParams: make(map[string]map[string]int)}
			input := &semantic.Result{Files: []*ast.File{{}}}
			for i := range count {
				name := fmt.Sprintf("echo%d", i)
				ids.APIs[name] = i + 1
				ids.APIParams[name] = map[string]int{"value": 1}
				input.Files[0].APIs = append(input.Files[0].APIs, &ast.ApiDecl{
					Name: name, Params: []*ast.ParamDecl{{Name: "value", Type: &ast.TypeRef{Name: "Int"}}},
				})
			}
			g := mustNewGenerator(b, GeneratorConfig{IDs: ids})
			b.Run("assignments", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if err := validateStableIDs(ids); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("active-parameters", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if err := g.validateRegisteredParameters(input, nil); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

func BenchmarkFieldIdentifierValidation(b *testing.B) {
	for _, count := range []int{1, 128} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			input := &semantic.Result{Files: []*ast.File{{}}}
			ids := StableIDs{ModelFields: make(map[string]map[string]int)}
			for i := range count {
				name := fmt.Sprintf("Model%d", i)
				model := &ast.ModelDecl{Name: name}
				ids.ModelFields[name] = make(map[string]int)
				for j := range 16 {
					field := fmt.Sprintf("field%d", j)
					model.Fields = append(model.Fields, &ast.FieldDecl{Name: field, Type: &ast.TypeRef{Name: "Int"}})
					ids.ModelFields[name][field] = j + 1
				}
				input.Files[0].Models = append(input.Files[0].Models, model)
			}
			g := mustNewGenerator(b, GeneratorConfig{IDs: ids})
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err := g.validateFieldIdentifiers(input, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkScalarSignatureImports(b *testing.B) {
	for _, count := range []int{1, 16} {
		body := strings.Repeat("Read(ctx context.Context, value json.RawMessage, date time.Time) (uuid.UUID, decimal.Decimal)\n", count)
		var before, after strings.Builder
		writeNativeScalarImportsBaseline(&before, body)
		writeScalarTypeImports(&after, body)
		if before.String() != after.String() {
			b.Fatal("comparable signature imports changed")
		}
		for _, side := range []struct {
			name  string
			write func(*strings.Builder, string)
		}{{"before-native", writeNativeScalarImportsBaseline}, {"shared", writeScalarTypeImports}} {
			b.Run(fmt.Sprintf("%d/%s", count, side.name), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					var out strings.Builder
					side.write(&out, body)
				}
			})
		}
	}
}

// Frozen native-signature import lookup; unlike the new scan it ignores syntax.
func writeNativeScalarImportsBaseline(b *strings.Builder, body string) {
	for _, imp := range []struct{ qualifier, path string }{
		{"json.", "encoding/json"}, {"time.", "time"},
		{"uuid.", "github.com/google/uuid"}, {"decimal.", "github.com/shopspring/decimal"},
	} {
		if strings.Contains(body, imp.qualifier) {
			fmt.Fprintf(b, "\t%q\n", imp.path)
		}
	}
}

func BenchmarkSoftDeleteIdentifiers(b *testing.B) {
	for _, kind := range []string{"ordinary", "implicit", "explicit"} {
		for _, count := range []int{1, 128} {
			b.Run(fmt.Sprintf("%s/%d", kind, count), func(b *testing.B) {
				file := &ast.File{Name: "soft.luxo"}
				ids := StableIDs{ModelFields: make(map[string]map[string]int)}
				for i := range count {
					model := &ast.ModelDecl{Name: fmt.Sprintf("Entry%d", i), Directives: []*ast.Directive{{Name: "noTime"}}}
					if kind != "ordinary" {
						model.Directives = append(model.Directives, &ast.Directive{Name: "soft"})
					}
					ids.ModelFields[model.Name] = make(map[string]int)
					for j := range 16 {
						field := &ast.FieldDecl{Name: fmt.Sprintf("field%d", j), Type: &ast.TypeRef{Name: "Int"}}
						if kind == "explicit" && j == 15 {
							field.Name = "deletedAt"
							field.Type = &ast.TypeRef{Name: "DateTime", Nullable: true}
						}
						model.Fields = append(model.Fields, field)
						ids.ModelFields[model.Name][field.Name] = j + 1
					}
					file.Models = append(file.Models, model)
				}
				g := mustNewGenerator(b, GeneratorConfig{IDs: ids})
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if err := g.validateFieldIdentifiers(&semantic.Result{Files: []*ast.File{file}}, nil); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkGeneratedMethodValidation(b *testing.B) {
	for _, kind := range []string{"ordinary", "hash", "auth", "event"} {
		for _, count := range []int{1, 128} {
			b.Run(fmt.Sprintf("%s/%d", kind, count), func(b *testing.B) {
				input, g := generatedMethodBenchmarkInput(b, kind, count)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if err := g.validateFieldIdentifiers(input, nil); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func generatedMethodBenchmarkInput(b *testing.B, kind string, count int) (*semantic.Result, *GeneratorContext) {
	file := &ast.File{Name: "names.luxo"}
	ids := StableIDs{ModelFields: make(map[string]map[string]int), EventFields: make(map[string]map[string]int)}
	for i := range count {
		model := &ast.ModelDecl{Name: fmt.Sprintf("Entry%d", i)}
		event := &ast.EventDecl{Name: model.Name}
		fields := make(map[string]int)
		for j := range 16 {
			field := &ast.FieldDecl{Name: fmt.Sprintf("field%d", j), Type: &ast.TypeRef{Name: "Int"}}
			if kind == "hash" && j == 0 {
				field.Type.Name = "String"
				field.Directives = []*ast.Directive{{Name: "hash"}}
			}
			fields[field.Name] = j + 1
			model.Fields = append(model.Fields, field)
			event.Params = append(event.Params, &ast.ParamDecl{Name: field.Name, Type: field.Type})
		}
		if kind == "auth" {
			model.Directives = []*ast.Directive{{Name: "withAuth", Args: []*ast.NamedArg{{Name: "stores", Value: &ast.ListExpr{Items: []ast.Expr{&ast.Ident{Name: "field0"}}}}}}}
		}
		if kind == "event" {
			file.Events = append(file.Events, event)
			ids.EventFields[model.Name] = fields
		} else {
			file.Models = append(file.Models, model)
			ids.ModelFields[model.Name] = fields
		}
	}
	return &semantic.Result{Files: []*ast.File{file}}, mustNewGenerator(b, GeneratorConfig{IDs: ids})
}

func BenchmarkIdentityConditionGeneration(b *testing.B) {
	for _, sample := range []struct {
		name, kind string
		right      ast.Expr
	}{
		{"string-literal", "String", &ast.Literal{Kind: token.String, Value: "member"}},
		{"string-parameter", "String", &ast.Ident{Name: "expected"}},
		{"integer-control", "Int", &ast.Literal{Kind: token.Int, Value: "42"}},
	} {
		field := &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "stored"}
		field.SetTypeTag(sample.kind)
		expression := &ast.BinaryExpr{Left: field, Op: "==", Right: sample.right}
		params := map[string]*ast.ParamDecl{"expected": {Name: "expected", Type: &ast.TypeRef{Name: sample.kind}}}
		b.Run(sample.name+"/visible", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if compileVisibleExpr(expression) == "" {
					b.Fatal("missing condition")
				}
			}
		})
		b.Run(sample.name+"/stream", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if output, ok := compileStreamExpr(expression, params, nil, ""); !ok || output == "" {
					b.Fatal("missing matcher")
				}
			}
		})
	}
}

func TestGeneratedSetterSlotBenchmarks(t *testing.T) {
	if os.Getenv("LUXO_GENERATED_BENCHMARK") == "" {
		t.Skip("generated benchmark fixture")
	}
	var source, assertions strings.Builder
	assertions.WriteString(`package generated
import("testing";"unsafe";"github.com/light-speak/luxo/pkg/lux";"github.com/light-speak/luxo/pkg/lux/pg")
`)
	ids := StableIDs{ModelFields: make(map[string]map[string]int)}
	for _, count := range []int{1, 16, 128} {
		model := fmt.Sprintf("Setter%d", count)
		fmt.Fprintf(&source, "model %s @noTime { id: Int @id @serial\n", model)
		fields := map[string]int{"id": 1}
		for i := 0; i < count; i++ {
			name := fmt.Sprintf("f%d", i)
			fmt.Fprintf(&source, "%s: Int\n", name)
			fields[name] = i + 2
		}
		source.WriteString("}\n")
		ids.ModelFields[model] = fields
		for _, kind := range []string{"Create", "Update"} {
			writeSetterBenchmarkTypes(&assertions, model, kind, count)
			for _, variant := range []string{"Legacy", "Static"} {
				for _, cold := range []bool{false, true} {
					writeSetterBenchmark(&assertions, model, kind, variant, count, cold)
				}
			}
		}
	}
	runGeneratedFixture(t, source.String(), assertions.String(), ids)
}

func setterBenchmarkBase(model, kind string) string {
	if kind == "Create" {
		return "pg.CreateBase[" + model + "]"
	}
	return "pg.UpdateBase[" + model + ",int64]"
}

func writeSetterBenchmarkTypes(out *strings.Builder, model, kind string, count int) {
	legacy := "Legacy" + model + kind + "Builder"
	fmt.Fprintf(out, "type %s struct{%s}\n", legacy, setterBenchmarkBase(model, kind))
	for i := 0; i < count; i++ {
		fmt.Fprintf(out, "func(b *%s)SetF%d(v int64)*%s{b.Set(%q,v);return b}\n", legacy, i, legacy, fmt.Sprintf("f%d", i))
	}
}

func writeSetterBenchmark(out *strings.Builder, model, kind, variant string, count int, cold bool) {
	typ := model + kind + "Builder"
	if variant == "Legacy" {
		typ = "Legacy" + typ
	}
	temperature := "Warm"
	if cold {
		temperature = "Cold"
	}
	name := model + kind + variant + temperature
	fmt.Fprintf(out, "var sink%s *%s\nfunc BenchmarkSetterSlot%s(b *testing.B){\n", name, typ, name)
	initial := typ + "{}"
	if !cold {
		fields := "Sets:sets[:0]"
		if kind == "Create" {
			fmt.Fprintf(out, "cols:=make([]string,0,%d);vals:=make([]any,0,%d)\n", count, count)
			fields = "Cols:cols[:0],Vals:vals[:0]"
		} else {
			fmt.Fprintf(out, "sets:=make([]lux.SetField,0,%d)\n", count)
		}
		initial = typ + "{" + kind + "Base:" + setterBenchmarkBase(model, kind) + "{" + fields + "}}"
		fmt.Fprintf(out, "var value %s\n", typ)
	}
	out.WriteString("b.ReportAllocs()\nfor b.Loop(){\n")
	if cold {
		fmt.Fprintf(out, "value:=&%s\n", initial)
	} else {
		fmt.Fprintf(out, "value=%s\n", initial)
	}
	for i := 0; i < count; i++ {
		fmt.Fprintf(out, "value.SetF%d(42)\n", i)
	}
	if cold {
		fmt.Fprintf(out, "sink%s=value\n", name)
	}
	out.WriteString("}\n")
	value := "value"
	if cold {
		value = "sink" + name
	}
	collection := "Sets"
	if kind == "Create" {
		collection = "Cols"
	}
	fmt.Fprintf(out, "if len(%s.%s)!=%d{b.Fatal(\"invalid assignments\")}\n", value, collection, count)
	fmt.Fprintf(out, "b.ReportMetric(float64(unsafe.Sizeof(%s{})),\"type-B\")\n}\n", typ)
}

// The built-in control lacks function failure semantics; it is not an equivalent
// previous implementation of the newly supported function-bearing annotation.
func TestGeneratedFallibleSaveBenchmarks(t *testing.T) {
	if os.Getenv("LUXO_GENERATED_BENCHMARK") == "" {
		t.Skip("generated benchmark fixture")
	}
	const source = `
fn normalize(value: String): String { if value == "bad" { throw error.NotFound } return value.trim() }
model Plain @noTime { id: Int @id name: String @beforeSave { it.trim() } }
model Checked @noTime { id: Int @id name: String @beforeSave { normalize(it) } }
`
	runGeneratedFixture(t, source, fallibleSaveBenchmarks, StableIDs{ModelFields: map[string]map[string]int{
		"Plain": {"id": 1, "name": 2}, "Checked": {"id": 1, "name": 2},
	}})
}

const fallibleSaveBenchmarks = `package generated
import("testing";"unsafe";"github.com/light-speak/luxo/pkg/lux";"github.com/light-speak/luxo/pkg/lux/pg")
var saveString string
var saveError error
var plainCreateSink *PlainCreateBuilder
var checkedCreateSink *CheckedCreateBuilder
func checkSaveBench(b *testing.B){
 b.Helper()
 if prepareSave5PlainName(" value ")!="value"{b.Fatal("invalid control")}
 if value,err:=prepareSave7CheckedName(" value ");err!=nil||value!="value"{b.Fatal(value,err)}
 if _,err:=prepareSave7CheckedName("bad");err==nil{b.Fatal("missing function error")}
}
func BenchmarkFallibleSavePrepare(b *testing.B){
 checkSaveBench(b)
 b.Run("Builtin",func(b *testing.B){b.ReportAllocs();for b.Loop(){saveString=prepareSave5PlainName(" value ")}})
 b.Run("Function",func(b *testing.B){b.ReportAllocs();for b.Loop(){saveString,saveError=prepareSave7CheckedName(" value ")}})
}
func BenchmarkFallibleSaveCreateWarm(b *testing.B){
 checkSaveBench(b)
 b.Run("Builtin",func(b *testing.B){
  target:=&PlainCreateBuilder{CreateBase:pg.CreateBase[Plain]{Cols:make([]string,0,1),Vals:make([]any,0,1)}}
  target.SetName(" value ");b.ReportAllocs();for b.Loop(){target.SetName(" value ")}
  if target.Vals[0]!="value"{b.Fatal(target.Vals)}
  b.ReportMetric(float64(unsafe.Sizeof(*target)),"type-B")
 })
 b.Run("Function",func(b *testing.B){
  target:=&CheckedCreateBuilder{CreateBase:pg.CreateBase[Checked]{Cols:make([]string,0,1),Vals:make([]any,0,1)}}
  target.SetName(" value ");b.ReportAllocs();for b.Loop(){target.SetName(" value ")}
  if target.Vals[0]!="value"||target._prepareErr!=nil{b.Fatal(target.Vals,target._prepareErr)}
  b.ReportMetric(float64(unsafe.Sizeof(*target)),"type-B")
 })
}
func BenchmarkFallibleSaveUpdateWarm(b *testing.B){
 checkSaveBench(b)
 b.Run("Builtin",func(b *testing.B){
  target:=&PlainUpdateBuilder{UpdateBase:pg.UpdateBase[Plain,int64]{Sets:make([]lux.SetField,0,1)}}
  target.SetName(" value ");b.ReportAllocs();for b.Loop(){target.SetName(" value ")}
  if target.Sets[0].Val!="value"{b.Fatal(target.Sets)}
  b.ReportMetric(float64(unsafe.Sizeof(*target)),"type-B")
 })
 b.Run("Function",func(b *testing.B){
  target:=&CheckedUpdateBuilder{UpdateBase:pg.UpdateBase[Checked,int64]{Sets:make([]lux.SetField,0,1)}}
  target.SetName(" value ");b.ReportAllocs();for b.Loop(){target.SetName(" value ")}
  if target.Sets[0].Val!="value"||target._prepareErr!=nil{b.Fatal(target.Sets,target._prepareErr)}
  b.ReportMetric(float64(unsafe.Sizeof(*target)),"type-B")
 })
}
func BenchmarkFallibleSaveCreateCold(b *testing.B){
 checkSaveBench(b)
 b.Run("Builtin",func(b *testing.B){b.ReportAllocs();for b.Loop(){plainCreateSink=(&PlainCreateBuilder{}).SetName(" value ")}})
 b.Run("Function",func(b *testing.B){b.ReportAllocs();for b.Loop(){checkedCreateSink=(&CheckedCreateBuilder{}).SetName(" value ")}})
}
func BenchmarkFallibleSaveBatchWarm(b *testing.B){
 checkSaveBench(b)
 b.Run("Builtin",func(b *testing.B){
  target:=&PlainCreateManyBuilder{rows:make([]PlainCreateInput,0,1)}
  b.ReportAllocs();for b.Loop(){target.rows=target.rows[:0];target.Add(PlainCreateInput{Id:1,Name:" value "})}
  if len(target.rows)!=1||target.rows[0].Name!="value"{b.Fatal(target.rows)}
  b.ReportMetric(float64(unsafe.Sizeof(*target)),"type-B")
 })
 b.Run("Function",func(b *testing.B){
  target:=&CheckedCreateManyBuilder{rows:make([]CheckedCreateInput,0,1)}
  b.ReportAllocs();for b.Loop(){target.rows=target.rows[:0];target.Add(CheckedCreateInput{Id:1,Name:" value "})}
  if len(target.rows)!=1||target.rows[0].Name!="value"||target._prepareErr!=nil{b.Fatal(target.rows,target._prepareErr)}
  b.ReportMetric(float64(unsafe.Sizeof(*target)),"type-B")
 })
}
func BenchmarkFallibleSaveRejected(b *testing.B){
 checkSaveBench(b)
 b.Run("Prepare",func(b *testing.B){b.ReportAllocs();for b.Loop(){saveString,saveError=prepareSave7CheckedName("bad")}})
 b.Run("BlockedSetter",func(b *testing.B){
  target:=(&CheckedCreateBuilder{}).SetName("bad");b.ReportAllocs()
  for b.Loop(){target.SetName(" value ")}
  if len(target.Cols)!=0||target._prepareErr==nil{b.Fatal("lost preparation failure")}
 })
 b.Run("BlockedExec",func(b *testing.B){
  target:=(&CheckedCreateBuilder{}).SetName("bad");ctx:=b.Context();b.ReportAllocs()
  for b.Loop(){_,saveError=target.Exec(ctx)}
  if saveError==nil{b.Fatal("lost preparation failure")}
 })
}
`

func TestGeneratedQueryFacadeBenchmarks(t *testing.T) {
	if os.Getenv("LUXO_GENERATED_BENCHMARK") == "" {
		t.Skip("generated benchmark fixture")
	}
	runGeneratedTransactionFixture(t, `model User @noTime { id: Int @id name: String @beforeSave { it.trim() } }`, queryFacadeBenchmarks)
}

const queryFacadeBenchmarks = `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/pg")
var queryReadSink *pg.Query[User]
var queryUpdateSink *UserQueryUpdateBuilder
var singleUpdateSink *UserUpdateBuilder
func BenchmarkQueryFacade(b *testing.B) {
 fields:=[]string{"id","name"};order:=[]string{"id"};client:=&UserClient{}
 b.Run("ColdRaw",func(b *testing.B){b.ReportAllocs();for b.Loop(){queryReadSink=pg.NewQuery[User](nil,"users",scanUser,nil).Select(fields...).OrderBy(order...).Limit(10).Offset(1)}})
 b.Run("ColdGenerated",func(b *testing.B){b.ReportAllocs();for b.Loop(){queryReadSink=(*pg.Query[User])(client.Where().Select(fields...).OrderBy(order...).Limit(10).Offset(1))}})
 b.Run("ReuseRaw",func(b *testing.B){q:=pg.NewQuery[User](nil,"users",scanUser,nil).OrderBy(order...);b.ReportAllocs();for b.Loop(){queryReadSink=q.Select(fields...).Limit(10).Offset(1)}})
 b.Run("ReuseGenerated",func(b *testing.B){q:=client.Where().OrderBy(order...);b.ReportAllocs();for b.Loop(){queryReadSink=(*pg.Query[User])(q.Select(fields...).Limit(10).Offset(1))}})
}
// Both update candidates use the same typed preparation and slot replacement.
// This compares the new bulk API with existing typed setters, not with raw SQL.
func BenchmarkQueryUpdatePreparation(b *testing.B){
 q:=(&UserClient{}).Where()
 b.Run("ColdSingle",func(b *testing.B){b.ReportAllocs();for b.Loop(){singleUpdateSink=newUserUpdateBuilder(nil,"users",0).SetName(" value ")};if singleUpdateSink.Sets[0].Val!="value"{b.Fatal(singleUpdateSink.Sets)}})
 b.Run("ColdBulk",func(b *testing.B){b.ReportAllocs();for b.Loop(){queryUpdateSink=q.Update().SetName(" value ")};if queryUpdateSink.state.Sets[0].Val!="value"{b.Fatal(queryUpdateSink.state.Sets)}})
 b.Run("ReuseSingle",func(b *testing.B){target:=newUserUpdateBuilder(nil,"users",0).SetName(" value ");b.ReportAllocs();for b.Loop(){target.SetName(" value ")};if len(target.Sets)!=1||target.Sets[0].Val!="value"{b.Fatal(target.Sets)}})
 b.Run("ReuseBulk",func(b *testing.B){target:=q.Update().SetName(" value ");b.ReportAllocs();for b.Loop(){target.SetName(" value ")};if len(target.state.Sets)!=1||target.state.Sets[0].Val!="value"{b.Fatal(target.state.Sets)}})
}
`

func TestGeneratedHashPreparationBenchmarks(t *testing.T) {
	if os.Getenv("LUXO_GENERATED_BENCHMARK") == "" {
		t.Skip("generated benchmark fixture")
	}
	const source = `
model Plain @noTime { id: Int @id name: String password: String @beforeSave { it.trim() } }
model Secured @noTime { id: Int @id name: String password: String @hash @beforeSave { it.trim() } }
`
	runGeneratedFixture(t, source, hashPreparationBenchmarks, StableIDs{ModelFields: map[string]map[string]int{
		"Plain": {"id": 1, "name": 2, "password": 3}, "Secured": {"id": 1, "name": 2, "password": 3},
	}})
}

const hashPreparationBenchmarks = `package generated
import("strings";"testing";"unsafe";luxocrypto "github.com/light-speak/luxo/pkg/lux/crypto")
var plainHashSink *PlainCreateBuilder
var securedHashSink *SecuredCreateBuilder
func BenchmarkHashOrdinaryField(b *testing.B){
 b.Run("ColdPlain",func(b *testing.B){b.ReportAllocs();for b.Loop(){plainHashSink=(&PlainCreateBuilder{}).SetName("name")};b.ReportMetric(float64(unsafe.Sizeof(*plainHashSink)),"type-B")})
 b.Run("ColdSecured",func(b *testing.B){b.ReportAllocs();for b.Loop(){securedHashSink=(&SecuredCreateBuilder{}).SetName("name")};if securedHashSink._prepareErr!=nil{b.Fatal(securedHashSink._prepareErr)};b.ReportMetric(float64(unsafe.Sizeof(*securedHashSink)),"type-B")})
 b.Run("WarmPlain",func(b *testing.B){v:=(&PlainCreateBuilder{}).SetName("name");b.ReportAllocs();for b.Loop(){v.SetName("name")};if len(v.Vals)!=1||v.Vals[0]!="name"{b.Fatal(v.Vals)}})
 b.Run("WarmSecured",func(b *testing.B){v:=(&SecuredCreateBuilder{}).SetName("name");b.ReportAllocs();for b.Loop(){v.SetName("name")};if len(v.Vals)!=1||v.Vals[0]!="name"||v._prepareErr!=nil{b.Fatal(v.Vals,v._prepareErr)}})
}
// Both paths trim and hash exactly once at the production password cost.
// The old plaintext-writing behavior is not a valid hashing baseline.
func BenchmarkHashPreparedWrite(b *testing.B){
 b.Run("ManualCorrect",func(b *testing.B){v:=&PlainCreateBuilder{};b.ReportAllocs();for b.Loop(){hashed,err:=luxocrypto.HashPassword(strings.TrimSpace(" secret "));if err!=nil{b.Fatal(err)};v.setPreparedPassword(hashed)};b.StopTimer();if !luxocrypto.VerifyPassword("secret",v.Vals[0].(string)){b.Fatal("invalid hash")}})
 b.Run("Generated",func(b *testing.B){v:=&SecuredCreateBuilder{};b.ReportAllocs();for b.Loop(){v.SetPassword(" secret ");if v._prepareErr!=nil{b.Fatal(v._prepareErr)}};b.StopTimer();if !luxocrypto.VerifyPassword("secret",v.Vals[0].(string)){b.Fatal("invalid hash")}})
}
`

func BenchmarkGenerateQueryTerminalArguments(b *testing.B) {
	baseline := os.Getenv("LUXO_QUERY_TERMINAL_BASELINE") == "1"
	for _, sample := range []struct {
		name, body, result string
		invalid            bool
	}{
		{"count-none", `return Item.count()`, "Int", false},
		{"count-false", `return Item.count(where: false)`, "Int", false},
		{"exists-field", `return Item.exists(where: it.id == requested)`, "Boolean", false},
		{"count-chain", `return Item.where(id > 0).count(where: id < requested)`, "Int", false},
		{"count-alias", `val query = Item.where(id > 0) return query.count(where: false)`, "Int", false},
		{"count-function", `return count(Item, where: false)`, "Int", false},
		{"bulk-control", `return Item.deleteMany(where: false)`, "Int", false},
		{"invalid-argument", `return Item.count(where: false)`, "Int", true},
	} {
		b.Run(sample.name, func(b *testing.B) {
			input := analyzePermissionFixture(b, `model Item @noTime { id: Int @id } api probe(requested: Int): `+sample.result+` { `+sample.body+` }`)
			if len(input.Errors) != 0 {
				b.Fatal(input.Errors)
			}
			if sample.invalid {
				mutateQueryWhereArguments(input.Files[0].APIs[0].Body, "count", "unknown")
			}
			ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Item": {"id": 1}}})
			generator, err := NewGenerator(GeneratorConfig{Driver: DriverPG, IDs: ids})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				output, err := generator.Generate(input, "generated", nil)
				if sample.invalid && !baseline {
					if err == nil || output != nil {
						b.Fatal("invalid query terminal AST produced output")
					}
				} else if err != nil || output == nil {
					b.Fatal(err)
				}
			}
		})
	}
}
