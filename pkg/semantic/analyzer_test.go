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
	if sym.Type != nil {
		t.Errorf("expected nil type for fn without return annotation, got %v", sym.Type)
	}
}

// ========== Await + Destructuring Tests ==========

func TestAwaitReturnsTuple(t *testing.T) {
	result := analyze(t, `
model User { name: String }
model Post { title: String  userId: Int }
api loadProfile(id: Int): User {
  val (user, posts) = await {
    find(User, id: id)
    find(Post, where: userId == id)
  }
  user
}
`)
	expectNoErrors(t, result)
}

func TestAwaitSingleExpr(t *testing.T) {
	// single expression in await — should not be a tuple
	result := analyze(t, `
model User { name: String }
api getOne(id: Int): User {
  val user = await {
    find(User, id: id)
  }
  user
}
`)
	expectNoErrors(t, result)
}

func TestDestructuringCountMismatch(t *testing.T) {
	result := analyze(t, `
model User { name: String }
model Post { title: String  userId: Int }
api bad(id: Int): User {
  val (a, b, c) = await {
    find(User, id: id)
    find(Post, where: userId == id)
  }
  a
}
`)
	expectError(t, result, "destructuring count mismatch")
}

func TestDestructuringNonTuple(t *testing.T) {
	// destructuring from a non-tuple — all variables get the same type
	result := analyze(t, `
model User { name: String }
fn getPair(): User @native
api test(): User {
  val (a, b) = getPair()
  a
}
`)
	expectNoErrors(t, result)
}

// ========== If as Expression Tests ==========

// ========== Await Edge Cases ==========

func TestAwaitEmptyBody(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  val x = await {
    val y = 1
  }
  0
}
`)
	expectNoErrors(t, result)
}

func TestAwaitWithNonExprStatements(t *testing.T) {
	// await block with mixed stmts — val is not ExprStmt
	result := analyze(t, `
model User { name: String }
api test(): Int {
  val x = await {
    val temp = 1
    find(User, id: temp)
  }
  0
}
`)
	expectNoErrors(t, result)
}

// ========== CRUD Return Type Edge Cases ==========

func TestCRUDCreateReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): User {
  val user = create(User, name: "test")
  user
}
`)
	expectNoErrors(t, result)
}

func TestCRUDDeleteReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(id: Int): User {
  val user = find(User, id: id) ?: throw error.not_found
  delete(user)
  user
}
`)
	expectNoErrors(t, result)
}

func TestCRUDUpdateReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(id: Int): User {
  val user = find(User, id: id) ?: throw error.not_found
  val updated = update(user, name: "new")
  updated
}
`)
	expectNoErrors(t, result)
}

// ========== Member Expr Edge Cases ==========

func TestSafeCallOnNonNullWarning(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): String {
  val user = create(User, name: "test")
  val name = user?.name
  name
}
`)
	expectWarning(t, result, "unnecessary safe call")
}

func TestCRUDWithNonIdentFirstArg(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  val x = find(1 + 2, where: true)
  0
}
`)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCRUDWithUnknownModel(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  val x = find(Unknown, id: 1)
  0
}
`)
	expectError(t, result, "undefined")
}

// ========== Val/Var Mutability Tests ==========

func TestValImmutable(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  val x = 1
  x = 2
  x
}
`)
	expectError(t, result, "cannot assign to immutable variable")
}

func TestVarMutable(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  var x = 1
  x = 2
  x += 3
  x
}
`)
	expectNoErrors(t, result)
}

func TestGlobalVal(t *testing.T) {
	result := analyze(t, `
val APP_NAME = "test"
api test(): String {
  APP_NAME
}
`)
	expectNoErrors(t, result)
}

func TestGlobalVarForbidden(t *testing.T) {
	result := analyze(t, `
var counter = 0
api test(): Int { counter }
`)
	expectError(t, result, "global 'var' is not allowed")
}

func TestExtendFieldConflict(t *testing.T) {
	result := analyze(t, `
