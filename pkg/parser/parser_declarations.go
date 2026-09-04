package parser

import (
	"strconv"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== Top-level Parsers ==========

func (p *Parser) parseModel() *ast.ModelDecl {
	pos := p.current().Pos
	doc := p.takeDoc()
	p.expect(token.Model)

	model := &ast.ModelDecl{
		Pos:  pos,
		Doc:  doc,
		Name: p.expectIdent(),
	}

	// inheritance: : Base, Searchable
	if p.match(token.Colon) {
		for {
			model.Parents = append(model.Parents, p.expectIdent())
			if !p.match(token.Comma) {
				break
			}
		}
	}

	// model-level directives: @crud
	model.Directives = p.parseDirectives()

	// fields
	p.expect(token.LBrace)
	for !p.check(token.RBrace) && !p.isEOF() {
		p.consumeDoc()

		// skip regular comments inside model body
		if p.check(token.Comment) {
			p.advance()
			continue
		}

		// scope declaration
		if p.check(token.Ident) && p.current().Val == "scope" {
			model.Scopes = append(model.Scopes, p.parseScope())
			continue
		}

		// computed field: val postCount: Int get { ... }
		if p.check(token.Val) {
			model.Fields = append(model.Fields, p.parseComputedField())
			continue
		}

		// @unique([field1, field2]) - model-level directive
		if p.check(token.At) {
			model.Directives = append(model.Directives, p.parseDirective())
			continue
		}

		model.Fields = append(model.Fields, p.parseField())
	}
	p.expect(token.RBrace)
	return model
}

func (p *Parser) parseInterface() *ast.InterfaceDecl {
	pos := p.current().Pos
	doc := p.takeDoc()
	p.expect(token.Interface)

	iface := &ast.InterfaceDecl{
		Pos:  pos,
		Doc:  doc,
		Name: p.expectIdent(),
	}

	p.expect(token.LBrace)
	for !p.check(token.RBrace) && !p.isEOF() {
		p.consumeDoc()
		if p.check(token.Fn) {
			iface.Methods = append(iface.Methods, p.parseFn())
		} else if p.check(token.Val) {
			iface.Fields = append(iface.Fields, p.parseComputedField())
		} else {
			iface.Fields = append(iface.Fields, p.parseField())
		}
	}
	p.expect(token.RBrace)
	return iface
}

func (p *Parser) parseEnum() *ast.EnumDecl {
	pos := p.current().Pos
	doc := p.takeDoc()
	p.expect(token.Enum)

	enum := &ast.EnumDecl{
		Pos:  pos,
		Doc:  doc,
		Name: p.expectIdent(),
	}

	p.expect(token.LBrace)
	for !p.check(token.RBrace) && !p.isEOF() {
		enum.Values = append(enum.Values, p.expectIdent())
	}
	p.expect(token.RBrace)
	return enum
}

func (p *Parser) parseSealed() *ast.SealedDecl {
	pos := p.current().Pos
	doc := p.takeDoc()
	p.expect(token.Sealed)

	sealed := &ast.SealedDecl{
		Pos:  pos,
		Doc:  doc,
		Name: p.expectIdent(),
	}

	p.expect(token.LBrace)
	for !p.check(token.RBrace) && !p.isEOF() {
		variant := &ast.SealedVariant{
			Name: p.expectIdent(),
		}
		if p.match(token.LParen) {
			for !p.check(token.RParen) && !p.isEOF() {
				variant.Fields = append(variant.Fields, p.parseParam())
				p.match(token.Comma)
			}
			p.expect(token.RParen)
		}
		sealed.Variants = append(sealed.Variants, variant)
	}
	p.expect(token.RBrace)
	return sealed
}

func (p *Parser) parseType() *ast.TypeDecl {
	pos := p.current().Pos
	doc := p.takeDoc()
	p.expect(token.KwType)

	td := &ast.TypeDecl{
		Pos:  pos,
		Doc:  doc,
		Name: p.expectIdent(),
	}

	// generic params: <T>
	if p.match(token.Lt) {
		for {
			td.TypeParams = append(td.TypeParams, p.expectIdent())
			if !p.match(token.Comma) {
				break
			}
		}
		p.expect(token.Gt)
	}

	// fields
	p.expect(token.LBrace)
	for !p.check(token.RBrace) && !p.isEOF() {
		td.Fields = append(td.Fields, p.parseField())
	}
	p.expect(token.RBrace)
	return td
}

func (p *Parser) parseOverrideAPI() *ast.ApiDecl {
	p.advance() // consume 'override'
	if !p.check(token.Api) {
		p.error("expected 'api' after 'override', got %s", p.current().Val)
		return &ast.ApiDecl{}
	}
	api := p.parseAPI()
	api.Override = true
	return api
}

func (p *Parser) parseAPI() *ast.ApiDecl {
	pos := p.current().Pos
	doc := p.takeDoc()
	p.expect(token.Api)

	api := &ast.ApiDecl{
		Pos:  pos,
		Doc:  doc,
		Name: p.expectIdent(),
	}

	// params (optional — inferred APIs can omit)
	if p.check(token.LParen) {
		p.advance()
		for !p.check(token.RParen) && !p.isEOF() {
			api.Params = append(api.Params, p.parseParam())
			p.match(token.Comma)
		}
		p.expect(token.RParen)
	}

	// return type (optional — inferred APIs can omit)
	if p.match(token.Colon) {
		api.ReturnType = p.parseTypeRef()
	}

	// directives
	api.Directives = p.parseDirectives()

	// body (optional)
	if p.check(token.LBrace) {
		api.Body = p.parseBlock()
	}

	return api
}

func (p *Parser) parseFn() *ast.FnDecl {
	pos := p.current().Pos
	doc := p.takeDoc()
	p.expect(token.Fn)

	fn := &ast.FnDecl{
		Pos:  pos,
		Doc:  doc,
		Name: p.expectIdent(),
	}

	p.expect(token.LParen)
	for !p.check(token.RParen) && !p.isEOF() {
		fn.Params = append(fn.Params, p.parseParam())
		p.match(token.Comma)
	}
	p.expect(token.RParen)

	if p.match(token.Colon) {
		fn.ReturnType = p.parseTypeRef()
	}

	fn.Directives = p.parseDirectives()

	if p.check(token.LBrace) {
		fn.Body = p.parseBlock()
	}

	return fn
}

func (p *Parser) parseError() *ast.ErrorDecl {
	pos := p.current().Pos
	doc := p.takeDoc()
	p.expect(token.Error)

	errDecl := &ast.ErrorDecl{
		Pos:  pos,
		Doc:  doc,
		Name: p.expectIdent(),
	}

	// params
	if p.match(token.LParen) {
		for !p.check(token.RParen) && !p.isEOF() {
			errDecl.Fields = append(errDecl.Fields, p.parseParam())
			p.match(token.Comma)
		}
		p.expect(token.RParen)
	}

	// body: { code: 404, message: error.not_found, internal: true }
	p.expect(token.LBrace)
	for !p.check(token.RBrace) && !p.isEOF() {
		key := p.expectIdent()
		p.expect(token.Colon)
		switch key {
		case "code":
			code, _ := strconv.Atoi(p.expect(token.Int).Val)
			errDecl.Code = code
		case "message":
			if p.check(token.String) {
				errDecl.Message = p.advance().Val
			} else {
				errDecl.Message = p.parseQualifiedName()
			}
		case "internal":
			errDecl.Internal = p.current().Type == token.True
			p.advance()
		}
	}
	p.expect(token.RBrace)
	return errDecl
}

func (p *Parser) parseExtend() *ast.ExtendDecl {
	pos := p.current().Pos
	p.expect(token.Extend)

	ext := &ast.ExtendDecl{
		Pos:  pos,
		Name: p.expectIdent(),
	}

	p.expect(token.LBrace)
	for !p.check(token.RBrace) && !p.isEOF() {
		ext.Fields = append(ext.Fields, p.parseField())
	}
	p.expect(token.RBrace)
	return ext
}

// parseUseDispatch handles the unified `use` keyword:
//   - `use http`           → ImportDecl (stdlib import)
//   - `use common.{ ... }` → UseDecl   (module import)
func (p *Parser) parseUseDispatch(file *ast.File) {
	pos := p.current().Pos
	p.expect(token.Use)

	name := p.expectIdentOrKeyword()

	// If next token is '.', this is a dotted module import: use common.{ Base }
	if p.check(token.Dot) {
		use := &ast.UseDecl{Pos: pos, Module: name}
		p.expect(token.Dot)
		p.expect(token.LBrace)
		for !p.check(token.RBrace) && !p.isEOF() {
			use.Names = append(use.Names, p.expectIdent())
			p.match(token.Comma)
		}
		p.expect(token.RBrace)
		file.Uses = append(file.Uses, use)
		return
	}

	// If next token is '{', this is a destructured import: use model { Base }
	if p.check(token.LBrace) {
		use := &ast.UseDecl{Pos: pos, Module: name}
		p.expect(token.LBrace)
		for !p.check(token.RBrace) && !p.isEOF() {
			use.Names = append(use.Names, p.expectIdent())
			p.match(token.Comma)
		}
		p.expect(token.RBrace)
		file.Uses = append(file.Uses, use)
		return
	}

	// Otherwise it's a stdlib import: use http
	file.Imports = append(file.Imports, &ast.ImportDecl{
		Pos:    pos,
		Module: name,
	})
}

func (p *Parser) parseMiddleware() *ast.MiddlewareDecl {
	pos := p.current().Pos
	doc := p.takeDoc()
	p.expect(token.Middleware)

	mw := &ast.MiddlewareDecl{
		Pos:  pos,
		Doc:  doc,
		Name: p.expectIdent(),
	}

	mw.Directives = p.parseDirectives()

	if p.check(token.LBrace) {
		mw.Body = p.parseBlock()
	}

	return mw
}

// parseEmitStmt: emit PostCreated(post: post, userId: my.id)
func (p *Parser) parseEmitStmt() *ast.EmitStmt {
	pos := p.current().Pos
	p.expect(token.Emit)
	stmt := &ast.EmitStmt{
		Pos:       pos,
		EventName: p.expectIdent(),
	}
	if p.match(token.LParen) {
		for !p.check(token.RParen) && !p.isEOF() {
			arg := &ast.NamedArg{}
			name := p.expectIdent()
			p.expect(token.Colon)
			arg.Name = name
			val := p.parseExpr(precNone)
			if val == nil {
				// incomplete expression — skip this arg
				p.match(token.Comma)
				continue
			}
			arg.Value = val
			stmt.Args = append(stmt.Args, arg)
			p.match(token.Comma)
		}
		p.expect(token.RParen)
	}
	return stmt
}

// parseEvent: event PostCreated(post: Post, userId: Int)
func (p *Parser) parseEvent() *ast.EventDecl {
	pos := p.current().Pos
	p.expect(token.Event)
	event := &ast.EventDecl{
		Pos:  pos,
		Name: p.expectIdent(),
	}
	if p.match(token.LParen) {
		for !p.check(token.RParen) && !p.isEOF() {
			event.Params = append(event.Params, p.parseParam())
			p.match(token.Comma)
		}
		p.expect(token.RParen)
	}
	return event
}

// parseOn: on PostCreated { ... } or on PostCreated @native
// Also supports lambda-style params: on PostCreated { user -> ... }
func (p *Parser) parseOn() *ast.OnDecl {
	pos := p.current().Pos
	p.expect(token.On)
	on := &ast.OnDecl{
		Pos:       pos,
		EventName: p.expectIdent(),
	}
	if p.check(token.At) {
		dirs := p.parseDirectives()
		for _, d := range dirs {
			switch d.Name {
			case "native":
				on.Native = true
			case "broadcast":
				on.Broadcast = true
			}
		}
	}
	if p.check(token.LBrace) {
		on.Params, on.Body = p.parseOnBlock()
	}
	return on
}

// parseOnBlock parses { params -> stmts } or { stmts }.
// It detects the lambda-style param list by scanning for ident (,ident)* -> pattern.
func (p *Parser) parseOnBlock() ([]string, *ast.Block) {
	pos := p.current().Pos
	p.expect(token.LBrace)
	block := &ast.Block{Pos: pos}

	// detect lambda-style params: ident (,ident)* ->
	params := p.tryParseLambdaParams()

	for !p.check(token.RBrace) && !p.isEOF() {
		stmt := p.parseStmt()
		if stmt != nil {
			block.Stmts = append(block.Stmts, stmt)
		}
	}
	block.EndPos = p.current().Pos
	p.expect(token.RBrace)
	return params, block
}

// tryParseLambdaParams attempts to parse ident (,ident)* -> at the current position.
// Returns the param names if the pattern matches, nil otherwise.
func (p *Parser) tryParseLambdaParams() []string {
	// scan ahead to check for ident (,ident)* -> pattern without consuming tokens
	save := p.pos
	if !p.check(token.Ident) {
		return nil
	}
	var names []string
	names = append(names, p.current().Val)
	p.advance()
	for p.check(token.Comma) {
		p.advance() // skip comma
		if !p.check(token.Ident) {
			p.pos = save
			return nil
		}
		names = append(names, p.current().Val)
		p.advance()
	}
	if !p.check(token.Arrow) {
		p.pos = save
		return nil
	}
	p.advance() // skip ->
	return names
}

func (p *Parser) parseScope() *ast.ScopeDecl {
	pos := p.current().Pos
	p.advance() // skip "scope" (it's an ident, not keyword)

	scope := &ast.ScopeDecl{
		Pos:  pos,
		Name: p.expectIdent(),
	}

	// Optional parameters: scope recent(days: Int) { ... }
	if p.match(token.LParen) {
		for !p.check(token.RParen) && !p.isEOF() {
			scope.Params = append(scope.Params, p.parseParam())
			p.match(token.Comma)
		}
		p.expect(token.RParen)
	}

	// Parse scope expression: = expr
	p.expect(token.Assign)
	scope.Expr = p.parseExpr(precNone)
	return scope
}

// ========== Fields & Params ==========

func (p *Parser) parseField() *ast.FieldDecl {
	doc := p.takeDoc()
	pos := p.current().Pos

	field := &ast.FieldDecl{
		Pos:  pos,
		Doc:  doc,
		Name: p.expectIdent(),
	}

	p.expect(token.Colon)
	field.Type = p.parseTypeRef()

	// default value: = "default"
	if p.match(token.Assign) {
		field.Default = p.parseExpr(precNone)
	}

	// directives
	field.Directives = p.parseDirectives()

	return field
}

func (p *Parser) parseComputedField() *ast.FieldDecl {
	doc := p.takeDoc()
	pos := p.current().Pos
	p.expect(token.Val)

	field := &ast.FieldDecl{
		Pos:  pos,
		Doc:  doc,
		Name: p.expectIdent(),
	}

	p.expect(token.Colon)
	field.Type = p.parseTypeRef()

	// expect "get"
	if p.check(token.Ident) && p.current().Val == "get" {
		p.advance()
		computed := &ast.ComputedField{}

		// @native, @count, @avg, etc.
		computed.Directives = p.parseDirectives()

		// body { ... }
		if p.check(token.LBrace) {
			computed.Body = p.parseBlock()
		}

		field.Computed = computed
	}

	return field
}

func (p *Parser) parseParam() *ast.ParamDecl {
	p.consumeDoc()
	param := &ast.ParamDecl{Pos: p.current().Pos, Doc: p.takeDoc()}
	if p.match(token.DotDotDot) {
		param.Spread = true
	}
	param.Name = p.expectIdent()
	p.expect(token.Colon)
	param.Type = p.parseTypeRef()

	if p.match(token.Assign) {
		param.Default = p.parseExpr(precNone)
	}
	return param
}

// ========== Type References ==========

func (p *Parser) parseTypeRef() *ast.TypeRef {
	pos := p.current().Pos

	// Tuple type: (Post, Video, Product)
	if p.check(token.LParen) {
		return p.parseTupleType(pos)
	}

	// List type: [Post], [Role] through UserRole
	if p.match(token.LBracket) {
		inner := p.parseTypeRef()
		p.expect(token.RBracket)
		ref := &ast.TypeRef{
			Pos:      pos,
			IsList:   true,
			Name:     inner.Name,
			TypeArgs: inner.TypeArgs,
		}
		// Nullable: [Post]?
		if p.match(token.Question) {
			ref.Nullable = true
		}
		return ref
	}

	ref := &ast.TypeRef{
		Pos:  pos,
		Name: p.expectIdent(),
	}

	// Generic args: Page<User>
	if p.match(token.Lt) {
		for {
			ref.TypeArgs = append(ref.TypeArgs, p.parseTypeRef())
			if !p.match(token.Comma) {
				break
			}
		}
		p.expect(token.Gt)
	}

	// Custom FK: User(key: authorId)
	if p.match(token.LParen) {
		if p.check(token.Ident) && p.current().Val == "key" {
			p.advance()
			p.expect(token.Colon)
			ref.FKField = p.expectIdent()
		}
		p.expect(token.RParen)
	}

	// Nullable: String?
	if p.match(token.Question) {
		ref.Nullable = true
	}

	return ref
}

func (p *Parser) parseTupleType(pos token.Position) *ast.TypeRef {
	p.expect(token.LParen)
	ref := &ast.TypeRef{Pos: pos}
	for {
		ref.Tuple = append(ref.Tuple, p.parseTypeRef())
		if !p.match(token.Comma) {
			break
		}
	}
	p.expect(token.RParen)
	return ref
}

// ========== Directives ==========

func (p *Parser) parseDirectives() []*ast.Directive {
	var directives []*ast.Directive
	for p.check(token.At) {
		directives = append(directives, p.parseDirective())
	}
	return directives
}

func (p *Parser) parseDirective() *ast.Directive {
	pos := p.current().Pos
	p.expect(token.At)

	dir := &ast.Directive{
		Pos:  pos,
		Name: p.expectIdent(),
	}

	// args: (ttl: 60, max: 100)
	if p.match(token.LParen) {
		for !p.check(token.RParen) && !p.isEOF() {
			arg := &ast.NamedArg{}
			// check if named: key: value
			if p.check(token.Ident) && p.peekType() == token.Colon {
				arg.Name = p.expectIdent()
				p.expect(token.Colon)
			}
			// lambda body for permission: { expr }
			if p.check(token.LBrace) {
				arg.Value = p.parseLambdaExpr()
			} else {
				arg.Value = p.parseExpr(precNone)
			}
			dir.Args = append(dir.Args, arg)
			p.match(token.Comma)
		}
		p.expect(token.RParen)
	}

	// body: @transform { ... }, @beforeSave { ... }, @visible(expr) { ... }
	// only specific directives have body blocks
	if p.check(token.LBrace) && hasDirectiveBody(dir.Name) {
		dir.Body = p.parseBlock()
	}

	return dir
}
