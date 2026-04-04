package semantic

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// Error represents a semantic error.
type Error struct {
	Pos        token.Position
	Message    string
	Suggestion string // "did you mean ...?"
}

func (e Error) Error() string {
	s := fmt.Sprintf("%s: %s", e.Pos, e.Message)
	if e.Suggestion != "" {
		s += " (" + e.Suggestion + ")"
	}
	return s
}

// Warning represents a semantic warning.
type Warning struct {
	Pos     token.Position
	Message string
}

// Result is the output of semantic analysis.
type Result struct {
	Scope    *Scope
	Types    map[string]*ResolvedType // all resolved types by name
	Errors   []Error
	Warnings []Warning
}

// Analyzer performs semantic analysis on AST nodes.
type Analyzer struct {
	scope    *Scope
	types    map[string]*ResolvedType
	errors   []Error
	warnings []Warning
}

// New creates a new Analyzer.
func New() *Analyzer {
	a := &Analyzer{
		scope: NewScope(),
		types: make(map[string]*ResolvedType),
	}
	// register built-in types
	for name, typ := range BuiltinTypes() {
		a.types[name] = typ
		a.scope.Define(&Symbol{
			Name: name,
			Kind: SymType,
			Type: typ,
		})
	}
	return a
}

// Analyze performs semantic analysis on one or more parsed files.
func (a *Analyzer) Analyze(files []*ast.File) *Result {
	// Pass 1: collect all top-level declarations (types, models, enums, etc.)
	for _, file := range files {
		a.collectDeclarations(file)
	}

	// Pass 2: resolve inheritance, interface implementation
	for _, file := range files {
		a.resolveInheritance(file)
	}

	// Pass 3: resolve field types, check directives
	for _, file := range files {
		a.resolveFields(file)
	}

	// Pass 4: check api/fn bodies (expressions, statements)
	for _, file := range files {
		a.checkBodies(file)
	}

	return &Result{
		Scope:    a.scope,
		Types:    a.types,
		Errors:   a.errors,
		Warnings: a.warnings,
	}
}

// ========== Pass 1: Collect Declarations ==========

func (a *Analyzer) collectDeclarations(file *ast.File) {
	for _, m := range file.Models {
		a.declareType(m.Name, TypeModel, m.Pos, m.Doc)
	}
	for _, i := range file.Interfaces {
		a.declareType(i.Name, TypeInterface, i.Pos, i.Doc)
	}
	for _, e := range file.Enums {
		typ := a.declareType(e.Name, TypeEnum, e.Pos, e.Doc)
		if typ != nil {
			typ.EnumValues = e.Values
		}
	}
	for _, s := range file.Sealeds {
		typ := a.declareType(s.Name, TypeSealed, s.Pos, s.Doc)
		if typ != nil {
			a.collectSealedVariants(typ, s.Variants)
		}
	}
	for _, t := range file.Types {
		a.declareType(t.Name, TypeCustom, t.Pos, t.Doc)
		a.registerTypeParams(t.TypeParams)
	}
	for _, api := range file.APIs {
		a.scope.Define(&Symbol{
			Name: api.Name,
			Kind: SymApi,
			Pos:  api.Pos,
			Doc:  api.Doc,
		})
	}
	for _, fn := range file.Functions {
		a.scope.Define(&Symbol{
			Name: fn.Name,
			Kind: SymFn,
			Pos:  fn.Pos,
			Doc:  fn.Doc,
		})
	}
	for _, e := range file.Errors {
		a.scope.Define(&Symbol{
			Name: e.Name,
			Kind: SymError,
			Pos:  e.Pos,
			Doc:  e.Doc,
		})
	}
	for _, mw := range file.Middlewares {
		a.scope.Define(&Symbol{
			Name: mw.Name,
			Kind: SymMiddleware,
			Pos:  mw.Pos,
			Doc:  mw.Doc,
		})
	}
}

// collectSealedVariants populates a sealed type with its variant information.
func (a *Analyzer) collectSealedVariants(typ *ResolvedType, variants []*ast.SealedVariant) {
	for _, v := range variants {
		vi := &SealedVariantInfo{Name: v.Name}
		for _, f := range v.Fields {
			vi.Fields = append(vi.Fields, &FieldInfo{
				Name: f.Name,
				Pos:  token.Position{},
			})
		}
		typ.Variants = append(typ.Variants, vi)
	}
}

