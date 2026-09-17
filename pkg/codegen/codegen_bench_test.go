package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

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

func BenchmarkGenerate(b *testing.B) {
	result := buildBenchResult()
	b.ResetTimer()
	for b.Loop() {
		Generate(result, "gen", DriverPG)
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

func BenchmarkAPIParameterMetadata(b *testing.B) {
	for _, typeName := range []string{"Int", "State"} {
		b.Run(typeName, func(b *testing.B) {
			generator := mustNewGenerator(b, GeneratorConfig{IDs: StableIDs{
				APIs:          map[string]int{"echo": 1},
				APIParams:     map[string]map[string]int{"echo": {"value": 1, "values": 2}},
				APIParamTypes: map[string]map[string]string{"echo": {"value": typeName, "values": "[" + typeName + "]?"}},
			}})
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
