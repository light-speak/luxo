package parser

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/token"
)

func parse(t *testing.T, input string) *ast.File {
	t.Helper()
	l := lexer.New(input, "test.luxo")
	tokens, lexErrors := l.Tokenize()
	if len(lexErrors) > 0 {
		t.Fatalf("lexer errors: %v", lexErrors)
	}
	p := New(tokens)
	file, parseErrors := p.Parse("test.luxo")
	if len(parseErrors) > 0 {
		t.Fatalf("parser errors: %v", parseErrors)
	}
	return file
}

func TestParseModel(t *testing.T) {
	input := `model User : Base {
  name: String @varchar(100)
  email: String @unique
  avatar: String?
  role: Role = Role.USER
}`
	file := parse(t, input)

	if len(file.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(file.Models))
	}

	m := file.Models[0]
	if m.Name != "User" {
		t.Errorf("expected model name 'User', got %q", m.Name)
	}
	if len(m.Parents) != 1 || m.Parents[0] != "Base" {
		t.Errorf("expected parent [Base], got %v", m.Parents)
	}
	if len(m.Fields) != 4 {
		t.Fatalf("expected 4 fields, got %d", len(m.Fields))
	}

	// name: String @varchar(100)
	f := m.Fields[0]
	if f.Name != "name" {
		t.Errorf("field[0]: expected 'name', got %q", f.Name)
	}
	if f.Type.Name != "String" {
		t.Errorf("field[0]: expected type 'String', got %q", f.Type.Name)
	}
	if len(f.Directives) != 1 || f.Directives[0].Name != "varchar" {
		t.Errorf("field[0]: expected directive @varchar")
	}

	// avatar: String?
	f2 := m.Fields[2]
	if !f2.Type.Nullable {
		t.Error("field[2]: expected nullable type")
	}

	// role: Role = Role.USER
	f3 := m.Fields[3]
	if f3.Default == nil {
		t.Error("field[3]: expected default value")
	}
}

func TestParseModelMultipleInheritance(t *testing.T) {
	input := `model User : Base, Searchable, Auditable {
  name: String
}`
	file := parse(t, input)
	m := file.Models[0]
	if len(m.Parents) != 3 {
		t.Fatalf("expected 3 parents, got %d", len(m.Parents))
	}
	if m.Parents[0] != "Base" || m.Parents[1] != "Searchable" || m.Parents[2] != "Auditable" {
		t.Errorf("parents: %v", m.Parents)
	}
}

func TestParseModelWithDirective(t *testing.T) {
	input := `model User : Base @crud {
  name: String
}`
	file := parse(t, input)
	m := file.Models[0]
	if len(m.Directives) != 1 || m.Directives[0].Name != "crud" {
		t.Error("expected @crud directive on model")
	}
}

func TestParseEnum(t *testing.T) {
	input := `enum Role {
  USER
  ADMIN
  MODERATOR
}`
	file := parse(t, input)

	if len(file.Enums) != 1 {
		t.Fatalf("expected 1 enum, got %d", len(file.Enums))
	}
	e := file.Enums[0]
	if e.Name != "Role" {
		t.Errorf("expected enum name 'Role', got %q", e.Name)
	}
	if len(e.Values) != 3 {
		t.Errorf("expected 3 values, got %d", len(e.Values))
	}
}

func TestParseInterface(t *testing.T) {
	input := `interface Auditable {
  createdBy: String
  updatedBy: String
  fn beforeCreate() {
  }
}`
	file := parse(t, input)

	if len(file.Interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(file.Interfaces))
	}
	iface := file.Interfaces[0]
	if len(iface.Fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(iface.Fields))
	}
	if len(iface.Methods) != 1 {
		t.Errorf("expected 1 method, got %d", len(iface.Methods))
	}
}

func TestParseSealed(t *testing.T) {
	input := `sealed PayResult {
  Success(transactionId: String)
  Failed(reason: String, code: Int)
  Pending(retryAfter: Duration)
}`
	file := parse(t, input)

	if len(file.Sealeds) != 1 {
		t.Fatalf("expected 1 sealed, got %d", len(file.Sealeds))
	}
	s := file.Sealeds[0]
	if len(s.Variants) != 3 {
		t.Errorf("expected 3 variants, got %d", len(s.Variants))
	}
	if s.Variants[1].Name != "Failed" {
		t.Errorf("expected 'Failed', got %q", s.Variants[1].Name)
	}
	if len(s.Variants[1].Fields) != 2 {
		t.Errorf("expected 2 fields in Failed, got %d", len(s.Variants[1].Fields))
	}
}

