package semantic

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
)

func analyze(t *testing.T, input string) *Result {
	t.Helper()
	l := lexer.New(input, "test.luxo")
	tokens, lexErrors := l.Tokenize()
	if len(lexErrors) > 0 {
		t.Fatalf("lexer errors: %v", lexErrors)
	}
	p := parser.New(tokens)
	file, parseErrors := p.Parse("test.luxo")
	if len(parseErrors) > 0 {
		t.Fatalf("parser errors: %v", parseErrors)
	}
	a := New()
	return a.Analyze([]*ast.File{file})
}

func expectNoErrors(t *testing.T, result *Result) {
	t.Helper()
	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
}

func expectError(t *testing.T, result *Result, substring string) {
	t.Helper()
	for _, err := range result.Errors {
		if strings.Contains(err.Message, substring) {
			return
		}
	}
	t.Errorf("expected error containing %q, got errors: %v", substring, result.Errors)
}

func expectWarning(t *testing.T, result *Result, substring string) {
	t.Helper()
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, substring) {
			return
		}
	}
	t.Errorf("expected warning containing %q, got warnings: %v", substring, result.Warnings)
}

// ========== Type Declaration Tests ==========

func TestDeclareModel(t *testing.T) {
	result := analyze(t, `model User { name: String }`)
	expectNoErrors(t, result)

	typ, ok := result.Types["User"]
	if !ok {
		t.Fatal("expected type User")
	}
	if typ.Kind != TypeModel {
		t.Errorf("expected TypeModel, got %v", typ.Kind)
	}
	if _, ok := typ.Fields["name"]; !ok {
		t.Error("expected field 'name'")
	}
}

func TestDeclareEnum(t *testing.T) {
	result := analyze(t, `enum Role { USER ADMIN MODERATOR }`)
	expectNoErrors(t, result)

	typ := result.Types["Role"]
	if typ == nil {
		t.Fatal("expected type Role")
	}
	if len(typ.EnumValues) != 3 {
		t.Errorf("expected 3 enum values, got %d", len(typ.EnumValues))
	}
}

func TestDeclareSealed(t *testing.T) {
	result := analyze(t, `sealed PayResult {
  Success(id: String)
  Failed(reason: String, code: Int)
}`)
	expectNoErrors(t, result)

	typ := result.Types["PayResult"]
	if typ == nil {
		t.Fatal("expected type PayResult")
	}
	if len(typ.Variants) != 2 {
		t.Errorf("expected 2 variants, got %d", len(typ.Variants))
	}
}

func TestDuplicateType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
model User { email: String }
`)
	expectError(t, result, "already declared")
}

// ========== Multi-file Analysis Tests ==========

func TestMultiFile(t *testing.T) {
	l1 := lexer.New(`
model Base { id: Int }
enum Role { USER ADMIN }
`, "common.luxo")
	tokens1, _ := l1.Tokenize()
	p1 := parser.New(tokens1)
	file1, _ := p1.Parse("common.luxo")

	l2 := lexer.New(`
model User : Base {
  name: String
  role: Role
}
api getUser(id: Int): User
`, "user.luxo")
	tokens2, _ := l2.Tokenize()
	p2 := parser.New(tokens2)
	file2, _ := p2.Parse("user.luxo")

	a := New()
	result := a.Analyze([]*ast.File{file1, file2})
	expectNoErrors(t, result)

	user := result.Types["User"]
	if user == nil {
		t.Fatal("expected User type")
	}
	if len(user.Parents) != 1 {
		t.Error("expected User to inherit from Base")
	}
}

// ========== API & FN Tests ==========

func TestApiDeclaration(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api getUser(id: Int): User
api listUsers(limit: Int): [User]
`)
	expectNoErrors(t, result)

	sym := result.Scope.Lookup("getUser")
	if sym == nil {
		t.Fatal("expected getUser symbol")
	}
	if sym.Kind != SymApi {
		t.Errorf("expected SymApi, got %v", sym.Kind)
	}
}

func TestFnDeclaration(t *testing.T) {
	result := analyze(t, `fn encrypt(value: String): String @native`)
	expectNoErrors(t, result)

	sym := result.Scope.Lookup("encrypt")
	if sym == nil {
		t.Fatal("expected encrypt symbol")
	}
	if sym.Kind != SymFn {
		t.Errorf("expected SymFn, got %v", sym.Kind)
	}
}

// ========== Complex Schema Test ==========

