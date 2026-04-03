package lexer

import (
	"testing"

	"github.com/light-speak/luxo/pkg/token"
)

func TestBasicTokens(t *testing.T) {
	input := `model User : Base {
  name: String @varchar(100)
  email: String @unique
}`
	expected := []struct {
		typ token.Type
		val string
	}{
		{token.Model, "model"},
		{token.Ident, "User"},
		{token.Colon, ":"},
		{token.Ident, "Base"},
		{token.LBrace, "{"},
		{token.Ident, "name"},
		{token.Colon, ":"},
		{token.Ident, "String"},
		{token.At, "@"},
		{token.Ident, "varchar"},
		{token.LParen, "("},
		{token.Int, "100"},
		{token.RParen, ")"},
		{token.Ident, "email"},
		{token.Colon, ":"},
		{token.Ident, "String"},
		{token.At, "@"},
		{token.Ident, "unique"},
		{token.RBrace, "}"},
		{token.EOF, ""},
	}

	l := New(input, "test.luxo")
	tokens, errs := l.Tokenize()
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}

	for i, exp := range expected {
		if tokens[i].Type != exp.typ {
			t.Errorf("token[%d]: expected type %s, got %s", i, exp.typ, tokens[i].Type)
		}
		if tokens[i].Val != exp.val {
			t.Errorf("token[%d]: expected val %q, got %q", i, exp.val, tokens[i].Val)
		}
	}
}

func TestOperators(t *testing.T) {
	input := `== != >= <= && || ?: ?. -> .. + - * / % ! = > <`
	expected := []token.Type{
		token.Eq, token.Neq, token.Gte, token.Lte,
		token.And, token.Or, token.Elvis, token.SafeDot,
		token.Arrow, token.DotDot, token.Plus, token.Minus,
		token.Star, token.Slash, token.Percent, token.Bang,
		token.Assign, token.Gt, token.Lt,
		token.EOF,
	}

	l := New(input, "test.luxo")
	tokens, errs := l.Tokenize()
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	for i, exp := range expected {
		if tokens[i].Type != exp {
			t.Errorf("token[%d]: expected %s, got %s (%q)", i, exp, tokens[i].Type, tokens[i].Val)
		}
	}
}

func TestKeywords(t *testing.T) {
	input := `model interface enum sealed type fn api error middleware
extend override use through stream
val when if else for in return is
find create update delete transaction emit throw
true false null`

	l := New(input, "test.luxo")
	tokens, errs := l.Tokenize()
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	for _, tok := range tokens {
		if tok.Type == token.EOF {
			break
		}
		if !token.IsKeyword(tok.Type) {
			t.Errorf("expected keyword for %q, got %s", tok.Val, tok.Type)
		}
	}
}

func TestNumbers(t *testing.T) {
	tests := []struct {
		input string
		typ   token.Type
		val   string
	}{
		{"42", token.Int, "42"},
		{"3.14", token.Float, "3.14"},
		{"0", token.Int, "0"},
		{"100", token.Int, "100"},
	}

	for _, tt := range tests {
		l := New(tt.input, "test.luxo")
		tokens, _ := l.Tokenize()
		if tokens[0].Type != tt.typ {
			t.Errorf("input %q: expected type %s, got %s", tt.input, tt.typ, tokens[0].Type)
		}
		if tokens[0].Val != tt.val {
			t.Errorf("input %q: expected val %q, got %q", tt.input, tt.val, tokens[0].Val)
		}
	}
}

func TestDuration(t *testing.T) {
	tests := []struct {
		input string
		val   string
	}{
		{"7d", "7d"},
		{"5m", "5m"},
		{"1h", "1h"},
		{"30s", "30s"},
		{"500ms", "500ms"},
	}

	for _, tt := range tests {
		l := New(tt.input, "test.luxo")
		tokens, _ := l.Tokenize()
		if tokens[0].Type != token.Duration {
			t.Errorf("input %q: expected DURATION, got %s", tt.input, tokens[0].Type)
		}
		if tokens[0].Val != tt.val {
			t.Errorf("input %q: expected val %q, got %q", tt.input, tt.val, tokens[0].Val)
		}
	}
}

func TestString(t *testing.T) {
	input := `"hello world"`
	l := New(input, "test.luxo")
	tokens, _ := l.Tokenize()
	if tokens[0].Type != token.String {
		t.Errorf("expected STRING, got %s", tokens[0].Type)
	}
	if tokens[0].Val != "hello world" {
		t.Errorf("expected %q, got %q", "hello world", tokens[0].Val)
	}
}

func TestStringEscape(t *testing.T) {
	input := `"hello \"world\""`
	l := New(input, "test.luxo")
	tokens, _ := l.Tokenize()
	if tokens[0].Type != token.String {
		t.Errorf("expected STRING, got %s", tokens[0].Type)
	}
	if tokens[0].Val != `hello \"world\"` {
		t.Errorf("expected %q, got %q", `hello \"world\"`, tokens[0].Val)
	}
}