// registerTypeParams registers generic type parameters as types if not already declared.
func (a *Analyzer) registerTypeParams(params []string) {
	for _, tp := range params {
		if _, exists := a.types[tp]; !exists {
			a.types[tp] = &ResolvedType{Kind: TypeGeneric, Name: tp, Fields: make(map[string]*FieldInfo)}
		}
	}
}

func (a *Analyzer) declareType(name string, kind TypeKind, pos token.Position, doc string) *ResolvedType {
	if _, exists := a.types[name]; exists {
		a.addError(pos, "type '%s' already declared / 类型 '%s' 已声明", name, name)
		return nil
	}
	typ := &ResolvedType{
		Kind:   kind,
		Name:   name,
		Fields: make(map[string]*FieldInfo),
		Pos:    pos,
	}
	a.types[name] = typ
	a.scope.Define(&Symbol{
		Name: name,
		Kind: kindToSymbol(kind),
		Type: typ,
		Pos:  pos,
		Doc:  doc,
	})
	return typ
}

// ========== Pass 2: Resolve Inheritance ==========

func (a *Analyzer) resolveInheritance(file *ast.File) {
	for _, m := range file.Models {
		typ := a.types[m.Name]
		if typ == nil {
			continue
		}
		for _, parentName := range m.Parents {
			parent, ok := a.types[parentName]
			if !ok {
				a.addErrorWithSuggestion(m.Pos, parentName, "type '%s' not found / 类型 '%s' 未找到", parentName, parentName)
				continue
			}
			typ.Parents = append(typ.Parents, parent)
		}
	}
}

// ========== Pass 3: Resolve Fields ==========

func (a *Analyzer) resolveFields(file *ast.File) {
	a.resolveModelFields(file)
	a.resolveInterfaceFields(file)
	a.resolveTypeFields(file)
	a.resolveApiTypes(file)
	a.resolveFnTypes(file)
	a.resolveExtendFields(file)
}

func (a *Analyzer) resolveModelFields(file *ast.File) {
	for _, m := range file.Models {
		typ := a.types[m.Name]
		if typ == nil {
			continue
		}
		if typ.Fields == nil {
			typ.Fields = make(map[string]*FieldInfo)
		}
		for _, f := range m.Fields {
			fi := a.resolveFieldDecl(f)
			if fi != nil {
				if _, exists := typ.Fields[fi.Name]; exists {
					a.addError(f.Pos, "duplicate field '%s' in model '%s' / 模型 '%s' 中字段 '%s' 重复", fi.Name, m.Name, m.Name, fi.Name)
					continue
				}
				typ.Fields[fi.Name] = fi
			}
		}
		a.checkDirectives(m.Directives, "model")
	}
}

func (a *Analyzer) resolveInterfaceFields(file *ast.File) {
	for _, i := range file.Interfaces {
		typ := a.types[i.Name]
		if typ == nil {
			continue
		}
		if typ.Fields == nil {
			typ.Fields = make(map[string]*FieldInfo)
		}
		for _, f := range i.Fields {
			fi := a.resolveFieldDecl(f)
			if fi != nil {
				typ.Fields[fi.Name] = fi
			}
		}
	}
}

func (a *Analyzer) resolveTypeFields(file *ast.File) {
	for _, t := range file.Types {
		typ := a.types[t.Name]
		if typ == nil {
			continue
		}
		if typ.Fields == nil {
			typ.Fields = make(map[string]*FieldInfo)
		}
		for _, f := range t.Fields {
			fi := a.resolveFieldDecl(f)
			if fi != nil {
				typ.Fields[fi.Name] = fi
			}
		}
	}
}

func (a *Analyzer) resolveApiTypes(file *ast.File) {
	for _, api := range file.APIs {
		sym := a.scope.Lookup(api.Name)
		if sym == nil {
			continue
		}
		retType := a.resolveTypeRef(api.ReturnType, api.Pos)
		sym.Type = retType

		for _, p := range api.Params {
			a.resolveTypeRef(p.Type, api.Pos)
		}
		a.checkDirectives(api.Directives, "api")
	}
}

