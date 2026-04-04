package lexer

import (
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/light-speak/luxo/pkg/token"
)

// Lexer tokenizes .luxo source code.
type Lexer struct {
	source string
	file   string
	pos    int
	line   int
	col    int

	tokens []token.Token
	errors []Error
}

// Error represents a lexer error with position.
type Error struct {
	Pos     token.Position
	Message string
}

func (e Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Pos, e.Message)
}

// New creates a new Lexer for the given source code.
func New(source, file string) *Lexer {
	return &Lexer{
		source: source,
		file:   file,
		line:   1,
		col:    1,
	}
}

// Tokenize scans the entire source and returns all tokens.
func (l *Lexer) Tokenize() ([]token.Token, []Error) {
	for {
		tok := l.nextToken()
		if tok.Type == token.Comment || tok.Type == token.DocComment {
			l.tokens = append(l.tokens, tok)
			continue
		}
		l.tokens = append(l.tokens, tok)
		if tok.Type == token.EOF {
			break
		}
	}
	return l.tokens, l.errors
}

func (l *Lexer) nextToken() token.Token {
	l.skipWhitespace()

	pos := l.position()
	ch := l.peek()

	if ch == 0 {
		return l.makeToken(token.EOF, "", pos)
	}

	// Try single-char tokens first
	if tok, ok := l.readSingleChar(ch, pos); ok {
		return tok
	}

	// Try compound operator tokens
	if tok, ok := l.readOperator(ch, pos); ok {
		return tok
	}

	switch {
	// String
	case ch == '"':
		return l.readString(pos)

	// Number or Duration
	case isDigit(ch):
		return l.readNumber(pos)

	// Identifier or Keyword
	case isLetter(ch) || ch == '_':
		return l.readIdent(pos)

	default:
		l.advance()
		l.error(pos, fmt.Sprintf("unexpected character: %c", ch))
		return l.makeToken(token.Illegal, string(ch), pos)
	}
}

// readSingleChar handles single-character tokens that need no lookahead.
func (l *Lexer) readSingleChar(ch rune, pos token.Position) (token.Token, bool) {
	var typ token.Type
	switch ch {
	case ':':
		typ = token.Colon
	case '{':
		typ = token.LBrace
	case '}':
		typ = token.RBrace
	case '(':
		typ = token.LParen
	case ')':
		typ = token.RParen
	case '[':
		typ = token.LBracket
	case ']':
		typ = token.RBracket
	case ',':
		typ = token.Comma
	case '@':
		typ = token.At
	default:
		return token.Token{}, false
	}
	l.advance()
	return l.makeToken(typ, string(ch), pos), true
}

// readOperator handles multi-character operators and delegated operator reads.
func (l *Lexer) readOperator(ch rune, pos token.Position) (token.Token, bool) {
	switch ch {
	case '+':
		return l.readCompoundAssign(pos, token.Plus, "+", token.PlusAssign, "+="), true
	case '*':
		return l.readCompoundAssign(pos, token.Star, "*", token.StarAssign, "*="), true
	case '%':
		return l.readCompoundAssign(pos, token.Percent, "%", token.PercentAssign, "%="), true
	case '.':
		return l.readDotOrRange(pos), true
	case '?':
		return l.readQuestion(pos), true
	case '=':
		return l.readEquals(pos), true
	case '!':
		return l.readBang(pos), true
	case '>':
		return l.readGreater(pos), true
	case '<':
		return l.readLess(pos), true
	case '&':
		return l.readAmpersand(pos), true
	case '|':
		return l.readPipe(pos), true
	case '-':
		return l.readMinus(pos), true
	case '/':
		return l.readSlashOrComment(pos), true
	}
	return token.Token{}, false
}

// readCompoundAssign reads an operator that may be followed by '=' for a compound assignment.
func (l *Lexer) readCompoundAssign(pos token.Position, simpleType token.Type, simpleLit string, assignType token.Type, assignLit string) token.Token {
	l.advance()
	if l.peek() == '=' {
		l.advance()
		return l.makeToken(assignType, assignLit, pos)
	}
	return l.makeToken(simpleType, simpleLit, pos)
}

