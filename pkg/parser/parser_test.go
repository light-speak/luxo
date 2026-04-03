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

func TestParseElvisExpr(t *testing.T) {
	input := `api test(): User {
  val user = find(User, id: 1) ?: throw error.not_found
  return user
}`
	file := parse(t, input)
	api := file.APIs[0]

	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	elvis, ok := valStmt.Value.(*ast.ElvisExpr)
	if !ok {
		t.Fatalf("expected ElvisExpr, got %T", valStmt.Value)
	}
	if elvis.Left == nil || elvis.Right == nil {
		t.Error("elvis left or right is nil")
	}
}

func TestParseWhenExpr(t *testing.T) {
	input := `api test(): String {
  val level = when(score) {
    in 90..100 -> "A"
    in 80..89 -> "B"
    else -> "F"
  }
  return level
}`
	file := parse(t, input)
	api := file.APIs[0]

	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	when, ok := valStmt.Value.(*ast.WhenExpr)
	if !ok {
		t.Fatalf("expected WhenExpr, got %T", valStmt.Value)
	}
	if when.Subject == nil {
		t.Error("expected when subject")
	}
	if len(when.Branches) != 2 {
		t.Errorf("expected 2 branches, got %d", len(when.Branches))
	}
	if when.Else == nil {
		t.Error("expected else branch")
	}
}

func TestParseForStmt(t *testing.T) {
	input := `api test(): Int {
  for item in items {
    update(item, status: "done")
  }
  return 0
}`
	file := parse(t, input)
	api := file.APIs[0]

	forStmt, ok := api.Body.Stmts[0].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected ForStmt, got %T", api.Body.Stmts[0])
	}
	if forStmt.VarName != "item" {
		t.Errorf("expected 'item', got %q", forStmt.VarName)
	}
}

func TestParseIfStmt(t *testing.T) {
	input := `api test(): Int {
  if condition {
    return 1
  } else {
    return 0
  }
}`
	file := parse(t, input)
	api := file.APIs[0]

	ifStmt, ok := api.Body.Stmts[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected IfStmt, got %T", api.Body.Stmts[0])
	}
	if ifStmt.Then == nil {
		t.Error("expected then block")
	}
	if ifStmt.Else == nil {
		t.Error("expected else block")
	}
}

func TestParseEmitStmt(t *testing.T) {
	input := `api test(): Boolean {
  emit("order.created", order, userId: order.userId)
  return true
}`
	file := parse(t, input)
	api := file.APIs[0]

	emitStmt, ok := api.Body.Stmts[0].(*ast.EmitStmt)
	if !ok {
		t.Fatalf("expected EmitStmt, got %T", api.Body.Stmts[0])
	}
	if len(emitStmt.Args) != 3 {
		t.Errorf("expected 3 args, got %d", len(emitStmt.Args))
	}
	// third arg should be named: userId: order.userId
	if emitStmt.Args[2].Name != "userId" {
		t.Errorf("expected named arg 'userId', got %q", emitStmt.Args[2].Name)
	}
}