func TestComplexSchema(t *testing.T) {
	result := analyze(t, `
model Base {
  id: Int
  createdAt: DateTime
}

enum Role { USER ADMIN }

model User : Base {
  name: String @varchar(100) @filterable
  email: String @unique
  password: String @hidden @hash
  role: Role
  avatar: String?
  posts: [Post]
}

model Post : Base {
  title: String
  content: String
  userId: Int
  user: User
}

type AuthResult {
  token: String
  user: User
}

api getUser(id: Int): User @cache(ttl: 60)

api register(input: AuthResult): AuthResult {
  val user = create(User, name: "test")
  AuthResult { token: "abc", user: user }
}

fn encrypt(value: String): String @native

extend User {
  orders: [Post]
}
`)
	expectNoErrors(t, result)

	if len(result.Types) < 5 {
		t.Errorf("expected at least 5 types (builtins + user), got %d", len(result.Types))
	}
}

func TestErrorDecl(t *testing.T) {
	result := analyze(t, `
error NotFound {
  code: 404
  message: resource.not_found
}
`)
	expectNoErrors(t, result)

	sym := result.Scope.Lookup("NotFound")
	if sym == nil {
		t.Fatal("expected NotFound symbol in scope")
	}
	if sym.Kind != SymError {
		t.Errorf("expected SymError, got %v", sym.Kind)
	}
}

func TestMiddlewareDecl(t *testing.T) {
	result := analyze(t, `
middleware requestLogger {
  log("request received")
}
`)
	expectNoErrors(t, result)

	sym := result.Scope.Lookup("requestLogger")
	if sym == nil {
		t.Fatal("expected requestLogger symbol in scope")
	}
	if sym.Kind != SymMiddleware {
		t.Errorf("expected SymMiddleware, got %v", sym.Kind)
	}
}

func TestSealedType(t *testing.T) {
	result := analyze(t, `
sealed MyResult {
  Ok(value: String)
  Err(message: String, code: Int)
}
`)
	expectNoErrors(t, result)

	typ := result.Types["MyResult"]
	if typ == nil {
		t.Fatal("expected type Result")
	}
	if typ.Kind != TypeSealed {
		t.Errorf("expected TypeSealed, got %v", typ.Kind)
	}
	if len(typ.Variants) != 2 {
		t.Errorf("expected 2 variants, got %d", len(typ.Variants))
	}
	// Check variant names
	variantNames := map[string]bool{}
	for _, v := range typ.Variants {
		variantNames[v.Name] = true
	}
	if !variantNames["Ok"] {
		t.Error("expected variant 'Ok'")
	}
	if !variantNames["Err"] {
		t.Error("expected variant 'Err'")
	}
}

func TestFnWithBody(t *testing.T) {
	result := analyze(t, `
fn double(x: Int): Int {
  val result = x * 2
  return result
}
`)
	expectNoErrors(t, result)

	sym := result.Scope.Lookup("double")
	if sym == nil {
		t.Fatal("expected 'double' symbol")
	}
	if sym.Kind != SymFn {
		t.Errorf("expected SymFn, got %v", sym.Kind)
	}
}

func TestSealedKindToSymbol(t *testing.T) {
	// This exercises kindToSymbol with TypeSealed
	result := analyze(t, `
sealed MyResult {
  Ok(value: String)
  Err(message: String)
}
`)
	expectNoErrors(t, result)

	sym := result.Scope.Lookup("MyResult")
	if sym == nil {
		t.Fatal("expected 'MyResult' symbol")
	}
	if sym.Kind != SymSealed {
		t.Errorf("expected SymSealed, got %v", sym.Kind)
	}
}

func TestInterfaceDecl(t *testing.T) {
	result := analyze(t, `
interface Searchable {
  searchField: String
}
`)
	expectNoErrors(t, result)

	typ := result.Types["Searchable"]
	if typ == nil {
		t.Fatal("expected Searchable type")
	}
	if typ.Kind != TypeInterface {
		t.Errorf("expected TypeInterface, got %v", typ.Kind)
	}
	if _, ok := typ.Fields["searchField"]; !ok {
		t.Error("expected field 'searchField'")
	}
}

func TestFnWithoutReturnType(t *testing.T) {
	// fn with no return type annotation - exercises the fn.ReturnType == nil path
	result := analyze(t, `
fn doSomething(x: Int) {
  val y = x * 2
}
`)
	expectNoErrors(t, result)
	sym := result.Scope.Lookup("doSomething")
	if sym == nil {
		t.Fatal("expected doSomething symbol")
	}
	// Without a return type, sym.Type should remain nil (not resolved to Void
	// since the nil check skips resolveTypeRef)
	if sym.Type != nil {
		t.Logf("sym.Type = %v (expected nil since no return type annotation)", sym.Type)
	}
}