func (a *Analyzer) resolveFnTypes(file *ast.File) {
	for _, fn := range file.Functions {
		sym := a.scope.Lookup(fn.Name)
		if sym == nil {
			continue
		}
		if fn.ReturnType != nil {
			sym.Type = a.resolveTypeRef(fn.ReturnType, fn.Pos)
		}
		for _, p := range fn.Params {
			a.resolveTypeRef(p.Type, fn.Pos)
		}
	}
}

func (a *Analyzer) resolveExtendFields(file *ast.File) {
	for _, ext := range file.Extends {
		if _, ok := a.types[ext.Name]; !ok {
			// extend target might be in another module (resolved at gateway)
			// just validate that fields are valid
		}
		for _, f := range ext.Fields {
			a.resolveFieldDecl(f)
		}
	}
}

func (a *Analyzer) resolveFieldDecl(f *ast.FieldDecl) *FieldInfo {
	fi := &FieldInfo{
		Name:     f.Name,
		Pos:      f.Pos,
		Doc:      f.Doc,
		Computed: f.Computed != nil,
	}

	if f.Type != nil {
		fi.Type = a.resolveTypeRef(f.Type, f.Pos)
		fi.Nullable = f.Type.Nullable
	}

	if f.Default != nil {
		fi.HasDefault = true
	}

	for _, d := range f.Directives {
		fi.Directives = append(fi.Directives, d.Name)
	}

	a.checkFieldDirectives(f)
	return fi
}

func (a *Analyzer) resolveTypeRef(ref *ast.TypeRef, pos token.Position) *ResolvedType {
	if ref == nil {
		return &ResolvedType{Kind: TypeVoid, Name: "Void"}
	}

	// Tuple type: (Post, Video, Product)
	if len(ref.Tuple) > 0 {
		result := &ResolvedType{
			Kind:     TypeTuple,
			Nullable: ref.Nullable,
		}
		for _, t := range ref.Tuple {
			resolved := a.resolveTypeRef(t, pos)
			result.Tuple = append(result.Tuple, resolved)
		}
		return result
	}

	// Stream type: strip "stream " prefix
	name := ref.Name
	if after, ok := strings.CutPrefix(name, "stream "); ok {
		name = after
	}

	// Look up the type
	typ, ok := a.types[name]
	if !ok {
		a.addErrorWithSuggestion(pos, name, "unknown type '%s' / 未知类型 '%s'", name, name)
		return &ResolvedType{Kind: TypeUnknown, Name: name, Nullable: ref.Nullable}
	}

	result := &ResolvedType{
		Kind:     typ.Kind,
		Name:     typ.Name,
		Nullable: ref.Nullable,
		IsList:   ref.IsList,
		Fields:   typ.Fields,
		Parents:  typ.Parents,
		Pos:      typ.Pos,
	}

	// Generic args
	for _, arg := range ref.TypeArgs {
		result.TypeArgs = append(result.TypeArgs, a.resolveTypeRef(arg, pos))
	}

	return result
}

// ========== Pass 4: Check Bodies ==========

func (a *Analyzer) checkBodies(file *ast.File) {
	for _, api := range file.APIs {
		if api.Body != nil {
			scope := a.scope.Child()
			// add params to scope
			for _, p := range api.Params {
				pType := a.resolveTypeRef(p.Type, api.Pos)
				scope.Define(&Symbol{
					Name: p.Name,
					Kind: SymParam,
					Type: pType,
					Pos:  api.Pos,
				})
			}
			// add currentUser to scope
			scope.Define(&Symbol{
				Name: "currentUser",
				Kind: SymVariable,
				Type: a.types["Identity"],
			})
			a.checkBlock(api.Body, scope)
		}
	}

	for _, fn := range file.Functions {
		if fn.Body != nil {
			scope := a.scope.Child()
			for _, p := range fn.Params {
				pType := a.resolveTypeRef(p.Type, fn.Pos)
				scope.Define(&Symbol{
					Name: p.Name,
					Kind: SymParam,
					Type: pType,
					Pos:  fn.Pos,
				})
			}
			a.checkBlock(fn.Body, scope)
		}
	}
}

func (a *Analyzer) checkBlock(block *ast.Block, scope *Scope) {
	if block == nil {
		return
	}
	for _, stmt := range block.Stmts {
		a.checkStmt(stmt, scope)
	}
}

