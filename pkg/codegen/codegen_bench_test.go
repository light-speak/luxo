package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

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
				if err := g.validateFieldIdentifiers(input); err != nil {
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