func TestParseMemberAccess(t *testing.T) {
	input := `api test(): String {
  val name = user.name
  val city = user?.address?.city
  return name
}`
	file := parse(t, input)
	api := file.APIs[0]

	// user.name
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	member, ok := valStmt.Value.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr, got %T", valStmt.Value)
	}
	if member.Field != "name" || member.SafeCall {
		t.Error("expected .name (not safe)")
	}

	// user?.address?.city
	valStmt2 := api.Body.Stmts[1].(*ast.ValStmt)
	member2, ok := valStmt2.Value.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr, got %T", valStmt2.Value)
	}
	if member2.Field != "city" || !member2.SafeCall {
		t.Error("expected ?.city (safe)")
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

func TestParseTransaction(t *testing.T) {
	input := `api test(): Order {
  val order = transaction {
    update(product, stock: 0)
    create(Order, total: 100)
  }
  return order
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)

	_, ok := valStmt.Value.(*ast.TransactionExpr)
	if !ok {
		t.Fatalf("expected TransactionExpr, got %T", valStmt.Value)
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

func TestParseBinaryExpr(t *testing.T) {
	input := `api test(): Boolean {
  val result = a + b * c
  return result
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)

	// should be: a + (b * c) due to precedence
	binExpr, ok := valStmt.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr, got %T", valStmt.Value)
	}
	if binExpr.Op != "+" {
		t.Errorf("expected '+' at top level, got %q", binExpr.Op)
	}
	rightBin, ok := binExpr.Right.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected right to be BinaryExpr, got %T", binExpr.Right)
	}
	if rightBin.Op != "*" {
		t.Errorf("expected '*' in right, got %q", rightBin.Op)
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

func TestStatementBoundaryAfterCall(t *testing.T) {
	input := `api test(): Int {
  val x = create(User, name: "test")
  val y = 42
  y
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
	t.Logf("stmts count: %d", len(api.Body.Stmts))
	for i, s := range api.Body.Stmts {
		t.Logf("stmt[%d]: %T", i, s)
	}
	if len(api.Body.Stmts) != 3 {
		t.Errorf("expected 3 statements, got %d", len(api.Body.Stmts))
	}
}

func TestStatementBoundaryAfterObjectExpr(t *testing.T) {
	input := `api test(): Int {
  val x = AuthResult { token: "abc", user: "test" }
  val y = 42
  y
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
	t.Logf("stmts count: %d", len(api.Body.Stmts))
	for i, s := range api.Body.Stmts {
		t.Logf("stmt[%d]: %T", i, s)
	}
	if len(api.Body.Stmts) != 3 {
		t.Errorf("expected 3 statements, got %d", len(api.Body.Stmts))
	}
}

func TestStatementBoundaryCreateThenObjectExpr(t *testing.T) {
	input := `api test(): Int {
  val user = create(User, name: "test")
  AuthResult { token: "abc", user: user }
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
	t.Logf("stmts count: %d", len(api.Body.Stmts))
	for i, s := range api.Body.Stmts {
		t.Logf("stmt[%d]: %T", i, s)
		if vs, ok := s.(*ast.ValStmt); ok {
			t.Logf("  val %s = %T", vs.Name, vs.Value)
		}
		if es, ok := s.(*ast.ExprStmt); ok {
			t.Logf("  expr: %T", es.Expr)
		}
	}
	if len(api.Body.Stmts) != 2 {
		t.Errorf("expected 2 statements, got %d", len(api.Body.Stmts))
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

func TestParseWhenIsType(t *testing.T) {
	input := `api test(): String {
  val x = when(result) {
    is Success -> "ok"
    is Failed -> "err"
    else -> "?"
  }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	when, ok := valStmt.Value.(*ast.WhenExpr)
	if !ok {
		t.Fatalf("expected WhenExpr, got %T", valStmt.Value)
	}
	if when.Subject == nil {
		t.Error("expected when subject")
	}
	if len(when.Branches) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(when.Branches))
	}
	if when.Branches[0].IsType != "Success" {
		t.Errorf("expected IsType 'Success', got %q", when.Branches[0].IsType)
	}
	if when.Branches[1].IsType != "Failed" {
		t.Errorf("expected IsType 'Failed', got %q", when.Branches[1].IsType)
	}
	if when.Else == nil {
		t.Error("expected else branch")
	}
}

func TestParseWhenNoSubject(t *testing.T) {
	input := `api test(): String {
  val x = when {
    x > 0 -> "pos"
    else -> "neg"
  }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	when, ok := valStmt.Value.(*ast.WhenExpr)
	if !ok {
		t.Fatalf("expected WhenExpr, got %T", valStmt.Value)
	}
	if when.Subject != nil {
		t.Error("expected no subject for when without parens")
	}
	if len(when.Branches) != 1 {
		t.Fatalf("expected 1 branch, got %d", len(when.Branches))
	}
	if when.Else == nil {
		t.Error("expected else branch")
	}
}

func TestParseTrailingLambda(t *testing.T) {
	input := `api test(): String {
  val x = items.filter { it.active }
  val y = items.map { it.name }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]

	// items.filter { it.active }
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	call, ok := valStmt.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", valStmt.Value)
	}
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr func, got %T", call.Func)
	}
	if member.Field != "filter" {
		t.Errorf("expected 'filter', got %q", member.Field)
	}
	if len(call.Args) != 1 {
		t.Fatalf("expected 1 arg (lambda), got %d", len(call.Args))
	}
	_, ok = call.Args[0].Value.(*ast.LambdaExpr)
	if !ok {
		t.Fatalf("expected LambdaExpr arg, got %T", call.Args[0].Value)
	}

	// items.map { it.name }
	valStmt2 := api.Body.Stmts[1].(*ast.ValStmt)
	call2, ok := valStmt2.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", valStmt2.Value)
	}
	member2, ok := call2.Func.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr func, got %T", call2.Func)
	}
	if member2.Field != "map" {
		t.Errorf("expected 'map', got %q", member2.Field)
	}
}