func (a *Analyzer) checkStmt(stmt ast.Stmt, scope *Scope) {
	switch s := stmt.(type) {
	case *ast.ValStmt:
		exprType := a.checkExpr(s.Value, scope)
		scope.Define(&Symbol{
			Name: s.Name,
			Kind: SymVariable,
			Type: exprType,
			Pos:  s.Pos,
		})

	case *ast.IfStmt:
		a.checkExpr(s.Condition, scope)
		childScope := scope.Child()
		a.checkBlock(s.Then, childScope)
		if s.Else != nil {
			elseScope := scope.Child()
			a.checkBlock(s.Else, elseScope)
		}

	case *ast.ForStmt:
		childScope := scope.Child()
		if s.Collection != nil {
			collType := a.checkExpr(s.Collection, childScope)
			// only define loop variable if VarName is set (for item in collection)
			if s.VarName != "" {
				var itemType *ResolvedType
				if collType != nil && collType.IsList {
					itemType = &ResolvedType{
						Kind:   collType.Kind,
						Name:   collType.Name,
						Fields: collType.Fields,
					}
				}
				childScope.Define(&Symbol{
					Name: s.VarName,
					Kind: SymVariable,
					Type: itemType,
					Pos:  s.Pos,
				})
			}
		}
		// for condition { } and for { } — body shares parent scope for variable access
		a.checkBlock(s.Body, childScope)

	case *ast.ReturnStmt:
		if s.Value != nil {
			a.checkExpr(s.Value, scope)
		}

	case *ast.ThrowStmt:
		a.checkExpr(s.Error, scope)

	case *ast.EmitStmt:
		for _, arg := range s.Args {
			a.checkExpr(arg.Value, scope)
		}

	case *ast.AssignStmt:
		// check target exists
		a.checkExpr(s.Target, scope)
		// check value
		a.checkExpr(s.Value, scope)

	case *ast.BreakStmt:
		// valid in any loop context, no check needed at semantic level

	case *ast.ExprStmt:
		if s.Expr != nil {
			a.checkExpr(s.Expr, scope)
		}
	}
}

func (a *Analyzer) checkExpr(expr ast.Expr, scope *Scope) *ResolvedType {
	if expr == nil {
		return nil
	}

	switch e := expr.(type) {
	case *ast.Literal:
		return a.checkLiteralExpr(e)
	case *ast.Ident:
		return a.checkIdentExpr(e, scope)
	case *ast.MemberExpr:
		return a.checkMemberExpr(e, scope)
	case *ast.CallExpr:
		return a.checkCallExpr(e, scope)
	case *ast.BinaryExpr:
		return a.checkBinaryExpr(e, scope)
	case *ast.UnaryExpr:
		return a.checkUnaryExpr(e, scope)
	case *ast.ElvisExpr:
		return a.checkElvisExpr(e, scope)
	case *ast.WhenExpr:
		return a.checkWhenExpr(e, scope)
	case *ast.LambdaExpr:
		return a.checkLambdaExpr(e, scope)
	default:
		return a.checkCompositeExpr(expr, scope)
	}
}

// checkCompositeExpr handles composite expression types (list, object, range, transaction, template).
func (a *Analyzer) checkCompositeExpr(expr ast.Expr, scope *Scope) *ResolvedType {
	switch e := expr.(type) {
	case *ast.ListExpr:
		return a.checkListExpr(e, scope)
	case *ast.ObjectExpr:
		return a.checkObjectExpr(e, scope)
	case *ast.RangeExpr:
		return a.checkRangeExpr(e, scope)
	case *ast.TransactionExpr:
		return a.checkTransactionExpr(e, scope)
	case *ast.TemplateString:
		return a.checkTemplateString(e, scope)
	case *ast.YieldExpr:
		return a.checkExpr(e.Value, scope)
	case *ast.AsyncExpr:
		childScope := scope.Child()
		a.checkBlock(e.Body, childScope)
		return nil
	case *ast.AwaitExpr:
		childScope := scope.Child()
		a.checkBlock(e.Body, childScope)
		return nil
	case *ast.ForStmt:
		// for as expression — check like statement but return type
		a.checkStmt(e, scope)
		return nil // yield type inference is complex, return nil for now
	}
	return nil
}

