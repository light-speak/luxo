package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestAuthStoreExtractionForFragments(t *testing.T) {
	if fields := authStoreFields(&ast.ModelDecl{}); len(fields) != 0 {
		t.Fatal(fields)
	}
	model := &ast.ModelDecl{Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}, Directives: []*ast.Directive{{Name: "withAuth", Args: []*ast.NamedArg{
		{Name: "default", Value: &ast.Ident{Name: "true"}},
		{Name: "stores", Value: &ast.ListExpr{Items: []ast.Expr{&ast.ListExpr{}, &ast.Ident{Name: "id"}}}},
	}}}}
	fields := authStoreFields(model)
	if len(fields) != 1 || fields[0].Name != "id" {
		t.Fatal("fragment extraction changed")
	}
}

func TestAuthStoresContract(t *testing.T) {
	field := &ast.FieldDecl{Name: "id", Type: &ast.TypeRef{Name: "Int"}}
	for _, sample := range []struct {
		name  string
		value ast.Expr
		valid bool
	}{
		{"missing", nil, false},
		{"not-list", &ast.Ident{Name: "id"}, false},
		{"not-field", &ast.ListExpr{Items: []ast.Expr{&ast.ListExpr{}}}, false},
		{"unknown", &ast.ListExpr{Items: []ast.Expr{&ast.Ident{Name: "missing"}}}, false},
		{"duplicate", &ast.ListExpr{Items: []ast.Expr{&ast.Ident{Name: "id"}, &ast.Ident{Name: "id"}}}, false},
		{"empty", &ast.ListExpr{}, true},
		{"valid", &ast.ListExpr{Items: []ast.Expr{&ast.Ident{Name: "id"}}}, true},
	} {
		model := &ast.ModelDecl{Name: "User", Fields: []*ast.FieldDecl{field}, Directives: []*ast.Directive{{Name: "withAuth"}}}
		if sample.value != nil {
			model.Directives[0].Args = []*ast.NamedArg{{Name: "stores", Value: sample.value}}
		}
		result := &semantic.Result{Files: []*ast.File{{Models: []*ast.ModelDecl{model}}}}
		if err := validateAuthStores(result); (err == nil) != sample.valid {
			t.Errorf("%s: %v", sample.name, err)
		}
	}
	if err := validateAuthStores(&semantic.Result{Files: []*ast.File{{Models: []*ast.ModelDecl{{Name: "Plain"}}}}}); err != nil {
		t.Fatal(err)
	}
}

func TestPublicGenerationRejectsInvalidAuthStores(t *testing.T) {
	model := &ast.ModelDecl{
		Name: "User", Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}},
		Directives: []*ast.Directive{{Name: "withAuth"}},
	}
	input := &semantic.Result{Files: []*ast.File{{Name: "origin/user/model.luxo", Models: []*ast.ModelDecl{model}}}}
	config := GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1}}}}
	generated, err := Generate(input, "generated", config)
	if err == nil || generated != nil || !strings.Contains(err.Error(), "requires an explicit stores field list") {
		t.Fatalf("public generation did not propagate auth contract error: %v", err)
	}
	model.Directives[0].Args = []*ast.NamedArg{{Name: "stores", Value: &ast.ListExpr{Items: []ast.Expr{&ast.Ident{Name: "id"}}}}}
	if generated, err = Generate(input, "generated", config); err != nil || generated == nil {
		t.Fatalf("repaired auth contract was rejected: %v", err)
	}
}

func TestGeneratedStaticClaims(t *testing.T) {
	fields := []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		{Name: "number", Type: &ast.TypeRef{Name: "JSON"}},
		{Name: "roles", Type: &ast.TypeRef{Name: "Role", IsList: true}},
		{Name: "child", Type: &ast.TypeRef{Name: "Child"}},
		{Name: "secret", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hidden"}}},
	}
	var stores []ast.Expr
	for _, field := range fields {
		stores = append(stores, &ast.Ident{Name: field.Name})
	}
	model := &ast.ModelDecl{Name: "User", Fields: fields, Directives: []*ast.Directive{{Name: "withAuth", Args: []*ast.NamedArg{{Name: "stores", Value: &ast.ListExpr{Items: stores}}}}}}
	file := &ast.File{Models: []*ast.ModelDecl{model}, Enums: []*ast.EnumDecl{{Name: "Role", Values: []string{"Admin", "Member"}}}, Types: []*ast.TypeDecl{{Name: "Child", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "secret", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hidden"}}},
	}}}}
	source := string(generateModelFile(&semantic.Result{Files: []*ast.File{file}}, "generated", map[string]bool{"Role": true}))
	for _, forbidden := range []string{"map[string]any", "json.Marshal", "json.Unmarshal", "reflect."} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("generated auth uses %s", forbidden)
		}
	}
	runGeneratedProtocolPackage(t, source, staticAuthAssertions)
}

