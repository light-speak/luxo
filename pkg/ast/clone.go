package ast

import "errors"

// CloneMap records source identities and their independently owned compilation nodes.
// Slices own new storage; nil and non-nil empty slices remain distinct.
// Maps are allocated only when their node kind occurs.
type CloneMap struct {
	Exprs           map[Expr]Expr
	Statements      map[Stmt]Stmt
	Files           map[*File]*File
	Imports         map[*ImportDecl]*ImportDecl
	Models          map[*ModelDecl]*ModelDecl
	Interfaces      map[*InterfaceDecl]*InterfaceDecl
	Enums           map[*EnumDecl]*EnumDecl
	Sealeds         map[*SealedDecl]*SealedDecl
	Variants        map[*SealedVariant]*SealedVariant
	Types           map[*TypeDecl]*TypeDecl
	APIs            map[*ApiDecl]*ApiDecl
	Functions       map[*FnDecl]*FnDecl
	Errors          map[*ErrorDecl]*ErrorDecl
	Extends         map[*ExtendDecl]*ExtendDecl
	Uses            map[*UseDecl]*UseDecl
	Middlewares     map[*MiddlewareDecl]*MiddlewareDecl
	Events          map[*EventDecl]*EventDecl
	Scopes          map[*ScopeDecl]*ScopeDecl
	Fields          map[*FieldDecl]*FieldDecl
	ComputedFields  map[*ComputedField]*ComputedField
	Params          map[*ParamDecl]*ParamDecl
	TypeRefs        map[*TypeRef]*TypeRef
	Directives      map[*Directive]*Directive
	Groups          map[*GroupProjection]*GroupProjection
	GroupConditions map[*GroupCondition]*GroupCondition
	Arguments       map[*NamedArg]*NamedArg
	WhenBranches    map[*WhenBranch]*WhenBranch
	Blocks          map[*Block]*Block
}

type cloneContext struct {
	*CloneMap
	err error
}

// CloneFiles copies the complete AST graph, retaining shared node identities and cycles.
// Original nodes and annotations are never modified. Unknown interface implementations
// return an error without exposing a partial clone or invoking their methods.
func CloneFiles(files []*File) ([]*File, *CloneMap, error) {
	c := &cloneContext{CloneMap: &CloneMap{}}
	clones := cloneNodeSlice(files, c.cloneFile)
	if c.err != nil {
		return nil, nil, c.err
	}
	return clones, c.CloneMap, nil
}

func cloneNodeSlice[T any](source []T, clone func(T) T) []T {
	if source == nil {
		return nil
	}
	target := make([]T, len(source))
	for i, value := range source {
		target[i] = clone(value)
	}
	return target
}

func cloneValueSlice[T any](source []T) []T {
	if source == nil {
		return nil
	}
	target := make([]T, len(source))
	copy(target, source)
	return target
}

func (c *cloneContext) cloneExpr(source Expr) Expr {
	if source == nil {
		return nil
	}
	switch source := source.(type) {
	case *Literal:
		return c.cloneLiteral(source)
	case *Ident:
		return c.cloneIdent(source)
	case *MemberExpr:
		return c.cloneMemberExpr(source)
	case *CallExpr:
		return c.cloneCallExpr(source)
	case *BinaryExpr:
		return c.cloneBinaryExpr(source)
	case *UnaryExpr:
		return c.cloneUnaryExpr(source)
	case *ElvisExpr:
		return c.cloneElvisExpr(source)
	case *BangElvisExpr:
		return c.cloneBangElvisExpr(source)
	case *WhenExpr:
		return c.cloneWhenExpr(source)
	case *LambdaExpr:
		return c.cloneLambdaExpr(source)
	case *ListExpr:
		return c.cloneListExpr(source)
	case *ObjectExpr:
		return c.cloneObjectExpr(source)
	case *TemplateString:
		return c.cloneTemplateString(source)
	case *RangeExpr:
		return c.cloneRangeExpr(source)
	case *TransactionExpr:
		return c.cloneTransactionExpr(source)
	case *YieldExpr:
		return c.cloneYieldExpr(source)
	case *AsyncExpr:
		return c.cloneAsyncExpr(source)
	case *AwaitExpr:
		return c.cloneAwaitExpr(source)
	case *ForStmt:
		return c.cloneForStmt(source)
	default:
		if c.err == nil {
			c.err = errors.New("cannot clone unsupported AST expr implementation")
		}
		return nil
	}
}