func (a *Analyzer) checkListExpr(e *ast.ListExpr, scope *Scope) *ResolvedType {
	for _, item := range e.Items {
		a.checkExpr(item, scope)
	}
	return nil
}

func (a *Analyzer) checkObjectExpr(e *ast.ObjectExpr, scope *Scope) *ResolvedType {
	for _, f := range e.Fields {
		a.checkExpr(f.Value, scope)
	}
	return nil
}

func (a *Analyzer) checkRangeExpr(e *ast.RangeExpr, scope *Scope) *ResolvedType {
	a.checkExpr(e.Start, scope)
	a.checkExpr(e.End, scope)
	return nil
}

func (a *Analyzer) checkTransactionExpr(e *ast.TransactionExpr, scope *Scope) *ResolvedType {
	childScope := scope.Child()
	a.checkBlock(e.Body, childScope)
	return nil
}

func (a *Analyzer) checkTemplateString(e *ast.TemplateString, scope *Scope) *ResolvedType {
	for _, part := range e.Parts {
		a.checkExpr(part, scope)
	}
	return &ResolvedType{Kind: TypeString, Name: "String"}
}

func (a *Analyzer) checkLiteralExpr(e *ast.Literal) *ResolvedType {
	return a.literalType(e)
}

func (a *Analyzer) checkIdentExpr(e *ast.Ident, scope *Scope) *ResolvedType {
	// check if it's a type name
	if typ, ok := a.types[e.Name]; ok {
		return typ
	}
	// check local scope (includes parent scopes)
	resolved := scope.ResolvedTypeOf(e.Name)
	if resolved != nil {
		return resolved
	}
	sym := scope.Lookup(e.Name)
	if sym != nil {
		return sym.Type
	}
	// don't error on built-in operations used as expressions
	if isBuiltinOp(e.Name) {
		return nil
	}
	a.addErrorWithSuggestion(e.Pos, e.Name, "undefined: '%s' / 未定义: '%s'", e.Name, e.Name)
	return nil
}

func (a *Analyzer) checkMemberExpr(e *ast.MemberExpr, scope *Scope) *ResolvedType {
	objType := a.checkExpr(e.Object, scope)
	if objType == nil {
		return nil
	}

	if e.SafeCall && !objType.Nullable {
		a.addWarning(e.Pos, "unnecessary safe call (?.) on non-null type '%s' / 对非空类型 '%s' 使用了不必要的安全调用 (?.)", objType.Name, objType.Name)
	}

	// collection methods on list types
	if objType.IsList && isCollectionMethod(e.Field) {
		return a.resolveCollectionMethod(e.Field, objType)
	}

	field := objType.LookupField(e.Field)
	if field == nil {
		// check enum values
		if objType.Kind == TypeEnum {
			for _, v := range objType.EnumValues {
				if v == e.Field {
					return objType
				}
			}
		}
		a.addFieldError(e.Pos, objType, e.Field)
		return nil
	}

	result := field.Type
	if result != nil && e.SafeCall {
		result = result.AsNullable()
	}
	return result
}

func (a *Analyzer) checkCallExpr(e *ast.CallExpr, scope *Scope) *ResolvedType {
	a.checkExpr(e.Func, scope)

	callScope := a.injectCRUDScope(e, scope)

	for _, arg := range e.Args {
		a.checkExpr(arg.Value, callScope)
	}

	return a.inferCallReturnType(e)
}

// injectCRUDScope creates a child scope with model fields injected for CRUD operations.
func (a *Analyzer) injectCRUDScope(e *ast.CallExpr, scope *Scope) *Scope {
	ident, ok := e.Func.(*ast.Ident)
	if !ok || !isCRUDOp(ident.Name) || len(e.Args) == 0 {
		return scope
	}
	modelIdent, ok := e.Args[0].Value.(*ast.Ident)
	if !ok {
		return scope
	}
	modelType, ok := a.types[modelIdent.Name]
	if !ok {
		return scope
	}
	callScope := scope.Child()
	a.defineFieldsInScope(callScope, modelType)
	for _, parent := range modelType.Parents {
		a.defineFieldsInScope(callScope, parent)
	}
	return callScope
}

