package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// ─── helpers ────────────────────────────────────────────────────────────────

func newCompiler(models map[string]*ast.ModelDecl) *compiler {
	if models == nil {
		models = map[string]*ast.ModelDecl{}
	}
	var b strings.Builder
	return &compiler{
		b:      &b,
		indent: "\t\t",
		models: models,
		api:    &ast.ApiDecl{Name: "test"},
	}
}

func compilerOut(c *compiler) string {
	return c.b.String()
}

func makeModels(names ...string) map[string]*ast.ModelDecl {
	m := make(map[string]*ast.ModelDecl, len(names))
	for _, n := range names {
		m[n] = testModel(n, nil, nil)
	}
	return m
}

// ─── compileExpr — Ident ────────────────────────────────────────────────────

func TestCompileExprIdent(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.Ident{Name: "userId"})
	if got != "userId" {
		t.Fatalf("want %q, got %q", "userId", got)
	}
}

// ─── compileExpr — Literal ──────────────────────────────────────────────────

func TestCompileLiteralString(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.Literal{Kind: token.String, Value: "hello"})
	if got != `"hello"` {
		t.Fatalf("want %q, got %q", `"hello"`, got)
	}
}

func TestCompileLiteralTrue(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.Literal{Kind: token.True, Value: "true"})
	if got != "true" {
		t.Fatalf("want 'true', got %q", got)
	}
}

func TestCompileLiteralFalse(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.Literal{Kind: token.False, Value: "false"})
	if got != "false" {
		t.Fatalf("want 'false', got %q", got)
	}
}

func TestCompileLiteralNull(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.Literal{Kind: token.Null, Value: "null"})
	if got != "nil" {
		t.Fatalf("want 'nil', got %q", got)
	}
}

func TestCompileLiteralInt(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.Literal{Kind: token.Int, Value: "42"})
	if got != "42" {
		t.Fatalf("want '42', got %q", got)
	}
}

func TestCompileLiteralFloat(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.Literal{Kind: token.Float, Value: "3.14"})
	if got != "3.14" {
		t.Fatalf("want '3.14', got %q", got)
	}
}

// ─── compileExpr — MemberExpr ───────────────────────────────────────────────

func TestCompileMemberNormalField(t *testing.T) {
	c := newCompiler(nil)
	expr := &ast.MemberExpr{
		Object: &ast.Ident{Name: "user"},
		Field:  "name",
	}
	got := c.compileExpr(expr)
	if got != "user.Name" {
		t.Fatalf("want 'user.Name', got %q", got)
	}
}

func TestCompileMemberErrorPackage(t *testing.T) {
	c := newCompiler(nil)
	expr := &ast.MemberExpr{
		Object: &ast.Ident{Name: "error"},
		Field:  "notFound",
	}
	got := c.compileExpr(expr)
	if got != "errors.NotFound" {
		t.Fatalf("want 'errors.NotFound', got %q", got)
	}
}

func TestCompileMemberNestedObject(t *testing.T) {
	c := newCompiler(nil)
	inner := &ast.MemberExpr{
		Object: &ast.Ident{Name: "user"},
		Field:  "address",
	}
	outer := &ast.MemberExpr{
		Object: inner,
		Field:  "city",
	}
	got := c.compileExpr(outer)
	if got != "user.Address.City" {
		t.Fatalf("want 'user.Address.City', got %q", got)
	}
}

// ─── compileExpr — BinaryExpr ───────────────────────────────────────────────

func TestCompileBinaryEqual(t *testing.T) {
	c := newCompiler(nil)
	expr := &ast.BinaryExpr{
		Left:  &ast.Ident{Name: "a"},
		Op:    "==",
		Right: &ast.Ident{Name: "b"},
	}
	got := c.compileExpr(expr)
	if got != "a == b" {
		t.Fatalf("want 'a == b', got %q", got)
	}
}

func TestCompileBinaryAnd(t *testing.T) {
	c := newCompiler(nil)
	expr := &ast.BinaryExpr{
		Left:  &ast.Ident{Name: "x"},
		Op:    "&&",
		Right: &ast.Literal{Kind: token.True, Value: "true"},
	}
	got := c.compileExpr(expr)
	if got != "x && true" {
		t.Fatalf("want 'x && true', got %q", got)
	}
}

// ─── compileExpr — UnaryExpr ────────────────────────────────────────────────

func TestCompileUnaryNot(t *testing.T) {
	c := newCompiler(nil)
	expr := &ast.UnaryExpr{Op: "!", Value: &ast.Ident{Name: "exists"}}
	got := c.compileExpr(expr)
	if got != "!exists" {
		t.Fatalf("want '!exists', got %q", got)
	}
}

func TestCompileUnaryNeg(t *testing.T) {
	c := newCompiler(nil)
	expr := &ast.UnaryExpr{Op: "-", Value: &ast.Literal{Kind: token.Int, Value: "1"}}
	got := c.compileExpr(expr)
	if got != "-1" {
		t.Fatalf("want '-1', got %q", got)
	}
}

