package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux"
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
		vars:   make(map[string]valType),
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

func TestCompileYieldExpr(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.YieldExpr{
		Value: &ast.Ident{Name: "item"},
	})
	if got != "_yieldResult" {
		t.Fatalf("yield should return _yieldResult, got %q", got)
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
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "paginate"},
		Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Int, Value: "10"}}},
	}
	got := c.compileExpr(call)
	if !strings.Contains(got, ".Paginate(") {
		t.Fatalf("expected .Paginate( in output, got %q", got)
	}
}

func TestCompileModelChainOrderBy(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	// User.where(...).orderBy(name.desc).all()
	call := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{
					Object: &ast.CallExpr{
						Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
						Args: []*ast.NamedArg{},
					},
					Field: "orderBy",
				},
				Args: []*ast.NamedArg{{Value: &ast.MemberExpr{Object: &ast.Ident{Name: "name"}, Field: "desc"}}},
			},
			Field: "all",
		},
		Args: []*ast.NamedArg{},
	}
	got := c.compileExpr(call)
	if !strings.Contains(got, `.OrderBy("name DESC")`) {
		t.Fatalf("expected OrderBy with DESC, got %q", got)
	}
	if !strings.Contains(got, ".All(ctx)") {
		t.Fatalf("expected .All(ctx), got %q", got)
	}
}

func TestCompileModelChainLimitOffset(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	call := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{
					Object: &ast.CallExpr{
						Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
						Args: []*ast.NamedArg{},
					},
					Field: "limit",
				},
				Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Int, Value: "10"}}},
			},
			Field: "offset",
		},
		Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Int, Value: "20"}}},
	}
	got := c.compileExpr(call)
	if !strings.Contains(got, ".Limit(10)") {
		t.Fatalf("expected .Limit(10), got %q", got)
	}
	if !strings.Contains(got, ".Offset(20)") {
		t.Fatalf("expected .Offset(20), got %q", got)
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
	if !strings.Contains(out, "x := int64(42)") {
		t.Fatalf("expected 'x := int64(42)', got %q", out)
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
	if !strings.Contains(out, "unsupported return type") {
		t.Fatalf("expected unsupported for scalar, got %q", out)
	}
}

func TestCompileStmtReturnModelVar(t *testing.T) {
	models := map[string]*ast.ModelDecl{"User": {Name: "User"}}
	c := newCompiler(models)
	c.vars = make(map[string]valType)

	// First compile the val statement to register the variable type
	valStmt := &ast.ValStmt{
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
	}
	c.compileStmt(valStmt)

	// Reset output, then compile return
	c.b.Reset()
	c.compileStmt(&ast.ReturnStmt{Value: &ast.Ident{Name: "user"}})
	out := compilerOut(c)
	if !strings.Contains(out, "user.WriteLuxo(req.Buf, req.FieldMask)") {
		t.Fatalf("expected WriteLuxo for model var, got %q", out)
	}
}

func TestCompileStmtReturnModelList(t *testing.T) {
	models := map[string]*ast.ModelDecl{"User": {Name: "User"}}
	c := newCompiler(models)
	c.vars = make(map[string]valType)

	// val users = User.where(...).all() → list type
	valStmt := &ast.ValStmt{
		Name: "users",
		Value: &ast.CallExpr{
			Func: &ast.MemberExpr{
				Object: &ast.CallExpr{
					Func: &ast.MemberExpr{
						Object: &ast.Ident{Name: "User"},
						Field:  "where",
					},
					Args: []*ast.NamedArg{},
				},
				Field: "all",
			},
			Args: []*ast.NamedArg{},
		},
	}
	c.compileStmt(valStmt)

	c.b.Reset()
	c.compileStmt(&ast.ReturnStmt{Value: &ast.Ident{Name: "users"}})
	out := compilerOut(c)
	if !strings.Contains(out, "WriteColumnar") {
		t.Fatalf("expected WriteColumnar for list var, got %q", out)
	}
}

func TestCompileStmtReturnWithAPIReturnType(t *testing.T) {
	c := newCompiler(nil)
	c.vars = make(map[string]valType)
	c.api = &ast.ApiDecl{
		Name:       "countUsers",
		ReturnType: &ast.TypeRef{Name: "Int"},
	}
	c.compileStmt(&ast.ReturnStmt{Value: &ast.Ident{Name: "count"}})
	out := compilerOut(c)
	if !strings.Contains(out, "codec.AppendSvarint(req.Buf.B, int64(count))") {
		t.Fatalf("expected AppendInt for Int return type, got %q", out)
	}
}

func TestCompileStmtReturnBooleanType(t *testing.T) {
	c := newCompiler(nil)
	c.vars = make(map[string]valType)
	c.api = &ast.ApiDecl{
		Name:       "existsUser",
		ReturnType: &ast.TypeRef{Name: "Boolean"},
	}
	c.compileStmt(&ast.ReturnStmt{Value: &ast.Ident{Name: "exists"}})
	out := compilerOut(c)
	if !strings.Contains(out, "codec.AppendBool(req.Buf.B, exists)") {
		t.Fatalf("expected AppendBool for Boolean return type, got %q", out)
	}
}

func TestCompileStmtReturnDirectQuery(t *testing.T) {
	models := map[string]*ast.ModelDecl{"User": {Name: "User"}}
	c := newCompiler(models)
	c.vars = make(map[string]valType)

	// return User.where(...).all() — direct query, list type
	retExpr := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{
					Object: &ast.Ident{Name: "User"},
					Field:  "where",
				},
				Args: []*ast.NamedArg{},
			},
			Field: "all",
		},
		Args: []*ast.NamedArg{},
	}
	c.compileStmt(&ast.ReturnStmt{Value: retExpr})
	out := compilerOut(c)
	if !strings.Contains(out, "WriteColumnar") {
		t.Fatalf("expected WriteColumnar for direct .all() return, got %q", out)
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
	// OnDecl is not handled in the switch → hits default
	c.compileStmt(&ast.OnDecl{})
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
	compileAPIBody(&b, api, nil, nil)
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
	compileAPIBody(&b, api, nil, nil)
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
	compileAPIBody(&b, api, nil, nil)
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
	compileAPIBody(&b, api, nil, nil)
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
	compileAPIBody(&b, api, nil, nil)
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
	compileAPIBody(&b, api, nil, nil)
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
	compileAPIBody(&b, api, models, nil)
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
	if !strings.Contains(out, "user.WriteLuxo(req.Buf, req.FieldMask)") {
		t.Fatalf("expected WriteLuxo, got %q", out)
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

func TestCompileReturnFloatType(t *testing.T) {
	c := newCompiler(nil)
	c.api = &ast.ApiDecl{Name: "getPrice", ReturnType: &ast.TypeRef{Name: "Float"}}
	c.compileStmt(&ast.ReturnStmt{Value: &ast.Ident{Name: "price"}})
	out := compilerOut(c)
	if !strings.Contains(out, "codec.AppendFixed64(req.Buf.B, price)") {
		t.Fatalf("expected AppendFloat, got %q", out)
	}
}

func TestCompileReturnStringType(t *testing.T) {
	c := newCompiler(nil)
	c.api = &ast.ApiDecl{Name: "getName", ReturnType: &ast.TypeRef{Name: "String"}}
	c.compileStmt(&ast.ReturnStmt{Value: &ast.Ident{Name: "name"}})
	out := compilerOut(c)
	if !strings.Contains(out, "codec.AppendString(req.Buf.B, name)") {
		t.Fatalf("expected AppendJSONString, got %q", out)
	}
}

func TestCompileReturnCustomType(t *testing.T) {
	// PascalCase return types are now treated as type declarations with WriteLuxo
	c := newCompiler(nil)
	c.api = &ast.ApiDecl{Name: "getCustom", ReturnType: &ast.TypeRef{Name: "CustomType"}}
	c.compileStmt(&ast.ReturnStmt{Value: &ast.Ident{Name: "data"}})
	out := compilerOut(c)
	if !strings.Contains(out, "WriteLuxo") {
		t.Fatalf("expected WriteLuxo for custom type, got %q", out)
	}
}

func TestCompileReturnNoAPIContext(t *testing.T) {
	c := newCompiler(nil)
	c.api = nil
	c.compileStmt(&ast.ReturnStmt{Value: &ast.Ident{Name: "x"}})
	out := compilerOut(c)
	if !strings.Contains(out, "unsupported return type") {
		t.Fatalf("expected unsupported fallback when no api, got %q", out)
	}
}

func TestCompileReturnExistsQueryVar(t *testing.T) {
	models := map[string]*ast.ModelDecl{"User": {Name: "User"}}
	c := newCompiler(models)
	// val exists = User.where(...).exists()
	c.compileStmt(&ast.ValStmt{
		Name: "exists",
		Value: &ast.CallExpr{
			Func: &ast.MemberExpr{
				Object: &ast.CallExpr{
					Func: &ast.MemberExpr{
						Object: &ast.Ident{Name: "User"},
						Field:  "where",
					},
					Args: []*ast.NamedArg{},
				},
				Field: "exists",
			},
			Args: []*ast.NamedArg{},
		},
	})
	c.b.Reset()
	c.compileStmt(&ast.ReturnStmt{Value: &ast.Ident{Name: "exists"}})
	out := compilerOut(c)
	if !strings.Contains(out, "codec.AppendBool(req.Buf.B, exists)") {
		t.Fatalf("expected AppendBool for exists var, got %q", out)
	}
}

func TestCompileReturnCreateQueryVar(t *testing.T) {
	models := map[string]*ast.ModelDecl{"User": {Name: "User"}}
	c := newCompiler(models)
	// val user = User.create(name: "lin")
	c.compileStmt(&ast.ValStmt{
		Name: "user",
		Value: &ast.CallExpr{
			Func: &ast.MemberExpr{
				Object: &ast.Ident{Name: "User"},
				Field:  "create",
			},
			Args: []*ast.NamedArg{{Name: "name", Value: &ast.Ident{Name: "n"}}},
		},
	})
	c.b.Reset()
	c.compileStmt(&ast.ReturnStmt{Value: &ast.Ident{Name: "user"}})
	out := compilerOut(c)
	if !strings.Contains(out, "user.WriteLuxo(req.Buf, req.FieldMask)") {
		t.Fatalf("expected WriteLuxo for create result, got %q", out)
	}
}

func TestWriteReturnByTypeScalars(t *testing.T) {
	tests := []struct {
		vt   valType
		want string
	}{
		{valType{name: "Int"}, "AppendSvarint"},
		{valType{name: "Float"}, "AppendFixed64"},
		{valType{name: "Boolean"}, "AppendBool"},
		{valType{name: "String"}, "AppendString"},
		{valType{name: ""}, "unsupported return type"},
	}
	for _, tt := range tests {
		t.Run(tt.vt.name, func(t *testing.T) {
			c := newCompiler(nil)
			c.writeReturnByType("val", tt.vt)
			out := compilerOut(c)
			if !strings.Contains(out, tt.want) {
				t.Errorf("writeReturnByType(%+v) missing %q, got %q", tt.vt, tt.want, out)
			}
		})
	}
}

func TestResolveQueryTypeFind(t *testing.T) {
	models := map[string]*ast.ModelDecl{"Post": {Name: "Post"}}
	c := newCompiler(models)
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Post"}, Field: "find"},
		Args: []*ast.NamedArg{{Name: "id", Value: &ast.Ident{Name: "id"}}},
	}
	vt := c.resolveQueryType(expr)
	if !vt.isModel || vt.name != "Post" || vt.isList {
		t.Errorf("resolveQueryType(Post.find) = %+v", vt)
	}
}

func TestCompileReturnDirectFirstQuery(t *testing.T) {
	models := map[string]*ast.ModelDecl{"User": {Name: "User"}}
	c := newCompiler(models)
	// return User.where(...).first() — direct non-list query
	retExpr := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
				Args: []*ast.NamedArg{},
			},
			Field: "first",
		},
		Args: []*ast.NamedArg{},
	}
	c.compileStmt(&ast.ReturnStmt{Value: retExpr})
	out := compilerOut(c)
	if !strings.Contains(out, ".WriteLuxo(req.Buf, req.FieldMask)") {
		t.Fatalf("expected WriteLuxo for direct .first() return, got %q", out)
	}
	if strings.Contains(out, "ListJSON") {
		t.Fatalf("should NOT use ListJSON for .first(), got %q", out)
	}
}

func TestResolveQueryTypeExec(t *testing.T) {
	models := map[string]*ast.ModelDecl{"Post": {Name: "Post"}}
	c := newCompiler(models)
	// Post.create(...).exec()
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Post"}, Field: "create"},
				Args: []*ast.NamedArg{},
			},
			Field: "exec",
		},
		Args: []*ast.NamedArg{},
	}
	vt := c.resolveQueryType(expr)
	if !vt.isModel || vt.name != "Post" {
		t.Errorf("resolveQueryType(Post.create.exec) = %+v, want model Post", vt)
	}
}

func TestResolveQueryTypeNonModel(t *testing.T) {
	c := newCompiler(nil)
	vt := c.resolveQueryType(&ast.Ident{Name: "x"})
	if vt.isModel || vt.name != "" {
		t.Errorf("resolveQueryType(non-model) = %+v", vt)
	}
}

// --- Phase 2 compiler tests ---

func TestCompileFor(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ForStmt{
		VarName:    "item",
		Collection: &ast.Ident{Name: "items"},
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ExprStmt{Expr: &ast.CallExpr{
				Func: &ast.Ident{Name: "process"},
				Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "item"}}},
			}},
		}},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "for _, item := range items {") {
		t.Fatalf("missing for range, got %q", out)
	}
	if !strings.Contains(out, "process(item)") {
		t.Fatalf("missing body, got %q", out)
	}
}

func TestCompileAssign(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.AssignStmt{
		Target: &ast.Ident{Name: "count"},
		Op:     "+=",
		Value:  &ast.Literal{Kind: token.Int, Value: "1"},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "count += 1") {
		t.Fatalf("missing assign, got %q", out)
	}
}

func TestCompileBreakContinue(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.BreakStmt{})
	c.compileStmt(&ast.ContinueStmt{})
	out := compilerOut(c)
	if !strings.Contains(out, "break") || !strings.Contains(out, "continue") {
		t.Fatalf("missing break/continue, got %q", out)
	}
}

func TestCompileList(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.ListExpr{Items: []ast.Expr{
		&ast.Literal{Kind: token.Int, Value: "1"},
		&ast.Literal{Kind: token.Int, Value: "2"},
	}})
	if got != "[]any{1, 2}" {
		t.Fatalf("expected []any{1, 2}, got %q", got)
	}
}

func TestCompileListEmpty(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.ListExpr{Items: nil})
	if got != "[]any{}" {
		t.Fatalf("expected []any{}, got %q", got)
	}
}

func TestCompileTemplate(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.TemplateString{Parts: []ast.Expr{
		&ast.Literal{Kind: token.String, Value: "hello "},
		&ast.Ident{Name: "name"},
		&ast.Literal{Kind: token.String, Value: "!"},
	}})
	if !strings.Contains(got, `"hello "`) || !strings.Contains(got, "name") || !strings.Contains(got, "_sb.") {
		t.Fatalf("unexpected template output: %q", got)
	}
}

func TestCompileRange(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.RangeExpr{
		Start: &ast.Literal{Kind: token.Int, Value: "1"},
		End:   &ast.Literal{Kind: token.Int, Value: "10"},
	})
	if got != "lux.IntRange(1, 10)" {
		t.Fatalf("expected lux.IntRange(1, 10), got %q", got)
	}
}

func TestCompileObject(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.ObjectExpr{Fields: []*ast.NamedArg{
		{Name: "name", Value: &ast.Literal{Kind: token.String, Value: "lin"}},
		{Name: "age", Value: &ast.Literal{Kind: token.Int, Value: "18"}},
	}})
	if !strings.Contains(got, "Name:") || !strings.Contains(got, "Age:") {
		t.Fatalf("missing field names, got %q", got)
	}
}

func TestCompileWhenWithSubject(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.WhenExpr{
		Subject: &ast.Ident{Name: "role"},
		Branches: []*ast.WhenBranch{
			{Condition: &ast.Literal{Kind: token.String, Value: "ADMIN"}, Body: &ast.Literal{Kind: token.True}},
		},
		Else: &ast.Literal{Kind: token.False},
	})
	if !strings.Contains(got, "switch role") {
		t.Fatalf("missing switch, got %q", got)
	}
	if !strings.Contains(got, "default:") {
		t.Fatalf("missing else/default, got %q", got)
	}
}

func TestCompileWhenWithoutSubject(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.WhenExpr{
		Branches: []*ast.WhenBranch{
			{Condition: &ast.BinaryExpr{Left: &ast.Ident{Name: "x"}, Op: ">", Right: &ast.Literal{Kind: token.Int, Value: "0"}},
				Body: &ast.Literal{Kind: token.String, Value: "positive"}},
		},
	})
	if !strings.Contains(got, "switch {") {
		t.Fatalf("missing switch {, got %q", got)
	}
}

func TestCompileLambda(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.LambdaExpr{
		Params: []string{"x"},
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ReturnStmt{Value: &ast.Ident{Name: "x"}},
		}},
	})
	if !strings.Contains(got, "func(x any) any {") {
		t.Fatalf("missing lambda signature, got %q", got)
	}
}

func TestCompileLambdaImplicitIt(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.LambdaExpr{
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ReturnStmt{Value: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "price"}},
		}},
	})
	if !strings.Contains(got, "func(it any) any {") {
		t.Fatalf("missing implicit it, got %q", got)
	}
}

func TestCompileTransaction(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.TransactionExpr{
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ExprStmt{Expr: &ast.Ident{Name: "doSomething"}},
		}},
	})
	if !strings.Contains(got, "app.DB.Tx(ctx, func(ctx context.Context) error {") {
		t.Fatalf("missing Tx wrapper, got %q", got)
	}
	if !strings.Contains(got, "return nil") {
		t.Fatalf("missing return nil, got %q", got)
	}
}

func TestCompileModelUpdate(t *testing.T) {
	models := map[string]*ast.ModelDecl{"User": {Name: "User"}}
	c := newCompiler(models)
	got := c.compileExpr(&ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
				Args: []*ast.NamedArg{},
			},
			Field: "update",
		},
		Args: []*ast.NamedArg{
			{Name: "name", Value: &ast.Literal{Kind: token.String, Value: "new"}},
		},
	})
	if !strings.Contains(got, ".Update(ctx,") {
		t.Fatalf("missing .Update(ctx, ...), got %q", got)
	}
	if !strings.Contains(got, `lux.SetField{Col: "name"`) {
		t.Fatalf("missing SetField, got %q", got)
	}
}

func TestCompileModelDelete(t *testing.T) {
	models := map[string]*ast.ModelDecl{"User": {Name: "User"}}
	c := newCompiler(models)
	got := c.compileExpr(&ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
				Args: []*ast.NamedArg{},
			},
			Field: "delete",
		},
		Args: []*ast.NamedArg{},
	})
	if !strings.Contains(got, ".Delete(ctx)") {
		t.Fatalf("missing .Delete(ctx), got %q", got)
	}
}

func TestCompileModelCount(t *testing.T) {
	models := map[string]*ast.ModelDecl{"User": {Name: "User"}}
	c := newCompiler(models)
	got := c.compileExpr(&ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
				Args: []*ast.NamedArg{},
			},
			Field: "count",
		},
		Args: []*ast.NamedArg{},
	})
	if !strings.Contains(got, ".Count(ctx)") {
		t.Fatalf("missing .Count(ctx), got %q", got)
	}
}

func TestResolveQueryTypeUpdateDeleteCount(t *testing.T) {
	models := map[string]*ast.ModelDecl{"User": {Name: "User"}}
	c := newCompiler(models)

	mkChain := func(method string) ast.Expr {
		return &ast.CallExpr{
			Func: &ast.MemberExpr{
				Object: &ast.CallExpr{
					Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
					Args: []*ast.NamedArg{},
				},
				Field: method,
			},
			Args: []*ast.NamedArg{},
		}
	}

	vt := c.resolveQueryType(mkChain("update"))
	if vt.name != "Int" {
		t.Errorf("update type = %q, want Int", vt.name)
	}
	vt = c.resolveQueryType(mkChain("delete"))
	if vt.name != "Int" {
		t.Errorf("delete type = %q, want Int", vt.name)
	}
	vt = c.resolveQueryType(mkChain("count"))
	if vt.name != "Int" {
		t.Errorf("count type = %q, want Int", vt.name)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// A. Real-world API scenarios (full compileAPIBody)
// ═══════════════════════════════════════════════════════════════════════════════

// TestCompileRegisterAPI — User registration:
//
//	api register(name: String, email: String, password: String): User {
//	  val exists = User.where(it.email == email).exists()
//	  !exists ?: throw error.Conflict
//	  val user = User.create(name: name, email: email, password: password)
//	  return user
//	}
func TestCompileRegisterAPI(t *testing.T) {
	models := makeModels("User")
	api := &ast.ApiDecl{
		Name: "register",
		Params: []*ast.ParamDecl{
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
			{Name: "email", Type: &ast.TypeRef{Name: "String"}},
			{Name: "password", Type: &ast.TypeRef{Name: "String"}},
		},
		ReturnType: &ast.TypeRef{Name: "User"},
		Body: &ast.Block{Stmts: []ast.Stmt{
			// val exists = User.where(it.email == email).exists()
			&ast.ValStmt{
				Name: "exists",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{
						Object: &ast.CallExpr{
							Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
							Args: []*ast.NamedArg{{
								Value: &ast.BinaryExpr{
									Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "email"},
									Op:    "==",
									Right: &ast.Ident{Name: "email"},
								},
							}},
						},
						Field: "exists",
					},
				},
			},
			// !exists ?: throw error.Conflict
			&ast.ExprStmt{
				Expr: &ast.ElvisExpr{
					Left:  &ast.UnaryExpr{Op: "!", Value: &ast.Ident{Name: "exists"}},
					Right: &ast.MemberExpr{Object: &ast.Ident{Name: "error"}, Field: "conflict"},
				},
			},
			// val user = User.create(name: name, email: email, password: password)
			&ast.ValStmt{
				Name: "user",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "create"},
					Args: []*ast.NamedArg{
						{Name: "name", Value: &ast.Ident{Name: "name"}},
						{Name: "email", Value: &ast.Ident{Name: "email"}},
						{Name: "password", Value: &ast.Ident{Name: "password"}},
					},
				},
			},
			// return user
			&ast.ReturnStmt{Value: &ast.Ident{Name: "user"}},
		}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, models, nil)
	out := b.String()

	// Check function signature
	if !strings.Contains(out, "func handleRegister(app *App) api.HandlerFunc") {
		t.Fatalf("missing handleRegister signature, got:\n%s", out)
	}
	// Check param extraction
	if !strings.Contains(out, `req.ParamString("name")`) {
		t.Fatalf("missing ParamString for name, got:\n%s", out)
	}
	if !strings.Contains(out, `req.ParamString("email")`) {
		t.Fatalf("missing ParamString for email, got:\n%s", out)
	}
	if !strings.Contains(out, `req.ParamString("password")`) {
		t.Fatalf("missing ParamString for password, got:\n%s", out)
	}
	// Check exists query
	if !strings.Contains(out, "UserWhere.Email.Eq(email)") {
		t.Fatalf("missing where clause, got:\n%s", out)
	}
	if !strings.Contains(out, ".Exists(ctx)") {
		t.Fatalf("missing .Exists(ctx), got:\n%s", out)
	}
	// Check elvis guard: !exists ?: throw error.Conflict → if exists { return errors.Conflict }
	if !strings.Contains(out, "if exists {") {
		t.Fatalf("missing elvis guard, got:\n%s", out)
	}
	if !strings.Contains(out, "return errors.Conflict") {
		t.Fatalf("missing throw error.Conflict, got:\n%s", out)
	}
	// Check create chain
	if !strings.Contains(out, "app.User.Create()") {
		t.Fatalf("missing Create(), got:\n%s", out)
	}
	if !strings.Contains(out, ".SetName(name)") {
		t.Fatalf("missing .SetName(name), got:\n%s", out)
	}
	if !strings.Contains(out, ".SetEmail(email)") {
		t.Fatalf("missing .SetEmail(email), got:\n%s", out)
	}
	if !strings.Contains(out, ".SetPassword(password)") {
		t.Fatalf("missing .SetPassword(password), got:\n%s", out)
	}
	// Check return — user is model (from create) → WriteLuxo
	if !strings.Contains(out, "user.WriteLuxo(req.Buf, req.FieldMask)") {
		t.Fatalf("missing WriteLuxo return, got:\n%s", out)
	}
}

