package codegen

import (
	"slices"

	"github.com/light-speak/luxo/pkg/ast"
)

// Capture discovery follows lexical scopes, including closures nested in a task.
type asyncCaptures struct {
	outer  map[string]valType
	shadow map[string]int
	found  map[string]bool
}

func capturedAsyncNames(body *ast.Block, outer map[string]valType) []string {
	if body == nil || len(body.Stmts) == 0 || len(outer) == 0 {
		return nil
	}
	walker := asyncCaptures{outer: outer, shadow: make(map[string]int), found: make(map[string]bool)}
	walker.block(body)
	names := make([]string, 0, len(walker.found))
	for name := range walker.found {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (w *asyncCaptures) block(block *ast.Block, bindings ...string) {
	if block == nil {
		return
	}
	locals := append([]string(nil), bindings...)
	for _, name := range bindings {
		w.shadow[name]++
	}
	for _, statement := range block.Stmts {
		w.statement(statement)
		if binding, ok := statement.(*ast.ValStmt); ok {
			names := binding.Names
			if len(names) == 0 {
				names = []string{binding.Name}
			}
			for _, name := range names {
				w.shadow[name]++
				locals = append(locals, name)
			}
		}
	}
	for _, name := range locals {
		w.shadow[name]--
	}
}

func (w *asyncCaptures) statement(statement ast.Stmt) {
	switch value := statement.(type) {
	case *ast.ValStmt:
		w.expression(value.Value)
	case *ast.AssignStmt:
		w.expression(value.Target)
		w.expression(value.Value)
	case *ast.ExprStmt:
		w.expression(value.Expr)
	case *ast.ReturnStmt:
		w.expression(value.Value)
	case *ast.ThrowStmt:
		w.expression(value.Error)
	case *ast.EmitStmt:
		w.arguments(value.Args)
	case *ast.IfStmt:
		w.expression(value.Condition)
		w.block(value.Then)
	case *ast.ForStmt:
		w.loop(value)
	}
}

func (w *asyncCaptures) arguments(arguments []*ast.NamedArg) {
	for _, argument := range arguments {
		w.expression(argument.Value)
	}
}

func (w *asyncCaptures) loop(loop *ast.ForStmt) {
	w.expression(loop.Collection)
	w.block(loop.Body, loop.VarName)
}

func (w *asyncCaptures) scoped(expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.LambdaExpr:
		w.block(value.Body, value.Params...)
	case *ast.ForStmt:
		w.loop(value)
	case *ast.AsyncExpr:
		w.block(value.Body)
	case *ast.AwaitExpr:
		w.block(value.Body)
	case *ast.TransactionExpr:
		w.block(value.Body)
	default:
		return false
	}
	return true
}

func (w *asyncCaptures) expression(expression ast.Expr) {
	if w.scoped(expression) {
		return
	}
	switch value := expression.(type) {
	case *ast.Ident:
		if _, exists := w.outer[value.Name]; exists && w.shadow[value.Name] == 0 {
			w.found[value.Name] = true
		}
	case *ast.MemberExpr:
		w.expression(value.Object)
	case *ast.CallExpr:
		w.expression(value.Func)
		w.callArguments(value.Args)
	case *ast.BinaryExpr:
		w.expression(value.Left)
		w.expression(value.Right)
	case *ast.UnaryExpr:
		w.expression(value.Value)
	case *ast.ElvisExpr:
		w.expression(value.Left)
		w.expression(value.Right)
	case *ast.BangElvisExpr:
		w.expression(value.Left)
		w.expression(value.Right)
	case *ast.WhenExpr:
		w.expression(value.Subject)
		for _, branch := range value.Branches {
			w.expression(branch.Condition)
			w.expression(branch.Body)
		}
		w.expression(value.Else)
	case *ast.ListExpr:
		for _, item := range value.Items {
			w.expression(item)
		}
	case *ast.ObjectExpr:
		w.arguments(value.Fields)
	case *ast.TemplateString:
		for _, part := range value.Parts {
			w.expression(part)
		}
	case *ast.RangeExpr:
		w.expression(value.Start)
		w.expression(value.End)
	case *ast.YieldExpr:
		w.expression(value.Value)
	}
}

// Only a callable lambda introduces implicit it; a when branch block does not.
func (w *asyncCaptures) callArguments(arguments []*ast.NamedArg) {
	for _, argument := range arguments {
		if lambda, ok := argument.Value.(*ast.LambdaExpr); ok && len(lambda.Params) == 0 {
			w.block(lambda.Body, "it")
		} else {
			w.expression(argument.Value)
		}
	}
}
