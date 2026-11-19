package semantic

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/light-speak/luxo/internal/testutil"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
)

func BenchmarkAnalyzeQueryOrder(b *testing.B) {
	baseline := os.Getenv("LUXO_QUERY_ORDER_BASELINE") == "1"
	for _, sample := range []struct {
		name, body, scope string
		errors            int
	}{
		{name: "direct", body: `return Item.orderBy(id.asc).all()`},
		{name: "named", body: `return Item.findMany(order: id.asc)`},
		{name: "multi", body: `return Item.orderBy(id.asc, name.desc).all()`},
		{name: "managed", body: `return Item.orderBy(createdAt.desc).all()`},
		{name: "field-it", body: `return Item.orderBy(it.desc).all()`},
		{name: "scope", scope: `scope ordered = where(id > 0).orderBy(it.desc)`, body: `return Item.all()`},
		{name: "invalid-call", body: `return Item.orderBy(id.desc()).all()`, errors: 1},
	} {
		b.Run(sample.name, func(b *testing.B) {
			directives := ""
			if sample.scope != "" {
				directives = "@scope(ordered)"
			}
			file := parseFile(b, `model Item @soft { id: Int @id name: String it: Int asc: Int desc: Int `+sample.scope+` } api sorted(): [Item] `+directives+` { `+sample.body+` }`)
			expected := sample.errors
			if baseline {
				expected = 0
			}
			b.ReportAllocs()
			for b.Loop() {
				if result := Analyze([]*ast.File{file}); len(result.Errors) != expected {
					b.Fatalf("errors=%v, want %d", result.Errors, expected)
				}
			}
		})
	}
}

func BenchmarkAnalyzeAsyncBoundary(b *testing.B) {
	for _, sample := range []struct{ name, body string }{
		{"control", ""},
		{"single", "async { val item = 1 }"},
		{"many", strings.Repeat("async { val item = 1 }\n", 64)},
		{"list-control", "val items = [" + strings.Repeat("1,", 127) + "1]"},
	} {
		b.Run(sample.name, func(b *testing.B) {
			file := parseFile(b, "fn run(): Int { "+sample.body+" return 42 }")
			b.ReportAllocs()
			for b.Loop() {
				if result := Analyze([]*ast.File{file}); len(result.Errors) != 0 {
					b.Fatal(result.Errors)
				}
			}
		})
	}
}

func BenchmarkAnalyzeServiceNameBoundary(b *testing.B) {
	for _, sample := range []struct {
		name, declaration string
		count             int
	}{
		{"ordinary1", `fn value%d(input: Int): Int { return input } api read%d(input: Int): Int { return value%d(input) }`, 1},
		{"ordinary128", `fn value%d(input: Int): Int { return input } api read%d(input: Int): Int { return value%d(input) }`, 128},
		{"shared-service128", `fn value%d(input: Int): Int @service @native api value%d(input: Int): Int @native // %d`, 128},
	} {
		b.Run(sample.name, func(b *testing.B) {
			var source strings.Builder
			for index := range sample.count {
				fmt.Fprintf(&source, sample.declaration+"\n", index, index, index)
			}
			file := parseFile(b, source.String())
			b.ReportAllocs()
			for b.Loop() {
				if result := Analyze([]*ast.File{file}); len(result.Errors) != 0 {
					b.Fatal(result.Errors)
				}
			}
		})
	}
}

func BenchmarkAnalyzeAPILocalBoundary(b *testing.B) {
	baseline := os.Getenv("LUXO_API_LOCAL_BASELINE") == "1"
	for _, sample := range []struct {
		name, declaration, call string
		invalid                 bool
	}{
		{"api-body", `api inner(value: Int): Int { return value }`, "inner(value)", true},
		{"api-native", `api inner(value: Int): Int @native`, "inner(value)", true},
		{"fn-control", `fn inner(value: Int): Int { return value }`, "inner(value)", false},
	} {
		for _, count := range []int{1, 128} {
			b.Run(fmt.Sprintf("%s/%d", sample.name, count), func(b *testing.B) {
				var source strings.Builder
				source.WriteString(sample.declaration)
				for index := range count {
					fmt.Fprintf(&source, "\napi outer%d(value: Int): Int { return %s }", index, sample.call)
				}
				file := parseFile(b, source.String())
				expected := 0
				if sample.invalid && !baseline {
					expected = count
				}
				b.ReportAllocs()
				for b.Loop() {
					if result := Analyze([]*ast.File{file}); len(result.Errors) != expected {
						b.Fatalf("expected %d diagnostics, got %v", expected, result.Errors)
					}
				}
			})
		}
	}
}