// defineFieldsInScope defines all fields from a type into the given scope.
func (a *Analyzer) defineFieldsInScope(scope *Scope, typ *ResolvedType) {
	for fieldName, field := range typ.Fields {
		scope.Define(&Symbol{
			Name: fieldName,
			Kind: SymField,
			Type: field.Type,
		})
	}
}

// inferCallReturnType infers the return type of a call expression.
func (a *Analyzer) inferCallReturnType(e *ast.CallExpr) *ResolvedType {
	ident, ok := e.Func.(*ast.Ident)
	if !ok {
		return nil
	}
	if isCRUDOp(ident.Name) && len(e.Args) > 0 {
		return a.inferCRUDReturnType(e, ident.Name)
	}
	sym := a.scope.Lookup(ident.Name)
	if sym != nil {
		return sym.Type
	}
	return nil
}

// inferCRUDReturnType infers the return type for CRUD operations.
func (a *Analyzer) inferCRUDReturnType(e *ast.CallExpr, opName string) *ResolvedType {
	modelIdent, ok := e.Args[0].Value.(*ast.Ident)
	if !ok {
		return nil
	}
	modelType, ok := a.types[modelIdent.Name]
	if !ok {
		return nil
	}
	if opName == "find" {
		return a.inferFindReturnType(e, modelType)
	}
	return modelType
}

// inferFindReturnType returns list type for where queries, nullable for id queries.
func (a *Analyzer) inferFindReturnType(e *ast.CallExpr, modelType *ResolvedType) *ResolvedType {
	for _, arg := range e.Args {
		if arg.Name == "where" {
			return &ResolvedType{
				Kind:    modelType.Kind,
				Name:    modelType.Name,
				IsList:  true,
				Fields:  modelType.Fields,
				Parents: modelType.Parents,
			}
		}
	}
	return modelType.AsNullable()
}

func isCRUDOp(name string) bool {
	switch name {
	case "find", "create", "update", "delete":
		return true
	}
	return false
}

func (a *Analyzer) checkBinaryExpr(e *ast.BinaryExpr, scope *Scope) *ResolvedType {
	left := a.checkExpr(e.Left, scope)
	right := a.checkExpr(e.Right, scope)
	return a.checkBinaryOp(e.Op, left, right, e.Pos)
}

func (a *Analyzer) checkUnaryExpr(e *ast.UnaryExpr, scope *Scope) *ResolvedType {
	inner := a.checkExpr(e.Value, scope)
	if e.Op == "throw" {
		return nil // throw never returns
	}
	if e.Op == "!" && inner != nil && inner.Kind != TypeBool {
		a.addError(e.Pos, "operator '!' requires Boolean, got '%s' / 运算符 '!' 需要 Boolean 类型，得到 '%s'", inner.Name, inner.Name)
	}
	return inner
}

func (a *Analyzer) checkElvisExpr(e *ast.ElvisExpr, scope *Scope) *ResolvedType {
	leftType := a.checkExpr(e.Left, scope)
	a.checkExpr(e.Right, scope)
	if leftType == nil {
		return nil
	}
	// after ?:, the left side is narrowed to non-null
	if ident, ok := e.Left.(*ast.Ident); ok {
		scope.Narrow(ident.Name, leftType.AsNonNull())
	}
	return leftType.AsNonNull()
}

func (a *Analyzer) checkWhenExpr(e *ast.WhenExpr, scope *Scope) *ResolvedType {
	if e.Subject != nil {
		a.checkExpr(e.Subject, scope)
	}
	for _, b := range e.Branches {
		if b.Condition != nil {
			a.checkExpr(b.Condition, scope)
		}
		if b.Body != nil {
			a.checkExpr(b.Body, scope)
		}
	}
	if e.Else != nil {
		a.checkExpr(e.Else, scope)
	}
	return nil // TODO: infer when return type
}

func (a *Analyzer) checkLambdaExpr(e *ast.LambdaExpr, scope *Scope) *ResolvedType {
	childScope := scope.Child()
	// 'it' is the implicit parameter
	childScope.Define(&Symbol{
		Name: "it",
		Kind: SymVariable,
	})
	a.checkBlock(e.Body, childScope)
	return nil
}