// TestCompileUpdateProfileAPI — Update with field assignment:
//
//	api updateProfile(id: Int, name: String, bio: String): User {
//	  val user = User.find(id)
//	  user.name = name
//	  user.bio = bio
//	  return user
//	}
func TestCompileUpdateProfileAPI(t *testing.T) {
	models := makeModels("User")
	api := &ast.ApiDecl{
		Name: "updateProfile",
		Params: []*ast.ParamDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
			{Name: "bio", Type: &ast.TypeRef{Name: "String"}},
		},
		ReturnType: &ast.TypeRef{Name: "User"},
		Body: &ast.Block{Stmts: []ast.Stmt{
			// val user = User.find(id)
			&ast.ValStmt{
				Name: "user",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "find"},
					Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "id"}}},
				},
			},
			// user.name = name
			&ast.AssignStmt{
				Target: &ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "name"},
				Op:     "=",
				Value:  &ast.Ident{Name: "name"},
			},
			// user.bio = bio
			&ast.AssignStmt{
				Target: &ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "bio"},
				Op:     "=",
				Value:  &ast.Ident{Name: "bio"},
			},
			// return user
			&ast.ReturnStmt{Value: &ast.Ident{Name: "user"}},
		}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, models, nil)
	out := b.String()

	if !strings.Contains(out, "func handleUpdateProfile(app *App) api.HandlerFunc") {
		t.Fatalf("missing function signature, got:\n%s", out)
	}
	if !strings.Contains(out, `req.ParamInt("id")`) {
		t.Fatalf("missing ParamInt for id, got:\n%s", out)
	}
	if !strings.Contains(out, `req.ParamString("name")`) {
		t.Fatalf("missing ParamString for name, got:\n%s", out)
	}
	// Check find
	if !strings.Contains(out, "app.User.Where(UserWhere.Id.Eq(id)).First(ctx)") {
		t.Fatalf("missing find chain, got:\n%s", out)
	}
	// Check assignments
	if !strings.Contains(out, "user.Name = name") {
		t.Fatalf("missing user.Name = name, got:\n%s", out)
	}
	if !strings.Contains(out, "user.Bio = bio") {
		t.Fatalf("missing user.Bio = bio, got:\n%s", out)
	}
	// Check return — user is model from find → WriteLuxo
	if !strings.Contains(out, "user.WriteLuxo(req.Buf, req.FieldMask)") {
		t.Fatalf("missing WriteLuxo return, got:\n%s", out)
	}
}

// TestCompileDeleteWithCheckAPI — Delete with existence check:
//
//	api deleteUser(id: Int): Int {
//	  val user = User.find(id)
//	  user ?: throw error.NotFound
//	  val count = User.where(it.id == id).delete()
//	  return count
//	}
func TestCompileDeleteWithCheckAPI(t *testing.T) {
	models := makeModels("User")
	api := &ast.ApiDecl{
		Name: "deleteUser",
		Params: []*ast.ParamDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		},
		ReturnType: &ast.TypeRef{Name: "Int"},
		Body: &ast.Block{Stmts: []ast.Stmt{
			// val user = User.find(id)
			&ast.ValStmt{
				Name: "user",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "find"},
					Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "id"}}},
				},
			},
			// user ?: throw error.NotFound
			&ast.ExprStmt{
				Expr: &ast.ElvisExpr{
					Left:  &ast.Ident{Name: "user"},
					Right: &ast.MemberExpr{Object: &ast.Ident{Name: "error"}, Field: "notFound"},
				},
			},
			// val count = User.where(it.id == id).delete()
			&ast.ValStmt{
				Name: "count",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{
						Object: &ast.CallExpr{
							Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
							Args: []*ast.NamedArg{{
								Value: &ast.BinaryExpr{
									Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "id"},
									Op:    "==",
									Right: &ast.Ident{Name: "id"},
								},
							}},
						},
						Field: "delete",
					},
				},
			},
			// return count
			&ast.ReturnStmt{Value: &ast.Ident{Name: "count"}},
		}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, models, nil)
	out := b.String()

	if !strings.Contains(out, "func handleDeleteUser(app *App) api.HandlerFunc") {
		t.Fatalf("missing function signature, got:\n%s", out)
	}
	// Check find
	if !strings.Contains(out, "app.User.Where(UserWhere.Id.Eq(id)).First(ctx)") {
		t.Fatalf("missing find, got:\n%s", out)
	}
	// Check elvis nil guard
	if !strings.Contains(out, "if user == nil {") {
		t.Fatalf("missing nil check, got:\n%s", out)
	}
	if !strings.Contains(out, "return errors.NotFound") {
		t.Fatalf("missing throw NotFound, got:\n%s", out)
	}
	// Check delete
	if !strings.Contains(out, ".Delete(ctx)") {
		t.Fatalf("missing .Delete(ctx), got:\n%s", out)
	}
	// Check return count — count is Int type from delete → AppendInt
	if !strings.Contains(out, "codec.AppendSvarint(req.Buf.B, int64(count))") {
		t.Fatalf("missing AppendInt for count, got:\n%s", out)
	}
}

// TestCompileListWithFilterAPI — List with for loop and conditional:
//
//	api processUsers(): Boolean {
//	  val users = User.where(it.role == "ADMIN").all()
//	  for user in users {
//	    if user.active == false {
//	      User.where(it.id == user.id).delete()
//	    }
//	  }
//	  return true
//	}
func TestCompileListWithFilterAPI(t *testing.T) {
	models := makeModels("User")
	api := &ast.ApiDecl{
		Name:       "processUsers",
		Params:     nil,
		ReturnType: &ast.TypeRef{Name: "Boolean"},
		Body: &ast.Block{Stmts: []ast.Stmt{
			// val users = User.where(it.role == "ADMIN").all()
			&ast.ValStmt{
				Name: "users",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{
						Object: &ast.CallExpr{
							Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
							Args: []*ast.NamedArg{{
								Value: &ast.BinaryExpr{
									Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "role"},
									Op:    "==",
									Right: &ast.Literal{Kind: token.String, Value: "ADMIN"},
								},
							}},
						},
						Field: "all",
					},
				},
			},
			// for user in users { if user.active == false { User.where(it.id == user.id).delete() } }
			&ast.ForStmt{
				VarName:    "user",
				Collection: &ast.Ident{Name: "users"},
				Body: &ast.Block{Stmts: []ast.Stmt{
					&ast.IfStmt{
						Condition: &ast.BinaryExpr{
							Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "active"},
							Op:    "==",
							Right: &ast.Literal{Kind: token.False, Value: "false"},
						},
						Then: &ast.Block{Stmts: []ast.Stmt{
							&ast.ExprStmt{
								Expr: &ast.CallExpr{
									Func: &ast.MemberExpr{
										Object: &ast.CallExpr{
											Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
											Args: []*ast.NamedArg{{
												Value: &ast.BinaryExpr{
													Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "id"},
													Op:    "==",
													Right: &ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "id"},
												},
											}},
										},
										Field: "delete",
									},
								},
							},
						}},
					},
				}},
			},
			// return true
			&ast.ReturnStmt{Value: &ast.Literal{Kind: token.True, Value: "true"}},
		}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, models, nil)
	out := b.String()

	if !strings.Contains(out, "func handleProcessUsers(app *App) api.HandlerFunc") {
		t.Fatalf("missing function signature, got:\n%s", out)
	}
	// Check all query
	if !strings.Contains(out, `UserWhere.Role.Eq("ADMIN")`) {
		t.Fatalf("missing where role ADMIN, got:\n%s", out)
	}
	if !strings.Contains(out, ".All(ctx)") {
		t.Fatalf("missing .All(ctx), got:\n%s", out)
	}
	// Check for loop
	if !strings.Contains(out, "for _, user := range users {") {
		t.Fatalf("missing for range, got:\n%s", out)
	}
	// Check if condition
	if !strings.Contains(out, "if user.Active == false {") {
		t.Fatalf("missing if condition, got:\n%s", out)
	}
	// Check nested delete
	if !strings.Contains(out, "UserWhere.Id.Eq(user.Id)") {
		t.Fatalf("missing nested where for delete, got:\n%s", out)
	}
	if !strings.Contains(out, ".Delete(ctx)") {
		t.Fatalf("missing .Delete(ctx), got:\n%s", out)
	}
	// Check return
	if !strings.Contains(out, "codec.AppendBool(req.Buf.B, true)") {
		t.Fatalf("missing AppendBool(true), got:\n%s", out)
	}
}

// TestCompileBatchCreateAPI — Create in a loop with emit:
//
//	api batchNotify(message: String): Int {
//	  val users = User.where(it.active == true).all()
//	  val count = 0
//	  for user in users {
//	    emit UserNotified(userId: user.id, message: message)
//	    count += 1
//	  }
//	  return count
//	}
func TestCompileBatchCreateAPI(t *testing.T) {
	models := makeModels("User")
	api := &ast.ApiDecl{
		Name: "batchNotify",
		Params: []*ast.ParamDecl{
			{Name: "message", Type: &ast.TypeRef{Name: "String"}},
		},
		ReturnType: &ast.TypeRef{Name: "Int"},
		Body: &ast.Block{Stmts: []ast.Stmt{
			// val users = User.where(it.active == true).all()
			&ast.ValStmt{
				Name: "users",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{
						Object: &ast.CallExpr{
							Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
							Args: []*ast.NamedArg{{
								Value: &ast.BinaryExpr{
									Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "active"},
									Op:    "==",
									Right: &ast.Literal{Kind: token.True, Value: "true"},
								},
							}},
						},
						Field: "all",
					},
				},
			},
			// val count = 0
			&ast.ValStmt{
				Name:  "count",
				Value: &ast.Literal{Kind: token.Int, Value: "0"},
			},
			// for user in users { emit UserNotified(...); count += 1 }
			&ast.ForStmt{
				VarName:    "user",
				Collection: &ast.Ident{Name: "users"},
				Body: &ast.Block{Stmts: []ast.Stmt{
					&ast.EmitStmt{
						EventName: "UserNotified",
						Args: []*ast.NamedArg{
							{Name: "userId", Value: &ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "id"}},
							{Name: "message", Value: &ast.Ident{Name: "message"}},
						},
					},
					&ast.AssignStmt{
						Target: &ast.Ident{Name: "count"},
						Op:     "+=",
						Value:  &ast.Literal{Kind: token.Int, Value: "1"},
					},
				}},
			},
			// return count
			&ast.ReturnStmt{Value: &ast.Ident{Name: "count"}},
		}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, models, nil)
	out := b.String()

	if !strings.Contains(out, "func handleBatchNotify(app *App) api.HandlerFunc") {
		t.Fatalf("missing function signature, got:\n%s", out)
	}
	if !strings.Contains(out, `req.ParamString("message")`) {
		t.Fatalf("missing ParamString for message, got:\n%s", out)
	}
	// Check users query
	if !strings.Contains(out, "UserWhere.Active.Eq(true)") {
		t.Fatalf("missing where active, got:\n%s", out)
	}
	if !strings.Contains(out, ".All(ctx)") {
		t.Fatalf("missing .All(ctx), got:\n%s", out)
	}
	// Check count init
	if !strings.Contains(out, "count := int64(0)") {
		t.Fatalf("missing count := 0, got:\n%s", out)
	}
	// Check for loop
	if !strings.Contains(out, "for _, user := range users {") {
		t.Fatalf("missing for range, got:\n%s", out)
	}
	// Check emit
	if !strings.Contains(out, "EmitUserNotified") {
		t.Fatalf("missing EmitUserNotified, got:\n%s", out)
	}
	if !strings.Contains(out, "UserNotifiedEvent{") {
		t.Fatalf("missing UserNotifiedEvent{, got:\n%s", out)
	}
	if !strings.Contains(out, "UserId: user.Id") {
		t.Fatalf("missing UserId: user.Id, got:\n%s", out)
	}
	if !strings.Contains(out, "Message: message") {
		t.Fatalf("missing Message: message, got:\n%s", out)
	}
	// Check count increment
	if !strings.Contains(out, "count += 1") {
		t.Fatalf("missing count += 1, got:\n%s", out)
	}
	// Check return — count is plain val not model query, api returns Int → AppendInt
	if !strings.Contains(out, "codec.AppendSvarint(req.Buf.B, int64(count))") {
		t.Fatalf("missing AppendInt(count), got:\n%s", out)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// B. Mixed expression tests (newCompiler + compileExpr/compileStmt)
// ═══════════════════════════════════════════════════════════════════════════════

// TestCompileNestedForWithBreak — for with if/break inside
func TestCompileNestedForWithBreak(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ForStmt{
		VarName:    "item",
		Collection: &ast.Ident{Name: "items"},
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.IfStmt{
				Condition: &ast.BinaryExpr{
					Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "item"}, Field: "done"},
					Op:    "==",
					Right: &ast.Literal{Kind: token.True, Value: "true"},
				},
				Then: &ast.Block{Stmts: []ast.Stmt{
					&ast.BreakStmt{},
				}},
			},
		}},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "for _, item := range items {") {
		t.Fatalf("missing for range, got:\n%s", out)
	}
	if !strings.Contains(out, "if item.Done == true {") {
		t.Fatalf("missing if condition, got:\n%s", out)
	}
	if !strings.Contains(out, "break") {
		t.Fatalf("missing break, got:\n%s", out)
	}
}

// TestCompileForWithContinue — for with continue
func TestCompileForWithContinue(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ForStmt{
		VarName:    "user",
		Collection: &ast.Ident{Name: "users"},
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.IfStmt{
				Condition: &ast.BinaryExpr{
					Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "active"},
					Op:    "==",
					Right: &ast.Literal{Kind: token.False, Value: "false"},
				},
				Then: &ast.Block{Stmts: []ast.Stmt{
					&ast.ContinueStmt{},
				}},
			},
			&ast.ExprStmt{Expr: &ast.CallExpr{
				Func: &ast.Ident{Name: "process"},
				Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "user"}}},
			}},
		}},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "for _, user := range users {") {
		t.Fatalf("missing for range, got:\n%s", out)
	}
	if !strings.Contains(out, "continue") {
		t.Fatalf("missing continue, got:\n%s", out)
	}
	if !strings.Contains(out, "process(user)") {
		t.Fatalf("missing process call after continue block, got:\n%s", out)
	}
}

// TestCompileWhenAsStatement — when used inside an if body
func TestCompileWhenAsStatement(t *testing.T) {
	c := newCompiler(nil)
	whenExpr := &ast.WhenExpr{
		Subject: &ast.Ident{Name: "status"},
		Branches: []*ast.WhenBranch{
			{Condition: &ast.Literal{Kind: token.String, Value: "ACTIVE"}, Body: &ast.Literal{Kind: token.Int, Value: "1"}},
			{Condition: &ast.Literal{Kind: token.String, Value: "INACTIVE"}, Body: &ast.Literal{Kind: token.Int, Value: "0"}},
		},
		Else: &ast.Literal{Kind: token.Int, Value: "-1"},
	}
	c.compileStmt(&ast.ValStmt{Name: "code", Value: whenExpr})
	out := compilerOut(c)
	if !strings.Contains(out, "code :=") {
		t.Fatalf("missing code :=, got:\n%s", out)
	}
	if !strings.Contains(out, "switch status") {
		t.Fatalf("missing switch, got:\n%s", out)
	}
	if !strings.Contains(out, `case "ACTIVE"`) {
		t.Fatalf("missing case ACTIVE, got:\n%s", out)
	}
	if !strings.Contains(out, `case "INACTIVE"`) {
		t.Fatalf("missing case INACTIVE, got:\n%s", out)
	}
	if !strings.Contains(out, "default:") {
		t.Fatalf("missing default, got:\n%s", out)
	}
}

// TestCompileTemplateStringComplex — template with expressions
func TestCompileTemplateStringComplex(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.TemplateString{Parts: []ast.Expr{
		&ast.Literal{Kind: token.String, Value: "User "},
		&ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "name"},
		&ast.Literal{Kind: token.String, Value: " has "},
		&ast.Ident{Name: "count"},
		&ast.Literal{Kind: token.String, Value: " posts"},
	}})
	if !strings.Contains(got, `"User "`) {
		t.Fatalf("missing literal part, got:\n%s", got)
	}
	if !strings.Contains(got, "_sb.WriteString(") {
		t.Fatalf("missing _sb.WriteString calls, got:\n%s", got)
	}
	if !strings.Contains(got, "user.Name") {
		t.Fatalf("missing user.Name, got:\n%s", got)
	}
	if !strings.Contains(got, "count") {
		t.Fatalf("missing count, got:\n%s", got)
	}
	if !strings.Contains(got, `" posts"`) {
		t.Fatalf("missing posts literal, got:\n%s", got)
	}
	// Check strings.Builder pattern
	if !strings.Contains(got, "strings.Builder") {
		t.Fatalf("missing strings.Builder, got:\n%s", got)
	}
}

// TestCompileListWithExpressions — [user.id, user.id + 1, 42]
func TestCompileListWithExpressions(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.ListExpr{Items: []ast.Expr{
		&ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "id"},
		&ast.BinaryExpr{
			Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "id"},
			Op:    "+",
			Right: &ast.Literal{Kind: token.Int, Value: "1"},
		},
		&ast.Literal{Kind: token.Int, Value: "42"},
	}})
	if !strings.Contains(got, "[]any{") {
		t.Fatalf("missing []any{, got:\n%s", got)
	}
	if !strings.Contains(got, "user.Id") {
		t.Fatalf("missing user.Id, got:\n%s", got)
	}
	if !strings.Contains(got, "user.Id + 1") {
		t.Fatalf("missing user.Id + 1, got:\n%s", got)
	}
	if !strings.Contains(got, "42") {
		t.Fatalf("missing 42, got:\n%s", got)
	}
}

// TestCompileAssignCompound — count += 1, total -= price
func TestCompileAssignCompound(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.AssignStmt{
		Target: &ast.Ident{Name: "count"},
		Op:     "+=",
		Value:  &ast.Literal{Kind: token.Int, Value: "1"},
	})
	c.compileStmt(&ast.AssignStmt{
		Target: &ast.Ident{Name: "total"},
		Op:     "-=",
		Value:  &ast.Ident{Name: "price"},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "count += 1") {
		t.Fatalf("missing count += 1, got:\n%s", out)
	}
	if !strings.Contains(out, "total -= price") {
		t.Fatalf("missing total -= price, got:\n%s", out)
	}
}

// TestCompileTransactionWithModelOps — tx { create + update }
func TestCompileTransactionWithModelOps(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)
	got := c.compileExpr(&ast.TransactionExpr{
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ValStmt{
				Name: "user",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "create"},
					Args: []*ast.NamedArg{
						{Name: "name", Value: &ast.Literal{Kind: token.String, Value: "lin"}},
					},
				},
			},
			&ast.AssignStmt{
				Target: &ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "active"},
				Op:     "=",
				Value:  &ast.Literal{Kind: token.True, Value: "true"},
			},
		}},
	})
	if !strings.Contains(got, "app.DB.Tx(ctx, func(ctx context.Context) error {") {
		t.Fatalf("missing Tx wrapper, got:\n%s", got)
	}
	if !strings.Contains(got, "app.User.Create()") {
		t.Fatalf("missing Create(), got:\n%s", got)
	}
	if !strings.Contains(got, `.SetName("lin")`) {
		t.Fatalf("missing SetName, got:\n%s", got)
	}
	if !strings.Contains(got, "user.Active = true") {
		t.Fatalf("missing assignment, got:\n%s", got)
	}
	if !strings.Contains(got, "return nil") {
		t.Fatalf("missing return nil, got:\n%s", got)
	}
}

// TestCompileAsyncFireAndForget — async { emit ... }
func TestCompileAsyncFireAndForget(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.AsyncExpr{
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.EmitStmt{
				EventName: "EmailSent",
				Args: []*ast.NamedArg{
					{Name: "to", Value: &ast.Ident{Name: "email"}},
				},
			},
		}},
	})
	if !strings.Contains(got, "go func() {") {
		t.Fatalf("missing go func(), got:\n%s", got)
	}
	if !strings.Contains(got, "EmitEmailSent") {
		t.Fatalf("missing EmitEmailSent, got:\n%s", got)
	}
	if !strings.Contains(got, "EmailSentEvent{") {
		t.Fatalf("missing EmailSentEvent{, got:\n%s", got)
	}
	if !strings.Contains(got, "To: email") {
		t.Fatalf("missing To: email, got:\n%s", got)
	}
}

// TestCompileAwaitParallelQueries — await with multiple model queries → errgroup
func TestCompileAwaitParallelQueries(t *testing.T) {
	models := map[string]*ast.ModelDecl{
		"User": {Name: "User"},
		"Post": {Name: "Post"},
	}
	c := newCompiler(models)
	// await { val user = User.find(id); val posts = Post.where(...).all() }
	c.compileExpr(&ast.AwaitExpr{
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ValStmt{
				Name: "user",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "find"},
					Args: []*ast.NamedArg{{Name: "id", Value: &ast.Ident{Name: "id"}}},
				},
			},
			&ast.ValStmt{
				Name: "posts",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{
						Object: &ast.CallExpr{
							Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Post"}, Field: "where"},
							Args: []*ast.NamedArg{},
						},
						Field: "all",
					},
					Args: []*ast.NamedArg{},
				},
			},
		}},
	})
	out := compilerOut(c)
	// Should declare vars in outer scope
	if !strings.Contains(out, "var user *User") {
		t.Fatalf("missing var user *User, got:\n%s", out)
	}
	if !strings.Contains(out, "var posts []*Post") {
		t.Fatalf("missing var posts []*Post, got:\n%s", out)
	}
	// Should use errgroup
	if !strings.Contains(out, "errgroup.WithContext(ctx)") {
		t.Fatalf("missing errgroup, got:\n%s", out)
	}
	if !strings.Contains(out, "g.Go(func() error {") {
		t.Fatalf("missing g.Go, got:\n%s", out)
	}
	// Should use gctx instead of ctx inside goroutines
	if !strings.Contains(out, "gctx") {
		t.Fatalf("missing gctx, got:\n%s", out)
	}
	// Should wait and check error
	if !strings.Contains(out, "g.Wait()") {
		t.Fatalf("missing g.Wait(), got:\n%s", out)
	}
	// Vars should be tracked
	if vt, ok := c.vars["user"]; !ok || !vt.isModel {
		t.Error("user var not tracked as model")
	}
	if vt, ok := c.vars["posts"]; !ok || !vt.isList {
		t.Error("posts var not tracked as list")
	}
}

// TestCompileAwaitNoVals — await with no val statements → sequential
func TestCompileAwaitNoVals(t *testing.T) {
	c := newCompiler(nil)
	c.compileExpr(&ast.AwaitExpr{
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ExprStmt{Expr: &ast.Ident{Name: "doSomething"}},
		}},
	})
	out := compilerOut(c)
	if strings.Contains(out, "errgroup") {
		t.Fatalf("should not use errgroup for non-val stmts, got:\n%s", out)
	}
	if !strings.Contains(out, "doSomething") {
		t.Fatalf("missing sequential stmt, got:\n%s", out)
	}
}

