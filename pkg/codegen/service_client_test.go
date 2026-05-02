package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestCollectRemoteServiceFns_Basic(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "origin/user.luxo",
				Functions: []*ast.FnDecl{
					{
						Name:       "getUserScore",
						Directives: []*ast.Directive{{Name: "service"}},
						Params: []*ast.ParamDecl{
							{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
						},
						ReturnType: &ast.TypeRef{Name: "Float"},
						Body:       &ast.Block{},
					},
					{
						Name: "internalHelper", // no @service
						Body: &ast.Block{},
					},
				},
			},
			{
				Name: "origin/post.luxo",
				Functions: []*ast.FnDecl{
					{
						Name:       "countPosts",
						Directives: []*ast.Directive{{Name: "service"}},
						ReturnType: &ast.TypeRef{Name: "Int"},
						Body:       &ast.Block{},
					},
				},
			},
		},
	}

	byModule := collectRemoteServiceFns(result)

	if len(byModule) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(byModule))
	}
	if len(byModule["user"]) != 1 {
		t.Errorf("user module should have 1 service fn, got %d", len(byModule["user"]))
	}
	if byModule["user"][0].fnName != "getUserScore" {
		t.Errorf("got fn name %q", byModule["user"][0].fnName)
	}
	if len(byModule["post"]) != 1 {
		t.Errorf("post module should have 1 service fn, got %d", len(byModule["post"]))
	}
}

func TestCollectRemoteServiceFns_NoServiceFns(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Functions: []*ast.FnDecl{
				{Name: "helper", Body: &ast.Block{}},
			},
		}},
	}
	byModule := collectRemoteServiceFns(result)
	if len(byModule) != 0 {
		t.Errorf("should have no service fns, got %d modules", len(byModule))
	}
}

func TestGenerateServiceClientFile_Nil(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{Name: "origin/user.luxo"}},
	}
	code := generateServiceClientFile(result, "luxo")
	if code != nil {
		t.Error("should return nil when no service fns")
	}
}

func TestGenerateServiceClientFile_Basic(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "origin/user.luxo",
				Functions: []*ast.FnDecl{
					{
						Name:       "getUserScore",
						Directives: []*ast.Directive{{Name: "service"}},
						Params: []*ast.ParamDecl{
							{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
						},
						ReturnType: &ast.TypeRef{Name: "Float"},
						Body:       &ast.Block{},
					},
				},
			},
		},
	}

	code := generateServiceClientFile(result, "luxo")
	if code == nil {
		t.Fatal("should generate client file")
	}
	src := string(code)

	checks := []string{
		"UserServiceClient",
		"rpc.Client",
		"NewUserServiceClient",
		"func (c *UserServiceClient) GetUserScore(ctx context.Context",
		"userId int64",
		"float64, error",
		"rpc.EncodeParams",
		"codec.ReadFixed64", // Float return decodes with ReadFixed64
		"Close()",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("missing %q in client:\n%s", check, src)
		}
	}
}

func TestGenerateServiceMethod_IntReturn(t *testing.T) {
	var b strings.Builder
	fn := serviceFnInfo{
		moduleName: "user",
		fnName:     "countUsers",
		returnType: &ast.TypeRef{Name: "Int"},
	}
	generateServiceMethod(&b, "UserServiceClient", fn)
	src := b.String()

	if !strings.Contains(src, "codec.ReadSvarint") {
		t.Errorf("Int return should use ReadSvarint:\n%s", src)
	}
}

func TestGenerateServiceMethod_StringReturn(t *testing.T) {
	var b strings.Builder
	fn := serviceFnInfo{
		moduleName: "user",
		fnName:     "getName",
		returnType: &ast.TypeRef{Name: "String"},
	}
	generateServiceMethod(&b, "UserServiceClient", fn)
	src := b.String()

	if !strings.Contains(src, "codec.ReadString") {
		t.Errorf("String return should use ReadString:\n%s", src)
	}
}

func TestGenerateServiceMethod_BoolReturn(t *testing.T) {
	var b strings.Builder
	fn := serviceFnInfo{
		moduleName: "user",
		fnName:     "isActive",
		returnType: &ast.TypeRef{Name: "Boolean"},
	}
	generateServiceMethod(&b, "UserServiceClient", fn)
	src := b.String()

	if !strings.Contains(src, "codec.ReadBool") {
		t.Errorf("Bool return should use ReadBool:\n%s", src)
	}
}

func TestGenerateServiceMethod_ModelReturn(t *testing.T) {
	var b strings.Builder
	fn := serviceFnInfo{
		moduleName: "user",
		fnName:     "getProfile",
		returnType: &ast.TypeRef{Name: "Profile"},
	}
	generateServiceMethod(&b, "UserServiceClient", fn)
	src := b.String()

	if !strings.Contains(src, "ReadLuxo") {
		t.Errorf("Model return should use ReadLuxo:\n%s", src)
	}
	if !strings.Contains(src, "codec.NewDecoder") {
		t.Errorf("Model return should create decoder:\n%s", src)
	}
}

func TestGenerateServiceMethod_MultipleParams(t *testing.T) {
	var b strings.Builder
	fn := serviceFnInfo{
		moduleName: "order",
		fnName:     "processPayment",
		params: []*ast.ParamDecl{
			{Name: "orderId", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "amount", Type: &ast.TypeRef{Name: "Float"}},
			{Name: "note", Type: &ast.TypeRef{Name: "String"}},
		},
		returnType: &ast.TypeRef{Name: "Boolean"},
	}
	generateServiceMethod(&b, "OrderServiceClient", fn)
	src := b.String()

	// All params should be in signature and encoding
	if !strings.Contains(src, "orderId int64") {
		t.Errorf("missing orderId param:\n%s", src)
	}
	if !strings.Contains(src, "amount float64") {
		t.Errorf("missing amount param:\n%s", src)
	}
	if !strings.Contains(src, "note string") {
		t.Errorf("missing note param:\n%s", src)
	}
	// Should encode all 3 params
	if strings.Count(src, "ParamField{") != 3 {
		t.Errorf("should encode 3 params:\n%s", src)
	}
}
