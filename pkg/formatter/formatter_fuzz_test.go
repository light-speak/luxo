package formatter

import (
	"testing"
)

func FuzzFormat(f *testing.F) {
	// Seed corpus with representative .luxo snippets
	seeds := []string{
		"use http",
		"val x = 1",
		"var count = 0",
		"model User {\n  name: String\n}",
		"enum Role { USER ADMIN }",
		"sealed Result {\n  Ok(value: Int)\n  Err(msg: String)\n}",
		"api getUser(id: Int): User @auth",
		"fn encrypt(value: String): String @native",
		"event OrderCreated(order: Order)",
		"on OrderCreated { order ->\n  \"done\".i\n}",
		"middleware auth @native",
		"extend User {\n  posts: [Post]\n}",
		"// comment\nval x = 1",
		"/// doc comment\nmodel T {\n  x: Int\n}",
		"/* block */\nuse http",
		"interface Searchable {\n  fn search(q: String): [Self]\n}",
		"error NotFound {\n  code: 404\n  message: \"error.not_found\"\n}",
		"type Page {\n  items: [Int]\n  total: Int\n}",
		"override api getUser(id: Int): User",
		"api test(a: Int, b: Int, c: Int, d: Int): Int",
		"",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// Format must not panic on any input
		result, err := Format(input, "fuzz.luxo")
		if err != nil {
			return // lexer errors are expected for random input
		}

		// Second format must not fail (output of formatter must be valid input)
		_, err = Format(result, "fuzz.luxo")
		if err != nil {
			t.Errorf("second format failed: %v\ninput: %q\nfirst result: %q", err, input, result)
		}
	})
}