// TestCompileAwaitNonQueryVal — await with non-model-query val → no error handling
func TestCompileAwaitNonQueryVal(t *testing.T) {
	c := newCompiler(nil)
	c.compileExpr(&ast.AwaitExpr{
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ValStmt{
				Name:  "x",
				Value: &ast.Literal{Kind: token.Int, Value: "42"},
			},
		}},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "var x any") {
		t.Fatalf("missing var declaration, got:\n%s", out)
	}
	if !strings.Contains(out, "x = 42") {
		t.Fatalf("missing assignment, got:\n%s", out)
	}
	if !strings.Contains(out, "return nil") {
		t.Fatalf("missing return nil in non-query task, got:\n%s", out)
	}
}

// TestCompileElvisWithThrow — user ?: throw error.NotFound
func TestCompileElvisWithThrow(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ExprStmt{
		Expr: &ast.ElvisExpr{
			Left: &ast.Ident{Name: "user"},
			Right: &ast.MemberExpr{
				Object: &ast.Ident{Name: "error"},
				Field:  "notFound",
			},
		},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "if user == nil {") {
		t.Fatalf("missing nil check, got:\n%s", out)
	}
	if !strings.Contains(out, "return errors.NotFound") {
		t.Fatalf("missing return errors.NotFound, got:\n%s", out)
	}
}

// TestCompileChainedModelOps — User.where(...).select(...).first()
func TestCompileChainedModelOps(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	// User.where(it.id == id).select(...).first()
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
	firstCall := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: selectCall, Field: "first"},
		Args: nil,
	}
	got := c.compileExpr(firstCall)
	if !strings.Contains(got, "app.User.Where(UserWhere.Id.Eq(id))") {
		t.Fatalf("missing where clause, got:\n%s", got)
	}
	if !strings.Contains(got, ".Select(selection.SQLColumns(req.Select)...)") {
		t.Fatalf("missing select, got:\n%s", got)
	}
	if !strings.Contains(got, ".First(ctx)") {
		t.Fatalf("missing .First(ctx), got:\n%s", got)
	}
}

// TestCompileModelCountExpr — User.where(...).count()
func TestCompileModelCountExpr(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	got := c.compileExpr(&ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
				Args: []*ast.NamedArg{{
					Value: &ast.BinaryExpr{
						Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "active"},
						Op:    "==",
						Right: &ast.Literal{Kind: token.True, Value: "true"},
					},
				}},
			},
			Field: "count",
		},
	})
	if !strings.Contains(got, "app.User.Where(UserWhere.Active.Eq(true))") {
		t.Fatalf("missing where clause, got:\n%s", got)
	}
	if !strings.Contains(got, ".Count(ctx)") {
		t.Fatalf("missing .Count(ctx), got:\n%s", got)
	}
}

// TestCompileModelDeleteExpr — User.where(...).delete()
func TestCompileModelDeleteExpr(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	got := c.compileExpr(&ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
				Args: []*ast.NamedArg{{
					Value: &ast.BinaryExpr{
						Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "id"},
						Op:    "==",
						Right: &ast.Literal{Kind: token.Int, Value: "5"},
					},
				}},
			},
			Field: "delete",
		},
	})
	if !strings.Contains(got, "app.User.Where(UserWhere.Id.Eq(5))") {
		t.Fatalf("missing where clause, got:\n%s", got)
	}
	if !strings.Contains(got, ".Delete(ctx)") {
		t.Fatalf("missing .Delete(ctx), got:\n%s", got)
	}
}

// TestCompileModelUpdateExpr — User.where(...).update(name: "new")
func TestCompileModelUpdateExpr(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)

	got := c.compileExpr(&ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
				Args: []*ast.NamedArg{{
					Value: &ast.BinaryExpr{
						Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "id"},
						Op:    "==",
						Right: &ast.Ident{Name: "id"},
					},
				}},
			},
			Field: "update",
		},
		Args: []*ast.NamedArg{
			{Name: "name", Value: &ast.Literal{Kind: token.String, Value: "new"}},
		},
	})
	if !strings.Contains(got, "app.User.Where(UserWhere.Id.Eq(id))") {
		t.Fatalf("missing where clause, got:\n%s", got)
	}
	if !strings.Contains(got, `.Update(ctx, lux.SetField{Col: "name"`) {
		t.Fatalf("missing .Update with SetField, got:\n%s", got)
	}
}

// TestCompileNestedIf — if inside if
func TestCompileNestedIf(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.IfStmt{
		Condition: &ast.BinaryExpr{
			Left:  &ast.Ident{Name: "x"},
			Op:    ">",
			Right: &ast.Literal{Kind: token.Int, Value: "0"},
		},
		Then: &ast.Block{Stmts: []ast.Stmt{
			&ast.IfStmt{
				Condition: &ast.BinaryExpr{
					Left:  &ast.Ident{Name: "x"},
					Op:    "<",
					Right: &ast.Literal{Kind: token.Int, Value: "100"},
				},
				Then: &ast.Block{Stmts: []ast.Stmt{
					&ast.ExprStmt{Expr: &ast.CallExpr{
						Func: &ast.Ident{Name: "doWork"},
						Args: nil,
					}},
				}},
			},
		}},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "if x > 0 {") {
		t.Fatalf("missing outer if, got:\n%s", out)
	}
	if !strings.Contains(out, "if x < 100 {") {
		t.Fatalf("missing inner if, got:\n%s", out)
	}
	if !strings.Contains(out, "doWork()") {
		t.Fatalf("missing doWork(), got:\n%s", out)
	}
}

// TestCompileIfWithElvisGuard — if + elvis in same body
func TestCompileIfWithElvisGuard(t *testing.T) {
	c := newCompiler(nil)
	// First: elvis guard
	c.compileStmt(&ast.ExprStmt{
		Expr: &ast.ElvisExpr{
			Left:  &ast.Ident{Name: "data"},
			Right: &ast.MemberExpr{Object: &ast.Ident{Name: "error"}, Field: "badRequest"},
		},
	})
	// Then: if check
	c.compileStmt(&ast.IfStmt{
		Condition: &ast.BinaryExpr{
			Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "data"}, Field: "valid"},
			Op:    "==",
			Right: &ast.Literal{Kind: token.True, Value: "true"},
		},
		Then: &ast.Block{Stmts: []ast.Stmt{
			&ast.ReturnStmt{Value: &ast.Ident{Name: "data"}},
		}},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "if data == nil {") {
		t.Fatalf("missing elvis guard, got:\n%s", out)
	}
	if !strings.Contains(out, "return errors.BadRequest") {
		t.Fatalf("missing throw, got:\n%s", out)
	}
	if !strings.Contains(out, "if data.Valid == true {") {
		t.Fatalf("missing if condition, got:\n%s", out)
	}
}

// TestCompileObjectExprFields — { name: "lin", age: 18, active: true }
func TestCompileObjectExprFields(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.ObjectExpr{Fields: []*ast.NamedArg{
		{Name: "name", Value: &ast.Literal{Kind: token.String, Value: "lin"}},
		{Name: "age", Value: &ast.Literal{Kind: token.Int, Value: "18"}},
		{Name: "active", Value: &ast.Literal{Kind: token.True, Value: "true"}},
	}})
	if !strings.Contains(got, `Name: "lin"`) {
		t.Fatalf("missing Name field, got:\n%s", got)
	}
	if !strings.Contains(got, "Age: 18") {
		t.Fatalf("missing Age field, got:\n%s", got)
	}
	if !strings.Contains(got, "Active: true") {
		t.Fatalf("missing Active field, got:\n%s", got)
	}
}

// TestCompileReturnList — return [1, 2, 3]
func TestCompileReturnList(t *testing.T) {
	c := newCompiler(nil)
	c.api = &ast.ApiDecl{Name: "getIds", ReturnType: &ast.TypeRef{Name: "Int", IsList: true}}
	c.compileStmt(&ast.ReturnStmt{
		Value: &ast.ListExpr{Items: []ast.Expr{
			&ast.Literal{Kind: token.Int, Value: "1"},
			&ast.Literal{Kind: token.Int, Value: "2"},
			&ast.Literal{Kind: token.Int, Value: "3"},
		}},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "[]any{1, 2, 3}") {
		t.Fatalf("missing list literal, got:\n%s", out)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// C. Edge cases
// ═══════════════════════════════════════════════════════════════════════════════

// TestCompileEmptyBody — API with no statements
func TestCompileEmptyBody(t *testing.T) {
	api := &ast.ApiDecl{
		Name:   "noop",
		Params: nil,
		Body:   &ast.Block{Stmts: nil},
	}
	var b strings.Builder
	compileAPIBody(&b, api, nil, nil)
	out := b.String()
	if !strings.Contains(out, "func handleNoop(app *App) api.HandlerFunc") {
		t.Fatalf("missing function signature, got:\n%s", out)
	}
	if !strings.Contains(out, "return func(ctx context.Context, req *api.Request) error {") {
		t.Fatalf("missing inner func, got:\n%s", out)
	}
}

// TestCompileMultipleReturns — early return + final return
func TestCompileMultipleReturns(t *testing.T) {
	c := newCompiler(nil)
	c.api = &ast.ApiDecl{Name: "check", ReturnType: &ast.TypeRef{Name: "Boolean"}}
	// if x == 0 { return false }
	c.compileStmt(&ast.IfStmt{
		Condition: &ast.BinaryExpr{
			Left:  &ast.Ident{Name: "x"},
			Op:    "==",
			Right: &ast.Literal{Kind: token.Int, Value: "0"},
		},
		Then: &ast.Block{Stmts: []ast.Stmt{
			&ast.ReturnStmt{Value: &ast.Literal{Kind: token.False, Value: "false"}},
		}},
	})
	// return true
	c.compileStmt(&ast.ReturnStmt{Value: &ast.Literal{Kind: token.True, Value: "true"}})
	out := compilerOut(c)
	if !strings.Contains(out, "if x == 0 {") {
		t.Fatalf("missing if, got:\n%s", out)
	}
	if !strings.Contains(out, "codec.AppendBool(req.Buf.B, false)") {
		t.Fatalf("missing early return false, got:\n%s", out)
	}
	if !strings.Contains(out, "codec.AppendBool(req.Buf.B, true)") {
		t.Fatalf("missing final return true, got:\n%s", out)
	}
}

// TestCompileForEmptyBody — for with empty body
func TestCompileForEmptyBody(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ForStmt{
		VarName:    "x",
		Collection: &ast.Ident{Name: "items"},
		Body:       &ast.Block{Stmts: nil},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "for _, x := range items {") {
		t.Fatalf("missing for range, got:\n%s", out)
	}
	if !strings.Contains(out, "}") {
		t.Fatalf("missing closing brace, got:\n%s", out)
	}
}

// TestCompileTemplateStringEmpty — empty template
func TestCompileTemplateStringEmpty(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.TemplateString{Parts: nil})
	if got != `""` {
		t.Fatalf("expected empty string, got %q", got)
	}
}

// TestCompileAssignSimple — x = 5
func TestCompileAssignSimple(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.AssignStmt{
		Target: &ast.Ident{Name: "x"},
		Op:     "=",
		Value:  &ast.Literal{Kind: token.Int, Value: "5"},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "x = 5") {
		t.Fatalf("missing x = 5, got:\n%s", out)
	}
}

// TestCompileParallelProfileAPI — full API with await for parallel queries
func TestCompileParallelProfileAPI(t *testing.T) {
	models := map[string]*ast.ModelDecl{
		"User": {Name: "User", Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		}},
		"Post": {Name: "Post", Fields: []*ast.FieldDecl{
			{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
		}},
	}

	api := &ast.ApiDecl{
		Name:       "getUserProfile",
		ReturnType: &ast.TypeRef{Name: "User"},
		Params: []*ast.ParamDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{
			// await { val user = User.find(id); val posts = Post.where(...).all() }
			&ast.ExprStmt{Expr: &ast.AwaitExpr{
				Body: &ast.Block{Stmts: []ast.Stmt{
					&ast.ValStmt{
						Name: "user",
						Value: &ast.CallExpr{
							Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "find"},
							Args: []*ast.NamedArg{{Name: "id", Value: &ast.Ident{Name: "id"}}},
						},
					},
					&ast.ValStmt{
						Name: "posts",
						Value: &ast.CallExpr{
							Func: &ast.MemberExpr{
								Object: &ast.CallExpr{
									Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Post"}, Field: "where"},
									Args: []*ast.NamedArg{},
								},
								Field: "all",
							},
							Args: []*ast.NamedArg{},
						},
					},
				}},
			}},
			// return user
			&ast.ReturnStmt{Value: &ast.Ident{Name: "user"}},
		}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, models, nil)
	out := b.String()

	// Should have function signature
	if !strings.Contains(out, "func handleGetUserProfile") {
		t.Fatalf("missing handler func, got:\n%s", out)
	}
	// Should have param extraction
	if !strings.Contains(out, "req.ParamInt") {
		t.Fatalf("missing ParamInt, got:\n%s", out)
	}
	// Should have var declarations
	if !strings.Contains(out, "var user *User") {
		t.Fatalf("missing var user, got:\n%s", out)
	}
	if !strings.Contains(out, "var posts []*Post") {
		t.Fatalf("missing var posts, got:\n%s", out)
	}
	// Should have errgroup
	if !strings.Contains(out, "errgroup.WithContext") {
		t.Fatalf("missing errgroup, got:\n%s", out)
	}
	// Should use gctx in goroutines
	if !strings.Contains(out, "gctx") {
		t.Fatalf("missing gctx, got:\n%s", out)
	}
	// Should have WriteLuxo for return
	if !strings.Contains(out, "WriteLuxo") {
		t.Fatalf("missing WriteLuxo, got:\n%s", out)
	}
}

// TestCompileForRangeExpr — for i in 0..10 { ... } → C-style for loop
func TestCompileForRangeExpr(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ForStmt{
		VarName: "i",
		Collection: &ast.RangeExpr{
			Start: &ast.Literal{Kind: token.Int, Value: "0"},
			End:   &ast.Literal{Kind: token.Int, Value: "10"},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ExprStmt{Expr: &ast.CallExpr{
				Func: &ast.Ident{Name: "process"},
				Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "i"}}},
			}},
		}},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "for i := int64(0); i <= 10; i++") {
		t.Fatalf("expected C-style for loop, got:\n%s", out)
	}
	if !strings.Contains(out, "process(i)") {
		t.Fatalf("missing body, got:\n%s", out)
	}
}

// TestCompileForRangeWithExprBounds — for i in start..end
func TestCompileForRangeWithExprBounds(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ForStmt{
		VarName: "idx",
		Collection: &ast.RangeExpr{
			Start: &ast.Ident{Name: "offset"},
			End:   &ast.BinaryExpr{Left: &ast.Ident{Name: "offset"}, Op: "+", Right: &ast.Ident{Name: "limit"}},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{}},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "for idx := int64(offset); idx <= offset + limit; idx++") {
		t.Fatalf("expected range with expr bounds, got:\n%s", out)
	}
}

// TestCompileForAsExpression — val doubled = for item in items { item * 2 }
func TestCompileForAsExpression(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.ForStmt{
		VarName:    "item",
		Collection: &ast.Ident{Name: "items"},
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ExprStmt{Expr: &ast.BinaryExpr{
				Left:  &ast.Ident{Name: "item"},
				Op:    "*",
				Right: &ast.Literal{Kind: token.Int, Value: "2"},
			}},
		}},
	})
	if !strings.Contains(got, "var _result []any") {
		t.Fatalf("missing result slice, got:\n%s", got)
	}
	if !strings.Contains(got, "for _, item := range items") {
		t.Fatalf("missing range loop, got:\n%s", got)
	}
	if !strings.Contains(got, "item * 2") {
		t.Fatalf("missing collected expression, got:\n%s", got)
	}
	if !strings.Contains(got, "_result = append(_result,") {
		t.Fatalf("missing append, got:\n%s", got)
	}
}

// TestCompileForExprWithRange — val squares = for i in 0..5 { i * i }
func TestCompileForExprWithRange(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.ForStmt{
		VarName: "i",
		Collection: &ast.RangeExpr{
			Start: &ast.Literal{Kind: token.Int, Value: "0"},
			End:   &ast.Literal{Kind: token.Int, Value: "5"},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ExprStmt{Expr: &ast.BinaryExpr{
				Left:  &ast.Ident{Name: "i"},
				Op:    "*",
				Right: &ast.Ident{Name: "i"},
			}},
		}},
	})
	if !strings.Contains(got, "for i := int64(0); i <= 5; i++") {
		t.Fatalf("expected C-style range in for expr, got:\n%s", got)
	}
	if !strings.Contains(got, "i * i") {
		t.Fatalf("missing expression, got:\n%s", got)
	}
}

// TestCompileForExprEmpty — for in empty → []any{}
func TestCompileForExprEmpty(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.ForStmt{
		VarName:    "x",
		Collection: &ast.Ident{Name: "items"},
		Body:       &ast.Block{Stmts: nil},
	})
	if got != "[]any{}" {
		t.Fatalf("expected []any{}, got %q", got)
	}
}

// TestCompileIntRange — lux.IntRange runtime function
func TestIntRange(t *testing.T) {
	result := lux.IntRange(0, 5)
	if len(result) != 6 {
		t.Fatalf("IntRange(0,5) len = %d, want 6", len(result))
	}
	if result[0] != 0 || result[5] != 5 {
		t.Errorf("IntRange(0,5) = %v", result)
	}
}

func TestIntRangeReverse(t *testing.T) {
	result := lux.IntRange(10, 5)
	if result != nil {
		t.Errorf("IntRange(10,5) should be nil, got %v", result)
	}
}

func TestIntRangeSingle(t *testing.T) {
	result := lux.IntRange(3, 3)
	if len(result) != 1 || result[0] != 3 {
		t.Errorf("IntRange(3,3) = %v, want [3]", result)
	}
}

// === ? operator tests ===

func TestCompileValWithQuestion(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ValStmt{
		Name: "x",
		Value: &ast.UnaryExpr{Op: "?", Value: &ast.CallExpr{
			Func: &ast.Ident{Name: "riskyCall"}, Args: []*ast.NamedArg{},
		}},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "x, err := riskyCall()") {
		t.Fatalf("missing err assignment, got:\n%s", out)
	}
	if !strings.Contains(out, "return err") {
		t.Fatalf("missing error propagation, got:\n%s", out)
	}
}

func TestCompileExprStmtWithQuestion(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ExprStmt{Expr: &ast.UnaryExpr{Op: "?", Value: &ast.CallExpr{
		Func: &ast.Ident{Name: "doSomething"}, Args: []*ast.NamedArg{},
	}}})
	out := compilerOut(c)
	if !strings.Contains(out, "if err := doSomething()") {
		t.Fatalf("missing inline err check, got:\n%s", out)
	}
}

// === New chain methods ===

func TestCompileChainOrderByDesc(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)
	call := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{
					Object: &ast.CallExpr{
						Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
						Args: []*ast.NamedArg{},
					},
					Field: "orderBy",
				},
				Args: []*ast.NamedArg{{Value: &ast.MemberExpr{Object: &ast.Ident{Name: "name"}, Field: "desc"}}},
			},
			Field: "all",
		},
		Args: []*ast.NamedArg{},
	}
	got := c.compileExpr(call)
	if !strings.Contains(got, `.OrderBy("name DESC")`) {
		t.Fatalf("expected OrderBy DESC, got %q", got)
	}
}

func TestCompileChainLimitAll(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)
	call := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{
					Object: &ast.CallExpr{
						Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
						Args: []*ast.NamedArg{},
					},
					Field: "limit",
				},
				Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Int, Value: "10"}}},
			},
			Field: "all",
		},
		Args: []*ast.NamedArg{},
	}
	got := c.compileExpr(call)
	if !strings.Contains(got, ".Limit(10)") {
		t.Fatalf("expected Limit, got %q", got)
	}
}

func TestCompileModelChainSum(t *testing.T) {
	models := makeModels("Order")
	c := newCompiler(models)
	call := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Order"}, Field: "where"},
				Args: []*ast.NamedArg{},
			},
			Field: "sum",
		},
		Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "total"}}},
	}
	got := c.compileExpr(call)
	if !strings.Contains(got, `.Sum(ctx, "total")`) {
		t.Fatalf("expected Sum, got %q", got)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// D. Real-world mixed scenarios (full compileAPIBody)
// ═══════════════════════════════════════════════════════════════════════════════

// TestCompileTransferAPI — Bank transfer with tx + await + error check:
//
//	api transfer(fromId: Int, toId: Int, amount: Float) {
//	  val from = User.find(fromId)
//	  from ?: throw error.NotFound
//	  tx {
//	    val balance = getBalance(from)?
//	    if balance < amount {
//	      throw error.InsufficientFunds
//	    }
//	    from.balance -= amount
//	  }
//	  emit TransferCompleted(fromId: fromId, toId: toId, amount: amount)
//	}
func TestCompileTransferAPI(t *testing.T) {
	models := makeModels("User")
	api := &ast.ApiDecl{
		Name: "transfer",
		Params: []*ast.ParamDecl{
			{Name: "fromId", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "toId", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "amount", Type: &ast.TypeRef{Name: "Float"}},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{
			// val from = User.find(fromId)
			&ast.ValStmt{
				Name: "from",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "find"},
					Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "fromId"}}},
				},
			},
			// from ?: throw error.NotFound
			&ast.ExprStmt{
				Expr: &ast.ElvisExpr{
					Left:  &ast.Ident{Name: "from"},
					Right: &ast.MemberExpr{Object: &ast.Ident{Name: "error"}, Field: "notFound"},
				},
			},
			// tx { val balance = getBalance(from)?; if balance < amount { throw error.InsufficientFunds }; from.balance -= amount }
			&ast.ExprStmt{
				Expr: &ast.TransactionExpr{
					Body: &ast.Block{Stmts: []ast.Stmt{
						&ast.ValStmt{
							Name: "balance",
							Value: &ast.UnaryExpr{
								Op: "?",
								Value: &ast.CallExpr{
									Func: &ast.Ident{Name: "getBalance"},
									Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "from"}}},
								},
							},
						},
						&ast.IfStmt{
							Condition: &ast.BinaryExpr{
								Left:  &ast.Ident{Name: "balance"},
								Op:    "<",
								Right: &ast.Ident{Name: "amount"},
							},
							Then: &ast.Block{Stmts: []ast.Stmt{
								&ast.ThrowStmt{
									Error: &ast.MemberExpr{Object: &ast.Ident{Name: "error"}, Field: "insufficientFunds"},
								},
							}},
						},
						&ast.AssignStmt{
							Target: &ast.MemberExpr{Object: &ast.Ident{Name: "from"}, Field: "balance"},
							Op:     "-=",
							Value:  &ast.Ident{Name: "amount"},
						},
					}},
				},
			},
			// emit TransferCompleted(fromId: fromId, toId: toId, amount: amount)
			&ast.EmitStmt{
				EventName: "TransferCompleted",
				Args: []*ast.NamedArg{
					{Name: "fromId", Value: &ast.Ident{Name: "fromId"}},
					{Name: "toId", Value: &ast.Ident{Name: "toId"}},
					{Name: "amount", Value: &ast.Ident{Name: "amount"}},
				},
			},
		}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, models, nil)
	out := b.String()

	// Check function signature
	if !strings.Contains(out, "func handleTransfer(app *App) api.HandlerFunc") {
		t.Fatalf("missing handleTransfer signature, got:\n%s", out)
	}
	// Check param extraction
	if !strings.Contains(out, `req.ParamInt("fromId")`) {
		t.Fatalf("missing ParamInt for fromId, got:\n%s", out)
	}
	if !strings.Contains(out, `req.ParamInt("toId")`) {
		t.Fatalf("missing ParamInt for toId, got:\n%s", out)
	}
	if !strings.Contains(out, `req.ParamFloat("amount")`) {
		t.Fatalf("missing ParamFloat for amount, got:\n%s", out)
	}
	// Check find
	if !strings.Contains(out, "app.User.Where(UserWhere.Id.Eq(fromId)).First(ctx)") {
		t.Fatalf("missing User.find, got:\n%s", out)
	}
	// Check elvis guard
	if !strings.Contains(out, "if from == nil {") {
		t.Fatalf("missing elvis nil check, got:\n%s", out)
	}
	if !strings.Contains(out, "return errors.NotFound") {
		t.Fatalf("missing throw NotFound, got:\n%s", out)
	}
	// Check transaction
	if !strings.Contains(out, "app.DB.Tx(ctx, func(ctx context.Context) error {") {
		t.Fatalf("missing Tx wrapper, got:\n%s", out)
	}
	// Check ? operator inside tx
	if !strings.Contains(out, "balance, err := getBalance(from)") {
		t.Fatalf("missing ? operator error propagation, got:\n%s", out)
	}
	// Check if condition
	if !strings.Contains(out, "if balance < amount {") {
		t.Fatalf("missing balance < amount check, got:\n%s", out)
	}
	// Check throw inside if
	if !strings.Contains(out, "return errors.InsufficientFunds") {
		t.Fatalf("missing throw InsufficientFunds, got:\n%s", out)
	}
	// Check compound assignment
	if !strings.Contains(out, "from.Balance -= amount") {
		t.Fatalf("missing from.balance -= amount, got:\n%s", out)
	}
	// Check emit
	if !strings.Contains(out, "EmitTransferCompleted") {
		t.Fatalf("missing EmitTransferCompleted, got:\n%s", out)
	}
	if !strings.Contains(out, "TransferCompletedEvent{") {
		t.Fatalf("missing TransferCompletedEvent, got:\n%s", out)
	}
	if !strings.Contains(out, "FromId: fromId") {
		t.Fatalf("missing FromId arg, got:\n%s", out)
	}
	if !strings.Contains(out, "ToId: toId") {
		t.Fatalf("missing ToId arg, got:\n%s", out)
	}
	if !strings.Contains(out, "Amount: amount") {
		t.Fatalf("missing Amount arg, got:\n%s", out)
	}
}

