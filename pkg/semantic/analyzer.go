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
	scope         *Scope
	types         map[string]*ResolvedType
	errors        []Error
	warnings      []Warning
	inLambda      bool // true when checking inside a lambda body
	inTransaction bool // true when checking inside a transaction block
	inAwait       bool // true when checking inside an await block
	files         []*ast.File
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
	a.files = files

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

	// Pass 5: check circular event dependencies
	a.checkEventCycles()

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
	for _, ev := range file.Events {
		a.scope.Define(&Symbol{
			Name: ev.Name,
			Kind: SymEvent,
			Pos:  ev.Pos,
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
	// detect extend field conflicts: same model + same field name
	extendFields := map[string]map[string]token.Position{} // model → field → first pos
	for _, ext := range file.Extends {
		if _, ok := a.types[ext.Name]; !ok {
			// extend target might be in another module (resolved at gateway)
		}
		if extendFields[ext.Name] == nil {
			extendFields[ext.Name] = map[string]token.Position{}
		}
		for _, f := range ext.Fields {
			if firstPos, exists := extendFields[ext.Name][f.Name]; exists {
				a.addError(f.Pos,
					"extend field '%s.%s' conflicts with previous declaration at line %d / extend 字段 '%s.%s' 与第 %d 行的声明冲突",
					ext.Name, f.Name, firstPos.Line, ext.Name, f.Name, firstPos.Line)
			} else {
				extendFields[ext.Name][f.Name] = f.Pos
			}
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
		Kind:       typ.Kind,
		Name:       typ.Name,
		Nullable:   ref.Nullable,
		IsList:     ref.IsList,
		Fields:     typ.Fields,
		Parents:    typ.Parents,
		Pos:        typ.Pos,
		Variants:   typ.Variants,
		EnumValues: typ.EnumValues,
	}

	// Generic args
	for _, arg := range ref.TypeArgs {
		result.TypeArgs = append(result.TypeArgs, a.resolveTypeRef(arg, pos))
	}

	return result
}

// ========== Pass 4: Check Bodies ==========

func (a *Analyzer) checkBodies(file *ast.File) {
	// global val/var declarations — var is forbidden at global scope
	for _, g := range file.Globals {
		if g.Mutable {
			a.addError(g.Pos, "global 'var' is not allowed, use 'val' for global constants or 'cache' for mutable state / 全局不允许 'var'，使用 'val' 定义全局常量或使用 'cache' 管理可变状态")
		}
		a.checkValStmt(g, a.scope)
	}

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
			// add my (current authenticated user) to scope
			scope.Define(&Symbol{
				Name: "my",
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

	for _, on := range file.Listeners {
		if on.Body != nil {
			scope := a.scope.Child()
			// add lambda-style params to scope
			for _, p := range on.Params {
				scope.Define(&Symbol{
					Name: p,
					Kind: SymParam,
				})
			}
			a.checkBlock(on.Body, scope)
		}
	}
}

func (a *Analyzer) checkBlock(block *ast.Block, scope *Scope) {
	if block == nil {
		return
	}
	terminated := false
	for _, stmt := range block.Stmts {
		if terminated {
			a.addWarning(stmt.GetPos(), "unreachable code / 不可达代码")
			break
		}
		a.checkStmt(stmt, scope)
		if isTerminating(stmt) {
			terminated = true
		}
	}
}

func isTerminating(stmt ast.Stmt) bool {
	switch stmt.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BreakStmt:
		return true
	case *ast.ThrowStmt:
		return true
	}
	return false
}

func (a *Analyzer) checkStmt(stmt ast.Stmt, scope *Scope) {
	switch s := stmt.(type) {
	case *ast.ValStmt:
		a.checkValStmt(s, scope)

	case *ast.IfStmt:
		a.checkExpr(s.Condition, scope)
		childScope := scope.Child()
		a.checkBlock(s.Then, childScope)

	case *ast.ForStmt:
		a.checkForStmt(s, scope)

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
		a.checkAssignStmt(s, scope)

	case *ast.BreakStmt:
		// valid in any loop context, no check needed at semantic level

	case *ast.ExprStmt:
		if s.Expr != nil {
			a.checkExpr(s.Expr, scope)
		}
	}
}

func (a *Analyzer) checkValStmt(s *ast.ValStmt, scope *Scope) {
	exprType := a.checkExpr(s.Value, scope)
	if len(s.Names) > 0 {
		a.defineDestructured(s, exprType, scope)
	} else {
		scope.Define(&Symbol{
			Name:    s.Name,
			Kind:    SymVariable,
			Type:    exprType,
			Pos:     s.Pos,
			Mutable: s.Mutable,
		})
	}
}

// defineDestructured unpacks a tuple type into individual variables.
// val (a, b, c) = await { expr1; expr2; expr3 }
// a gets type of expr1, b gets type of expr2, c gets type of expr3.
func (a *Analyzer) defineDestructured(s *ast.ValStmt, exprType *ResolvedType, scope *Scope) {
	for i, name := range s.Names {
		var varType *ResolvedType
		if exprType != nil && exprType.Kind == TypeTuple && i < len(exprType.Tuple) {
			varType = exprType.Tuple[i]
		} else if exprType != nil && exprType.Kind != TypeTuple {
			// non-tuple assigned to destructuring — all get same type
			varType = exprType
		}
		scope.Define(&Symbol{
			Name:    name,
			Kind:    SymVariable,
			Type:    varType,
			Pos:     s.Pos,
			Mutable: s.Mutable,
		})
	}
	// warn if count mismatch
	if exprType != nil && exprType.Kind == TypeTuple && len(s.Names) != len(exprType.Tuple) {
		a.addError(s.Pos,
			"destructuring count mismatch: %d variables but tuple has %d elements / 解构数量不匹配：%d 个变量但元组有 %d 个元素",
			len(s.Names), len(exprType.Tuple), len(s.Names), len(exprType.Tuple))
	}
}

func (a *Analyzer) checkAssignStmt(s *ast.AssignStmt, scope *Scope) {
	a.checkExpr(s.Target, scope)
	a.checkExpr(s.Value, scope)
	if ident, ok := s.Target.(*ast.Ident); ok {
		if sym := scope.Lookup(ident.Name); sym != nil && sym.Kind == SymVariable && !sym.Mutable {
			a.addError(s.Pos, "cannot assign to immutable variable '%s' (declared with val) / 不能给不可变变量 '%s' 赋值（使用 val 声明）", ident.Name, ident.Name)
		}
	}
}

func (a *Analyzer) checkForStmt(s *ast.ForStmt, scope *Scope) {
	childScope := scope.Child()
	if s.Collection != nil {
		collType := a.checkExpr(s.Collection, childScope)
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
	a.checkBlock(s.Body, childScope)
}

// checkAwaitExpr collects the type of each expression statement in the await
// block and returns a tuple type so that destructuring can assign each variable
// its own type: val (user, posts) = await { getUser(id); getPosts(uid) }
func (a *Analyzer) checkAwaitExpr(e *ast.AwaitExpr, scope *Scope) *ResolvedType {
	if a.inAwait {
		a.addError(e.Pos, "nested await is not allowed / 不允许嵌套 await")
		return nil
	}
	prevAwait := a.inAwait
	a.inAwait = true
	defer func() { a.inAwait = prevAwait }()
	childScope := scope.Child()
	var elements []*ResolvedType
	if e.Body != nil {
		for _, stmt := range e.Body.Stmts {
			if es, ok := stmt.(*ast.ExprStmt); ok && es.Expr != nil {
				t := a.checkExpr(es.Expr, childScope)
				elements = append(elements, t)
			} else {
				a.checkStmt(stmt, childScope)
			}
		}
	}
	// single expression — return its type directly, not a tuple
	if len(elements) == 1 {
		return elements[0]
	}
	if len(elements) > 1 {
		return &ResolvedType{
			Kind:  TypeTuple,
			Name:  "Tuple",
			Tuple: elements,
		}
	}
	return nil
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
		return a.checkAwaitExpr(e, scope)
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
	prev := a.inTransaction
	a.inTransaction = true
	a.checkBlock(e.Body, childScope)
	a.inTransaction = prev
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

	// debug chain methods — valid on all types, return self
	if isDebugMethod(e.Field) {
		return objType
	}

	// warn about .contains() on String (non-list) without @search — generates LIKE '%%...%%'
	if e.Field == "contains" && objType.Kind == TypeString && !objType.IsList {
		if !a.hasSearchDirective(e.Object) {
			a.addWarning(e.Pos, "'contains' generates LIKE '%%%%...%%%%' which causes full table scan, consider @search for full-text index / 'contains' 会生成 LIKE 模糊查询导致全表扫描，建议使用 @search 全文索引")
		}
	}

	// collection methods on list types
	if objType.IsList && isCollectionMethod(e.Field) {
		return a.resolveCollectionMethod(e.Field, objType)
	}

	// .let scope function — works on any type
	if e.Field == "let" {
		return nil // lambda return type, can't infer
	}

	return a.resolveFieldAccess(e, objType)
}

// resolveFieldAccess resolves a field or enum value access on a type.
func (a *Analyzer) resolveFieldAccess(e *ast.MemberExpr, objType *ResolvedType) *ResolvedType {
	field := objType.LookupField(e.Field)
	if field == nil {
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

	// check CRUD inside lambda
	if a.inLambda {
		if ident, ok := e.Func.(*ast.Ident); ok && isCRUDOp(ident.Name) {
			a.addError(e.Pos, "database query inside collection lambda is forbidden, use batch query instead / 集合 lambda 内禁止数据库查询，请使用批量查询")
		}
	}

	// transaction { ... } — parsed as CallExpr(Ident("transaction"), [LambdaExpr])
	// Set inTransaction for the lambda body; also suppress inLambda since
	// transaction blocks are NOT collection lambdas.
	if ident, ok := e.Func.(*ast.Ident); ok && ident.Name == "transaction" {
		return a.checkTransactionCall(e, scope)
	}

	// my.load(field, ...) — field names are not variables, skip checking args
	if a.isMyMethodCall(e) {
		return nil
	}

	callScope := a.injectCRUDScope(e, scope)

	for _, arg := range e.Args {
		a.checkExpr(arg.Value, callScope)
	}

	return a.inferCallReturnType(e)
}

// checkTransactionCall handles transaction { ... } which is parsed as
// CallExpr(Ident("transaction"), [LambdaExpr]). It sets inTransaction
// and suppresses inLambda for the body.
func (a *Analyzer) checkTransactionCall(e *ast.CallExpr, scope *Scope) *ResolvedType {
	prevTx := a.inTransaction
	prevLambda := a.inLambda
	a.inTransaction = true
	a.inLambda = false
	for _, arg := range e.Args {
		// If the arg is a LambdaExpr, check its body directly without
		// entering checkLambdaExpr (which would set inLambda=true).
		if lambda, ok := arg.Value.(*ast.LambdaExpr); ok {
			childScope := scope.Child()
			childScope.Define(&Symbol{
				Name: "it",
				Kind: SymVariable,
			})
			a.checkBlock(lambda.Body, childScope)
		} else {
			a.checkExpr(arg.Value, scope)
		}
	}
	a.inTransaction = prevTx
	a.inLambda = prevLambda
	return nil
}

// isMyMethodCall returns true if the call is my.load(...) or similar my.xxx() builtin.
func (a *Analyzer) isMyMethodCall(e *ast.CallExpr) bool {
	member, ok := e.Func.(*ast.MemberExpr)
	if !ok {
		return false
	}
	ident, ok := member.Object.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "my"
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
	// inject 'it' as a reference to the model type for disambiguation
	callScope.Define(&Symbol{
		Name: "it",
		Kind: SymVariable,
		Type: modelType,
	})
	// warn about ambiguous identifiers: field name shadows an outer scope symbol
	a.warnAmbiguousCRUDFields(e, callScope, scope)
	return callScope
}

// warnAmbiguousCRUDFields warns when a CRUD-injected field name shadows a symbol from the outer scope.
// Only checks "where:" args — other named args (id:, title:, etc.) have clear semantics.
func (a *Analyzer) warnAmbiguousCRUDFields(e *ast.CallExpr, callScope *Scope, outerScope *Scope) {
	for _, arg := range e.Args {
		if arg.Name == "where" {
			a.collectAmbiguousIdents(arg.Value, callScope, outerScope)
		}
	}
}

// collectAmbiguousIdents walks an expression tree and warns about identifiers
// that match both a CRUD-injected field and an outer scope symbol.
func (a *Analyzer) collectAmbiguousIdents(expr ast.Expr, callScope *Scope, outerScope *Scope) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.Ident:
		// bare ident is not ambiguous on its own — only same-name comparison (x == x) is
	case *ast.BinaryExpr:
		a.checkBinaryAmbiguity(e, callScope, outerScope)
	case *ast.UnaryExpr:
		a.collectAmbiguousIdents(e.Value, callScope, outerScope)
	case *ast.CallExpr:
		for _, arg := range e.Args {
			a.collectAmbiguousIdents(arg.Value, callScope, outerScope)
		}
	case *ast.MemberExpr:
		a.collectAmbiguousIdents(e.Object, callScope, outerScope)
	}
}

func (a *Analyzer) checkBinaryAmbiguity(e *ast.BinaryExpr, callScope *Scope, outerScope *Scope) {
	if itMemberField(e.Left) != "" || itMemberField(e.Right) != "" {
		return
	}
	leftIdent, leftOk := e.Left.(*ast.Ident)
	rightIdent, rightOk := e.Right.(*ast.Ident)
	if leftOk && rightOk && leftIdent.Name == rightIdent.Name {
		fieldSym := callScope.LookupLocal(leftIdent.Name)
		outerSym := outerScope.Lookup(leftIdent.Name)
		if fieldSym != nil && fieldSym.Kind == SymField && outerSym != nil {
			a.addWarning(leftIdent.Pos,
				"ambiguous: '%s' == '%s' — both refer to same name, use 'it.%s' for field / 歧义：'%s' == '%s'，请用 'it.%s' 指定字段",
				leftIdent.Name, rightIdent.Name, leftIdent.Name, leftIdent.Name, rightIdent.Name, leftIdent.Name)
		}
		return
	}
	a.collectAmbiguousIdents(e.Left, callScope, outerScope)
	a.collectAmbiguousIdents(e.Right, callScope, outerScope)
}

// itMemberField returns the field name if expr is `it.fieldName`, empty string otherwise.
func itMemberField(expr ast.Expr) string {
	m, ok := expr.(*ast.MemberExpr)
	if !ok {
		return ""
	}
	ident, ok := m.Object.(*ast.Ident)
	if !ok || ident.Name != "it" {
		return ""
	}
	return m.Field
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
	var subjectType *ResolvedType
	if e.Subject != nil {
		subjectType = a.checkExpr(e.Subject, scope)
	}
	// check duplicate branches
	seenBranches := map[string]bool{}
	for _, b := range e.Branches {
		if b.IsType != "" {
			if seenBranches[b.IsType] {
				a.addError(e.Pos, "duplicate when branch 'is %s' / 重复的 when 分支 'is %s'", b.IsType, b.IsType)
			}
			seenBranches[b.IsType] = true
		}
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
	// sealed exhaustiveness check
	a.checkWhenExhaustive(e, subjectType)
	return nil
}

// checkWhenExhaustive verifies when exhaustiveness for sealed/enum types and else requirement.
func (a *Analyzer) checkWhenExhaustive(e *ast.WhenExpr, subjectType *ResolvedType) {
	// when without subject (no sealed/enum) — must have else
	if subjectType == nil {
		if e.Else == nil {
			a.addError(e.Pos, "when expression must have 'else' branch / when 表达式必须有 'else' 分支")
		}
		return
	}
	// enum exhaustiveness
	if subjectType.Kind == TypeEnum {
		a.checkEnumExhaustive(e, subjectType)
		return
	}
	if subjectType.Kind != TypeSealed {
		if e.Else == nil {
			a.addError(e.Pos, "when expression must have 'else' branch / when 表达式必须有 'else' 分支")
		}
		return
	}
	// if there's an else branch, exhaustiveness is guaranteed
	if e.Else != nil {
		return
	}
	// collect matched variant names
	matched := map[string]bool{}
	for _, b := range e.Branches {
		if b.IsType != "" {
			matched[b.IsType] = true
		}
	}
	// check for missing variants
	for _, v := range subjectType.Variants {
		if !matched[v.Name] {
			a.addError(e.Pos,
				"when on sealed type '%s' is not exhaustive, missing variant '%s' / when 未穷举密封类型 '%s'，缺少变体 '%s'",
				subjectType.Name, v.Name, subjectType.Name, v.Name)
		}
	}
}

// checkEnumExhaustive verifies that when on an enum type covers all values.
func (a *Analyzer) checkEnumExhaustive(e *ast.WhenExpr, subjectType *ResolvedType) {
	if e.Else != nil {
		return
	}
	// collect matched enum values from branch conditions (e.g. Role.ADMIN -> ...)
	matched := map[string]bool{}
	for _, b := range e.Branches {
		a.collectEnumValues(b.Condition, matched)
	}
	for _, v := range subjectType.EnumValues {
		if !matched[v] {
			a.addError(e.Pos,
				"when on enum '%s' is not exhaustive, missing value '%s' / when 未穷举枚举 '%s'，缺少值 '%s'",
				subjectType.Name, v, subjectType.Name, v)
		}
	}
}

func (a *Analyzer) collectEnumValues(expr ast.Expr, matched map[string]bool) {
	if expr == nil {
		return
	}
	if member, ok := expr.(*ast.MemberExpr); ok {
		matched[member.Field] = true
	}
}

func (a *Analyzer) checkLambdaExpr(e *ast.LambdaExpr, scope *Scope) *ResolvedType {
	childScope := scope.Child()
	// 'it' is the implicit parameter
	childScope.Define(&Symbol{
		Name: "it",
		Kind: SymVariable,
	})
	prev := a.inLambda
	a.inLambda = true
	a.checkBlock(e.Body, childScope)
	a.inLambda = prev
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

// ========== Event Cycle Detection ==========

// checkEventCycles detects circular event dependencies among on-listeners.
// For each on EventA { ... emit EventB(...) ... }, edge A → B is added.
// DFS detects cycles and reports them as errors.
func (a *Analyzer) checkEventCycles() {
	graph := a.buildEventGraph()
	if len(graph) == 0 {
		return
	}
	a.detectCycles(graph)
}

// buildEventGraph walks all OnDecl bodies looking for EmitStmt nodes
// and returns an adjacency map: event → list of emitted events.
func (a *Analyzer) buildEventGraph() map[string][]string {
	graph := map[string][]string{}
	for _, file := range a.files {
		for _, on := range file.Listeners {
			if on.Body == nil {
				continue
			}
			emitted := collectEmits(on.Body)
			if len(emitted) > 0 {
				graph[on.EventName] = append(graph[on.EventName], emitted...)
			}
		}
	}
	return graph
}

// collectEmits recursively collects all event names from EmitStmt in a block.
func collectEmits(block *ast.Block) []string {
	var result []string
	for _, stmt := range block.Stmts {
		if emit, ok := stmt.(*ast.EmitStmt); ok {
			result = append(result, emit.EventName)
		}
	}
	return result
}

// detectCycles runs DFS on the event graph to find and report cycles.
func (a *Analyzer) detectCycles(graph map[string][]string) {
	const (
		white = 0 // unvisited
		gray  = 1 // in progress
		black = 2 // done
	)
	color := map[string]int{}
	parent := map[string]string{}

	var dfs func(node string)
	dfs = func(node string) {
		color[node] = gray
		for _, next := range graph[node] {
			if color[next] == gray {
				// cycle found — reconstruct path
				path := a.buildCyclePath(next, node, parent)
				a.addError(token.Position{},
					"circular event dependency detected: %s / 检测到循环事件依赖: %s",
					path, path)
				return
			}
			if color[next] == white {
				parent[next] = node
				dfs(next)
			}
		}
		color[node] = black
	}

	for node := range graph {
		if color[node] == white {
			dfs(node)
		}
	}
}

// buildCyclePath reconstructs the cycle path string from parent map.
func (a *Analyzer) buildCyclePath(cycleStart, cycleEnd string, parent map[string]string) string {
	var path []string
	cur := cycleEnd
	for cur != cycleStart {
		path = append([]string{cur}, path...)
		cur = parent[cur]
	}
	path = append([]string{cycleStart}, path...)
	path = append(path, cycleStart)
	return strings.Join(path, " → ")
}

// ========== Directive Validation ==========

var validModelDirectives = map[string]bool{
	"crud": true, "unique": true, "index": true,
	"soft": true, "noTime": true,
}

var validFieldDirectives = map[string]bool{
	"id": true, "unique": true, "index": true,
	"hidden": true, "hash": true, "immutable": true, "internal": true,
	"visible": true, "transform": true, "beforeSave": true, "mask": true,
	"filterable": true, "sortable": true, "search": true,
	"auto": true, "deprecated": true, "reserved": true,
	"count": true, "sum": true, "avg": true, "min": true, "max": true,
	"encrypt": true,
	// database type annotations
	"length": true, "serial": true, "bigint": true, "smallint": true,
	"decimal": true, "uuid": true, "inet": true, "point": true,
	"brin": true, "date": true, "time": true, "vector": true,
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

// hasSearchDirective checks if the expression's underlying field has @search directive.
func (a *Analyzer) hasSearchDirective(expr ast.Expr) bool {
	member, ok := expr.(*ast.MemberExpr)
	if !ok {
		return false
	}
	objType := a.checkExpr(member.Object, a.scope)
	if objType == nil {
		return false
	}
	field := objType.LookupField(member.Field)
	if field == nil {
		return false
	}
	for _, d := range field.Directives {
		if d == "search" {
			return true
		}
	}
	return false
}

func isDebugMethod(name string) bool {
	switch name {
	case "d", "i", "w", "e":
		return true
	}
	return false
}

func isCollectionMethod(name string) bool {
	switch name {
	case "map", "filter", "sumOf", "count", "any", "firstOrNull",
		"sortBy", "groupBy", "forEach", "flatMap", "joinToString",
		"take", "takeLast", "size", "isEmpty", "contains",
		"first", "last", "lastOrNull", "reversed", "shuffled",
		"distinct", "distinctBy", "drop", "dropLast",
		"indexOf", "zip", "chunked", "windowed", "all", "none",
		"minOf", "maxOf", "sortByDesc", "isNotEmpty", "let":
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
	case "find", "create", "update", "delete",
		"throw", "transaction", "cache", "storage",
		"mail", "task", "services", "error", "request",
		"Channel", "Result", "my",
		"http", "json", "time", "math", "crypto",
		"regex", "base64", "url", "uuid":
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