func TestParseTypeGeneric(t *testing.T) {
	input := `type Page<T> {
  items: [T]
  total: Int
  page: Int
}`
	file := parse(t, input)

	if len(file.Types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(file.Types))
	}
	td := file.Types[0]
	if td.Name != "Page" {
		t.Errorf("expected 'Page', got %q", td.Name)
	}
	if len(td.TypeParams) != 1 || td.TypeParams[0] != "T" {
		t.Errorf("expected type params [T], got %v", td.TypeParams)
	}
	if len(td.Fields) != 3 {
		t.Errorf("expected 3 fields, got %d", len(td.Fields))
	}
	// items: [T]
	if !td.Fields[0].Type.IsList {
		t.Error("expected items to be list type")
	}
}

func TestParseFn(t *testing.T) {
	input := `fn encrypt(value: String): String @native`
	file := parse(t, input)

	if len(file.Functions) != 1 {
		t.Fatalf("expected 1 fn, got %d", len(file.Functions))
	}
	fn := file.Functions[0]
	if fn.Name != "encrypt" {
		t.Errorf("expected 'encrypt', got %q", fn.Name)
	}
	if fn.Body != nil {
		t.Error("expected no body for @native fn")
	}
	if len(fn.Directives) != 1 || fn.Directives[0].Name != "native" {
		t.Error("expected @native directive")
	}
}

func TestParseExtend(t *testing.T) {
	input := `extend User {
  posts: [Post]
  comments: [Comment]
}`
	file := parse(t, input)

	if len(file.Extends) != 1 {
		t.Fatalf("expected 1 extend, got %d", len(file.Extends))
	}
	ext := file.Extends[0]
	if ext.Name != "User" {
		t.Errorf("expected 'User', got %q", ext.Name)
	}
	if len(ext.Fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(ext.Fields))
	}
	if !ext.Fields[0].Type.IsList {
		t.Error("expected list type for posts")
	}
}

func TestParseUse(t *testing.T) {
	input := `use common.{ Base, Searchable, Page }`
	file := parse(t, input)

	if len(file.Uses) != 1 {
		t.Fatalf("expected 1 use, got %d", len(file.Uses))
	}
	u := file.Uses[0]
	if u.Module != "common" {
		t.Errorf("expected module 'common', got %q", u.Module)
	}
	if len(u.Names) != 3 {
		t.Errorf("expected 3 names, got %d", len(u.Names))
	}
}

func TestParseComputedField(t *testing.T) {
	input := `model Post : Base {
  title: String
  val totalCount: Int get @count
  val avgLikes: Float get @avg(field: likes)
}`
	file := parse(t, input)
	m := file.Models[0]

	if len(m.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(m.Fields))
	}

	// val totalCount: Int get @count
	f := m.Fields[1]
	if f.Name != "totalCount" {
		t.Errorf("expected 'totalCount', got %q", f.Name)
	}
	if f.Computed == nil {
		t.Fatal("expected computed field")
	}
	if len(f.Computed.Directives) != 1 || f.Computed.Directives[0].Name != "count" {
		t.Error("expected @count directive")
	}
}

func TestParseTupleType(t *testing.T) {
	input := `model Comment : Base {
  content: String
  target: (Post, Video, Product)
}`
	file := parse(t, input)
	m := file.Models[0]

	f := m.Fields[1]
	if f.Name != "target" {
		t.Errorf("expected 'target', got %q", f.Name)
	}
	if len(f.Type.Tuple) != 3 {
		t.Fatalf("expected 3 tuple types, got %d", len(f.Type.Tuple))
	}
	if f.Type.Tuple[0].Name != "Post" {
		t.Errorf("expected 'Post', got %q", f.Type.Tuple[0].Name)
	}
}

func TestParseListType(t *testing.T) {
	input := `model User : Base {
  posts: [Post]
  tags: [String]
}`
	file := parse(t, input)
	m := file.Models[0]

	if !m.Fields[0].Type.IsList {
		t.Error("expected posts to be list type")
	}
	if !m.Fields[1].Type.IsList {
		t.Error("expected tags to be list type")
	}
}

func TestParseDocComment(t *testing.T) {
	input := `/// 获取用户信息
api getUser(id: Int): User`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Doc != "获取用户信息" {
		t.Errorf("expected doc comment, got %q", api.Doc)
	}
}

