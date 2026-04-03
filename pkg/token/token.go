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
	Assign    // =
	Plus      // +
	Minus     // -
	Star      // *
	Slash     // /
	Percent   // %
	Bang      // !
	Eq        // ==
	Neq       // !=
	Gt        // >
	Gte       // >=
	Lt        // <
	Lte       // <=
	And       // &&
	Or        // ||
	Arrow     // ->
	DotDot    // ..
	Elvis     // ?:
	SafeDot   // ?.

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

	// Keywords - Modifiers
	Extend   // extend
	Override // override
	Use      // use
	Through  // through
	Stream   // stream

	// Keywords - Logic
	Val    // val
	When   // when
	If     // if
	Else   // else
	For    // for
	In     // in
	Return // return
	Is     // is

	// Keywords - Operations
	Find        // find
	Create      // create
	Update      // update
	Delete      // delete
	Transaction // transaction
	Emit        // emit
	Throw       // throw

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

	Assign:  "=",
	Plus:    "+",
	Minus:   "-",
	Star:    "*",
	Slash:   "/",
	Percent: "%",
	Bang:    "!",
	Eq:      "==",
	Neq:     "!=",
	Gt:      ">",
	Gte:     ">=",
	Lt:      "<",
	Lte:     "<=",
	And:     "&&",
	Or:      "||",
	Arrow:   "->",
	DotDot:  "..",
	Elvis:   "?:",
	SafeDot: "?.",

	Model:      "model",
	Interface:  "interface",
	Enum:       "enum",
	Sealed:     "sealed",
	KwType:     "type",
	Fn:         "fn",
	Api:        "api",
	Error:      "error",
	Middleware: "middleware",
	Extend:     "extend",
	Override:   "override",
	Use:        "use",
	Through:    "through",
	Stream:     "stream",
	Val:        "val",
	When:       "when",
	If:         "if",
	Else:       "else",
	For:        "for",
	In:         "in",
	Return:     "return",
	Is:         "is",
	Find:       "find",
	Create:     "create",
	Update:     "update",
	Delete:     "delete",
	Transaction: "transaction",
	Emit:       "emit",
	Throw:      "throw",
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
	"model":       Model,
	"interface":   Interface,
	"enum":        Enum,
	"sealed":      Sealed,
	"type":        KwType,
	"fn":          Fn,
	"api":         Api,
	"error":       Error,
	"middleware":  Middleware,
	"extend":      Extend,
	"override":    Override,
	"use":         Use,
	"through":     Through,
	"stream":      Stream,
	"val":         Val,
	"when":        When,
	"if":          If,
	"else":        Else,
	"for":         For,
	"in":          In,
	"return":      Return,
	"is":          Is,
	"find":        Find,
	"create":      Create,
	"update":      Update,
	"delete":      Delete,
	"transaction": Transaction,
	"emit":        Emit,
	"throw":       Throw,
	"true":        True,
	"false":       False,
	"null":        Null,
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