// readDotOrRange reads a dot, dot-dot, or dot-dot-dot token.
func (l *Lexer) readDotOrRange(pos token.Position) token.Token {
	l.advance()
	if l.peek() == '.' {
		l.advance()
		if l.peek() == '.' {
			l.advance()
			return l.makeToken(token.DotDotDot, "...", pos)
		}
		return l.makeToken(token.DotDot, "..", pos)
	}
	return l.makeToken(token.Dot, ".", pos)
}

// readQuestion reads a question mark, elvis, or safe-dot token.
func (l *Lexer) readQuestion(pos token.Position) token.Token {
	l.advance()
	switch l.peek() {
	case ':':
		l.advance()
		return l.makeToken(token.Elvis, "?:", pos)
	case '.':
		l.advance()
		return l.makeToken(token.SafeDot, "?.", pos)
	}
	return l.makeToken(token.Question, "?", pos)
}

// readEquals reads an assign or equality token.
func (l *Lexer) readEquals(pos token.Position) token.Token {
	l.advance()
	if l.peek() == '=' {
		l.advance()
		return l.makeToken(token.Eq, "==", pos)
	}
	return l.makeToken(token.Assign, "=", pos)
}

// readBang reads a bang or not-equal token.
func (l *Lexer) readBang(pos token.Position) token.Token {
	l.advance()
	if l.peek() == '=' {
		l.advance()
		return l.makeToken(token.Neq, "!=", pos)
	}
	return l.makeToken(token.Bang, "!", pos)
}

// readGreater reads a greater-than or greater-than-or-equal token.
func (l *Lexer) readGreater(pos token.Position) token.Token {
	l.advance()
	if l.peek() == '=' {
		l.advance()
		return l.makeToken(token.Gte, ">=", pos)
	}
	return l.makeToken(token.Gt, ">", pos)
}

// readLess reads a less-than, less-than-or-equal, or channel-send token.
func (l *Lexer) readLess(pos token.Position) token.Token {
	l.advance()
	switch l.peek() {
	case '=':
		l.advance()
		return l.makeToken(token.Lte, "<=", pos)
	case '-':
		l.advance()
		return l.makeToken(token.ChanSend, "<-", pos)
	}
	return l.makeToken(token.Lt, "<", pos)
}

// readAmpersand reads an ampersand (expecting &&).
func (l *Lexer) readAmpersand(pos token.Position) token.Token {
	l.advance()
	if l.peek() == '&' {
		l.advance()
		return l.makeToken(token.And, "&&", pos)
	}
	l.error(pos, "unexpected '&', did you mean '&&'?")
	return l.makeToken(token.Illegal, "&", pos)
}

// readPipe reads a pipe (expecting ||).
func (l *Lexer) readPipe(pos token.Position) token.Token {
	l.advance()
	if l.peek() == '|' {
		l.advance()
		return l.makeToken(token.Or, "||", pos)
	}
	l.error(pos, "unexpected '|', did you mean '||'?")
	return l.makeToken(token.Illegal, "|", pos)
}

// readMinus reads a minus, arrow, or minus-assign token.
func (l *Lexer) readMinus(pos token.Position) token.Token {
	l.advance()
	switch l.peek() {
	case '>':
		l.advance()
		return l.makeToken(token.Arrow, "->", pos)
	case '=':
		l.advance()
		return l.makeToken(token.MinusAssign, "-=", pos)
	}
	return l.makeToken(token.Minus, "-", pos)
}

// readSlashOrComment reads a slash, slash-assign, line comment, doc comment, or block comment.
func (l *Lexer) readSlashOrComment(pos token.Position) token.Token {
	l.advance()
	switch l.peek() {
	case '/':
		l.advance()
		if l.peek() == '/' {
			l.advance()
			return l.readDocComment(pos)
		}
		return l.readLineComment(pos)
	case '*':
		l.advance()
		return l.readBlockComment(pos)
	case '=':
		l.advance()
		return l.makeToken(token.SlashAssign, "/=", pos)
	}
	return l.makeToken(token.Slash, "/", pos)
}

// readIdent reads an identifier or keyword.
func (l *Lexer) readIdent(pos token.Position) token.Token {
	start := l.pos
	for isLetter(l.peek()) || isDigit(l.peek()) || l.peek() == '_' {
		l.advance()
	}
	val := l.source[start:l.pos]
	typ := token.LookupKeyword(val)
	return l.makeToken(typ, val, pos)
}

