package parser

import (
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== Block ==========

// parseLambdaExpr parses a lambda expression: { body } or { params -> body }.
// The opening { must be the current token.
func (p *Parser) parseLambdaExpr() *ast.LambdaExpr {
	pos := p.current().Pos
	p.expect(token.LBrace)
	params := p.tryParseLambdaParams()
	block := &ast.Block{Pos: pos}
	for !p.check(token.RBrace) && !p.isEOF() {
		stmt := p.parseStmt()
		if stmt != nil {
			block.Stmts = append(block.Stmts, stmt)
		}
	}
	block.EndPos = p.current().Pos
	p.expect(token.RBrace)
	return &ast.LambdaExpr{Pos: pos, Params: params, Body: block}
}

func (p *Parser) parseBlock() *ast.Block {
	pos := p.current().Pos
	p.expect(token.LBrace)
	block := &ast.Block{Pos: pos}

	for !p.check(token.RBrace) && !p.isEOF() {
		stmt := p.parseStmt()
		if stmt != nil {
			block.Stmts = append(block.Stmts, stmt)
		}
	}
	block.EndPos = p.current().Pos
	p.expect(token.RBrace)
	return block
}

// ========== Statements ==========

func (p *Parser) parseStmt() ast.Stmt {
	switch {
	case p.check(token.Val), p.check(token.Var):
		return p.parseValStmt()
	case p.check(token.If):
		return p.parseIfStmt()
	case p.check(token.For):
		return p.parseForStmt()
	case p.check(token.Return):
		return p.parseReturnStmt()
	case p.check(token.Throw):
		return p.parseThrowStmt()
	case p.check(token.Break):
		pos := p.current().Pos
		p.advance()
		return &ast.BreakStmt{Pos: pos}
	case p.check(token.Continue):
		pos := p.current().Pos
		p.advance()
		return &ast.ContinueStmt{Pos: pos}
	case p.check(token.Emit):
		return p.parseEmitStmt()
	case p.check(token.Ident) && p.isAssignOp(p.peekType()):
		return p.parseAssignStmt()
	case p.check(token.Ident) && p.peekType() == token.Dot:
		// could be member assignment: user.name = "value"
		// or regular expression: user.name (member access)
		return p.parseExprOrAssignStmt()
	case p.check(token.Comment), p.check(token.DocComment):
		p.advance()
		return nil
	default:
		return p.parseExprStmt()
	}
}

func (p *Parser) isAssignOp(t token.Type) bool {
	switch t {
	case token.Assign, token.PlusAssign, token.MinusAssign,
		token.StarAssign, token.SlashAssign, token.PercentAssign:
		return true
	}
	return false
}

func (p *Parser) parseAssignStmt() *ast.AssignStmt {
	pos := p.current().Pos
	target := &ast.Ident{Pos: pos, Name: p.advance().Val}
	op := p.advance().Val
	value := p.parseExpr(precNone)
	return &ast.AssignStmt{
		Pos:    pos,
		Target: target,
		Op:     op,
		Value:  value,
	}
}

// parseExprOrAssignStmt parses either a member assignment (user.name = value)
// or a regular expression statement (user.name).
func (p *Parser) parseExprOrAssignStmt() ast.Stmt {
	pos := p.current().Pos
	// parse the left side as an expression (handles member access chains)
	expr := p.parseExpr(precNone)

	// check if followed by assign op
	if p.isAssignOp(p.current().Type) {
		op := p.advance().Val
		value := p.parseExpr(precNone)
		return &ast.AssignStmt{
			Pos:    pos,
			Target: expr,
			Op:     op,
			Value:  value,
		}
	}

	return &ast.ExprStmt{Pos: pos, Expr: expr}
}

func (p *Parser) parseValStmt() *ast.ValStmt {
	pos := p.current().Pos
	mutable := p.check(token.Var)
	if mutable {
		p.expect(token.Var)
	} else {
		p.expect(token.Val)
	}

	stmt := &ast.ValStmt{Pos: pos, Mutable: mutable}

	// destructuring: val (x, y) = ...
	if p.check(token.LParen) {
		p.advance()
		for !p.check(token.RParen) && !p.isEOF() {
			stmt.Names = append(stmt.Names, p.expectIdent())
			p.match(token.Comma)
		}
		p.expect(token.RParen)
	} else {
		stmt.NamePos = p.current().Pos
		stmt.Name = p.expectIdent()
	}

	// optional type: val x: Int = ...
	if p.match(token.Colon) {
		stmt.Type = p.parseTypeRef()
	}

	p.expect(token.Assign)
	stmt.Value = p.parseExpr(precNone)
	return stmt
}

func (p *Parser) parseIfStmt() *ast.IfStmt {
	pos := p.current().Pos
	p.expect(token.If)

	return &ast.IfStmt{
		Pos:       pos,
		Condition: p.parseConditionExpr(),
		Then:      p.parseBlock(),
	}
}

func (p *Parser) parseForStmt() *ast.ForStmt {
	pos := p.current().Pos
	p.expect(token.For)

	stmt := &ast.ForStmt{Pos: pos}

	// infinite loop: for { ... }
	if p.check(token.LBrace) {
		stmt.Body = p.parseBlock()
		return stmt
	}

	// Check if this is a "for x in ..." or "for condition { ... }"
	// If current is Ident and next is In, it's a range loop
	if p.check(token.Ident) && p.peekType() == token.In {
		stmt.VarName = p.expectIdent()
		p.expect(token.In)
		stmt.Collection = p.parseForCondition()
	} else {
		// conditional loop: for condition { ... }
		stmt.Collection = p.parseForCondition()
	}

	stmt.Body = p.parseBlock()
	return stmt
}

func (p *Parser) parseReturnStmt() *ast.ReturnStmt {
	pos := p.current().Pos
	p.expect(token.Return)
	return &ast.ReturnStmt{
		Pos:   pos,
		Value: p.parseExpr(precNone),
	}
}

func (p *Parser) parseThrowStmt() *ast.ThrowStmt {
	pos := p.current().Pos
	p.expect(token.Throw)
	return &ast.ThrowStmt{
		Pos:   pos,
		Error: p.parseExpr(precNone),
	}
}

func (p *Parser) parseExprStmt() *ast.ExprStmt {
	pos := p.current().Pos
	startPos := p.pos
	expr := p.parseExpr(precNone)
	// prevent infinite loop if no tokens consumed
	if p.pos == startPos {
		p.error("unexpected token in statement: %s", p.current().Val)
		p.advance()
		return nil
	}
	return &ast.ExprStmt{Pos: pos, Expr: expr}
}