func TestCompileUnaryThrowMemberError(t *testing.T) {
	c := newCompiler(nil)
	expr := &ast.UnaryExpr{
		Op: "throw",
		Value: &ast.MemberExpr{
			Object: &ast.Ident{Name: "error"},
			Field:  "notFound",
		},
	}
	got := c.compileExpr(expr)
	if got != "errors.NotFound" {
		t.Fatalf("want 'errors.NotFound', got %q", got)
	}
}

func TestCompileUnaryThrowCallExpr(t *testing.T) {
	c := newCompiler(nil)
	expr := &ast.UnaryExpr{
		Op: "throw",
		Value: &ast.CallExpr{
			Func: &ast.Ident{Name: "DuplicateEmail"},
			Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "email"}}},
		},
	}
	got := c.compileExpr(expr)
	if got != "NewDuplicateEmail(email)" {
		t.Fatalf("want 'NewDuplicateEmail(email)', got %q", got)
	}
}

func TestCompileUnaryThrowCallExprMultiArgs(t *testing.T) {
	c := newCompiler(nil)
	expr := &ast.UnaryExpr{
		Op: "throw",
		Value: &ast.CallExpr{
			Func: &ast.Ident{Name: "MyError"},
			Args: []*ast.NamedArg{
				{Value: &ast.Ident{Name: "a"}},
				{Value: &ast.Ident{Name: "b"}},
			},
		},
	}
	got := c.compileExpr(expr)
	if got != "NewMyError(a, b)" {
		t.Fatalf("want 'NewMyError(a, b)', got %q", got)
	}
}

func TestCompileUnaryThrowNonIdentFunc(t *testing.T) {
	// throw with a non-ident func (member expr) falls through to compileExpr
	c := newCompiler(nil)
	expr := &ast.UnaryExpr{
		Op: "throw",
		Value: &ast.CallExpr{
			Func: &ast.MemberExpr{
				Object: &ast.Ident{Name: "pkg"},
				Field:  "NewError",
			},
			Args: nil,
		},
	}
	got := c.compileExpr(expr)
	// Should be a generic call compilation
	if !strings.Contains(got, "pkg") {
		t.Fatalf("expected pkg in output, got %q", got)
	}
}

func TestCompileUnaryThrowMemberNonError(t *testing.T) {
	// throw with a member expr that is not error.X
	c := newCompiler(nil)
	expr := &ast.UnaryExpr{
		Op: "throw",
		Value: &ast.MemberExpr{
			Object: &ast.Ident{Name: "pkg"},
			Field:  "someErr",
		},
	}
	got := c.compileExpr(expr)
	if !strings.Contains(got, "pkg") {
		t.Fatalf("expected pkg in output, got %q", got)
	}
}

func TestCompileUnaryThrowIdent(t *testing.T) {
	// throw with a plain ident
	c := newCompiler(nil)
	expr := &ast.UnaryExpr{
		Op:    "throw",
		Value: &ast.Ident{Name: "someErr"},
	}
	got := c.compileExpr(expr)
	if got != "someErr" {
		t.Fatalf("want 'someErr', got %q", got)
	}
}

// ─── compileExpr — ElvisExpr (standalone) ───────────────────────────────────

func TestCompileElvisExprStandalone(t *testing.T) {
	c := newCompiler(nil)
	expr := &ast.ElvisExpr{
		Left:  &ast.Ident{Name: "x"},
		Right: &ast.Ident{Name: "y"},
	}
	got := c.compileExpr(expr)
	if !strings.Contains(got, "/* elvis */") {
		t.Fatalf("expected elvis comment in output, got %q", got)
	}
	if !strings.Contains(got, "x") {
		t.Fatalf("expected 'x' in output, got %q", got)
	}
}

// ─── compileExpr — unknown type ─────────────────────────────────────────────

func TestCompileExprUnknownType(t *testing.T) {
	c := newCompiler(nil)
	// Use a type not handled by the switch
	got := c.compileExpr(&ast.ListExpr{Items: nil})
	if !strings.Contains(got, "/* TODO:") {
		t.Fatalf("expected TODO fallback, got %q", got)
	}
}

// ─── compileExpr — CallExpr (generic) ───────────────────────────────────────

func TestCompileCallGeneric(t *testing.T) {
	c := newCompiler(nil)
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "encrypt"},
		Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "password"}}},
	}
	got := c.compileExpr(call)
	if got != "encrypt(password)" {
		t.Fatalf("want 'encrypt(password)', got %q", got)
	}
}

func TestCompileCallGenericNamedArg(t *testing.T) {
	c := newCompiler(nil)
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "fn"},
		Args: []*ast.NamedArg{{Name: "key", Value: &ast.Ident{Name: "val"}}},
	}
	got := c.compileExpr(call)
	// Go does not support named args — only value is emitted
	if got != "fn(val)" {
		t.Fatalf("want 'fn(val)', got %q", got)
	}
}