func BenchmarkAnalyzeSameNameCalls(b *testing.B) {
	for _, sample := range []struct{ name, declaration string }{
		{"native128", `fn shared%d(input: Int = 7): Result<String> @native api shared%d(value: Int = 99): String @native api invoke%d(): String { return shared%d()? }`},
		{"body128", `fn shared%d(input: Int = 7): Int { return input } api shared%d(): Int { return 99 } api invoke%d(): Int { return shared%d() }`},
	} {
		b.Run(sample.name, func(b *testing.B) {
			var source strings.Builder
			for index := range 128 {
				fmt.Fprintf(&source, sample.declaration+"\n", index, index, index, index)
			}
			file := parseFile(b, source.String())
			b.ReportAllocs()
			for b.Loop() {
				if result := Analyze([]*ast.File{file}); len(result.Errors) != 0 {
					b.Fatal(result.Errors)
				}
			}
		})
	}
}

func BenchmarkAnalyzeCheckedArithmetic(b *testing.B) {
	for _, sample := range []struct{ name, source string }{
		{"dynamic", "fn add(left: Int, right: Int): Int { return left + right }"},
		{"constants32", "fn value(): Int { return " + strings.Repeat("1 + ", 31) + "1 }"},
		{"float-control", "fn add(left: Float, right: Float): Float { return left + right }"},
	} {
		b.Run(sample.name, func(b *testing.B) {
			file := parseFile(b, sample.source)
			b.ReportAllocs()
			for b.Loop() {
				if result := Analyze([]*ast.File{file}); len(result.Errors) != 0 {
					b.Fatal(result.Errors)
				}
			}
		})
	}
}

func BenchmarkAnalyzeVariableBindings(b *testing.B) {
	baseline := os.Getenv("LUXO_VARIABLE_BINDINGS_BASELINE") == "1"
	for _, sample := range []struct {
		name, body           string
		errors, beforeErrors int
	}{
		{"empty-control", "return input", 0, 0},
		{"locals-control", "val first = input + 1 val second = first + 1 return second", 0, 0},
		{"shadow-control", "if true { val input = 1 val copy = input } return input", 0, 0},
		{"discard", "val _ = input val _ = input return input", 0, 0},
		{"await-control", "val (left, right) = await {\ninput\ninput + 1\n} return left + right", 0, 0},
		{"duplicate", "val value = input val value = input return value", 1, 0},
		{"duplicate-tuple", "val (value, value) = await {\ninput\ninput + 1\n} return value", 1, 0},
		{"discard-read", "val _ = input return _", 1, 0},
		{"undefined-control", "return absentValue", 1, 1},
	} {
		for _, count := range []int{1, 128} {
			b.Run(fmt.Sprintf("%s/%d", sample.name, count), func(b *testing.B) {
				var source strings.Builder
				for index := range count {
					fmt.Fprintf(&source, "api probe%d(input: Int): Int { %s }\n", index, sample.body)
				}
				file := parseFile(b, source.String())
				expected := sample.errors * count
				if baseline {
					expected = sample.beforeErrors * count
				}
				b.ReportAllocs()
				for b.Loop() {
					if result := Analyze([]*ast.File{file}); len(result.Errors) != expected {
						b.Fatalf("expected %d diagnostics, got %v", expected, result.Errors)
					}
				}
			})
		}
	}
}

