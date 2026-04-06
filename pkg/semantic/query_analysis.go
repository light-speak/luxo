package semantic

import (
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// analyzeQueries performs compile-time query optimization analysis.
// It checks CRUD calls for missing indexes, full table scans, and N+1 patterns.
func (a *Analyzer) analyzeQueries(file *ast.File) {
	for _, api := range file.APIs {
		if api.Body != nil {
			a.analyzeBlock(api.Body)
		}
	}
	for _, fn := range file.Functions {
		if fn.Body != nil {
			a.analyzeBlock(fn.Body)
		}
	}
	for _, on := range file.Listeners {
		if on.Body != nil {
			a.analyzeBlock(on.Body)
		}
	}
}

// analyzeBlock walks a block looking for CRUD calls and for-loop N+1 patterns.
func (a *Analyzer) analyzeBlock(block *ast.Block) {
	if block == nil {
		return
	}
	for _, stmt := range block.Stmts {
		a.analyzeStmt(stmt)
	}
}

// analyzeStmt dispatches statement analysis.
func (a *Analyzer) analyzeStmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		a.analyzeExpr(s.Expr)
	case *ast.ValStmt:
		a.analyzeExpr(s.Value)
	case *ast.AssignStmt:
		a.analyzeExpr(s.Value)
	case *ast.ForStmt:
		a.analyzeForN1(s)
		a.analyzeBlock(s.Body)
	case *ast.IfStmt:
		a.analyzeBlock(s.Then)
	case *ast.ReturnStmt:
		a.analyzeExpr(s.Value)
	}
}

// analyzeExpr checks an expression for query optimization opportunities.
func (a *Analyzer) analyzeExpr(expr ast.Expr) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.CallExpr:
		a.analyzeCallExpr(e)
		for _, arg := range e.Args {
			a.analyzeExpr(arg.Value)
		}
	case *ast.BinaryExpr:
		a.analyzeExpr(e.Left)
		a.analyzeExpr(e.Right)
	case *ast.UnaryExpr:
		a.analyzeExpr(e.Value)
	case *ast.MemberExpr:
		a.analyzeExpr(e.Object)
	case *ast.LambdaExpr:
		a.analyzeBlock(e.Body)
	case *ast.TransactionExpr:
		a.analyzeBlock(e.Body)
	}
}

// analyzeCallExpr checks a CRUD call for index coverage.
// Supports both function-style find(Model, ...) and chain-style Model.find(...).
func (a *Analyzer) analyzeCallExpr(e *ast.CallExpr) {
	var opName string
	var modelType *ResolvedType

	// function-style: find(Model, ...)
	if ident, ok := e.Func.(*ast.Ident); ok && isQueryOp(ident.Name) && len(e.Args) > 0 {
		opName = ident.Name
		if modelIdent, ok := e.Args[0].Value.(*ast.Ident); ok {
			modelType = a.types[modelIdent.Name]
		}
	}

	// chain-style: Model.find(...)
	if method, modelName := chainCRUDInfo(e); method != "" && isQueryOp(method) {
		opName = method
		modelType = a.types[modelName]
	}

	if opName == "" || modelType == nil {
		return
	}

	// Check where conditions for index coverage
	for _, arg := range e.Args {
		if arg.Name == "where" {
			a.checkWhereIndex(e, modelType, arg.Value)
		}
		if arg.Name == "order" {
			a.checkOrderIndex(e, modelType, arg.Value)
		}
	}
}

// isQueryOp returns true for operations that perform database queries.
func isQueryOp(name string) bool {
	switch name {
	case "find", "findFirst", "findMany",
		"update", "updateMany",
		"delete", "deleteMany",
		"count", "exists":
		return true
	}
	return false
}

// checkWhereIndex checks if where conditions use indexed fields.
func (a *Analyzer) checkWhereIndex(e *ast.CallExpr, modelType *ResolvedType, whereExpr ast.Expr) {
	fields := extractWhereFields(whereExpr)
	for _, fieldName := range fields {
		if !a.fieldHasIndex(modelType, fieldName) {
			a.addWarning(e.Pos,
				"query on '%s.%s' without index may cause full table scan, consider @index / 查询 '%s.%s' 无索引可能导致全表扫描，建议添加 @index",
				modelType.Name, fieldName, modelType.Name, fieldName)
		}
	}
}

// checkOrderIndex checks if order fields have indexes.
func (a *Analyzer) checkOrderIndex(e *ast.CallExpr, modelType *ResolvedType, orderExpr ast.Expr) {
	field := extractOrderField(orderExpr)
	if field != "" && !a.fieldHasIndex(modelType, field) {
		a.addWarning(e.Pos,
			"sorting by '%s.%s' without index, consider @index or @brin / 按 '%s.%s' 排序无索引，建议添加 @index 或 @brin",
			modelType.Name, field, modelType.Name, field)
	}
}

