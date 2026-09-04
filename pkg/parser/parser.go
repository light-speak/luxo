package parser

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// Precedence levels for Pratt parser.
const (
	precNone       = 0
	precOr         = 1  // ||
	precAnd        = 2  // &&
	precElvis      = 3  // ?:
	precEquality   = 4  // == !=
	precComparison = 5  // > >= < <= in is
	precRange      = 6  // ..
	precAdd        = 7  // + -
	precMul        = 8  // * / %
	precUnary      = 9  // ! -
	precCall       = 10 // () . ?.
)

// Error represents a parser error.
type Error struct {
	Pos     token.Position
	Message string
}

func (e Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Pos, e.Message)
}

// bailout is a sentinel used for panic-based error recovery.
// When the parser encounters an unexpected token during a declaration,
// it panics with bailout so the top-level loop can recover and
// synchronize to the next declaration boundary.
type bailout struct{}

// Parser parses a token stream into an AST.
type Parser struct {
	tokens        []token.Token
	pos           int
	errors        []Error
	lastDoc       string
	noBraceLambda bool // when true, { is not treated as trailing lambda in expressions
}

// New creates a new Parser.
func New(tokens []token.Token) *Parser {
	return &Parser{tokens: tokens}
}

// Parse parses the token stream into a File AST.
func (p *Parser) Parse(filename string) (*ast.File, []Error) {
	file := &ast.File{Name: filename}

	for !p.isEOF() {
		p.parseTopLevelRecover(file)
	}

	return file, p.errors
}

// parseTopLevelRecover wraps parseTopLevel with panic recovery.
// When a declaration parse fails (via bailout panic), it catches the
// panic and synchronizes to the next top-level keyword so that
// subsequent valid declarations can still be parsed.
func (p *Parser) parseTopLevelRecover(file *ast.File) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r) // re-panic for unexpected errors
			}
			p.synchronize()
		}
	}()
	p.parseTopLevel(file)
}

// isTopLevelKeyword reports whether typ is a keyword that can start
// a top-level declaration.
func isTopLevelKeyword(typ token.Type) bool {
	switch typ {
	case token.Model, token.Interface, token.Enum, token.Sealed,
		token.KwType, token.Api, token.Fn, token.Error,
		token.Event, token.On, token.Middleware, token.Extend,
		token.Override, token.Use, token.Val, token.Var:
		return true
	}
	return false
}

// synchronize advances tokens until the parser reaches a top-level
// declaration boundary, allowing parsing to continue after an error.
func (p *Parser) synchronize() {
	for !p.isEOF() {
		// If current token is a top-level keyword, we can resume here.
		if isTopLevelKeyword(p.current().Type) {
			return
		}
		// Also treat DocComment before a top-level keyword as a boundary.
		if p.current().Type == token.DocComment {
			return
		}
		p.advance()
	}
}

// parseTopLevel parses one top-level declaration and appends it to the file.
func (p *Parser) parseTopLevel(file *ast.File) {
	p.consumeDoc()

	switch {
	case p.check(token.Model):
		file.Models = append(file.Models, p.parseModel())
	case p.check(token.Interface):
		file.Interfaces = append(file.Interfaces, p.parseInterface())
	case p.check(token.Enum):
		file.Enums = append(file.Enums, p.parseEnum())
	case p.check(token.Sealed):
		file.Sealeds = append(file.Sealeds, p.parseSealed())
	case p.check(token.KwType):
		file.Types = append(file.Types, p.parseType())
	case p.check(token.Api), p.check(token.Override):
		file.APIs = append(file.APIs, p.parseAPIOrOverride())
	case p.check(token.Fn):
		file.Functions = append(file.Functions, p.parseFn())
	default:
		p.parseTopLevelExtended(file)
	}
}

func (p *Parser) parseTopLevelExtended(file *ast.File) {
	switch {
	case p.check(token.Error):
		file.Errors = append(file.Errors, p.parseError())
	case p.check(token.Extend):
		file.Extends = append(file.Extends, p.parseExtend())
	case p.check(token.Use):
		p.parseUseDispatch(file)
	case p.check(token.Middleware):
		file.Middlewares = append(file.Middlewares, p.parseMiddleware())
	case p.check(token.Event):
		file.Events = append(file.Events, p.parseEvent())
	case p.check(token.On):
		file.Listeners = append(file.Listeners, p.parseOn())
	case p.check(token.Val), p.check(token.Var):
		file.Globals = append(file.Globals, p.parseValStmt())
	case p.check(token.Comment), p.check(token.EOF):
		p.handleNonDecl()
	default:
		p.error("unexpected token: %s", p.current().Val)
		p.advance()
	}
}

// parseAPIOrOverride parses an api declaration or an override api declaration.
func (p *Parser) parseAPIOrOverride() *ast.ApiDecl {
	if p.check(token.Override) {
		return p.parseOverrideAPI()
	}
	return p.parseAPI()
}

// handleNonDecl handles non-declaration tokens (comments and EOF).
func (p *Parser) handleNonDecl() {
	if p.check(token.Comment) {
		p.advance()
	}
	// EOF: do nothing, caller will break
}
