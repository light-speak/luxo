package lexer

import (
	"testing"
)

// FuzzLexer tests that the lexer never panics or loops infinitely on any input.
func FuzzLexer(f *testing.F) {
	// seed corpus with interesting inputs
	seeds := []string{
		"",
		" ",
		"\n\n\n",
		"model User { name: String }",
		`api getUser(id: Int): User @cache(ttl: 60)`,
		`"hello ${world}"`,
		`"unterminated`,
		`/* unclosed block`,
		`/* nested /* comment */ */`,
		`/// doc comment`,
		`42 3.14 7d 500ms`,
		`== != >= <= && || ?: ?. -> ..`,
		`@hidden @hash @varchar(100)`,
		`~!@#$%^&*()_+`,
		`用户 名前 이름`,
		`"\\"escaped\\""`,
		`model { } [ ] ( ) , : . @ ? = + - * / %`,
		"model\x00User",      // null byte
		"model\tUser\r\n{}\r\n",
		string([]byte{0xFF, 0xFE, 0xFD}), // invalid UTF-8
		`"` + string(make([]byte, 10000)),  // very long string
		`model ` + string(make([]byte, 1000)) + ` {}`, // very long identifier
	}

	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		l := New(input, "fuzz.luxo")
		tokens, _ := l.Tokenize()

		// must always end with EOF
		if len(tokens) == 0 {
			t.Fatal("Tokenize returned empty token list")
		}
		last := tokens[len(tokens)-1]
		if last.Type != 1 { // token.EOF
			t.Fatalf("last token is not EOF: %v", last)
		}
	})
}