// readNumber reads an integer, float, or duration literal.
func (l *Lexer) readNumber(pos token.Position) token.Token {
	start := l.pos
	for isDigit(l.peek()) {
		l.advance()
	}

	// Float
	if l.peek() == '.' && l.peekAt(1) != '.' {
		l.advance()
		for isDigit(l.peek()) {
			l.advance()
		}
		val := l.source[start:l.pos]
		return l.makeToken(token.Float, val, pos)
	}

	// Duration suffix: d, h, m, s, ms
	ch := l.peek()
	if ch == 'd' || ch == 'h' || ch == 's' {
		l.advance()
		val := l.source[start:l.pos]
		return l.makeToken(token.Duration, val, pos)
	}
	if ch == 'm' {
		l.advance()
		if l.peek() == 's' {
			l.advance()
		}
		val := l.source[start:l.pos]
		return l.makeToken(token.Duration, val, pos)
	}

	val := l.source[start:l.pos]
	return l.makeToken(token.Int, val, pos)
}

// readString reads a string literal, supporting template interpolation.
func (l *Lexer) readString(pos token.Position) token.Token {
	l.advance() // skip opening "
	start := l.pos
	for {
		ch := l.peek()
		if ch == 0 {
			l.error(pos, "unterminated string")
			return l.makeToken(token.Illegal, l.source[start:l.pos], pos)
		}
		if ch == '\\' {
			l.advance() // skip backslash
			l.advance() // skip escaped char
			continue
		}
		if ch == '"' {
			val := l.source[start:l.pos]
			l.advance() // skip closing "
			return l.makeToken(token.String, val, pos)
		}
		l.advance()
	}
}

// readLineComment reads a single-line comment (// ...).
func (l *Lexer) readLineComment(pos token.Position) token.Token {
	l.skipWhitespace() // skip space after //
	start := l.pos
	for l.peek() != '\n' && l.peek() != 0 {
		l.advance()
	}
	val := l.source[start:l.pos]
	return l.makeToken(token.Comment, val, pos)
}

// readDocComment reads a doc comment (/// ...).
func (l *Lexer) readDocComment(pos token.Position) token.Token {
	l.skipWhitespace()
	start := l.pos
	for l.peek() != '\n' && l.peek() != 0 {
		l.advance()
	}
	val := l.source[start:l.pos]
	return l.makeToken(token.DocComment, val, pos)
}

// readBlockComment reads a block comment (/* ... */), supporting nesting.
func (l *Lexer) readBlockComment(pos token.Position) token.Token {
	start := l.pos
	depth := 1
	for depth > 0 {
		ch := l.peek()
		if ch == 0 {
			l.error(pos, "unterminated block comment")
			return l.makeToken(token.Illegal, l.source[start:l.pos], pos)
		}
		if ch == '/' && l.peekAt(1) == '*' {
			depth++
			l.advance()
			l.advance()
			continue
		}
		if ch == '*' && l.peekAt(1) == '/' {
			depth--
			l.advance()
			l.advance()
			continue
		}
		l.advance()
	}
	val := l.source[start : l.pos-2] // exclude closing */
	return l.makeToken(token.Comment, val, pos)
}

// Helper methods

func (l *Lexer) peek() rune {
	if l.pos >= len(l.source) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.source[l.pos:])
	return r
}

func (l *Lexer) peekAt(offset int) rune {
	p := l.pos + offset
	if p >= len(l.source) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.source[p:])
	return r
}

func (l *Lexer) advance() {
	if l.pos >= len(l.source) {
		return
	}
	r, size := utf8.DecodeRuneInString(l.source[l.pos:])
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	l.pos += size
}

func (l *Lexer) skipWhitespace() {
	for {
		ch := l.peek()
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			l.advance()
		} else {
			break
		}
	}
}

func (l *Lexer) position() token.Position {
	return token.Position{
		File:   l.file,
		Line:   l.line,
		Col:    l.col,
		Offset: l.pos,
	}
}

func (l *Lexer) makeToken(typ token.Type, val string, pos token.Position) token.Token {
	return token.Token{Type: typ, Val: val, Pos: pos}
}

func (l *Lexer) error(pos token.Position, msg string) {
	l.errors = append(l.errors, Error{Pos: pos, Message: msg})
}

func isDigit(ch rune) bool {
	return ch >= '0' && ch <= '9'
}

func isLetter(ch rune) bool {
	return unicode.IsLetter(ch)
}