func (a *Analyzer) literalType(lit *ast.Literal) *ResolvedType {
	switch lit.Kind {
	case token.Int:
		return a.types["Int"]
	case token.Float:
		return a.types["Float"]
	case token.String:
		return a.types["String"]
	case token.Duration:
		return a.types["Duration"]
	case token.True, token.False:
		return a.types["Boolean"]
	case token.Null:
		return &ResolvedType{Kind: TypeUnknown, Name: "null", Nullable: true}
	}
	return nil
}

func (a *Analyzer) checkBinaryOp(op string, left, right *ResolvedType, pos token.Position) *ResolvedType {
	if left == nil || right == nil {
		return nil
	}

	switch op {
	case "+", "-", "*", "/", "%":
		return a.checkArithmeticOp(op, left, right, pos)

	case "==", "!=":
		return a.types["Boolean"]

	case ">", ">=", "<", "<=":
		if !left.IsOrderable() {
			a.addError(pos, "operator '%s' requires orderable type, got '%s' / 运算符 '%s' 需要可排序类型，得到 '%s'", op, left.Name, op, left.Name)
		}
		return a.types["Boolean"]

	case "&&", "||":
		return a.types["Boolean"]

	case "in":
		return a.types["Boolean"]

	case "is":
		return a.types["Boolean"]
	}

	return nil
}

func (a *Analyzer) checkArithmeticOp(op string, left, right *ResolvedType, pos token.Position) *ResolvedType {
	if !left.IsNumeric() || !right.IsNumeric() {
		if op == "+" && left.Kind == TypeString {
			return left // string concatenation
		}
		a.addError(pos, "operator '%s' requires numeric types, got '%s' and '%s' / 运算符 '%s' 需要数字类型，得到 '%s' 和 '%s'", op, left.Name, right.Name, op, left.Name, right.Name)
	}
	if left.Kind == TypeFloat || right.Kind == TypeFloat {
		return a.types["Float"]
	}
	return a.types["Int"]
}

// ========== Directive Validation ==========

var validModelDirectives = map[string]bool{
	"crud": true, "unique": true, "index": true,
}

var validFieldDirectives = map[string]bool{
	"id": true, "unique": true, "index": true, "varchar": true,
	"hidden": true, "hash": true, "immutable": true, "internal": true,
	"visible": true, "transform": true, "beforeSave": true, "mask": true,
	"filterable": true, "sortable": true, "search": true,
	"auto": true, "soft": true, "deprecated": true, "reserved": true,
	"count": true, "sum": true, "avg": true, "min": true, "max": true,
}

var validApiDirectives = map[string]bool{
	"auth": true, "native": true, "cache": true, "rateLimit": true,
	"scope": true, "stream": true,
}

var stringOnlyDirectives = map[string]bool{
	"email": true, "varchar": true, "hash": true, "mask": true,
	"pattern": true, "minLength": true, "maxLength": true, "notBlank": true,
}

var numericOnlyDirectives = map[string]bool{
	"range": true,
}

func (a *Analyzer) checkDirectives(directives []*ast.Directive, context string) {
	valid := validModelDirectives
	if context == "api" {
		valid = validApiDirectives
	}
	for _, d := range directives {
		if !valid[d.Name] && !validFieldDirectives[d.Name] {
			a.addWarning(d.Pos, "unknown directive '@%s' in %s context / 未知指令 '@%s'，在 %s 上下文中", d.Name, context, d.Name, context)
		}
	}
}

func (a *Analyzer) checkFieldDirectives(f *ast.FieldDecl) {
	if f.Type == nil {
		return
	}
	typeName := f.Type.Name
	for _, d := range f.Directives {
		if stringOnlyDirectives[d.Name] {
			if typeName != "String" && typeName != "" {
				a.addError(d.Pos, "@%s can only be used on String fields, got '%s' / @%s 只能用在 String 字段上，得到 '%s'", d.Name, typeName, d.Name, typeName)
			}
		}
		if numericOnlyDirectives[d.Name] {
			resolved := a.resolveTypeRef(f.Type, f.Pos)
			if resolved != nil && !resolved.IsNumeric() {
				a.addError(d.Pos, "@%s can only be used on numeric fields, got '%s' / @%s 只能用在数字字段上，得到 '%s'", d.Name, typeName, d.Name, typeName)
			}
		}
	}
}