func (c *cloneContext) cloneStmt(source Stmt) Stmt {
	if source == nil {
		return nil
	}
	switch source := source.(type) {
	case *OnDecl:
		return c.cloneOnDecl(source)
	case *ValStmt:
		return c.cloneValStmt(source)
	case *IfStmt:
		return c.cloneIfStmt(source)
	case *ForStmt:
		return c.cloneForStmt(source)
	case *ReturnStmt:
		return c.cloneReturnStmt(source)
	case *ThrowStmt:
		return c.cloneThrowStmt(source)
	case *EmitStmt:
		return c.cloneEmitStmt(source)
	case *AssignStmt:
		return c.cloneAssignStmt(source)
	case *BreakStmt:
		return c.cloneBreakStmt(source)
	case *ContinueStmt:
		return c.cloneContinueStmt(source)
	case *ExprStmt:
		return c.cloneExprStmt(source)
	default:
		if c.err == nil {
			c.err = errors.New("cannot clone unsupported AST stmt implementation")
		}
		return nil
	}
}

func (c *cloneContext) cloneFile(source *File) *File {
	if source == nil {
		return nil
	}
	if target, ok := c.Files[source]; ok {
		return target
	}
	target := *source
	if c.Files == nil {
		c.Files = make(map[*File]*File)
	}
	c.Files[source] = &target
	target.Models = cloneNodeSlice(source.Models, c.cloneModelDecl)
	target.Interfaces = cloneNodeSlice(source.Interfaces, c.cloneInterfaceDecl)
	target.Enums = cloneNodeSlice(source.Enums, c.cloneEnumDecl)
	target.Sealeds = cloneNodeSlice(source.Sealeds, c.cloneSealedDecl)
	target.Types = cloneNodeSlice(source.Types, c.cloneTypeDecl)
	target.APIs = cloneNodeSlice(source.APIs, c.cloneApiDecl)
	target.Functions = cloneNodeSlice(source.Functions, c.cloneFnDecl)
	target.Errors = cloneNodeSlice(source.Errors, c.cloneErrorDecl)
	target.Extends = cloneNodeSlice(source.Extends, c.cloneExtendDecl)
	target.Uses = cloneNodeSlice(source.Uses, c.cloneUseDecl)
	target.Middlewares = cloneNodeSlice(source.Middlewares, c.cloneMiddlewareDecl)
	target.Events = cloneNodeSlice(source.Events, c.cloneEventDecl)
	target.Listeners = cloneNodeSlice(source.Listeners, c.cloneOnDecl)
	target.Imports = cloneNodeSlice(source.Imports, c.cloneImportDecl)
	target.Globals = cloneNodeSlice(source.Globals, c.cloneValStmt)
	return &target
}

func (c *cloneContext) cloneImportDecl(source *ImportDecl) *ImportDecl {
	if source == nil {
		return nil
	}
	if target, ok := c.Imports[source]; ok {
		return target
	}
	target := *source
	if c.Imports == nil {
		c.Imports = make(map[*ImportDecl]*ImportDecl)
	}
	c.Imports[source] = &target
	return &target
}

func (c *cloneContext) cloneModelDecl(source *ModelDecl) *ModelDecl {
	if source == nil {
		return nil
	}
	if target, ok := c.Models[source]; ok {
		return target
	}
	target := *source
	if c.Models == nil {
		c.Models = make(map[*ModelDecl]*ModelDecl)
	}
	c.Models[source] = &target
	target.Parents = cloneValueSlice(source.Parents)
	target.Fields = cloneNodeSlice(source.Fields, c.cloneFieldDecl)
	target.Scopes = cloneNodeSlice(source.Scopes, c.cloneScopeDecl)
	target.Directives = cloneNodeSlice(source.Directives, c.cloneDirective)
	return &target
}