func TestCompileCallNoArgs(t *testing.T) {
	c := newCompiler(nil)
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "doSomething"},
		Args: nil,
	}
	got := c.compileExpr(call)
	if got != "doSomething()" {
		t.Fatalf("want 'doSomething()', got %q", got)
	}
}

// ─── flattenChain ────────────────────────────────────────────────────────────

func TestFlattenChainSingleIdent(t *testing.T) {
	chain := flattenChain(&ast.Ident{Name: "User"})
	if len(chain) != 1 {
		t.Fatalf("want 1 link, got %d", len(chain))
	}
	if ident, ok := chain[0].expr.(*ast.Ident); !ok || ident.Name != "User" {
		t.Fatalf("want Ident 'User' at root, got %+v", chain[0])
	}
}

func TestFlattenChainMethodCall(t *testing.T) {
	// User.first()
	call := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "User"},
			Field:  "first",
		},
		Args: nil,
	}
	chain := flattenChain(call)
	if len(chain) != 2 {
		t.Fatalf("want 2 links, got %d: %+v", len(chain), chain)
	}
	if ident, ok := chain[0].expr.(*ast.Ident); !ok || ident.Name != "User" {
		t.Fatalf("want root 'User', got %+v", chain[0])
	}
	if chain[1].method != "first" {
		t.Fatalf("want method 'first', got %q", chain[1].method)
	}
}

func TestFlattenChainNestedCalls(t *testing.T) {
	// User.where(...).first()
	whereCall := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "User"},
			Field:  "where",
		},
		Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "cond"}}},
	}
	firstCall := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: whereCall,
			Field:  "first",
		},
		Args: nil,
	}
	chain := flattenChain(firstCall)
	if len(chain) != 3 {
		t.Fatalf("want 3 links, got %d", len(chain))
	}
	if chain[1].method != "where" {
		t.Fatalf("want 'where', got %q", chain[1].method)
	}
	if chain[2].method != "first" {
		t.Fatalf("want 'first', got %q", chain[2].method)
	}
}

func TestFlattenChainDirectCall(t *testing.T) {
	// encrypt(password) — non-member Func
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "encrypt"},
		Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "pw"}}},
	}
	chain := flattenChain(call)
	// direct call: one link with expr set
	if len(chain) != 1 {
		t.Fatalf("want 1 link, got %d", len(chain))
	}
}

func TestFlattenChainMemberExprNoCall(t *testing.T) {
	// obj.field (not a call)
	me := &ast.MemberExpr{
		Object: &ast.Ident{Name: "obj"},
		Field:  "field",
	}
	chain := flattenChain(me)
	if len(chain) != 2 {
		t.Fatalf("want 2 links, got %d", len(chain))
	}
	if chain[1].method != "field" {
		t.Fatalf("want method 'field', got %q", chain[1].method)
	}
}

// ─── isModelQuery ────────────────────────────────────────────────────────────

func TestIsModelQueryTrue(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	terminals := []string{"first", "all", "create", "exec", "find", "exists"}
	for _, term := range terminals {
		call := &ast.CallExpr{
			Func: &ast.MemberExpr{
				Object: &ast.Ident{Name: "User"},
				Field:  term,
			},
		}
		if !c.isModelQuery(call) {
			t.Errorf("isModelQuery should be true for terminal %q", term)
		}
	}
}

func TestIsModelQueryFalseNonModel(t *testing.T) {
	c := newCompiler(nil)
	call := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "NotAModel"},
			Field:  "first",
		},
	}
	if c.isModelQuery(call) {
		t.Fatal("isModelQuery should be false for non-model")
	}
}

func TestIsModelQueryFalseNonTerminal(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	// where is not a terminal; only the chain's last link matters
	whereCall := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "User"},
			Field:  "where",
		},
	}
	if c.isModelQuery(whereCall) {
		t.Fatal("isModelQuery should be false when terminal is not a query finalizer")
	}
}

func TestIsModelQueryFalseSingleLink(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)
	// Just an Ident, no chain
	if c.isModelQuery(&ast.Ident{Name: "User"}) {
		t.Fatal("isModelQuery should be false for single ident")
	}
}

// ─── compileModelChain — find ────────────────────────────────────────────────

func TestCompileModelChainFind(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	call := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "User"},
			Field:  "find",
		},
		Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "id"}}},
	}
	got := c.compileExpr(call)
	if !strings.Contains(got, "app.User.Where(UserWhere.Id.Eq(id)).First(ctx)") {
		t.Fatalf("unexpected output: %q", got)
	}
}

// ─── compileModelChain — first ───────────────────────────────────────────────