func TestParseChainedCalls(t *testing.T) {
	input := `api test(): String {
  val x = users.filter { it.active }.map { it.name }.sortBy { it }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)

	// The outermost should be a CallExpr for .sortBy { it }
	call, ok := valStmt.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", valStmt.Value)
	}
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr func, got %T", call.Func)
	}
	if member.Field != "sortBy" {
		t.Errorf("expected 'sortBy', got %q", member.Field)
	}
}

func TestParseNestedMemberAccess(t *testing.T) {
	input := `api test(): String {
  val x = a.b.c.d
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)

	// a.b.c.d => MemberExpr{Object: MemberExpr{Object: MemberExpr{Object: Ident{a}, Field: b}, Field: c}, Field: d}
	m, ok := valStmt.Value.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr, got %T", valStmt.Value)
	}
	if m.Field != "d" {
		t.Errorf("expected field 'd', got %q", m.Field)
	}
	m2, ok := m.Object.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr for .c, got %T", m.Object)
	}
	if m2.Field != "c" {
		t.Errorf("expected field 'c', got %q", m2.Field)
	}
	m3, ok := m2.Object.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr for .b, got %T", m2.Object)
	}
	if m3.Field != "b" {
		t.Errorf("expected field 'b', got %q", m3.Field)
	}
}

func TestParseSafeDotChain(t *testing.T) {
	input := `api test(): String {
  val x = user?.address?.city?.name
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)

	// user?.address?.city?.name
	m, ok := valStmt.Value.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr, got %T", valStmt.Value)
	}
	if m.Field != "name" || !m.SafeCall {
		t.Errorf("expected ?.name (safe), got field=%q safe=%v", m.Field, m.SafeCall)
	}
	m2, ok := m.Object.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr for ?.city, got %T", m.Object)
	}
	if m2.Field != "city" || !m2.SafeCall {
		t.Errorf("expected ?.city (safe), got field=%q safe=%v", m2.Field, m2.SafeCall)
	}
	m3, ok := m2.Object.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr for ?.address, got %T", m2.Object)
	}
	if m3.Field != "address" || !m3.SafeCall {
		t.Errorf("expected ?.address (safe), got field=%q safe=%v", m3.Field, m3.SafeCall)
	}
}

func TestParseUnaryNot(t *testing.T) {
	input := `api test(): Boolean {
  val x = !condition
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)

	unary, ok := valStmt.Value.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("expected UnaryExpr, got %T", valStmt.Value)
	}
	if unary.Op != "!" {
		t.Errorf("expected op '!', got %q", unary.Op)
	}
	ident, ok := unary.Value.(*ast.Ident)
	if !ok {
		t.Fatalf("expected Ident, got %T", unary.Value)
	}
	if ident.Name != "condition" {
		t.Errorf("expected 'condition', got %q", ident.Name)
	}
}

