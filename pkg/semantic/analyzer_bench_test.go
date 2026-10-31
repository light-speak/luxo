package semantic

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/internal/testutil"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
)

func BenchmarkEditDistance(b *testing.B) {
	for _, size := range []int{8, 63, 64, 128} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			left := strings.Repeat("a", size)
			right := strings.Repeat("a", size-1) + "b"
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if editDistance(left, right) != 1 {
					b.Fatal("incorrect edit distance")
				}
			}
		})
	}
}

// Compare compile-time event collection, not event delivery or request handling.
func BenchmarkCollectEventEmits(b *testing.B) {
	for _, count := range []int{0, 1, 128} {
		block := &ast.Block{}
		for range count {
			block.Stmts = append(block.Stmts, &ast.EmitStmt{EventName: "Changed"})
		}
		for _, recursive := range []bool{false, true} {
			b.Run(fmt.Sprintf("direct_%d/recursive=%t", count, recursive), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					var events []string
					if recursive {
						events, _ = collectReachableEmits(block, "", nil, nil)
					} else {
						for _, stmt := range block.Stmts {
							if emit, ok := stmt.(*ast.EmitStmt); ok {
								events = append(events, emit.EventName)
							}
						}
					}
					if len(events) != count {
						b.Fatal(events)
					}
				}
			})
		}
	}
}

// Synthetic, prebuilt callable graphs isolate reachable-event collection.
func BenchmarkReachableEventEmits(b *testing.B) {
	for _, count := range []int{1, 32, 128} {
		bodies := make(map[callableKey]*ast.Block, count)
		for index := range count {
			var statement ast.Stmt = &ast.EmitStmt{EventName: "Changed"}
			if index+1 < count {
				statement = &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: fmt.Sprintf("helper%d", index+1)}}}
			}
			bodies[callableKey{name: fmt.Sprintf("helper%d", index)}] = &ast.Block{Stmts: []ast.Stmt{statement}}
		}
		root := &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: "helper0"}}}}}
		b.Run(fmt.Sprintf("helpers_%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				events, _ := collectReachableEmits(root, "", bodies, nil)
				if len(events) != 1 || events[0] != "Changed" {
					b.Fatal(events)
				}
			}
		})
	}
}

// Isolate the added compile-time validation; no request handler calls this pass.
func BenchmarkErrorDeclarationValidation(b *testing.B) {
	for _, count := range []int{0, 1, 16} {
		b.Run(fmt.Sprintf("fields_%d", count), func(b *testing.B) {
			analyzer := New()
			file := &ast.File{}
			if count > 0 {
				declaration := &ast.ErrorDecl{Name: "Rejected"}
				for index := range count {
					declaration.Fields = append(declaration.Fields, &ast.ParamDecl{
						Name: fmt.Sprintf("value%d", index), Type: &ast.TypeRef{Name: "JSON"},
					})
				}
				file.Errors = []*ast.ErrorDecl{declaration}
			}
			b.ReportAllocs()
			for b.Loop() {
				analyzer.resolveErrorTypes(file)
			}
			if len(analyzer.errors) != 0 {
				b.Fatal(analyzer.errors)
			}
		})
	}
}

// Representative .luxo source for semantic analysis benchmarks.
const benchSemanticInput = `
enum Role {
  USER
  ADMIN
  MODERATOR
}

enum OrderStatus {
  PENDING
  PAID
  SHIPPED
  COMPLETED
  CANCELLED
}

model User @crud {
  id:        Int
  name:      String   @filterable
  email:     String   @unique
  password:  String   @hidden @hash
  role:      Role     = Role.USER
  avatar:    String?
  phone:     String   @immutable
  createdAt: DateTime
}

model Post @crud {
  id:        Int
  title:     String     @filterable
  content:   String
  userId:    Int        @index
  likes:     Int        = 0
  createdAt: DateTime   @sortable
}

model Order {
  id:        Int
  orderNo:   String      @unique
  userId:    Int
  quantity:  Int
  total:     Float
  status:    OrderStatus = OrderStatus.PENDING
  createdAt: DateTime
}

type AuthResult {
  token: String
  user:  User
}

error NotFound(resource: String) {
  code:    404
  message: "error.not_found"
}

error Unauthorized {
  code:    401
  message: "error.unauthorized"
}

type PostSnapshot {
  id: Int
  title: String
}

event PostCreated(post: PostSnapshot, userId: Int)

api createPost(title: String, content: String): Post @auth {
  val post = Post.create(
    title: title,
    content: content,
    userId: my.id
  )
  emit PostCreated(post: PostSnapshot { id: post.id, title: post.title }, userId: my.id)
  post
}

api listPosts(page: Int = 1, limit: Int = 20): [Post] {
  val posts = Post.where(likes > 0).all()
  posts
}

api getLevel(score: Int): String {
  when(score) {
    in 90..100 -> "A"
    in 80..89 -> "B"
    in 60..79 -> "C"
    else -> "D"
  }
}

fn calcDiscount(total: Float, role: Role): Float {
  when(role) {
    Role.ADMIN -> total * 0.15
    Role.MODERATOR -> total * 0.10
    else -> 0.0
  }
}
`