func TestComments(t *testing.T) {
	input := `// this is a comment
model User {}`
	l := New(input, "test.luxo")
	tokens, _ := l.Tokenize()

	if tokens[0].Type != token.Comment {
		t.Errorf("expected COMMENT, got %s", tokens[0].Type)
	}
	if tokens[1].Type != token.Model {
		t.Errorf("expected MODEL, got %s", tokens[1].Type)
	}
}

func TestDocComment(t *testing.T) {
	input := `/// This is a doc comment
api getUser(id: Int): User`
	l := New(input, "test.luxo")
	tokens, _ := l.Tokenize()

	if tokens[0].Type != token.DocComment {
		t.Errorf("expected DOC_COMMENT, got %s", tokens[0].Type)
	}
	if tokens[1].Type != token.Api {
		t.Errorf("expected API, got %s", tokens[1].Type)
	}
}

func TestBlockComment(t *testing.T) {
	input := `/* block
comment */ model`
	l := New(input, "test.luxo")
	tokens, _ := l.Tokenize()

	if tokens[0].Type != token.Comment {
		t.Errorf("expected COMMENT, got %s", tokens[0].Type)
	}
	if tokens[1].Type != token.Model {
		t.Errorf("expected MODEL, got %s (%q)", tokens[1].Type, tokens[1].Val)
	}
}

func TestPositionTracking(t *testing.T) {
	input := "model User {\n  name: String\n}"
	l := New(input, "test.luxo")
	tokens, _ := l.Tokenize()

	// "model" should be at line 1, col 1
	if tokens[0].Pos.Line != 1 || tokens[0].Pos.Col != 1 {
		t.Errorf("model: expected 1:1, got %d:%d", tokens[0].Pos.Line, tokens[0].Pos.Col)
	}

	// "name" should be at line 2, col 3
	for _, tok := range tokens {
		if tok.Val == "name" {
			if tok.Pos.Line != 2 || tok.Pos.Col != 3 {
				t.Errorf("name: expected 2:3, got %d:%d", tok.Pos.Line, tok.Pos.Col)
			}
			break
		}
	}
}

func TestComplexSchema(t *testing.T) {
	input := `model User : Base, Searchable {
  name:     String  @varchar(100)
  email:    String  @unique
  password: String  @hidden @hash
  role:     Role    = Role.USER
  avatar:   String?
  posts:    [Post]
}

enum Role {
  USER
  ADMIN
  MODERATOR
}

api getUser(id: Int): User @cache(ttl: 60)
api register(input: RegisterInput): AuthResult {
  input.password.length >= 8 ?: throw error.password_too_short
  val user = create(User, name: input.name, email: input.email)
  return AuthResult { token: generateToken(user, expires: 7d), user: user }
}`

	l := New(input, "test.luxo")
	tokens, errs := l.Tokenize()
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	// Should have tokens and end with EOF
	if tokens[len(tokens)-1].Type != token.EOF {
		t.Errorf("expected EOF at end")
	}

	// Count some expected tokens
	counts := map[token.Type]int{}
	for _, tok := range tokens {
		counts[tok.Type]++
	}

	if counts[token.Model] != 1 {
		t.Errorf("expected 1 model, got %d", counts[token.Model])
	}
	if counts[token.Enum] != 1 {
		t.Errorf("expected 1 enum, got %d", counts[token.Enum])
	}
	if counts[token.Api] != 2 {
		t.Errorf("expected 2 api, got %d", counts[token.Api])
	}
	if counts[token.Elvis] != 1 {
		t.Errorf("expected 1 elvis, got %d", counts[token.Elvis])
	}
	if counts[token.Duration] != 1 {
		t.Errorf("expected 1 duration (7d), got %d", counts[token.Duration])
	}
}

func TestErrorRecovery(t *testing.T) {
	input := `model User { ~ name: String }`
	l := New(input, "test.luxo")
	tokens, errs := l.Tokenize()

	if len(errs) == 0 {
		t.Error("expected errors for invalid character ~")
	}

	// Should still have tokens after the error
	hasModel := false
	for _, tok := range tokens {
		if tok.Type == token.Model {
			hasModel = true
		}
	}
	if !hasModel {
		t.Error("expected to find model token despite error")
	}
}

func TestDotVsDotDot(t *testing.T) {
	input := `user.name 1..10`
	l := New(input, "test.luxo")
	tokens, _ := l.Tokenize()

	expected := []token.Type{
		token.Ident, token.Dot, token.Ident,
		token.Int, token.DotDot, token.Int,
		token.EOF,
	}

	for i, exp := range expected {
		if tokens[i].Type != exp {
			t.Errorf("token[%d]: expected %s, got %s", i, exp, tokens[i].Type)
		}
	}
}

func TestQuestionVariants(t *testing.T) {
	input := `String? user?.name value ?: fallback`
	l := New(input, "test.luxo")
	tokens, _ := l.Tokenize()

	expected := []token.Type{
		token.Ident, token.Question,          // String?
		token.Ident, token.SafeDot, token.Ident, // user?.name
		token.Ident, token.Elvis, token.Ident,    // value ?: fallback
		token.EOF,
	}

	for i, exp := range expected {
		if tokens[i].Type != exp {
			t.Errorf("token[%d]: expected %s, got %s (%q)", i, exp, tokens[i].Type, tokens[i].Val)
		}
	}
}
