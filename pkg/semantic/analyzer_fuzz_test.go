package semantic

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
)

// FuzzAnalyzer tests that the semantic analyzer never panics on any input.
func FuzzAnalyzer(f *testing.F) {
	seeds := []string{
		"model User { name: String }",
		"enum Role { USER ADMIN }",
		"api getUser(id: Int): User",
		"fn encrypt(value: String): String @native",
		`model User : Base {
  name: String @varchar(100)
  email: String @unique
  password: String @hidden @hash
  role: Role = Role.USER
  avatar: String?
  posts: [Post]
  val count: Int get @count
}`,
		`api test(): Int {
  val x = 42 + 3.14
  val y = "hello" + " world"
  val z = true && false
  x > 0 ?: throw error.bad
  when(x) { in 1..10 -> "a" else -> "b" }
  for item in items { item.update(v: 1) }
  if true { val inner = 1 }
  transaction { Order.create(total: 100) }
  emit("event", data)
  x
}`,
		// type errors — should report but not crash
		"model User { age: Int @hash }",
		"model User { name: Stirng }",
		"model User : NonExistent { name: String }",
		"model User { name: String }\nmodel User { email: String }",
		// malformed
		"",
		"model { }",
		"@@@",
	}

	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		l := lexer.New(input, "fuzz.luxo")
		tokens, _ := l.Tokenize()
		p := parser.New(tokens)
		file, _ := p.Parse("fuzz.luxo")
		if file == nil {
			return
		}
		a := New()
		// must not panic
		a.Analyze([]*ast.File{file})
	})
}
