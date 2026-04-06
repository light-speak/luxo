package semantic

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== New CRUD Operations ==========

func TestDeleteMany(t *testing.T) {
	result := analyze(t, `
model Order {
  id: Int @id
  status: String @index
}
api cleanup(): Int @auth {
  Order.deleteMany(where: status == "cancelled")
}`)
	expectNoErrors(t, result)
}

func TestUpdateMany(t *testing.T) {
	result := analyze(t, `
model Product {
  id: Int @id
  price: Float
  active: Boolean @index
}
api deactivateAll(): Int @auth {
  Product.updateMany(where: active == true, active: false)
}`)
	expectNoErrors(t, result)
}

func TestCreateMany(t *testing.T) {
	result := analyze(t, `
model Tag {
  id: Int @id
  name: String
}
api batchTags(names: [String]): [Tag] @auth {
  Tag.createMany(names)
}`)
	expectNoErrors(t, result)
}

func TestCount(t *testing.T) {
	result := analyze(t, `
model User {
  id: Int @id
  role: String @index
}
api countAdmins(): Int {
  User.count(where: role == "admin")
}`)
	expectNoErrors(t, result)
}

func TestExists(t *testing.T) {
	result := analyze(t, `
model User {
  id: Int @id
  email: String @unique
}
api emailTaken(email: String): Boolean {
  User.exists(where: it.email == email)
}`)
	expectNoErrors(t, result)
}

func TestUpsert(t *testing.T) {
	result := analyze(t, `
model Setting {
  id: Int @id
  key: String @unique
  value: String
}
api setSetting(key: String, value: String): Setting {
  Setting.upsert(where: it.key == key, key: key, value: value)
}`)
	expectNoErrors(t, result)
}

func TestFindFirst(t *testing.T) {
	result := analyze(t, `
model Post {
  id: Int @id
  title: String @index
}
api getFirst(): String {
  val post = Post.findFirst(where: title == "hello")
  post?.title ?: "none"
}`)
	expectNoErrors(t, result)
}

func TestFindMany(t *testing.T) {
	result := analyze(t, `
model Post {
  id: Int @id
  status: String @index
}
api listPublished(): [Post] {
  Post.findMany(where: status == "published")
}`)
	expectNoErrors(t, result)
}

// ========== Query Optimization Warnings ==========

func TestQueryNoIndexWarning(t *testing.T) {
	result := analyze(t, `
model Order {
  id: Int @id
  total: Float
}
api expensive(): [Order] {
  Order.find(where: total > 100)
}`)
	expectWarning(t, result, "without index")
}

func TestQueryWithIndexNoWarning(t *testing.T) {
	result := analyze(t, `
model Order {
  id: Int @id
  status: String @index
}
api byStatus(): [Order] {
  Order.find(where: status == "paid")
}`)
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "status") && strings.Contains(w.Message, "without index") {
			t.Errorf("unexpected index warning for indexed field: %s", w.Message)
		}
	}
}

func TestQueryUniqueFieldNoWarning(t *testing.T) {
	result := analyze(t, `
model User {
  id: Int @id
  email: String @unique
}
api byEmail(): [User] {
  User.find(where: email == "test@test.com")
}`)
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "email") && strings.Contains(w.Message, "without index") {
			t.Errorf("unexpected index warning for @unique field: %s", w.Message)
		}
	}
}

func TestOrderWithoutIndexWarning(t *testing.T) {
	result := analyze(t, `
model Post {
  id: Int @id
  likes: Int
}
api sorted(): [Post] {
  Post.findMany(order: likes.desc)
}`)
	expectWarning(t, result, "sorting by")
}

func TestN1QueryWarning(t *testing.T) {
	result := analyze(t, `
model User {
  id: Int @id
  name: String
}
model Post {
  id: Int @id
  userId: Int @index
}
api bad(users: [User]): Int {
  var count = 0
  for user in users {
    val posts = Post.find(where: userId == user.id)
    count += posts.size
  }
  count
}`)
	expectWarning(t, result, "N+1 query")
}