func TestParseUnaryMinus(t *testing.T) {
	input := `api test(): Int {
  val x = -42
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)

	unary, ok := valStmt.Value.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("expected UnaryExpr, got %T", valStmt.Value)
	}
	if unary.Op != "-" {
		t.Errorf("expected op '-', got %q", unary.Op)
	}
	lit, ok := unary.Value.(*ast.Literal)
	if !ok {
		t.Fatalf("expected Literal, got %T", unary.Value)
	}
	if lit.Value != "42" {
		t.Errorf("expected '42', got %q", lit.Value)
	}
}

func TestParseParenExpr(t *testing.T) {
	input := `api test(): Int {
  val x = (a + b) * c
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)

	// (a + b) * c => BinaryExpr{Left: BinaryExpr{a + b}, Op: *, Right: c}
	bin, ok := valStmt.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr, got %T", valStmt.Value)
	}
	if bin.Op != "*" {
		t.Errorf("expected '*' at top level, got %q", bin.Op)
	}
	inner, ok := bin.Left.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr for (a + b), got %T", bin.Left)
	}
	if inner.Op != "+" {
		t.Errorf("expected '+' inside parens, got %q", inner.Op)
	}
}

func TestParseListExpr(t *testing.T) {
	input := `api test(): Int {
  val x = [1, 2, 3]
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)

	list, ok := valStmt.Value.(*ast.ListExpr)
	if !ok {
		t.Fatalf("expected ListExpr, got %T", valStmt.Value)
	}
	if len(list.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(list.Items))
	}
	for i, item := range list.Items {
		lit, ok := item.(*ast.Literal)
		if !ok {
			t.Fatalf("item[%d]: expected Literal, got %T", i, item)
		}
		expected := []string{"1", "2", "3"}
		if lit.Value != expected[i] {
			t.Errorf("item[%d]: expected %q, got %q", i, expected[i], lit.Value)
		}
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

func TestParseThrowStmt(t *testing.T) {
	input := `api test(): Int {
  throw error.not_found
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
	if len(api.Body.Stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(api.Body.Stmts))
	}
	throwStmt, ok := api.Body.Stmts[0].(*ast.ThrowStmt)
	if !ok {
		t.Fatalf("expected ThrowStmt, got %T", api.Body.Stmts[0])
	}
	if throwStmt.Error == nil {
		t.Error("expected non-nil error expression")
	}
}

func TestParseElseIf(t *testing.T) {
	input := `api test(): Int {
  if x {
    return 1
  } else if y {
    return 2
  } else {
    return 3
  }
}`
	file := parse(t, input)
	api := file.APIs[0]
	ifStmt, ok := api.Body.Stmts[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected IfStmt, got %T", api.Body.Stmts[0])
	}
	if ifStmt.Then == nil {
		t.Error("expected then block")
	}
	if ifStmt.Else == nil {
		t.Fatal("expected else block")
	}
	// else block should contain a nested IfStmt
	if len(ifStmt.Else.Stmts) != 1 {
		t.Fatalf("expected 1 stmt in else block, got %d", len(ifStmt.Else.Stmts))
	}
	innerIf, ok := ifStmt.Else.Stmts[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected nested IfStmt, got %T", ifStmt.Else.Stmts[0])
	}
	if innerIf.Then == nil {
		t.Error("expected inner then block")
	}
	if innerIf.Else == nil {
		t.Error("expected inner else block")
	}
}

