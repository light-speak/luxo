package parser

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/internal/testutil"
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

model User @withAuth(default: true) @crud {
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

func BenchmarkParseChannelPayload(b *testing.B) {
	for _, sample := range []struct{ name, body string }{
		{"send-simple", "ch <- value"},
		{"send-arithmetic", "ch <- value + 2 * 3"},
		{"send-parenthesized", "ch <- (value + 2 * 3)"},
		{"send-boolean", "ch <- value > 0 && value < 10"},
		{"receive-control", "val value = <-ch + 1"},
		{"arithmetic-control", "val value = a + b * 2"},
	} {
		b.Run(sample.name, func(b *testing.B) {
			tokens := tokenize(b, "api run(): Int { "+sample.body+" return 0 }")
			b.ReportAllocs()
			for b.Loop() {
				if _, errors := New(tokens).Parse("bench.luxo"); len(errors) != 0 {
					b.Fatal(errors)
				}
			}
		})
	}
}

func BenchmarkParseForBoundary(b *testing.B) {
	for _, sample := range []struct{ name, body string }{
		{"collection", `for row in rows { consume(row) }`},
		{"condition", `for count > 0 { count -= 1 }`},
		{"value", `val selected = for row in rows { row }`},
	} {
		b.Run(sample.name, func(b *testing.B) {
			tokens := tokenize(b, "api run(): Int { "+sample.body+" return 0 }")
			b.ReportAllocs()
			for b.Loop() {
				if _, errors := New(tokens).Parse("bench.luxo"); len(errors) != 0 {
					b.Fatal(errors)
				}
			}
		})
	}
}

// BenchmarkParse measures parsing of a medium-sized .luxo source.
func BenchmarkParseAssignmentStatements(b *testing.B) {
	for _, sample := range []struct{ name, statement string }{
		{"calls", "consume()\n"},
		{"members", "item.value += 1\n"},
	} {
		for _, size := range []struct {
			name  string
			count int
		}{{"single", 1}, {"batch", 32}} {
			b.Run(sample.name+"/"+size.name, func(b *testing.B) {
				tokens := tokenize(b, "api run(): Int {\n"+strings.Repeat(sample.statement, size.count)+"return 0\n}")
				if _, errs := New(tokens).Parse("bench.luxo"); len(errs) != 0 {
					b.Fatal(errs)
				}
				b.ReportAllocs()
				for b.Loop() {
					if _, errs := New(tokens).Parse("bench.luxo"); len(errs) != 0 {
						b.Fatal(errs)
					}
				}
			})
		}
	}
}

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

// BenchmarkParseDemoFile measures parsing of the immutable V1 corpus.
func BenchmarkParseDemoFile(b *testing.B) {
	src := testutil.CompilerDemoV1()
	tokens := tokenize(b, src)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		p := New(tokens)
		p.Parse("demo.luxo")
	}
}

func BenchmarkParseListDeclarations(b *testing.B) {
	tokens := tokenize(b, `model User { id: Int } type Sample { ints: [Int] names: [String]? models: [User] }`)
	b.ReportAllocs()
	for b.Loop() {
		if _, diagnostics := New(tokens).Parse("bench.luxo"); len(diagnostics) != 0 {
			b.Fatal(diagnostics)
		}
	}
}

var benchmarkNamedArgumentDiagnostic string

func BenchmarkParseNamedArgumentValue(b *testing.B) {
	for _, sample := range []struct {
		name, call string
		errors     int
		consume    bool
	}{
		{"empty-control", "helper()", 0, false},
		{"positional-control", "helper(1)", 0, false},
		{"named", "helper(value: 1)", 0, false},
		{"mixed", "helper(1, enabled: true, value: 2)", 0, false},
		{"missing-value", "helper(value: )", 1, false},
		{"missing-value-error-consumption", "helper(value: )", 1, true},
	} {
		b.Run(sample.name, func(b *testing.B) {
			tokens := tokenize(b, "fn run(): Int { "+sample.call+" 0 } fn recovered(): Int { 1 }")
			if _, errors := New(tokens).Parse("bench.luxo"); len(errors) != sample.errors {
				b.Fatal(errors)
			}
			b.ReportAllocs()
			for b.Loop() {
				_, errors := New(tokens).Parse("bench.luxo")
				if len(errors) != sample.errors {
					b.Fatal(errors)
				}
				if sample.consume {
					benchmarkNamedArgumentDiagnostic = errors[0].Error()
				}
			}
		})
	}
}

func BenchmarkParseGenericConstructors(b *testing.B) {
	for _, sample := range []struct{ name, expression string }{
		{"explicit", `Box<Int>(value: 1)`}, {"nested", `Box<Box<Int>>(value: Box<Int>(value: 1))`},
		{"comparison", `a < b && c > (d)`},
	} {
		b.Run(sample.name, func(b *testing.B) {
			tokens := tokenize(b, `api probe(): Int { return `+sample.expression+` }`)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, errors := New(tokens).Parse("benchmark.luxo"); len(errors) != 0 {
					b.Fatal(errors)
				}
			}
		})
	}
}