// TestCompileSearchAPI — Search with orderBy + limit + template string:
//
//	api searchPosts(keyword: String, limit: Int): [Post] {
//	  val posts = Post.where(it.title == keyword).orderBy(views.desc).limit(limit).all()
//	  return posts
//	}
func TestCompileSearchAPI(t *testing.T) {
	models := makeModels("Post")
	api := &ast.ApiDecl{
		Name: "searchPosts",
		Params: []*ast.ParamDecl{
			{Name: "keyword", Type: &ast.TypeRef{Name: "String"}},
			{Name: "limit", Type: &ast.TypeRef{Name: "Int"}},
		},
		ReturnType: &ast.TypeRef{Name: "Post", IsList: true},
		Body: &ast.Block{Stmts: []ast.Stmt{
			// val posts = Post.where(it.title == keyword).orderBy(views.desc).limit(limit).all()
			&ast.ValStmt{
				Name: "posts",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{
						Object: &ast.CallExpr{
							Func: &ast.MemberExpr{
								Object: &ast.CallExpr{
									Func: &ast.MemberExpr{
										Object: &ast.CallExpr{
											Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Post"}, Field: "where"},
											Args: []*ast.NamedArg{{
												Value: &ast.BinaryExpr{
													Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "title"},
													Op:    "==",
													Right: &ast.Ident{Name: "keyword"},
												},
											}},
										},
										Field: "orderBy",
									},
									Args: []*ast.NamedArg{{Value: &ast.MemberExpr{Object: &ast.Ident{Name: "views"}, Field: "desc"}}},
								},
								Field: "limit",
							},
							Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "limit"}}},
						},
						Field: "all",
					},
				},
			},
			// return posts
			&ast.ReturnStmt{Value: &ast.Ident{Name: "posts"}},
		}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, models, nil)
	out := b.String()

	if !strings.Contains(out, "func handleSearchPosts(app *App) api.HandlerFunc") {
		t.Fatalf("missing function signature, got:\n%s", out)
	}
	if !strings.Contains(out, `req.ParamString("keyword")`) {
		t.Fatalf("missing ParamString for keyword, got:\n%s", out)
	}
	if !strings.Contains(out, `req.ParamInt("limit")`) {
		t.Fatalf("missing ParamInt for limit, got:\n%s", out)
	}
	if !strings.Contains(out, "PostWhere.Title.Eq(keyword)") {
		t.Fatalf("missing where clause, got:\n%s", out)
	}
	if !strings.Contains(out, `.OrderBy("views DESC")`) {
		t.Fatalf("missing OrderBy DESC, got:\n%s", out)
	}
	if !strings.Contains(out, ".Limit(limit)") {
		t.Fatalf("missing .Limit(limit), got:\n%s", out)
	}
	if !strings.Contains(out, ".All(ctx)") {
		t.Fatalf("missing .All(ctx), got:\n%s", out)
	}
	// Check return — posts is list model → listJSON
	if !strings.Contains(out, "WriteColumnar") {
		t.Fatalf("missing WriteColumnar, got:\n%s", out)
	}
}

// TestCompileAnalyticsAPI — Aggregation + count + sum:
//
//	api getAnalytics(): Int {
//	  val total = Order.where(it.status == "PAID").count()
//	  val revenue = Order.where(it.status == "PAID").sum(amount)
//	  return total
//	}
func TestCompileAnalyticsAPI(t *testing.T) {
	models := makeModels("Order")
	api := &ast.ApiDecl{
		Name:       "getAnalytics",
		Params:     nil,
		ReturnType: &ast.TypeRef{Name: "Int"},
		Body: &ast.Block{Stmts: []ast.Stmt{
			// val total = Order.where(it.status == "PAID").count()
			&ast.ValStmt{
				Name: "total",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{
						Object: &ast.CallExpr{
							Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Order"}, Field: "where"},
							Args: []*ast.NamedArg{{
								Value: &ast.BinaryExpr{
									Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "status"},
									Op:    "==",
									Right: &ast.Literal{Kind: token.String, Value: "PAID"},
								},
							}},
						},
						Field: "count",
					},
				},
			},
			// val revenue = Order.where(it.status == "PAID").sum(amount)
			&ast.ValStmt{
				Name: "revenue",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{
						Object: &ast.CallExpr{
							Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Order"}, Field: "where"},
							Args: []*ast.NamedArg{{
								Value: &ast.BinaryExpr{
									Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "status"},
									Op:    "==",
									Right: &ast.Literal{Kind: token.String, Value: "PAID"},
								},
							}},
						},
						Field: "sum",
					},
					Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "amount"}}},
				},
			},
			// return total
			&ast.ReturnStmt{Value: &ast.Ident{Name: "total"}},
		}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, models, nil)
	out := b.String()

	if !strings.Contains(out, "func handleGetAnalytics(app *App) api.HandlerFunc") {
		t.Fatalf("missing function signature, got:\n%s", out)
	}
	// Check count query
	if !strings.Contains(out, `OrderWhere.Status.Eq("PAID")`) {
		t.Fatalf("missing where status PAID, got:\n%s", out)
	}
	if !strings.Contains(out, ".Count(ctx)") {
		t.Fatalf("missing .Count(ctx), got:\n%s", out)
	}
	// Check sum query
	if !strings.Contains(out, `.Sum(ctx, "amount")`) {
		t.Fatalf("missing .Sum(ctx, amount), got:\n%s", out)
	}
	// Check return — total tracked as Int from count → AppendInt
	if !strings.Contains(out, "codec.AppendSvarint(req.Buf.B, int64(total))") {
		t.Fatalf("missing AppendInt(total), got:\n%s", out)
	}
}

// TestCompileAsyncNotificationAPI — async fire-and-forget + for loop:
//
//	api notifyAll(message: String) {
//	  val users = User.where(it.active == true).all()
//	  for user in users {
//	    async { emit Notification(userId: user.id, message: message) }
//	  }
//	}
func TestCompileAsyncNotificationAPI(t *testing.T) {
	models := makeModels("User")
	api := &ast.ApiDecl{
		Name: "notifyAll",
		Params: []*ast.ParamDecl{
			{Name: "message", Type: &ast.TypeRef{Name: "String"}},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{
			// val users = User.where(it.active == true).all()
			&ast.ValStmt{
				Name: "users",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{
						Object: &ast.CallExpr{
							Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
							Args: []*ast.NamedArg{{
								Value: &ast.BinaryExpr{
									Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "active"},
									Op:    "==",
									Right: &ast.Literal{Kind: token.True, Value: "true"},
								},
							}},
						},
						Field: "all",
					},
				},
			},
			// for user in users { async { emit Notification(...) } }
			&ast.ForStmt{
				VarName:    "user",
				Collection: &ast.Ident{Name: "users"},
				Body: &ast.Block{Stmts: []ast.Stmt{
					&ast.ExprStmt{Expr: &ast.AsyncExpr{
						Body: &ast.Block{Stmts: []ast.Stmt{
							&ast.EmitStmt{
								EventName: "Notification",
								Args: []*ast.NamedArg{
									{Name: "userId", Value: &ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "id"}},
									{Name: "message", Value: &ast.Ident{Name: "message"}},
								},
							},
						}},
					}},
				}},
			},
		}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, models, nil)
	out := b.String()

	if !strings.Contains(out, "func handleNotifyAll(app *App) api.HandlerFunc") {
		t.Fatalf("missing function signature, got:\n%s", out)
	}
	if !strings.Contains(out, `req.ParamString("message")`) {
		t.Fatalf("missing ParamString for message, got:\n%s", out)
	}
	if !strings.Contains(out, "UserWhere.Active.Eq(true)") {
		t.Fatalf("missing where active, got:\n%s", out)
	}
	if !strings.Contains(out, ".All(ctx)") {
		t.Fatalf("missing .All(ctx), got:\n%s", out)
	}
	if !strings.Contains(out, "for _, user := range users {") {
		t.Fatalf("missing for range, got:\n%s", out)
	}
	if !strings.Contains(out, "go func() {") {
		t.Fatalf("missing go func(), got:\n%s", out)
	}
	if !strings.Contains(out, "EmitNotification") {
		t.Fatalf("missing EmitNotification, got:\n%s", out)
	}
	if !strings.Contains(out, "UserId: user.Id") {
		t.Fatalf("missing UserId: user.Id, got:\n%s", out)
	}
	if !strings.Contains(out, "Message: message") {
		t.Fatalf("missing Message: message, got:\n%s", out)
	}
}

// TestCompileForRangeWithAccumulator — for in range + compound assign:
//
//	api sumRange(): Int {
//	  var total = 0
//	  for i in 1..100 { total += i }
//	  return total
//	}
func TestCompileForRangeWithAccumulator(t *testing.T) {
	api := &ast.ApiDecl{
		Name:       "sumRange",
		Params:     nil,
		ReturnType: &ast.TypeRef{Name: "Int"},
		Body: &ast.Block{Stmts: []ast.Stmt{
			// var total = 0
			&ast.ValStmt{
				Name:    "total",
				Value:   &ast.Literal{Kind: token.Int, Value: "0"},
				Mutable: true,
			},
			// for i in 1..100 { total += i }
			&ast.ForStmt{
				VarName: "i",
				Collection: &ast.RangeExpr{
					Start: &ast.Literal{Kind: token.Int, Value: "1"},
					End:   &ast.Literal{Kind: token.Int, Value: "100"},
				},
				Body: &ast.Block{Stmts: []ast.Stmt{
					&ast.AssignStmt{
						Target: &ast.Ident{Name: "total"},
						Op:     "+=",
						Value:  &ast.Ident{Name: "i"},
					},
				}},
			},
			// return total
			&ast.ReturnStmt{Value: &ast.Ident{Name: "total"}},
		}},
	}

	var b strings.Builder
	compileAPIBody(&b, api, nil, nil)
	out := b.String()

	if !strings.Contains(out, "func handleSumRange(app *App) api.HandlerFunc") {
		t.Fatalf("missing function signature, got:\n%s", out)
	}
	if !strings.Contains(out, "total := int64(0)") {
		t.Fatalf("missing total := 0, got:\n%s", out)
	}
	if !strings.Contains(out, "for i := int64(1); i <= 100; i++") {
		t.Fatalf("missing C-style for loop, got:\n%s", out)
	}
	if !strings.Contains(out, "total += i") {
		t.Fatalf("missing total += i, got:\n%s", out)
	}
	if !strings.Contains(out, "codec.AppendSvarint(req.Buf.B, int64(total))") {
		t.Fatalf("missing AppendInt(total), got:\n%s", out)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// E. Feature combination tests (newCompiler)
// ═══════════════════════════════════════════════════════════════════════════════

// TestCompileWhenWithModelQuery — when branches containing model queries
func TestCompileWhenWithModelQuery(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)
	got := c.compileExpr(&ast.WhenExpr{
		Subject: &ast.Ident{Name: "role"},
		Branches: []*ast.WhenBranch{
			{
				Condition: &ast.Literal{Kind: token.String, Value: "ADMIN"},
				Body: &ast.CallExpr{
					Func: &ast.MemberExpr{
						Object: &ast.CallExpr{
							Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
							Args: []*ast.NamedArg{{
								Value: &ast.BinaryExpr{
									Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "role"},
									Op:    "==",
									Right: &ast.Literal{Kind: token.String, Value: "ADMIN"},
								},
							}},
						},
						Field: "count",
					},
				},
			},
		},
		Else: &ast.Literal{Kind: token.Int, Value: "0"},
	})
	if !strings.Contains(got, "switch role") {
		t.Fatalf("missing switch, got:\n%s", got)
	}
	if !strings.Contains(got, `case "ADMIN"`) {
		t.Fatalf("missing case ADMIN, got:\n%s", got)
	}
	if !strings.Contains(got, ".Count(ctx)") {
		t.Fatalf("missing .Count(ctx) in when branch, got:\n%s", got)
	}
	if !strings.Contains(got, "default:") {
		t.Fatalf("missing default, got:\n%s", got)
	}
}

// TestCompileLambdaWithForLoop — lambda containing a for loop
func TestCompileLambdaWithForLoop(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.LambdaExpr{
		Params: []string{"items"},
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ForStmt{
				VarName:    "item",
				Collection: &ast.Ident{Name: "items"},
				Body: &ast.Block{Stmts: []ast.Stmt{
					&ast.ExprStmt{Expr: &ast.CallExpr{
						Func: &ast.Ident{Name: "process"},
						Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "item"}}},
					}},
				}},
			},
		}},
	})
	if !strings.Contains(got, "func(items any) any {") {
		t.Fatalf("missing lambda signature, got:\n%s", got)
	}
	if !strings.Contains(got, "for _, item := range items {") {
		t.Fatalf("missing for range inside lambda, got:\n%s", got)
	}
	if !strings.Contains(got, "process(item)") {
		t.Fatalf("missing process(item), got:\n%s", got)
	}
}

// TestCompileNestedTransaction — tx containing model create + update
func TestCompileNestedTransaction(t *testing.T) {
	models := makeModels("User", "Post")
	c := newCompiler(models)
	got := c.compileExpr(&ast.TransactionExpr{
		Body: &ast.Block{Stmts: []ast.Stmt{
			// val user = User.create(name: "alice")
			&ast.ValStmt{
				Name: "user",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "create"},
					Args: []*ast.NamedArg{{Name: "name", Value: &ast.Literal{Kind: token.String, Value: "alice"}}},
				},
			},
			// Post.where(it.userId == user.id).update(authorName: user.name)
			&ast.ExprStmt{
				Expr: &ast.CallExpr{
					Func: &ast.MemberExpr{
						Object: &ast.CallExpr{
							Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Post"}, Field: "where"},
							Args: []*ast.NamedArg{{
								Value: &ast.BinaryExpr{
									Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "userId"},
									Op:    "==",
									Right: &ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "id"},
								},
							}},
						},
						Field: "update",
					},
					Args: []*ast.NamedArg{
						{Name: "authorName", Value: &ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "name"}},
					},
				},
			},
		}},
	})
	if !strings.Contains(got, "app.DB.Tx(ctx, func(ctx context.Context) error {") {
		t.Fatalf("missing Tx wrapper, got:\n%s", got)
	}
	if !strings.Contains(got, "app.User.Create()") {
		t.Fatalf("missing User.Create(), got:\n%s", got)
	}
	if !strings.Contains(got, `.SetName("alice")`) {
		t.Fatalf("missing SetName, got:\n%s", got)
	}
	if !strings.Contains(got, "PostWhere.UserId.Eq(user.Id)") {
		t.Fatalf("missing where condition, got:\n%s", got)
	}
	if !strings.Contains(got, ".Update(ctx,") {
		t.Fatalf("missing Update, got:\n%s", got)
	}
	if !strings.Contains(got, `lux.SetField{Col: "author_name"`) {
		t.Fatalf("missing SetField for authorName, got:\n%s", got)
	}
}

// TestCompileForExprWithTransform — for as expr: val ids = for user in users { user.id }
func TestCompileForExprWithTransform(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.ForStmt{
		VarName:    "user",
		Collection: &ast.Ident{Name: "users"},
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ExprStmt{Expr: &ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "id"}},
		}},
	})
	if !strings.Contains(got, "var _result []any") {
		t.Fatalf("missing result slice, got:\n%s", got)
	}
	if !strings.Contains(got, "for _, user := range users") {
		t.Fatalf("missing range loop, got:\n%s", got)
	}
	if !strings.Contains(got, "user.Id") {
		t.Fatalf("missing user.Id, got:\n%s", got)
	}
	if !strings.Contains(got, "_result = append(_result,") {
		t.Fatalf("missing append, got:\n%s", got)
	}
}

// TestCompileAwaitWithThreeQueries — await with 3 parallel model queries
func TestCompileAwaitWithThreeQueries(t *testing.T) {
	models := map[string]*ast.ModelDecl{
		"User":  {Name: "User"},
		"Post":  {Name: "Post"},
		"Order": {Name: "Order"},
	}
	c := newCompiler(models)
	c.compileExpr(&ast.AwaitExpr{
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ValStmt{
				Name: "user",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "find"},
					Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "id"}}},
				},
			},
			&ast.ValStmt{
				Name: "posts",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{
						Object: &ast.CallExpr{
							Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Post"}, Field: "where"},
							Args: []*ast.NamedArg{},
						},
						Field: "all",
					},
				},
			},
			&ast.ValStmt{
				Name: "orderCount",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{
						Object: &ast.CallExpr{
							Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Order"}, Field: "where"},
							Args: []*ast.NamedArg{},
						},
						Field: "count",
					},
				},
			},
		}},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "var user *User") {
		t.Fatalf("missing var user, got:\n%s", out)
	}
	if !strings.Contains(out, "var posts []*Post") {
		t.Fatalf("missing var posts, got:\n%s", out)
	}
	if !strings.Contains(out, "var orderCount int64") {
		t.Fatalf("missing var orderCount int64, got:\n%s", out)
	}
	// Should have 3 g.Go calls
	count := strings.Count(out, "g.Go(func() error {")
	if count != 3 {
		t.Fatalf("expected 3 g.Go calls, got %d in:\n%s", count, out)
	}
	if !strings.Contains(out, "g.Wait()") {
		t.Fatalf("missing g.Wait(), got:\n%s", out)
	}
}

// TestCompileIfWithQuestionOperator — if + ? in same body
func TestCompileIfWithQuestionOperator(t *testing.T) {
	c := newCompiler(nil)
	// val result = riskyCall()?
	c.compileStmt(&ast.ValStmt{
		Name: "result",
		Value: &ast.UnaryExpr{Op: "?", Value: &ast.CallExpr{
			Func: &ast.Ident{Name: "riskyCall"},
			Args: nil,
		}},
	})
	// if result > 0 { return result }
	c.compileStmt(&ast.IfStmt{
		Condition: &ast.BinaryExpr{
			Left:  &ast.Ident{Name: "result"},
			Op:    ">",
			Right: &ast.Literal{Kind: token.Int, Value: "0"},
		},
		Then: &ast.Block{Stmts: []ast.Stmt{
			&ast.ReturnStmt{Value: &ast.Ident{Name: "result"}},
		}},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "result, err := riskyCall()") {
		t.Fatalf("missing ? err assignment, got:\n%s", out)
	}
	if !strings.Contains(out, "if err != nil") {
		t.Fatalf("missing err check, got:\n%s", out)
	}
	if !strings.Contains(out, "if result > 0 {") {
		t.Fatalf("missing if condition, got:\n%s", out)
	}
}

// TestCompileChainWhereOrderByLimitAll — full chain: where().orderBy().limit().offset().all()
func TestCompileChainWhereOrderByLimitAll(t *testing.T) {
	models := makeModels("Post")
	c := newCompiler(models)
	// Post.where(it.views > 100).orderBy(views.desc).limit(10).offset(20).all()
	whereCall := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Post"}, Field: "where"},
		Args: []*ast.NamedArg{{
			Value: &ast.BinaryExpr{
				Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "views"},
				Op:    ">",
				Right: &ast.Literal{Kind: token.Int, Value: "100"},
			},
		}},
	}
	orderByCall := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: whereCall, Field: "orderBy"},
		Args: []*ast.NamedArg{{Value: &ast.MemberExpr{Object: &ast.Ident{Name: "views"}, Field: "desc"}}},
	}
	limitCall := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: orderByCall, Field: "limit"},
		Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Int, Value: "10"}}},
	}
	offsetCall := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: limitCall, Field: "offset"},
		Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Int, Value: "20"}}},
	}
	allCall := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: offsetCall, Field: "all"},
		Args: nil,
	}
	got := c.compileExpr(allCall)
	if !strings.Contains(got, "app.Post.Where(PostWhere.Views.Gt(100))") {
		t.Fatalf("missing where clause, got:\n%s", got)
	}
	if !strings.Contains(got, `.OrderBy("views DESC")`) {
		t.Fatalf("missing OrderBy, got:\n%s", got)
	}
	if !strings.Contains(got, ".Limit(10)") {
		t.Fatalf("missing Limit, got:\n%s", got)
	}
	if !strings.Contains(got, ".Offset(20)") {
		t.Fatalf("missing Offset, got:\n%s", got)
	}
	if !strings.Contains(got, ".All(ctx)") {
		t.Fatalf("missing All, got:\n%s", got)
	}
}

// TestCompileAssignToMember — user.name = "new" (member assignment)
func TestCompileAssignToMember(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.AssignStmt{
		Target: &ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "name"},
		Op:     "=",
		Value:  &ast.Literal{Kind: token.String, Value: "new"},
	})
	out := compilerOut(c)
	if !strings.Contains(out, `user.Name = "new"`) {
		t.Fatalf("missing member assignment, got:\n%s", out)
	}
}

// TestCompileTemplateWithMemberAccess — "Hello ${user.name}, you have ${post.count} posts"
func TestCompileTemplateWithMemberAccess(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.TemplateString{Parts: []ast.Expr{
		&ast.Literal{Kind: token.String, Value: "Hello "},
		&ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "name"},
		&ast.Literal{Kind: token.String, Value: ", you have "},
		&ast.MemberExpr{Object: &ast.Ident{Name: "post"}, Field: "count"},
		&ast.Literal{Kind: token.String, Value: " posts"},
	}})
	if !strings.Contains(got, `"Hello "`) {
		t.Fatalf("missing Hello literal, got:\n%s", got)
	}
	if !strings.Contains(got, "user.Name") {
		t.Fatalf("missing user.Name, got:\n%s", got)
	}
	if !strings.Contains(got, `", you have "`) {
		t.Fatalf("missing middle literal, got:\n%s", got)
	}
	if !strings.Contains(got, "post.Count") {
		t.Fatalf("missing post.Count, got:\n%s", got)
	}
	if !strings.Contains(got, `" posts"`) {
		t.Fatalf("missing posts literal, got:\n%s", got)
	}
	// Should use strings.Builder pattern
	if !strings.Contains(got, "strings.Builder") {
		t.Fatalf("missing strings.Builder, got:\n%s", got)
	}
	// Should have 5 WriteString calls
	count := strings.Count(got, "_sb.WriteString(")
	if count != 5 {
		t.Fatalf("expected 5 WriteString calls, got %d:\n%s", count, got)
	}
}