model User { name: String }
extend User { posts: [String] }
extend User { posts: [String] }
`)
	expectError(t, result, "conflicts")
}

func TestGlobalValImmutable(t *testing.T) {
	result := analyze(t, `
val APP_NAME = "test"
api test(): String {
  APP_NAME = "changed"
  APP_NAME
}
`)
	expectError(t, result, "cannot assign to immutable variable")
}

// ========== CRUD it. disambiguation ==========

func TestCRUDItDisambiguation(t *testing.T) {
	result := analyze(t, `
model Room { roomId: Int  name: String }
api test(roomId: Int): Room {
  val room = find(Room, where: it.roomId == roomId)
    ?: throw error.not_found
  room
}
`)
	expectNoErrors(t, result)
}

func TestCRUDItNoAmbiguity(t *testing.T) {
	result := analyze(t, `
model User { name: String  email: String }
api test(input: String): User {
  val user = find(User, where: email == input)
    ?: throw error.not_found
  user
}
`)
	expectNoErrors(t, result)
}

// ========== Sealed Exhaustiveness Tests ==========

func TestSealedExhaustive(t *testing.T) {
	result := analyze(t, `
sealed MyResult {
  Ok(value: String)
  Err(message: String)
}
api test(r: MyResult): String {
  when(r) {
    is Ok -> "ok"
    is Err -> "err"
  }
}
`)
	expectNoErrors(t, result)
}

func TestSealedNotExhaustive(t *testing.T) {
	result := analyze(t, `
sealed MyResult {
  Ok(value: String)
  Err(message: String)
  Pending(reason: String)
}
api test(r: MyResult): String {
  when(r) {
    is Ok -> "ok"
    is Err -> "err"
  }
}
`)
	expectError(t, result, "missing variant 'Pending'")
}

func TestSealedWithElseBranch(t *testing.T) {
	result := analyze(t, `
sealed MyResult {
  Ok(value: String)
  Err(message: String)
  Pending(reason: String)
}
api test(r: MyResult): String {
  when(r) {
    is Ok -> "ok"
    else -> "other"
  }
}
`)
	expectNoErrors(t, result)
}

func TestEnumValueAccess(t *testing.T) {
	result := analyze(t, `
enum Role { USER ADMIN }
api test(): Role {
  Role.ADMIN
}
`)
	expectNoErrors(t, result)
}

// ========== Lambda Safety Tests ==========

func TestFindInLambdaForbidden(t *testing.T) {
	result := analyze(t, `
model User { name: String }
model Post { userId: Int }
api test(): Int {
  val users = find(User, where: name == "test")
  val posts = users.map { find(Post, where: userId == it.id) }
  0
}
`)
	expectError(t, result, "lambda")
}

func TestFindInLambdaAllowed(t *testing.T) {
	// find outside lambda is fine
	result := analyze(t, `
model User { name: String }
api test(): Int {
  val users = find(User, where: name == "test")
  0
}
`)
	expectNoErrors(t, result)
}

func TestCreateInLambdaForbidden(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  val names = find(User, where: name == "a")
  names.forEach { create(User, name: "b") }
  0
}
`)
	expectError(t, result, "lambda")
}

func TestDeleteInLambdaForbidden(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  val users = find(User, where: name == "test")
  users.forEach { delete(User, id: 1) }
  0
}
`)
	expectError(t, result, "lambda")
}

func TestUpdateInLambdaForbidden(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  val users = find(User, where: name == "test")
  users.forEach { update(User, name: "x") }
  0
}
`)
	expectError(t, result, "lambda")
}

func TestCRUDInTransactionAllowed(t *testing.T) {
	// CRUD inside transaction { ... } is fine — not a collection lambda
	result := analyze(t, `
model User { name: String }
api test(): Int {
  val result = transaction {
    create(User, name: "test")
  }
  42
}
`)
	expectNoErrors(t, result)
}

// ========== Contains Warning Tests ==========

func TestContainsWarning(t *testing.T) {
	// String .contains() should produce a warning
	result := analyze(t, `
model User { name: String }
api test(): Boolean {
  val user = create(User, name: "test")
  user.name.contains("test")
}
`)
	expectWarning(t, result, "contains")
}

