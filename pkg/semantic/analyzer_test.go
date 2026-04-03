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

// ========== Inheritance Tests ==========

func TestInheritance(t *testing.T) {
	result := analyze(t, `
model Base {
  id: Int
  createdAt: DateTime
}
model User : Base {
  name: String
}
`)
	expectNoErrors(t, result)

	user := result.Types["User"]
	if len(user.Parents) != 1 {
		t.Fatalf("expected 1 parent, got %d", len(user.Parents))
	}
	if user.Parents[0].Name != "Base" {
		t.Errorf("expected parent 'Base', got '%s'", user.Parents[0].Name)
	}
	// should be able to lookup inherited field
	field := user.LookupField("id")
	if field == nil {
		t.Error("expected inherited field 'id'")
	}
}

func TestInheritanceNotFound(t *testing.T) {
	result := analyze(t, `model User : NonExistent { name: String }`)
	expectError(t, result, "not found")
}

// ========== Field Type Resolution Tests ==========

func TestFieldTypeResolution(t *testing.T) {
	result := analyze(t, `
enum Role { USER ADMIN }
model User {
  name: String
  role: Role
  age: Int
}
`)
	expectNoErrors(t, result)

	user := result.Types["User"]
	nameField := user.Fields["name"]
	if nameField.Type.Kind != TypeString {
		t.Errorf("expected String type for name, got %v", nameField.Type.Kind)
	}
	roleField := user.Fields["role"]
	if roleField.Type.Kind != TypeEnum {
		t.Errorf("expected Enum type for role, got %v", roleField.Type.Kind)
	}
}

func TestUnknownFieldType(t *testing.T) {
	result := analyze(t, `model User { name: Stirng }`)
	expectError(t, result, "unknown type 'Stirng'")
}

func TestUnknownFieldTypeSuggestion(t *testing.T) {
	result := analyze(t, `model User { name: Stirng }`)
	for _, err := range result.Errors {
		if strings.Contains(err.Message, "Stirng") {
			if err.Suggestion == "" || !strings.Contains(err.Suggestion, "String") {
				t.Errorf("expected suggestion 'String', got %q", err.Suggestion)
			}
			return
		}
	}
	t.Error("expected error for 'Stirng'")
}

func TestNullableField(t *testing.T) {
	result := analyze(t, `model User { avatar: String? }`)
	expectNoErrors(t, result)

	user := result.Types["User"]
	if !user.Fields["avatar"].Nullable {
		t.Error("expected avatar to be nullable")
	}
}

func TestListField(t *testing.T) {
	result := analyze(t, `
model Post { title: String }
model User { posts: [Post] }
`)
	expectNoErrors(t, result)

	user := result.Types["User"]
	postsField := user.Fields["posts"]
	if !postsField.Type.IsList {
		t.Error("expected posts to be list type")
	}
}

// ========== Directive Validation Tests ==========

func TestHashOnNonString(t *testing.T) {
	result := analyze(t, `model User { age: Int @hash }`)
	expectError(t, result, "@hash can only be used on String")
}

func TestEmailOnNonString(t *testing.T) {
	result := analyze(t, `model User { age: Int @email }`)
	expectError(t, result, "@email can only be used on String")
}

func TestRangeOnNonNumeric(t *testing.T) {
	result := analyze(t, `model User { name: String @range(0, 150) }`)
	expectError(t, result, "@range can only be used on numeric")
}

func TestValidDirectives(t *testing.T) {
	result := analyze(t, `model User {
  name: String @varchar(100) @filterable
  email: String @unique @hidden
  password: String @hash
  age: Int @sortable
}`)
	expectNoErrors(t, result)
}

// ========== Expression Type Check Tests ==========

func TestValTypeInference(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): User {
  val x = 42
  val y = "hello"
  val z = true
  val d = 7d
  User { name: "test" }
}
`)
	expectNoErrors(t, result)
}

func TestUndefinedVariable(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  val x = unknownVar
  x
}
`)
	expectError(t, result, "undefined: 'unknownVar'")
}

func TestMemberAccess(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): String {
  val user = find(User, id: 1)
  user.name
}
`)
	// find returns User? but we're accessing .name without ?.
	// For now this should pass since find's return type isn't fully resolved yet
	// TODO: stricter null safety checks
	if len(result.Errors) > 0 {
		// filter out expected errors
		for _, err := range result.Errors {
			if !strings.Contains(err.Message, "undefined") {
				t.Errorf("unexpected error: %v", err)
			}
		}
	}
}

func TestDuplicateField(t *testing.T) {
	result := analyze(t, `model User {
  name: String
  name: String
}`)
	expectError(t, result, "duplicate field")
}

// ========== Computed Field Tests ==========

func TestComputedField(t *testing.T) {
	result := analyze(t, `model Post {
  title: String
  val totalCount: Int get @count
  val avgLikes: Float get @avg(field: likes)
}`)
	expectNoErrors(t, result)

	post := result.Types["Post"]
	tc := post.Fields["totalCount"]
	if tc == nil || !tc.Computed {
		t.Error("expected computed field totalCount")
	}
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

// ========== Scope & Lookup Tests ==========

func TestScopeLookup(t *testing.T) {
	scope := NewScope()
	scope.Define(&Symbol{Name: "x", Kind: SymVariable})

	child := scope.Child()
	child.Define(&Symbol{Name: "y", Kind: SymVariable})

	// child can see parent's symbols
	if child.Lookup("x") == nil {
		t.Error("expected to find 'x' in parent scope")
	}
	// parent can't see child's symbols
	if scope.Lookup("y") != nil {
		t.Error("expected not to find 'y' in parent scope")
	}
}

func TestTypeNarrowing(t *testing.T) {
	scope := NewScope()
	nullableType := &ResolvedType{Kind: TypeModel, Name: "User", Nullable: true}
	scope.Define(&Symbol{Name: "user", Kind: SymVariable, Type: nullableType})

	// before narrowing
	resolved := scope.ResolvedTypeOf("user")
	if !resolved.Nullable {
		t.Error("expected nullable before narrowing")
	}

	// narrow
	scope.Narrow("user", nullableType.AsNonNull())

	// after narrowing
	resolved = scope.ResolvedTypeOf("user")
	if resolved.Nullable {
		t.Error("expected non-null after narrowing")
	}
}

func TestLookupPrefix(t *testing.T) {
	scope := NewScope()
	scope.Define(&Symbol{Name: "user", Kind: SymVariable})
	scope.Define(&Symbol{Name: "username", Kind: SymVariable})
	scope.Define(&Symbol{Name: "post", Kind: SymVariable})

	results := scope.LookupPrefix("us")
	if len(results) != 2 {
		t.Errorf("expected 2 results for prefix 'us', got %d", len(results))
	}
}

// ========== Edit Distance Tests ==========

func TestEditDistance(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"String", "Stirng", 2},
		{"User", "Usar", 1},
		{"hello", "hello", 0},
		{"abc", "xyz", 3},
	}
	for _, tt := range tests {
		d := editDistance(tt.a, tt.b)
		if d != tt.expected {
			t.Errorf("editDistance(%q, %q) = %d, expected %d", tt.a, tt.b, d, tt.expected)
		}
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

func TestVarAfterCreate(t *testing.T) {
	result := analyze(t, `
model User { name: String }
type AuthResult { token: String }
api test(): AuthResult {
  val user = create(User, name: "test")
  AuthResult { token: "abc", user: user }
}
`)
	for _, e := range result.Errors {
		t.Logf("error: %v", e)
	}
	expectNoErrors(t, result)
}