func TestParseError(t *testing.T) {
	input := `error NotFound(resource: String, id: Int) {
  code: 404
  message: error.not_found
}`
	file := parse(t, input)

	if len(file.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(file.Errors))
	}
	e := file.Errors[0]
	if e.Name != "NotFound" {
		t.Errorf("expected 'NotFound', got %q", e.Name)
	}
	if e.Code != 404 {
		t.Errorf("expected code 404, got %d", e.Code)
	}
	if len(e.Fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(e.Fields))
	}
}

func TestParseScope(t *testing.T) {
	input := `model Post : Base {
  status: String
  scope published {
    val x = 1
  }
}`
	file := parse(t, input)
	m := file.Models[0]

	if len(m.Scopes) != 1 {
		t.Fatalf("expected 1 scope, got %d", len(m.Scopes))
	}
	if m.Scopes[0].Name != "published" {
		t.Errorf("expected 'published', got %q", m.Scopes[0].Name)
	}
}

func TestParseComplexSchema(t *testing.T) {
	input := `use common.{ Base, Page }

model User : Base @crud {
  name:     String @varchar(100) @filterable
  email:    String @unique
  password: String @hidden @hash
  role:     Role = Role.USER
  avatar:   String?
  posts:    [Post]
  profile:  Profile?
}

enum Role {
  USER
  ADMIN
}

api getUser(id: Int): User @cache(ttl: 60)

api register(input: RegisterInput): AuthResult {
  input.password.length >= 8 ?: throw error.password_too_short
  val exists = find(User, where: email == input.email)
  exists == null ?: throw error.email_exists
  val user = create(User, name: input.name, email: input.email, password: input.password)
  val tok = generateToken(user, expires: 7d)
  return AuthResult { token: tok, user: user }
}

extend User {
  orders: [Order]
}

fn encrypt(value: String): String @native

error NotFound(resource: String) {
  code: 404
  message: error.not_found
}`

	file := parse(t, input)

	if len(file.Uses) != 1 {
		t.Errorf("expected 1 use, got %d", len(file.Uses))
	}
	if len(file.Models) != 1 {
		t.Errorf("expected 1 model, got %d", len(file.Models))
	}
	if len(file.Enums) != 1 {
		t.Errorf("expected 1 enum, got %d", len(file.Enums))
	}
	if len(file.APIs) != 2 {
		t.Errorf("expected 2 apis, got %d", len(file.APIs))
	}
	if len(file.Extends) != 1 {
		t.Errorf("expected 1 extend, got %d", len(file.Extends))
	}
	if len(file.Functions) != 1 {
		t.Errorf("expected 1 function, got %d", len(file.Functions))
	}
	if len(file.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(file.Errors))
	}

	// register api should have body with 5 statements
	registerAPI := file.APIs[1]
	if registerAPI.Body == nil {
		t.Fatal("expected body for register api")
	}
	if len(registerAPI.Body.Stmts) != 6 {
		t.Errorf("expected 6 statements in register body, got %d", len(registerAPI.Body.Stmts))
	}
}

func TestParseMiddleware(t *testing.T) {
	input := `middleware customAuth @native`
	file := parse(t, input)

	if len(file.Middlewares) != 1 {
		t.Fatalf("expected 1 middleware, got %d", len(file.Middlewares))
	}
	mw := file.Middlewares[0]
	if mw.Name != "customAuth" {
		t.Errorf("expected 'customAuth', got %q", mw.Name)
	}
	if len(mw.Directives) != 1 || mw.Directives[0].Name != "native" {
		t.Error("expected @native directive")
	}
}

func TestParseForeignKey(t *testing.T) {
	input := `model Post : Base {
  authorId: Int
  author: User(key: authorId)
}`
	file := parse(t, input)
	m := file.Models[0]

	f := m.Fields[1]
	if f.Type.FKField != "authorId" {
		t.Errorf("expected FK 'authorId', got %q", f.Type.FKField)
	}
}

func TestParseThroughRelation(t *testing.T) {
	input := `model User : Base {
  roles: [Role] through UserRole
}`
	file := parse(t, input)
	m := file.Models[0]

	f := m.Fields[0]
	if !f.Type.IsList {
		t.Error("expected list type")
	}
	if f.Type.FKField != "through:UserRole" {
		t.Errorf("expected 'through:UserRole', got %q", f.Type.FKField)
	}
}