func TestCompileModelChainFirst(t *testing.T) {
	models := makeModels("Post")
	c := newCompiler(models)

	whereCall := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "Post"},
			Field:  "where",
		},
		Args: []*ast.NamedArg{{
			Value: &ast.BinaryExpr{
				Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "id"},
				Op:    "==",
				Right: &ast.Ident{Name: "postId"},
			},
		}},
	}
	firstCall := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: whereCall, Field: "first"},
		Args: nil,
	}
	got := c.compileExpr(firstCall)
	if !strings.Contains(got, "app.Post.Where(PostWhere.Id.Eq(postId)).First(ctx)") {
		t.Fatalf("unexpected output: %q", got)
	}
}

// ─── compileModelChain — all ─────────────────────────────────────────────────

func TestCompileModelChainAll(t *testing.T) {
	models := makeModels("Post")
	c := newCompiler(models)

	whereCall := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "Post"},
			Field:  "where",
		},
		Args: []*ast.NamedArg{{
			Value: &ast.BinaryExpr{
				Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "status"},
				Op:    "==",
				Right: &ast.Ident{Name: "active"},
			},
		}},
	}
	allCall := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: whereCall, Field: "all"},
		Args: nil,
	}
	got := c.compileExpr(allCall)
	if !strings.Contains(got, ".All(ctx)") {
		t.Fatalf("expected .All(ctx) in output, got %q", got)
	}
}

// ─── compileModelChain — exists ──────────────────────────────────────────────

func TestCompileModelChainExists(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	whereCall := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
		Args: []*ast.NamedArg{{
			Value: &ast.BinaryExpr{
				Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "email"},
				Op:    "==",
				Right: &ast.Ident{Name: "email"},
			},
		}},
	}
	existsCall := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: whereCall, Field: "exists"},
		Args: nil,
	}
	got := c.compileExpr(existsCall)
	if !strings.Contains(got, ".Exists(ctx)") {
		t.Fatalf("expected .Exists(ctx) in output, got %q", got)
	}
	if !strings.Contains(got, "UserWhere.Email.Eq(email)") {
		t.Fatalf("expected UserWhere.Email.Eq(email) in output, got %q", got)
	}
}

// ─── compileModelChain — create ──────────────────────────────────────────────

func TestCompileModelChainCreate(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	createCall := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "create"},
		Args: []*ast.NamedArg{
			{Name: "email", Value: &ast.Ident{Name: "email"}},
			{Name: "name", Value: &ast.Ident{Name: "name"}},
		},
	}
	got := c.compileExpr(createCall)
	if !strings.Contains(got, "app.User.Create()") {
		t.Fatalf("expected Create() in output, got %q", got)
	}
	if !strings.Contains(got, ".SetEmail(email)") {
		t.Fatalf("expected .SetEmail(email) in output, got %q", got)
	}
	if !strings.Contains(got, ".SetName(name)") {
		t.Fatalf("expected .SetName(name) in output, got %q", got)
	}
	if !strings.Contains(got, ".Exec(ctx)") {
		t.Fatalf("expected auto .Exec(ctx) in output, got %q", got)
	}
}

// ─── compileModelChain — create + exec explicit ──────────────────────────────

func TestCompileModelChainCreateExec(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	createCall := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "create"},
		Args: []*ast.NamedArg{
			{Name: "email", Value: &ast.Ident{Name: "email"}},
		},
	}
	execCall := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: createCall, Field: "exec"},
		Args: nil,
	}
	got := c.compileExpr(execCall)
	if !strings.Contains(got, ".Exec(ctx)") {
		t.Fatalf("expected .Exec(ctx) in output, got %q", got)
	}
}

// ─── compileModelChain — select ──────────────────────────────────────────────

func TestCompileModelChainSelect(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	whereCall := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
		Args: []*ast.NamedArg{{
			Value: &ast.BinaryExpr{
				Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "id"},
				Op:    "==",
				Right: &ast.Ident{Name: "id"},
			},
		}},
	}
	selectCall := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: whereCall, Field: "select"},
		Args: nil,
	}
	got := c.compileExpr(selectCall)
	if !strings.Contains(got, ".Select(selection.SQLColumns(req.Select)...)") {
		t.Fatalf("expected select in output, got %q", got)
	}
}

// ─── compileModelChain — unknown method ──────────────────────────────────────

func TestCompileModelChainUnknownMethod(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	call := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "orderBy"},
		Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "createdAt"}}},
	}
	got := c.compileExpr(call)
	if !strings.Contains(got, ".OrderBy(") {
		t.Fatalf("expected .OrderBy( in output, got %q", got)
	}
	if !strings.Contains(got, "createdAt") {
		t.Fatalf("expected createdAt in output, got %q", got)
	}
}

// ─── compileWhereArg ─────────────────────────────────────────────────────────

func TestCompileWhereArgItField(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	bin := &ast.BinaryExpr{
		Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "email"},
		Op:    "==",
		Right: &ast.Ident{Name: "email"},
	}
	got := c.compileWhereArg("User", bin)
	if got != "UserWhere.Email.Eq(email)" {
		t.Fatalf("want 'UserWhere.Email.Eq(email)', got %q", got)
	}
}