func BenchmarkAnalyzeGenericWhen(b *testing.B) {
	baseline := os.Getenv("LUXO_GENERIC_WHEN_BASELINE") == "1"
	for _, sample := range []struct {
		name, body                string
		beforeErrors, afterErrors int
	}{
		{"scalar-control", "return value", 0, 0},
		{"native-control", "return nativeEcho(value)?", 0, 0},
		{"channel-control", "val channel: Channel<Int> = Channel(1) channel <- value return <-channel", 0, 0},
		{"when-expressions", "val left: Channel<Int> = Channel(1) val right: Channel<Int> = Channel(1) val channel = when { flag -> left else -> right } channel <- value return <-channel", 0, 0},
		{"when-blocks", "val left: Channel<Int> = Channel(1) val channel = when { flag -> { left } else -> { left } } channel <- value return <-channel", 2, 0},
		{"invalid-blocks", "val left: Channel<Int> = Channel(1) val right: Channel<String> = Channel(1) val channel = when { flag -> { left } else -> { right } } return value", 0, 1},
	} {
		for _, count := range []int{1, 128} {
			b.Run(fmt.Sprintf("%s/%d", sample.name, count), func(b *testing.B) {
				var source strings.Builder
				source.WriteString("fn nativeEcho(value: Int): Result<Int> @native\n")
				for index := range count {
					fmt.Fprintf(&source, "api probe%d(flag: Boolean, value: Int): Int { %s }\n", index, sample.body)
				}
				file := parseFile(b, source.String())
				expected := sample.afterErrors * count
				if baseline {
					expected = sample.beforeErrors * count
				}
				b.ReportAllocs()
				for b.Loop() {
					if result := Analyze([]*ast.File{file}); len(result.Errors) != expected {
						b.Fatalf("expected %d diagnostics, got %v", expected, result.Errors)
					}
				}
			})
		}
	}
}

// Database context changes are compile-time checks, not request execution.
func BenchmarkAnalyzeWhenDatabaseContext(b *testing.B) {
	baseline := os.Getenv("LUXO_WHEN_DATABASE_BASELINE") == "1"
	for _, sample := range []struct {
		name, body                string
		beforeErrors, afterErrors int
	}{
		{"scalar-control", "return value", 0, 0},
		{"when-control", "return when { value > 0 -> { value } else -> { 0 } }", 0, 0},
		{"transaction-control", "transaction { Entry.find(id: value) } return value", 0, 0},
		{"when-query", "return when { value > 0 -> { Entry.find(id: value).id } else -> { 0 } }", 1, 0},
		{"transaction-query", "transaction { when { value > 0 -> { Entry.find(id: value) } else -> {} } } return value", 1, 0},
		{"forbidden-query", "[1].forEach { transaction { Entry.find(id: value) } } return value", 0, 1},
	} {
		for _, count := range []int{1, 128} {
			b.Run(fmt.Sprintf("%s/%d", sample.name, count), func(b *testing.B) {
				var source strings.Builder
				source.WriteString("model Entry @noTime { id: Int @id }\n")
				for index := range count {
					fmt.Fprintf(&source, "api probe%d(value: Int): Int { %s }\n", index, sample.body)
				}
				file := parseFile(b, source.String())
				expected := sample.afterErrors * count
				if baseline {
					expected = sample.beforeErrors * count
				}
				b.ReportAllocs()
				for b.Loop() {
					if result := Analyze([]*ast.File{file}); len(result.Errors) != expected {
						b.Fatalf("expected %d diagnostics, got %v", expected, result.Errors)
					}
				}
			})
		}
	}
}