func TestParseValWithType(t *testing.T) {
	input := `api test(): Int {
  val x: Int = 42
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt, ok := api.Body.Stmts[0].(*ast.ValStmt)
	if !ok {
		t.Fatalf("expected ValStmt, got %T", api.Body.Stmts[0])
	}
	if valStmt.Name != "x" {
		t.Errorf("expected 'x', got %q", valStmt.Name)
	}
	if valStmt.Type == nil {
		t.Fatal("expected type annotation")
	}
	if valStmt.Type.Name != "Int" {
		t.Errorf("expected type 'Int', got %q", valStmt.Type.Name)
	}
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

func TestParseWhenBodyBlock(t *testing.T) {
	input := `api test(): String {
  val x = when(status) {
    "active" -> {
      val y = 1
      return "ok"
    }
    else -> "no"
  }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	when, ok := valStmt.Value.(*ast.WhenExpr)
	if !ok {
		t.Fatalf("expected WhenExpr, got %T", valStmt.Value)
	}
	if len(when.Branches) != 1 {
		t.Fatalf("expected 1 branch, got %d", len(when.Branches))
	}
	// Body should be a LambdaExpr (block body)
	_, ok = when.Branches[0].Body.(*ast.LambdaExpr)
	if !ok {
		t.Fatalf("expected LambdaExpr for block body, got %T", when.Branches[0].Body)
	}
}

func TestParseBoolAndNullLiterals(t *testing.T) {
	input := `api test(): Boolean {
  val a = true
  val b = false
  val c = null
  return a
}`
	file := parse(t, input)
	api := file.APIs[0]

	// true
	valA := api.Body.Stmts[0].(*ast.ValStmt)
	litA, ok := valA.Value.(*ast.Literal)
	if !ok {
		t.Fatalf("expected Literal, got %T", valA.Value)
	}
	if litA.Value != "true" {
		t.Errorf("expected 'true', got %q", litA.Value)
	}

	// false
	valB := api.Body.Stmts[1].(*ast.ValStmt)
	litB, ok := valB.Value.(*ast.Literal)
	if !ok {
		t.Fatalf("expected Literal, got %T", valB.Value)
	}
	if litB.Value != "false" {
		t.Errorf("expected 'false', got %q", litB.Value)
	}

	// null
	valC := api.Body.Stmts[2].(*ast.ValStmt)
	litC, ok := valC.Value.(*ast.Literal)
	if !ok {
		t.Fatalf("expected Literal, got %T", valC.Value)
	}
	if litC.Value != "null" {
		t.Errorf("expected 'null', got %q", litC.Value)
	}
}

func TestParseFloatAndDurationLiterals(t *testing.T) {
	input := `api test(): Float {
  val a = 3.14
  val b = 7d
  return a
}`
	file := parse(t, input)
	api := file.APIs[0]

	// 3.14
	valA := api.Body.Stmts[0].(*ast.ValStmt)
	litA, ok := valA.Value.(*ast.Literal)
	if !ok {
		t.Fatalf("expected Literal, got %T", valA.Value)
	}
	if litA.Value != "3.14" {
		t.Errorf("expected '3.14', got %q", litA.Value)
	}

	// 7d
	valB := api.Body.Stmts[1].(*ast.ValStmt)
	litB, ok := valB.Value.(*ast.Literal)
	if !ok {
		t.Fatalf("expected Literal, got %T", valB.Value)
	}
	if litB.Value != "7d" {
		t.Errorf("expected '7d', got %q", litB.Value)
	}
}

func TestParseTransactionExpr(t *testing.T) {
	input := `api test(): Order {
  val result = transaction {
    val x = create(Order, total: 100)
    return x
  }
  return result
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	txn, ok := valStmt.Value.(*ast.TransactionExpr)
	if !ok {
		t.Fatalf("expected TransactionExpr, got %T", valStmt.Value)
	}
	if txn.Body == nil {
		t.Error("expected transaction body")
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

func TestParseConditionExprNullCheck(t *testing.T) {
	// Use null in a non-condition context (val assignment) to cover null literal in condition expr
	input := `api test(): Int {
  val isNull = x == null
  return 0
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt, ok := api.Body.Stmts[0].(*ast.ValStmt)
	if !ok {
		t.Fatalf("expected ValStmt, got %T", api.Body.Stmts[0])
	}
	binExpr, ok := valStmt.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr, got %T", valStmt.Value)
	}
	if binExpr.Op != "==" {
		t.Errorf("expected '==', got %q", binExpr.Op)
	}
	// Right side should be null literal
	lit, ok := binExpr.Right.(*ast.Literal)
	if !ok {
		t.Fatalf("expected Literal for null, got %T", binExpr.Right)
	}
	if lit.Value != "null" {
		t.Errorf("expected 'null', got %q", lit.Value)
	}
}

func TestParseWhenConditionStopAtRBrace(t *testing.T) {
	input := `api test(): String {
  val x = when {
    a > b -> "yes"
    else -> "no"
  }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	when, ok := valStmt.Value.(*ast.WhenExpr)
	if !ok {
		t.Fatalf("expected WhenExpr, got %T", valStmt.Value)
	}
	if len(when.Branches) != 1 {
		t.Fatalf("expected 1 branch, got %d", len(when.Branches))
	}
	if when.Else == nil {
		t.Error("expected else branch")
	}
}

func TestParseFindCreateExpr(t *testing.T) {
	input := `api test(): User {
  val u = find(User, id: 1)
  val o = create(Order, total: 100)
  return u
}`
	file := parse(t, input)
	api := file.APIs[0]

	// find(User, id: 1)
	valU := api.Body.Stmts[0].(*ast.ValStmt)
	call, ok := valU.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", valU.Value)
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok {
		t.Fatalf("expected Ident func, got %T", call.Func)
	}
	if ident.Name != "find" {
		t.Errorf("expected 'find', got %q", ident.Name)
	}

	// create(Order, total: 100)
	valO := api.Body.Stmts[1].(*ast.ValStmt)
	call2, ok := valO.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", valO.Value)
	}
	ident2, ok := call2.Func.(*ast.Ident)
	if !ok {
		t.Fatalf("expected Ident func, got %T", call2.Func)
	}
	if ident2.Name != "create" {
		t.Errorf("expected 'create', got %q", ident2.Name)
	}
}

func TestParseExprStmtStuckRecovery(t *testing.T) {
	// Use a token that can't start an expression to trigger stuck recovery
	input := `api test(): Int {
  @badtoken
  return 1
}`
	// This will produce errors but shouldn't hang
	file, errs := parseWithErrors(t, input)
	if file == nil {
		t.Fatal("expected non-nil file")
	}
	_ = errs // errors are expected
}

func TestParseBlockStuckRecovery(t *testing.T) {
	// A block that might get stuck on an unexpected token
	input := `api test(): Int {
  )
  return 1
}`
	file, errs := parseWithErrors(t, input)
	if file == nil {
		t.Fatal("expected non-nil file")
	}
	_ = errs
}

func TestParseExpectIdentError(t *testing.T) {
	// Test expectIdent error: override without api produces an error via expectIdent
	// The "override" consumes one token, then the parser checks for "api" and errors
	// This covers the error path in expectIdent indirectly
	input := `override fn test(): Int`
	_, errs := parseWithErrors(t, input)
	if len(errs) == 0 {
		t.Error("expected parser errors for override without api")
	}
}

func TestParseCurrentEOF(t *testing.T) {
	// Parse an empty input - exercises current() EOF check
	input := ``
	file := parse(t, input)
	if file == nil {
		t.Fatal("expected non-nil file")
	}
}

func TestParsePeekEOF(t *testing.T) {
	// Single token then EOF - exercises peek() EOF check
	input := `api getUser(id: Int): User`
	file := parse(t, input)
	if len(file.APIs) != 1 {
		t.Fatalf("expected 1 api, got %d", len(file.APIs))
	}
}

func TestParseBinaryOpVariants(t *testing.T) {
	input := `api test(): Boolean {
  val a = x == y
  val b = x != y
  val c = x >= y
  val d = x <= y
  val e = x && y
  val f = x || y
  val g = x % y
  val h = x / y
  val i = x - y
  val j = x is Y
  val k = x in y
  return a
}`
	file := parse(t, input)
	api := file.APIs[0]
	// Verify all parsed as BinaryExpr
	ops := []string{"==", "!=", ">=", "<=", "&&", "||", "%", "/", "-", "is", "in"}
	for idx, op := range ops {
		valStmt := api.Body.Stmts[idx].(*ast.ValStmt)
		bin, ok := valStmt.Value.(*ast.BinaryExpr)
		if !ok {
			t.Fatalf("stmt[%d]: expected BinaryExpr, got %T", idx, valStmt.Value)
		}
		if bin.Op != op {
			t.Errorf("stmt[%d]: expected op %q, got %q", idx, op, bin.Op)
		}
	}
}

func TestParseIsCallable(t *testing.T) {
	// Test trailing lambda with ident (isCallable path)
	input := `api test(): String {
  val x = myFunc { it }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	call, ok := valStmt.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", valStmt.Value)
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok {
		t.Fatalf("expected Ident func, got %T", call.Func)
	}
	if ident.Name != "myFunc" {
		t.Errorf("expected 'myFunc', got %q", ident.Name)
	}
}

func TestParseIsTypeNameEmpty(t *testing.T) {
	// Triggers isTypeName with a non-uppercase name (lowercase won't be treated as object construction)
	input := `api test(): Int {
  val x = lowercase
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	ident, ok := valStmt.Value.(*ast.Ident)
	if !ok {
		t.Fatalf("expected Ident (not object construction), got %T", valStmt.Value)
	}
	if ident.Name != "lowercase" {
		t.Errorf("expected 'lowercase', got %q", ident.Name)
	}
}

func TestParseOverrideNonApi(t *testing.T) {
	// override followed by non-api should produce error
	input := `override model Foo {
  name: String
}`
	_, errs := parseWithErrors(t, input)
	if len(errs) == 0 {
		t.Error("expected error for 'override' without 'api'")
	}
}

func TestParseUnexpectedTopLevelToken(t *testing.T) {
	input := `12345`
	_, errs := parseWithErrors(t, input)
	if len(errs) == 0 {
		t.Error("expected error for unexpected top-level token")
	}
}

func TestParseCommentTopLevel(t *testing.T) {
	input := `// this is a comment
api getUser(id: Int): User`
	file := parse(t, input)
	if len(file.APIs) != 1 {
		t.Fatalf("expected 1 api, got %d", len(file.APIs))
	}
}

func TestParseCommentInBlock(t *testing.T) {
	// Cover Comment/DocComment skip in parseStmt
	input := `api test(): Int {
  // a comment inside a block
  return 1
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
	// Comment should be skipped, only return remains
	if len(api.Body.Stmts) != 1 {
		t.Errorf("expected 1 statement, got %d", len(api.Body.Stmts))
	}
}

func TestParseDocCommentInBlock(t *testing.T) {
	// Cover DocComment skip in parseStmt
	input := `api test(): Int {
  /// a doc comment inside a block
  return 1
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
	if len(api.Body.Stmts) != 1 {
		t.Errorf("expected 1 statement, got %d", len(api.Body.Stmts))
	}
}

func TestParseUpdateDeleteExpr(t *testing.T) {
	// Cover update/delete branches in parsePrefixExpr
	input := `api test(): Int {
  update(User, id: 1, name: "new")
  delete(User, id: 1)
  return 0
}`
	file := parse(t, input)
	api := file.APIs[0]
	if len(api.Body.Stmts) != 3 {
		t.Fatalf("expected 3 statements, got %d", len(api.Body.Stmts))
	}
}

func TestParseTrailingLambdaOnCallArgs(t *testing.T) {
	// Cover trailing lambda after parseCallArgs (line 982-986)
	input := `api test(): Int {
  val x = find(User, id: 1) {
    val y = 1
  }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	call, ok := valStmt.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", valStmt.Value)
	}
	// Should have args including trailing lambda
	if len(call.Args) < 2 {
		t.Errorf("expected at least 2 args (including trailing lambda), got %d", len(call.Args))
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
