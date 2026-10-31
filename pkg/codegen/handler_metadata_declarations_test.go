package codegen

import (
	"github.com/light-speak/luxo/pkg/ast"
	"strings"
	"testing"
)

// Adapt legacy focused emitter fixtures to the same typed declaration shape.
func requestParamDeclarations(types map[string]map[string]string) map[string][]*ast.ParamDecl {
	result := make(map[string][]*ast.ParamDecl)
	for api, params := range types {
		for name, typ := range params {
			nullable := strings.HasSuffix(typ, "?")
			typ = strings.TrimSuffix(typ, "?")
			list := strings.HasPrefix(typ, "[")
			typ = strings.TrimSuffix(strings.TrimPrefix(typ, "["), "]")
			result[api] = append(result[api], &ast.ParamDecl{Name: name, Type: &ast.TypeRef{Name: typ, IsList: list, Nullable: nullable}})
		}
	}
	return result
}

func buildParamTypesFromAST(files []*ast.File) map[string]map[string]string {
	g := defaultGenerator().withRequestDeclarations(result(files...), CollectEnumsFromResult(result(files...)))
	types := make(map[string]map[string]string)
	for api, params := range g.requestParams {
		fields := make(map[string]string)
		for _, param := range params {
			typ := param.Type.Name
			if param.Type.IsList {
				typ = "[" + typ + "]"
			}
			if param.Type.Nullable {
				typ += "?"
			}
			fields[param.Name] = typ
		}
		types[strings.TrimPrefix(api, "svc:")] = fields
	}
	return types
}

func TestBuildParamTypesFromASTIncludesServiceFunctions(t *testing.T) {
	files := []*ast.File{{
		Functions: []*ast.FnDecl{{
			Name: "heartbeat",
			Params: []*ast.ParamDecl{
				{Name: "cpuPercent", Type: &ast.TypeRef{Name: "Float"}},
				{Name: "uptime", Type: &ast.TypeRef{Name: "Duration"}},
			},
			Directives: []*ast.Directive{{Name: "service"}},
		}},
	}}

	types := buildParamTypesFromAST(files)
	if got := types["heartbeat"]["cpuPercent"]; got != "Float" {
		t.Fatalf("cpuPercent type = %q, want Float", got)
	}
	if got := types["heartbeat"]["uptime"]; got != "Duration" {
		t.Fatalf("uptime type = %q, want Duration", got)
	}
}

func TestBuildParamTypesFromASTPreservesLists(t *testing.T) {
	files := []*ast.File{{
		APIs: []*ast.ApiDecl{{
			Name: "findMany",
			Params: []*ast.ParamDecl{
				{Name: "ids", Type: &ast.TypeRef{Name: "UUID", IsList: true}},
				{Name: "names", Type: &ast.TypeRef{Name: "String", IsList: true, Nullable: true}},
			},
		}},
	}}

	types := buildParamTypesFromAST(files)
	if got := types["findMany"]["ids"]; got != "[UUID]" {
		t.Fatalf("ids type = %q, want [UUID]", got)
	}
	if got := types["findMany"]["names"]; got != "[String]?" {
		t.Fatalf("names type = %q, want [String]?", got)
	}
}

func TestBuildParamTypesFromASTInjectsPaginationTypes(t *testing.T) {
	files := []*ast.File{{APIs: []*ast.ApiDecl{{
		Name:       "browsePosts",
		Params:     []*ast.ParamDecl{{Name: "status", Type: &ast.TypeRef{Name: "String", Nullable: true}}},
		Directives: []*ast.Directive{{Name: "paginate"}},
	}}}}

	types := buildParamTypesFromAST(files)["browsePosts"]
	if types["status"] != "String?" || types["page"] != "Int" || types["pageSize"] != "Int" {
		t.Fatalf("paginated API types = %v", types)
	}
}

func TestBuildParamTypesFromASTNormalizesWireTypes(t *testing.T) {
	files := []*ast.File{{
		Enums: []*ast.EnumDecl{{Name: "Role"}},
		Types: []*ast.TypeDecl{{Name: "CreateInput"}},
		APIs: []*ast.ApiDecl{{
			Name: "create",
			Params: []*ast.ParamDecl{
				{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
				{Name: "input", Type: &ast.TypeRef{Name: "CreateInput"}},
				{Name: "inputs", Type: &ast.TypeRef{Name: "CreateInput", IsList: true}},
			},
		}},
	}}

	types := buildParamTypesFromAST(files)
	if got := types["create"]["role"]; got != "Role" {
		t.Fatalf("role wire type = %q, want Role", got)
	}
	if got := types["create"]["input"]; got != "CreateInput" {
		t.Fatalf("input wire type = %q, want CreateInput", got)
	}
	if got := types["create"]["inputs"]; got != "[CreateInput]" {
		t.Fatalf("inputs wire type = %q, want [CreateInput]", got)
	}
}

func TestBuildParamTypesFromASTUsesOnlyGeneratedCRUDParams(t *testing.T) {
	autoID := &ast.FieldDecl{Name: "id", Type: &ast.TypeRef{Name: "UUID"}, Directives: []*ast.Directive{{Name: "auto"}}}
	internal := &ast.FieldDecl{Name: "secret", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "internal"}}}
	immutable := &ast.FieldDecl{Name: "slug", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "immutable"}}}
	files := []*ast.File{{
		Models: []*ast.ModelDecl{
			{Name: "Post", Directives: []*ast.Directive{{Name: "crud"}}, Fields: []*ast.FieldDecl{
				autoID,
				{Name: "title", Type: &ast.TypeRef{Name: "String"}},
				immutable,
				internal,
				{Name: "author", Type: &ast.TypeRef{Name: "User"}},
			}},
			{Name: "User", Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "UUID"}}}},
		},
	}}

	types := buildParamTypesFromAST(files)
	create := types["createPost"]
	update := types["updatePost"]
	if len(create) != 2 || create["title"] != "String" || create["slug"] != "String" {
		t.Fatalf("create types = %v", create)
	}
	if len(update) != 2 || update["id"] != "UUID" || update["title"] != "String" {
		t.Fatalf("update types = %v", update)
	}
}