func TestCompileWhereArgIdentField(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	bin := &ast.BinaryExpr{
		Left:  &ast.Ident{Name: "email"},
		Op:    "==",
		Right: &ast.Ident{Name: "email"},
	}
	got := c.compileWhereArg("User", bin)
	if got != "UserWhere.Email.Eq(email)" {
		t.Fatalf("want 'UserWhere.Email.Eq(email)', got %q", got)
	}
}

func TestCompileWhereArgNotEqual(t *testing.T) {
	c := newCompiler(nil)
	bin := &ast.BinaryExpr{
		Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "role"},
		Op:    "!=",
		Right: &ast.Ident{Name: "admin"},
	}
	got := c.compileWhereArg("User", bin)
	if !strings.Contains(got, "Neq") {
		t.Fatalf("want Neq in output, got %q", got)
	}
}

func TestCompileWhereArgGt(t *testing.T) {
	c := newCompiler(nil)
	bin := &ast.BinaryExpr{
		Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "age"},
		Op:    ">",
		Right: &ast.Literal{Kind: token.Int, Value: "18"},
	}
	got := c.compileWhereArg("User", bin)
	if !strings.Contains(got, "Gt") {
		t.Fatalf("want Gt in output, got %q", got)
	}
}

func TestCompileWhereArgGte(t *testing.T) {
	c := newCompiler(nil)
	bin := &ast.BinaryExpr{
		Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "age"},
		Op:    ">=",
		Right: &ast.Literal{Kind: token.Int, Value: "18"},
	}
	got := c.compileWhereArg("User", bin)
	if !strings.Contains(got, "Gte") {
		t.Fatalf("want Gte in output, got %q", got)
	}
}

func TestCompileWhereArgLt(t *testing.T) {
	c := newCompiler(nil)
	bin := &ast.BinaryExpr{
		Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "score"},
		Op:    "<",
		Right: &ast.Literal{Kind: token.Int, Value: "100"},
	}
	got := c.compileWhereArg("User", bin)
	if !strings.Contains(got, "Lt") {
		t.Fatalf("want Lt in output, got %q", got)
	}
}

func TestCompileWhereArgLte(t *testing.T) {
	c := newCompiler(nil)
	bin := &ast.BinaryExpr{
		Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "score"},
		Op:    "<=",
		Right: &ast.Literal{Kind: token.Int, Value: "100"},
	}
	got := c.compileWhereArg("User", bin)
	if !strings.Contains(got, "Lte") {
		t.Fatalf("want Lte in output, got %q", got)
	}
}

func TestCompileWhereArgUnknownOp(t *testing.T) {
	c := newCompiler(nil)
	bin := &ast.BinaryExpr{
		Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "x"},
		Op:    "in",
		Right: &ast.Ident{Name: "vals"},
	}
	got := c.compileWhereArg("User", bin)
	// Falls back to compileExpr of the whole binary expression
	if !strings.Contains(got, "in") {
		t.Fatalf("expected 'in' in fallback output, got %q", got)
	}
}

func TestCompileWhereArgNonBinary(t *testing.T) {
	c := newCompiler(nil)
	// Not a BinaryExpr — falls through to compileExpr
	got := c.compileWhereArg("User", &ast.Ident{Name: "someIdent"})
	if got != "someIdent" {
		t.Fatalf("want 'someIdent', got %q", got)
	}
}

func TestCompileWhereArgMemberNonIt(t *testing.T) {
	// Left is MemberExpr but object is not "it" — field is empty, so uses empty field name
	// This exercises the path where member.Object.Name != "it", field stays "", then
	// the ident check also fails (not an ast.Ident), so we fall into the op switch with field="".
	c := newCompiler(nil)
	bin := &ast.BinaryExpr{
		Left: &ast.MemberExpr{
			Object: &ast.Ident{Name: "other"},
			Field:  "email",
		},
		Op:    "==",
		Right: &ast.Ident{Name: "email"},
	}
	// Field stays "" → produces UserWhere..Eq(email) — not a fallback to compileExpr,
	// but the Eq branch is still taken with empty field name.
	got := c.compileWhereArg("User", bin)
	if !strings.Contains(got, "UserWhere.") {
		t.Fatalf("expected 'UserWhere.' in output, got %q", got)
	}
	if !strings.Contains(got, "Eq(email)") {
		t.Fatalf("expected 'Eq(email)' in output, got %q", got)
	}
}

// ─── compileStmt — ValStmt ───────────────────────────────────────────────────

func TestCompileStmtValNonModel(t *testing.T) {
	c := newCompiler(nil)
	stmt := &ast.ValStmt{
		Name:  "x",
		Value: &ast.Literal{Kind: token.Int, Value: "42"},
	}
	c.compileStmt(stmt)
	out := compilerOut(c)
	if !strings.Contains(out, "x := 42") {
		t.Fatalf("expected 'x := 42', got %q", out)
	}
}