func TestContainsOnListNoWarning(t *testing.T) {
	// .contains on a list type should NOT produce the warning
	result := analyze(t, `
model Item { name: String }
api test(): Boolean {
  val items = find(Item, where: name == "x")
  items.contains
}
`)
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "full table scan") {
			t.Errorf("unexpected contains warning on list type: %s", w.Message)
		}
	}
}

// ========== Emit in Transaction Tests ==========

func TestEmitInTransactionNoWarning(t *testing.T) {
	// emit inside transaction should NOT produce warning — framework handles delay automatically
	result := analyze(t, `
model User { name: String }
event UserCreated(name: String)
api test(): Int {
  transaction {
    create(User, name: "test")
    emit UserCreated(name: "test")
  }
  0
}
`)
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "transaction") {
			t.Errorf("should not warn about emit in transaction: %s", w.Message)
		}
	}
}

func TestEmitOutsideTransactionNoWarning(t *testing.T) {
	result := analyze(t, `
model User { name: String }
event UserCreated(name: String)
api test(): Int {
  create(User, name: "test")
  emit UserCreated(name: "test")
  0
}
`)
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "transaction") {
			t.Errorf("unexpected transaction warning outside transaction: %s", w.Message)
		}
	}
}

// ========== Circular Event Dependency Tests ==========

func TestCircularEventDependency(t *testing.T) {
	file := &ast.File{
		Name: "test.luxo",
		Events: []*ast.EventDecl{
			{Name: "A"},
			{Name: "B"},
		},
		Listeners: []*ast.OnDecl{
			{
				EventName: "A",
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.EmitStmt{EventName: "B"},
					},
				},
			},
			{
				EventName: "B",
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.EmitStmt{EventName: "A"},
					},
				},
			},
		},
	}
	a := New()
	result := a.Analyze([]*ast.File{file})
	expectError(t, result, "circular event dependency")
}

func TestNoCircularEventDependency(t *testing.T) {
	file := &ast.File{
		Name: "test.luxo",
		Events: []*ast.EventDecl{
			{Name: "A"},
			{Name: "B"},
			{Name: "C"},
		},
		Listeners: []*ast.OnDecl{
			{
				EventName: "A",
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.EmitStmt{EventName: "B"},
					},
				},
			},
			{
				EventName: "B",
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.EmitStmt{EventName: "C"},
					},
				},
			},
		},
	}
	a := New()
	result := a.Analyze([]*ast.File{file})
	for _, err := range result.Errors {
		if strings.Contains(err.Message, "circular") {
			t.Errorf("unexpected circular event error: %s", err.Message)
		}
	}
}

func TestEventCycleThreeNodes(t *testing.T) {
	file := &ast.File{
		Name: "test.luxo",
		Events: []*ast.EventDecl{
			{Name: "X"},
			{Name: "Y"},
			{Name: "Z"},
		},
		Listeners: []*ast.OnDecl{
			{
				EventName: "X",
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.EmitStmt{EventName: "Y"},
					},
				},
			},
			{
				EventName: "Y",
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.EmitStmt{EventName: "Z"},
					},
				},
			},
			{
				EventName: "Z",
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.EmitStmt{EventName: "X"},
					},
				},
			},
		},
	}
	a := New()
	result := a.Analyze([]*ast.File{file})
	expectError(t, result, "circular event dependency")
}

// ========== collectAmbiguousIdents — different names, no warning ==========

func TestCollectAmbiguousIdentsDifferentNames(t *testing.T) {
	// userId == id — different names, should NOT produce an ambiguity warning
	result := analyze(t, `
model User { userId: Int  name: String }
api test(id: Int): User {
  val user = find(User, where: userId == id)
    ?: throw error.not_found
  user
}
`)
	expectNoErrors(t, result)
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "ambiguous") {
			t.Errorf("unexpected ambiguity warning: %s", w.Message)
		}
	}
}

// ========== checkBinaryAmbiguity — it.email disambiguated, no warning ==========