// ========== Error Helpers ==========

func (a *Analyzer) addError(pos token.Position, format string, args ...any) {
	a.errors = append(a.errors, Error{
		Pos:     pos,
		Message: fmt.Sprintf(format, args...),
	})
}

func (a *Analyzer) addWarning(pos token.Position, format string, args ...any) {
	a.warnings = append(a.warnings, Warning{
		Pos:     pos,
		Message: fmt.Sprintf(format, args...),
	})
}

func (a *Analyzer) addErrorWithSuggestion(pos token.Position, name string, format string, args ...any) {
	err := Error{
		Pos:     pos,
		Message: fmt.Sprintf(format, args...),
	}
	// find closest match
	if suggestion := a.findClosest(name); suggestion != "" {
		err.Suggestion = fmt.Sprintf("did you mean '%s'?", suggestion)
	}
	a.errors = append(a.errors, err)
}

func (a *Analyzer) addFieldError(pos token.Position, objType *ResolvedType, fieldName string) {
	msg := fmt.Sprintf("'%s' has no field '%s' / '%s' 没有字段 '%s'", objType.Name, fieldName, objType.Name, fieldName)
	err := Error{Pos: pos, Message: msg}

	// suggest closest field name
	if objType.Fields != nil {
		closest := findClosestString(fieldName, fieldNames(objType))
		if closest != "" {
			err.Suggestion = fmt.Sprintf("did you mean '%s'?", closest)
		}
	}
	a.errors = append(a.errors, err)
}

func (a *Analyzer) findClosest(name string) string {
	var candidates []string
	for typeName := range a.types {
		candidates = append(candidates, typeName)
	}
	for _, sym := range a.scope.AllSymbols() {
		candidates = append(candidates, sym.Name)
	}
	return findClosestString(name, candidates)
}

func fieldNames(t *ResolvedType) []string {
	var names []string
	for name := range t.Fields {
		names = append(names, name)
	}
	return names
}

func findClosestString(target string, candidates []string) string {
	best := ""
	bestDist := 3 // max edit distance to suggest
	for _, c := range candidates {
		d := editDistance(target, c)
		if d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

func isCollectionMethod(name string) bool {
	switch name {
	case "map", "filter", "sumOf", "count", "any", "firstOrNull",
		"sortBy", "groupBy", "forEach", "flatMap", "joinToString",
		"take", "takeLast", "size", "isEmpty", "contains":
		return true
	}
	return false
}

func (a *Analyzer) resolveCollectionMethod(method string, listType *ResolvedType) *ResolvedType {
	intType := a.types["Int"]
	boolType := a.types["Boolean"]
	stringType := a.types["String"]

	switch method {
	case "size", "count":
		return intType
	case "isEmpty", "any", "contains":
		return boolType
	case "joinToString":
		return stringType
	case "filter", "sortBy", "take", "takeLast":
		return listType // returns same list type
	case "map", "flatMap", "groupBy":
		return nil // type depends on lambda, can't infer here
	case "forEach":
		return nil // void
	case "firstOrNull":
		// returns single element, nullable
		return &ResolvedType{
			Kind:     listType.Kind,
			Name:     listType.Name,
			Nullable: true,
			Fields:   listType.Fields,
			Parents:  listType.Parents,
		}
	case "sumOf":
		return a.types["Float"]
	default:
		// distinct, reversed, shuffled, drop, dropLast, zip, chunked, windowed, indexOf, etc.
		return nil
	}
}

func editDistance(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
		dp[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		dp[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			dp[i][j] = min3(dp[i-1][j]+1, dp[i][j-1]+1, dp[i-1][j-1]+cost)
		}
	}
	return dp[la][lb]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

func isBuiltinOp(name string) bool {
	switch name {
	case "find", "create", "update", "delete", "emit",
		"throw", "transaction", "log", "cache", "storage",
		"mail", "task", "services", "error", "request":
		return true
	}
	return false
}

func kindToSymbol(kind TypeKind) SymbolKind {
	switch kind {
	case TypeModel:
		return SymModel
	case TypeInterface:
		return SymInterface
	case TypeEnum:
		return SymEnum
	case TypeSealed:
		return SymSealed
	default:
		return SymType
	}
}