func TestCompileStmtValModelQuery(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	call := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "first"},
		Args: nil,
	}
	stmt := &ast.ValStmt{Name: "user", Value: call}
	c.compileStmt(stmt)
	out := compilerOut(c)
	if !strings.Contains(out, "user, err :=") {
		t.Fatalf("expected 'user, err :=', got %q", out)
	}
	if !strings.Contains(out, "if err != nil") {
		t.Fatalf("expected error check, got %q", out)
	}
}

// ─── compileStmt — ReturnStmt ────────────────────────────────────────────────

func TestCompileStmtReturnNil(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ReturnStmt{Value: nil})
	out := compilerOut(c)
	if !strings.Contains(out, "return nil") {
		t.Fatalf("expected 'return nil', got %q", out)
	}
}

func TestCompileStmtReturnScalar(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ReturnStmt{Value: &ast.Ident{Name: "count"}})
	out := compilerOut(c)
	if !strings.Contains(out, "req.Buf.AppendJSON(count)") {
		t.Fatalf("expected AppendJSON for scalar, got %q", out)
	}
}

func TestCompileStmtReturnModelVar(t *testing.T) {
	models := map[string]*ast.ModelDecl{"User": {Name: "User"}}
	c := newCompiler(models)
	// Set up api context with a val statement that assigns from a model query
	c.api = &ast.ApiDecl{
		Name: "test",
		Body: &ast.Block{
			Stmts: []ast.Stmt{
				&ast.ValStmt{
					Name: "user",
					Value: &ast.CallExpr{
						Func: &ast.MemberExpr{
							Object: &ast.CallExpr{
								Func: &ast.MemberExpr{
									Object: &ast.Ident{Name: "User"},
									Field:  "where",
								},
								Args: []*ast.NamedArg{},
							},
							Field: "first",
						},
						Args: []*ast.NamedArg{},
					},
				},
			},
		},
	}
	c.compileStmt(&ast.ReturnStmt{Value: &ast.Ident{Name: "user"}})
	out := compilerOut(c)
	if !strings.Contains(out, "user.WriteJSON(req.Buf, req.Select)") {
		t.Fatalf("expected WriteJSON for model var, got %q", out)
	}
}

// ─── compileStmt — ThrowStmt ─────────────────────────────────────────────────

func TestCompileStmtThrow(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ThrowStmt{
		Error: &ast.MemberExpr{
			Object: &ast.Ident{Name: "error"},
			Field:  "notFound",
		},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "return errors.NotFound") {
		t.Fatalf("expected 'return errors.NotFound', got %q", out)
	}
}

// ─── compileStmt — IfStmt ────────────────────────────────────────────────────

func TestCompileStmtIf(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.IfStmt{
		Condition: &ast.BinaryExpr{
			Left:  &ast.Ident{Name: "x"},
			Op:    "==",
			Right: &ast.Literal{Kind: token.Null, Value: "null"},
		},
		Then: &ast.Block{
			Stmts: []ast.Stmt{
				&ast.ReturnStmt{Value: nil},
			},
		},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "if x == nil {") {
		t.Fatalf("expected null→nil replacement in if, got %q", out)
	}
	if !strings.Contains(out, "return nil") {
		t.Fatalf("expected body in if, got %q", out)
	}
}

func TestCompileStmtIfCondWithNullPrefix(t *testing.T) {
	// cover "null " → "nil " branch (null on left side of binary)
	c := newCompiler(nil)
	c.compileStmt(&ast.IfStmt{
		Condition: &ast.BinaryExpr{
			Left:  &ast.Literal{Kind: token.Null, Value: "null"},
			Op:    "==",
			Right: &ast.Ident{Name: "x"},
		},
		Then: &ast.Block{Stmts: nil},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "nil ==") {
		t.Fatalf("expected 'nil ==' in output, got %q", out)
	}
}

// ─── compileStmt — ExprStmt ──────────────────────────────────────────────────

func TestCompileStmtExprStmt(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ExprStmt{
		Expr: &ast.CallExpr{
			Func: &ast.Ident{Name: "doWork"},
			Args: nil,
		},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "doWork()") {
		t.Fatalf("expected 'doWork()', got %q", out)
	}
}

// ─── compileStmt — EmitStmt ──────────────────────────────────────────────────

func TestCompileStmtEmit(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.EmitStmt{
		EventName: "UserCreated",
		Args:      []*ast.NamedArg{{Name: "user", Value: &ast.Ident{Name: "currentUser"}}},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "EmitUserCreated") {
		t.Fatalf("expected EmitUserCreated, got %q", out)
	}
	if !strings.Contains(out, "UserCreatedEvent{") {
		t.Fatalf("expected UserCreatedEvent{, got %q", out)
	}
	if !strings.Contains(out, "User: currentUser") {
		t.Fatalf("expected 'User: currentUser', got %q", out)
	}
	if !strings.Contains(out, "return err") {
		t.Fatalf("expected error check in emit, got %q", out)
	}
}

func TestCompileStmtEmitNoArgs(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.EmitStmt{
		EventName: "Ping",
		Args:      nil,
	})
	out := compilerOut(c)
	if !strings.Contains(out, "EmitPing") {
		t.Fatalf("expected EmitPing, got %q", out)
	}
}