func (c *cloneContext) cloneInterfaceDecl(source *InterfaceDecl) *InterfaceDecl {
	if source == nil {
		return nil
	}
	if target, ok := c.Interfaces[source]; ok {
		return target
	}
	target := *source
	if c.Interfaces == nil {
		c.Interfaces = make(map[*InterfaceDecl]*InterfaceDecl)
	}
	c.Interfaces[source] = &target
	target.Fields = cloneNodeSlice(source.Fields, c.cloneFieldDecl)
	target.Methods = cloneNodeSlice(source.Methods, c.cloneFnDecl)
	return &target
}

func (c *cloneContext) cloneEnumDecl(source *EnumDecl) *EnumDecl {
	if source == nil {
		return nil
	}
	if target, ok := c.Enums[source]; ok {
		return target
	}
	target := *source
	if c.Enums == nil {
		c.Enums = make(map[*EnumDecl]*EnumDecl)
	}
	c.Enums[source] = &target
	target.Values = cloneValueSlice(source.Values)
	return &target
}

func (c *cloneContext) cloneSealedDecl(source *SealedDecl) *SealedDecl {
	if source == nil {
		return nil
	}
	if target, ok := c.Sealeds[source]; ok {
		return target
	}
	target := *source
	if c.Sealeds == nil {
		c.Sealeds = make(map[*SealedDecl]*SealedDecl)
	}
	c.Sealeds[source] = &target
	target.Variants = cloneNodeSlice(source.Variants, c.cloneSealedVariant)
	return &target
}

func (c *cloneContext) cloneSealedVariant(source *SealedVariant) *SealedVariant {
	if source == nil {
		return nil
	}
	if target, ok := c.Variants[source]; ok {
		return target
	}
	target := *source
	if c.Variants == nil {
		c.Variants = make(map[*SealedVariant]*SealedVariant)
	}
	c.Variants[source] = &target
	target.Fields = cloneNodeSlice(source.Fields, c.cloneParamDecl)
	return &target
}

func (c *cloneContext) cloneTypeDecl(source *TypeDecl) *TypeDecl {
	if source == nil {
		return nil
	}
	if target, ok := c.Types[source]; ok {
		return target
	}
	target := *source
	if c.Types == nil {
		c.Types = make(map[*TypeDecl]*TypeDecl)
	}
	c.Types[source] = &target
	target.TypeParams = cloneValueSlice(source.TypeParams)
	target.Fields = cloneNodeSlice(source.Fields, c.cloneFieldDecl)
	return &target
}

func (c *cloneContext) cloneApiDecl(source *ApiDecl) *ApiDecl {
	if source == nil {
		return nil
	}
	if target, ok := c.APIs[source]; ok {
		return target
	}
	target := *source
	if c.APIs == nil {
		c.APIs = make(map[*ApiDecl]*ApiDecl)
	}
	c.APIs[source] = &target
	target.Params = cloneNodeSlice(source.Params, c.cloneParamDecl)
	target.ReturnType = c.cloneTypeRef(source.ReturnType)
	target.Directives = cloneNodeSlice(source.Directives, c.cloneDirective)
	target.Body = c.cloneBlock(source.Body)
	return &target
}

func (c *cloneContext) cloneFnDecl(source *FnDecl) *FnDecl {
	if source == nil {
		return nil
	}
	if target, ok := c.Functions[source]; ok {
		return target
	}
	target := *source
	if c.Functions == nil {
		c.Functions = make(map[*FnDecl]*FnDecl)
	}
	c.Functions[source] = &target
	target.Params = cloneNodeSlice(source.Params, c.cloneParamDecl)
	target.ReturnType = c.cloneTypeRef(source.ReturnType)
	target.Directives = cloneNodeSlice(source.Directives, c.cloneDirective)
	target.Body = c.cloneBlock(source.Body)
	return &target
}

func (c *cloneContext) cloneErrorDecl(source *ErrorDecl) *ErrorDecl {
	if source == nil {
		return nil
	}
	if target, ok := c.Errors[source]; ok {
		return target
	}
	target := *source
	if c.Errors == nil {
		c.Errors = make(map[*ErrorDecl]*ErrorDecl)
	}
	c.Errors[source] = &target
	target.Fields = cloneNodeSlice(source.Fields, c.cloneParamDecl)
	return &target
}

