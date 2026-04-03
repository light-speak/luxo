package lexer

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/token"
)

// ========== Unicode Edge Cases ==========

func TestUnicodeEmoji(t *testing.T) {
	// emoji is not unicode.IsLetter, so it's ILLEGAL — expected behavior
	l := New("val 🔥 = 1", "test.luxo")
	_, errs := l.Tokenize()
	if len(errs) == 0 {
		t.Error("expected error for emoji (not a valid identifier char)")
	}
}

func TestUnicodeCJK(t *testing.T) {
	inputs := []string{"用户名", "ユーザー", "사용자"}
	for _, input := range inputs {
		l := New(input, "test.luxo")
		tokens, errs := l.Tokenize()
		if len(errs) > 0 {
			t.Errorf("unexpected errors for %q: %v", input, errs)
		}
		if tokens[0].Type != token.Ident {
			t.Errorf("expected IDENT for %q, got %s", input, tokens[0].Type)
		}
		if tokens[0].Val != input {
			t.Errorf("expected val %q, got %q", input, tokens[0].Val)
		}
	}
}

func TestUnicodePositionTracking(t *testing.T) {
	// multi-byte chars should still track positions correctly
	l := New("用户 名前", "test.luxo")
	tokens, _ := l.Tokenize()
	if len(tokens) < 2 {
		t.Fatal("expected at least 2 tokens")
	}
	// second token should be on same line
	if tokens[1].Pos.Line != 1 {
		t.Errorf("expected line 1 for second token, got %d", tokens[1].Pos.Line)
	}
}

// ========== Large Input Tests ==========

func TestLargeFile(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 1000; i++ {
		b.WriteString("model Model")
		b.WriteString(strings.Repeat("A", 5))
		b.WriteString(" { name: String }\n")
	}
	input := b.String()

	l := New(input, "test.luxo")
	tokens, _ := l.Tokenize()

	if len(tokens) < 1000 {
		t.Errorf("expected many tokens for large file, got %d", len(tokens))
	}
}

func TestVeryLongIdentifier(t *testing.T) {
	ident := strings.Repeat("a", 10000)
	l := New(ident, "test.luxo")
	tokens, errs := l.Tokenize()
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if tokens[0].Type != token.Ident {
		t.Errorf("expected IDENT, got %s", tokens[0].Type)
	}
	if len(tokens[0].Val) != 10000 {
		t.Errorf("expected 10000 char ident, got %d", len(tokens[0].Val))
	}
}

func TestVeryLongString(t *testing.T) {
	content := strings.Repeat("x", 10000)
	input := `"` + content + `"`
	l := New(input, "test.luxo")
	tokens, errs := l.Tokenize()
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if tokens[0].Type != token.String {
		t.Errorf("expected STRING, got %s", tokens[0].Type)
	}
}

func TestVeryLargeNumber(t *testing.T) {
	num := strings.Repeat("9", 100)
	l := New(num, "test.luxo")
	tokens, errs := l.Tokenize()
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if tokens[0].Type != token.Int {
		t.Errorf("expected INT, got %s", tokens[0].Type)
	}
}

// ========== String Edge Cases ==========

func TestEmptyString(t *testing.T) {
	l := New(`""`, "test.luxo")
	tokens, _ := l.Tokenize()
	if tokens[0].Type != token.String {
		t.Errorf("expected STRING, got %s", tokens[0].Type)
	}
	if tokens[0].Val != "" {
		t.Errorf("expected empty string val, got %q", tokens[0].Val)
	}
}

func TestStringWithAllEscapes(t *testing.T) {
	l := New(`"\\\"\n\r\t"`, "test.luxo")
	tokens, errs := l.Tokenize()
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if tokens[0].Type != token.String {
		t.Errorf("expected STRING, got %s", tokens[0].Type)
	}
}

func TestStringWithNewline(t *testing.T) {
	// unterminated string at end of line
	input := "\"hello\nworld\""
	l := New(input, "test.luxo")
	tokens, _ := l.Tokenize()
	// behavior depends on implementation: might be unterminated or span lines
	_ = tokens
}

// ========== Comment Edge Cases ==========

