package semantic

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/token"
)

func TestConcurrentAnalysesAreIsolated(t *testing.T) {
	const workers = 64
	var wait sync.WaitGroup
	errors := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			modelName := fmt.Sprintf("Model%d", index)
			input := "model " + modelName + " { value: Int }"
			tokens, lexErrors := lexer.New(input, modelName+".luxo").Tokenize()
			if len(lexErrors) > 0 {
				errors <- fmt.Errorf("worker %d lexer: %v", index, lexErrors)
				return
			}
			file, parseErrors := parser.New(tokens).Parse(modelName + ".luxo")
			if len(parseErrors) > 0 {
				errors <- fmt.Errorf("worker %d parser: %v", index, parseErrors)
				return
			}
			result := Analyze([]*ast.File{file})
			if len(result.Errors) > 0 || result.Types[modelName] == nil {
				errors <- fmt.Errorf("worker %d result: errors=%v types=%v", index, result.Errors, result.Types)
			}
		}(i)
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

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
	result := analyze(t, `fn encrypt(value: String): Result<String> @native`)
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
  val user = User.create(name: "test")
  AuthResult { token: "abc", user: user }
}

fn encrypt(value: String): Result<String> @native

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
    User.find(id: id)
    Post.find(where: userId == id)
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
    User.find(id: id)
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
    User.find(id: id)
    Post.find(where: userId == id)
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
fn getPair(): User @native @service
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
    User.find(id: temp)
  }
  0
}
`)
	expectNoErrors(t, result)
}

// ========== CRUD Return Type Edge Cases ==========

// TestCRUDCreateReturnType removed — duplicated by TestCreateReturnsModelType in analyzer_crud_test.go

func TestCRUDDeleteReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(id: Int): User {
  val user = User.find(id: id)
  user.delete()
  user
}
`)
	expectNoErrors(t, result)
}

func TestCRUDUpdateReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(id: Int): User {
  val user = User.find(id: id)
  val updated = user.update(name: "new")
  updated
}
`)
	expectNoErrors(t, result)
}

func TestSaveMethod(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(id: Int): User {
  val user = User.find(id: id) ?: throw error.not_found
  user.name = "updated"
  user.save()
  user
}
`)
	expectNoErrors(t, result)
}

// ========== Member Expr Edge Cases ==========

func TestSafeCallOnNonNullWarning(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): String {
  val user = User.create(name: "test")
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
  val x = Unknown.find(id: 1)
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
  val room = Room.find(where: it.roomId == roomId).firstOrNull
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
  val user = User.find(where: email == input).firstOrNull
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
  val users = User.find(where: name == "test")
  val posts = users.map { Post.find(where: userId == it.id) }
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
  val users = User.find(where: name == "test")
  0
}
`)
	expectNoErrors(t, result)
}

func TestCreateInLambdaForbidden(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  val names = User.find(where: name == "a")
  names.forEach { User.create(name: "b") }
  0
}
`)
	expectError(t, result, "lambda")
}

func TestDeleteInLambdaForbidden(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  val users = User.find(where: name == "test")
  users.forEach { User.delete(id: 1) }
  0
}
`)
	expectError(t, result, "lambda")
}

func TestUpdateInLambdaForbidden(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  val users = User.find(where: name == "test")
  users.forEach { User.update(name: "x") }
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
    User.create(name: "test")
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
  val user = User.create(name: "test")
  user.name.contains("test")
}
`)
	expectWarning(t, result, "contains")
}

