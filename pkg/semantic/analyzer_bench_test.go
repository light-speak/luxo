package semantic

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
)

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

event PostCreated(post: Post, userId: Int)

api createPost(title: String, content: String): Post @auth {
  val post = Post.create(
    title: title,
    content: content,
    userId: my.id
  )
  emit PostCreated(post: post, userId: my.id)
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

// BenchmarkAnalyzeDemoFile measures semantic analysis of the full demo.luxo file.
func BenchmarkAnalyzeDemoFile(b *testing.B) {
	data, err := os.ReadFile("../../editors/vscode/examples/demo.luxo")
	if err != nil {
		b.Skipf("demo.luxo not found: %v", err)
	}
	src := string(data)
	file := parseFile(b, src)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		a := New()
		a.Analyze([]*ast.File{file})
	}
}