func TestEmptyComment(t *testing.T) {
	l := New("//\n42", "test.luxo")
	tokens, errs := l.Tokenize()
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	// comment content might include "42" since readLineComment skips whitespace
	// then reads until \n — but \n is right after //, so comment is empty,
	// then 42 should be on next line
	hasComment := false
	for _, tok := range tokens {
		if tok.Type == token.Comment {
			hasComment = true
		}
	}
	if !hasComment {
		t.Error("expected comment token")
	}
}

func TestEmptyBlockComment(t *testing.T) {
	l := New("/**/42", "test.luxo")
	tokens, _ := l.Tokenize()
	hasInt := false
	for _, tok := range tokens {
		if tok.Type == token.Int && tok.Val == "42" {
			hasInt = true
		}
	}
	if !hasInt {
		t.Error("expected int after empty block comment")
	}
}

func TestDocCommentEmpty(t *testing.T) {
	l := New("///\nmodel User {}", "test.luxo")
	tokens, errs := l.Tokenize()
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	hasDoc := false
	for _, tok := range tokens {
		if tok.Type == token.DocComment {
			hasDoc = true
		}
	}
	if !hasDoc {
		t.Error("expected doc comment token")
	}
}

// ========== Number Edge Cases ==========

func TestZero(t *testing.T) {
	l := New("0", "test.luxo")
	tokens, _ := l.Tokenize()
	if tokens[0].Type != token.Int || tokens[0].Val != "0" {
		t.Errorf("expected INT 0, got %s %q", tokens[0].Type, tokens[0].Val)
	}
}

func TestFloatZero(t *testing.T) {
	l := New("0.0", "test.luxo")
	tokens, _ := l.Tokenize()
	if tokens[0].Type != token.Float || tokens[0].Val != "0.0" {
		t.Errorf("expected FLOAT 0.0, got %s %q", tokens[0].Type, tokens[0].Val)
	}
}

func TestDurationAllUnits(t *testing.T) {
	units := []string{"1d", "2h", "3m", "4s", "500ms"}
	for _, u := range units {
		l := New(u, "test.luxo")
		tokens, _ := l.Tokenize()
		if tokens[0].Type != token.Duration {
			t.Errorf("expected DURATION for %q, got %s", u, tokens[0].Type)
		}
		if tokens[0].Val != u {
			t.Errorf("expected val %q, got %q", u, tokens[0].Val)
		}
	}
}

// ========== Whitespace Edge Cases ==========

func TestTabsAndSpaces(t *testing.T) {
	l := New("\t  \t  model\t  User  \t{}", "test.luxo")
	tokens, _ := l.Tokenize()
	hasModel := false
	hasUser := false
	for _, tok := range tokens {
		if tok.Type == token.Model {
			hasModel = true
		}
		if tok.Val == "User" {
			hasUser = true
		}
	}
	if !hasModel || !hasUser {
		t.Error("expected model and User tokens")
	}
}

func TestWindowsLineEndings(t *testing.T) {
	l := New("model User {\r\n  name: String\r\n}", "test.luxo")
	tokens, _ := l.Tokenize()
	// "name" should be on line 2
	for _, tok := range tokens {
		if tok.Val == "name" {
			if tok.Pos.Line != 2 {
				t.Errorf("expected name on line 2, got %d", tok.Pos.Line)
			}
		}
	}
}

// ========== Token Position Accuracy ==========

func TestPositionAccuracyMultiLine(t *testing.T) {
	input := "model User {\n  name: String\n  email: String\n}"
	l := New(input, "test.luxo")
	tokens, _ := l.Tokenize()

	expected := map[string]struct{ line, col int }{
		"model": {1, 1},
		"User":  {1, 7},
		"name":  {2, 3},
		"email": {3, 3},
	}

	for _, tok := range tokens {
		if exp, ok := expected[tok.Val]; ok {
			if tok.Pos.Line != exp.line || tok.Pos.Col != exp.col {
				t.Errorf("token %q: expected %d:%d, got %d:%d",
					tok.Val, exp.line, exp.col, tok.Pos.Line, tok.Pos.Col)
			}
		}
	}
}
