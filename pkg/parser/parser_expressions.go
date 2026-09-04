package parser

import (
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== Pratt Expression Parser ==========

func (p *Parser) parseExpr(prec int) ast.Expr {
	left := p.parsePrefixExpr()
	if left == nil {
		return nil
	}

	for prec < p.currentPrec() && !p.isEOF() {
		startPos := p.pos
		left = p.parseInfixExpr(left)
		if p.pos == startPos {
			break // prevent infinite loop
		}
	}
	return left
}

func (p *Parser) parsePrefixExpr() ast.Expr {
	pos := p.current().Pos

	switch {
	case p.check(token.Int), p.check(token.Float), p.check(token.String),
		p.check(token.Duration), p.check(token.True), p.check(token.False),
		p.check(token.Null):
		return p.parseLiteral(pos)

	case p.check(token.StringStart):
		return p.parseTemplateString(pos)

	case p.check(token.My):
		tok := p.advance()
		return &ast.Ident{Pos: pos, Name: tok.Val}

	case p.check(token.Ident) || p.isKeywordUsedAsIdent():
		return p.parseIdentOrConstruction(pos)

	case p.check(token.Bang), p.check(token.Minus),
		p.check(token.Throw), p.check(token.ChanSend):
		return p.parsePrefixUnary(pos)

	case p.check(token.LParen):
		return p.parseParenExpr()

	case p.check(token.LBracket):
		return p.parseListExpr(pos)

	case p.check(token.When):
		return p.parseWhenExpr(pos)

	case p.check(token.Yield), p.check(token.Async), p.check(token.Await):
		return p.parseConcurrencyExpr(pos)

	case p.check(token.For):
		return p.parseForStmt()

	default:
		p.error("expected expression, got %s", p.current().Type)
		p.advance()
		return nil
	}
}

// parseIdentOrConstruction parses an identifier or object construction expression.
func (p *Parser) parseIdentOrConstruction(pos token.Position) ast.Expr {
	tok := p.advance()
	ident := &ast.Ident{Pos: pos, Name: tok.Val}
	if p.check(token.LBrace) && p.isTypeName(tok.Val) {
		return p.parseObjectConstruction(pos, tok.Val)
	}
	return ident
}

// parsePrefixUnary parses a unary prefix expression (!, -, throw, <-).
func (p *Parser) parsePrefixUnary(pos token.Position) ast.Expr {
	tok := p.advance()
	switch tok.Type {
	case token.Bang:
		return &ast.UnaryExpr{Pos: pos, Op: "!", Value: p.parseExpr(precUnary)}
	case token.Minus:
		return &ast.UnaryExpr{Pos: pos, Op: "-", Value: p.parseExpr(precUnary)}
	case token.ChanSend:
		return &ast.UnaryExpr{Pos: pos, Op: "<-", Value: p.parseExpr(precUnary)}
	default: // token.Throw
		return &ast.UnaryExpr{Pos: pos, Op: "throw", Value: p.parseExpr(precNone)}
	}
}

// parseParenExpr parses a parenthesized expression.
func (p *Parser) parseParenExpr() ast.Expr {
	p.advance()
	expr := p.parseExpr(precNone)
	p.expect(token.RParen)
	return expr
}

// parseConcurrencyExpr parses yield, async, or await expressions.
func (p *Parser) parseConcurrencyExpr(pos token.Position) ast.Expr {
	tok := p.advance()
	switch tok.Type {
	case token.Yield:
		return &ast.YieldExpr{Pos: pos, Value: p.parseExpr(precNone)}
	case token.Async:
		return &ast.AsyncExpr{Pos: pos, Body: p.parseBlock()}
	default: // token.Await
		return &ast.AwaitExpr{Pos: pos, Body: p.parseBlock()}
	}
}

// parseTemplateString parses a string template: "text ${expr} more ${expr} end"
// Tokens arrive as: StringStart, expr..., StringMid, expr..., StringEnd
func (p *Parser) parseTemplateString(pos token.Position) ast.Expr {
	tmpl := &ast.TemplateString{Pos: pos}

	// StringStart: "text ${"
	start := p.advance()
	if start.Val != "" {
		tmpl.Parts = append(tmpl.Parts, &ast.Literal{Pos: start.Pos, Kind: token.String, Value: start.Val})
	}

	// Parse the first interpolated expression
	tmpl.Parts = append(tmpl.Parts, p.parseExpr(0))

	// Loop: StringMid or StringEnd
	for p.check(token.StringMid) {
		mid := p.advance()
		if mid.Val != "" {
			tmpl.Parts = append(tmpl.Parts, &ast.Literal{Pos: mid.Pos, Kind: token.String, Value: mid.Val})
		}
		tmpl.Parts = append(tmpl.Parts, p.parseExpr(0))
	}

	// StringEnd: "} text"
	if p.check(token.StringEnd) {
		end := p.advance()
		if end.Val != "" {
			tmpl.Parts = append(tmpl.Parts, &ast.Literal{Pos: end.Pos, Kind: token.String, Value: end.Val})
		}
	} else {
		p.error("expected end of template string")
	}

	return tmpl
}

// parseLiteral parses a literal expression (Int, Float, String, Duration, True, False, Null).
func (p *Parser) parseLiteral(pos token.Position) ast.Expr {
	switch {
	case p.check(token.Int):
		tok := p.advance()
		return &ast.Literal{Pos: pos, Kind: token.Int, Value: tok.Val}
	case p.check(token.Float):
		tok := p.advance()
		return &ast.Literal{Pos: pos, Kind: token.Float, Value: tok.Val}
	case p.check(token.String):
		tok := p.advance()
		return &ast.Literal{Pos: pos, Kind: token.String, Value: tok.Val}
	case p.check(token.Duration):
		tok := p.advance()
		return &ast.Literal{Pos: pos, Kind: token.Duration, Value: tok.Val}
	case p.check(token.True):
		p.advance()
		return &ast.Literal{Pos: pos, Kind: token.True, Value: "true"}
	case p.check(token.False):
		p.advance()
		return &ast.Literal{Pos: pos, Kind: token.False, Value: "false"}
	default: // token.Null
		p.advance()
		return &ast.Literal{Pos: pos, Kind: token.Null, Value: "null"}
	}
}

func (p *Parser) parseInfixExpr(left ast.Expr) ast.Expr {
	pos := p.current().Pos

	switch {
	// Member access: user.name
	case p.check(token.Dot):
		p.advance()
		field := p.expectIdent()
		member := &ast.MemberExpr{Pos: pos, Object: left, Field: field}

		// Method call: user.filter { ... }
		if p.check(token.LParen) {
			return p.parseCallArgs(pos, member)
		}
		// Lambda call: items.map { it.name } or items.map { x -> x.name }
		// Skip in condition context (noBraceLambda) to avoid consuming if/for block
		if p.check(token.LBrace) && !p.noBraceLambda {
			return &ast.CallExpr{
				Pos:  pos,
				Func: member,
				Args: []*ast.NamedArg{{Value: p.parseLambdaExpr()}},
			}
		}
		return member

	// Safe member access: user?.name
	case p.check(token.SafeDot):
		p.advance()
		field := p.expectIdent()
		return &ast.MemberExpr{Pos: pos, Object: left, Field: field, SafeCall: true}

	// Postfix ? operator: riskyOperation(id)?
	case p.check(token.Question):
		p.advance()
		return &ast.UnaryExpr{Pos: pos, Op: "?", Value: left}

	// Function call: find(User, id: 1)
	case p.check(token.LParen):
		return p.parseCallArgs(pos, left)

	// Trailing lambda: items.filter { it.active } or { x -> x.active }
	case p.check(token.LBrace) && p.isCallable(left):
		return &ast.CallExpr{
			Pos:  pos,
			Func: left,
			Args: []*ast.NamedArg{{Value: p.parseLambdaExpr()}},
		}

	// Elvis: expr ?: fallback
	case p.check(token.Elvis):
		p.advance()
		return &ast.ElvisExpr{Pos: pos, Left: left, Right: p.parseExpr(precElvis)}

	// BangElvis: expr !: fallback
	case p.check(token.BangElvis):
		p.advance()
		return &ast.BangElvisExpr{Pos: pos, Left: left, Right: p.parseExpr(precElvis)}

	// Range: 1..10
	case p.check(token.DotDot):
		p.advance()
		return &ast.RangeExpr{Pos: pos, Start: left, End: p.parseExpr(precRange)}

	// Channel send: ch <- 42
	case p.check(token.ChanSend):
		op := p.current().Val
		prec := p.currentPrec()
		p.advance()
		return &ast.BinaryExpr{Pos: pos, Left: left, Op: op, Right: p.parseExpr(prec)}

	// Binary operators
	case p.isBinaryOp():
		op := p.current().Val
		prec := p.currentPrec()
		p.advance()
		return &ast.BinaryExpr{Pos: pos, Left: left, Op: op, Right: p.parseExpr(prec)}

	default:
		return left
	}
}

func (p *Parser) parseCallArgs(pos token.Position, fn ast.Expr) *ast.CallExpr {
	p.expect(token.LParen)
	call := &ast.CallExpr{Pos: pos, Func: fn}

	for !p.check(token.RParen) && !p.isEOF() {
		arg := &ast.NamedArg{}
		// named arg: key: value
		if p.check(token.Ident) && p.peekType() == token.Colon {
			arg.Name = p.expectIdent()
			p.expect(token.Colon)
			// incomplete: `where: )` — no expression after colon
			if p.check(token.RParen) {
				break
			}
		}
		val := p.parseExpr(precNone)
		if val == nil {
			break
		}
		arg.Value = val
		call.Args = append(call.Args, arg)
		p.match(token.Comma)
	}
	p.expect(token.RParen)

	// trailing lambda: find(User, id: 1) { ... } or { x -> ... }
	// but NOT inside if/for conditions (noBraceLambda flag)
	if p.check(token.LBrace) && !p.noBraceLambda {
		call.Args = append(call.Args, &ast.NamedArg{
			Value: p.parseLambdaExpr(),
		})
	}

	return call
}

func (p *Parser) parseListExpr(pos token.Position) ast.Expr {
	p.expect(token.LBracket)
	list := &ast.ListExpr{Pos: pos}
	for !p.check(token.RBracket) && !p.isEOF() {
		list.Items = append(list.Items, p.parseExpr(precNone))
		p.match(token.Comma)
	}
	p.expect(token.RBracket)
	return list
}

func (p *Parser) parseWhenExpr(pos token.Position) ast.Expr {
	p.expect(token.When)
	when := &ast.WhenExpr{Pos: pos}

	// when(subject) { ... } or when subject { ... } or when { ... }
	if p.match(token.LParen) {
		when.Subject = p.parseExpr(precNone)
		p.expect(token.RParen)
	} else if !p.check(token.LBrace) {
		// when subject { ... } — parse subject expression stopping before {
		when.Subject = p.parseConditionExpr()
	}

	p.expect(token.LBrace)
	for !p.check(token.RBrace) && !p.isEOF() {
		// else -> ...
		if p.match(token.Else) {
			p.expect(token.Arrow)
			when.Else = p.parseWhenBody()
			continue
		}

		branch := &ast.WhenBranch{}

		// is Type -> ...
		if p.match(token.Is) {
			branch.IsType = p.expectIdent()
		} else if p.match(token.In) {
			// in range -> ...
			branch.Condition = &ast.BinaryExpr{
				Pos:   p.current().Pos,
				Op:    "in",
				Right: p.parseWhenCondition(),
			}
		} else {
			branch.Condition = p.parseWhenCondition()
		}

		p.expect(token.Arrow)
		branch.Body = p.parseWhenBody()
		when.Branches = append(when.Branches, branch)
	}
	p.expect(token.RBrace)
	return when
}

func (p *Parser) parseWhenBody() ast.Expr {
	if p.check(token.LBrace) {
		return p.parseLambdaExpr()
	}
	return p.parseWhenCondition() // stop before next branch keyword (in/is/else)
}

// parseWhenCondition parses an expression that stops before '->'.
func (p *Parser) parseWhenCondition() ast.Expr {
	left := p.parsePrefixExpr()
	if left == nil {
		return nil
	}
	for !p.check(token.Arrow) && !p.check(token.LBrace) && !p.check(token.RBrace) &&
		!p.check(token.Else) && !p.check(token.In) && !p.check(token.Is) &&
		!p.isEOF() && p.currentPrec() > precNone {
		left = p.parseInfixExpr(left)
	}
	return left
}

func hasDirectiveBody(name string) bool {
	switch name {
	case "transform", "beforeSave", "visible", "apply":
		return true
	}
	return false
}

// parseConditionExpr parses an expression that stops before '{'.
// Used in if/for conditions where '{' starts the block, not a lambda.
// parseForCondition parses the condition/collection for a for loop.
// Unlike parseExpr, it never treats { as trailing lambda — { starts the for body.
func (p *Parser) parseForCondition() ast.Expr {
	left := p.parsePrefixExpr()
	if left == nil {
		return nil
	}
	for !p.check(token.LBrace) && !p.isEOF() {
		// only process infix operators that aren't {
		switch {
		case p.check(token.Dot):
			p.advance()
			field := p.expectIdent()
			left = &ast.MemberExpr{Pos: left.GetPos(), Object: left, Field: field}
		case p.check(token.SafeDot):
			p.advance()
			field := p.expectIdent()
			left = &ast.MemberExpr{Pos: left.GetPos(), Object: left, Field: field, SafeCall: true}
		case p.check(token.LParen):
			left = p.parseCallArgs(left.GetPos(), left)
		case p.isBinaryOp():
			pos := p.current().Pos
			op := p.current().Val
			p.advance()
			right := p.parseForCondition() // recurse without { being eaten
			left = &ast.BinaryExpr{Pos: pos, Left: left, Op: op, Right: right}
		case p.check(token.DotDot):
			pos := p.current().Pos
			p.advance()
			right := p.parseForCondition()
			left = &ast.RangeExpr{Pos: pos, Start: left, End: right}
		case p.check(token.Elvis):
			pos := p.current().Pos
			p.advance()
			right := p.parseForCondition()
			left = &ast.ElvisExpr{Pos: pos, Left: left, Right: right}
		default:
			return left
		}
	}
	return left
}

func (p *Parser) parseConditionExpr() ast.Expr {
	old := p.noBraceLambda
	p.noBraceLambda = true
	defer func() { p.noBraceLambda = old }()

	left := p.parsePrefixExpr()
	if left == nil {
		return nil
	}
	for p.currentPrec() > precNone && !p.check(token.LBrace) {
		left = p.parseInfixExpr(left)
	}
	return left
}
