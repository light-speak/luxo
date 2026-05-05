package ast

import "github.com/light-speak/luxo/pkg/token"

// File represents a parsed .luxo file.
type File struct {
	Name        string
	Models      []*ModelDecl
	Interfaces  []*InterfaceDecl
	Enums       []*EnumDecl
	Sealeds     []*SealedDecl
	Types       []*TypeDecl
	APIs        []*ApiDecl
	Functions   []*FnDecl
	Errors      []*ErrorDecl
	Extends     []*ExtendDecl
	Uses        []*UseDecl
	Middlewares []*MiddlewareDecl
	Events      []*EventDecl
	Listeners   []*OnDecl
	Imports     []*ImportDecl
	Globals     []*ValStmt // top-level val/var declarations
}

// ImportDecl: use http (stdlib import)
type ImportDecl struct {
	Pos    token.Position
	Module string
}

// ========== Top-level Declarations ==========

// ModelDecl: model User : Base, Searchable { ... }
type ModelDecl struct {
	Pos        token.Position
	Doc        string
	Name       string
	Parents    []string // inheritance: ["Base", "Searchable"]
	Fields     []*FieldDecl
	Scopes     []*ScopeDecl
	Directives []*Directive
}

// InterfaceDecl: interface Searchable { ... }
type InterfaceDecl struct {
	Pos     token.Position
	Doc     string
	Name    string
	Fields  []*FieldDecl
	Methods []*FnDecl
}

// EnumDecl: enum Role { USER, ADMIN }
type EnumDecl struct {
	Pos    token.Position
	Doc    string
	Name   string
	Values []string
}

// SealedDecl: sealed PayResult { Success(...), Failed(...) }
type SealedDecl struct {
	Pos      token.Position
	Doc      string
	Name     string
	Variants []*SealedVariant
}

type SealedVariant struct {
	Name   string
	Fields []*ParamDecl
}

// TypeDecl: type Page<T> { ... }
type TypeDecl struct {
	Pos        token.Position
	Doc        string
	Name       string
	TypeParams []string // generic params: <T>
	Fields     []*FieldDecl
}

// ApiDecl: api getUser(id: Int): User @auth { ... }
type ApiDecl struct {
	Pos        token.Position
	Doc        string
	Override   bool // override api ...
	Name       string
	Params     []*ParamDecl
	ReturnType *TypeRef
	Directives []*Directive
	Body       *Block // nil = auto-generate or @native
}

// FnDecl: fn encrypt(value: String): String @native
type FnDecl struct {
	Pos        token.Position
	Doc        string
	Name       string
	Params     []*ParamDecl
	ReturnType *TypeRef
	Directives []*Directive
	Body       *Block // nil = @native
}

// ErrorDecl: error NotFound(resource: String, id: Int) { code: 404, ... }
type ErrorDecl struct {
	Pos      token.Position
	Doc      string
	Name     string
	Fields   []*ParamDecl
	Code     int
	Message  string
	Internal bool
}

// ExtendDecl: extend User { posts: [Post] }
type ExtendDecl struct {
	Pos    token.Position
	Name   string
	Fields []*FieldDecl
}

// UseDecl: use common.{ Base, Searchable }
type UseDecl struct {
	Pos    token.Position
	Module string
	Names  []string
}

// MiddlewareDecl: middleware requestLogger { ... }
type MiddlewareDecl struct {
	Pos        token.Position
	Doc        string
	Name       string
	Directives []*Directive
	Body       *Block
}

// EventDecl: event UserCreated(user: User)
type EventDecl struct {
	Pos    token.Position
	Name   string
	Params []*ParamDecl
}

// OnDecl: on UserCreated { ... }
type OnDecl struct {
	Pos       token.Position
	EventName string
	Params    []string // lambda-style params: on Event { user -> ... }
	Body      *Block   // nil if @native
	Native    bool
	Broadcast bool // @broadcast — all instances receive (default: queue per module)
}

// ScopeDecl: scope published = where(status == PostStatus.PUBLISHED)
// ScopeDecl: scope recent(days: Int) = where(createdAt > now() - days.days)
type ScopeDecl struct {
	Pos    token.Position
	Name   string
	Params []*ParamDecl // optional parameters
	Expr   Expr         // where(...).orderBy(...) chain expression
}

// ========== Fields & Params ==========

// FieldDecl: name: String @varchar(100) = "default"
type FieldDecl struct {
	Pos        token.Position
	Doc        string
	Name       string
	Type       *TypeRef
	Default    Expr
	Directives []*Directive
	Computed   *ComputedField // val get { ... } or val get @native
}

// ComputedField: val postCount: Int get { posts.size } or val creditScore: Int get @native
type ComputedField struct {
	Body       *Block       // nil = @native or @aggregate
	Directives []*Directive // @native, @count, @sum, @avg, etc.
}

