package token

import "testing"

func TestTypeStringKnown(t *testing.T) {
	tests := []struct {
		typ  Type
		want string
	}{
		{Illegal, "ILLEGAL"},
		{EOF, "EOF"},
		{Comment, "COMMENT"},
		{DocComment, "DOC_COMMENT"},
		{Ident, "IDENT"},
		{Int, "INT"},
		{Float, "FLOAT"},
		{String, "STRING"},
		{Duration, "DURATION"},
		{Colon, ":"},
		{LBrace, "{"},
		{RBrace, "}"},
		{LParen, "("},
		{RParen, ")"},
		{LBracket, "["},
		{RBracket, "]"},
		{Comma, ","},
		{Dot, "."},
		{At, "@"},
		{Question, "?"},
		{Assign, "="},
		{Plus, "+"},
		{Minus, "-"},
		{Star, "*"},
		{Slash, "/"},
		{Percent, "%"},
		{Bang, "!"},
		{Eq, "=="},
		{Neq, "!="},
		{Gt, ">"},
		{Gte, ">="},
		{Lt, "<"},
		{Lte, "<="},
		{And, "&&"},
		{Or, "||"},
		{Arrow, "->"},
		{DotDot, ".."},
		{Elvis, "?:"},
		{SafeDot, "?."},
		{Model, "model"},
		{Interface, "interface"},
		{Enum, "enum"},
		{Sealed, "sealed"},
		{KwType, "type"},
		{Fn, "fn"},
		{Api, "api"},
		{Error, "error"},
		{Middleware, "middleware"},
		{Extend, "extend"},
		{Override, "override"},
		{Use, "use"},
		{Through, "through"},
		{Stream, "stream"},
		{Val, "val"},
		{When, "when"},
		{If, "if"},
		{Else, "else"},
		{For, "for"},
		{In, "in"},
		{Return, "return"},
		{Is, "is"},
		{Find, "find"},
		{Create, "create"},
		{Update, "update"},
		{Delete, "delete"},
		{Transaction, "transaction"},
		{Emit, "emit"},
		{Throw, "throw"},
		{True, "true"},
		{False, "false"},
		{Null, "null"},
	}
	for _, tt := range tests {
		got := tt.typ.String()
		if got != tt.want {
			t.Errorf("Type(%d).String() = %q, want %q", tt.typ, got, tt.want)
		}
	}
}

func TestTypeStringUnknown(t *testing.T) {
	unknown := Type(9999)
	if got := unknown.String(); got != "UNKNOWN" {
		t.Errorf("Type(9999).String() = %q, want %q", got, "UNKNOWN")
	}
	// kwStart and kwEnd are also not in tokenNames
	if got := kwStart.String(); got != "UNKNOWN" {
		t.Errorf("kwStart.String() = %q, want %q", got, "UNKNOWN")
	}
}

func TestLookupKeywordFound(t *testing.T) {
	keywords := map[string]Type{
		"model": Model, "interface": Interface, "enum": Enum,
		"sealed": Sealed, "type": KwType, "fn": Fn, "api": Api,
		"error": Error, "middleware": Middleware, "extend": Extend,
		"override": Override, "use": Use, "through": Through,
		"stream": Stream, "val": Val, "when": When, "if": If,
		"else": Else, "for": For, "in": In, "return": Return,
		"is": Is, "find": Find, "create": Create, "update": Update,
		"delete": Delete, "transaction": Transaction, "emit": Emit,
		"throw": Throw, "true": True, "false": False, "null": Null,
	}
	for kw, want := range keywords {
		got := LookupKeyword(kw)
		if got != want {
			t.Errorf("LookupKeyword(%q) = %v, want %v", kw, got, want)
		}
	}
}

func TestLookupKeywordNotFound(t *testing.T) {
	nonKeywords := []string{"foo", "bar", "User", "xyz", ""}
	for _, s := range nonKeywords {
		got := LookupKeyword(s)
		if got != Ident {
			t.Errorf("LookupKeyword(%q) = %v, want Ident", s, got)
		}
	}
}

func TestIsKeyword(t *testing.T) {
	// Keywords should return true
	keywordTypes := []Type{
		Model, Interface, Enum, Sealed, KwType, Fn, Api, Error,
		Middleware, Extend, Override, Use, Through, Stream,
		Val, When, If, Else, For, In, Return, Is,
		Find, Create, Update, Delete, Transaction, Emit, Throw,
		True, False, Null,
	}
	for _, tt := range keywordTypes {
		if !IsKeyword(tt) {
			t.Errorf("IsKeyword(%v) = false, want true", tt)
		}
	}

	// Non-keywords should return false
	nonKeyword := []Type{Illegal, EOF, Comment, Ident, Int, Float, String,
		Colon, LBrace, RBrace, Plus, Minus, Eq, kwStart, kwEnd}
	for _, tt := range nonKeyword {
		if IsKeyword(tt) {
			t.Errorf("IsKeyword(%v) = true, want false", tt)
		}
	}
}

func TestPositionStringWithFile(t *testing.T) {
	p := Position{File: "main.luxo", Line: 10, Col: 5}
	want := "main.luxo:10:5"
	if got := p.String(); got != want {
		t.Errorf("Position.String() = %q, want %q", got, want)
	}
}

func TestPositionStringWithoutFile(t *testing.T) {
	p := Position{Line: 3, Col: 12}
	want := "3:12"
	if got := p.String(); got != want {
		t.Errorf("Position.String() = %q, want %q", got, want)
	}
}

func TestPositionStringSingleDigit(t *testing.T) {
	p := Position{Line: 1, Col: 1}
	want := "1:1"
	if got := p.String(); got != want {
		t.Errorf("Position.String() = %q, want %q", got, want)
	}
}

func TestPositionStringMultiDigit(t *testing.T) {
	// Tests itoa with multi-digit numbers (>= 10)
	p := Position{Line: 123, Col: 45}
	want := "123:45"
	if got := p.String(); got != want {
		t.Errorf("Position.String() = %q, want %q", got, want)
	}
}

func TestTokenStringWithValue(t *testing.T) {
	tok := Token{Type: Ident, Val: "foo"}
	want := "IDENT(foo)"
	if got := tok.String(); got != want {
		t.Errorf("Token.String() = %q, want %q", got, want)
	}
}

func TestTokenStringWithoutValue(t *testing.T) {
	tok := Token{Type: LBrace}
	want := "{"
	if got := tok.String(); got != want {
		t.Errorf("Token.String() = %q, want %q", got, want)
	}
}

func TestTokenStringKeyword(t *testing.T) {
	tok := Token{Type: Model, Val: "model"}
	want := "model(model)"
	if got := tok.String(); got != want {
		t.Errorf("Token.String() = %q, want %q", got, want)
	}
}

func TestItoaZero(t *testing.T) {
	// itoa(0) - the loop doesn't execute for 0, returns ""
	// We test via Position with Line=0 Col=0
	p := Position{Line: 0, Col: 0}
	got := p.String()
	// itoa(0) returns "" since 0 is not < 10 check for rune, but actually 0 < 10 is true
	// so itoa(0) returns string(rune('0'+0)) = "0"
	want := "0:0"
	if got != want {
		t.Errorf("Position.String() with zeros = %q, want %q", got, want)
	}
}