func (c *cloneContext) cloneExtendDecl(source *ExtendDecl) *ExtendDecl {
	if source == nil {
		return nil
	}
	if target, ok := c.Extends[source]; ok {
		return target
	}
	target := *source
	if c.Extends == nil {
		c.Extends = make(map[*ExtendDecl]*ExtendDecl)
	}
	c.Extends[source] = &target
	target.Fields = cloneNodeSlice(source.Fields, c.cloneFieldDecl)
	return &target
}

func (c *cloneContext) cloneUseDecl(source *UseDecl) *UseDecl {
	if source == nil {
		return nil
	}
	if target, ok := c.Uses[source]; ok {
		return target
	}
	target := *source
	if c.Uses == nil {
		c.Uses = make(map[*UseDecl]*UseDecl)
	}
	c.Uses[source] = &target
	target.Names = cloneValueSlice(source.Names)
	return &target
}

func (c *cloneContext) cloneMiddlewareDecl(source *MiddlewareDecl) *MiddlewareDecl {
	if source == nil {
		return nil
	}
	if target, ok := c.Middlewares[source]; ok {
		return target
	}
	target := *source
	if c.Middlewares == nil {
		c.Middlewares = make(map[*MiddlewareDecl]*MiddlewareDecl)
	}
	c.Middlewares[source] = &target
	target.Directives = cloneNodeSlice(source.Directives, c.cloneDirective)
	target.Body = c.cloneBlock(source.Body)
	return &target
}

func (c *cloneContext) cloneEventDecl(source *EventDecl) *EventDecl {
	if source == nil {
		return nil
	}
	if target, ok := c.Events[source]; ok {
		return target
	}
	target := *source
	if c.Events == nil {
		c.Events = make(map[*EventDecl]*EventDecl)
	}
	c.Events[source] = &target
	target.Params = cloneNodeSlice(source.Params, c.cloneParamDecl)
	return &target
}

func (c *cloneContext) cloneOnDecl(source *OnDecl) *OnDecl {
	if source == nil {
		return nil
	}
	if target, ok := c.Statements[source]; ok {
		return target.(*OnDecl)
	}
	target := *source
	if c.Statements == nil {
		c.Statements = make(map[Stmt]Stmt)
	}
	c.Statements[source] = &target
	target.Params = cloneValueSlice(source.Params)
	target.Body = c.cloneBlock(source.Body)
	return &target
}

func (c *cloneContext) cloneScopeDecl(source *ScopeDecl) *ScopeDecl {
	if source == nil {
		return nil
	}
	if target, ok := c.Scopes[source]; ok {
		return target
	}
	target := *source
	if c.Scopes == nil {
		c.Scopes = make(map[*ScopeDecl]*ScopeDecl)
	}
	c.Scopes[source] = &target
	target.Params = cloneNodeSlice(source.Params, c.cloneParamDecl)
	target.Expr = c.cloneExpr(source.Expr)
	return &target
}

func (c *cloneContext) cloneFieldDecl(source *FieldDecl) *FieldDecl {
	if source == nil {
		return nil
	}
	if target, ok := c.Fields[source]; ok {
		return target
	}
	target := *source
	if c.Fields == nil {
		c.Fields = make(map[*FieldDecl]*FieldDecl)
	}
	c.Fields[source] = &target
	target.Type = c.cloneTypeRef(source.Type)
	target.Default = c.cloneExpr(source.Default)
	target.Directives = cloneNodeSlice(source.Directives, c.cloneDirective)
	target.Computed = c.cloneComputedField(source.Computed)
	return &target
}

func (c *cloneContext) cloneComputedField(source *ComputedField) *ComputedField {
	if source == nil {
		return nil
	}
	if target, ok := c.ComputedFields[source]; ok {
		return target
	}
	target := *source
	if c.ComputedFields == nil {
		c.ComputedFields = make(map[*ComputedField]*ComputedField)
	}
	c.ComputedFields[source] = &target
	target.Body = c.cloneBlock(source.Body)
	target.Directives = cloneNodeSlice(source.Directives, c.cloneDirective)
	return &target
}

func (c *cloneContext) cloneParamDecl(source *ParamDecl) *ParamDecl {
	if source == nil {
		return nil
	}
	if target, ok := c.Params[source]; ok {
		return target
	}
	target := *source
	if c.Params == nil {
		c.Params = make(map[*ParamDecl]*ParamDecl)
	}
	c.Params[source] = &target
	target.Type = c.cloneTypeRef(source.Type)
	target.Default = c.cloneExpr(source.Default)
	return &target
}