func TestContainsOnListNoWarning(t *testing.T) {
	// .contains on a list type should NOT produce the LIKE warning
	result := analyze(t, `
model Item { name: String @index }
api test(): Boolean {
  val items = Item.find(where: name == "x")
  items.contains
}
`)
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "LIKE") {
			t.Errorf("unexpected contains/LIKE warning on list type: %s", w.Message)
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
    User.create(name: "test")
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
  User.create(name: "test")
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
  val user = User.find(where: userId == id).firstOrNull
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
  val user = User.find(where: it.email == email).firstOrNull
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
  val posts = Post.find(where: title == keyword)
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
  val posts = Post.find(where: title.contains(keyword))
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
  val users = User.find(where: it.name == "test")
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
  val users = User.find(where: !active)
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
      User.find(id: 1)
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
  val user = User.find(id: 1)
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
  val user = User.find(where: email == email)
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
  val users = User.find(where: name == keyword)
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
  val users = User.find(where: email == email)
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
  val users = User.find(where: it.age > 18)
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
  val p = Product.find(id: 1)
  transaction {
    p.update(stock: 10)
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
  val users = User.find(where: !active == active)
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
  val users = User.find(where: lower(name) == name)
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
  val users = User.find(where: it.name == name)
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
  val users = User.find(where: name == name)
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
  val users = User.find(where: u.name == u.name)
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
  val posts = Post.find(where: title == keyword)
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
    val u = User.find(id: 1)
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
model User { name: String }
api test(): Int {
  val results = await {
    User.find(id: 1)
    User.find(id: 2)
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
  val users = User.find(where: it.name == u.name)
  users
}
`)
	expectNoErrors(t, result)
}

// ========== New CRUD Operation Tests ==========

func TestAggregateOperation(t *testing.T) {
	result := analyze(t, `
model Order {
  total: Float
  status: String
}
api test(): Int {
  val agg = aggregate(Order, sum: total, count: true)
  0
}
`)
	expectNoErrors(t, result)
}

func TestGroupByOperation(t *testing.T) {
	result := analyze(t, `
model Order {
  total: Float
  status: String
}
api test(): Int {
  val grouped = groupBy(Order, by: status, sum: total)
  0
}
`)
	expectNoErrors(t, result)
}

func TestPaginateOperation(t *testing.T) {
	result := analyze(t, `
model User {
  name: String
}
api test(): Int {
  val page = paginate(User, page: 1, pageSize: 20)
  0
}
`)
	expectNoErrors(t, result)
}

func TestRawOperation(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  val r = raw("SELECT 1")
  0
}
`)
	expectNoErrors(t, result)
}

// ========== @withAuth Directive Tests ==========

func TestWithAuthDirective(t *testing.T) {
	result := analyze(t, `
model User @withAuth(stores: [id, role]) {
  name: String
  email: String
}
`)
	expectNoErrors(t, result)

	// verify injected methods have Doc strings
	userType := result.Types["User"]
	if userType == nil {
		t.Fatal("User type not found")
	}
	if f, ok := userType.Fields["createToken"]; !ok {
		t.Error("createToken field not injected")
	} else if f.Doc == "" {
		t.Error("createToken field missing Doc string")
	}
	if f, ok := userType.Fields["verify"]; !ok {
		t.Error("verify field not injected")
	} else if f.Doc == "" {
		t.Error("verify field missing Doc string")
	}
}

func TestWithAuthDirectiveRefreshTokenDoc(t *testing.T) {
	result := analyze(t, `
model User @withAuth(stores: [id, role]) {
  name: String
  email: String
}
`)
	expectNoErrors(t, result)

	userType := result.Types["User"]
	if userType == nil {
		t.Fatal("User type not found")
	}
	// refreshToken is always injected — runtime env controls whether it's enabled
	if f, ok := userType.Fields["refreshToken"]; !ok {
		t.Error("refreshToken field not injected")
	} else if f.Doc == "" {
		t.Error("refreshToken field missing Doc string")
	}
}

func TestWithAuthStoresInjectIdentityFields(t *testing.T) {
	// Regression: my.teamId should work when teamId is in @withAuth(stores: [...])
	result := analyze(t, `
model Member @crud @withAuth(stores: [id, teamId, role]) {
  id: Int @id @auto @serial
  teamId: Int
  name: String
  role: String
}
api test: [Member] @auth {
  val members = Member.where(it.teamId == my.teamId).all()
  return members
}
`)
	expectNoErrors(t, result)

	identityType := result.Types["Identity"]
	if identityType == nil {
		t.Fatal("Identity type not found")
	}
	if _, ok := identityType.Fields["teamId"]; !ok {
		t.Error("teamId should be injected into Identity from @withAuth stores")
	}
}

func TestHashInjectsVerifyPassword(t *testing.T) {
	// Regression: @hash field should inject verifyPassword method on model
	result := analyze(t, `
model User @crud @withAuth(stores: [id]) {
  id: Int @id @auto @serial
  email: String
  password: String @hash @hidden
}
api login(email: String, password: String): User {
  val user = User.where(it.email == email).first()
  user ?: throw error.NotFound
  user.verifyPassword(password) ?: throw error.NotFound
  return user
}
`)
	expectNoErrors(t, result)

	userType := result.Types["User"]
	if userType == nil {
		t.Fatal("User type not found")
	}
	if _, ok := userType.Fields["verifyPassword"]; !ok {
		t.Error("verifyPassword should be injected for @hash field")
	}
}

func TestHashVerifyPasswordConflict(t *testing.T) {
	// User-defined verifyPassword field should conflict with @hash injection
	result := analyze(t, `
model User @crud {
  id: Int @id @auto @serial
  password: String @hash @hidden
  verifyPassword: String
}
`)
	expectError(t, result, "conflicts with @hash")
}

func TestWithAuthStoresSkipsMethod(t *testing.T) {
	// Methods like createToken should not be injected into Identity
	result := analyze(t, `
model User @withAuth(stores: [id, role]) {
  id: Int @id @auto @serial
  name: String
  role: String
}
api test: Int @auth {
  return my.id
}
`)
	expectNoErrors(t, result)

	identityType := result.Types["Identity"]
	if identityType == nil {
		t.Fatal("Identity type not found")
	}
	// createToken is a method on User, should NOT be in Identity
	if fi, ok := identityType.Fields["createToken"]; ok && !fi.IsMethod {
		t.Error("createToken method should not leak into Identity as a field")
	}
}

func TestWithAuthStoresNonListValue(t *testing.T) {
	// stores with non-list value should not crash
	result := analyze(t, `
model User @withAuth(stores: id) {
  id: Int @id @auto @serial
  name: String
}
`)
	// may have validation error for stores format, but should not panic
	_ = result
}

func TestDurationProperties(t *testing.T) {
	// Duration properties on Int variables: n.days, n.hours, n.minutes, n.seconds
	// Note: literal 7.days is lexed as FLOAT("7.") + IDENT("days"), use variable instead
	result := analyze(t, `
model Task @crud {
  id: Int @id @auto @serial
  name: String
}
api test(n: Int): Boolean {
  val d = n.days
  val h = n.hours
  val m = n.minutes
  val s = n.seconds
  val ms = n.milliseconds
  return true
}
`)
	expectNoErrors(t, result)
}

func TestNowReturnsDateTime(t *testing.T) {
	result := analyze(t, `
model Log @crud {
  id: Int @id @auto @serial
  timestamp: DateTime
}
api test: Boolean {
  val t = now()
  return true
}
`)
	expectNoErrors(t, result)
}

// ========== @auth Directive with Model References ==========

func TestAuthDirectiveWithModels(t *testing.T) {
	result := analyze(t, `
model User { name: String }
model Admin { name: String }
api getProfile(): Int @auth(User, Admin)
`)
	expectNoErrors(t, result)
}

// ========== isDynamicReturnCRUD Unit Tests ==========

func TestIsDynamicReturnCRUD(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"aggregate", true},
		{"groupBy", true},
		{"raw", true},
		{"paginate", true},
		{"find", false},
		{"create", false},
		{"update", false},
		{"delete", false},
		{"unknown", false},
	}
	for _, tt := range tests {
		got := isDynamicReturnCRUD(tt.name)
		if got != tt.want {
			t.Errorf("isDynamicReturnCRUD(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// ========== Method Without Call Error Tests ==========

func TestMethodWithoutCallError(t *testing.T) {
	// user.createToken without () should be a compile error
	result := analyze(t, `
model User @withAuth(stores: [id, role]) {
  name: String
  email: String
}
api test(): String {
  val user = User.create(name: "test", email: "a@b.com")
  val t = user.createToken
  t
}
`)
	expectError(t, result, "is a method, use createToken()")
}

func TestMethodWithCallOK(t *testing.T) {
	// user.createToken() with () should be fine
	result := analyze(t, `
model User @withAuth(stores: [id, role]) {
  name: String
  email: String
}
api test(): String {
  val user = User.create(name: "test", email: "a@b.com")
  val t = user.createToken()
  t
}
`)
	expectNoErrors(t, result)
}

func TestMethodVerifyWithoutCallError(t *testing.T) {
	// user.verify without () should be a compile error
	result := analyze(t, `
model User @withAuth(stores: [id, role]) {
  name: String
  email: String
}
api test(): Boolean {
  val user = User.create(name: "test", email: "a@b.com")
  val v = user.verify
  v
}
`)
	expectError(t, result, "is a method, use verify()")
}

func TestMethodRefreshTokenWithoutCallError(t *testing.T) {
	// user.refreshToken without () should be a compile error
	result := analyze(t, `
model User @withAuth(stores: [id, role]) {
  name: String
  email: String
}
api test(): String {
  val user = User.create(name: "test", email: "a@b.com")
  val r = user.refreshToken
  r
}
`)
	expectError(t, result, "is a method, use refreshToken()")
}

func TestWithAuthIsMethodFlag(t *testing.T) {
	// verify that IsMethod flag is set on injected auth methods
	result := analyze(t, `
model User @withAuth(stores: [id, role]) {
  name: String
  email: String
}
`)
	expectNoErrors(t, result)

	userType := result.Types["User"]
	if userType == nil {
		t.Fatal("User type not found")
	}
	for _, methodName := range []string{"createToken", "verify", "refreshToken"} {
		f, ok := userType.Fields[methodName]
		if !ok {
			t.Errorf("%s field not injected", methodName)
			continue
		}
		if !f.IsMethod {
			t.Errorf("%s field should have IsMethod=true", methodName)
		}
	}
	// regular field should NOT have IsMethod
	if f, ok := userType.Fields["name"]; ok && f.IsMethod {
		t.Error("regular field 'name' should not have IsMethod=true")
	}
}

// ========== Function-style CRUD Return Type Tests ==========

func TestFunctionStyleCreateReturnType(t *testing.T) {
	// function-style create(User, ...) — exercises checkCreateRequiredFields and inferCRUDReturnType
	result := analyze(t, `
model User { name: String }
api test(): User {
  val user = create(User, name: "alice")
  user
}
`)
	expectNoErrors(t, result)
}

func TestFunctionStyleFindReturnType(t *testing.T) {
	// function-style find(User, id: 1) returns non-nullable (auto-throws NotFound)
	result := analyze(t, `
model User { name: String }
api test(): User {
  find(User, id: 1)
}
`)
	expectNoErrors(t, result)
}

func TestFunctionStyleFindWhereReturnType(t *testing.T) {
	// function-style find(User, where: ...) returns list
	result := analyze(t, `
model User { name: String }
api test(): [User] {
  find(User, where: name == "test")
}
`)
	expectNoErrors(t, result)
}

func TestFunctionStyleFindFirstReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  val user = findFirst(User, where: name == "test")
  0
}
`)
	expectNoErrors(t, result)
}

func TestFunctionStyleFindManyReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): [User] {
  findMany(User, where: name == "test")
}
`)
	expectNoErrors(t, result)
}

func TestFunctionStyleCreateManyReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): [User] {
  createMany(User, name: "a")
}
`)
	expectNoErrors(t, result)
}

func TestFunctionStyleUpdateReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): User {
  update(User, name: "b")
}
`)
	expectNoErrors(t, result)
}

func TestFunctionStyleUpsertReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): User {
  upsert(User, name: "c")
}
`)
	expectNoErrors(t, result)
}

func TestFunctionStyleDeleteReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): User {
  delete(User, id: 1)
}
`)
	expectNoErrors(t, result)
}

func TestFunctionStyleDeleteManyReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  deleteMany(User, where: name == "old")
}
`)
	expectNoErrors(t, result)
}

func TestFunctionStyleUpdateManyReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  updateMany(User, where: name == "a")
}
`)
	expectNoErrors(t, result)
}

func TestFunctionStyleCountReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  count(User, where: name == "a")
}
`)
	expectNoErrors(t, result)
}

func TestFunctionStyleExistsReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Boolean {
  exists(User, where: name == "a")
}
`)
	expectNoErrors(t, result)
}

func TestFunctionStyleAggregateReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  val r = aggregate(User, count: true)
  0
}
`)
	expectNoErrors(t, result)
}

func TestFunctionStyleCreateRequiredFieldsMissing(t *testing.T) {
	// function-style create(User, ...) without required field should warn
	result := analyze(t, `
model Product {
  name: String
  price: Float
}
api test(): Product {
  create(Product, name: "item")
}
`)
	expectWarning(t, result, "missing required field 'price'")
}

func TestFunctionStyleCreateWithNonIdentFirstArg(t *testing.T) {
	// function-style create with non-Ident first arg — exercises early return in checkCreateRequiredFields
	result := analyze(t, `
api test(): Int {
  create(1 + 2, name: "x")
  0
}
`)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestFunctionStyleCreateWithUnknownModel(t *testing.T) {
	// function-style create with unknown model — exercises modelType not found path
	result := analyze(t, `
api test(): Int {
  create(UnknownModel, name: "x")
  0
}
`)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestFunctionStyleCreateModelNilFields(t *testing.T) {
	// model with nil Fields map — exercises early return in checkCreateRequiredFields
	a := New()
	a.types["EmptyModel"] = &ResolvedType{Kind: TypeModel, Name: "EmptyModel", Fields: nil}
	a.scope.Define(&Symbol{Name: "EmptyModel", Kind: SymType, Type: a.types["EmptyModel"]})
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Int"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.CallExpr{
								Func: &ast.Ident{Name: "create"},
								Args: []*ast.NamedArg{
									{Value: &ast.Ident{Name: "EmptyModel"}},
									{Name: "name", Value: &ast.Literal{Kind: token.String, Value: "x"}},
								},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestFunctionStyleCreateEmptyArgs(t *testing.T) {
	// create() with no args — exercises the len(e.Args) == 0 early return
	a := New()
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Int"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.CallExpr{
								Func: &ast.Ident{Name: "create"},
								Args: []*ast.NamedArg{},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// ========== @scope Directive Validation Tests ==========

func TestScopeDirectiveOnNonModelReturn(t *testing.T) {
	// @scope on API returning a custom type (not a model) — should warn
	result := analyze(t, `
type MyType { value: String }
api test(): MyType @scope(active) {
  MyType { value: "test" }
}
`)
	expectWarning(t, result, "@scope can only be used on APIs returning a model type")
}

func TestScopeDirectiveNoReturnType(t *testing.T) {
	// @scope on API with no return type — exercises baseTypeName == "" path
	a := New()
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name: "test",
				// no ReturnType
				Directives: []*ast.Directive{
					{Name: "scope", Args: []*ast.NamedArg{
						{Value: &ast.Ident{Name: "active"}},
					}},
				},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{Expr: &ast.Literal{Kind: token.Int, Value: "42"}},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	expectWarning(t, result, "@scope can only be used on APIs returning a model type")
}

func TestScopeDirectiveScopeNotFoundWithSuggestion(t *testing.T) {
	// @scope referencing a non-existent scope with close match — exercises suggestion path
	a := New()
	file := &ast.File{
		Models: []*ast.ModelDecl{
			{
				Name:   "User",
				Fields: []*ast.FieldDecl{{Name: "name", Type: &ast.TypeRef{Name: "String"}}},
				Scopes: []*ast.ScopeDecl{{Name: "active"}},
			},
		},
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "User", IsList: true},
				Directives: []*ast.Directive{
					{Name: "scope", Args: []*ast.NamedArg{
						{Value: &ast.Ident{Name: "actve"}}, // typo
					}},
				},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{Expr: &ast.Literal{Kind: token.Int, Value: "0"}},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	expectError(t, result, "did you mean 'active'")
}

func TestScopeDirectiveScopeNotFoundNoSuggestion(t *testing.T) {
	// @scope referencing a non-existent scope with no close match
	a := New()
	file := &ast.File{
		Models: []*ast.ModelDecl{
			{
				Name:   "User",
				Fields: []*ast.FieldDecl{{Name: "name", Type: &ast.TypeRef{Name: "String"}}},
				Scopes: []*ast.ScopeDecl{{Name: "active"}},
			},
		},
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "User", IsList: true},
				Directives: []*ast.Directive{
					{Name: "scope", Args: []*ast.NamedArg{
						{Value: &ast.Ident{Name: "xyzzy"}}, // no close match
					}},
				},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{Expr: &ast.Literal{Kind: token.Int, Value: "0"}},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	expectError(t, result, "scope 'xyzzy' not found on model 'User'")
}

func TestScopeDirectiveUnknownReturnType(t *testing.T) {
	// @scope on API with unknown return type — type not found, already reported by resolveTypeRef
	result := analyze(t, `
api test(): UnknownType @scope(active) {
  42
}
`)
	// Should have error about UnknownType but no panic
	expectError(t, result, "unknown type 'UnknownType'")
}

func TestScopeDirectiveNonIdentArg(t *testing.T) {
	// @scope arg is not an Ident (e.g., a literal) — exercises scopeName == "" continue path
	a := New()
	file := &ast.File{
		Models: []*ast.ModelDecl{
			{
				Name:   "User",
				Fields: []*ast.FieldDecl{{Name: "name", Type: &ast.TypeRef{Name: "String"}}},
				Scopes: []*ast.ScopeDecl{{Name: "active"}},
			},
		},
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "User", IsList: true},
				Directives: []*ast.Directive{
					{Name: "scope", Args: []*ast.NamedArg{
						{Value: &ast.Literal{Kind: token.String, Value: "active"}}, // literal, not ident
					}},
				},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{Expr: &ast.Literal{Kind: token.Int, Value: "0"}},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	// scopeName is "" because arg.Value is a Literal, not an Ident — should skip without error
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// ========== Compound Assignment Atomic Tests ==========

func TestCompoundAssignOnModelMemberAtomic(t *testing.T) {
	// += on model member should mark Atomic
	result := analyze(t, `
