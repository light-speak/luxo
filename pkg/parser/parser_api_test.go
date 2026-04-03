package parser

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestParseApiSimple(t *testing.T) {
	input := `api getUser(id: Int): User`
	file := parse(t, input)

	if len(file.APIs) != 1 {
		t.Fatalf("expected 1 api, got %d", len(file.APIs))
	}
	api := file.APIs[0]
	if api.Name != "getUser" {
		t.Errorf("expected 'getUser', got %q", api.Name)
	}
	if len(api.Params) != 1 {
		t.Errorf("expected 1 param, got %d", len(api.Params))
	}
	if api.ReturnType.Name != "User" {
		t.Errorf("expected return type 'User', got %q", api.ReturnType.Name)
	}
	if api.Body != nil {
		t.Error("expected no body")
	}
}

func TestParseApiWithDirectives(t *testing.T) {
	input := `api getUser(id: Int): User @auth @cache(ttl: 60)`
	file := parse(t, input)
	api := file.APIs[0]

	if len(api.Directives) != 2 {
		t.Fatalf("expected 2 directives, got %d", len(api.Directives))
	}
	if api.Directives[0].Name != "auth" {
		t.Errorf("expected @auth, got @%s", api.Directives[0].Name)
	}
	if api.Directives[1].Name != "cache" {
		t.Errorf("expected @cache, got @%s", api.Directives[1].Name)
	}
	if len(api.Directives[1].Args) != 1 {
		t.Errorf("expected 1 arg for @cache, got %d", len(api.Directives[1].Args))
	}
}

func TestParseApiWithBody(t *testing.T) {
	input := `api register(input: RegisterInput): AuthResult {
  val user = create(User, name: input.name)
  return user
}`
	file := parse(t, input)
	api := file.APIs[0]

	if api.Body == nil {
		t.Fatal("expected body")
	}
	if len(api.Body.Stmts) != 2 {
		t.Errorf("expected 2 statements, got %d", len(api.Body.Stmts))
	}

	// val user = ...
	valStmt, ok := api.Body.Stmts[0].(*ast.ValStmt)
	if !ok {
		t.Fatalf("expected ValStmt, got %T", api.Body.Stmts[0])
	}
	if valStmt.Name != "user" {
		t.Errorf("expected 'user', got %q", valStmt.Name)
	}

	// return user
	_, ok = api.Body.Stmts[1].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected ReturnStmt, got %T", api.Body.Stmts[1])
	}
}

func TestParseApiDefaultParam(t *testing.T) {
	input := `api listUsers(status: String?, limit: Int = 20): Page<User>`
	file := parse(t, input)
	api := file.APIs[0]

	if len(api.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(api.Params))
	}
	if !api.Params[0].Type.Nullable {
		t.Error("expected status to be nullable")
	}
	if api.Params[1].Default == nil {
		t.Error("expected default value for limit")
	}
	if api.ReturnType.Name != "Page" {
		t.Errorf("expected return type 'Page', got %q", api.ReturnType.Name)
	}
	if len(api.ReturnType.TypeArgs) != 1 {
		t.Error("expected generic type arg")
	}
}

func TestParseApiStream(t *testing.T) {
	input := `api watchComments(postId: Int): stream Comment`
	file := parse(t, input)

	if len(file.APIs) != 1 {
		t.Fatalf("expected 1 api, got %d", len(file.APIs))
	}
	api := file.APIs[0]
	if api.Name != "watchComments" {
		t.Errorf("expected 'watchComments', got %q", api.Name)
	}
	if api.ReturnType.Name != "stream Comment" {
		t.Errorf("expected 'stream Comment', got %q", api.ReturnType.Name)
	}
	if len(api.Params) != 1 {
		t.Errorf("expected 1 param, got %d", len(api.Params))
	}
	if api.Params[0].Name != "postId" {
		t.Errorf("expected param 'postId', got %q", api.Params[0].Name)
	}
}

func TestParseMultipleApis(t *testing.T) {
	input := `api getUser(id: Int): User
api listUsers(limit: Int): Page<User>
api deleteUser(id: Int): Boolean`
	file := parse(t, input)

	if len(file.APIs) != 3 {
		t.Fatalf("expected 3 apis, got %d", len(file.APIs))
	}
	names := []string{"getUser", "listUsers", "deleteUser"}
	for i, api := range file.APIs {
		if api.Name != names[i] {
			t.Errorf("api[%d]: expected %q, got %q", i, names[i], api.Name)
		}
	}

	// Verify return types
	if file.APIs[0].ReturnType.Name != "User" {
		t.Errorf("api[0]: expected return type 'User', got %q", file.APIs[0].ReturnType.Name)
	}
	if file.APIs[1].ReturnType.Name != "Page" {
		t.Errorf("api[1]: expected return type 'Page', got %q", file.APIs[1].ReturnType.Name)
	}
	if len(file.APIs[1].ReturnType.TypeArgs) != 1 {
		t.Error("api[1]: expected generic type arg")
	}
	if file.APIs[2].ReturnType.Name != "Boolean" {
		t.Errorf("api[2]: expected return type 'Boolean', got %q", file.APIs[2].ReturnType.Name)
	}
}

func TestParseOverrideApi(t *testing.T) {
	input := `override api createUser(input: UserInput): User {
  val x = 1
}`
	file := parse(t, input)

	if len(file.APIs) != 1 {
		t.Fatalf("expected 1 api, got %d", len(file.APIs))
	}
	api := file.APIs[0]
	if !api.Override {
		t.Error("expected api to be marked as override")
	}
	if api.Name != "createUser" {
		t.Errorf("expected 'createUser', got %q", api.Name)
	}
	if len(api.Params) != 1 {
		t.Errorf("expected 1 param, got %d", len(api.Params))
	}
	if api.ReturnType.Name != "User" {
		t.Errorf("expected return type 'User', got %q", api.ReturnType.Name)
	}
	if api.Body == nil {
		t.Fatal("expected body")
	}
	if len(api.Body.Stmts) != 1 {
		t.Errorf("expected 1 statement, got %d", len(api.Body.Stmts))
	}
}

func TestParseStreamReturnType(t *testing.T) {
	input := `api watchComments(postId: Int): stream Comment`
	file := parse(t, input)
	api := file.APIs[0]

	if api.ReturnType.Name != "stream Comment" {
		t.Errorf("expected 'stream Comment', got %q", api.ReturnType.Name)
	}
}

func TestParseStreamType(t *testing.T) {
	// Already partially covered by TestParseStreamReturnType, but cover parseTypeRef stream branch
	input := `api watch(): stream Event`
	file := parse(t, input)
	api := file.APIs[0]
	if api.ReturnType.Name != "stream Event" {
		t.Errorf("expected 'stream Event', got %q", api.ReturnType.Name)
	}
}