func (c *cloneContext) cloneTypeRef(source *TypeRef) *TypeRef {
	if source == nil {
		return nil
	}
	if target, ok := c.TypeRefs[source]; ok {
		return target
	}
	target := *source
	if c.TypeRefs == nil {
		c.TypeRefs = make(map[*TypeRef]*TypeRef)
	}
	c.TypeRefs[source] = &target
	target.TypeArgs = cloneNodeSlice(source.TypeArgs, c.cloneTypeRef)
	target.Tuple = cloneNodeSlice(source.Tuple, c.cloneTypeRef)
	return &target
}

func (c *cloneContext) cloneDirective(source *Directive) *Directive {
	if source == nil {
		return nil
	}
	if target, ok := c.Directives[source]; ok {
		return target
	}
	target := *source
	if c.Directives == nil {
		c.Directives = make(map[*Directive]*Directive)
	}
	c.Directives[source] = &target
	target.Args = cloneNodeSlice(source.Args, c.cloneNamedArg)
	target.Body = c.cloneBlock(source.Body)
	return &target
}

func (c *cloneContext) cloneLiteral(source *Literal) *Literal {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*Literal)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	return &target
}

func (c *cloneContext) cloneIdent(source *Ident) *Ident {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*Ident)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	return &target
}

func (c *cloneContext) cloneMemberExpr(source *MemberExpr) *MemberExpr {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*MemberExpr)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	target.Object = c.cloneExpr(source.Object)
	return &target
}

func (c *cloneContext) cloneCallExpr(source *CallExpr) *CallExpr {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*CallExpr)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	target.Func = c.cloneExpr(source.Func)
	target.TypeArgs = cloneNodeSlice(source.TypeArgs, c.cloneTypeRef)
	target.ConstructorType = c.cloneTypeRef(source.ConstructorType)
	target.Args = cloneNodeSlice(source.Args, c.cloneNamedArg)
	target.Group = c.cloneGroupProjection(source.Group)
	target.ErrorConstructor = c.cloneErrorDecl(source.ErrorConstructor)
	return &target
}

func (c *cloneContext) cloneGroupProjection(source *GroupProjection) *GroupProjection {
	if source == nil {
		return nil
	}
	if target, ok := c.Groups[source]; ok {
		return target
	}
	target := *source
	if c.Groups == nil {
		c.Groups = make(map[*GroupProjection]*GroupProjection)
	}
	c.Groups[source] = &target
	target.Input = c.cloneExpr(source.Input)
	target.Keys = cloneValueSlice(source.Keys)
	target.Columns = cloneValueSlice(source.Columns)
	target.Having = cloneNodeSlice(source.Having, c.cloneGroupCondition)
	return &target
}

func (c *cloneContext) cloneGroupCondition(source *GroupCondition) *GroupCondition {
	if source == nil {
		return nil
	}
	if target, ok := c.GroupConditions[source]; ok {
		return target
	}
	target := *source
	if c.GroupConditions == nil {
		c.GroupConditions = make(map[*GroupCondition]*GroupCondition)
	}
	c.GroupConditions[source] = &target
	target.Value = c.cloneExpr(source.Value)
	target.Left = c.cloneGroupCondition(source.Left)
	target.Right = c.cloneGroupCondition(source.Right)
	return &target
}

func (c *cloneContext) cloneNamedArg(source *NamedArg) *NamedArg {
	if source == nil {
		return nil
	}
	if target, ok := c.Arguments[source]; ok {
		return target
	}
	target := *source
	if c.Arguments == nil {
		c.Arguments = make(map[*NamedArg]*NamedArg)
	}
	c.Arguments[source] = &target
	target.Value = c.cloneExpr(source.Value)
	target.ExpectedType = c.cloneTypeRef(source.ExpectedType)
	return &target
}

func (c *cloneContext) cloneBinaryExpr(source *BinaryExpr) *BinaryExpr {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*BinaryExpr)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	target.Left = c.cloneExpr(source.Left)
	target.Right = c.cloneExpr(source.Right)
	return &target
}

func (c *cloneContext) cloneUnaryExpr(source *UnaryExpr) *UnaryExpr {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*UnaryExpr)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	target.Value = c.cloneExpr(source.Value)
	return &target
}