func TestN1QueryNoWarningOutsideLoop(t *testing.T) {
	result := analyze(t, `
model Post {
  id: Int @id
  status: String @index
}
api ok(): [Post] {
  Post.find(where: status == "published")
}`)
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "N+1") {
			t.Errorf("unexpected N+1 warning outside loop: %s", w.Message)
		}
	}
}

// ========== Helper Function Tests ==========

func TestExtractWhereFields(t *testing.T) {
	fields := extractWhereFields(&ast.BinaryExpr{
		Left:  &ast.Ident{Name: "status"},
		Right: &ast.Literal{Kind: token.String, Value: "active"},
		Op:    "==",
	})
	if len(fields) != 1 || fields[0] != "status" {
		t.Errorf("expected [status], got %v", fields)
	}
}

func TestExtractWhereFieldsCompound(t *testing.T) {
	expr := &ast.BinaryExpr{
		Left: &ast.BinaryExpr{
			Left:  &ast.Ident{Name: "status"},
			Right: &ast.Literal{Kind: token.String, Value: "active"},
			Op:    "==",
		},
		Right: &ast.BinaryExpr{
			Left:  &ast.Ident{Name: "role"},
			Right: &ast.Literal{Kind: token.String, Value: "admin"},
			Op:    "==",
		},
		Op: "&&",
	}
	fields := extractWhereFields(expr)
	if len(fields) != 2 {
		t.Errorf("expected 2 fields, got %v", fields)
	}
}

func TestExtractWhereFieldsItDot(t *testing.T) {
	fields := extractWhereFields(&ast.BinaryExpr{
		Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "email"},
		Right: &ast.Literal{Kind: token.String, Value: "test"},
		Op:    "==",
	})
	if len(fields) != 1 || fields[0] != "email" {
		t.Errorf("expected [email], got %v", fields)
	}
}

func TestExtractOrderField(t *testing.T) {
	f := extractOrderField(&ast.MemberExpr{
		Object: &ast.Ident{Name: "createdAt"},
		Field:  "desc",
	})
	if f != "createdAt" {
		t.Errorf("expected 'createdAt', got %q", f)
	}
}

func TestExtractOrderFieldBare(t *testing.T) {
	f := extractOrderField(&ast.Ident{Name: "name"})
	if f != "name" {
		t.Errorf("expected 'name', got %q", f)
	}
}

func TestIsQueryOp(t *testing.T) {
	ops := []string{"find", "findFirst", "findMany", "count", "exists",
		"update", "updateMany", "delete", "deleteMany"}
	for _, op := range ops {
		if !isQueryOp(op) {
			t.Errorf("expected %q to be a query op", op)
		}
	}
	notOps := []string{"create", "createMany", "upsert", "transaction"}
	for _, op := range notOps {
		if isQueryOp(op) {
			t.Errorf("expected %q NOT to be a query op", op)
		}
	}
}

func TestIsComparisonOp(t *testing.T) {
	for _, op := range []string{"==", "!=", ">", "<", ">=", "<=", "in"} {
		if !isComparisonOp(op) {
			t.Errorf("expected %q to be comparison", op)
		}
	}
	if isComparisonOp("&&") {
		t.Error("&& should not be comparison")
	}
}

func TestIsBuiltinIdent(t *testing.T) {
	for _, name := range []string{"true", "false", "null", "my", "it", "request"} {
		if !isBuiltinIdent(name) {
			t.Errorf("expected %q to be builtin", name)
		}
	}
	if isBuiltinIdent("userId") {
		t.Error("userId should not be builtin")
	}
}

func TestBatchAlternative(t *testing.T) {
	tests := map[string]string{
		"find":      "find",
		"findFirst": "find",
		"delete":    "delete",
		"update":    "update",
		"count":     "count",
	}
	for op, want := range tests {
		got := batchAlternative(op)
		if got != want {
			t.Errorf("batchAlternative(%q) = %q, want %q", op, got, want)
		}
	}
}