func TestParseErrorRecovery(t *testing.T) {
	// Invalid syntax: missing colon in field declaration.
	// The parser should not crash/panic but should collect errors.
	input := `model Broken {
  name String
}`
	l := lexer.New(input, "test.luxo")
	tokens, lexErrors := l.Tokenize()
	if len(lexErrors) > 0 {
		t.Fatalf("lexer errors: %v", lexErrors)
	}
	p := New(tokens)
	file, parseErrors := p.Parse("test.luxo")

	// We expect parse errors (missing colon)
	if len(parseErrors) == 0 {
		t.Error("expected parse errors for invalid syntax, got none")
	}
	// The parser should still return a file (not nil/crash)
	if file == nil {
		t.Fatal("expected non-nil file even with errors")
	}
}

// ========== New coverage tests ==========

func parseWithErrors(t *testing.T, input string) (*ast.File, []Error) {
	t.Helper()
	l := lexer.New(input, "test.luxo")
	tokens, lexErrors := l.Tokenize()
	if len(lexErrors) > 0 {
		t.Fatalf("lexer errors: %v", lexErrors)
	}
	p := New(tokens)
	return p.Parse("test.luxo")
}

func TestParseModelUnique(t *testing.T) {
	// @unique must appear as first token in a model body iteration (not after a field)
	// to hit the model-level directive branch at line 145-148
	input := `model User : Base {
  @unique([name, email])
  name: String
  email: String
}`
	file := parse(t, input)
	m := file.Models[0]
	if len(m.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(m.Fields))
	}
	// model-level directive @unique should be in Directives
	found := false
	for _, d := range m.Directives {
		if d.Name == "unique" {
			found = true
		}
	}
	if !found {
		t.Error("expected @unique directive on model")
	}
}

func TestParseComputedWithBody(t *testing.T) {
	input := `model Post : Base {
  title: String
  val commentCount: Int get {
    return 42
  }
}`
	file := parse(t, input)
	m := file.Models[0]
	if len(m.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(m.Fields))
	}
	f := m.Fields[1]
	if f.Name != "commentCount" {
		t.Errorf("expected 'commentCount', got %q", f.Name)
	}
	if f.Computed == nil {
		t.Fatal("expected computed field")
	}
	if f.Computed.Body == nil {
		t.Error("expected computed field body")
	}
}

func TestParseNullableList(t *testing.T) {
	input := `model User : Base {
  posts: [Post]?
}`
	file := parse(t, input)
	m := file.Models[0]
	f := m.Fields[0]
	if !f.Type.IsList {
		t.Error("expected list type")
	}
	if !f.Type.Nullable {
		t.Error("expected nullable list type")
	}
	if f.Type.Name != "Post" {
		t.Errorf("expected inner type 'Post', got %q", f.Type.Name)
	}
}

func TestParseDirectiveWithBody(t *testing.T) {
	input := `model User : Base {
  name: String @transform {
    val x = 1
  }
}`
	file := parse(t, input)
	m := file.Models[0]
	f := m.Fields[0]
	if len(f.Directives) != 1 {
		t.Fatalf("expected 1 directive, got %d", len(f.Directives))
	}
	d := f.Directives[0]
	if d.Name != "transform" {
		t.Errorf("expected 'transform', got %q", d.Name)
	}
	if d.Body == nil {
		t.Error("expected directive body")
	}
}

func TestParseErrorInternal(t *testing.T) {
	input := `error ServerError {
  code: 500
  message: error.internal
  internal: true
}`
	file := parse(t, input)
	if len(file.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(file.Errors))
	}
	e := file.Errors[0]
	if e.Name != "ServerError" {
		t.Errorf("expected 'ServerError', got %q", e.Name)
	}
	if e.Code != 500 {
		t.Errorf("expected code 500, got %d", e.Code)
	}
	if !e.Internal {
		t.Error("expected internal to be true")
	}
}

func TestParseMiddlewareWithBody(t *testing.T) {
	input := `middleware logger {
  val x = 1
}`
	file := parse(t, input)
	if len(file.Middlewares) != 1 {
		t.Fatalf("expected 1 middleware, got %d", len(file.Middlewares))
	}
	mw := file.Middlewares[0]
	if mw.Name != "logger" {
		t.Errorf("expected 'logger', got %q", mw.Name)
	}
	if mw.Body == nil {
		t.Error("expected middleware body")
	}
}