// ParamDecl: id: Int = 0
type ParamDecl struct {
	Pos     token.Position
	Doc     string
	Name    string
	Type    *TypeRef
	Default Expr
	Spread  bool // ...messages: String
}

// TypeRef: String, String?, [Post], Page<User>, (Post, Video, Product)
type TypeRef struct {
	Pos      token.Position
	Name     string     // type name, empty for tuple types
	Nullable bool       // ?
	IsList   bool       // [xxx]
	TypeArgs []*TypeRef // generic args: Page<User>
	Tuple    []*TypeRef // polymorphic: (Post, Video, Product)
	FKField  string     // custom foreign key: User(key: authorId)
}

// Directive: @cache(ttl: 60), @hidden, @transform { ... }
type Directive struct {
	Pos  token.Position
	Name string
	Args []*NamedArg
	Body *Block // @transform { ... }, @beforeSave { ... }
}

// ========== Expressions ==========

type Expr interface {
	exprNode()
	GetPos() token.Position
}

// Literal: 42, 3.14, "hello", true, false, null, 7d
type Literal struct {
	Pos   token.Position
	Kind  token.Type // Int, Float, String, Duration, True, False, Null
	Value string
}

// Ident: user, currentUser, Role.ADMIN
type Ident struct {
	Pos  token.Position
	Name string
}

// MemberExpr: user.name, user?.address?.city
type MemberExpr struct {
	Pos      token.Position
	Object   Expr
	Field    string
	SafeCall bool // ?.
}

// CallExpr: find(User, id: 1), encrypt(password)
type CallExpr struct {
	Pos  token.Position
	Func Expr
	Args []*NamedArg
}

// NamedArg: name: value or positional value
type NamedArg struct {
	Name  string // empty = positional
	Value Expr
}

// BinaryExpr: a + b, a == b, a && b, a in 1..10
type BinaryExpr struct {
	Pos   token.Position
	Left  Expr
	Op    string
	Right Expr
}

// UnaryExpr: !a, -b
type UnaryExpr struct {
	Pos   token.Position
	Op    string
	Value Expr
}

// ElvisExpr: expr ?: fallback
type ElvisExpr struct {
	Pos   token.Position
	Left  Expr
	Right Expr
}

// BangElvisExpr: expr !: fallback
type BangElvisExpr struct {
	Pos   token.Position
	Left  Expr
	Right Expr
}

// WhenExpr: when(x) { in 90..100 -> "A", else -> "D" }
type WhenExpr struct {
	Pos      token.Position
	Subject  Expr // when(x), nil for when { }
	Branches []*WhenBranch
	Else     Expr
}

type WhenBranch struct {
	Condition Expr
	IsType    string // is User, is Post
	Body      Expr
}

// LambdaExpr: { it.name } or { x -> x.name } or { a, b -> a + b }
type LambdaExpr struct {
	Pos    token.Position
	Params []string // named params: { x -> ... }, nil = use implicit 'it'
	Body   *Block
}

// ListExpr: [1, 2, 3]
type ListExpr struct {
	Pos   token.Position
	Items []Expr
}

// ObjectExpr: { name: "lin", age: 18 }
type ObjectExpr struct {
	Pos    token.Position
	Fields []*NamedArg
}

// TemplateString: "hello ${user.name}, age ${user.age}"
type TemplateString struct {
	Pos   token.Position
	Parts []Expr // alternating StringLiteral and expressions
}

// RangeExpr: 1..10
type RangeExpr struct {
	Pos   token.Position
	Start Expr
	End   Expr
}

// TransactionExpr: transaction { ... }
type TransactionExpr struct {
	Pos  token.Position
	Body *Block
}

// YieldExpr: yield expr
type YieldExpr struct {
	Pos   token.Position
	Value Expr
}

// AsyncExpr: async { ... }
type AsyncExpr struct {
	Pos  token.Position
	Body *Block
}

// AwaitExpr: await { ... }
type AwaitExpr struct {
	Pos  token.Position
	Body *Block
}

// ========== Statements ==========

type Stmt interface {
	stmtNode()
	GetPos() token.Position
}

// ValStmt: val x = expr (immutable), var x = expr (mutable)
type ValStmt struct {
	Pos     token.Position
	NamePos token.Position // position of the variable name identifier
	Name    string         // single name: val x = ...
	Names   []string       // destructure: val (a, b, c) = ...
	Type    *TypeRef       // optional, usually inferred
	Value   Expr
	Mutable bool // true for var, false for val
}

// IfStmt: if condition { ... } — single branch only, use when for multi-branch
type IfStmt struct {
	Pos       token.Position
	Condition Expr
	Then      *Block
}

// ForStmt: for item in collection { ... }
type ForStmt struct {
	Pos        token.Position
	VarName    string
	Collection Expr
	Body       *Block
}

// ReturnStmt: return expr
type ReturnStmt struct {
	Pos   token.Position
	Value Expr
}

