package token

// Type represents the type of a lexical token.
type Type int

const (
	// Special
	Illegal Type = iota
	EOF
	Comment    // // ...
	DocComment // /// ...

	// Literals
	Ident    // identifier
	Int      // 42
	Float    // 3.14
	String   // "hello"
	Duration // 7d, 5m, 1h, 30s, 500ms

	// Symbols
	Colon    // :
	LBrace   // {
	RBrace   // }
	LParen   // (
	LParen2  // placeholder - not used
	RParen   // )
	LBracket // [
	RBracket // ]
	Comma    // ,
	Dot      // .
	At       // @
	Question // ?

	// Operators
	Assign        // =
	Plus          // +
	Minus         // -
	Star          // *
	Slash         // /
	Percent       // %
	Bang          // !
	Eq            // ==
	Neq           // !=
	Gt            // >
	Gte           // >=
	Lt            // <
	Lte           // <=
	And           // &&
	Or            // ||
	Arrow         // ->
	DotDot        // ..
	DotDotDot     // ...
	Elvis         // ?:
	SafeDot       // ?.
	ChanSend      // <-
	PlusAssign    // +=
	MinusAssign   // -=
	StarAssign    // *=
	SlashAssign   // /=
	PercentAssign // %=

	// Keywords - Definitions
	kwStart
	Model      // model
	Interface  // interface
	Enum       // enum
	Sealed     // sealed
	KwType     // type
	Fn         // fn
	Api        // api
	Error      // error
	Middleware // middleware
	Event      // event

	// Keywords - Modifiers
	Extend   // extend
	Override // override
	Use      // use
	Stream   // stream

	// Keywords - Logic
	Val    // val
	Var    // var
	When   // when
	If     // if
	Else   // else
	For    // for
	In     // in
	Return // return
	Break  // break
	Is     // is
	On     // on
	My     // my

	// Keywords - Concurrency
	Async // async
	Await // await
	Yield // yield

	// Keywords - Operations
	Throw // throw
	Emit  // emit

	// Keywords - Constants
	True  // true
	False // false
	Null  // null
	kwEnd
)

var tokenNames = map[Type]string{
	Illegal:    "ILLEGAL",
	EOF:        "EOF",
	Comment:    "COMMENT",
	DocComment: "DOC_COMMENT",

	Ident:    "IDENT",
	Int:      "INT",
	Float:    "FLOAT",
	String:   "STRING",
	Duration: "DURATION",

	Colon:    ":",
	LBrace:   "{",
	RBrace:   "}",
	LParen:   "(",
	RParen:   ")",
	LBracket: "[",
	RBracket: "]",
	Comma:    ",",
	Dot:      ".",
	At:       "@",
	Question: "?",

	Assign:        "=",
	Plus:          "+",
	Minus:         "-",
	Star:          "*",
	Slash:         "/",
	Percent:       "%",
	Bang:          "!",
	Eq:            "==",
	Neq:           "!=",
	Gt:            ">",
	Gte:           ">=",
	Lt:            "<",
	Lte:           "<=",
	And:           "&&",
	Or:            "||",
	Arrow:         "->",
	DotDot:        "..",
	DotDotDot:     "...",
	Elvis:         "?:",
	SafeDot:       "?.",
	ChanSend:      "<-",
	PlusAssign:    "+=",
	MinusAssign:   "-=",
	StarAssign:    "*=",
	SlashAssign:   "/=",
	PercentAssign: "%=",

	Model:      "model",
	Interface:  "interface",
	Enum:       "enum",
	Sealed:     "sealed",
	KwType:     "type",
	Fn:         "fn",
	Api:        "api",
	Error:      "error",
	Middleware: "middleware",
	Event:      "event",
	Extend:     "extend",
	Override:   "override",
	Use:        "use",
	Stream:     "stream",
	Val:        "val",
	Var:        "var",
	When:       "when",
	If:         "if",
	Else:       "else",
	For:        "for",
	In:         "in",
	Return:     "return",
	Break:      "break",
	Is:         "is",
	On:         "on",
	My:         "my",
	Async:      "async",
	Await:      "await",
	Yield:      "yield",
	Throw:      "throw",
	Emit:       "emit",
	True:       "true",
	False:      "false",
	Null:       "null",
}

func (t Type) String() string {
	if s, ok := tokenNames[t]; ok {
		return s
	}
	return "UNKNOWN"
}

var keywords = map[string]Type{
	"model":      Model,
	"interface":  Interface,
	"enum":       Enum,
	"sealed":     Sealed,
	"type":       KwType,
	"fn":         Fn,
	"api":        Api,
	"error":      Error,
	"middleware": Middleware,
	"event":      Event,
	"extend":     Extend,
	"override":   Override,
	"use":        Use,
	"stream":     Stream,
	"val":        Val,
	"var":        Var,
	"when":       When,
	"if":         If,
	"else":       Else,
	"for":        For,
	"in":         In,
	"return":     Return,
	"break":      Break,
	"is":         Is,
	"on":         On,
	"my":         My,
	"async":      Async,
	"await":      Await,
	"yield":      Yield,
	"throw":      Throw,
	"emit":       Emit,
	"true":       True,
	"false":      False,
	"null":       Null,
}

// LookupKeyword returns the keyword token type for the given identifier,
// or Ident if it is not a keyword.
func LookupKeyword(ident string) Type {
	if tok, ok := keywords[ident]; ok {
		return tok
	}
	return Ident
}

// IsKeyword returns true if the token type is a keyword.
func IsKeyword(t Type) bool {
	return t > kwStart && t < kwEnd
}

// Position represents a source code position.
type Position struct {
	File   string
	Line   int
	Col    int
	Offset int
}

func (p Position) String() string {
	if p.File != "" {
		return p.File + ":" + itoa(p.Line) + ":" + itoa(p.Col)
	}
	return itoa(p.Line) + ":" + itoa(p.Col)
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}

// Token represents a lexical token with its type, value, and position.
type Token struct {
	Type Type
	Val  string
	Pos  Position
}

func (t Token) String() string {
	if t.Val != "" {
		return t.Type.String() + "(" + t.Val + ")"
	}
	return t.Type.String()
}
