package parser

import (
	"fmt"
	"strconv"

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

	// when(subject)
	if p.match(token.LParen) {
		when.Subject = p.parseExpr(precNone)
		p.expect(token.RParen)
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
		startPos := p.pos
		left = p.parseInfixExpr(left)
		if p.pos == startPos {
			break // guard against infinite loop
		}
	}
	return left
}

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
	obj := &ast.ObjectExpr{Pos: pos}
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