func TestExtractWhereFieldsNil(t *testing.T) {
	fields := extractWhereFields(nil)
	if len(fields) != 0 {
		t.Errorf("expected empty, got %v", fields)
	}
}

func TestExtractOrderFieldNonMatch(t *testing.T) {
	f := extractOrderField(&ast.MemberExpr{
		Object: &ast.Ident{Name: "user"},
		Field:  "name",
	})
	if f != "" {
		t.Errorf("expected empty, got %q", f)
	}
}

func TestExtractWhereFieldsUnary(t *testing.T) {
	fields := extractWhereFields(&ast.UnaryExpr{
		Op:    "!",
		Value: &ast.BinaryExpr{Left: &ast.Ident{Name: "active"}, Right: &ast.Literal{Kind: token.True}, Op: "=="},
	})
	if len(fields) != 1 || fields[0] != "active" {
		t.Errorf("expected [active], got %v", fields)
	}
}

func TestAsListType(t *testing.T) {
	base := &ResolvedType{Kind: TypeModel, Name: "User"}
	list := base.AsList()
	if !list.IsList {
		t.Error("expected IsList to be true")
	}
	if list.Name != "User" {
		t.Errorf("expected name 'User', got %q", list.Name)
	}
}

// ========== N+1 Detection — chain-style in for loop ==========

func TestN1ChainStyleFindFirstInLoop(t *testing.T) {
	result := analyze(t, `
model User { name: String }
model Post { userId: Int  title: String }
api test(ids: [Int]): Int {
  var count = 0
  for id in ids {
    val p = Post.findFirst(where: userId == id)
    count += 1
  }
  count
}
`)
	expectWarning(t, result, "N+1 query")
}

func TestN1FunctionStyleInLoop(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(ids: [Int]): Int {
  var count = 0
  for id in ids {
    val u = find(User, id: id)
    count += 1
  }
  count
}
`)
	expectWarning(t, result, "N+1 query")
}

func TestN1ForLoopNilBody(t *testing.T) {
	// for loop with nil body — exercises analyzeForN1 nil body path
	a := New()
	file := &ast.File{
		Models: []*ast.ModelDecl{
			{Name: "User", Fields: []*ast.FieldDecl{{Name: "name", Type: &ast.TypeRef{Name: "String"}}}},
		},
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Int"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ForStmt{
							VarName:    "i",
							Collection: &ast.Ident{Name: "items"},
							Body:       nil,
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

func TestDetectN1InExprNonCallExpr(t *testing.T) {
	// non-CallExpr in for loop — should NOT trigger N+1 warning
	result := analyze(t, `
api test(items: [Int]): Int {
  var sum = 0
  for item in items {
    sum += item
  }
  sum
}
`)
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "N+1") {
			t.Errorf("unexpected N+1 warning for non-CRUD expression: %s", w.Message)
		}
	}
}

func TestDetectN1NilExpr(t *testing.T) {
	// directly test detectN1InExpr with nil expression
	a := New()
	// should not panic
	a.detectN1InExpr(nil, token.Position{})
}

// ========== analyzeCallExpr — chain-style with order arg ==========

func TestAnalyzeCallExprChainStyleOrder(t *testing.T) {
	result := analyze(t, `
model Post {
  id: Int @id
  title: String
  likes: Int
}
api test(): [Post] {
  Post.findMany(order: likes.desc)
}
`)
	expectWarning(t, result, "sorting by")
}

// ========== analyzeCallExpr — function-style query op ==========

func TestAnalyzeCallExprFunctionStyleQueryOp(t *testing.T) {
	result := analyze(t, `
model Post {
  id: Int @id
  title: String
}
api test(): [Post] {
  find(Post, where: title == "hello")
}
`)
	// title has no @index, should warn
	expectWarning(t, result, "without index")
}