// ─── compileStmt — unknown (default) ─────────────────────────────────────────

func TestCompileStmtUnknown(t *testing.T) {
	c := newCompiler(nil)
	// BreakStmt is not handled in the switch → hits default
	c.compileStmt(&ast.BreakStmt{})
	out := compilerOut(c)
	if !strings.Contains(out, "// TODO: unsupported statement") {
		t.Fatalf("expected TODO fallback, got %q", out)
	}
}

// ─── compileElvisGuard ───────────────────────────────────────────────────────

func TestCompileElvisGuardPointerNilCheck(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ExprStmt{
		Expr: &ast.ElvisExpr{
			Left:  &ast.Ident{Name: "user"},
			Right: &ast.Ident{Name: "errNotFound"},
		},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "if user == nil {") {
		t.Fatalf("expected 'if user == nil {', got %q", out)
	}
	if !strings.Contains(out, "return errNotFound") {
		t.Fatalf("expected 'return errNotFound', got %q", out)
	}
}

func TestCompileElvisGuardUnaryNot(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ExprStmt{
		Expr: &ast.ElvisExpr{
			Left:  &ast.UnaryExpr{Op: "!", Value: &ast.Ident{Name: "exists"}},
			Right: &ast.Ident{Name: "errDuplicate"},
		},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "if exists {") {
		t.Fatalf("expected 'if exists {', got %q", out)
	}
	if !strings.Contains(out, "return errDuplicate") {
		t.Fatalf("expected 'return errDuplicate', got %q", out)
	}
}

// ─── compileAPIBody ───────────────────────────────────────────────────────────

func TestCompileAPIBodyMinimal(t *testing.T) {
	api := &ast.ApiDecl{
		Name:   "ping",
		Params: nil,
		Body: &ast.Block{
			Stmts: []ast.Stmt{
				&ast.ReturnStmt{Value: nil},
			},
		},
	}
	var b strings.Builder
	compileAPIBody(&b, api, nil)
	out := b.String()

	if !strings.Contains(out, "func handlePing(app *App) api.HandlerFunc {") {
		t.Fatalf("expected handlePing func signature, got %q", out)
	}
	if !strings.Contains(out, "return func(ctx context.Context, req *api.Request) error {") {
		t.Fatalf("expected inner func signature, got %q", out)
	}
	if !strings.Contains(out, "return nil") {
		t.Fatalf("expected 'return nil', got %q", out)
	}
}

func TestCompileAPIBodyWithPrimitiveParam(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "getUser",
		Params: []*ast.ParamDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		},
		Body: &ast.Block{Stmts: nil},
	}
	var b strings.Builder
	compileAPIBody(&b, api, nil)
	out := b.String()

	if !strings.Contains(out, `req.ParamInt("id")`) {
		t.Fatalf("expected ParamInt call, got %q", out)
	}
	if !strings.Contains(out, "if err != nil") {
		t.Fatalf("expected error check, got %q", out)
	}
}

func TestCompileAPIBodyWithCustomTypeParam(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "createPost",
		Params: []*ast.ParamDecl{
			{Name: "input", Type: &ast.TypeRef{Name: "PostInput"}},
		},
		Body: &ast.Block{Stmts: nil},
	}
	var b strings.Builder
	compileAPIBody(&b, api, nil)
	out := b.String()

	if !strings.Contains(out, "var input PostInput") {
		t.Fatalf("expected 'var input PostInput', got %q", out)
	}
	if !strings.Contains(out, `req.ParamJSON("input", &input)`) {
		t.Fatalf("expected ParamJSON call, got %q", out)
	}
}

func TestCompileAPIBodyWithStringParam(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "search",
		Params: []*ast.ParamDecl{
			{Name: "query", Type: &ast.TypeRef{Name: "String"}},
		},
		Body: &ast.Block{Stmts: nil},
	}
	var b strings.Builder
	compileAPIBody(&b, api, nil)
	out := b.String()

	if !strings.Contains(out, `req.ParamString("query")`) {
		t.Fatalf("expected ParamString call, got %q", out)
	}
}

func TestCompileAPIBodyWithBoolParam(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "toggle",
		Params: []*ast.ParamDecl{
			{Name: "active", Type: &ast.TypeRef{Name: "Boolean"}},
		},
		Body: &ast.Block{Stmts: nil},
	}
	var b strings.Builder
	compileAPIBody(&b, api, nil)
	out := b.String()

	if !strings.Contains(out, `req.ParamBool("active")`) {
		t.Fatalf("expected ParamBool call, got %q", out)
	}
}