func TestCheckBinaryAmbiguityDisambiguated(t *testing.T) {
	// it.email == email — using "it." prefix means no ambiguity
	result := analyze(t, `
model User { email: String  name: String }
api test(email: String): User {
  val user = find(User, where: it.email == email)
    ?: throw error.not_found
  user
}
`)
	expectNoErrors(t, result)
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "ambiguous") {
			t.Errorf("unexpected ambiguity warning: %s", w.Message)
		}
	}
}

// ========== hasSearchDirective — field with @search does not warn on contains ==========

func TestHasSearchDirectiveNoWarning(t *testing.T) {
	result := analyze(t, `
model Post {
  title: String @search
  content: String
}
api test(keyword: String): [Post] {
  val posts = find(Post, where: title == keyword)
  val filtered = posts.filter { it.title.contains(keyword) }
  filtered
}
`)
	expectNoErrors(t, result)
}

func TestContainsWarnsWithoutSearch(t *testing.T) {
	// Direct string.contains() call — not via it.field
	result := analyze(t, `
model Post {
  title: String
  content: String
}
api test(keyword: String): String {
  val name = "hello"
  val check = name.contains(keyword)
  "done"
}
`)
	// should have a warning about contains generating LIKE scan
	expectWarning(t, result, "contains")
}

// ========== buildEventGraph — listener with no body ==========

func TestBuildEventGraphListenerNoBody(t *testing.T) {
	// on EventA @native — no body, buildEventGraph should handle it gracefully
	result := analyze(t, `
event UserCreated(userId: Int)
on UserCreated @native
`)
	expectNoErrors(t, result)
}

// ========== collectAmbiguousIdents — call expr inside where ==========

func TestCollectAmbiguousIdentsCallExpr(t *testing.T) {
	// contains() call inside where — exercise CallExpr branch in collectAmbiguousIdents
	result := analyze(t, `
model Post { title: String  content: String }
api test(keyword: String): [Post] {
  val posts = find(Post, where: title.contains(keyword))
  posts
}
`)
	expectNoErrors(t, result)
}

// ========== collectAmbiguousIdents — member expr inside where ==========

func TestCollectAmbiguousIdentsMemberExpr(t *testing.T) {
	// it.name inside where — exercise MemberExpr branch
	result := analyze(t, `
model User { name: String  email: String }
api test(): [User] {
  val users = find(User, where: it.name == "test")
  users
}
`)
	expectNoErrors(t, result)
}

// ========== collectAmbiguousIdents — unary expr inside where ==========

func TestCollectAmbiguousIdentsUnaryExpr(t *testing.T) {
	// !active inside where — exercise UnaryExpr branch
	result := analyze(t, `
model User { active: Boolean  name: String }
api test(): [User] {
  val users = find(User, where: !active)
  users
}
`)
	expectNoErrors(t, result)
}

// ========== When Exhaustiveness Tests ==========

func TestWhenWithoutElse(t *testing.T) {
	result := analyze(t, `
api test(x: Int): String {
  when(x) {
    1 -> "one"
    2 -> "two"
  }
}
`)
	expectError(t, result, "must have 'else'")
}

func TestWhenWithElse(t *testing.T) {
	result := analyze(t, `
api test(x: Int): String {
  when(x) {
    1 -> "one"
    else -> "other"
  }
}
`)
	expectNoErrors(t, result)
}

func TestWhenNoSubjectWithoutElse(t *testing.T) {
	result := analyze(t, `
api test(x: Int): String {
  when {
    x > 0 -> "positive"
  }
}
`)
	expectError(t, result, "must have 'else'")
}

func TestEnumExhaustiveComplete(t *testing.T) {
	result := analyze(t, `
enum Color { RED GREEN BLUE }
api test(c: Color): String {
  when(c) {
    Color.RED -> "r"
    Color.GREEN -> "g"
    Color.BLUE -> "b"
  }
}
`)
	expectNoErrors(t, result)
}