// TestCompileListOfObjects — [{name: "a"}, {name: "b"}]
func TestCompileListOfObjects(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.ListExpr{Items: []ast.Expr{
		&ast.ObjectExpr{Fields: []*ast.NamedArg{
			{Name: "name", Value: &ast.Literal{Kind: token.String, Value: "a"}},
		}},
		&ast.ObjectExpr{Fields: []*ast.NamedArg{
			{Name: "name", Value: &ast.Literal{Kind: token.String, Value: "b"}},
		}},
	}})
	if !strings.Contains(got, "[]any{") {
		t.Fatalf("missing []any{, got:\n%s", got)
	}
	if !strings.Contains(got, `Name: "a"`) {
		t.Fatalf("missing first object, got:\n%s", got)
	}
	if !strings.Contains(got, `Name: "b"`) {
		t.Fatalf("missing second object, got:\n%s", got)
	}
}

// TestCompileElvisChainedGuards — multiple x ?: throw in sequence
func TestCompileElvisChainedGuards(t *testing.T) {
	c := newCompiler(nil)
	// user ?: throw error.NotFound
	c.compileStmt(&ast.ExprStmt{
		Expr: &ast.ElvisExpr{
			Left:  &ast.Ident{Name: "user"},
			Right: &ast.MemberExpr{Object: &ast.Ident{Name: "error"}, Field: "notFound"},
		},
	})
	// profile ?: throw error.NotFound
	c.compileStmt(&ast.ExprStmt{
		Expr: &ast.ElvisExpr{
			Left:  &ast.Ident{Name: "profile"},
			Right: &ast.MemberExpr{Object: &ast.Ident{Name: "error"}, Field: "notFound"},
		},
	})
	// token ?: throw error.Unauthorized
	c.compileStmt(&ast.ExprStmt{
		Expr: &ast.ElvisExpr{
			Left:  &ast.Ident{Name: "token"},
			Right: &ast.MemberExpr{Object: &ast.Ident{Name: "error"}, Field: "unauthorized"},
		},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "if user == nil {") {
		t.Fatalf("missing user nil check, got:\n%s", out)
	}
	if !strings.Contains(out, "if profile == nil {") {
		t.Fatalf("missing profile nil check, got:\n%s", out)
	}
	if !strings.Contains(out, "if token == nil {") {
		t.Fatalf("missing token nil check, got:\n%s", out)
	}
	if !strings.Contains(out, "return errors.Unauthorized") {
		t.Fatalf("missing errors.Unauthorized, got:\n%s", out)
	}
	// All three should have return errors.NotFound (first two)
	notFoundCount := strings.Count(out, "return errors.NotFound")
	if notFoundCount != 2 {
		t.Fatalf("expected 2 return errors.NotFound, got %d in:\n%s", notFoundCount, out)
	}
}

// TestCompileForWithNestedIf — for > if > break pattern
func TestCompileForWithNestedIf(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ForStmt{
		VarName:    "item",
		Collection: &ast.Ident{Name: "items"},
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.IfStmt{
				Condition: &ast.BinaryExpr{
					Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "item"}, Field: "found"},
					Op:    "==",
					Right: &ast.Literal{Kind: token.True, Value: "true"},
				},
				Then: &ast.Block{Stmts: []ast.Stmt{
					&ast.ExprStmt{Expr: &ast.CallExpr{
						Func: &ast.Ident{Name: "log"},
						Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "item"}}},
					}},
					&ast.BreakStmt{},
				}},
			},
		}},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "for _, item := range items {") {
		t.Fatalf("missing for range, got:\n%s", out)
	}
	if !strings.Contains(out, "if item.Found == true {") {
		t.Fatalf("missing if condition, got:\n%s", out)
	}
	if !strings.Contains(out, "log(item)") {
		t.Fatalf("missing log(item), got:\n%s", out)
	}
	if !strings.Contains(out, "break") {
		t.Fatalf("missing break, got:\n%s", out)
	}
}

// TestCompileQuestionWithModelFind — val user = User.find(id)?
func TestCompileQuestionWithModelFind(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)
	c.compileStmt(&ast.ValStmt{
		Name: "user",
		Value: &ast.UnaryExpr{
			Op: "?",
			Value: &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "find"},
				Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "id"}}},
			},
		},
	})
	out := compilerOut(c)
	// ? takes priority: val x = expr? → x, err := expr
	if !strings.Contains(out, "user, err :=") {
		t.Fatalf("missing user, err :=, got:\n%s", out)
	}
	if !strings.Contains(out, "app.User.Where(UserWhere.Id.Eq(id)).First(ctx)") {
		t.Fatalf("missing find chain, got:\n%s", out)
	}
	if !strings.Contains(out, "if err != nil") {
		t.Fatalf("missing err check, got:\n%s", out)
	}
}

// TestCompileAsyncWithMultipleEmits — async { emit A; emit B }
func TestCompileAsyncWithMultipleEmits(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.AsyncExpr{
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.EmitStmt{
				EventName: "TaskStarted",
				Args:      []*ast.NamedArg{{Name: "taskId", Value: &ast.Ident{Name: "id"}}},
			},
			&ast.EmitStmt{
				EventName: "TaskLogged",
				Args:      []*ast.NamedArg{{Name: "message", Value: &ast.Literal{Kind: token.String, Value: "started"}}},
			},
		}},
	})
	if !strings.Contains(got, "go func() {") {
		t.Fatalf("missing go func(), got:\n%s", got)
	}
	if !strings.Contains(got, "EmitTaskStarted") {
		t.Fatalf("missing EmitTaskStarted, got:\n%s", got)
	}
	if !strings.Contains(got, "TaskStartedEvent{") {
		t.Fatalf("missing TaskStartedEvent, got:\n%s", got)
	}
	if !strings.Contains(got, "EmitTaskLogged") {
		t.Fatalf("missing EmitTaskLogged, got:\n%s", got)
	}
	if !strings.Contains(got, "TaskLoggedEvent{") {
		t.Fatalf("missing TaskLoggedEvent, got:\n%s", got)
	}
}

// TestCompileModelDeleteWithCondition — User.where(it.active == false).delete()
func TestCompileModelDeleteWithCondition(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)
	got := c.compileExpr(&ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
				Args: []*ast.NamedArg{{
					Value: &ast.BinaryExpr{
						Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "active"},
						Op:    "==",
						Right: &ast.Literal{Kind: token.False, Value: "false"},
					},
				}},
			},
			Field: "delete",
		},
	})
	if !strings.Contains(got, "app.User.Where(UserWhere.Active.Eq(false))") {
		t.Fatalf("missing where clause, got:\n%s", got)
	}
	if !strings.Contains(got, ".Delete(ctx)") {
		t.Fatalf("missing .Delete(ctx), got:\n%s", got)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// F. Edge cases
// ═══════════════════════════════════════════════════════════════════════════════

// TestCompileEmptyTransaction — tx { } (empty body)
func TestCompileEmptyTransaction(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.TransactionExpr{
		Body: &ast.Block{Stmts: nil},
	})
	if !strings.Contains(got, "app.DB.Tx(ctx, func(ctx context.Context) error {") {
		t.Fatalf("missing Tx wrapper, got:\n%s", got)
	}
	if !strings.Contains(got, "return nil") {
		t.Fatalf("missing return nil, got:\n%s", got)
	}
	if !strings.Contains(got, "})") {
		t.Fatalf("missing closing, got:\n%s", got)
	}
}

// TestCompileEmptyAsync — async { } (empty body)
func TestCompileEmptyAsync(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.AsyncExpr{
		Body: &ast.Block{Stmts: nil},
	})
	if !strings.Contains(got, "go func() {") {
		t.Fatalf("missing go func(), got:\n%s", got)
	}
	if !strings.Contains(got, "}()") {
		t.Fatalf("missing }(), got:\n%s", got)
	}
}

// TestCompileEmptyAwait — await { } (empty body)
func TestCompileEmptyAwait(t *testing.T) {
	c := newCompiler(nil)
	c.compileExpr(&ast.AwaitExpr{
		Body: &ast.Block{Stmts: nil},
	})
	out := compilerOut(c)
	// Empty await with no val stmts → no errgroup, just empty
	if strings.Contains(out, "errgroup") {
		t.Fatalf("empty await should not produce errgroup, got:\n%s", out)
	}
}

// TestCompileEmptyLambda — { -> } (empty body)
func TestCompileEmptyLambda(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.LambdaExpr{
		Params: nil,
		Body:   &ast.Block{Stmts: nil},
	})
	if !strings.Contains(got, "func(it any) any {") {
		t.Fatalf("missing lambda signature with implicit it, got:\n%s", got)
	}
	if !strings.Contains(got, "}") {
		t.Fatalf("missing closing brace, got:\n%s", got)
	}
}

// TestCompileReturnNullExplicit — return null
func TestCompileReturnNullExplicit(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ReturnStmt{
		Value: &ast.Literal{Kind: token.Null, Value: "null"},
	})
	out := compilerOut(c)
	// null literal compiles to nil, then goes through writeScalarReturn
	if !strings.Contains(out, "nil") {
		t.Fatalf("missing nil for null literal, got:\n%s", out)
	}
}

// TestCompileMultipleAssignments — x = 1; y = 2; z = x + y
func TestCompileMultipleAssignments(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.AssignStmt{
		Target: &ast.Ident{Name: "x"},
		Op:     "=",
		Value:  &ast.Literal{Kind: token.Int, Value: "1"},
	})
	c.compileStmt(&ast.AssignStmt{
		Target: &ast.Ident{Name: "y"},
		Op:     "=",
		Value:  &ast.Literal{Kind: token.Int, Value: "2"},
	})
	c.compileStmt(&ast.AssignStmt{
		Target: &ast.Ident{Name: "z"},
		Op:     "=",
		Value: &ast.BinaryExpr{
			Left:  &ast.Ident{Name: "x"},
			Op:    "+",
			Right: &ast.Ident{Name: "y"},
		},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "x = 1") {
		t.Fatalf("missing x = 1, got:\n%s", out)
	}
	if !strings.Contains(out, "y = 2") {
		t.Fatalf("missing y = 2, got:\n%s", out)
	}
	if !strings.Contains(out, "z = x + y") {
		t.Fatalf("missing z = x + y, got:\n%s", out)
	}
}

// TestCompileForBreakImmediately — for { break }
func TestCompileForBreakImmediately(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ForStmt{
		VarName:    "x",
		Collection: &ast.Ident{Name: "items"},
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.BreakStmt{},
		}},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "for _, x := range items {") {
		t.Fatalf("missing for range, got:\n%s", out)
	}
	if !strings.Contains(out, "break") {
		t.Fatalf("missing break, got:\n%s", out)
	}
}

// TestCompileNestedForLoops — for > for
func TestCompileNestedForLoops(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ForStmt{
		VarName:    "row",
		Collection: &ast.Ident{Name: "matrix"},
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ForStmt{
				VarName:    "cell",
				Collection: &ast.Ident{Name: "row"},
				Body: &ast.Block{Stmts: []ast.Stmt{
					&ast.ExprStmt{Expr: &ast.CallExpr{
						Func: &ast.Ident{Name: "process"},
						Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "cell"}}},
					}},
				}},
			},
		}},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "for _, row := range matrix {") {
		t.Fatalf("missing outer for, got:\n%s", out)
	}
	if !strings.Contains(out, "for _, cell := range row {") {
		t.Fatalf("missing inner for, got:\n%s", out)
	}
	if !strings.Contains(out, "process(cell)") {
		t.Fatalf("missing process(cell), got:\n%s", out)
	}
}

// TestCompileWhenWithoutElse — when with no else clause
func TestCompileWhenWithoutElse(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.WhenExpr{
		Subject: &ast.Ident{Name: "status"},
		Branches: []*ast.WhenBranch{
			{Condition: &ast.Literal{Kind: token.String, Value: "OK"}, Body: &ast.Literal{Kind: token.Int, Value: "200"}},
			{Condition: &ast.Literal{Kind: token.String, Value: "ERR"}, Body: &ast.Literal{Kind: token.Int, Value: "500"}},
		},
		Else: nil,
	})
	if !strings.Contains(got, "switch status") {
		t.Fatalf("missing switch, got:\n%s", got)
	}
	if !strings.Contains(got, `case "OK"`) {
		t.Fatalf("missing case OK, got:\n%s", got)
	}
	if !strings.Contains(got, `case "ERR"`) {
		t.Fatalf("missing case ERR, got:\n%s", got)
	}
	if strings.Contains(got, "default:") {
		t.Fatalf("should NOT have default clause when no else, got:\n%s", got)
	}
	// Should still have return nil as fallback
	if !strings.Contains(got, "return nil") {
		t.Fatalf("missing return nil fallback, got:\n%s", got)
	}
}

// TestCompileUnaryNegation — val x = -count
func TestCompileUnaryNegation(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ValStmt{
		Name: "x",
		Value: &ast.UnaryExpr{
			Op:    "-",
			Value: &ast.Ident{Name: "count"},
		},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "x := -count") {
		t.Fatalf("missing x := -count, got:\n%s", out)
	}
}

// ─── Channel ───────────────────────────────────────────────────────────────

func TestCompileChannelConstruct(t *testing.T) {
	c := newCompiler(nil)
	// val ch = Channel(10)
	c.compileStmt(&ast.ValStmt{
		Name: "ch",
		Value: &ast.CallExpr{
			Func: &ast.Ident{Name: "Channel"},
			Args: []*ast.NamedArg{{Value: &ast.Literal{Value: "10"}}},
		},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "ch := make(chan any, 10)") {
		t.Fatalf("want make(chan any, 10), got:\n%s", out)
	}
	// ch should be tracked as channel
	if vt, ok := c.vars["ch"]; !ok || !vt.isChan {
		t.Fatal("ch should be tracked as channel variable")
	}
}

func TestCompileChannelConstructNoArgs(t *testing.T) {
	c := newCompiler(nil)
	c.compileStmt(&ast.ValStmt{
		Name:  "ch",
		Value: &ast.CallExpr{Func: &ast.Ident{Name: "Channel"}},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "ch := make(chan any, 0)") {
		t.Fatalf("want make(chan any, 0), got:\n%s", out)
	}
}

func TestCompileChannelSend(t *testing.T) {
	c := newCompiler(nil)
	// ch <- 42 as ExprStmt (BinaryExpr with op "<-")
	c.compileStmt(&ast.ExprStmt{
		Expr: &ast.BinaryExpr{
			Left:  &ast.Ident{Name: "ch"},
			Op:    "<-",
			Right: &ast.Literal{Value: "42"},
		},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "ch <- 42") {
		t.Fatalf("want 'ch <- 42', got:\n%s", out)
	}
}

func TestCompileChannelReceive(t *testing.T) {
	c := newCompiler(nil)
	// val value = <-ch
	c.compileStmt(&ast.ValStmt{
		Name: "value",
		Value: &ast.UnaryExpr{
			Op:    "<-",
			Value: &ast.Ident{Name: "ch"},
		},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "value := <-ch") {
		t.Fatalf("want 'value := <-ch', got:\n%s", out)
	}
}

func TestCompileChannelClose(t *testing.T) {
	c := newCompiler(nil)
	// First create a channel variable so it's tracked
	c.vars["ch"] = valType{isChan: true}
	// ch.close()
	c.compileStmt(&ast.ExprStmt{
		Expr: &ast.CallExpr{
			Func: &ast.MemberExpr{
				Object: &ast.Ident{Name: "ch"},
				Field:  "close",
			},
		},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "close(ch)") {
		t.Fatalf("want 'close(ch)', got:\n%s", out)
	}
}

func TestCompileChannelForRange(t *testing.T) {
	c := newCompiler(nil)
	c.vars["ch"] = valType{isChan: true}
	// for value in ch { ... }
	c.compileStmt(&ast.ForStmt{
		VarName:    "value",
		Collection: &ast.Ident{Name: "ch"},
		Body: &ast.Block{
			Stmts: []ast.Stmt{
				&ast.ExprStmt{Expr: &ast.CallExpr{
					Func: &ast.Ident{Name: "process"},
					Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "value"}}},
				}},
			},
		},
	})
	out := compilerOut(c)
	// Should use single-variable range for channels
	if !strings.Contains(out, "for value := range ch {") {
		t.Fatalf("want 'for value := range ch', got:\n%s", out)
	}
	// Should NOT have the _, prefix
	if strings.Contains(out, "for _, value") {
		t.Fatal("channel range should not have _ prefix")
	}
}

func TestCompileChannelForRangeSlice(t *testing.T) {
	c := newCompiler(nil)
	// Non-channel: for item in items { ... } — should keep _, prefix
	c.compileStmt(&ast.ForStmt{
		VarName:    "item",
		Collection: &ast.Ident{Name: "items"},
		Body:       &ast.Block{Stmts: []ast.Stmt{}},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "for _, item := range items {") {
		t.Fatalf("slice range should have _, prefix, got:\n%s", out)
	}
}

func TestCompileChannelSelect(t *testing.T) {
	c := newCompiler(nil)
	// when { <-ch1 -> { process(it) }, <-ch2 -> { handle(it) } }
	got := c.compileExpr(&ast.WhenExpr{
		Branches: []*ast.WhenBranch{
			{
				Condition: &ast.UnaryExpr{Op: "<-", Value: &ast.Ident{Name: "ch1"}},
				Body: &ast.LambdaExpr{
					Params: []string{"msg"},
					Body: &ast.Block{
						Stmts: []ast.Stmt{
							&ast.ExprStmt{Expr: &ast.CallExpr{
								Func: &ast.Ident{Name: "process"},
								Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "msg"}}},
							}},
						},
					},
				},
			},
			{
				Condition: &ast.UnaryExpr{Op: "<-", Value: &ast.Ident{Name: "ch2"}},
				Body: &ast.LambdaExpr{
					Params: []string{"data"},
					Body: &ast.Block{
						Stmts: []ast.Stmt{
							&ast.ExprStmt{Expr: &ast.CallExpr{
								Func: &ast.Ident{Name: "handle"},
								Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "data"}}},
							}},
						},
					},
				},
			},
		},
	})

	if !strings.Contains(got, "select {") {
		t.Fatalf("should generate select, got:\n%s", got)
	}
	if !strings.Contains(got, "case msg := <-ch1:") {
		t.Fatalf("should have 'case msg := <-ch1:', got:\n%s", got)
	}
	if !strings.Contains(got, "case data := <-ch2:") {
		t.Fatalf("should have 'case data := <-ch2:', got:\n%s", got)
	}
	if !strings.Contains(got, "process(msg)") {
		t.Fatalf("should call process(msg), got:\n%s", got)
	}
}

func TestCompileChannelSelectWithDefault(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.WhenExpr{
		Branches: []*ast.WhenBranch{
			{
				Condition: &ast.UnaryExpr{Op: "<-", Value: &ast.Ident{Name: "ch"}},
				Body: &ast.LambdaExpr{
					Params: []string{"v"},
					Body:   &ast.Block{Stmts: []ast.Stmt{}},
				},
			},
		},
		Else: &ast.Literal{Value: `"timeout"`},
	})

	if !strings.Contains(got, "select {") {
		t.Fatalf("should generate select, got:\n%s", got)
	}
	if !strings.Contains(got, "default:") {
		t.Fatalf("should have default branch, got:\n%s", got)
	}
}

func TestCompileChannelSelectNoLambda(t *testing.T) {
	c := newCompiler(nil)
	// Branch body is a plain expression, not a lambda
	got := c.compileExpr(&ast.WhenExpr{
		Branches: []*ast.WhenBranch{
			{
				Condition: &ast.UnaryExpr{Op: "<-", Value: &ast.Ident{Name: "ch"}},
				Body:      &ast.Literal{Value: `"received"`},
			},
		},
	})

	if !strings.Contains(got, "select {") {
		t.Fatalf("should generate select, got:\n%s", got)
	}
	if !strings.Contains(got, "case <-ch:") {
		t.Fatalf("should have 'case <-ch:' for no-lambda branch, got:\n%s", got)
	}
}

func TestCompileChannelSelectMixed(t *testing.T) {
	// Mix of channel receive and non-channel condition
	c := newCompiler(nil)
	got := c.compileExpr(&ast.WhenExpr{
		Branches: []*ast.WhenBranch{
			{
				Condition: &ast.UnaryExpr{Op: "<-", Value: &ast.Ident{Name: "ch1"}},
				Body: &ast.LambdaExpr{
					Params: []string{"msg"},
					Body:   &ast.Block{Stmts: []ast.Stmt{}},
				},
			},
			{
				// Non-channel branch: timeout case
				Condition: &ast.CallExpr{
					Func: &ast.MemberExpr{
						Object: &ast.Ident{Name: "time"},
						Field:  "After",
					},
					Args: []*ast.NamedArg{{Value: &ast.Literal{Value: "1000"}}},
				},
				Body: &ast.Literal{Value: `"timeout"`},
			},
		},
	})

	if !strings.Contains(got, "select {") {
		t.Fatalf("should generate select, got:\n%s", got)
	}
	if !strings.Contains(got, "case msg := <-ch1:") {
		t.Fatalf("should have channel case, got:\n%s", got)
	}
	if !strings.Contains(got, "case time.After(1000):") {
		t.Fatalf("should have non-channel case, got:\n%s", got)
	}
}

func TestCompileChannelForRangeNonIdent(t *testing.T) {
	// for item in getChannel() { ... } — non-Ident collection, isChannelVar should return false
	c := newCompiler(nil)
	c.compileStmt(&ast.ForStmt{
		VarName: "item",
		Collection: &ast.CallExpr{
			Func: &ast.Ident{Name: "getItems"},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{}},
	})
	out := compilerOut(c)
	// Should use _, prefix since it's not a known channel var
	if !strings.Contains(out, "for _, item := range") {
		t.Fatalf("non-ident collection should use _, prefix, got:\n%s", out)
	}
}

func TestCompileChannelCloseNonChannel(t *testing.T) {
	// obj.close() where obj is NOT a channel — should NOT generate close(obj)
	c := newCompiler(nil)
	c.compileStmt(&ast.ExprStmt{
		Expr: &ast.CallExpr{
			Func: &ast.MemberExpr{
				Object: &ast.Ident{Name: "conn"},
				Field:  "close",
			},
		},
	})
	out := compilerOut(c)
	if strings.Contains(out, "close(conn)") {
		t.Fatal("non-channel .close() should NOT become close()")
	}
	if !strings.Contains(out, "conn.Close()") {
		t.Fatalf("should keep original method call, got:\n%s", out)
	}
}

func TestCompileChannelReceiveExpr(t *testing.T) {
	// <-ch as standalone expression (not val assignment)
	c := newCompiler(nil)
	got := c.compileExpr(&ast.UnaryExpr{
		Op:    "<-",
		Value: &ast.Ident{Name: "ch"},
	})
	if got != "<-ch" {
		t.Fatalf("want '<-ch', got %q", got)
	}
}

func TestCompileIsChannelConstructorNonCall(t *testing.T) {
	c := newCompiler(nil)
	// Non-CallExpr should return false
	if c.isChannelConstructor(&ast.Ident{Name: "x"}) {
		t.Fatal("Ident should not be a channel constructor")
	}
	// CallExpr with non-Channel name
	if c.isChannelConstructor(&ast.CallExpr{Func: &ast.Ident{Name: "foo"}}) {
		t.Fatal("foo() should not be a channel constructor")
	}
	// CallExpr with Channel name
	if !c.isChannelConstructor(&ast.CallExpr{Func: &ast.Ident{Name: "Channel"}}) {
		t.Fatal("Channel() should be a channel constructor")
	}
}

func TestCompileHasChannelBranchFalse(t *testing.T) {
	c := newCompiler(nil)
	// When with no channel branches
	when := &ast.WhenExpr{
		Branches: []*ast.WhenBranch{
			{Condition: &ast.Literal{Value: "true"}, Body: &ast.Literal{Value: "1"}},
		},
	}
	if c.hasChannelBranch(when) {
		t.Fatal("should return false for non-channel branches")
	}
}