func (c *cloneContext) cloneElvisExpr(source *ElvisExpr) *ElvisExpr {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*ElvisExpr)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	target.Left = c.cloneExpr(source.Left)
	target.Right = c.cloneExpr(source.Right)
	return &target
}

func (c *cloneContext) cloneBangElvisExpr(source *BangElvisExpr) *BangElvisExpr {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*BangElvisExpr)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	target.Left = c.cloneExpr(source.Left)
	target.Right = c.cloneExpr(source.Right)
	return &target
}

func (c *cloneContext) cloneWhenExpr(source *WhenExpr) *WhenExpr {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*WhenExpr)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	target.Subject = c.cloneExpr(source.Subject)
	target.Branches = cloneNodeSlice(source.Branches, c.cloneWhenBranch)
	target.Else = c.cloneExpr(source.Else)
	return &target
}

func (c *cloneContext) cloneWhenBranch(source *WhenBranch) *WhenBranch {
	if source == nil {
		return nil
	}
	if target, ok := c.WhenBranches[source]; ok {
		return target
	}
	target := *source
	if c.WhenBranches == nil {
		c.WhenBranches = make(map[*WhenBranch]*WhenBranch)
	}
	c.WhenBranches[source] = &target
	target.Condition = c.cloneExpr(source.Condition)
	target.Body = c.cloneExpr(source.Body)
	return &target
}

func (c *cloneContext) cloneLambdaExpr(source *LambdaExpr) *LambdaExpr {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*LambdaExpr)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	target.Params = cloneValueSlice(source.Params)
	target.Body = c.cloneBlock(source.Body)
	return &target
}

func (c *cloneContext) cloneListExpr(source *ListExpr) *ListExpr {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*ListExpr)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	target.Items = cloneNodeSlice(source.Items, c.cloneExpr)
	return &target
}

func (c *cloneContext) cloneObjectExpr(source *ObjectExpr) *ObjectExpr {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*ObjectExpr)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	target.Fields = cloneNodeSlice(source.Fields, c.cloneNamedArg)
	return &target
}

func (c *cloneContext) cloneTemplateString(source *TemplateString) *TemplateString {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*TemplateString)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	target.Parts = cloneNodeSlice(source.Parts, c.cloneExpr)
	return &target
}

func (c *cloneContext) cloneRangeExpr(source *RangeExpr) *RangeExpr {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*RangeExpr)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	target.Start = c.cloneExpr(source.Start)
	target.End = c.cloneExpr(source.End)
	return &target
}

func (c *cloneContext) cloneTransactionExpr(source *TransactionExpr) *TransactionExpr {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*TransactionExpr)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	target.Body = c.cloneBlock(source.Body)
	return &target
}

func (c *cloneContext) cloneYieldExpr(source *YieldExpr) *YieldExpr {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*YieldExpr)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	target.Value = c.cloneExpr(source.Value)
	return &target
}

func (c *cloneContext) cloneAsyncExpr(source *AsyncExpr) *AsyncExpr {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*AsyncExpr)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	target.Body = c.cloneBlock(source.Body)
	return &target
}

func (c *cloneContext) cloneAwaitExpr(source *AwaitExpr) *AwaitExpr {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*AwaitExpr)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	target.Body = c.cloneBlock(source.Body)
	return &target
}

func (c *cloneContext) cloneValStmt(source *ValStmt) *ValStmt {
	if source == nil {
		return nil
	}
	if target, ok := c.Statements[source]; ok {
		return target.(*ValStmt)
	}
	target := *source
	if c.Statements == nil {
		c.Statements = make(map[Stmt]Stmt)
	}
	c.Statements[source] = &target
	target.Names = cloneValueSlice(source.Names)
	target.Type = c.cloneTypeRef(source.Type)
	target.Value = c.cloneExpr(source.Value)
	return &target
}

func (c *cloneContext) cloneIfStmt(source *IfStmt) *IfStmt {
	if source == nil {
		return nil
	}
	if target, ok := c.Statements[source]; ok {
		return target.(*IfStmt)
	}
	target := *source
	if c.Statements == nil {
		c.Statements = make(map[Stmt]Stmt)
	}
	c.Statements[source] = &target
	target.Condition = c.cloneExpr(source.Condition)
	target.Then = c.cloneBlock(source.Then)
	return &target
}