func TestParseInterfaceWithVal(t *testing.T) {
	input := `interface Countable {
  val totalCount: Int get @count
}`
	file := parse(t, input)
	if len(file.Interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(file.Interfaces))
	}
	iface := file.Interfaces[0]
	if len(iface.Fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(iface.Fields))
	}
	f := iface.Fields[0]
	if f.Name != "totalCount" {
		t.Errorf("expected 'totalCount', got %q", f.Name)
	}
	if f.Computed == nil {
		t.Fatal("expected computed field")
	}
	if len(f.Computed.Directives) != 1 || f.Computed.Directives[0].Name != "count" {
		t.Error("expected @count directive")
	}
}

func TestParserErrorMethod(t *testing.T) {
	e := Error{
		Pos:     token.Position{Line: 1, Col: 5, File: "test.luxo"},
		Message: "unexpected token",
	}
	s := e.Error()
	if s == "" {
		t.Error("expected non-empty error string")
	}
	// Should contain the message
	if !contains(s, "unexpected token") {
		t.Errorf("error string should contain message, got %q", s)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchStr(s, substr)
}

func searchStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestParseMultiLineDocComment(t *testing.T) {
	input := `/// Line one
/// Line two
/// Line three
api getUser(id: Int): User`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Doc == "" {
		t.Fatal("expected doc comment")
	}
	// Should contain newlines for multi-line
	if !contains(api.Doc, "\n") {
		t.Errorf("expected multi-line doc, got %q", api.Doc)
	}
	if !contains(api.Doc, "Line one") || !contains(api.Doc, "Line two") || !contains(api.Doc, "Line three") {
		t.Errorf("expected all three lines in doc, got %q", api.Doc)
	}
}

func TestParseDirectiveBeforeSaveBody(t *testing.T) {
	input := `model User : Base {
  name: String @beforeSave {
    val x = 1
  }
}`
	file := parse(t, input)
	m := file.Models[0]
	f := m.Fields[0]
	if len(f.Directives) != 1 {
		t.Fatalf("expected 1 directive, got %d", len(f.Directives))
	}
	if f.Directives[0].Name != "beforeSave" {
		t.Errorf("expected 'beforeSave', got %q", f.Directives[0].Name)
	}
	if f.Directives[0].Body == nil {
		t.Error("expected directive body for @beforeSave")
	}
}

func TestParseDirectiveVisibleBody(t *testing.T) {
	input := `model User : Base {
  name: String @visible {
    val x = 1
  }
}`
	file := parse(t, input)
	m := file.Models[0]
	f := m.Fields[0]
	if len(f.Directives) != 1 {
		t.Fatalf("expected 1 directive, got %d", len(f.Directives))
	}
	if f.Directives[0].Name != "visible" {
		t.Errorf("expected 'visible', got %q", f.Directives[0].Name)
	}
	if f.Directives[0].Body == nil {
		t.Error("expected directive body for @visible")
	}
}

// Test isTypeName with empty string and edge cases.
func TestIsTypeNameEdgeCases(t *testing.T) {
	p := New([]token.Token{{Type: token.EOF}})
	if p.isTypeName("") {
		t.Error("expected false for empty name")
	}
	if !p.isTypeName("Foo") {
		t.Error("expected true for uppercase name")
	}
	if p.isTypeName("foo") {
		t.Error("expected false for lowercase name")
	}
}

// Test nullable list type [Post]? in parseTypeRef.
func TestParseNullableListTypeRef(t *testing.T) {
	input := `extend User {
  tags: [String]?
}`
	file := parse(t, input)
	ext := file.Extends[0]
	f := ext.Fields[0]
	if !f.Type.IsList {
		t.Error("expected list type")
	}
	if !f.Type.Nullable {
		t.Error("expected nullable list")
	}
}

// Test through on non-list type (line 581-583 in parseTypeRef).
func TestParseThroughOnScalarType(t *testing.T) {
	input := `model User : Base {
  profile: Profile through UserProfile
}`
	file := parse(t, input)
	m := file.Models[0]
	f := m.Fields[0]
	if f.Type.Name != "Profile" {
		t.Errorf("expected type 'Profile', got %q", f.Type.Name)
	}
	if f.Type.FKField != "through:UserProfile" {
		t.Errorf("expected FK 'through:UserProfile', got %q", f.Type.FKField)
	}
}