func TestCompileChannelProducerConsumer(t *testing.T) {
	c := newCompiler(nil)
	// Full producer-consumer pattern:
	// val ch = Channel(10)
	// async { ch <- 42; ch.close() }
	// for value in ch { process(value) }
	c.compileStmt(&ast.ValStmt{
		Name: "ch",
		Value: &ast.CallExpr{
			Func: &ast.Ident{Name: "Channel"},
			Args: []*ast.NamedArg{{Value: &ast.Literal{Value: "10"}}},
		},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "make(chan any, 10)") {
		t.Fatalf("missing channel creation, got:\n%s", out)
	}
}

// --- compileUpdateChain ---

func TestCompileUpdateChainEmpty(t *testing.T) {
	c := newCompiler(nil)
	var b strings.Builder
	c.compileUpdateChain(&b, "User", nil)
	got := b.String()
	if got != ".Update(ctx)" {
		t.Fatalf("empty args should produce .Update(ctx), got %q", got)
	}
}

func TestCompileUpdateChainBasic(t *testing.T) {
	c := newCompiler(makeModels("User"))
	var b strings.Builder
	c.compileUpdateChain(&b, "User", []*ast.NamedArg{
		{Name: "name", Value: &ast.Literal{Kind: token.String, Value: "lin"}},
	})
	got := b.String()
	if !strings.Contains(got, `lux.SetField{Col: "name", Val: "lin"}`) {
		t.Fatalf("basic update chain wrong, got:\n%s", got)
	}
}

func TestCompileUpdateChainAtomic(t *testing.T) {
	c := newCompiler(makeModels("User"))
	var b strings.Builder
	c.compileUpdateChain(&b, "User", []*ast.NamedArg{
		{Name: "score", Value: &ast.BinaryExpr{
			Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "score"},
			Op:    "+",
			Right: &ast.Literal{Value: "10"},
		}},
	})
	got := b.String()
	if !strings.Contains(got, `Atomic: "+"`) {
		t.Fatalf("atomic update should have Atomic field, got:\n%s", got)
	}
}

func TestCompileUpdateChainHash(t *testing.T) {
	models := map[string]*ast.ModelDecl{
		"User": {
			Name: "User",
			Fields: []*ast.FieldDecl{
				{Name: "password", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hash"}}},
			},
		},
	}
	c := newCompiler(models)
	var b strings.Builder
	c.compileUpdateChain(&b, "User", []*ast.NamedArg{
		{Name: "password", Value: &ast.Literal{Kind: token.String, Value: "secret"}},
	})
	out := compilerOut(c) + b.String()
	if !strings.Contains(out, "luxocrypto.HashPassword") {
		t.Fatalf("hash field should generate HashPassword, got:\n%s", out)
	}
	if !strings.Contains(out, "hashedPassword") {
		t.Fatalf("hash field should use hashedPassword, got:\n%s", out)
	}
}

// --- isHashField ---

func TestIsHashField(t *testing.T) {
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
			{Name: "password", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hash"}}},
		},
	}
	if !isHashField(m, "password") {
		t.Error("password should be a hash field")
	}
	if isHashField(m, "name") {
		t.Error("name should not be a hash field")
	}
	if isHashField(m, "missing") {
		t.Error("missing field should return false")
	}
}

// --- compileTerminalMethod ---

func TestCompileTerminalMethodFind(t *testing.T) {
	c := newCompiler(makeModels("User"))
	var b strings.Builder
	done := c.compileTerminalMethod(&b, "User", chainLink{
		method: "find",
		args:   []*ast.NamedArg{{Value: &ast.Literal{Value: "42"}}},
	})
	if !done {
		t.Fatal("find should return true")
	}
	got := b.String()
	if !strings.Contains(got, "app.User.Where(UserWhere.Id.Eq(42))") {
		t.Fatalf("find should generate Where+First, got:\n%s", got)
	}
}

func TestCompileTerminalMethodDelete(t *testing.T) {
	c := newCompiler(makeModels("User"))
	var b strings.Builder
	done := c.compileTerminalMethod(&b, "User", chainLink{method: "delete"})
	if !done {
		t.Fatal("delete should return true")
	}
	got := b.String()
	if !strings.Contains(got, ".Delete(ctx)") {
		t.Fatalf("delete should generate .Delete(ctx), got:\n%s", got)
	}
}

func TestCompileTerminalMethodDeleteSoft(t *testing.T) {
	models := map[string]*ast.ModelDecl{
		"User": {
			Name:       "User",
			Directives: []*ast.Directive{{Name: "soft"}},
		},
	}
	c := newCompiler(models)
	var b strings.Builder
	done := c.compileTerminalMethod(&b, "User", chainLink{method: "delete"})
	if !done {
		t.Fatal("delete should return true")
	}
	got := b.String()
	if !strings.Contains(got, ".SoftDelete(ctx)") {
		t.Fatalf("soft delete should generate .SoftDelete(ctx), got:\n%s", got)
	}
}

func TestCompileTerminalMethodAll(t *testing.T) {
	c := newCompiler(makeModels("User"))
	var b strings.Builder
	done := c.compileTerminalMethod(&b, "User", chainLink{method: "all"})
	if !done {
		t.Fatal("all should return true")
	}
	if !strings.Contains(b.String(), ".All(ctx)") {
		t.Fatalf("got: %s", b.String())
	}
}

func TestCompileTerminalMethodAllPaginate(t *testing.T) {
	c := newCompiler(makeModels("User"))
	c.paginate = true
	var b strings.Builder
	done := c.compileTerminalMethod(&b, "User", chainLink{method: "all"})
	if !done {
		t.Fatal("all should return true")
	}
	if !strings.Contains(b.String(), "AllWithCount(ctx)") {
		t.Fatalf("paginated all should use AllWithCount, got: %s", b.String())
	}
}

func TestCompileTerminalMethodFirst(t *testing.T) {
	c := newCompiler(makeModels("User"))
	var b strings.Builder
	done := c.compileTerminalMethod(&b, "User", chainLink{method: "first"})
	if !done {
		t.Fatal("first should return true")
	}
	if !strings.Contains(b.String(), ".First(ctx)") {
		t.Fatalf("got: %s", b.String())
	}
}

func TestCompileTerminalMethodExists(t *testing.T) {
	c := newCompiler(makeModels("User"))
	var b strings.Builder
	done := c.compileTerminalMethod(&b, "User", chainLink{method: "exists"})
	if !done {
		t.Fatal("exists should return true")
	}
	if !strings.Contains(b.String(), ".Exists(ctx)") {
		t.Fatalf("got: %s", b.String())
	}
}

func TestCompileTerminalMethodCount(t *testing.T) {
	c := newCompiler(makeModels("User"))
	var b strings.Builder
	done := c.compileTerminalMethod(&b, "User", chainLink{method: "count"})
	if !done {
		t.Fatal("count should return true")
	}
	if !strings.Contains(b.String(), ".Count(ctx)") {
		t.Fatalf("got: %s", b.String())
	}
}

func TestCompileTerminalMethodSave(t *testing.T) {
	c := newCompiler(makeModels("User"))
	var b strings.Builder
	done := c.compileTerminalMethod(&b, "User", chainLink{method: "save"})
	if !done {
		t.Fatal("save should return true")
	}
	if !strings.Contains(b.String(), ".Exec(ctx)") {
		t.Fatalf("got: %s", b.String())
	}
}

func TestCompileTerminalMethodSumWithLambda(t *testing.T) {
	c := newCompiler(makeModels("Order"))
	var b strings.Builder
	done := c.compileTerminalMethod(&b, "Order", chainLink{
		method: "sum",
		args: []*ast.NamedArg{{Value: &ast.LambdaExpr{
			Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.ExprStmt{Expr: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "amount"}},
			}},
		}}},
	})
	if !done {
		t.Fatal("sum should return true")
	}
	if !strings.Contains(b.String(), `.Sum(ctx, "amount")`) {
		t.Fatalf("sum with lambda should extract field, got: %s", b.String())
	}
}

func TestCompileTerminalMethodSumNoArgs(t *testing.T) {
	c := newCompiler(makeModels("Order"))
	var b strings.Builder
	done := c.compileTerminalMethod(&b, "Order", chainLink{method: "sum"})
	if !done {
		t.Fatal("sum should return true")
	}
	if !strings.Contains(b.String(), ".Sum(ctx)") {
		t.Fatalf("sum without args, got: %s", b.String())
	}
}

func TestCompileTerminalMethodUpdate(t *testing.T) {
	c := newCompiler(makeModels("User"))
	var b strings.Builder
	done := c.compileTerminalMethod(&b, "User", chainLink{
		method: "update",
		args:   []*ast.NamedArg{{Name: "name", Value: &ast.Literal{Kind: token.String, Value: "x"}}},
	})
	if !done {
		t.Fatal("update should return true")
	}
	if !strings.Contains(b.String(), ".Update(ctx,") {
		t.Fatalf("got: %s", b.String())
	}
}

func TestCompileTerminalMethodNonTerminal(t *testing.T) {
	c := newCompiler(makeModels("User"))
	var b strings.Builder
	done := c.compileTerminalMethod(&b, "User", chainLink{method: "where"})
	if done {
		t.Fatal("where should not be terminal")
	}
}

func TestCompileTerminalMethodEmptyBuilder(t *testing.T) {
	// Terminal on empty builder — should seed with app.Model
	c := newCompiler(makeModels("User"))
	var b strings.Builder
	done := c.compileTerminalMethod(&b, "User", chainLink{method: "count"})
	if !done {
		t.Fatal("count should return true")
	}
	if !strings.Contains(b.String(), "app.User") {
		t.Fatalf("should seed builder with app.User, got: %s", b.String())
	}
}

// --- compileScopeExpr ---

func TestCompileScopeExpr(t *testing.T) {
	c := newCompiler(makeModels("Post"))
	scope := &ast.ScopeDecl{
		Name: "published",
		Expr: &ast.CallExpr{
			Func: &ast.Ident{Name: "where"},
			Args: []*ast.NamedArg{
				{Name: "status", Value: &ast.Literal{Kind: token.String, Value: "PUBLISHED"}},
			},
		},
	}
	var b strings.Builder
	c.compileScopeExpr(&b, "Post", scope)
	got := b.String()
	if !strings.Contains(got, "PostWhere.Status.Eq") {
		t.Fatalf("scope should compile where condition, got:\n%s", got)
	}
}

func TestCompileScopeExprOrderByAndLimit(t *testing.T) {
	c := newCompiler(makeModels("Post"))
	// scope recent = orderBy(createdAt.desc).limit(10)
	scope := &ast.ScopeDecl{
		Name: "recent",
		Expr: &ast.CallExpr{
			Func: &ast.MemberExpr{
				Object: &ast.CallExpr{
					Func: &ast.Ident{Name: "orderBy"},
					Args: []*ast.NamedArg{{Value: &ast.MemberExpr{
						Object: &ast.Ident{Name: "createdAt"},
						Field:  "desc",
					}}},
				},
				Field: "limit",
			},
			Args: []*ast.NamedArg{{Value: &ast.Literal{Value: "10"}}},
		},
	}
	var b strings.Builder
	c.compileScopeExpr(&b, "Post", scope)
	got := b.String()
	if !strings.Contains(got, "OrderBy") {
		t.Fatalf("scope should compile orderBy, got:\n%s", got)
	}
	if !strings.Contains(got, "Limit(10)") {
		t.Fatalf("scope should compile limit, got:\n%s", got)
	}
}

// --- isStringExpr ---

func TestIsStringExprLiteral(t *testing.T) {
	c := newCompiler(nil)
	if !c.isStringExpr(&ast.Literal{Kind: token.String, Value: "hello"}) {
		t.Error("string literal should be string")
	}
	if c.isStringExpr(&ast.Literal{Kind: token.Int, Value: "42"}) {
		t.Error("int literal should not be string")
	}
}

func TestIsStringExprIdent(t *testing.T) {
	c := newCompiler(nil)
	c.vars["name"] = valType{name: "String"}
	if !c.isStringExpr(&ast.Ident{Name: "name"}) {
		t.Error("String var should be string")
	}
	c.vars["count"] = valType{name: "Int"}
	if c.isStringExpr(&ast.Ident{Name: "count"}) {
		t.Error("Int var should not be string")
	}
}

func TestIsStringExprMember(t *testing.T) {
	models := map[string]*ast.ModelDecl{
		"User": {
			Name: "User",
			Fields: []*ast.FieldDecl{
				{Name: "name", Type: &ast.TypeRef{Name: "String"}},
				{Name: "age", Type: &ast.TypeRef{Name: "Int"}},
			},
		},
	}
	c := newCompiler(models)
	c.vars["user"] = valType{name: "User", isModel: true}

	if !c.isStringExpr(&ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "name"}) {
		t.Error("user.name should be string")
	}
	if c.isStringExpr(&ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "age"}) {
		t.Error("user.age should not be string")
	}
}

// --- inferWhenReturnType ---

func TestInferWhenReturnType(t *testing.T) {
	tests := []struct {
		retType *ast.TypeRef
		want    string
	}{
		{nil, "any"},
		{&ast.TypeRef{Name: "String"}, "string"},
		{&ast.TypeRef{Name: "Int"}, "int64"},
		{&ast.TypeRef{Name: "Float"}, "float64"},
		{&ast.TypeRef{Name: "Boolean"}, "bool"},
		{&ast.TypeRef{Name: "User"}, "any"},
	}
	for _, tt := range tests {
		c := newCompiler(nil)
		c.api.ReturnType = tt.retType
		got := c.inferWhenReturnType(&ast.WhenExpr{})
		if got != tt.want {
			name := "nil"
			if tt.retType != nil {
				name = tt.retType.Name
			}
			t.Errorf("inferWhenReturnType(%s) = %q, want %q", name, got, tt.want)
		}
	}
}

// --- zeroValueForType ---

func TestZeroValueForType(t *testing.T) {
	tests := []struct {
		typ  string
		want string
	}{
		{"string", `""`},
		{"int64", "0"},
		{"int", "0"},
		{"float64", "0"},
		{"bool", "false"},
		{"any", "nil"},
		{"*User", "nil"},
	}
	for _, tt := range tests {
		if got := zeroValueForType(tt.typ); got != tt.want {
			t.Errorf("zeroValueForType(%q) = %q, want %q", tt.typ, got, tt.want)
		}
	}
}

// --- extractLambdaField ---

func TestExtractLambdaField(t *testing.T) {
	// Valid: { it.minutes }
	got := extractLambdaField(&ast.LambdaExpr{
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ExprStmt{Expr: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "minutes"}},
		}},
	})
	if got != "minutes" {
		t.Fatalf("want minutes, got %q", got)
	}

	// Non-lambda
	if extractLambdaField(&ast.Ident{Name: "x"}) != "" {
		t.Error("non-lambda should return empty")
	}

	// Empty body
	if extractLambdaField(&ast.LambdaExpr{Body: &ast.Block{}}) != "" {
		t.Error("empty body should return empty")
	}

	// Non-ExprStmt
	if extractLambdaField(&ast.LambdaExpr{Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ReturnStmt{},
	}}}) != "" {
		t.Error("non-ExprStmt should return empty")
	}

	// Non-MemberExpr
	if extractLambdaField(&ast.LambdaExpr{Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.Ident{Name: "x"}},
	}}}) != "" {
		t.Error("non-MemberExpr should return empty")
	}

	// Not "it" object
	if extractLambdaField(&ast.LambdaExpr{Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.MemberExpr{Object: &ast.Ident{Name: "other"}, Field: "x"}},
	}}}) != "" {
		t.Error("non-it object should return empty")
	}

	// Non-Ident object
	if extractLambdaField(&ast.LambdaExpr{Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.MemberExpr{Object: &ast.CallExpr{Func: &ast.Ident{Name: "x"}}, Field: "y"}},
	}}}) != "" {
		t.Error("non-Ident object should return empty")
	}
}

// --- compileModelChain with @scope ---

func TestCompileModelChainWithScope(t *testing.T) {
	models := map[string]*ast.ModelDecl{
		"Post": {
			Name: "Post",
			Scopes: []*ast.ScopeDecl{
				{
					Name: "published",
					Expr: &ast.CallExpr{
						Func: &ast.Ident{Name: "where"},
						Args: []*ast.NamedArg{
							{Name: "status", Value: &ast.Literal{Kind: token.String, Value: "PUBLISHED"}},
						},
					},
				},
			},
		},
	}
	c := newCompiler(models)
	c.api.Directives = []*ast.Directive{
		{Name: "scope", Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "published"}}}},
	}

	got := c.compileModelChain("Post", []chainLink{
		{method: "all"},
	})
	if !strings.Contains(got, "PostWhere.Status.Eq") {
		t.Fatalf("scope injection should add where condition, got:\n%s", got)
	}
}

// --- compileCreateLink ---

func TestCompileCreateLinkBasic(t *testing.T) {
	c := newCompiler(makeModels("User"))
	var b strings.Builder
	c.compileCreateLink(&b, "User", chainLink{
		method: "create",
		args: []*ast.NamedArg{
			{Name: "name", Value: &ast.Literal{Kind: token.String, Value: "lin"}},
		},
	}, true)
	got := b.String()
	if !strings.Contains(got, `app.User.Create().SetName("lin").Exec(ctx)`) {
		t.Fatalf("create should generate builder chain, got:\n%s", got)
	}
}

func TestCompileCreateLinkNullable(t *testing.T) {
	models := map[string]*ast.ModelDecl{
		"Post": {
			Name: "Post",
			Fields: []*ast.FieldDecl{
				{Name: "subtitle", Type: &ast.TypeRef{Name: "String", Nullable: true}},
			},
		},
	}
	c := newCompiler(models)
	var b strings.Builder
	c.compileCreateLink(&b, "Post", chainLink{
		method: "create",
		args: []*ast.NamedArg{
			{Name: "subtitle", Value: &ast.Literal{Kind: token.String, Value: "test"}},
		},
	}, false)
	got := b.String()
	if !strings.Contains(got, `&"test"`) {
		t.Fatalf("nullable field should wrap with &, got:\n%s", got)
	}
}

func TestCompileCreateLinkHash(t *testing.T) {
	models := map[string]*ast.ModelDecl{
		"User": {
			Name: "User",
			Fields: []*ast.FieldDecl{
				{Name: "password", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hash"}}},
			},
		},
	}
	c := newCompiler(models)
	var b strings.Builder
	c.compileCreateLink(&b, "User", chainLink{
		method: "create",
		args: []*ast.NamedArg{
			{Name: "password", Value: &ast.Literal{Kind: token.String, Value: "secret"}},
		},
	}, true)
	out := compilerOut(c)
	if !strings.Contains(out, "luxocrypto.HashPassword") {
		t.Fatalf("hash field should call HashPassword, got:\n%s", out)
	}
}

// ─── writeReturnByType — BinaryMode branches ──────────────────────────────

func TestWriteReturnByTypeModelSingle(t *testing.T) {
	c := newCompiler(nil)
	c.writeReturnByType("user", valType{isModel: true, name: "User"})
	out := compilerOut(c)
	if !strings.Contains(out, "user.WriteLuxo(req.Buf, req.FieldMask)") {
		t.Fatalf("missing WriteLuxo for model single binary, got:\n%s", out)
	}
	if !strings.Contains(out, "user.WriteLuxo(req.Buf, req.FieldMask)") {
		t.Fatalf("missing WriteLuxo for model single json, got:\n%s", out)
	}
	if !strings.Contains(out, "req.Buf") {
		t.Fatalf("missing BinaryMode check, got:\n%s", out)
	}
}

func TestWriteReturnByTypeModelList(t *testing.T) {
	c := newCompiler(nil)
	c.writeReturnByType("users", valType{isModel: true, isList: true, name: "User"})
	out := compilerOut(c)
	if !strings.Contains(out, "WriteColumnarUser(req.Buf, users, req.FieldMask)") {
		t.Fatalf("missing WriteColumnarUser call, got:\n%s", out)
	}
}

func TestWriteReturnByTypePaginatedList(t *testing.T) {
	c := newCompiler(nil)
	c.paginate = true
	c.hasTotalVar = true
	c.writeReturnByType("posts", valType{isModel: true, isList: true, name: "Post"})
	out := compilerOut(c)
	// Binary mode paginated response
	if !strings.Contains(out, "codec.AppendSvarint(req.Buf.B, _total)") {
		t.Fatalf("missing _total in binary paginated response, got:\n%s", out)
	}
	if !strings.Contains(out, "codec.AppendSvarint(req.Buf.B, int64(req.Page))") {
		t.Fatalf("missing page in binary paginated response, got:\n%s", out)
	}
	if !strings.Contains(out, "codec.AppendSvarint(req.Buf.B, int64(req.PageSize))") {
		t.Fatalf("missing pageSize in binary paginated response, got:\n%s", out)
	}
	// Binary-only — no JSON keys expected (Luvia converts via schema)
	if strings.Contains(out, `"items"`) {
		t.Fatalf("should not have JSON items key in binary-only mode, got:\n%s", out)
	}
}

func TestWriteReturnByTypeScalarsBinaryMode(t *testing.T) {
	tests := []struct {
		name   string
		vt     valType
		binary string
	}{
		{"Int", valType{name: "Int"}, "codec.AppendSvarint"},
		{"Float", valType{name: "Float"}, "codec.AppendFixed64"},
		{"Boolean", valType{name: "Boolean"}, "codec.AppendBool"},
		{"String", valType{name: "String"}, "codec.AppendString"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newCompiler(nil)
			c.writeReturnByType("val", tt.vt)
			out := compilerOut(c)
			if !strings.Contains(out, tt.binary) {
				t.Errorf("missing binary %q, got:\n%s", tt.binary, out)
			}
			if !strings.Contains(out, "req.Buf") {
				t.Errorf("missing req.Buf, got:\n%s", out)
			}
		})
	}
}

// ─── compileExprStmt — uncovered branches ───────────────────────────────────

func TestCompileExprStmtTransactionStandalone(t *testing.T) {
	c := newCompiler(nil)
	// transaction { ... } as ExprStmt
	txExpr := &ast.CallExpr{
		Func: &ast.Ident{Name: "transaction"},
		Args: []*ast.NamedArg{
			{Value: &ast.LambdaExpr{Body: &ast.Block{Stmts: []ast.Stmt{}}}},
		},
	}
	c.compileStmt(&ast.ExprStmt{Expr: txExpr})
	out := compilerOut(c)
	if !strings.Contains(out, "if err :=") {
		t.Fatalf("transaction ExprStmt should wrap in error check, got:\n%s", out)
	}
	if !strings.Contains(out, "return err") {
		t.Fatalf("transaction ExprStmt should return err, got:\n%s", out)
	}
}

func TestCompileExprStmtModelQueryStandalone(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)
	// User.where(...).delete() as standalone ExprStmt
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
				Args: []*ast.NamedArg{},
			},
			Field: "delete",
		},
		Args: []*ast.NamedArg{},
	}
	c.compileStmt(&ast.ExprStmt{Expr: expr})
	out := compilerOut(c)
	if !strings.Contains(out, "if _, err :=") {
		t.Fatalf("model query ExprStmt should wrap in error check, got:\n%s", out)
	}
}

func TestCompileExprStmtGenericFallthrough(t *testing.T) {
	c := newCompiler(nil)
	// Plain function call: doSomething()
	c.compileStmt(&ast.ExprStmt{Expr: &ast.CallExpr{
		Func: &ast.Ident{Name: "doSomething"},
		Args: []*ast.NamedArg{},
	}})
	out := compilerOut(c)
	if !strings.Contains(out, "doSomething()") {
		t.Fatalf("generic ExprStmt should compile directly, got:\n%s", out)
	}
}

// ─── compileUnary — ? operator ─────────────────────────────────────────────

func TestCompileUnaryQuestion(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.UnaryExpr{
		Op:    "?",
		Value: &ast.Ident{Name: "result"},
	})
	if got != "result" {
		t.Fatalf("? unary should return operand, got %q", got)
	}
}

// ─── compileMember — enum member ────────────────────────────────────────────

