package parser

import (
	"testing"

	"github.com/light-speak/luxo/pkg/lexer"
)

// FuzzParser tests that the parser never panics or loops infinitely on any input.
func FuzzParser(f *testing.F) {
	seeds := []string{
		"",
		"model User { name: String }",
		"enum Role { USER ADMIN }",
		"api getUser(id: Int): User @auth",
		"fn encrypt(value: String): String @native",
		"sealed Result { Ok(v: String) Failed(e: String) }",
		"type Page<T> { items: [T] total: Int }",
		"extend User { posts: [Post] }",
		"model Post { authorId: Int author: User @by(id, authorId) }",
		"use common.{ Base }",
		"error NotFound { code: 404 message: error.not_found }",
		"middleware auth @native",
		`api test(): Int {
  val x = 42 + 3.14
  val y = User.find(id: 1) ?: throw error.not_found
  when(x) { in 1..10 -> "a" else -> "b" }
  for item in items { item.update(status: "done") }
  if true { val z = 1 } else { val z = 2 }
  transaction { Order.create(total: 100) }
  emit("event", data, key: value)
  x
}`,
		// malformed inputs
		"model { }",
		"api (: )",
		"fn @native",
		"{ { { { } } } }",
		"val = = = =",
		"@@@@@",
		"model User : : : { }",
		"model Post { author: User(key: authorId) } model Recovered { id: Int }",
		"model Post { authors: [User(key: authorId)] }",
		"api lookup(): Page<User(key: authorId)> @native",
		"api lookup(): [User] { User.findMany(order: ) 0 } fn recovered(): Int { 1 }",
		"fn lookup(): Int { outer(inner(value: ), keep: 7) 0 } fn recovered(): Int { 1 }",
	}

	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		l := lexer.New(input, "fuzz.luxo")
		tokens, _ := l.Tokenize()
		p := New(tokens)
		// must not panic or loop forever (timeout enforced by go test)
		p.Parse("fuzz.luxo")
	})
}