func TestCompileAPIBodyWithFloatParam(t *testing.T) {
	api := &ast.ApiDecl{
		Name: "calculate",
		Params: []*ast.ParamDecl{
			{Name: "amount", Type: &ast.TypeRef{Name: "Float"}},
		},
		Body: &ast.Block{Stmts: nil},
	}
	var b strings.Builder
	compileAPIBody(&b, api, nil)
	out := b.String()

	if !strings.Contains(out, `req.ParamFloat("amount")`) {
		t.Fatalf("expected ParamFloat call, got %q", out)
	}
}

func TestCompileAPIBodyFullFlow(t *testing.T) {
	models := makeModels("User")
	api := &ast.ApiDecl{
		Name: "getUser",
		Params: []*ast.ParamDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		},
		Body: &ast.Block{
			Stmts: []ast.Stmt{
				&ast.ValStmt{
					Name: "user",
					Value: &ast.CallExpr{
						Func: &ast.MemberExpr{
							Object: &ast.Ident{Name: "User"},
							Field:  "find",
						},
						Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "id"}}},
					},
				},
				&ast.ExprStmt{
					Expr: &ast.ElvisExpr{
						Left:  &ast.Ident{Name: "user"},
						Right: &ast.MemberExpr{Object: &ast.Ident{Name: "error"}, Field: "notFound"},
					},
				},
				&ast.ReturnStmt{Value: &ast.Ident{Name: "user"}},
			},
		},
	}

	var b strings.Builder
	compileAPIBody(&b, api, models)
	out := b.String()

	if !strings.Contains(out, "func handleGetUser(app *App) api.HandlerFunc {") {
		t.Fatalf("expected handleGetUser, got %q", out)
	}
	if !strings.Contains(out, "user, err :=") {
		t.Fatalf("expected user, err :=, got %q", out)
	}
	if !strings.Contains(out, "if user == nil {") {
		t.Fatalf("expected nil check, got %q", out)
	}
	if !strings.Contains(out, "user.WriteJSON(req.Buf, req.Select)") {
		t.Fatalf("expected WriteJSON, got %q", out)
	}
}

// ─── compileWhereChain multiple args ─────────────────────────────────────────

func TestCompileWhereChainMultipleArgs(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	whereCall := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
		Args: []*ast.NamedArg{
			{Value: &ast.BinaryExpr{
				Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "email"},
				Op:    "==",
				Right: &ast.Ident{Name: "email"},
			}},
			{Value: &ast.BinaryExpr{
				Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "active"},
				Op:    "==",
				Right: &ast.Literal{Kind: token.True, Value: "true"},
			}},
		},
	}
	allCall := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: whereCall, Field: "all"},
		Args: nil,
	}
	got := c.compileExpr(allCall)
	if !strings.Contains(got, "UserWhere.Email.Eq(email)") {
		t.Fatalf("expected first where arg, got %q", got)
	}
	if !strings.Contains(got, "UserWhere.Active.Eq(true)") {
		t.Fatalf("expected second where arg, got %q", got)
	}
}

// ─── compileWhereChain non-first (chained where) ─────────────────────────────

func TestCompileModelChainDoubleWhere(t *testing.T) {
	models := makeModels("Post")
	c := newCompiler(models)

	// Post.where(it.status == active).where(it.id == id).first()
	where1 := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Post"}, Field: "where"},
		Args: []*ast.NamedArg{{
			Value: &ast.BinaryExpr{
				Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "status"},
				Op:    "==",
				Right: &ast.Ident{Name: "active"},
			},
		}},
	}
	where2 := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: where1, Field: "where"},
		Args: []*ast.NamedArg{{
			Value: &ast.BinaryExpr{
				Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "id"},
				Op:    "==",
				Right: &ast.Ident{Name: "id"},
			},
		}},
	}
	firstCall := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: where2, Field: "first"},
		Args: nil,
	}
	got := c.compileExpr(firstCall)
	if !strings.Contains(got, "app.Post.Where(") {
		t.Fatalf("expected app.Post.Where(, got %q", got)
	}
	if !strings.Contains(got, ".Where(") {
		t.Fatalf("expected second .Where(, got %q", got)
	}
	if !strings.Contains(got, ".First(ctx)") {
		t.Fatalf("expected .First(ctx), got %q", got)
	}
}

// ─── compileModelChain — find with no args (edge) ────────────────────────────

func TestCompileModelChainFindNoArgs(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	call := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "find"},
		Args: nil, // no args
	}
	got := c.compileExpr(call)
	// Should return empty string (no args → nothing written)
	_ = got // just ensure no panic
}

// ─── compiler — write helper ──────────────────────────────────────────────────

func TestCompilerWriteAddsIndent(t *testing.T) {
	var b strings.Builder
	c := &compiler{b: &b, indent: ">>>"}
	c.write("hello %s", "world")
	out := b.String()
	if !strings.HasPrefix(out, ">>>") {
		t.Fatalf("expected indent prefix, got %q", out)
	}
	if !strings.Contains(out, "hello world") {
		t.Fatalf("expected 'hello world', got %q", out)
	}
}