model Product { stock: Int }
api test(): Int {
  var p = Product.create(stock: 10)
  p.stock += 5
  p.stock
}
`)
	expectNoErrors(t, result)
}

func TestCompoundAssignSubOnModelMemberAtomic(t *testing.T) {
	// -= on model member should mark Atomic
	result := analyze(t, `
model Product { stock: Int }
api test(): Int {
  var p = Product.create(stock: 10)
  p.stock -= 3
  p.stock
}
`)
	expectNoErrors(t, result)
}

// ========== Unused Variable with _ Prefix ==========

func TestUnusedVariableUnderscorePrefix(t *testing.T) {
	// _ prefixed variables should NOT produce unused warnings
	result := analyze(t, `
api test(): Int {
  val _unused = 42
  0
}
`)
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "_unused") {
			t.Errorf("should not warn about _ prefixed variable: %s", w.Message)
		}
	}
}

// ========== Enum Comparison with String ==========

func TestEnumCompareWithString(t *testing.T) {
	result := analyze(t, `
enum Role { USER ADMIN }
api test(): Boolean {
  val r = Role.USER
  r == "USER"
}
`)
	expectError(t, result, "cannot compare enum")
}

func TestStringCompareWithEnum(t *testing.T) {
	result := analyze(t, `
enum Role { USER ADMIN }
api test(): Boolean {
  val r = Role.USER
  "USER" == r
}
`)
	expectError(t, result, "cannot compare String with enum")
}

// ========== Nullable Arithmetic ==========

func TestNullableArithmeticLeft(t *testing.T) {
	result := analyze(t, `
model User { age: Int }
api test(): Int {
  val user = User.findFirst(where: age > 0)
  val x = user?.age + 1
  x
}
`)
	expectError(t, result, "cannot use '+' on nullable")
}

func TestNullableArithmeticRight(t *testing.T) {
	result := analyze(t, `
model User { age: Int }
api test(): Int {
  val user = User.findFirst(where: age > 0)
  val x = 1 + user?.age
  x
}
`)
	expectError(t, result, "cannot use '+' on nullable")
}

// ========== Sealed Variant Field Injection ==========

func TestSealedVariantFieldInjection(t *testing.T) {
	// when(r) { is Ok -> r.value } — exercises injectSealedVariantFields
	result := analyze(t, `
sealed PayResult {
  Success(transactionId: String)
  Failed(reason: String)
}
api test(r: PayResult): String {
  when(r) {
    is Success -> r.transactionId
    is Failed -> r.reason
  }
}
`)
	expectNoErrors(t, result)
}

func TestSealedVariantFieldInjectionNonIdentSubject(t *testing.T) {
	// when with non-Ident subject — should not crash; injectSealedVariantFields returns scope unchanged
	a := New()
	a.declareType("PayResult", TypeSealed, token.Position{}, "")
	payResult := a.types["PayResult"]
	payResult.Variants = []*SealedVariantInfo{
		{Name: "Ok", Fields: []*FieldInfo{{Name: "value", Type: a.types["String"]}}},
	}
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "String"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.WhenExpr{
								Subject: &ast.MemberExpr{Object: &ast.Ident{Name: "x"}, Field: "result"},
								Branches: []*ast.WhenBranch{
									{
										IsType: "Ok",
										Body:   &ast.Literal{Kind: token.String, Value: "ok"},
									},
								},
								Else: &ast.Literal{Kind: token.String, Value: "other"},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestInjectSealedVariantFieldsFoundWithFieldsDirect(t *testing.T) {
	// directly test injectSealedVariantFields when variant IS found and has fields
	a := New()
	sealedType := &ResolvedType{
		Kind: TypeSealed,
		Name: "Result",
		Variants: []*SealedVariantInfo{
			{Name: "Ok", Fields: []*FieldInfo{
				{Name: "value", Type: a.types["String"]},
				{Name: "code", Type: a.types["Int"]},
			}},
		},
	}
	scope := NewScope()
	subject := &ast.Ident{Name: "r"}

	result := a.injectSealedVariantFields(scope, sealedType, "Ok", subject)
	if result == scope {
		t.Error("expected new child scope when variant found")
	}
	// check that variant fields are accessible
	sym := result.Lookup("r")
	if sym == nil {
		t.Fatal("expected 'r' symbol in narrowed scope")
	}
	if sym.Type == nil || len(sym.Type.Fields) != 2 {
		t.Errorf("expected 2 fields in narrowed type, got %v", sym.Type)
	}
}

func TestInjectSealedVariantFieldsVariantNotFoundDirect(t *testing.T) {
	// directly test injectSealedVariantFields when variant is not found
	a := New()
	sealedType := &ResolvedType{
		Kind: TypeSealed,
		Name: "PayResult",
		Variants: []*SealedVariantInfo{
			{Name: "Ok", Fields: []*FieldInfo{{Name: "value", Type: a.types["String"]}}},
		},
	}
	scope := NewScope()
	subject := &ast.Ident{Name: "r"}

	// variant "NonExistent" doesn't exist — should return the original scope
	result := a.injectSealedVariantFields(scope, sealedType, "NonExistent", subject)
	if result != scope {
		t.Error("expected same scope when variant not found")
	}
}

func TestSealedVariantFieldInjectionVariantNotFound(t *testing.T) {
	// when(r) { is NonExistent -> ... } where NonExistent is not a variant of the sealed type
	// exercises the variant not found path (loop finishes without match, returns scope)
	a := New()
	a.declareType("PayResult", TypeSealed, token.Position{}, "")
	payResult := a.types["PayResult"]
	payResult.Variants = []*SealedVariantInfo{
		{Name: "Ok", Fields: []*FieldInfo{{Name: "value", Type: a.types["String"]}}},
		{Name: "Err", Fields: []*FieldInfo{{Name: "message", Type: a.types["String"]}}},
	}
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "String"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ValStmt{
							Name:  "r",
							Value: &ast.Ident{Name: "PayResult"},
						},
						&ast.ExprStmt{
							Expr: &ast.WhenExpr{
								Subject: &ast.Ident{Name: "r"},
								Branches: []*ast.WhenBranch{
									{
										IsType: "Ok",
										Body:   &ast.Literal{Kind: token.String, Value: "ok"},
									},
									{
										IsType: "NonExistent", // not a real variant
										Body:   &ast.Literal{Kind: token.String, Value: "?"},
									},
								},
								Else: &ast.Literal{Kind: token.String, Value: "other"},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// ========== Chain CRUD Return Type Edge Cases ==========

func TestChainCRUDFindFirstReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  val u = User.findFirst(where: name == "x")
  0
}
`)
	expectNoErrors(t, result)
}

func TestChainCRUDDeleteManyReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  User.deleteMany(where: name == "old")
}
`)
	expectNoErrors(t, result)
}

func TestChainCRUDUpdateManyReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  User.updateMany(where: name == "old")
}
`)
	expectNoErrors(t, result)
}

func TestChainCRUDCountReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  User.count(where: name == "x")
}
`)
	expectNoErrors(t, result)
}

func TestChainCRUDExistsReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Boolean {
  User.exists(where: name == "x")
}
`)
	expectNoErrors(t, result)
}

func TestChainCRUDUpsertReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): User {
  User.upsert(where: name == "x", name: "y")
}
`)
	expectNoErrors(t, result)
}

func TestChainCRUDCreateManyReturnType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): [User] {
  User.createMany(name: "a")
}
`)
	expectNoErrors(t, result)
}

func TestChainCRUDAggregateReturnType(t *testing.T) {
	// chain-style aggregate — exercises the isDynamicReturnCRUD path in inferChainCRUDReturnType
	a := New()
	a.declareType("User", TypeModel, token.Position{}, "")
	userType := a.types["User"]
	userType.Fields["name"] = &FieldInfo{Name: "name", Type: a.types["String"]}

	// call inferChainCRUDReturnType directly
	e := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "aggregate"},
		Args: []*ast.NamedArg{{Name: "count", Value: &ast.Literal{Kind: token.True, Value: "true"}}},
	}
	result := a.inferChainCRUDReturnType(e, "aggregate", "User")
	if result != nil {
		t.Errorf("expected nil for aggregate, got %v", result)
	}
}

// ========== isAutoManagedField Edge Cases ==========

func TestCreateMissingFieldWithIdDirective(t *testing.T) {
	// field with @id should be auto-managed — no warning
	result := analyze(t, `
model Product {
  sku: Int @id
  name: String
}
api test(): Product {
  Product.create(name: "item")
}
`)
	// sku has @id, so should not warn about it
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "sku") {
			t.Errorf("should not warn about @id field: %s", w.Message)
		}
	}
}

func TestCreateMissingFieldComputedField(t *testing.T) {
	// computed fields should be auto-managed — no warning
	result := analyze(t, `
model Post {
  title: String
  val totalCount: Int get @count
}
api test(): Post {
  Post.create(title: "hello")
}
`)
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "totalCount") {
			t.Errorf("should not warn about computed field: %s", w.Message)
		}
	}
}

// ========== Chain Create Required Fields — modelType not found ==========

func TestChainCreateRequiredFieldsUnknownModel(t *testing.T) {
	// chain-style create on undefined model — exercises the !ok early return
	result := analyze(t, `
api test(): Int {
  val r = UnknownModel.create(name: "x")
  0
}
`)
	expectError(t, result, "undefined")
}

// ========== hasDirective found path ==========

func TestHasDirectiveFound(t *testing.T) {
	found := hasDirective([]string{"auto", "id", "unique"}, "id")
	if !found {
		t.Error("expected hasDirective to return true for 'id'")
	}
}

func TestHasDirectiveNotFound(t *testing.T) {
	found := hasDirective([]string{"auto", "unique"}, "id")
	if found {
		t.Error("expected hasDirective to return false for 'id'")
	}
}

// ========== isCRUDCall and isCRUDIdent member expr path ==========

func TestIsCRUDCallMemberExpr(t *testing.T) {
	call := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "User"},
			Field:  "find",
		},
	}
	if !isCRUDCall(call) {
		t.Error("expected isCRUDCall to return true for User.find(...)")
	}
}

func TestIsCRUDCallNonCRUD(t *testing.T) {
	call := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "User"},
			Field:  "toString",
		},
	}
	if isCRUDCall(call) {
		t.Error("expected isCRUDCall to return false for User.toString(...)")
	}
}

func TestIsCRUDCallIdentNonCRUD(t *testing.T) {
	// ident func with non-CRUD name — exercises ident branch falling through
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "myFunc"},
	}
	if isCRUDCall(call) {
		t.Error("expected isCRUDCall to return false for non-CRUD ident")
	}
}

func TestIsCRUDIdentMemberExpr(t *testing.T) {
	call := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "User"},
			Field:  "create",
		},
	}
	if !isCRUDIdent(call) {
		t.Error("expected isCRUDIdent to return true for User.create(...)")
	}
}

func TestIsCRUDIdentNonCRUD(t *testing.T) {
	call := &ast.CallExpr{
		Func: &ast.Literal{Kind: token.Int, Value: "42"},
	}
	if isCRUDIdent(call) {
		t.Error("expected isCRUDIdent to return false for non-ident func")
	}
	if isCRUDCall(call) {
		t.Error("expected isCRUDCall to return false for non-ident func")
	}
}

// ========== chainCRUDInfo edge cases ==========

func TestChainCRUDInfoNonMember(t *testing.T) {
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "find"},
	}
	method, model := chainCRUDInfo(call)
	if method != "" || model != "" {
		t.Errorf("expected empty for non-member func, got %q, %q", method, model)
	}
}

func TestChainCRUDInfoNonCRUDMethod(t *testing.T) {
	call := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "User"},
			Field:  "toString",
		},
	}
	method, _ := chainCRUDInfo(call)
	if method != "" {
		t.Errorf("expected empty for non-CRUD method, got %q", method)
	}
}

func TestChainCRUDInfoNonIdentObject(t *testing.T) {
	// when object is not an Ident, method is returned but modelName is empty
	call := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.MemberExpr{
				Object: &ast.Ident{Name: "x"},
				Field:  "y",
			},
			Field: "find",
		},
	}
	method, modelName := chainCRUDInfo(call)
	if method != "find" {
		t.Errorf("expected method 'find' for non-ident object, got %q", method)
	}
	if modelName != "" {
		t.Errorf("expected empty modelName for non-ident object, got %q", modelName)
	}
}

// ========== collectEnumValues nil expr ==========

func TestCollectEnumValuesNilExprDirect(t *testing.T) {
	a := New()
	matched := map[string]bool{}
	a.collectEnumValues(nil, matched)
	if len(matched) != 0 {
		t.Errorf("expected empty map after nil expr, got %v", matched)
	}
}

// ========== resolveQueryMethod CRUD chain methods ==========