// fieldHasIndex returns true if the field has @index, @unique, @id, or @brin.
func (a *Analyzer) fieldHasIndex(modelType *ResolvedType, fieldName string) bool {
	field := modelType.LookupField(fieldName)
	if field == nil {
		return false
	}
	for _, d := range field.Directives {
		switch d {
		case "index", "unique", "id", "brin", "serial":
			return true
		}
	}
	return false
}

// extractWhereFields extracts field names used in where conditions.
// For binary expressions like `status == x && userId == y`, returns ["status", "userId"].
func extractWhereFields(expr ast.Expr) []string {
	var fields []string
	collectWhereFields(expr, &fields)
	return fields
}

// collectWhereFields recursively collects field identifiers from where expressions.
func collectWhereFields(expr ast.Expr, fields *[]string) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.BinaryExpr:
		// For && / ||, recurse both sides
		if e.Op == "&&" || e.Op == "||" {
			collectWhereFields(e.Left, fields)
			collectWhereFields(e.Right, fields)
			return
		}
		// For comparison ops (==, !=, >, <, >=, <=, in), extract the field side
		if isComparisonOp(e.Op) {
			if name := exprFieldName(e.Left); name != "" {
				*fields = append(*fields, name)
			}
			if name := exprFieldName(e.Right); name != "" {
				*fields = append(*fields, name)
			}
		}
	case *ast.UnaryExpr:
		collectWhereFields(e.Value, fields)
	}
}

// isComparisonOp returns true for comparison operators.
func isComparisonOp(op string) bool {
	switch op {
	case "==", "!=", ">", "<", ">=", "<=", "in":
		return true
	}
	return false
}

// exprFieldName returns the field name if the expression is a bare identifier
// or it.fieldName pattern. Returns empty for non-field expressions.
func exprFieldName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		// bare field name in CRUD scope
		if !isBuiltinIdent(e.Name) {
			return e.Name
		}
	case *ast.MemberExpr:
		// it.fieldName
		if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == "it" {
			return e.Field
		}
	}
	return ""
}

// isBuiltinIdent returns true for identifiers that are NOT field names.
func isBuiltinIdent(name string) bool {
	switch name {
	case "true", "false", "null", "my", "it", "request":
		return true
	}
	return false
}

// extractOrderField extracts the field name from an order expression.
// Handles: field, field.asc, field.desc
func extractOrderField(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.MemberExpr:
		// field.desc or field.asc
		if ident, ok := e.Object.(*ast.Ident); ok {
			if e.Field == "asc" || e.Field == "desc" {
				return ident.Name
			}
		}
	}
	return ""
}

// analyzeForN1 detects N+1 query patterns: CRUD inside a for loop.
func (a *Analyzer) analyzeForN1(s *ast.ForStmt) {
	if s.Body == nil {
		return
	}
	for _, stmt := range s.Body.Stmts {
		a.detectN1InStmt(stmt)
	}
}

// detectN1InStmt checks if a statement contains a CRUD call (N+1 pattern).
func (a *Analyzer) detectN1InStmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		a.detectN1InExpr(s.Expr, s.Pos)
	case *ast.ValStmt:
		a.detectN1InExpr(s.Value, s.Pos)
	}
}

// detectN1InExpr checks if an expression is a query CRUD call.
// Supports both function-style find(Model, ...) and chain-style Model.find(...).
func (a *Analyzer) detectN1InExpr(expr ast.Expr, stmtPos token.Position) {
	if expr == nil {
		return
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return
	}

	var opName string

	// function-style
	if ident, ok := call.Func.(*ast.Ident); ok && isQueryOp(ident.Name) {
		opName = ident.Name
	}

	// chain-style
	if method, _ := chainCRUDInfo(call); method != "" && isQueryOp(method) {
		opName = method
	}

	if opName != "" {
		a.addWarning(stmtPos,
			"N+1 query: '%s' inside for loop, consider batch query ('%sMany') / N+1 查询：for 循环内使用 '%s'，建议使用批量查询（'%sMany'）",
			opName, batchAlternative(opName), opName, batchAlternative(opName))
	}
}

// batchAlternative returns the batch version prefix for a CRUD op.
func batchAlternative(op string) string {
	switch op {
	case "find", "findFirst":
		return "find"
	case "delete":
		return "delete"
	case "update":
		return "update"
	default:
		return op
	}
}