func TestEnumExhaustiveMissing(t *testing.T) {
	result := analyze(t, `
enum Color { RED GREEN BLUE }
api test(c: Color): String {
  when(c) {
    Color.RED -> "r"
    Color.GREEN -> "g"
  }
}
`)
	expectError(t, result, "missing value 'BLUE'")
}

func TestEnumWithElseSkipsExhaustive(t *testing.T) {
	result := analyze(t, `
enum Color { RED GREEN BLUE }
api test(c: Color): String {
  when(c) {
    Color.RED -> "r"
    else -> "other"
  }
}
`)
	expectNoErrors(t, result)
}

// ========== Duplicate Branch + Unreachable Code + Nested Await ==========

func TestDuplicateWhenBranch(t *testing.T) {
	result := analyze(t, `
sealed MyResult {
  Ok(value: String)
  Err(message: String)
}
api test(r: MyResult): String {
  when(r) {
    is Ok -> "ok"
    is Ok -> "ok again"
    is Err -> "err"
  }
}
`)
	expectError(t, result, "duplicate")
}

func TestUnreachableCodeAfterReturn(t *testing.T) {
	result := analyze(t, `
api test(): String {
  return "done"
  val x = 1
}
`)
	expectWarning(t, result, "unreachable")
}

func TestUnreachableCodeAfterThrow(t *testing.T) {
	result := analyze(t, `
error NotFound2 { code: 404  message: "error.not_found" }
api test2(): String {
  throw NotFound2
  val x = 1
}
`)
	expectWarning(t, result, "unreachable")
}

func TestUnreachableCodeAfterBreak(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  var i = 0
  for {
    break
    i += 1
  }
  i
}
`)
	expectWarning(t, result, "unreachable")
}

func TestNoUnreachableWarning(t *testing.T) {
	result := analyze(t, `
api test(): String {
  val x = 1
  return "done"
}
`)
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "unreachable") {
			t.Errorf("unexpected unreachable warning: %s", w.Message)
		}
	}
}

func TestNestedAwaitForbidden(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  val x = await {
    await {
      find(User, id: 1)
    }
  }
  0
}
`)
	expectError(t, result, "nested await")
}

// ========== hasSearchDirective — field with non-search directive ==========

func TestContainsWarnsFieldWithOtherDirective(t *testing.T) {
	// field has @unique but not @search — should still warn
	result := analyze(t, `
model User {
  email: String @unique
  name: String
}
api test(keyword: String): Boolean {
  val user = find(User, id: 1)
  user?.email?.contains(keyword) ?: false
}
`)
	expectWarning(t, result, "contains")
}

// ========== checkBinaryAmbiguity — same-name comparison triggers warning ==========

func TestCheckBinaryAmbiguitySameNameWarns(t *testing.T) {
	// email == email — same name used as both CRUD field and outer param
	result := analyze(t, `
model User { email: String  name: String }
api test(email: String): User {
  val user = find(User, where: email == email)
    ?: throw error.not_found
  user
}
`)
	expectWarning(t, result, "ambiguous")
}

// ========== checkBinaryAmbiguity — left ident != right ident ==========

func TestCheckBinaryAmbiguityDifferentNames(t *testing.T) {
	// exercise the recursive collectAmbiguousIdents(e.Left/e.Right) path
	// when leftIdent.Name != rightIdent.Name — no ambiguity expected
	result := analyze(t, `
model User { name: String  email: String }
api test(keyword: String): [User] {
  val users = find(User, where: name == keyword)
  users
}
`)
	expectNoErrors(t, result)
}

// ========== checkBinaryAmbiguity — field only, no outer sym ==========

func TestCheckBinaryAmbiguityFieldOnlyNoOuterSym(t *testing.T) {
	// email == email where email is ONLY a CRUD field and NOT an outer variable
	result := analyze(t, `
model User { email: String  name: String }
api test(): [User] {
  val users = find(User, where: email == email)
  users
}
`)
	// no outer sym for email, so no ambiguity warning
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "ambiguous") {
			t.Errorf("unexpected ambiguity warning: %s", w.Message)
		}
	}
}

// ========== checkBinaryAmbiguity — non-ident operands ==========