func TestResolveQueryMethodCRUDChain(t *testing.T) {
	a := New()
	a.declareType("Order", TypeModel, token.Position{}, "")
	orderType := a.types["Order"]
	orderType.Fields["total"] = &FieldInfo{Name: "total", Type: a.types["Float"]}

	tests := []struct {
		method   string
		wantNil  bool
		wantKind TypeKind
		wantList bool
		wantNull bool
	}{
		{"find", true, 0, false, false}, // find returns nil (resolved in checkCallExpr)
		{"findFirst", false, TypeModel, false, true},
		{"findMany", false, TypeModel, true, false},
		{"createMany", false, TypeModel, true, false},
		{"create", false, TypeModel, false, false},
		{"update", false, TypeModel, false, false},
		{"upsert", false, TypeModel, false, false},
		{"delete", false, TypeModel, false, false},
		{"deleteMany", false, TypeInt, false, false},
		{"updateMany", false, TypeInt, false, false},
		{"exists", false, TypeBool, false, false},
		{"aggregate", true, 0, false, false},
		{"raw", true, 0, false, false},
		{"paginate", true, 0, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			got := a.resolveQueryMethod(tt.method, orderType)
			if tt.wantNil {
				if got != nil {
					t.Errorf("resolveQueryMethod(%q) = %v, want nil", tt.method, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("resolveQueryMethod(%q) returned nil", tt.method)
			}
			if got.Kind != tt.wantKind {
				t.Errorf("resolveQueryMethod(%q).Kind = %v, want %v", tt.method, got.Kind, tt.wantKind)
			}
			if got.IsList != tt.wantList {
				t.Errorf("resolveQueryMethod(%q).IsList = %v, want %v", tt.method, got.IsList, tt.wantList)
			}
			if got.Nullable != tt.wantNull {
				t.Errorf("resolveQueryMethod(%q).Nullable = %v, want %v", tt.method, got.Nullable, tt.wantNull)
			}
		})
	}
}

// ========== N+1 Detection — chain-style in for loop ==========

func TestN1ChainStyleInForLoop(t *testing.T) {
	result := analyze(t, `
model User { name: String }
model Post { userId: Int  title: String }
api test(users: [User]): Int {
  var count = 0
  for user in users {
    val posts = Post.findMany(where: userId == 1)
    count += posts.size
  }
  count
}
`)
	expectWarning(t, result, "N+1 query")
}

func TestN1DetectionExprStmt(t *testing.T) {
	// CRUD as ExprStmt (not ValStmt) in for loop
	result := analyze(t, `
model User { name: String }
api test(ids: [Int]): Int {
  for id in ids {
    User.find(id: id)
  }
  0
}
`)
	expectWarning(t, result, "N+1 query")
}

// ========== CRUD in lambda — chain-style forbidden ==========

func TestChainStyleCRUDInLambdaForbidden(t *testing.T) {
	result := analyze(t, `
model User { name: String }
model Post { userId: Int }
api test(): Int {
  val users = User.find(where: name == "test")
  users.forEach { Post.findMany(where: userId == 1) }
  0
}
`)
	expectError(t, result, "lambda")
}

// ========== hasSearchDirective — directives loop iterates but no search ==========

func TestHasSearchDirectiveNonSearchDirective(t *testing.T) {
	// field has directives but none is @search — exercises the loop without finding "search"
	result := analyze(t, `
model User {
  email: String @unique @index
}
api test(keyword: String): Boolean {
  val u = User.find(id: 1)
  u?.email?.contains(keyword) ?: false
}
`)
	expectWarning(t, result, "contains")
}

func TestHasSearchDirectiveFieldWithSearchDirectAccess(t *testing.T) {
	// exercise the return true path in hasSearchDirective
	// by pre-defining a variable in scope with a @search field
	a := New()
	postType := &ResolvedType{
		Kind: TypeModel,
		Name: "Post",
		Fields: map[string]*FieldInfo{
			"title": {Name: "title", Type: a.types["String"], Directives: []string{"search"}},
		},
	}
	a.types["Post"] = postType
	a.scope.Define(&Symbol{Name: "Post", Kind: SymType, Type: postType})
	// define "post" variable in the global scope so hasSearchDirective can find it
	a.scope.Define(&Symbol{Name: "post", Kind: SymVariable, Type: postType})

	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Boolean"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						// post.title.contains("keyword")
						&ast.ExprStmt{
							Expr: &ast.CallExpr{
								Func: &ast.MemberExpr{
									Object: &ast.MemberExpr{
										Object: &ast.Ident{Name: "post"},
										Field:  "title",
									},
									Field: "contains",
								},
								Args: []*ast.NamedArg{
									{Value: &ast.Literal{Kind: token.String, Value: "keyword"}},
								},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	// post.title has @search, so no "contains" warning should appear
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "contains") {
			t.Errorf("should not warn about contains on @search field: %s", w.Message)
		}
	}
}

func TestHasSearchDirectiveEmptyDirectives(t *testing.T) {
	// field has NO directives at all — exercises the empty loop path
	a := New()
	postType := &ResolvedType{
		Kind: TypeModel,
		Name: "Post",
		Fields: map[string]*FieldInfo{
			"title": {Name: "title", Type: a.types["String"], Directives: nil},
		},
	}
	a.scope.Define(&Symbol{Name: "post", Kind: SymVariable, Type: postType})

	expr := &ast.MemberExpr{Object: &ast.Ident{Name: "post"}, Field: "title"}
	got := a.hasSearchDirective(expr)
	if got {
		t.Error("expected false for field with no directives")
	}
}

func TestHasSearchDirectiveDirectLoopNoSearch(t *testing.T) {
	// directly test hasSearchDirective where field has directives but none is "search"
	a := New()
	postType := &ResolvedType{
		Kind: TypeModel,
		Name: "Post",
		Fields: map[string]*FieldInfo{
			"title": {Name: "title", Type: a.types["String"], Directives: []string{"unique", "index"}},
		},
	}
	a.scope.Define(&Symbol{Name: "post", Kind: SymVariable, Type: postType})

	// hasSearchDirective(post.title) — should return false because no "search" directive
	expr := &ast.MemberExpr{Object: &ast.Ident{Name: "post"}, Field: "title"}
	got := a.hasSearchDirective(expr)
	if got {
		t.Error("expected false for field without @search")
	}
}

func TestHasSearchDirectiveDirectLoopWithSearch(t *testing.T) {
	// directly test hasSearchDirective where field has @search directive
	a := New()
	postType := &ResolvedType{
		Kind: TypeModel,
		Name: "Post",
		Fields: map[string]*FieldInfo{
			"title": {Name: "title", Type: a.types["String"], Directives: []string{"search"}},
		},
	}
	a.scope.Define(&Symbol{Name: "post", Kind: SymVariable, Type: postType})

	expr := &ast.MemberExpr{Object: &ast.Ident{Name: "post"}, Field: "title"}
	got := a.hasSearchDirective(expr)
	if !got {
		t.Error("expected true for field with @search")
	}
}

func TestHasSearchDirectiveFieldWithNonSearchDirective(t *testing.T) {
	// field has directives but no @search — exercises the loop falling through to return false
	a := New()
	postType := &ResolvedType{
		Kind: TypeModel,
		Name: "Post",
		Fields: map[string]*FieldInfo{
			"title": {Name: "title", Type: a.types["String"], Directives: []string{"unique", "index"}},
		},
	}
	a.types["Post"] = postType
	a.scope.Define(&Symbol{Name: "Post", Kind: SymType, Type: postType})
	a.scope.Define(&Symbol{Name: "post", Kind: SymVariable, Type: postType})

	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Boolean"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.CallExpr{
								Func: &ast.MemberExpr{
									Object: &ast.MemberExpr{
										Object: &ast.Ident{Name: "post"},
										Field:  "title",
									},
									Field: "contains",
								},
								Args: []*ast.NamedArg{
									{Value: &ast.Literal{Kind: token.String, Value: "keyword"}},
								},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	expectWarning(t, result, "contains")
}

// ========== Transaction with explicit non-lambda arg ==========

func TestTransactionCallWithNonLambdaArgDirect(t *testing.T) {
	// transaction called with a non-LambdaExpr argument — exercises the else branch in checkTransactionCall
	a := New()
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Int"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.CallExpr{
								Func: &ast.Ident{Name: "transaction"},
								Args: []*ast.NamedArg{
									{Value: &ast.Literal{Kind: token.Int, Value: "42"}},
								},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// ========== ForStmt as expression in checkCompositeExpr ==========

func TestForStmtAsExpression(t *testing.T) {
	// for as expression — exercises the ForStmt case in checkCompositeExpr
	a := New()
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Int"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ValStmt{
							Name: "result",
							Value: &ast.ForStmt{
								VarName:    "i",
								Collection: &ast.ListExpr{Items: []ast.Expr{&ast.Literal{Kind: token.Int, Value: "1"}}},
								Body: &ast.Block{
									Stmts: []ast.Stmt{
										&ast.ExprStmt{Expr: &ast.Ident{Name: "i"}},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// ========== isAggregateFieldRefCall Tests ==========

func TestIsAggregateFieldRefCallIdent(t *testing.T) {
	call := &ast.CallExpr{Func: &ast.Ident{Name: "aggregate"}}
	if !isAggregateFieldRefCall(call) {
		t.Error("expected true for aggregate ident")
	}
	call2 := &ast.CallExpr{Func: &ast.Ident{Name: "groupBy"}}
	if !isAggregateFieldRefCall(call2) {
		t.Error("expected true for groupBy ident")
	}
	call3 := &ast.CallExpr{Func: &ast.Ident{Name: "find"}}
	if isAggregateFieldRefCall(call3) {
		t.Error("expected false for find ident")
	}
}

func TestIsAggregateFieldRefCallMember(t *testing.T) {
	call := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "aggregate"},
	}
	if !isAggregateFieldRefCall(call) {
		t.Error("expected true for User.aggregate member")
	}
	call2 := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "find"},
	}
	if isAggregateFieldRefCall(call2) {
		t.Error("expected false for User.find member")
	}
}

func TestIsAggregateFieldRefCallNonIdentNonMember(t *testing.T) {
	call := &ast.CallExpr{Func: &ast.Literal{Kind: token.Int, Value: "1"}}
	if isAggregateFieldRefCall(call) {
		t.Error("expected false for literal func")
	}
}

// ========== isQueryModifierMethod Tests ==========

func TestQueryModifierMethodOrderBy(t *testing.T) {
	// Model.orderBy(field.desc) — exercises isQueryModifierMethod returning true
	result := analyze(t, `
model Post {
  title: String
  likes: Int
}
api test(): [Post] {
  Post.orderBy(likes.desc).all
}
`)
	expectNoErrors(t, result)
}

func TestQueryModifierMethodGroupBy(t *testing.T) {
	// Model.groupBy(field) — exercises isQueryModifierMethod returning true
	result := analyze(t, `
model Post {
  title: String
  status: String
}
api test(): Int {
  val grouped = Post.groupBy(status)
  0
}
`)
	expectNoErrors(t, result)
}

// ========== isAutoManagedField — IsMethod field ==========

func TestIsAutoManagedFieldIsMethod(t *testing.T) {
	field := &FieldInfo{Name: "verify", IsMethod: true}
	if !isAutoManagedField(field) {
		t.Error("expected IsMethod field to be auto-managed")
	}
}

func TestIsAutoManagedFieldHasDefault(t *testing.T) {
	field := &FieldInfo{Name: "status", HasDefault: true}
	if !isAutoManagedField(field) {
		t.Error("expected HasDefault field to be auto-managed")
	}
}

func TestIsAutoManagedFieldDeletedAt(t *testing.T) {
	field := &FieldInfo{Name: "deletedAt"}
	if !isAutoManagedField(field) {
		t.Error("expected deletedAt field to be auto-managed")
	}
}

func TestIsAutoManagedFieldWithAutoDirective(t *testing.T) {
	field := &FieldInfo{Name: "seq", Directives: []string{"auto"}}
	if !isAutoManagedField(field) {
		t.Error("expected @auto field to be auto-managed")
	}
}

func TestIsAutoManagedFieldWithIdDirective(t *testing.T) {
	field := &FieldInfo{Name: "pk", Directives: []string{"id"}}
	if !isAutoManagedField(field) {
		t.Error("expected @id field to be auto-managed")
	}
}

func TestIsAutoManagedFieldRegularRequired(t *testing.T) {
	field := &FieldInfo{Name: "title"}
	if isAutoManagedField(field) {
		t.Error("expected regular non-nullable field to NOT be auto-managed")
	}
}

// ========== resolveModelFromExpr — QueryBuilder variable ==========

func TestResolveModelFromExprQueryBuilder(t *testing.T) {
	// directly test resolveModelFromExpr with a QueryBuilder type variable
	a := New()
	a.declareType("Order", TypeModel, token.Position{}, "")
	orderType := a.types["Order"]
	orderType.Fields["total"] = &FieldInfo{Name: "total", Type: a.types["Float"]}

	qbType := &ResolvedType{
		Kind:      TypeQueryBuilder,
		Name:      "OrderQueryBuilder",
		Fields:    orderType.Fields,
		ModelType: orderType,
	}
	scope := NewScope()
	scope.Define(&Symbol{Name: "qb", Kind: SymVariable, Type: qbType})

	result := a.resolveModelFromExpr(&ast.Ident{Name: "qb"}, scope)
	if result == nil {
		t.Fatal("expected non-nil result for QueryBuilder variable")
	}
	if result.Kind != TypeModel {
		t.Errorf("expected TypeModel, got %v", result.Kind)
	}
	if result.Name != "Order" {
		t.Errorf("expected 'Order', got %q", result.Name)
	}
}

// ========== resolveModelFromExpr — chained CallExpr (direct) ==========

func TestResolveModelFromExprCallExprDirect(t *testing.T) {
	// directly test resolveModelFromExpr with a CallExpr whose Func is a MemberExpr
	a := New()
	a.declareType("Order", TypeModel, token.Position{}, "")
	scope := NewScope()

	// CallExpr(MemberExpr(Ident("Order"), "where"), args)
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "Order"},
			Field:  "where",
		},
		Args: []*ast.NamedArg{},
	}
	result := a.resolveModelFromExpr(expr, scope)
	if result == nil {
		t.Fatal("expected non-nil result for chained CallExpr")
	}
	if result.Name != "Order" {
		t.Errorf("expected 'Order', got %q", result.Name)
	}
}