func parseFile(b *testing.B, input string) *ast.File {
	b.Helper()
	l := lexer.New(input, "bench.luxo")
	tokens, lexErrors := l.Tokenize()
	if len(lexErrors) > 0 {
		b.Fatalf("lexer errors: %v", lexErrors)
	}
	p := parser.New(tokens)
	file, parseErrors := p.Parse("bench.luxo")
	if len(parseErrors) > 0 {
		b.Fatalf("parser errors: %v", parseErrors)
	}
	return file
}

// BenchmarkAnalyzeWhereMembership separates query validation from ordinary comparisons.
func BenchmarkAnalyzeWhereMembership(b *testing.B) {
	for _, predicate := range []struct{ name, source string }{
		{"comparison", "it.points > 0"}, {"range", "it.points in 1..10"}, {"list", "it.points in values"},
	} {
		b.Run(predicate.name, func(b *testing.B) {
			file := parseFile(b, "model Probe { id: Int @id points: Int }\napi selected(values: [Int]): [Probe] { return Probe.where { "+predicate.source+" }.all() }")
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if result := Analyze([]*ast.File{file}); len(result.Errors) != 0 {
					b.Fatal(result.Errors)
				}
			}
		})
	}
}

// BenchmarkAnalyzeQueryValueScopes covers string and CRUD predicate bindings.
func BenchmarkAnalyzeQueryValueScopes(b *testing.B) {
	for _, sample := range []struct{ name, expression string }{
		{"string", `Probe.where { it.title.startsWith(value) }.all()`},
		{"string-expression", `Probe.where { it.title.startsWith(value.trim()) }.all()`},
		{"crud-comparison", `Probe.find(where: it.title == value)`},
		{"crud-membership", `Probe.find(where: it.points in values)`},
	} {
		b.Run(sample.name, func(b *testing.B) {
			file := parseFile(b, `model Probe { id: Int @id points: Int title: String }
api selected(value: String, values: [Int]): [Probe] { return `+sample.expression+` }`)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if result := Analyze([]*ast.File{file}); len(result.Errors) != 0 {
					b.Fatal(result.Errors)
				}
			}
		})
	}
}

// BenchmarkAnalyze measures full semantic analysis of a medium-sized .luxo source.
func BenchmarkAnalyze(b *testing.B) {
	file := parseFile(b, benchSemanticInput)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		a := New()
		a.Analyze([]*ast.File{file})
	}
}

// BenchmarkAnalyzeLargeFile measures semantic analysis with a large synthetic .luxo source.
func BenchmarkAnalyzeLargeFile(b *testing.B) {
	// Generate a large .luxo source with many models and APIs.
	var sb strings.Builder
	sb.WriteString("enum Status { ACTIVE  INACTIVE }\n")
	for i := range 50 {
		fmt.Fprintf(&sb, "model Item%d {\n  id: Int\n  name: String\n  value: Float\n  status: Status = Status.ACTIVE\n}\n\n", i)
	}
	for i := range 50 {
		fmt.Fprintf(&sb, "api getItem%d(id: Int): Item%d {\n  val item = Item%d.find(id: id)\n  item\n}\n\n", i, i, i)
	}
	src := sb.String()
	file := parseFile(b, src)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		a := New()
		a.Analyze([]*ast.File{file})
	}
}

// BenchmarkAnalyzeDemoFile retains the immutable V1 analysis/diagnostics workload.
// The historical corpus is not a valid current application; errors are intentional here.
func BenchmarkAnalyzeDemoFile(b *testing.B) {
	src := testutil.CompilerDemoV1()
	file := parseFile(b, src)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		a := New()
		a.Analyze([]*ast.File{file})
	}
}

func TestHistoricalCompilerCorpusIncludesDiagnostics(t *testing.T) {
	tokens, lexErrors := lexer.New(testutil.CompilerDemoV1(), "bench.luxo").Tokenize()
	file, parseErrors := parser.New(tokens).Parse("bench.luxo")
	if len(lexErrors) != 0 || len(parseErrors) != 0 {
		t.Fatalf("historical input must be syntactically complete: %v %v", lexErrors, parseErrors)
	}
	if result := New().Analyze([]*ast.File{file}); len(result.Errors) == 0 {
		t.Fatal("reassess benchmark classification: the historical diagnostics workload no longer reports errors")
	}
}