func TestCompileMemberEnumValue(t *testing.T) {
	c := newCompiler(nil)
	c.enums = map[string]bool{"Role": true}
	got := c.compileExpr(&ast.MemberExpr{
		Object: &ast.Ident{Name: "Role"},
		Field:  "admin",
	})
	if got != "RoleADMIN" {
		t.Fatalf("enum member should be RoleADMIN, got %q", got)
	}
}

// ─── compileCall — channel close, Channel constructor ───────────────────────

func TestCompileCallChannelClose(t *testing.T) {
	c := newCompiler(nil)
	c.vars["ch"] = valType{isChan: true}
	got := c.compileExpr(&ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "ch"},
			Field:  "close",
		},
		Args: []*ast.NamedArg{},
	})
	if got != "close(ch)" {
		t.Fatalf("channel close should be close(ch), got %q", got)
	}
}

func TestCompileCallTransactionLambda(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.CallExpr{
		Func: &ast.Ident{Name: "transaction"},
		Args: []*ast.NamedArg{
			{Value: &ast.LambdaExpr{Body: &ast.Block{Stmts: []ast.Stmt{}}}},
		},
	})
	if !strings.Contains(got, "app.DB.Tx(ctx") {
		t.Fatalf("transaction should compile to app.DB.Tx, got %q", got)
	}
}

// ─── compileModifierMethod — select, groupBy, offset, unknown method ────────

func TestCompileModifierMethodSelect(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)
	got := c.compileModelChain("User", []chainLink{
		{method: "select"},
		{method: "all"},
	})
	if !strings.Contains(got, "selection.SQLColumns(req.Select)") {
		t.Fatalf("select should use SQLColumns, got %q", got)
	}
}

func TestCompileModifierMethodGroupBy(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)
	got := c.compileModelChain("User", []chainLink{
		{method: "groupBy", args: []*ast.NamedArg{{Value: &ast.Ident{Name: "status"}}}},
	})
	if !strings.Contains(got, `"status"`) {
		t.Fatalf("groupBy should contain column name, got %q", got)
	}
	if !strings.Contains(got, "GroupBy(ctx") {
		t.Fatalf("groupBy should call GroupBy(ctx, ...), got %q", got)
	}
}

func TestCompileModifierMethodOffset(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)
	got := c.compileModelChain("User", []chainLink{
		{method: "offset", args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Int, Value: "10"}}}},
		{method: "all"},
	})
	if !strings.Contains(got, ".Offset(10)") {
		t.Fatalf("offset should generate .Offset(), got %q", got)
	}
}

func TestCompileModifierMethodUnknown(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)
	got := c.compileModelChain("User", []chainLink{
		{method: "customMethod", args: []*ast.NamedArg{{Value: &ast.Ident{Name: "x"}}}},
		{method: "all"},
	})
	if !strings.Contains(got, "app.User.CustomMethod(x)") {
		t.Fatalf("unknown method should passthrough, got %q", got)
	}
}

// ─── compileOrderByChain — plain ident (no .desc/.asc) ─────────────────────

func TestCompileOrderByChainPlainIdent(t *testing.T) {
	c := newCompiler(nil)
	var b strings.Builder
	c.compileOrderByChain(&b, []*ast.NamedArg{
		{Value: &ast.Ident{Name: "createdAt"}},
	})
	out := b.String()
	if !strings.Contains(out, `"created_at ASC"`) {
		t.Fatalf("plain ident should default to ASC, got %q", out)
	}
}

// ─── compileForExpr — range expr variant ────────────────────────────────────

func TestCompileForExprRangeCollect(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.ForStmt{
		VarName: "i",
		Collection: &ast.RangeExpr{
			Start: &ast.Literal{Kind: token.Int, Value: "1"},
			End:   &ast.Literal{Kind: token.Int, Value: "5"},
		},
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ExprStmt{Expr: &ast.Ident{Name: "i"}},
		}},
	})
	if !strings.Contains(got, "int64(1)") || !strings.Contains(got, "_result") {
		t.Fatalf("for-range expr should generate range loop with _result, got:\n%s", got)
	}
}

func TestCompileForExprWithReturnStmt(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.ForStmt{
		VarName:    "item",
		Collection: &ast.Ident{Name: "items"},
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ReturnStmt{Value: &ast.Ident{Name: "item"}},
		}},
	})
	if !strings.Contains(got, "item") && !strings.Contains(got, "_result") {
		t.Fatalf("for expr with return should collect results, got:\n%s", got)
	}
}

// ─── compileTemplate — non-string expression ────────────────────────────────

func TestCompileTemplateWithIntExpr(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.TemplateString{
		Parts: []ast.Expr{
			&ast.Literal{Kind: token.String, Value: "count: "},
			&ast.Ident{Name: "count"},
		},
	})
	if !strings.Contains(got, "strconv.FormatInt") {
		t.Fatalf("int expr in template should use FormatInt, got:\n%s", got)
	}
}

func TestCompileTemplateWithStringMember(t *testing.T) {
	models := map[string]*ast.ModelDecl{
		"User": {
			Name: "User",
			Fields: []*ast.FieldDecl{
				{Name: "name", Type: &ast.TypeRef{Name: "String"}},
			},
		},
	}
	c := newCompiler(models)
	c.vars["user"] = valType{isModel: true, name: "User"}
	got := c.compileExpr(&ast.TemplateString{
		Parts: []ast.Expr{
			&ast.Literal{Kind: token.String, Value: "hello "},
			&ast.MemberExpr{Object: &ast.Ident{Name: "user"}, Field: "name"},
		},
	})
	if !strings.Contains(got, "WriteString(user.Name)") {
		t.Fatalf("string member in template should use WriteString, got:\n%s", got)
	}
}

// ─── compileWhen — subject switch, type switch, no-else ─────────────────────

func TestCompileWhenSubjectSwitch(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.WhenExpr{
		Subject: &ast.Ident{Name: "status"},
		Branches: []*ast.WhenBranch{
			{Condition: &ast.Literal{Kind: token.String, Value: "active"}, Body: &ast.Literal{Kind: token.Int, Value: "1"}},
			{Condition: &ast.Literal{Kind: token.String, Value: "inactive"}, Body: &ast.Literal{Kind: token.Int, Value: "0"}},
		},
	})
	if !strings.Contains(got, "switch status") {
		t.Fatalf("when with subject should generate switch, got:\n%s", got)
	}
}

func TestCompileWhenTypeSwitch(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.WhenExpr{
		Subject: &ast.Ident{Name: "val"},
		Branches: []*ast.WhenBranch{
			{IsType: "string", Body: &ast.Literal{Kind: token.String, Value: "str"}},
			{IsType: "int64", Body: &ast.Literal{Kind: token.String, Value: "num"}},
		},
	})
	if !strings.Contains(got, "switch val.(type)") {
		t.Fatalf("when with isType should generate type switch, got:\n%s", got)
	}
	if !strings.Contains(got, "case string:") {
		t.Fatalf("should have case string, got:\n%s", got)
	}
}

func TestCompileWhenWithElse(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.WhenExpr{
		Branches: []*ast.WhenBranch{
			{Condition: &ast.Ident{Name: "x"}, Body: &ast.Literal{Kind: token.Int, Value: "1"}},
		},
		Else: &ast.Literal{Kind: token.Int, Value: "0"},
	})
	if !strings.Contains(got, "default:") {
		t.Fatalf("when with else should have default, got:\n%s", got)
	}
}

// ─── compileVal — paginate + all → _total ───────────────────────────────────

func TestCompileValPaginateAllWithCount(t *testing.T) {
	models := makeModels("Post")
	c := newCompiler(models)
	c.paginate = true
	c.compileStmt(&ast.ValStmt{
		Name: "posts",
		Value: &ast.CallExpr{
			Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Post"}, Field: "all"},
			Args: []*ast.NamedArg{},
		},
	})
	out := compilerOut(c)
	if !strings.Contains(out, "_total") {
		t.Fatalf("paginate + all should generate _total, got:\n%s", out)
	}
	if !strings.Contains(out, "AllWithCount") {
		t.Fatalf("paginate + all should call AllWithCount, got:\n%s", out)
	}
}

// ─── compileTerminalMethod — paginate all, sum with column arg ──────────────

func TestCompileTerminalMethodSumWithColumn(t *testing.T) {
	models := makeModels("Order")
	c := newCompiler(models)
	got := c.compileModelChain("Order", []chainLink{
		{method: "sum", args: []*ast.NamedArg{
			{Value: &ast.LambdaExpr{
				Body: &ast.Block{Stmts: []ast.Stmt{
					&ast.ExprStmt{Expr: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "amount"}},
				}},
			}},
		}},
	})
	if !strings.Contains(got, `.Sum(ctx, "amount")`) {
		t.Fatalf("sum with lambda should extract column, got %q", got)
	}
}

// ─── resolveQueryType — more terminal methods ───────────────────────────────

func TestResolveQueryTypeAggregates(t *testing.T) {
	models := makeModels("Order")
	c := newCompiler(models)
	tests := []struct {
		method string
		want   string
	}{
		{"exists", "Boolean"},
		{"count", "Int"},
		{"update", "Int"},
		{"delete", "Int"},
		{"sum", "Int"},
		{"avg", "Int"},
		{"min", "Int"},
		{"max", "Int"},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			expr := &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Order"}, Field: tt.method},
				Args: []*ast.NamedArg{},
			}
			vt := c.resolveQueryType(expr)
			if vt.name != tt.want {
				t.Errorf("resolveQueryType(%s) = %q, want %q", tt.method, vt.name, tt.want)
			}
		})
	}
}

// ─── compileAwaitStmt — non-val statements in await ─────────────────────────

func TestCompileAwaitWithNonValStmts(t *testing.T) {
	models := makeModels("User")
	c := newCompiler(models)
	c.compileStmt(&ast.ExprStmt{Expr: &ast.AwaitExpr{
		Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ValStmt{
				Name: "user",
				Value: &ast.CallExpr{
					Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "first"},
					Args: []*ast.NamedArg{},
				},
			},
			// Non-val statement mixed in
			&ast.ExprStmt{Expr: &ast.CallExpr{
				Func: &ast.Ident{Name: "doSomething"},
				Args: []*ast.NamedArg{},
			}},
		}},
	}})
	out := compilerOut(c)
	if !strings.Contains(out, "errgroup") {
		t.Fatalf("await with val should use errgroup, got:\n%s", out)
	}
	if !strings.Contains(out, "doSomething()") {
		t.Fatalf("non-val stmt in await should be compiled after, got:\n%s", out)
	}
}

// ─── compileLoad ───────────────────────────────────────────────────────────

func TestWriteReturnByTypePaginatedList_NoTotal(t *testing.T) {
	// @paginate set but _total not assigned — should fall through to non-paginated
	c := newCompiler(nil)
	c.paginate = true
	c.hasTotalVar = false
	c.writeReturnByType("posts", valType{isModel: true, isList: true, name: "Post"})
	out := compilerOut(c)
	// Should write columnar but NOT append _total
	if !strings.Contains(out, "WriteColumnarPost(req.Buf, posts, req.FieldMask)") {
		t.Fatalf("missing WriteColumnar, got:\n%s", out)
	}
	if strings.Contains(out, "_total") {
		t.Fatalf("should not reference _total when hasTotalVar is false, got:\n%s", out)
	}
}

func TestCompileLoad_PK(t *testing.T) {
	c := newCompiler(makeModels("User"))
	var b strings.Builder
	c.compileLoad(&b, "User", []*ast.NamedArg{
		{Value: &ast.Ident{Name: "userId"}},
	})
	got := b.String()
	if !strings.Contains(got, "app.loaders.ExtendUser.Load(ctx, userId, nil)") {
		t.Errorf("PK load: got %q", got)
	}
}

func TestCompileLoad_SingleFK(t *testing.T) {
	c := newCompiler(makeModels("Post"))
	var b strings.Builder
	c.compileLoad(&b, "Post", []*ast.NamedArg{
		{Name: "userId", Value: &ast.Ident{Name: "uid"}},
	})
	got := b.String()
	if !strings.Contains(got, "app.loaders.PostByUserId.Load(ctx, uid, nil)") {
		t.Errorf("FK load: got %q", got)
	}
}

func TestCompileLoad_CompositeKey(t *testing.T) {
	c := newCompiler(makeModels("Post"))
	var b strings.Builder
	c.compileLoad(&b, "Post", []*ast.NamedArg{
		{Name: "userId", Value: &ast.Ident{Name: "uid"}},
		{Name: "type", Value: &ast.Literal{Kind: token.String, Value: "article"}},
	})
	got := b.String()
	if !strings.Contains(got, "PostByUserIdAndType") {
		t.Errorf("composite load: got %q", got)
	}
	if !strings.Contains(got, "PostByUserIdAndTypeKey{") {
		t.Errorf("composite key struct: got %q", got)
	}
}

func TestCompileLoad_Empty(t *testing.T) {
	c := newCompiler(nil)
	var b strings.Builder
	c.compileLoad(&b, "User", nil)
	if b.Len() != 0 {
		t.Error("empty args should produce no output")
	}
}

// ─── resolveQueryType ──────────────────────────────────────────────────────

func TestResolveQueryType_Load_PK(t *testing.T) {
	c := newCompiler(makeModels("User"))
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "load"},
		Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "id"}}},
	}
	vt := c.resolveQueryType(expr)
	if !vt.isModel || vt.name != "User" {
		t.Errorf("PK load should return model User, got %+v", vt)
	}
	if vt.isList {
		t.Error("PK load should not be a list")
	}
}

func TestResolveQueryType_Load_FK(t *testing.T) {
	c := newCompiler(makeModels("Post"))
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Post"}, Field: "load"},
		Args: []*ast.NamedArg{{Name: "userId", Value: &ast.Ident{Name: "uid"}}},
	}
	vt := c.resolveQueryType(expr)
	if !vt.isModel || vt.name != "Post" {
		t.Errorf("FK load should return model Post, got %+v", vt)
	}
	if !vt.isList {
		t.Error("FK load should be a list")
	}
}

func TestResolveQueryType_Exists(t *testing.T) {
	c := newCompiler(makeModels("User"))
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
				Args: []*ast.NamedArg{{Name: "id", Value: &ast.Literal{Kind: token.Int, Value: "1"}}},
			},
			Field: "exists",
		},
	}
	vt := c.resolveQueryType(expr)
	if vt.name != "Boolean" {
		t.Errorf("exists should return Boolean, got %+v", vt)
	}
}

func TestResolveQueryType_Count(t *testing.T) {
	c := newCompiler(makeModels("User"))
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
			},
			Field: "count",
		},
	}
	vt := c.resolveQueryType(expr)
	if vt.name != "Int" {
		t.Errorf("count should return Int, got %+v", vt)
	}
}

func TestResolveQueryType_Sum(t *testing.T) {
	c := newCompiler(makeModels("Order"))
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "Order"},
			Field:  "sum",
		},
		Args: []*ast.NamedArg{{Value: &ast.Literal{Value: "amount"}}},
	}
	vt := c.resolveQueryType(expr)
	if vt.name != "Int" {
		t.Errorf("sum should return Int, got %+v", vt)
	}
}

func TestResolveQueryType_Update(t *testing.T) {
	c := newCompiler(makeModels("User"))
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
			},
			Field: "update",
		},
	}
	vt := c.resolveQueryType(expr)
	if vt.name != "Int" {
		t.Errorf("update should return Int, got %+v", vt)
	}
}

func TestResolveQueryType_Delete(t *testing.T) {
	c := newCompiler(makeModels("User"))
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "where"},
			},
			Field: "delete",
		},
	}
	vt := c.resolveQueryType(expr)
	if vt.name != "Int" {
		t.Errorf("delete should return Int, got %+v", vt)
	}
}

func TestResolveQueryType_NonModel(t *testing.T) {
	c := newCompiler(nil)
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "notAModel"}, Field: "find"},
	}
	vt := c.resolveQueryType(expr)
	if vt.isModel || vt.name != "" {
		t.Errorf("non-model should return empty, got %+v", vt)
	}
}

func TestResolveQueryType_ShortChain(t *testing.T) {
	c := newCompiler(nil)
	vt := c.resolveQueryType(&ast.Ident{Name: "x"})
	if vt.isModel || vt.name != "" {
		t.Errorf("short chain should return empty, got %+v", vt)
	}
}

// ─── String method compilation tests ─────────────────────────────────────────

func TestCompileStringTransformAll(t *testing.T) {
	tests := []struct {
		method string
		nargs  int
		args   []string
		want   string
	}{
		{"lowercase", 0, nil, "strings.ToLower(s)"},
		{"uppercase", 0, nil, "strings.ToUpper(s)"},
		{"trim", 0, nil, "strings.TrimSpace(s)"},
		{"trimStart", 0, nil, `strings.TrimLeft(s, " ")`},
		{"trimStart", 1, []string{`"-"`}, `strings.TrimLeft(s, "-")`},
		{"trimEnd", 0, nil, `strings.TrimRight(s, " ")`},
		{"trimEnd", 1, []string{`"-"`}, `strings.TrimRight(s, "-")`},
		{"reversed", 0, nil, "str.Reverse(s)"},
		{"replace", 2, []string{`"a"`, `"b"`}, `strings.ReplaceAll(s, "a", "b")`},
		{"replaceAll", 2, []string{`"a"`, `"b"`}, `strings.ReplaceAll(s, "a", "b")`},
		{"substring", 2, []string{"1", "5"}, "s[1:5]"},
		{"substring", 1, []string{"3"}, "s[3:]"},
		{"repeat", 1, []string{"3"}, "strings.Repeat(s, int(3))"},
		{"padStart", 2, []string{"10", `" "`}, `str.PadLeft(s, int(10), " ")`},
		{"padEnd", 2, []string{"10", `" "`}, `str.PadRight(s, int(10), " ")`},
		{"split", 1, []string{`","`}, `strings.Split(s, ",")`},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			arg := func(i int) string {
				if i < len(tt.args) {
					return tt.args[i]
				}
				return ""
			}
			got := compileStringTransform(tt.method, "s", arg, tt.nargs)
			if got != tt.want {
				t.Errorf("compileStringTransform(%q) = %q, want %q", tt.method, got, tt.want)
			}
		})
	}
}

func TestCompileStringQueryAll(t *testing.T) {
	tests := []struct {
		method string
		nargs  int
		args   []string
		want   string
	}{
		{"contains", 1, []string{`"x"`}, `strings.Contains(s, "x")`},
		{"startsWith", 1, []string{`"pre"`}, `strings.HasPrefix(s, "pre")`},
		{"endsWith", 1, []string{`".go"`}, `strings.HasSuffix(s, ".go")`},
		{"isEmpty", 0, nil, "(len(s) == 0)"},
		{"matches", 1, []string{`"^[a-z]+$"`}, `str.Matches("^[a-z]+$", s)`},
		{"length", 0, nil, "int64(len(s))"},
		{"size", 0, nil, "int64(len(s))"},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			arg := func(i int) string {
				if i < len(tt.args) {
					return tt.args[i]
				}
				return ""
			}
			got := compileStringQuery(tt.method, "s", arg, tt.nargs)
			if got != tt.want {
				t.Errorf("compileStringQuery(%q) = %q, want %q", tt.method, got, tt.want)
			}
		})
	}
}

func TestCompileStringConvertAll(t *testing.T) {
	if got := compileStringConvert("toInt", "s"); got != `convert.StringToInt(s)` {
		t.Errorf("toInt = %q", got)
	}
	if got := compileStringConvert("toFloat", "s"); got != `convert.StringToFloat(s)` {
		t.Errorf("toFloat = %q", got)
	}
	if got := compileStringConvert("unknown", "s"); got != "" {
		t.Errorf("unknown should return empty, got %q", got)
	}
}

func TestCompileFieldExpr_ChainedMethods(t *testing.T) {
	// it.trim().lowercase() → strings.ToLower(strings.TrimSpace(varName))
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.CallExpr{
				Func: &ast.MemberExpr{
					Object: &ast.Ident{Name: "it"},
					Field:  "trim",
				},
			},
			Field: "lowercase",
		},
	}
	got := compileFieldExpr(expr, "nameVal")
	if got != "strings.ToLower(strings.TrimSpace(nameVal))" {
		t.Errorf("chained = %q", got)
	}
}

func TestCompileFieldExpr_ItIdent(t *testing.T) {
	got := compileFieldExpr(&ast.Ident{Name: "it"}, "val")
	if got != "val" {
		t.Errorf("it → %q, want val", got)
	}
}

func TestCompileFieldExpr_Literal(t *testing.T) {
	got := compileFieldExpr(&ast.Literal{Kind: token.String, Value: "hello"}, "v")
	if got != `"hello"` {
		t.Errorf("literal = %q", got)
	}
	got = compileFieldExpr(&ast.Literal{Kind: token.Int, Value: "42"}, "v")
	if got != "42" {
		t.Errorf("int literal = %q", got)
	}
}

func TestCompileMyField(t *testing.T) {
	if got := compileMyField("id", "identity"); got != "api.IdentityID(identity)" {
		t.Errorf("my.id = %q", got)
	}
	if got := compileMyField("role", "identity"); got != `api.IdentityInt(identity, "role")` {
		t.Errorf("my.role = %q", got)
	}
}

func TestInferMyFieldType(t *testing.T) {
	myRole := &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "role"}
	// String literal → IdentityString
	compiled := `api.IdentityInt(identity, "role")`
	got := inferMyFieldType(compiled, myRole, &ast.Literal{Kind: token.String, Value: "admin"}, "identity")
	if !strings.Contains(got, "IdentityString") {
		t.Errorf("string context should use IdentityString, got %q", got)
	}
	// Int literal → stays IdentityInt
	got = inferMyFieldType(compiled, myRole, &ast.Literal{Kind: token.Int, Value: "5"}, "identity")
	if !strings.Contains(got, "IdentityInt") {
		t.Errorf("int context should use IdentityInt, got %q", got)
	}
	// Non-my expr → unchanged
	got = inferMyFieldType("something", &ast.Ident{Name: "x"}, &ast.Literal{Kind: token.String, Value: "a"}, "identity")
	if got != "something" {
		t.Errorf("non-my should be unchanged, got %q", got)
	}
}

// ─── compileStringMethod integration (dispatches to sub-functions) ──────────

func TestCompileStringMethod_ViaCallExpr(t *testing.T) {
	c := newCompiler(nil)
	// s.lowercase() → strings.ToLower(s)
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "s"}, Field: "lowercase"},
	}
	got := c.compileStringMethod(expr)
	if got != "strings.ToLower(s)" {
		t.Errorf("got %q", got)
	}
}

func TestCompileStringMethod_NotMember(t *testing.T) {
	c := newCompiler(nil)
	// Plain function call → not a string method
	expr := &ast.CallExpr{Func: &ast.Ident{Name: "foo"}}
	got := c.compileStringMethod(expr)
	if got != "" {
		t.Errorf("non-member should return empty, got %q", got)
	}
}

func TestCompileStringMethod_Unknown(t *testing.T) {
	c := newCompiler(nil)
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "s"}, Field: "unknownMethod"},
	}
	got := c.compileStringMethod(expr)
	if got != "" {
		t.Errorf("unknown method should return empty, got %q", got)
	}
}

func TestCompileStringMethod_Contains(t *testing.T) {
	c := newCompiler(nil)
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "s"}, Field: "contains"},
		Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.String, Value: "x"}}},
	}
	got := c.compileStringMethod(expr)
	if !strings.Contains(got, "strings.Contains") {
		t.Errorf("got %q", got)
	}
}

func TestCompileStringMethod_ToInt(t *testing.T) {
	c := newCompiler(nil)
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "s"}, Field: "toInt"},
	}
	got := c.compileStringMethod(expr)
	if !strings.Contains(got, "convert.StringToInt") {
		t.Errorf("got %q", got)
	}
}

func TestCompileFieldExpr_MemberExpr(t *testing.T) {
	got := compileFieldExpr(&ast.MemberExpr{
		Object: &ast.Ident{Name: "it"},
		Field:  "name",
	}, "val")
	if got != "val.Name" {
		t.Errorf("it.name = %q", got)
	}
}