func TestResolveModelFromExprChainedCall(t *testing.T) {
	// Order.where(...).sum(total) — exercises the CallExpr path in resolveModelFromExpr
	a := New()
	a.declareType("Order", TypeModel, token.Position{}, "")
	orderType := a.types["Order"]
	orderType.Fields["total"] = &FieldInfo{Name: "total", Type: a.types["Float"]}

	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Float"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						// Order.where(true).sum(total) — chained call
						&ast.ExprStmt{
							Expr: &ast.CallExpr{
								Func: &ast.MemberExpr{
									Object: &ast.CallExpr{
										Func: &ast.MemberExpr{
											Object: &ast.Ident{Name: "Order"},
											Field:  "where",
										},
										Args: []*ast.NamedArg{
											{Value: &ast.Literal{Kind: token.True, Value: "true"}},
										},
									},
									Field: "sum",
								},
								Args: []*ast.NamedArg{
									{Value: &ast.Ident{Name: "total"}},
								},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// ========== isCRUDCall: ident with DB query op name ==========

func TestIsCRUDCallIdentWithDBQueryOp(t *testing.T) {
	// Exercises the ident branch returning true in isCRUDCall (line 1358-1360).
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "find"},
	}
	if !isCRUDCall(call) {
		t.Error("expected isCRUDCall to return true for ident 'find'")
	}
}

// ========== injectSealedVariantFields: non-ident subject ==========

func TestInjectSealedVariantFieldsNonIdentSubject(t *testing.T) {
	// Exercises the !ok early return when subject is not an *ast.Ident (line 1725).
	// Directly call injectSealedVariantFields with a non-ident subject.
	a := New()
	sealedType := &ResolvedType{
		Kind: TypeSealed,
		Name: "Result",
		Variants: []*SealedVariantInfo{
			{Name: "Ok", Fields: []*FieldInfo{{Name: "value"}}},
		},
	}
	// MemberExpr is not an *ast.Ident — should return scope unchanged
	subject := &ast.MemberExpr{Object: &ast.Ident{Name: "x"}, Field: "val"}
	scope := NewScope()
	got := a.injectSealedVariantFields(scope, sealedType, "Ok", subject)
	if got != scope {
		t.Error("expected original scope to be returned for non-ident subject")
	}
}

// ========== hasSearchDirective: field is nil ==========

func TestHasSearchDirectiveFieldNilDirect(t *testing.T) {
	// Exercises the field == nil return path in hasSearchDirective (line 2016).
	// Directly call hasSearchDirective with a MemberExpr whose object type
	// resolves but the field does not exist in the resolved type.
	a := New()
	a.types = BuiltinTypes()
	a.scope = NewScope()
	// Define a variable "obj" with type that has fields
	objType := &ResolvedType{
		Kind:   TypeModel,
		Name:   "Obj",
		Fields: map[string]*FieldInfo{"real": {Name: "real"}},
	}
	a.scope.Define(&Symbol{Name: "obj", Kind: SymVariable, Type: objType})

	// MemberExpr "obj.ghost" — obj resolves, "ghost" does not exist → field == nil
	expr := &ast.MemberExpr{Object: &ast.Ident{Name: "obj"}, Field: "ghost"}
	if a.hasSearchDirective(expr) {
		t.Error("expected false for non-existent field")
	}
}

// ========== checkLambdaExpr: named lambda params ==========

func TestNamedLambdaParams(t *testing.T) {
	// Exercises the named params branch in checkLambdaExpr (line 1631-1635).
	// Lambda with explicit named params: { x -> x + 1 }
	result := analyze(t, `
model Item { value: Int }
api test(): [Item] {
  val items = Item.find(where: value > 0)
  items.filter { x -> true }
}
`)
	expectNoErrors(t, result)
}

func TestNamedLambdaMultipleParams(t *testing.T) {
	// Lambda with multiple named params: { a, b -> a + b }
	result := analyze(t, `
model Item { value: Int }
api test(): Int {
  val items = Item.find(where: value > 0)
  items.forEach { x -> x }
  0
}
`)
	expectNoErrors(t, result)
}

func TestDateTimeDurationArithmetic(t *testing.T) {
	result := analyze(t, `
model Log @crud {
  id: Int @id @auto @serial
  timestamp: DateTime
  retentionDays: Int
}
api test(logId: Int): Boolean {
  val log = Log.find(id: logId)
  log ?: throw error.NotFound
  val cutoff = now() - log.retentionDays.days
  val future = now() + log.retentionDays.hours
  return true
}
`)
	expectNoErrors(t, result)
}

func TestDurationDurationArithmetic(t *testing.T) {
	result := analyze(t, `
api test(a: Int, b: Int): Boolean {
  val total = a.hours + b.minutes
  return true
}
`)
	expectNoErrors(t, result)
}

func TestStreamItInjected(t *testing.T) {
	result := analyze(t, `
event TraceIngested(traceId: String, projectId: Int)

api liveTraces(projectId: Int): String @stream(TraceIngested) {
  it.projectId == projectId
}
`)
	expectNoErrors(t, result)
}

func TestStreamItFieldAccess(t *testing.T) {
	result := analyze(t, `
event AlertFired(alert: String, projectId: Int)

api liveAlerts(projectId: Int): String @stream(AlertFired) {
  it.projectId == projectId
}
`)
	expectNoErrors(t, result)
}

func TestStreamWithoutEventRequiresNative(t *testing.T) {
	result := analyze(t, `
api watch(): Int @stream {
  true
}
`)
	expectError(t, result, "@stream without an event source requires @native")
}

func TestStreamContractValidation(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "missing return type",
			input: `event Changed(value: Int)\napi watch @stream(Changed)`,
			want:  "@stream API must declare a return type",
		},
		{
			name:  "event argument must be identifier",
			input: `event Changed(value: Int)\napi watch: Int @stream("Changed")`,
			want:  "@stream event source must be an event identifier",
		},
		{
			name:  "unknown event",
			input: `api watch: Int @stream(Missing)`,
			want:  "@stream event 'Missing' does not exist",
		},
		{
			name:  "missing payload",
			input: `event Changed(value: String)\napi watch: Int @stream(Changed)`,
			want:  "must contain exactly one payload parameter of type 'Int'",
		},
		{
			name:  "ambiguous payload",
			input: `event Changed(before: Int, after: Int)\napi watch: Int @stream(Changed)`,
			want:  "must contain exactly one payload parameter of type 'Int'",
		},
		{
			name:  "nullable payload mismatch",
			input: `event Changed(value: Int?)\napi watch: Int @stream(Changed)`,
			want:  "must contain exactly one payload parameter of type 'Int'",
		},
		{
			name:  "nullable return",
			input: `event Changed(value: Int?)\napi watch: Int? @stream(Changed)`,
			want:  "@stream return type must be non-nullable",
		},
		{
			name:  "native body",
			input: `event Changed(value: Int)\napi watch: Int @stream(Changed) @native { true }`,
			want:  "@native @stream API cannot declare a Luxo matcher body",
		},
		{
			name:  "multiple matcher statements",
			input: `event Changed(value: String, id: Int)\napi watch(id: Int): String @stream(Changed) { true\nit.id == id }`,
			want:  "@stream matcher body must contain exactly one boolean expression",
		},
		{
			name:  "non boolean matcher",
			input: `event Changed(value: String, id: Int)\napi watch(id: Int): String @stream(Changed) { it.id }`,
			want:  "@stream matcher expression must be Boolean",
		},
		{
			name:  "cache conflict",
			input: `event Changed(value: Int)\napi watch: Int @stream(Changed) @cache(10)`,
			want:  "@stream cannot be combined with @cache",
		},
		{
			name:  "paginate conflict",
			input: `event Changed(value: Int)\napi watch: Int @stream(Changed) @paginate`,
			want:  "@stream cannot be combined with @paginate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := analyze(t, strings.ReplaceAll(tt.input, `\n`, "\n"))
			expectError(t, result, tt.want)
		})
	}
}