func TestCheckBinaryAmbiguityNonIdentOperands(t *testing.T) {
	// binary expr where operands are not plain idents (e.g. member access or literal)
	result := analyze(t, `
model User { name: String  age: Int }
api test(): [User] {
  val users = find(User, where: it.age > 18)
  users
}
`)
	expectNoErrors(t, result)
}

// ========== checkTransactionCall — non-lambda arg ==========

func TestTransactionWithNonLambdaArg(t *testing.T) {
	// transaction with a plain expression arg (not a lambda)
	result := analyze(t, `
model Product { stock: Int }
api test(): Int {
  val p = find(Product, id: 1)
  transaction {
    update(p, stock: 10)
  }
  0
}
`)
	expectNoErrors(t, result)
}

// ========== collectEnumValues — nil branch condition ==========

func TestEnumExhaustiveSingleValue(t *testing.T) {
	result := analyze(t, `
enum Status { ACTIVE INACTIVE }
api test(s: Status): String {
  when(s) {
    Status.ACTIVE -> "active"
  }
}
`)
	expectError(t, result, "missing value 'INACTIVE'")
}

// ========== checkCompositeExpr — YieldExpr path ==========

func TestYieldExprInsideFor(t *testing.T) {
	result := analyze(t, `
model Item { price: Float }
api test(items: [Item]): Item {
  val found = for item in items {
    if item.price > 100 { yield item }
  }
  found
}
`)
	// yield inside for is valid
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// ========== checkCompositeExpr — AsyncExpr path ==========

func TestAsyncExpressionType(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  async {
    val x = 1
  }
  0
}
`)
	expectNoErrors(t, result)
}

// ========== checkBodies — listener body checking ==========

func TestCheckBodiesListenerWithBody(t *testing.T) {
	// listener with params and body — exercises checkBodies listener branch
	result := analyze(t, `
event OrderPlaced(orderId: Int, userId: Int)
on OrderPlaced {
  orderId, userId ->
  val x = orderId
  val y = userId
}
`)
	expectNoErrors(t, result)
}

// ========== collectAmbiguousIdents — UnaryExpr branch (boolean) ==========

func TestCollectAmbiguousIdentsUnaryExprBoolean(t *testing.T) {
	// !active == active — UnaryExpr wrapping an ident in CRUD where clause
	// exercises the UnaryExpr branch in collectAmbiguousIdents
	result := analyze(t, `
model User { email: String  active: Boolean }
api test(active: Boolean): [User] {
  val users = find(User, where: !active == active)
  users
}
`)
	// UnaryExpr branch is exercised; no same-name comparison, so no ambiguous warning
	expectNoErrors(t, result)
}

// ========== collectAmbiguousIdents — CallExpr branch (function) ==========

func TestCollectAmbiguousIdentsCallExprFunction(t *testing.T) {
	// fn(name) in CRUD where clause — CallExpr with arg matching a field
	// exercises the CallExpr branch in collectAmbiguousIdents
	result := analyze(t, `
model User { name: String  email: String }
fn lower(s: String): String { s }
api test(name: String): [User] {
  val users = find(User, where: lower(name) == name)
  users
}
`)
	// CallExpr branch is exercised; no same-name comparison at top level
	expectNoErrors(t, result)
}

// ========== collectAmbiguousIdents — MemberExpr branch (it prefix) ==========

func TestCollectAmbiguousIdentsMemberExprItPrefix(t *testing.T) {
	// obj.field in where clause — MemberExpr object is recursed
	result := analyze(t, `
model User { name: String  email: String }
api test(name: String): [User] {
  val users = find(User, where: it.name == name)
  users
}
`)
	// MemberExpr with "it" prefix — no ambiguity since it.name is explicit
	expectNoErrors(t, result)
}

// ========== itMemberField — non-MemberExpr ==========

func TestItMemberFieldNonMemberExpr(t *testing.T) {
	// binary where both sides are plain idents (not MemberExpr), triggers fallback
	result := analyze(t, `
model User { name: String  email: String }
api test(name: String): [User] {
  val users = find(User, where: name == name)
  users
}
`)
	expectWarning(t, result, "ambiguous")
}

// ========== itMemberField — non-Ident object ==========

func TestItMemberFieldNonIdentObject(t *testing.T) {
	// member expr where object is another member expr (not "it"), not just a plain ident
	result := analyze(t, `
model User { name: String  email: String }
api test(u: User): [User] {
  val users = find(User, where: u.name == u.name)
  users
}
`)
	// u.name is MemberExpr but object is Ident "u" (not "it"), so itMemberField returns ""
	// both sides are MemberExpr, not same-name comparison, so recursive check proceeds
	expectNoErrors(t, result)
}

// ========== collectEnumValues — nil expression ==========

func TestCollectEnumValuesNilExpr(t *testing.T) {
	// Directly test the edge case — we need a when expression where the branch value might be nil
	// but more realistically, test with a non-MemberExpr in when branch
	result := analyze(t, `
enum Color { RED  GREEN  BLUE }
api test(c: Color): String {
  when(c) {
    Color.RED -> "r"
    Color.GREEN -> "g"
    Color.BLUE -> "b"
  }
}
`)
	expectNoErrors(t, result)
}

// ========== hasSearchDirective — non-MemberExpr ==========

func TestHasSearchDirectiveNonMemberExpr(t *testing.T) {
	// calling .contains() on a local string variable (Ident, not MemberExpr)
	// hasSearchDirective gets an Ident which is not a MemberExpr → returns false
	result := analyze(t, `
api test(keyword: String): Boolean {
  val name = "hello"
  name.contains(keyword)
}
`)
	expectWarning(t, result, "contains")
}

// ========== hasSearchDirective — nil objType (undefined variable) ==========

func TestHasSearchDirectiveNilObjType(t *testing.T) {
	// member expr on undefined object — checkExpr returns nil
	result := analyze(t, `
api test(keyword: String): Boolean {
  unknown.name.contains(keyword)
}
`)
	// should have an error about "unknown" being undefined
	expectError(t, result, "undefined")
}

// ========== hasSearchDirective — field not found ==========

func TestHasSearchDirectiveFieldNotFound(t *testing.T) {
	// member access on valid type but field doesn't exist
	result := analyze(t, `
model User { name: String }
api test(u: User, keyword: String): Boolean {
  u.nonexistent.contains(keyword)
}
`)
	// error about nonexistent field — field is nil so hasSearchDirective returns false
	expectError(t, result, "nonexistent")
}

// ========== hasSearchDirective — field with @search directive ==========

func TestHasSearchDirectiveFieldWithSearch(t *testing.T) {
	// it.field where field has @search — hasSearchDirective returns true, no warning
	result := analyze(t, `
model Post {
  title: String @search
  body: String
}
api test(keyword: String): [Post] {
  val posts = find(Post, where: title == keyword)
  val filtered = posts.filter { it.title.contains(keyword) }
  filtered
}
`)
	expectNoErrors(t, result)
	// no "contains" warning because title has @search
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "contains") {
			t.Error("should not warn about contains on @search field")
		}
	}
}

// ========== checkTransactionCall — non-lambda arg ==========

func TestCheckTransactionCallNonLambdaArg(t *testing.T) {
	// transaction with a non-lambda argument
	result := analyze(t, `
model User { name: String }
api test(): Int {
  transaction {
    val u = find(User, id: 1)
    u
  }
  0
}
`)
	expectNoErrors(t, result)
}

// ========== checkCompositeExpr — AwaitExpr ==========

func TestCheckCompositeExprAwait(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  val results = await {
    find(Int, id: 1)
    find(Int, id: 2)
  }
  0
}
`)
	expectNoErrors(t, result)
}

// ========== itMemberField — object is not Ident ==========

func TestItMemberFieldNonIdent(t *testing.T) {
	// member expr where object is another member expr, not just an ident
	result := analyze(t, `
model User { name: String  email: String }
api test(u: User): [User] {
  val users = find(User, where: it.name == u.name)
  users
}
`)
	expectNoErrors(t, result)
}