// Analyze pre-parsed input; baseline mode retains the prior missing diagnostics.
func BenchmarkAnalyzeLoopScopes(b *testing.B) {
	for _, sample := range []struct {
		name, body string
		invalid    bool
	}{
		{"scalar-control", "return value + 1", false},
		{"loop-when", "for item in 0..4 { when { item == 2 -> { break } else -> { continue } } } return value", false},
		{"condition", "var index = 0 for index < value { index += 1 } return index", false},
		{"value-when", "return when { value > 0 -> { return value } else -> { 0 } }", false},
		{"isolated-scopes", `for item in 0..1 {
 async { for inner in 0..1 { break } }
 transaction { for inner in 0..1 { continue } }
 [1].forEach { for inner in 0..1 { break } }
 val (left, right) = await { echo(value) echo(value) }
} return value`, false},
		{"invalid-condition", "for value {} return value", true},
		{"invalid-escape", "for item in 0..1 { [1].forEach { when { true -> { break } else -> {} } } } return value", true},
	} {
		for _, count := range []int{1, 128} {
			b.Run(fmt.Sprintf("%s/%d", sample.name, count), func(b *testing.B) {
				var source strings.Builder
				source.WriteString("fn echo(value: Int): Int { return value }\n")
				for index := range count {
					fmt.Fprintf(&source, "api probe%d(value: Int): Int { %s }\n", index, sample.body)
				}
				file := parseFile(b, source.String())
				expectedErrors := 0
				if sample.invalid && os.Getenv("LUXO_LOOP_SCOPE_BASELINE") != "1" {
					expectedErrors = count
				}
				b.ReportAllocs()
				for b.Loop() {
					if result := Analyze([]*ast.File{file}); len(result.Errors) != expectedErrors {
						b.Fatalf("expected %d diagnostics, got %v", expectedErrors, result.Errors)
					}
				}
			})
		}
	}
}

func BenchmarkAnalyzeCompoundInferredAPIs(b *testing.B) {
	for _, count := range []int{1, 128} {
		var source strings.Builder
		source.WriteString("model Item @noTime { id: Int @id other: String ")
		for index := range count {
			fmt.Fprintf(&source, "value%c%c: String ", 'a'+index/26, 'a'+index%26)
		}
		source.WriteString("}\n")
		for index := range count {
			fmt.Fprintf(&source, "api getItemByValue%c%cAndOther\n", 'a'+index/26, 'a'+index%26)
		}
		tokens, lexErrors := lexer.New(source.String(), "inferred.luxo").Tokenize()
		file, parseErrors := parser.New(tokens).Parse("inferred.luxo")
		if len(lexErrors) != 0 || len(parseErrors) != 0 {
			b.Fatal(lexErrors, parseErrors)
		}
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if result := Analyze([]*ast.File{file}); len(result.Errors) != 0 {
					b.Fatal(result.Errors)
				}
			}
		})
	}
}

// Reuse parsed syntax to measure warm semantic checks, excluding parsing and I/O.
func BenchmarkAnalyzeErrorGuards(b *testing.B) {
	for _, sample := range []struct{ name, body string }{
		{"control", "return value"},
		{"bang", "value !: throw Rejected(detail: text) return value"},
		{"elvis", "return value ?: throw Rejected(detail: text)"},
		{"throw", "throw Rejected(detail: text)"},
	} {
		for _, count := range []int{1, 32} {
			b.Run(fmt.Sprintf("%s/%d", sample.name, count), func(b *testing.B) {
				var source strings.Builder
				source.WriteString("error Rejected(detail: String) { code: 409 message: \"rejected\" }\n")
				for index := range count {
					fmt.Fprintf(&source, "fn guard%d(value: Boolean, text: String): Boolean { %s }\n", index, sample.body)
				}
				tokens, lexErrors := lexer.New(source.String(), "guard.luxo").Tokenize()
				file, parseErrors := parser.New(tokens).Parse("guard.luxo")
				if len(lexErrors) != 0 || len(parseErrors) != 0 {
					b.Fatal(lexErrors, parseErrors)
				}
				b.ReportAllocs()
				for b.Loop() {
					if result := Analyze([]*ast.File{file}); len(result.Errors) != 0 {
						b.Fatal(result.Errors)
					}
				}
			})
		}
	}
}

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