func TestStreamGeneratedMatcherRejectsUnsupportedFieldTypes(t *testing.T) {
	result := analyze(t, `
event Changed(value: String, metadata: JSON)
api watch(metadata: JSON): String @stream(Changed) {
  it.metadata == metadata
}
`)
	expectError(t, result, "cannot be used in a generated @stream matcher")
}

func TestEventWireTypesAreResolvedAndValidated(t *testing.T) {
	valid := analyze(t, `
type Payload { metadata: JSON }
event Changed(payload: Payload, metadata: JSON, ids: [UUID])
`)
	expectNoErrors(t, valid)

	duplicate := analyze(t, `event Changed(id: Int, id: Int)`)
	expectError(t, duplicate, "duplicate parameter 'id' in event 'Changed'")

	unsupported := analyze(t, `
interface Payload { id: Int }
event Changed(payload: Payload)
`)
	expectError(t, unsupported, "unsupported wire type 'Payload'")
}

func TestOnHandlerNamedParam(t *testing.T) {
	result := analyze(t, `
event ProjectDeleted(projectId: Int)

model Trace @crud {
  id: Int @id @auto @serial
  projectId: Int @filterable
}

on ProjectDeleted { ev ->
  Trace.where(it.projectId == ev.projectId).deleteMany()
}
`)
	expectNoErrors(t, result)
}

func TestOnHandlerImplicitIt(t *testing.T) {
	result := analyze(t, `
event UserCreated(name: String)

on UserCreated {
  val n = it.name
}
`)
	expectNoErrors(t, result)
}