func TestCompileFieldExpr_OtherIdent(t *testing.T) {
	got := compileFieldExpr(&ast.Ident{Name: "other"}, "val")
	if got != "other" {
		t.Errorf("other ident = %q", got)
	}
}

func TestCompileFieldExpr_QueryMethod(t *testing.T) {
	// it.isEmpty() → (len(val) == 0)
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "isEmpty"},
	}
	got := compileFieldExpr(expr, "val")
	if got != "(len(val) == 0)" {
		t.Errorf("isEmpty = %q", got)
	}
}

func TestCompileFieldExpr_ConvertMethod(t *testing.T) {
	// it.toInt() → convert.StringToInt(val)
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "toInt"},
	}
	got := compileFieldExpr(expr, "val")
	if got != "convert.StringToInt(val)" {
		t.Errorf("toInt = %q", got)
	}
}

func TestCompileFieldExpr_Unsupported(t *testing.T) {
	got := compileFieldExpr(&ast.BinaryExpr{Left: &ast.Ident{Name: "a"}, Op: "+", Right: &ast.Ident{Name: "b"}}, "val")
	if got != "" {
		t.Errorf("unsupported should be empty, got %q", got)
	}
}

func TestCompileFieldExpr_CallNonMember(t *testing.T) {
	// foo() — CallExpr with Ident func (not MemberExpr)
	expr := &ast.CallExpr{Func: &ast.Ident{Name: "foo"}}
	got := compileFieldExpr(expr, "val")
	if got != "" {
		t.Errorf("non-member call should return empty, got %q", got)
	}
}

func TestCompileFieldExpr_QueryViaCall(t *testing.T) {
	// it.contains("x") — query method in field context
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "contains"},
		Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.String, Value: "x"}}},
	}
	got := compileFieldExpr(expr, "val")
	if !strings.Contains(got, "strings.Contains") {
		t.Errorf("contains = %q", got)
	}
}

func TestCompileFieldExpr_ConvertViaCall(t *testing.T) {
	// it.toFloat() — convert method in field context
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "toFloat"},
	}
	got := compileFieldExpr(expr, "val")
	if !strings.Contains(got, "convert.StringToFloat") {
		t.Errorf("toFloat = %q", got)
	}
}

func TestCompileFieldExpr_UnknownMethod(t *testing.T) {
	// it.unknownMethod() — falls through all three dispatchers
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "unknownMethod"},
	}
	got := compileFieldExpr(expr, "val")
	if got != "" {
		t.Errorf("unknown method should return empty, got %q", got)
	}
}

func TestCompileFieldExpr_ArgFallback(t *testing.T) {
	// it.replace("a", "b") — tests arg() helper inside compileFieldExpr
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "replace"},
		Args: []*ast.NamedArg{
			{Value: &ast.Literal{Kind: token.String, Value: "a"}},
			{Value: &ast.Literal{Kind: token.String, Value: "b"}},
		},
	}
	got := compileFieldExpr(expr, "val")
	if !strings.Contains(got, "strings.ReplaceAll") {
		t.Errorf("replace = %q", got)
	}
}

func TestCompileFieldExpr_MemberNonIt(t *testing.T) {
	// other.field — MemberExpr with non-it object
	got := compileFieldExpr(&ast.MemberExpr{
		Object: &ast.Ident{Name: "other"},
		Field:  "name",
	}, "val")
	if got != "" {
		t.Errorf("non-it member should return empty, got %q", got)
	}
}

func TestCompileBangElvisGuard(t *testing.T) {
	c := newCompiler(nil)
	c.indent = "\t\t"
	e := &ast.BangElvisExpr{
		Left:  &ast.Ident{Name: "exists"},
		Right: &ast.Ident{Name: "AlreadySetup"},
	}
	c.compileBangElvisGuard(e)
	out := c.b.String()
	if !strings.Contains(out, "if exists {") {
		t.Errorf("should check if true: %s", out)
	}
	if !strings.Contains(out, "NewAlreadySetup()") {
		t.Errorf("should compile throw: %s", out)
	}
}

func TestCompileBangElvisExpr(t *testing.T) {
	c := newCompiler(nil)
	got := c.compileExpr(&ast.BangElvisExpr{
		Left: &ast.Ident{Name: "x"},
	})
	if !strings.Contains(got, "!:") {
		t.Errorf("expr should contain !: marker, got %q", got)
	}
}

func TestCompileElvisGuard_BoolMethod(t *testing.T) {
	c := newCompiler(nil)
	c.indent = "\t\t"
	e := &ast.ElvisExpr{
		Left: &ast.CallExpr{
			Func: &ast.MemberExpr{Object: &ast.Ident{Name: "member"}, Field: "verifyPassword"},
			Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "password"}}},
		},
		Right: &ast.Ident{Name: "InvalidCredentials"},
	}
	c.compileElvisGuard(e)
	out := c.b.String()
	if !strings.Contains(out, "!member.VerifyPassword") {
		t.Errorf("bool ?: should generate if !val: %s", out)
	}
}

func TestCompileElvisGuard_ErrorReturningBool(t *testing.T) {
	c := newCompiler(nil)
	c.indent = "\t\t"
	e := &ast.ElvisExpr{
		Left: &ast.CallExpr{
			Func: &ast.MemberExpr{Object: &ast.Ident{Name: "app"}, Field: "exists"},
			Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "ctx"}}},
		},
		Right: &ast.Ident{Name: "NotFound"},
	}
	c.compileElvisGuard(e)
	out := c.b.String()
	if !strings.Contains(out, "_ok, _err :=") {
		t.Errorf("should generate _ok, _err: %s", out)
	}
	if !strings.Contains(out, "if !_ok") {
		t.Errorf("should check !_ok: %s", out)
	}
}

func TestCompileBangElvisGuard_ErrorReturning(t *testing.T) {
	c := newCompiler(nil)
	c.indent = "\t\t"
	e := &ast.BangElvisExpr{
		Left: &ast.CallExpr{
			Func: &ast.MemberExpr{Object: &ast.Ident{Name: "app"}, Field: "exists"},
			Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "ctx"}}},
		},
		Right: &ast.Ident{Name: "AlreadySetup"},
	}
	c.compileBangElvisGuard(e)
	out := c.b.String()
	if !strings.Contains(out, "_ok, _err :=") {
		t.Errorf("should generate _ok, _err: %s", out)
	}
	if !strings.Contains(out, "if _ok {") {
		t.Errorf("should check if _ok: %s", out)
	}
}

func TestIsErrorReturningBool(t *testing.T) {
	if !isErrorReturningBool(&ast.CallExpr{Func: &ast.MemberExpr{Field: "exists"}}) {
		t.Error("exists should be error-returning")
	}
	if isErrorReturningBool(&ast.CallExpr{Func: &ast.MemberExpr{Field: "verifyPassword"}}) {
		t.Error("verifyPassword should NOT be error-returning")
	}
	if isErrorReturningBool(&ast.Ident{Name: "x"}) {
		t.Error("ident should not be error-returning")
	}
	if isErrorReturningBool(&ast.CallExpr{Func: &ast.Ident{Name: "foo"}}) {
		t.Error("non-member should not be error-returning")
	}
}

func TestIsBoolExpr(t *testing.T) {
	for _, name := range []string{"verifyPassword", "contains", "startsWith", "endsWith", "isEmpty", "matches"} {
		if !isBoolExpr(&ast.CallExpr{Func: &ast.MemberExpr{Field: name}}) {
			t.Errorf("%s should be bool", name)
		}
	}
	if isBoolExpr(&ast.CallExpr{Func: &ast.MemberExpr{Field: "find"}}) {
		t.Error("find should not be bool")
	}
	if isBoolExpr(&ast.Ident{Name: "x"}) {
		t.Error("ident should not be bool")
	}
}

// ─── Duration arithmetic ────────────────────────────────────────────────────

func TestCompileDurationProperties(t *testing.T) {
	c := newCompiler(nil)
	tests := []struct {
		field string
		want  string
	}{
		{"days", "(time.Duration(7) * 24 * time.Hour)"},
		{"hours", "(time.Duration(7) * time.Hour)"},
		{"minutes", "(time.Duration(7) * time.Minute)"},
		{"seconds", "(time.Duration(7) * time.Second)"},
		{"milliseconds", "(time.Duration(7) * time.Millisecond)"},
	}
	for _, tt := range tests {
		expr := &ast.MemberExpr{
			Object: &ast.Literal{Kind: token.Int, Value: "7"},
			Field:  tt.field,
		}
		// Simulate semantic analyzer setting TypeTag
		expr.SetTypeTag("Duration")
		got := c.compileExpr(expr)
		if got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.field, got, tt.want)
		}
	}
}

func TestCompileNow(t *testing.T) {
	c := newCompiler(nil)
	expr := &ast.CallExpr{
		Func: &ast.Ident{Name: "now"},
	}
	got := c.compileExpr(expr)
	if got != "time.Now()" {
		t.Errorf("now(): got %q, want %q", got, "time.Now()")
	}
}

func TestCompileDateTimePlusDuration(t *testing.T) {
	c := newCompiler(nil)
	dur := &ast.MemberExpr{Object: &ast.Literal{Kind: token.Int, Value: "7"}, Field: "days"}
	dur.SetTypeTag("Duration")
	nowCall := &ast.CallExpr{Func: &ast.Ident{Name: "now"}}
	nowCall.SetTypeTag("DateTime")
	expr := &ast.BinaryExpr{
		Left:  nowCall,
		Op:    "+",
		Right: dur,
	}
	got := c.compileExpr(expr)
	if !strings.Contains(got, ".Add(") {
		t.Errorf("DateTime + Duration should use .Add(): got %q", got)
	}
	if strings.Contains(got, ".Add(-") {
		t.Error("+ should not negate")
	}
}

func TestCompileDateTimeMinusDuration(t *testing.T) {
	c := newCompiler(nil)
	dur := &ast.MemberExpr{Object: &ast.Literal{Kind: token.Int, Value: "7"}, Field: "days"}
	dur.SetTypeTag("Duration")
	nowCall := &ast.CallExpr{Func: &ast.Ident{Name: "now"}}
	nowCall.SetTypeTag("DateTime")
	expr := &ast.BinaryExpr{
		Left:  nowCall,
		Op:    "-",
		Right: dur,
	}
	got := c.compileExpr(expr)
	if !strings.Contains(got, ".Add(-") {
		t.Errorf("DateTime - Duration should use .Add(-): got %q", got)
	}
}

func TestIsDurationExpr(t *testing.T) {
	// TypeTag-based
	tagged := &ast.MemberExpr{Field: "days"}
	tagged.SetTypeTag("Duration")
	if !isDurationExpr(tagged) {
		t.Error("Duration-tagged should be duration")
	}
	// Untagged member — not duration
	if isDurationExpr(&ast.MemberExpr{Field: "days"}) {
		t.Error("untagged member should not be duration")
	}
	// Duration literal — always duration
	if !isDurationExpr(&ast.Literal{Kind: token.Duration, Value: "5m"}) {
		t.Error("5m literal should be duration")
	}
	// Int literal — not duration
	if isDurationExpr(&ast.Literal{Kind: token.Int, Value: "42"}) {
		t.Error("int literal should not be duration")
	}
}

func TestDurationTypeTagGuard(t *testing.T) {
	c := newCompiler(nil)
	c.enums = map[string]bool{}

	// Without TypeTag: Model.days → normal field access
	expr := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Project"},
		Field:  "days",
	}
	got := c.compileExpr(expr)
	if strings.Contains(got, "time.Duration") {
		t.Errorf("untagged Model.days should not be duration: got %q", got)
	}

	// Without TypeTag: user.days → normal field access
	expr2 := &ast.MemberExpr{
		Object: &ast.MemberExpr{Object: &ast.Ident{Name: "project"}, Field: "retention"},
		Field:  "days",
	}
	got2 := c.compileExpr(expr2)
	if strings.Contains(got2, "time.Duration") {
		t.Errorf("untagged chain.days should not be duration: got %q", got2)
	}

	// With TypeTag: n.days → duration
	expr3 := &ast.MemberExpr{
		Object: &ast.Ident{Name: "n"},
		Field:  "days",
	}
	expr3.SetTypeTag("Duration")
	got3 := c.compileExpr(expr3)
	if !strings.Contains(got3, "time.Duration") {
		t.Errorf("tagged n.days should be duration: got %q", got3)
	}
}

// ─── GroupBy ────────────────────────────────────────────────────────────────

func TestFindGroupByLink(t *testing.T) {
	links := []chainLink{
		{method: "where"},
		{method: "groupBy"},
		{method: "select"},
	}
	idx := findGroupByLink(links)
	if idx != 1 {
		t.Errorf("expected 1, got %d", idx)
	}
	if findGroupByLink([]chainLink{{method: "where"}, {method: "all"}}) != -1 {
		t.Error("should be -1 when no groupBy")
	}
}

func TestExtractGroupByCols(t *testing.T) {
	c := newCompiler(nil)
	// Single field: groupBy { it.apiName }
	link := chainLink{
		method: "groupBy",
		args: []*ast.NamedArg{{Value: &ast.LambdaExpr{
			Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.ExprStmt{Expr: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "apiName"}},
			}},
		}}},
	}
	cols := c.extractGroupByCols(link)
	if len(cols) != 1 || cols[0] != `"api_name"` {
		t.Errorf("expected [\"api_name\"], got %v", cols)
	}
}

func TestIsGroupKeyRef(t *testing.T) {
	if !isGroupKeyRef(&ast.MemberExpr{Field: "key"}) {
		t.Error("it.key should be key ref")
	}
	if isGroupKeyRef(&ast.MemberExpr{Field: "count"}) {
		t.Error("it.count should not be key ref")
	}
}

func TestExtractSelectAggs(t *testing.T) {
	c := newCompiler(nil)
	// .select { count: it.count(), totalSum: it.sum { it.total } }
	link := chainLink{
		method: "select",
		args: []*ast.NamedArg{{Value: &ast.LambdaExpr{
			Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.ExprStmt{Expr: &ast.ObjectExpr{
					Fields: []*ast.NamedArg{
						{Name: "apiName", Value: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "key"}},
						{Name: "count", Value: &ast.CallExpr{
							Func: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "count"},
						}},
						{Name: "totalSum", Value: &ast.CallExpr{
							Func: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "sum"},
							Args: []*ast.NamedArg{{Value: &ast.LambdaExpr{
								Body: &ast.Block{Stmts: []ast.Stmt{
									&ast.ExprStmt{Expr: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "total"}},
								}},
							}}},
						}},
					},
				}},
			}},
		}}},
	}
	aggs := c.extractSelectAggs(link)
	if len(aggs) != 2 {
		t.Fatalf("expected 2 aggs, got %d: %v", len(aggs), aggs)
	}
	if !strings.Contains(aggs[0], "COUNT") {
		t.Errorf("first agg should be COUNT: %s", aggs[0])
	}
	if !strings.Contains(aggs[1], "SUM") || !strings.Contains(aggs[1], "total") {
		t.Errorf("second agg should be SUM(total): %s", aggs[1])
	}
}

func TestCompileGroupByChainWithWhere(t *testing.T) {
	models := makeModels("Order")
	c := newCompiler(models)
	// Order.where(...).groupBy { it.status }.select { count: it.count() }
	got := c.compileModelChain("Order", []chainLink{
		{method: "where", args: []*ast.NamedArg{{Value: &ast.BinaryExpr{
			Left: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "total"},
			Op:   ">", Right: &ast.Literal{Kind: token.Int, Value: "100"},
		}}}},
		{method: "groupBy", args: []*ast.NamedArg{{Value: &ast.LambdaExpr{
			Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.ExprStmt{Expr: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "status"}},
			}},
		}}}},
		{method: "select", args: []*ast.NamedArg{{Value: &ast.LambdaExpr{
			Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.ExprStmt{Expr: &ast.ObjectExpr{
					Fields: []*ast.NamedArg{
						{Name: "status", Value: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "key"}},
						{Name: "count", Value: &ast.CallExpr{
							Func: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "count"},
						}},
					},
				}},
			}},
		}}}},
	})
	if !strings.Contains(got, "GroupBy(ctx") {
		t.Errorf("should call GroupBy: got %q", got)
	}
	if !strings.Contains(got, `"status"`) {
		t.Errorf("should contain group column: got %q", got)
	}
	if !strings.Contains(got, "COUNT") {
		t.Errorf("should contain COUNT agg: got %q", got)
	}
}

func TestExtractGroupByColsList(t *testing.T) {
	c := newCompiler(nil)
	// groupBy { [it.status, it.apiName] } — multi-column
	link := chainLink{
		method: "groupBy",
		args: []*ast.NamedArg{{Value: &ast.LambdaExpr{
			Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.ExprStmt{Expr: &ast.ListExpr{Items: []ast.Expr{
					&ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "status"},
					&ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "apiName"},
				}}},
			}},
		}}},
	}
	cols := c.extractGroupByCols(link)
	if len(cols) != 2 {
		t.Fatalf("expected 2 cols, got %d: %v", len(cols), cols)
	}
}

func TestExtractGroupByColsEmpty(t *testing.T) {
	c := newCompiler(nil)
	cols := c.extractGroupByCols(chainLink{method: "groupBy"})
	if len(cols) != 0 {
		t.Errorf("empty args should return nil: got %v", cols)
	}
}

func TestCompileBuiltinCryptoRandomBytes(t *testing.T) {
	c := newCompiler(nil)
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "crypto"},
			Field:  "randomBytes",
		},
		Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Int, Value: "16"}}},
	}
	got := c.compileExpr(expr)
	if !strings.Contains(got, "luxocrypto.RandomBytes(16)") {
		t.Errorf("crypto.randomBytes: got %q", got)
	}
	if strings.Contains(got, "hex.EncodeToString") {
		t.Error("randomBytes should NOT use hex encoding")
	}
}

func TestCompileBuiltinNonCrypto(t *testing.T) {
	c := newCompiler(nil)
	// crypto.unknownMethod → should return empty (not a builtin)
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "crypto"},
			Field:  "unknownMethod",
		},
	}
	got := c.compileBuiltinCall(expr)
	if got != "" {
		t.Errorf("unknown crypto method should return empty: got %q", got)
	}
	// Non-crypto member → should return empty
	expr2 := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "math"},
			Field:  "abs",
		},
	}
	got2 := c.compileBuiltinCall(expr2)
	if got2 != "" {
		t.Errorf("non-crypto member should return empty: got %q", got2)
	}
	// Simple ident (not now) → should return empty
	expr3 := &ast.CallExpr{Func: &ast.Ident{Name: "print"}}
	got3 := c.compileBuiltinCall(expr3)
	if got3 != "" {
		t.Errorf("print should return empty: got %q", got3)
	}
}

func TestCompileCryptoRandomHex(t *testing.T) {
	c := newCompiler(nil)
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{
			Object: &ast.Ident{Name: "crypto"},
			Field:  "randomHex",
		},
		Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Int, Value: "32"}}},
	}
	got := c.compileExpr(expr)
	if !strings.Contains(got, "luxocrypto.RandomBytes(32)") {
		t.Errorf("crypto.randomHex: got %q", got)
	}
	if !strings.Contains(got, "hex.EncodeToString") {
		t.Errorf("should use hex.EncodeToString: got %q", got)
	}
}

// ─── Instance methods ─────────────────────────────────────────────────────────

func TestCompileInstanceDelete(t *testing.T) {
	models := makeModels("Project")
	c := newCompiler(models)
	c.vars["project"] = valType{isModel: true, name: "Project"}
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "project"}, Field: "delete"},
	}
	got := c.compileExpr(expr)
	if !strings.Contains(got, "app.Project.Where(ProjectWhere.Id.Eq(project.Id)).Delete(ctx)") {
		t.Errorf("instance delete: got %q", got)
	}
}

func TestCompileInstanceUpdate(t *testing.T) {
	models := makeModels("Project")
	c := newCompiler(models)
	c.vars["project"] = valType{isModel: true, name: "Project"}
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "project"}, Field: "update"},
		Args: []*ast.NamedArg{
			{Name: "name", Value: &ast.Literal{Kind: token.String, Value: "new"}},
		},
	}
	got := c.compileExpr(expr)
	if !strings.Contains(got, "app.Project.Where(ProjectWhere.Id.Eq(project.Id)).Update()") {
		t.Errorf("instance update: got %q", got)
	}
	if !strings.Contains(got, `.SetName("new")`) {
		t.Errorf("instance update missing SetName: got %q", got)
	}
}

func TestCompileInstanceMethodNonModel(t *testing.T) {
	c := newCompiler(nil)
	// Non-model variable → should not match
	expr := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "str"}, Field: "delete"},
	}
	got := c.compileInstanceMethod(expr)
	if got != "" {
		t.Errorf("non-model should return empty: got %q", got)
	}
}

func TestCompileInstanceMethodNonMember(t *testing.T) {
	c := newCompiler(nil)
	// Direct call (not member) → should not match
	expr := &ast.CallExpr{Func: &ast.Ident{Name: "delete"}}
	got := c.compileInstanceMethod(expr)
	if got != "" {
		t.Errorf("non-member should return empty: got %q", got)
	}
}

// ─── isTypeDecl ────────────────────────────────────────────────────────────────

func TestIsTypeDeclVariants(t *testing.T) {
	c := newCompiler(nil)
	c.enums = map[string]bool{"Status": true}

	// Enum → not a type
	if c.isTypeDecl("Status") {
		t.Error("enum should not be type")
	}
	// Primitive → not a type
	if c.isTypeDecl("Int") {
		t.Error("Int should not be type")
	}
	// Empty → not a type
	if c.isTypeDecl("") {
		t.Error("empty should not be type")
	}
	// lowercase → not a type
	if c.isTypeDecl("result") {
		t.Error("lowercase should not be type")
	}
	// PascalCase unknown → IS a type
	if !c.isTypeDecl("AuthPayload") {
		t.Error("AuthPayload should be type")
	}
	// With explicit types map
	c.types = map[string]bool{"MyType": true}
	if !c.isTypeDecl("MyType") {
		t.Error("MyType in types map should be type")
	}
	if c.isTypeDecl("OtherType") {
		t.Error("OtherType not in types map should not be type")
	}
}

// ─── deleteMany terminal ───────────────────────────────────────────────────────

func TestCompileDeleteManyTerminal(t *testing.T) {
	models := makeModels("Trace")
	c := newCompiler(models)
	got := c.compileModelChain("Trace", []chainLink{
		{method: "where", args: []*ast.NamedArg{{Value: &ast.BinaryExpr{
			Left: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "projectId"},
			Op:   "==", Right: &ast.Literal{Kind: token.Int, Value: "1"},
		}}}},
		{method: "deleteMany"},
	})
	if !strings.Contains(got, ".Delete(ctx)") {
		t.Errorf("deleteMany should compile to .Delete(ctx): got %q", got)
	}
}

// ─── compileObject TypeName ─────────────────────────────────────────────────────

func TestCompileObjectWithTypeName(t *testing.T) {
	c := newCompiler(nil)
	expr := &ast.ObjectExpr{
		TypeName: "AuthPayload",
		Fields: []*ast.NamedArg{
			{Name: "token", Value: &ast.Literal{Kind: token.String, Value: "abc"}},
		},
	}
	got := c.compileExpr(expr)
	if !strings.Contains(got, "AuthPayload{") {
		t.Errorf("should prefix with TypeName: got %q", got)
	}
}

func TestCompileObjectWithoutTypeName(t *testing.T) {
	c := newCompiler(nil)
	c.api = &ast.ApiDecl{ReturnType: &ast.TypeRef{Name: "MyResult"}}
	expr := &ast.ObjectExpr{
		Fields: []*ast.NamedArg{
			{Name: "value", Value: &ast.Literal{Kind: token.Int, Value: "1"}},
		},
	}
	got := c.compileExpr(expr)
	// Should infer type from api return type
	if !strings.Contains(got, "MyResult{") {
		t.Errorf("should infer TypeName from api return: got %q", got)
	}
}