func BenchmarkFindClosestString(b *testing.B) {
	short := []string{"User", "Usar", "Usor", "Door", "Task", "Post", "Team", "Role"}
	long := make([]string, 64)
	for i := range long {
		long[i] = strings.Repeat("x", 128) + fmt.Sprint(i)
	}
	mixed := append(append([]string{}, long...), short...)
	for _, sample := range []struct {
		name, target string
		candidates   []string
	}{
		{"same-length", "Usre", short},
		{"mixed", "Usre", mixed},
		{"long-target", strings.Repeat("x", 128), short},
		{"exact-first", "User", append(append([]string{}, short...), long...)},
	} {
		b.Run(sample.name, func(b *testing.B) {
			want := findClosestString(sample.target, sample.candidates)
			b.ReportAllocs()
			for b.Loop() {
				if got := findClosestString(sample.target, sample.candidates); got != want {
					b.Fatal("closest spelling changed")
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

func BenchmarkAnalyzeSoftDeleteModels(b *testing.B) {
	for _, kind := range []string{"ordinary", "implicit", "explicit"} {
		for _, count := range []int{1, 128} {
			b.Run(fmt.Sprintf("%s/%d", kind, count), func(b *testing.B) {
				file := &ast.File{Name: "soft.luxo"}
				for i := range count {
					model := &ast.ModelDecl{Name: fmt.Sprintf("Entry%d", i), Directives: []*ast.Directive{{Name: "noTime"}}}
					if kind != "ordinary" {
						model.Directives = append(model.Directives, &ast.Directive{Name: "soft"})
					}
					for j := range 16 {
						field := &ast.FieldDecl{Name: fmt.Sprintf("field%d", j), Type: &ast.TypeRef{Name: "Int"}}
						if kind == "explicit" && j == 15 {
							field.Name = "deletedAt"
							field.Type = &ast.TypeRef{Name: "DateTime", Nullable: true}
						}
						model.Fields = append(model.Fields, field)
					}
					file.Models = append(file.Models, model)
				}

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
}

func BenchmarkIdentityInheritedFieldLookup(b *testing.B) {
	field := &FieldInfo{Name: "id", Type: &ResolvedType{Name: "String"}}
	base := &ResolvedType{Fields: map[string]*FieldInfo{"id": field}}
	child := &ResolvedType{Parents: []*ResolvedType{base}}
	for _, sample := range []struct {
		name  string
		value *ResolvedType
	}{{"Local", base}, {"Inherited", child}} {
		b.Run(sample.name+"/Previous", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if previousIdentityFieldLookup(sample.value, "id") != field {
					b.Fatal("missing field")
				}
			}
		})
		b.Run(sample.name+"/Current", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if sample.value.LookupField("id") != field {
					b.Fatal("missing field")
				}
			}
		})
	}
}

func previousIdentityFieldLookup(value *ResolvedType, name string) *FieldInfo {
	if field, ok := value.Fields[name]; ok {
		return field
	}
	for _, parent := range value.Parents {
		if field := previousIdentityFieldLookup(parent, name); field != nil {
			return field
		}
	}
	return nil
}

func BenchmarkAnalyzeStorageAndListenerContracts(b *testing.B) {
	for _, kind := range []string{"stored", "inherited", "listener"} {
		for _, count := range []int{1, 128} {
			var source strings.Builder
			source.WriteString("type Base { label: String }\nevent Changed(value: Int)\n")
			for i := range count {
				switch kind {
				case "stored":
					fmt.Fprintf(&source, "model Record%d { id: Int @id label: String }\n", i)
				case "inherited":
					fmt.Fprintf(&source, "model Record%d : Base { id: Int @id }\n", i)
				case "listener":
					source.WriteString("on Changed { payload -> val value = payload.value }\n")
				}
			}
			tokens, lexErrors := lexer.New(source.String(), "contracts.luxo").Tokenize()
			file, parseErrors := parser.New(tokens).Parse("contracts.luxo")
			if len(lexErrors) != 0 || len(parseErrors) != 0 {
				b.Fatal(lexErrors, parseErrors)
			}
			b.Run(fmt.Sprintf("%s/%d", kind, count), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if result := Analyze([]*ast.File{file}); len(result.Errors) != 0 {
						b.Fatal(result.Errors)
					}
				}
			})
		}
	}
}

func BenchmarkAnalyzeImplicitRelations(b *testing.B) {
	for _, count := range []int{1, 128} {
		var source strings.Builder
		for i := range count {
			fmt.Fprintf(&source, "model Source%d @noTime { id: Int @id targets: [Target%d] } model Target%d @noTime { id: Int @id source%dId: Int }\n", i, i, i, i)
		}
		tokens, lexErrors := lexer.New(source.String(), "relations.luxo").Tokenize()
		file, parseErrors := parser.New(tokens).Parse("relations.luxo")
		if len(lexErrors) != 0 || len(parseErrors) != 0 {
			b.Fatal(lexErrors, parseErrors)
		}
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if result := Analyze([]*ast.File{file}); len(result.Errors) != 0 {
					b.Fatal(result.Errors)
				}
			}
		})
	}
}

func BenchmarkAnalyzeLoadPrimaryKeys(b *testing.B) {
	for _, kind := range []string{"direct", "inherited", "ordinary_call"} {
		for _, count := range []int{1, 128} {
			var source strings.Builder
			switch kind {
			case "direct":
				source.WriteString("model Product @noTime { key: String @id }\n")
			case "inherited":
				source.WriteString("model Base @noTime { key: String @id } model Product : Base @noTime { name: String }\n")
			default:
				source.WriteString("fn echo(key: String): String { return key }\n")
			}
			for i := range count {
				if kind == "ordinary_call" {
					fmt.Fprintf(&source, "api lookup%d(key: String): String { return echo(key) }\n", i)
				} else {
					fmt.Fprintf(&source, "api lookup%d(key: String): Product? { return Product.load(key) }\n", i)
				}
			}
			tokens, lexErrors := lexer.New(source.String(), "load.luxo").Tokenize()
			file, parseErrors := parser.New(tokens).Parse("load.luxo")
			if len(lexErrors) != 0 || len(parseErrors) != 0 {
				b.Fatal(lexErrors, parseErrors)
			}
			b.Run(fmt.Sprintf("%s/%d", kind, count), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if result := Analyze([]*ast.File{file}); len(result.Errors) != 0 {
						b.Fatal(result.Errors)
					}
				}
			})
		}
	}
}