// ThrowStmt: throw error.not_found
type ThrowStmt struct {
	Pos   token.Position
	Error Expr
}

// EmitStmt: emit UserCreated(user: currentUser)
type EmitStmt struct {
	Pos       token.Position
	EventName string
	Args      []*NamedArg
}

// AssignStmt: x = 2, x += 1
type AssignStmt struct {
	Pos    token.Position
	Target Expr   // variable being assigned
	Op     string // "=", "+=", "-=", "*=", "/=", "%="
	Value  Expr
	Atomic bool // set by semantic analysis: field += n on model → SQL SET field = field + n
}

// BreakStmt: break
type BreakStmt struct {
	Pos token.Position
}

// ContinueStmt: continue
type ContinueStmt struct {
	Pos token.Position
}

// ExprStmt: expression used as statement (function call, assignment, etc.)
type ExprStmt struct {
	Pos  token.Position
	Expr Expr
}

// Block: a sequence of statements
type Block struct {
	Pos    token.Position
	EndPos token.Position // position of closing }
	Stmts  []Stmt
}

// ========== Interface implementations ==========

func (n *Literal) exprNode()         {}
func (n *Ident) exprNode()           {}
func (n *MemberExpr) exprNode()      {}
func (n *CallExpr) exprNode()        {}
func (n *BinaryExpr) exprNode()      {}
func (n *UnaryExpr) exprNode()       {}
func (n *ElvisExpr) exprNode()       {}
func (n *BangElvisExpr) exprNode()   {}
func (n *WhenExpr) exprNode()        {}
func (n *LambdaExpr) exprNode()      {}
func (n *ListExpr) exprNode()        {}
func (n *ObjectExpr) exprNode()      {}
func (n *TemplateString) exprNode()  {}
func (n *RangeExpr) exprNode()       {}
func (n *TransactionExpr) exprNode() {}
func (n *YieldExpr) exprNode()       {}
func (n *AsyncExpr) exprNode()       {}
func (n *AwaitExpr) exprNode()       {}
func (n *ForStmt) exprNode()         {} // for can be used as expression

func (n *ValStmt) stmtNode()      {}
func (n *IfStmt) stmtNode()       {}
func (n *ForStmt) stmtNode()      {}
func (n *ReturnStmt) stmtNode()   {}
func (n *ThrowStmt) stmtNode()    {}
func (n *EmitStmt) stmtNode()     {}
func (n *AssignStmt) stmtNode()   {}
func (n *BreakStmt) stmtNode()    {}
func (n *ContinueStmt) stmtNode() {}
func (n *ExprStmt) stmtNode()     {}
func (n *OnDecl) stmtNode()       {}

func (n *Literal) GetPos() token.Position         { return n.Pos }
func (n *Ident) GetPos() token.Position           { return n.Pos }
func (n *MemberExpr) GetPos() token.Position      { return n.Pos }
func (n *CallExpr) GetPos() token.Position        { return n.Pos }
func (n *BinaryExpr) GetPos() token.Position      { return n.Pos }
func (n *UnaryExpr) GetPos() token.Position       { return n.Pos }
func (n *ElvisExpr) GetPos() token.Position       { return n.Pos }
func (n *BangElvisExpr) GetPos() token.Position   { return n.Pos }
func (n *WhenExpr) GetPos() token.Position        { return n.Pos }
func (n *LambdaExpr) GetPos() token.Position      { return n.Pos }
func (n *ListExpr) GetPos() token.Position        { return n.Pos }
func (n *ObjectExpr) GetPos() token.Position      { return n.Pos }
func (n *TemplateString) GetPos() token.Position  { return n.Pos }
func (n *RangeExpr) GetPos() token.Position       { return n.Pos }
func (n *TransactionExpr) GetPos() token.Position { return n.Pos }
func (n *YieldExpr) GetPos() token.Position       { return n.Pos }
func (n *AsyncExpr) GetPos() token.Position       { return n.Pos }
func (n *AwaitExpr) GetPos() token.Position       { return n.Pos }

func (n *ValStmt) GetPos() token.Position      { return n.Pos }
func (n *IfStmt) GetPos() token.Position       { return n.Pos }
func (n *ForStmt) GetPos() token.Position      { return n.Pos }
func (n *ReturnStmt) GetPos() token.Position   { return n.Pos }
func (n *ThrowStmt) GetPos() token.Position    { return n.Pos }
func (n *EmitStmt) GetPos() token.Position     { return n.Pos }
func (n *AssignStmt) GetPos() token.Position   { return n.Pos }
func (n *BreakStmt) GetPos() token.Position    { return n.Pos }
func (n *ContinueStmt) GetPos() token.Position { return n.Pos }
func (n *ExprStmt) GetPos() token.Position     { return n.Pos }
func (n *EventDecl) GetPos() token.Position    { return n.Pos }
func (n *OnDecl) GetPos() token.Position       { return n.Pos }
