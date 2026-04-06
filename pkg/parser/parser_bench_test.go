package parser

import (
	"os"
	"testing"

	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/token"
)

// Representative .luxo source for parser benchmarks.
const benchParseInput = `
use http
use json

val APP_NAME = "MyShop"

enum Role {
  USER
  ADMIN
  MODERATOR
}

model User @withAuth(secret: env("JWT_SECRET"), expires: 7d, stores: [id, role]) @crud {
  id:        Int
  name:      String   @filterable
  email:     String   @unique
  password:  String   @hidden @hash
  role:      Role     = Role.USER
  avatar:    String?
  phone:     String   @immutable
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

event PostCreated(post: Post, userId: Int)

api register(input: RegisterInput): AuthResult {
  input.password.length >= 8 ?: throw PasswordTooShort
  val exists = User.where(email == input.email).first()
  exists == null ?: throw DuplicateEmail
  val user = User.create(
    name: input.name,
    email: input.email,
    password: input.password,
    phone: input.phone
  )
  AuthResult { token: user.createToken(), user: user }
}
`

// Complex expression input for expression-parsing benchmarks.
const benchExprInput = `
api complexExpr(x: Int, y: Float, flag: Boolean): String {
  val a = x * 2 + y / 3 - 1
  val b = a >= 10 && flag || !flag
  val c = when {
    x > 100 -> "high"
    x > 50 -> "medium"
    x > 0 -> "low"
    else -> "none"
  }
  val d = items.filter { it.price > 100 }
    .map { it.name }
    .sortBy { it }
    .joinToString(", ")
  val e = user?.name?.uppercase ?: "Anonymous"
  val f = "result: ${a}, flag: ${b}, level: ${c}"
  f
}
`

func tokenize(b *testing.B, input string) []token.Token {
	b.Helper()
	l := lexer.New(input, "bench.luxo")
	tokens, errs := l.Tokenize()
	if len(errs) > 0 {
		b.Fatalf("lexer errors: %v", errs)
	}
	return tokens
}

// BenchmarkParse measures parsing of a medium-sized .luxo source.
func BenchmarkParse(b *testing.B) {
	tokens := tokenize(b, benchParseInput)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		p := New(tokens)
		p.Parse("bench.luxo")
	}
}

// BenchmarkParseExpr measures parsing of complex expressions.
func BenchmarkParseExpr(b *testing.B) {
	tokens := tokenize(b, benchExprInput)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		p := New(tokens)
		p.Parse("bench.luxo")
	}
}

// BenchmarkParseDemoFile measures parsing of the full demo.luxo file.
func BenchmarkParseDemoFile(b *testing.B) {
	data, err := os.ReadFile("../../editors/vscode/examples/demo.luxo")
	if err != nil {
		b.Skipf("demo.luxo not found: %v", err)
	}
	src := string(data)
	tokens := tokenize(b, src)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		p := New(tokens)
		p.Parse("demo.luxo")
	}
}