func BenchmarkAnalyzeQueryTerminalArguments(b *testing.B) {
	baseline := os.Getenv("LUXO_QUERY_TERMINAL_BASELINE") == "1"
	for _, sample := range []struct {
		name, prefix, body, result string
		invalid                    bool
	}{
		{"count-none", "", `return Item.count()`, "Int", false},
		{"count-false", "", `return Item.count(where: false)`, "Int", false},
		{"exists-field", "", `return Item.exists(where: it.id == requested)`, "Boolean", false},
		{"count-chain", "", `return Item.where(id > 0).count(where: id < requested)`, "Int", false},
		{"count-alias", "", `val query = Item.where(id > 0) return query.count(where: false)`, "Int", false},
		{"count-function", "", `return count(Item, where: false)`, "Int", false},
		{"ordinary-count", `fn count(value: Int): Int { return value }`, `return count(requested)`, "Int", false},
		{"bulk-control", "", `return Item.deleteMany(where: false)`, "Int", false},
		{"invalid-argument", "", `return Item.count(id: 1)`, "Int", true},
	} {
		b.Run(sample.name, func(b *testing.B) {
			file := parseFile(b, `model Item @noTime { id: Int @id } `+sample.prefix+` api probe(requested: Int): `+sample.result+` { `+sample.body+` }`)
			b.ReportAllocs()
			for b.Loop() {
				result := Analyze([]*ast.File{file})
				if (len(result.Errors) != 0) != (sample.invalid && !baseline) {
					b.Fatal("query terminal diagnostic contract changed", result.Errors)
				}
			}
		})
	}
}

func BenchmarkAnalyzeBulkSetterUniqueness(b *testing.B) {
	for _, count := range []int{1, 8, 16, 128, 129} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			var fields, args strings.Builder
			for index := range count {
				fmt.Fprintf(&fields, " field%d: Int", index)
				if index > 0 {
					args.WriteString(", ")
				}
				fmt.Fprintf(&args, "field%d: 1", index)
			}
			file := parseFile(b, "model Item @noTime { id: Int @id "+fields.String()+" } api write(): Int { return Item.updateMany("+args.String()+") }")
			b.ReportAllocs()
			for b.Loop() {
				if result := Analyze([]*ast.File{file}); len(result.Errors) != 0 {
					b.Fatal(result.Errors)
				}
			}
		})
	}
}