func (c *cloneContext) cloneForStmt(source *ForStmt) *ForStmt {
	if source == nil {
		return nil
	}
	if target, ok := c.Exprs[source]; ok {
		return target.(*ForStmt)
	}
	target := *source
	if c.Exprs == nil {
		c.Exprs = make(map[Expr]Expr)
	}
	c.Exprs[source] = &target
	if c.Statements == nil {
		c.Statements = make(map[Stmt]Stmt)
	}
	c.Statements[source] = &target
	target.Collection = c.cloneExpr(source.Collection)
	target.Body = c.cloneBlock(source.Body)
	return &target
}

func (c *cloneContext) cloneReturnStmt(source *ReturnStmt) *ReturnStmt {
	if source == nil {
		return nil
	}
	if target, ok := c.Statements[source]; ok {
		return target.(*ReturnStmt)
	}
	target := *source
	if c.Statements == nil {
		c.Statements = make(map[Stmt]Stmt)
	}
	c.Statements[source] = &target
	target.Value = c.cloneExpr(source.Value)
	return &target
}

func (c *cloneContext) cloneThrowStmt(source *ThrowStmt) *ThrowStmt {
	if source == nil {
		return nil
	}
	if target, ok := c.Statements[source]; ok {
		return target.(*ThrowStmt)
	}
	target := *source
	if c.Statements == nil {
		c.Statements = make(map[Stmt]Stmt)
	}
	c.Statements[source] = &target
	target.Error = c.cloneExpr(source.Error)
	return &target
}

func (c *cloneContext) cloneEmitStmt(source *EmitStmt) *EmitStmt {
	if source == nil {
		return nil
	}
	if target, ok := c.Statements[source]; ok {
		return target.(*EmitStmt)
	}
	target := *source
	if c.Statements == nil {
		c.Statements = make(map[Stmt]Stmt)
	}
	c.Statements[source] = &target
	target.Args = cloneNodeSlice(source.Args, c.cloneNamedArg)
	return &target
}

func (c *cloneContext) cloneAssignStmt(source *AssignStmt) *AssignStmt {
	if source == nil {
		return nil
	}
	if target, ok := c.Statements[source]; ok {
		return target.(*AssignStmt)
	}
	target := *source
	if c.Statements == nil {
		c.Statements = make(map[Stmt]Stmt)
	}
	c.Statements[source] = &target
	target.Target = c.cloneExpr(source.Target)
	target.Value = c.cloneExpr(source.Value)
	return &target
}

func (c *cloneContext) cloneBreakStmt(source *BreakStmt) *BreakStmt {
	if source == nil {
		return nil
	}
	if target, ok := c.Statements[source]; ok {
		return target.(*BreakStmt)
	}
	target := *source
	if c.Statements == nil {
		c.Statements = make(map[Stmt]Stmt)
	}
	c.Statements[source] = &target
	return &target
}

func (c *cloneContext) cloneContinueStmt(source *ContinueStmt) *ContinueStmt {
	if source == nil {
		return nil
	}
	if target, ok := c.Statements[source]; ok {
		return target.(*ContinueStmt)
	}
	target := *source
	if c.Statements == nil {
		c.Statements = make(map[Stmt]Stmt)
	}
	c.Statements[source] = &target
	return &target
}

func (c *cloneContext) cloneExprStmt(source *ExprStmt) *ExprStmt {
	if source == nil {
		return nil
	}
	if target, ok := c.Statements[source]; ok {
		return target.(*ExprStmt)
	}
	target := *source
	if c.Statements == nil {
		c.Statements = make(map[Stmt]Stmt)
	}
	c.Statements[source] = &target
	target.Expr = c.cloneExpr(source.Expr)
	return &target
}

func (c *cloneContext) cloneBlock(source *Block) *Block {
	if source == nil {
		return nil
	}
	if target, ok := c.Blocks[source]; ok {
		return target
	}
	target := *source
	if c.Blocks == nil {
		c.Blocks = make(map[*Block]*Block)
	}
	c.Blocks[source] = &target
	target.Stmts = cloneNodeSlice(source.Stmts, c.cloneStmt)
	return &target
}