const staticAuthAssertions = `package generated
import (
 "math"
 "net/http"
 "net/http/httptest"
 "strconv"
 "strings"
 "testing"
 "github.com/light-speak/luxo/pkg/lux/auth"
 "github.com/light-speak/luxo/pkg/lux/luvia"
)
func TestStaticClaims(t *testing.T) {
 t.Setenv("JWT_SECRET","generated-auth-test-only");t.Setenv("JWT_EXPIRES","1h")
 t.Setenv("JWT_REFRESH","true");t.Setenv("JWT_REFRESH_EXPIRES","2h")
 auth.ResetConfig();t.Cleanup(auth.ResetConfig);auth.ResetVerifyCache();t.Cleanup(auth.ResetVerifyCache)
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 for _,id:=range []int64{math.MinInt64,9007199254740993,math.MaxInt64} {
  user:=&User{Id:id,Role:Role("Admin"),Name:"東京",Number:[]byte("123456789012345678901234567890"),Roles:[]Role{Role("Member")},Child:&Child{Id:id,Secret:"nested-private"},Secret:"explicit-store"}
  token,err:=user.CreateToken();if err!=nil||token==""{t.Fatal("token failure",err)}
  claims,err:=auth.Verify(cfg,token);if err!=nil{t.Fatal(err)}
  if claims.ID()!=id||claims.String("name")!="東京"||claims.Get("number").Raw()!="123456789012345678901234567890"{t.Fatal("precision changed")}
  if claims.String("secret")!="explicit-store"||strings.Contains(claims.Get("child").Raw(),"secret"){t.Fatal("explicit stores or nested hidden boundary changed")}
  refresh,err:=user.CreateRefreshToken();if err!=nil{t.Fatal(err)}
  if _,err:=user.RefreshToken(token);err==nil{t.Fatal("access token used for refresh")}
  refreshed,err:=user.RefreshToken(refresh);if err!=nil{t.Fatal(err)};again,err:=auth.Verify(cfg,refreshed)
  if err!=nil||again.Raw()!=claims.Raw(){t.Fatal("refresh changed identity")}
  endpoint:=luvia.AuthMiddleware(cfg,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
   identity:=luvia.Identity(r.Context());if identity==nil{t.Fatal("identity missing")}
   w.Write([]byte(strconv.FormatInt(identity.ID(),10)))
   *identity=luvia.Claims{}
  }))
  for range 2 {r:=httptest.NewRequest("GET","/",nil);r.Header.Set("Authorization","Bearer "+token);w:=httptest.NewRecorder();endpoint.ServeHTTP(w,r)
   if w.Body.String()!=strconv.FormatInt(id,10){t.Fatal("request or cache identity corrupted")}
  }
  user.Role=Role("Unknown");if _,err:=user.CreateToken();err==nil{t.Fatal("invalid enum signed")}
  if _,err:=user.CreateRefreshToken();err==nil{t.Fatal("invalid enum signed in refresh")}
  user.Role=Role("Admin");user.Name=string([]byte{0xff});if _,err:=user.CreateToken();err==nil{t.Fatal("invalid text signed")}
  if value,err:=user.RefreshToken("invalid");err==nil||value!=""{t.Fatal("invalid token refresh succeeded")}
 }
 var missing *User
 if out,err:=missing.AppendClaimsJSON([]byte("prefix"));err==nil||string(out)!="prefix"{t.Fatal("nil receiver emitted partial output")}
 t.Setenv("JWT_SECRET","");auth.ResetConfig()
 if _,err:=missing.CreateToken();err==nil{t.Fatal("missing config was swallowed")}
 if _,err:=missing.CreateRefreshToken();err==nil{t.Fatal("missing refresh config was swallowed")}
 if _,err:=missing.RefreshToken("invalid");err==nil{t.Fatal("refresh config error was swallowed")}
}
`
