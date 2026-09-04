package parser

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== Helpers ==========

func (p *Parser) currentPrec() int {
	switch p.current().Type {
	case token.Or:
		return precOr
	case token.And:
		return precAnd
	case token.Elvis, token.BangElvis:
		return precElvis
	case token.Eq, token.Neq:
		return precEquality
	case token.Gt, token.Gte, token.Lt, token.Lte, token.In, token.Is:
		return precComparison
	case token.DotDot:
		return precRange
	case token.ChanSend:
		return precAdd
	case token.Plus, token.Minus:
		return precAdd
	case token.Star, token.Slash, token.Percent:
		return precMul
	case token.Dot, token.SafeDot, token.LParen, token.Question:
		return precCall
	case token.LBrace:
		// trailing lambda only for callable expressions, and not in condition context
		if p.noBraceLambda {
			return precNone
		}
		return precCall
	}
	return precNone
}

func (p *Parser) isBinaryOp() bool {
	switch p.current().Type {
	case token.Plus, token.Minus, token.Star, token.Slash, token.Percent,
		token.Eq, token.Neq, token.Gt, token.Gte, token.Lt, token.Lte,
		token.And, token.Or, token.In, token.Is:
		return true
	}
	return false
}

func (p *Parser) isCallable(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.Ident, *ast.MemberExpr:
		return true
	}
	return false
}

func (p *Parser) isTypeName(name string) bool {
	if len(name) == 0 {
		return false
	}
	return name[0] >= 'A' && name[0] <= 'Z'
}

func (p *Parser) parseObjectConstruction(pos token.Position, typeName string) ast.Expr {
	p.expect(token.LBrace)
	obj := &ast.ObjectExpr{Pos: pos, TypeName: typeName}
	for !p.check(token.RBrace) && !p.isEOF() {
		arg := &ast.NamedArg{}
		if p.check(token.Ident) && p.peekType() == token.Colon {
			arg.Name = p.expectIdent()
			p.expect(token.Colon)
			arg.Value = p.parseExpr(precNone)
		} else {
			arg.Value = p.parseExpr(precNone)
			// shorthand: { token } → { token: token }
			if ident, ok := arg.Value.(*ast.Ident); ok && arg.Name == "" {
				arg.Name = ident.Name
			}
		}
		obj.Fields = append(obj.Fields, arg)
		p.match(token.Comma)
	}
	p.expect(token.RBrace)
	return obj
}

// expectIdentOrKeyword accepts an identifier or any keyword as a name.
// Used in contexts like `use model { ... }` where keywords serve as module names.
func (p *Parser) expectIdentOrKeyword() string {
	tok := p.current()
	if tok.Type == token.Ident || token.IsKeyword(tok.Type) {
		p.advance()
		return tok.Val
	}
	p.error("expected identifier, got %s", tok.Type)
	p.advance()
	return ""
}

func (p *Parser) isKeywordUsedAsIdent() bool {
	// some keywords can be used as identifiers in certain contexts
	// e.g., field names: find, create, update, delete, error, etc.
	switch p.current().Type {
	case token.Error:
		return true
	}
	return false
}

func (p *Parser) parseQualifiedName() string {
	name := p.expectIdent()
	for p.match(token.Dot) {
		name += "." + p.expectIdent()
	}
	return name
}

// ========== Token Operations ==========

func (p *Parser) current() token.Token {
	if p.pos >= len(p.tokens) {
		return token.Token{Type: token.EOF}
	}
	return p.tokens[p.pos]
}

func (p *Parser) peek() token.Token {
	next := p.pos + 1
	if next >= len(p.tokens) {
		return token.Token{Type: token.EOF}
	}
	return p.tokens[next]
}

func (p *Parser) peekType() token.Type {
	return p.peek().Type
}

func (p *Parser) check(typ token.Type) bool {
	return p.current().Type == typ
}

func (p *Parser) match(typ token.Type) bool {
	if p.check(typ) {
		p.advance()
		return true
	}
	return false
}

func (p *Parser) advance() token.Token {
	tok := p.current()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

func (p *Parser) expect(typ token.Type) token.Token {
	if p.check(typ) {
		return p.advance()
	}
	p.error("expected %s, got %s", typ, p.current().Type)
	panic(bailout{})
}

func (p *Parser) expectIdent() string {
	if p.check(token.Ident) || p.isKeywordUsedAsIdent() {
		return p.advance().Val
	}
	p.error("expected identifier, got %s", p.current().Type)
	panic(bailout{})
}

func (p *Parser) isEOF() bool {
	return p.current().Type == token.EOF
}

func (p *Parser) consumeDoc() {
	for p.check(token.DocComment) {
		if p.lastDoc != "" {
			p.lastDoc += "\n"
		}
		p.lastDoc += p.current().Val
		p.advance()
	}
}

func (p *Parser) takeDoc() string {
	doc := p.lastDoc
	p.lastDoc = ""
	return doc
}

func (p *Parser) error(format string, args ...any) {
	p.errors = append(p.errors, Error{
		Pos:     p.current().Pos,
		Message: fmt.Sprintf(format, args...),
	})
}
